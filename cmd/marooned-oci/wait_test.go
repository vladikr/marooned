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

func TestFinishOCIContainerWritesExitBeforeStop(t *testing.T) {
	dir := t.TempDir()
	exits := t.TempDir()
	old := crioExitDirs
	crioExitDirs = []string{exits}
	defer func() { crioExitDirs = old }()

	finishOCIContainer(dir, "initctr", 0)
	got, err := os.ReadFile(filepath.Join(exits, "initctr"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "0\n" {
		t.Fatalf("got %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "guest-exited")); err != nil {
		t.Fatal("guest-exited stamp")
	}
	got, err = os.ReadFile(filepath.Join(dir, "exitcode"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "0\n" {
		t.Fatalf("exitcode %q", got)
	}
}

func TestPauseExitCodeFromFile(t *testing.T) {
	dir := t.TempDir()
	if pauseExitCode(dir) != 0 {
		t.Fatal("missing file is 0")
	}
	if err := os.WriteFile(filepath.Join(dir, "exitcode"), []byte("0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if pauseExitCode(dir) != 0 {
		t.Fatal("want 0")
	}
	if err := os.WriteFile(filepath.Join(dir, "exitcode"), []byte("12\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if pauseExitCode(dir) != 12 {
		t.Fatal("want 12")
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
