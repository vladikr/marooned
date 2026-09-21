package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteCrioExit(t *testing.T) {
	dir := t.TempDir()
	old := crioExitDirs
	crioExitDirs = []string{dir, filepath.Join(t.TempDir(), "missing")}
	defer func() { crioExitDirs = old }()

	writeCrioExit("abc123", 137)
	got, err := os.ReadFile(filepath.Join(dir, "abc123"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "137\n" {
		t.Fatalf("got %q", got)
	}
}

func TestGuestHasExitedBeforeStart(t *testing.T) {
	dir := t.TempDir()
	if guestHasExited(dir) {
		t.Fatal("booting guest must not look exited")
	}
}

func TestGuestHasExitedStamp(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "guest-exited"), []byte("1"), 0644); err != nil {
		t.Fatal(err)
	}
	if !guestHasExited(dir) {
		t.Fatal("guest-exited stamp")
	}
}
