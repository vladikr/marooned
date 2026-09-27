package maroonedpods_server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	"maroonedpods.io/maroonedpods/pkg/sandbox"
)

type metricsHandler struct {
	cli kubernetes.Interface
}

func (h *metricsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/apis/metrics.k8s.io/v1beta1")
	path = strings.Trim(path, "/")
	w.Header().Set("Content-Type", "application/json")
	ctx := r.Context()
	switch {
	case path == "":
		_, _ = w.Write(metricsAPIResourceList())
	case path == "nodes" || strings.HasPrefix(path, "nodes/"):
		_, _ = w.Write([]byte(`{"kind":"NodeMetricsList","apiVersion":"metrics.k8s.io/v1beta1","items":[]}`))
	case path == "pods":
		h.writePodList(ctx, w, "")
	case strings.HasPrefix(path, "namespaces/"):
		h.serveNamespaced(ctx, w, strings.TrimPrefix(path, "namespaces/"))
	default:
		http.NotFound(w, r)
	}
}

func (h *metricsHandler) serveNamespaced(ctx context.Context, w http.ResponseWriter, rest string) {
	parts := strings.Split(rest, "/")
	if len(parts) < 2 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	ns := parts[0]
	if len(parts) == 2 && parts[1] == "pods" {
		h.writePodList(ctx, w, ns)
		return
	}
	if len(parts) == 3 && parts[1] == "pods" {
		h.writePod(ctx, w, ns, parts[2])
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

func (h *metricsHandler) writePodList(ctx context.Context, w http.ResponseWriter, ns string) {
	items := []json.RawMessage{}
	list, err := h.cli.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		klog.Errorf("metrics list pods: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for i := range list.Items {
		p := &list.Items[i]
		if p.Annotations == nil {
			continue
		}
		snap, err := sandbox.ParseStatsAnnotation(p.Annotations[sandbox.GuestStatsAnnotation])
		if err != nil || snap == nil || sandbox.StatsStale(*snap) {
			continue
		}
		items = append(items, sandbox.PodMetricsJSON(p.Namespace, p.Name, *snap))
	}
	out := map[string]interface{}{
		"kind":       "PodMetricsList",
		"apiVersion": "metrics.k8s.io/v1beta1",
		"items":      items,
	}
	_ = json.NewEncoder(w).Encode(out)
}

func (h *metricsHandler) writePod(ctx context.Context, w http.ResponseWriter, ns, name string) {
	p, err := h.cli.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if p.Annotations == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	snap, err := sandbox.ParseStatsAnnotation(p.Annotations[sandbox.GuestStatsAnnotation])
	if err != nil || snap == nil || sandbox.StatsStale(*snap) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	_, _ = w.Write(sandbox.PodMetricsJSON(ns, name, *snap))
}

func metricsAPIResourceList() []byte {
	return []byte(`{"kind":"APIResourceList","apiVersion":"v1","groupVersion":"metrics.k8s.io/v1beta1","resources":[{"name":"pods","singularName":"pod","namespaced":true,"kind":"PodMetrics","verbs":["get","list"]},{"name":"nodes","singularName":"node","namespaced":false,"kind":"NodeMetrics","verbs":["get","list"]}]}`)
}
