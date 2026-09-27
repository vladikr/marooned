package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsMountPointTempDir(t *testing.T) {
	dir := t.TempDir()
	if isMountPoint(dir) {
		t.Fatalf("%s should not be a mount point", dir)
	}
	if isMountPoint(filepath.Join(dir, "missing")) {
		t.Fatal("missing path")
	}
}

func TestRootfsPopulated(t *testing.T) {
	dir := t.TempDir()
	if rootfsPopulated(dir) {
		t.Fatal("empty")
	}
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "sh"), []byte("x"), 0755); err != nil {
		t.Fatal(err)
	}
	if !rootfsPopulated(dir) {
		t.Fatal("want populated")
	}
}

func TestRootfsDestPrefersContainerName(t *testing.T) {
	got := rootfsDest("box", "uid-box")
	if got != filepath.Join(ctrRoot, "box", "root") {
		t.Fatalf("%s", got)
	}
}

func TestHasExtSuperblock(t *testing.T) {
	p := filepath.Join(t.TempDir(), "dev")
	buf := make([]byte, 1100)
	buf[1080] = 0x53
	buf[1081] = 0xef
	if err := os.WriteFile(p, buf, 0644); err != nil {
		t.Fatal(err)
	}
	if !hasExtSuperblock(p) {
		t.Fatal("expected ext magic")
	}
	if hasExtSuperblock(filepath.Join(t.TempDir(), "missing")) {
		t.Fatal("missing device")
	}
}
