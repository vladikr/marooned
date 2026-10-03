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
	if len(ds.Spec.Template.Spec.InitContainers) != 3 {
		t.Fatalf("init containers %d", len(ds.Spec.Template.Spec.InitContainers))
	}
	want := []string{"extract-virt-launcher", "extract-guest-kernel", "extract-guest-disk"}
	for i, name := range want {
		if ds.Spec.Template.Spec.InitContainers[i].Name != name {
			t.Fatalf("init[%d] %s", i, ds.Spec.Template.Spec.InitContainers[i].Name)
		}
	}
	if ds.Spec.Template.Spec.InitContainers[0].Image != util.DefaultVirtLauncherImage {
		t.Fatalf("init image %s", ds.Spec.Template.Spec.InitContainers[0].Image)
	}
	foundRootfs, foundGuest := false, false
	for _, v := range ds.Spec.Template.Spec.Volumes {
		if v.Name == "virt-launcher-rootfs" && v.HostPath != nil && v.HostPath.Path == util.VirtLauncherRootfsPath {
			foundRootfs = true
		}
		if v.Name == "guest-files" && v.HostPath != nil && v.HostPath.Path == util.GuestFilesPath {
			foundGuest = true
		}
	}
	if !foundRootfs {
		t.Fatal("missing virt-launcher-rootfs hostPath")
	}
	if !foundGuest {
		t.Fatal("missing guest-files hostPath")
	}
}
