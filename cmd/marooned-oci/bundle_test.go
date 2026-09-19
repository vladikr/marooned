package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadProcessSpec(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "rootfs")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	cfg := []byte(`{"root":{"path":"rootfs"},"process":{"args":["sleep","3600"],"env":["PATH=/bin"],"cwd":"/"}}`)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), cfg, 0644); err != nil {
		t.Fatal(err)
	}
	spec := readProcessSpec(dir)
	if len(spec.Args) != 2 || spec.Args[0] != "sleep" {
		t.Fatalf("args %v", spec.Args)
	}
	if spec.Cwd != "/" || spec.Env[0] != "PATH=/bin" {
		t.Fatalf("cwd/env %+v", spec)
	}
	if spec.Root != root {
		t.Fatalf("root %s", spec.Root)
	}
}

func TestTarDirectoryRoundTrip(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "hello"), []byte("world"), 0644); err != nil {
		t.Fatal(err)
	}
	tarPath := filepath.Join(t.TempDir(), "rootfs.tar")
	if err := tarDirectory(src, tarPath); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(tarPath)
	if err != nil || st.Size() == 0 {
		t.Fatalf("tar %v size %v", err, st)
	}
}
