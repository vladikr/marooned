package main

import (
	"archive/tar"
	"os"
	"path/filepath"
	"testing"
)

func TestUnpackTar(t *testing.T) {
	src := t.TempDir()
	tarPath := filepath.Join(src, "r.tar")
	f, err := os.Create(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(f)
	hdr := &tar.Header{Name: "bin/busybox", Mode: 0755, Size: 4}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("busy")); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = f.Close()
	dest := filepath.Join(src, "root")
	if err := unpackTar(tarPath, dest); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dest, "bin/busybox"))
	if err != nil || string(b) != "busy" {
		t.Fatalf("got %q err %v", b, err)
	}
}

func TestLookPathInRootBusybox(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "sleep"), []byte(""), 0755); err != nil {
		t.Fatal(err)
	}
	got := lookPathInRoot(root, "sleep", []string{"PATH=/usr/bin:/bin"})
	if got != "/bin/sleep" {
		t.Fatalf("got %s", got)
	}
	if lookPathInRoot(root, "/bin/sleep", nil) != "/bin/sleep" {
		t.Fatal("abs")
	}
}
