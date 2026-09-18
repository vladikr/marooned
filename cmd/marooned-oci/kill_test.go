package main

import (
	"syscall"
	"testing"
)

func TestParseKillArgsCRIO(t *testing.T) {
	id, sig, all := parseKillArgs([]string{"adcbe7b2d4867849b03f07527effec290dda87a82ab9f3c6af8d31c3aa6d6d6f", "15"})
	if id != "adcbe7b2d4867849b03f07527effec290dda87a82ab9f3c6af8d31c3aa6d6d6f" {
		t.Fatalf("id %s", id)
	}
	if sig != syscall.SIGTERM {
		t.Fatalf("sig %v", sig)
	}
	if all {
		t.Fatal("all")
	}
}

func TestParseKillArgsAllSIGKILL(t *testing.T) {
	id, sig, all := parseKillArgs([]string{"-a", "ctr", "SIGKILL"})
	if id != "ctr" || sig != syscall.SIGKILL || !all {
		t.Fatalf("id=%s sig=%v all=%v", id, sig, all)
	}
}

func TestFirstPositionalNotLast(t *testing.T) {
	if got := firstPositional([]string{"--force", "ctrid"}); got != "ctrid" {
		t.Fatalf("got %s", got)
	}
}
