package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	jsonpatch "gomodules.xyz/jsonpatch/v2"
	admissionv1 "k8s.io/api/admission/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"maroonedpods.io/maroonedpods/pkg/webhook"
)

const (
	allowPodRequest = "Pod is not a marooned sandbox"
)

type Handler struct {
	request         *admissionv1.AdmissionRequest
	maroonedpodsCli kubernetes.Interface
	maroonedpodsNS  string
	vsockfwdImage   string
}

func NewHandler(Request *admissionv1.AdmissionRequest, maroonedpodsCli kubernetes.Interface, maroonedpodsNS string) *Handler {
	img := os.Getenv("MAROONED_VSOCKFWD_IMAGE")
	if img == "" {
		img = webhook.DefaultVsockfwdImage
	}
	return &Handler{
		request:         Request,
		maroonedpodsCli: maroonedpodsCli,
		maroonedpodsNS:  maroonedpodsNS,
		vsockfwdImage:   img,
	}
}

func (v Handler) Handle() (*admissionv1.AdmissionReview, error) {
	if v.request.Kind.Kind != "Pod" {
		return nil, fmt.Errorf("Marooned webhook doesn't recongnize request: %+v", v.request)
	}
	if v.request.Operation != admissionv1.Create && v.request.Operation != admissionv1.Update {
		return reviewResponse(v.request.UID, true, http.StatusAccepted, allowPodRequest), nil
	}
	pod := v1.Pod{}
	if err := json.Unmarshal(v.request.Object.Raw, &pod); err != nil {
		return nil, err
	}
	if webhook.IsSandboxPod(&pod) {
		return v.mutateSandboxPod(&pod)
	}
	if webhook.IsMaroonedVirtLauncher(&pod) {
		return v.mutateVirtLauncher(&pod)
	}
	return reviewResponse(v.request.UID, true, http.StatusAccepted, allowPodRequest), nil
}

func (v Handler) mutateVirtLauncher(pod *v1.Pod) (*admissionv1.AdmissionReview, error) {
	original, err := json.Marshal(pod)
	if err != nil {
		return nil, err
	}
	mutated := pod.DeepCopy()
	if err := webhook.MutateVirtLauncher(mutated, v.vsockfwdImage); err != nil {
		return reviewResponse(v.request.UID, false, http.StatusForbidden, err.Error()), nil
	}
	modified, err := json.Marshal(mutated)
	if err != nil {
		return nil, err
	}
	ops, err := jsonpatch.CreatePatch(original, modified)
	if err != nil {
		return nil, err
	}
	if len(ops) == 0 {
		return reviewResponse(v.request.UID, true, http.StatusAccepted, "virt-launcher already has vsockfwd"), nil
	}
	patch, err := json.Marshal(ops)
	if err != nil {
		return nil, err
	}
	return reviewResponseWithPatch(v.request.UID, true, http.StatusAccepted, "virt-launcher vsockfwd injected", patch), nil
}

func (v Handler) mutateSandboxPod(pod *v1.Pod) (*admissionv1.AdmissionReview, error) {
	original, err := json.Marshal(pod)
	if err != nil {
		return nil, err
	}
	mutated := pod.DeepCopy()
	if err := webhook.MutateSandboxPod(mutated); err != nil {
		return reviewResponse(v.request.UID, false, http.StatusForbidden, err.Error()), nil
	}
	modified, err := json.Marshal(mutated)
	if err != nil {
		return nil, err
	}
	ops, err := jsonpatch.CreatePatch(original, modified)
	if err != nil {
		return nil, err
	}
	if len(ops) == 0 {
		return reviewResponse(v.request.UID, true, http.StatusAccepted, "sandbox pod already mutated"), nil
	}
	patch, err := json.Marshal(ops)
	if err != nil {
		return nil, err
	}
	return reviewResponseWithPatch(v.request.UID, true, http.StatusAccepted, "sandbox pod mutated", patch), nil
}

func reviewResponseWithPatch(uid types.UID, allowed bool, httpCode int32,
	reason string, patch []byte) *admissionv1.AdmissionReview {
	rr := reviewResponse(uid, allowed, httpCode, reason)
	patchType := admissionv1.PatchTypeJSONPatch
	rr.Response.PatchType = &patchType
	rr.Response.Patch = patch
	return rr
}

func reviewResponse(uid types.UID, allowed bool, httpCode int32,
	reason string) *admissionv1.AdmissionReview {
	return &admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			Kind:       "AdmissionReview",
			APIVersion: "admission.k8s.io/v1",
		},
		Response: &admissionv1.AdmissionResponse{
			UID:     uid,
			Allowed: allowed,
			Result: &metav1.Status{
				Code:    httpCode,
				Message: reason,
			},
		},
	}
}
