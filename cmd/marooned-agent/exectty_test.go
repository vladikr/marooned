package main

import (
	"io"
	"os"
	"os/exec"
	"testing"
	"time"
)

// os.Pipe + close parent write end after Start: ReadAll EOFs when the child exits.
func TestExecPipesEOFAfterChildExit(t *testing.T) {
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", "-c", "printf 'it-ok\\n'")
	cmd.Stdout = stdoutW
	cmd.Stderr = stdoutW
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = stdoutW.Close()
	done := make(chan []byte, 1)
	go func() {
		b, _ := io.ReadAll(stdoutR)
		done <- b
	}()
	select {
	case b := <-done:
		if string(b) != "it-ok\n" {
			t.Fatalf("got %q", b)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stdout pipe did not EOF after child exit")
	}
	_ = cmd.Wait()
}
