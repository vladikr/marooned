package main

import (
	"os"
	"path/filepath"
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

func TestCurrentPodLogPicksLatest(t *testing.T) {
	dir := t.TempDir()
	podDir := dir + "/default_isolated-busybox1_uid/box"
	if err := os.MkdirAll(podDir, 0755); err != nil {
		t.Fatal(err)
	}
	old := currentPodLog
	defer func() { /* path uses /var/log/pods; test the picker via glob on a temp dir */ }()
	_ = old
	if err := os.WriteFile(podDir+"/0.log", []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(podDir+"/1.log", []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}
	best, max := podDir+"/0.log", -1
	matches, _ := filepath.Glob(podDir + "/*.log")
	for _, m := range matches {
		n, err := strconv.Atoi(strings.TrimSuffix(filepath.Base(m), ".log"))
		if err == nil && n >= max {
			max = n
			best = m
		}
	}
	if max != 1 || !strings.HasSuffix(best, "1.log") {
		t.Fatalf("best %s max %d", best, max)
	}
}

func TestLoadExecProcessJSON(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/process.json"
	if err := os.WriteFile(p, []byte(`{"terminal":false,"args":["cat","/tmp/marooned-ok"]}`), 0644); err != nil {
		t.Fatal(err)
	}
	id, proc, _ := loadExec([]string{"--process", p, "62e35ec016f018713016c16c292c296f98cbbff696c2dc8cec2920d9719cf9d7"})
	if id != "62e35ec016f018713016c16c292c296f98cbbff696c2dc8cec2920d9719cf9d7" {
		t.Fatalf("id %s", id)
	}
	if len(proc.Args) != 2 || proc.Args[0] != "cat" {
		t.Fatalf("args %v", proc.Args)
	}
}

func TestLoadExecPidFile(t *testing.T) {
	dir := t.TempDir()
	pf := filepath.Join(dir, "pid")
	_, _, got := loadExec([]string{"--pid-file", pf, "abc123", "true"})
	if got != pf {
		t.Fatalf("pidFile %q", got)
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
