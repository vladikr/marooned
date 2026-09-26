package maroonedpods_server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"maroonedpods.io/maroonedpods/pkg/sandbox"
)

func TestMetricsListsAnnotatedPod(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "isolated-model",
			Namespace: "default",
			Annotations: map[string]string{
				sandbox.GuestStatsAnnotation: sandbox.FormatStatsAnnotation(sandbox.StatsSnapshot{
					Container: "engine",
					RSSBytes:  12345,
					MilliCPU:  3,
					TS:        1e9,
				}),
			},
		},
	}
	h := &metricsHandler{cli: fake.NewSimpleClientset(pod)}
	req := httptest.NewRequest(http.MethodGet, "/apis/metrics.k8s.io/v1beta1/namespaces/default/pods", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status %d %s", rr.Code, rr.Body.String())
	}
	var out map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["kind"] != "PodMetricsList" {
		t.Fatalf("%v", out)
	}
}
