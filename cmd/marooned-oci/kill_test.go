package main

import (
	"strconv"
	"strings"
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

func TestParseExecArgsCRIO(t *testing.T) {
	id, cmd := parseExecArgs([]string{"--tty", "62e35ec016f018713016c16c292c296f98cbbff696c2dc8cec2920d9719cf9d7", "/bin/sh"})
	if id != "62e35ec016f018713016c16c292c296f98cbbff696c2dc8cec2920d9719cf9d7" {
		t.Fatalf("id %s", id)
	}
	if len(cmd) != 1 || cmd[0] != "/bin/sh" {
		t.Fatalf("cmd %v", cmd)
	}
}

func TestParseExecArgsDashDash(t *testing.T) {
	id, cmd := parseExecArgs([]string{"abc123", "--", "cat", "/etc/os-release"})
	if id != "abc123" || len(cmd) != 2 || cmd[0] != "cat" {
		t.Fatalf("id=%s cmd=%v", id, cmd)
	}
}

func TestParsePauseDaemonPID(t *testing.T) {
	pid, err := strconv.Atoi(strings.TrimSpace("12345\n"))
	if err != nil || pid != 12345 {
		t.Fatalf("pid %d err %v", pid, err)
	}
}
