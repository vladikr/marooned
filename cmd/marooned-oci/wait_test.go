package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestApplyStoppedStateSetsFinished(t *testing.T) {
	dir := t.TempDir()
	st := state{OCIVersion: "1.0.2", ID: "c", Status: "running", PID: 1 << 30}
	got := applyStoppedState(dir, st)
	if got.Status != "stopped" || got.PID != 0 || got.Finished == "" {
		t.Fatalf("%+v", got)
	}
	again, err := readState(dir)
	if err != nil || again.Finished == "" {
		t.Fatalf("persist: %v %+v", err, again)
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

func TestDoGuestWaitRetriesUntilWaitSucceeds(t *testing.T) {
	root := t.TempDir()
	id := "ctr"
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "criid"), []byte("pod-box\n"), 0644); err != nil {
		t.Fatal(err)
	}
	exits := t.TempDir()
	oldExits := crioExitDirs
	crioExitDirs = []string{exits}
	defer func() { crioExitDirs = oldExits }()

	n := 0
	oldWait := guestWaitOnce
	oldPause := guestWaitPause
	guestWaitPause = 0
	guestWaitOnce = func(string) error {
		n++
		if n < 3 {
			return fmt.Errorf("dial")
		}
		return nil
	}
	defer func() {
		guestWaitOnce = oldWait
		guestWaitPause = oldPause
	}()

	if doGuestWait(root, []string{id}) != 0 {
		t.Fatal("wait")
	}
	if n != 3 {
		t.Fatalf("tries %d", n)
	}
	if _, err := os.Stat(filepath.Join(dir, "guest-exited")); err != nil {
		t.Fatal("finish after success")
	}
}

func TestDoGuestWaitErrorDoesNotFinish(t *testing.T) {
	root := t.TempDir()
	id := "ctr"
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "criid"), []byte("pod-box\n"), 0644); err != nil {
		t.Fatal(err)
	}

	oldWait := guestWaitOnce
	oldPause := guestWaitPause
	guestWaitPause = time.Millisecond
	tries := 0
	guestWaitOnce = func(string) error {
		tries++
		if tries >= 4 {
			return nil
		}
		if _, err := os.Stat(filepath.Join(dir, "guest-exited")); err == nil {
			t.Fatal("finished on wait error")
		}
		return fmt.Errorf("not found")
	}
	defer func() {
		guestWaitOnce = oldWait
		guestWaitPause = oldPause
	}()

	_ = doGuestWait(root, []string{id})
	if tries < 4 {
		t.Fatalf("tries %d", tries)
	}
}

func TestPauseDead(t *testing.T) {
	dir := t.TempDir()
	if pauseDead(dir) {
		t.Fatal("no state")
	}
	writeState(dir, state{OCIVersion: "1.0.2", ID: "c", Status: "running", PID: 1 << 30})
	if !pauseDead(dir) {
		t.Fatal("dead pid")
	}
}
