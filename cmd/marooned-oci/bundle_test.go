package main

import (
	"bytes"
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

func TestDirSize(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a"), []byte("12345"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "proc"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "proc", "huge"), []byte(make([]byte, 1<<20)), 0644); err != nil {
		t.Fatal(err)
	}
	if dirSize(dir) != 5 {
		t.Fatalf("size %d want 5 (proc skipped)", dirSize(dir))
	}
}

func TestDirSizeHardlinksOnce(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "busybox"), []byte("1234567890"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(dir, "busybox"), filepath.Join(dir, "ls")); err != nil {
		t.Fatal(err)
	}
	if dirSize(dir) != 10 {
		t.Fatalf("size %d want 10 (hardlinks counted once)", dirSize(dir))
	}
}

func TestTarDirectoryHardlinks(t *testing.T) {
	src := t.TempDir()
	body := bytes.Repeat([]byte("busybox-binary"), 100)
	if err := os.WriteFile(filepath.Join(src, "busybox"), body, 0755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"ls", "cat", "sh", "cp", "mv"} {
		if err := os.Link(filepath.Join(src, "busybox"), filepath.Join(src, n)); err != nil {
			t.Fatal(err)
		}
	}
	tarPath := filepath.Join(t.TempDir(), "rootfs.tar")
	if err := tarDirectory(src, tarPath); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	// One copy of the payload plus headers, not 6 full copies.
	if st.Size() > int64(len(body)*2+4096) {
		t.Fatalf("tar %d too large; hardlinks not preserved", st.Size())
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
