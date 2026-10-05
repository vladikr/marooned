package maroonedpods_server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"maroonedpods.io/maroonedpods/pkg/util"
)

func TestServeHTTPValidatePathDropsPatch(t *testing.T) {
	rc := util.RuntimeClassName
	now := metav1.NewTime(time.Now())
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "box",
			Namespace:         "app",
			DeletionTimestamp: &now,
			Finalizers:        []string{util.SandboxFinalizer},
		},
		Spec: corev1.PodSpec{
			RuntimeClassName: &rc,
			Containers:       []corev1.Container{{Name: "box", Image: "busybox"}},
		},
	}
	raw, err := json.Marshal(pod)
	if err != nil {
		t.Fatal(err)
	}
	review, err := json.Marshal(&admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{APIVersion: "admission.k8s.io/v1", Kind: "AdmissionReview"},
		Request: &admissionv1.AdmissionRequest{
			UID:       "del",
			Kind:      metav1.GroupVersionKind{Kind: "Pod"},
			Operation: admissionv1.Update,
			Object:    runtime.RawExtension{Raw: raw},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	h := NewMaroonedPodsServerHandler("maroonedpods", nil)

	mutateReq := httptest.NewRequest(http.MethodPost, ServePath, bytes.NewReader(review))
	mutateReq.Header.Set("Content-Type", "application/json")
	mutateRec := httptest.NewRecorder()
	h.ServeHTTP(mutateRec, mutateReq)
	mutateOut := decodeReview(t, mutateRec)
	if mutateOut.Response.Patch == nil {
		t.Fatal("/serve-path must still patch the finalizer off")
	}
	if !strings.Contains(string(mutateOut.Response.Patch), "finalizers") {
		t.Fatalf("mutate patch %s", mutateOut.Response.Patch)
	}

	validateReq := httptest.NewRequest(http.MethodPost, ValidatePath, bytes.NewReader(review))
	validateReq.Header.Set("Content-Type", "application/json")
	validateRec := httptest.NewRecorder()
	h.ServeHTTP(validateRec, validateReq)
	validateOut := decodeReview(t, validateRec)
	if !validateOut.Response.Allowed {
		t.Fatal("/validate-path must allow")
	}
	if validateOut.Response.Patch != nil || validateOut.Response.PatchType != nil {
		t.Fatalf("validating webhook may not return patch: %s", validateOut.Response.Patch)
	}
}

func decodeReview(t *testing.T, rec *httptest.ResponseRecorder) *admissionv1.AdmissionReview {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var out admissionv1.AdmissionReview
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Response == nil {
		t.Fatal("missing response")
	}
	return &out
}
