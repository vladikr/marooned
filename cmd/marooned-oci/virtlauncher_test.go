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
