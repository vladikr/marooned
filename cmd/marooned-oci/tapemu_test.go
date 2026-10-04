package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTapEmulatorScript(t *testing.T) {
	s := tapEmulatorScript()
	if !tapEmulatorWrapped(s) {
		t.Fatalf("script %s", s)
	}
	if !strings.Contains(s, "/run/marooned-tap.mac") {
		t.Fatal("mac file")
	}
	if !strings.Contains(s, "vhost=on") {
		t.Fatal("vhost")
	}
	if !strings.HasPrefix(s, "#!/bin/sh\n") {
		t.Fatal("shebang")
	}
}

func TestPlantTapEmulatorWritesMAC(t *testing.T) {
	root := t.TempDir()
	if err := plantTapEmulator(root, "0a:58:0a:f4:00:6d"); err != os.ErrNotExist {
		t.Fatalf("no qemu binary: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(root, tapMACFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(b)) != "0a:58:0a:f4:00:6d" {
		t.Fatalf("mac %q", b)
	}
}

func TestPlantTapEmulatorEmpty(t *testing.T) {
	if err := plantTapEmulator("", "aa:bb:cc:dd:ee:ff"); err != nil {
		t.Fatal(err)
	}
	if err := plantTapEmulator(t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
}

func TestQemuEmulatorHiddenPaths(t *testing.T) {
	got := qemuEmulatorHiddenPaths("/overlay")
	if len(got) != len(qemuEmulatorRels) {
		t.Fatalf("len %d", len(got))
	}
	if !strings.HasSuffix(got[0], "qemu-kvm.marooned") {
		t.Fatalf("first %s", got[0])
	}
}
