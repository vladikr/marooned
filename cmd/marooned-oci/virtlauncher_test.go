package main

import (
	"os"
	"path/filepath"
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