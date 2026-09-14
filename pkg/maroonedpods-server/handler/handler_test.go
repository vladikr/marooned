package handler

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"maroonedpods.io/maroonedpods/pkg/util"
)

func TestHandleSandboxCreate(t *testing.T) {
	rc := util.RuntimeClassName
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "box", Namespace: "app"},
		Spec: corev1.PodSpec{
			RuntimeClassName: &rc,
			Containers: []corev1.Container{{
				Name:  "box",
				Image: "busybox",
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:                   resource.MustParse("100m"),
						corev1.ResourceMemory:                resource.MustParse("128Mi"),
						corev1.ResourceName("hugepages-2Mi"): resource.MustParse("64Mi"),
					},
				},
				VolumeMounts: []corev1.VolumeMount{{Name: "data", MountPath: "/data"}},
			}},
			Volumes: []corev1.Volume{{
				Name: "data",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "pvc"},
				},
			}},
		},
	}
	raw, err := json.Marshal(pod)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(&admissionv1.AdmissionRequest{
		UID:       "1",
		Kind:      metav1.GroupVersionKind{Kind: "Pod"},
		Operation: admissionv1.Create,
		Object:    runtime.RawExtension{Raw: raw},
	}, nil, "maroonedpods")
	out, err := h.Handle()
	if err != nil {
		t.Fatal(err)
	}
	if !out.Response.Allowed {
		t.Fatal("not allowed")
	}
	if out.Response.Patch == nil {
		t.Fatal("expected patch")
	}
	if out.Response.Result.Code != http.StatusAccepted {
		t.Fatalf("code %d", out.Response.Result.Code)
	}
	if !strings.Contains(string(out.Response.Patch), util.VolumesAnnotation) {
		t.Fatal("admit patch must persist stripped volumes")
	}
	if !strings.Contains(string(out.Response.Patch), util.PlacementAnnotation) {
		t.Fatal("admit patch must set placement")
	}
}

func TestHandleMaroonLabelIsIgnored(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "legacy",
			Namespace: "app",
			Labels:    map[string]string{util.MaroonedPodLabel: "true"},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "c", Image: "nginx"}},
		},
	}
	raw, _ := json.Marshal(pod)
	h := NewHandler(&admissionv1.AdmissionRequest{
		UID:       "2",
		Kind:      metav1.GroupVersionKind{Kind: "Pod"},
		Operation: admissionv1.Create,
		Object:    runtime.RawExtension{Raw: raw},
	}, nil, "maroonedpods")
	out, err := h.Handle()
	if err != nil {
		t.Fatal(err)
	}
	if !out.Response.Allowed {
		t.Fatal("label-only pod must be allowed without mutation")
	}
	if out.Response.Patch != nil {
		t.Fatal("maroonedpods.io/maroon must not opt into sandbox or node mode here")
	}
}

func TestHandlePlainPodAllowed(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "plain", Namespace: "app"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "nginx"}}},
	}
	raw, _ := json.Marshal(pod)
	h := NewHandler(&admissionv1.AdmissionRequest{
		UID:       "3",
		Kind:      metav1.GroupVersionKind{Kind: "Pod"},
		Operation: admissionv1.Create,
		Object:    runtime.RawExtension{Raw: raw},
	}, nil, "maroonedpods")
	out, err := h.Handle()
	if err != nil {
		t.Fatal(err)
	}
	if !out.Response.Allowed {
		t.Fatal("plain pod should be allowed")
	}
	if out.Response.Patch != nil {
		t.Fatal("plain pod should not be patched")
	}
}
