package cluster

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"maroonedpods.io/maroonedpods/pkg/sandbox"
	"maroonedpods.io/maroonedpods/pkg/util"
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

func TestShimDaemonSetExtractsVirtLauncher(t *testing.T) {
	ds := createShimDaemonSet("shim:test", "IfNotPresent")
	if len(ds.Spec.Template.Spec.InitContainers) != 1 {
		t.Fatalf("init containers %d", len(ds.Spec.Template.Spec.InitContainers))
	}
	init := ds.Spec.Template.Spec.InitContainers[0]
	if init.Name != "extract-virt-launcher" {
		t.Fatalf("init name %s", init.Name)
	}
	if init.Image != util.DefaultVirtLauncherImage {
		t.Fatalf("init image %s", init.Image)
	}
	found := false
	for _, v := range ds.Spec.Template.Spec.Volumes {
		if v.Name == "virt-launcher-rootfs" && v.HostPath != nil && v.HostPath.Path == util.VirtLauncherRootfsPath {
			found = true
		}
	}
	if !found {
		t.Fatal("missing virt-launcher-rootfs hostPath")
	}
}
