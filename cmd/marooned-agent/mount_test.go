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
