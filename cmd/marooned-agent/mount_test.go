package main

import (
	"os"
	"path/filepath"
	"testing"

	"maroonedpods.io/maroonedpods/pkg/sandbox/agentproto"
)

func TestEnsureMountInCreatesGuestPath(t *testing.T) {
	base := t.TempDir()
	m := agentproto.Mount{GuestPath: "/scratch", Kind: "files"}
	if err := ensureMountIn(base, "cid", m); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(base, "cid", "root", "scratch")
	st, err := os.Stat(want)
	if err != nil {
		t.Fatal(err)
	}
	if !st.IsDir() {
		t.Fatalf("%s is not a dir", want)
	}
}

func TestSharedVolPath(t *testing.T) {
	p := sharedVolPath(agentproto.Mount{VolumeName: "scratch", GuestPath: "/scratch"})
	if p != filepath.Join(volRoot, "scratch") {
		t.Fatalf("%s", p)
	}
}

func TestPrepareBlockTargetReplacesDirectory(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dev", "xvda")
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}
	if err := prepareBlockTarget(target); err != nil {
		t.Fatal(err)
	}
	st, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	if st.IsDir() {
		t.Fatal("devicePath is still a directory")
	}
}

func TestEnsureMountInBlockDoesNotMkdirTarget(t *testing.T) {
	old := diskWait
	diskWait = 0
	defer func() { diskWait = old }()
	base := t.TempDir()
	m := agentproto.Mount{VolumeName: "data", GuestPath: "/dev/xvda", Kind: "block", Serial: "nosuch"}
	if err := ensureMountIn(base, "cid", m); err == nil {
		t.Fatal("expected missing disk")
	}
	want := filepath.Join(base, "cid", "root", "dev", "xvda")
	if st, err := os.Lstat(want); err == nil && st.IsDir() {
		t.Fatal("devicePath became a directory")
	}
}
