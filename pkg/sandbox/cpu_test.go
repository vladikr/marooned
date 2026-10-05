package sandbox

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"maroonedpods.io/maroonedpods/pkg/util"
)

func TestWantsDedicatedCPU(t *testing.T) {
	guaranteed := func() *corev1.Pod {
		cpu := resource.MustParse("1")
		mem := resource.MustParse("512Mi")
		return &corev1.Pod{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Name: "box",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{corev1.ResourceCPU: cpu, corev1.ResourceMemory: mem},
						Limits:   corev1.ResourceList{corev1.ResourceCPU: cpu, corev1.ResourceMemory: mem},
					},
				}},
			},
		}
	}
	if !WantsDedicatedCPU(guaranteed()) {
		t.Fatal("Guaranteed integer CPU")
	}
	p := guaranteed()
	p.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse("500m")
	p.Spec.Containers[0].Resources.Limits[corev1.ResourceCPU] = resource.MustParse("500m")
	if WantsDedicatedCPU(p) {
		t.Fatal("Guaranteed fractional CPU is not exclusive")
	}
	best := &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "box"}}}}
	if WantsDedicatedCPU(best) {
		t.Fatal("BestEffort")
	}
	best.Annotations = map[string]string{util.DedicatedCPUAnnotation: "true"}
	if !WantsDedicatedCPU(best) {
		t.Fatal("annotation true")
	}
	g := guaranteed()
	g.Annotations = map[string]string{util.DedicatedCPUAnnotation: "false"}
	if WantsDedicatedCPU(g) {
		t.Fatal("annotation false")
	}
}

func TestIntegerCPUCores(t *testing.T) {
	p := &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{{
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1500m")},
		},
	}}}}
	if IntegerCPUCores(p) != 0 {
		t.Fatal("1500m is not an integer core count")
	}
	p.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse("2")
	if IntegerCPUCores(p) != 2 {
		t.Fatalf("got %d", IntegerCPUCores(p))
	}
}

func TestIsGuaranteedQoS(t *testing.T) {
	p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "x"}, Spec: corev1.PodSpec{
		Containers: []corev1.Container{{
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("512Mi"),
				},
			},
		}},
	}}
	if IsGuaranteedQoS(p) {
		t.Fatal("request without limit is Burstable")
	}
}
