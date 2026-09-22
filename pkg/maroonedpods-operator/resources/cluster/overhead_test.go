package cluster

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"maroonedpods.io/maroonedpods/pkg/sandbox"
)

func TestRuntimeClassOverheadIsLauncherTax(t *testing.T) {
	rc := createRuntimeClass()
	if rc.Overhead == nil || rc.Overhead.PodFixed == nil {
		t.Fatal("missing overhead")
	}
	cpu := rc.Overhead.PodFixed[corev1.ResourceCPU]
	mem := rc.Overhead.PodFixed[corev1.ResourceMemory]
	if cpu.Cmp(resource.MustParse(sandbox.RuntimeClassOverheadCPU)) != 0 {
		t.Fatalf("cpu overhead %s", cpu.String())
	}
	if mem.Cmp(resource.MustParse(sandbox.RuntimeClassOverheadMemory)) != 0 {
		t.Fatalf("memory overhead %s", mem.String())
	}
	if mem.Cmp(resource.MustParse("32Mi")) <= 0 {
		t.Fatal("overhead still dummy 32Mi")
	}
}
