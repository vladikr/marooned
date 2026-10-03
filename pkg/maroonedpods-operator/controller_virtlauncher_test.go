package maroonedpods_operator

import (
	"testing"

	"maroonedpods.io/maroonedpods/pkg/util"
)

func TestResolveVirtLauncherImage(t *testing.T) {
	pinned := "quay.io/kubevirt/virt-launcher:v1.9.0"
	if got := resolveVirtLauncherImage(pinned, nil); got != pinned {
		t.Fatalf("pinned %s", got)
	}
	if got := resolveVirtLauncherImage("", nil); got != util.DefaultVirtLauncherImage {
		t.Fatalf("empty %s", got)
	}
	if got := resolveVirtLauncherImage(util.DefaultVirtLauncherImage, nil); got != util.DefaultVirtLauncherImage {
		t.Fatalf("latest fallback %s", got)
	}
}
