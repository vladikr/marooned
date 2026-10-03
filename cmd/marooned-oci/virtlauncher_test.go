package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStandaloneVMISpecFromFile(t *testing.T) {
	dir := t.TempDir()
	want := `{"kind":"VirtualMachineInstance"}`
	if err := os.WriteFile(filepath.Join(dir, "standalone-vmi.json"), []byte(want), 0644); err != nil {
		t.Fatal(err)
	}
	got := standaloneVMISpec(dir, "")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestStandaloneVMISpecMissing(t *testing.T) {
	dir := t.TempDir()
	if got := standaloneVMISpec(dir, ""); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestStartVirtLauncherPauseWithoutBinary(t *testing.T) {
	dir := t.TempDir()
	if pid := startVirtLauncherPause("id", dir); pid != 0 {
		t.Fatalf("pid %d without binary or spec", pid)
	}
}

func TestBundleNetNS(t *testing.T) {
	dir := t.TempDir()
	if got := bundleNetNS(dir); got != "" {
		t.Fatalf("empty bundle %q", got)
	}
	cfg := []byte(`{
		"linux": {
			"namespaces": [
				{"type": "pid"},
				{"type": "network", "path": "/var/run/netns/cni-abc"},
				{"type": "uts", "path": "/proc/123/ns/uts"}
			]
		}
	}`)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), cfg, 0644); err != nil {
		t.Fatal(err)
	}
	if got := bundleNetNS(dir); got != "/var/run/netns/cni-abc" {
		t.Fatalf("netns %q", got)
	}
}

func TestImageVolumeBinds(t *testing.T) {
	binds := imageVolumeBinds("/overlay", "uid-1")
	if len(binds) != 2 {
		t.Fatalf("binds %d", len(binds))
	}
	if binds[0][0] != "/var/lib/kubelet/pods/uid-1/volumes/kubernetes.io~image/rootdisk" {
		t.Fatalf("disk src %s", binds[0][0])
	}
	if binds[0][1] != "/overlay/var/run/kubevirt-image-volume/disk_0" {
		t.Fatalf("disk dst %s", binds[0][1])
	}
	if binds[1][1] != "/overlay/var/run/kubevirt-kernel-boot" {
		t.Fatalf("kernel dst %s", binds[1][1])
	}
}

func TestVirtLauncherCmdChrootAndNetNS(t *testing.T) {
	cmd := virtLauncherCmd("/opt/marooned/virt-launcher-rootfs", "/var/run/netns/cni-x", []string{"--name", "box"})
	if cmd == nil {
		t.Fatal("nil cmd")
	}
	got := strings.Join(cmd.Args, " ")
	want := "nsenter --net=/var/run/netns/cni-x -- chroot /opt/marooned/virt-launcher-rootfs /usr/bin/virt-launcher --name box"
	if got != want {
		t.Fatalf("args %q want %q", got, want)
	}
}
