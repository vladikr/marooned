package main

import (
	"os"
	"testing"
)

func TestParseProcStat(t *testing.T) {
	// pid (comm with spaces) state ppid ... utime stime ... rss
	line := `395 (httpd -f) S 1 395 395 0 -1 4194304 100 0 0 0 12 3 0 0 20 0 1 0 123 999 42 18446744073709551615 0 0 0 0 0 0 0 0 0 0 0 0 17 0 0 0 0 0 0`
	utime, stime, rss, err := parseProcStat(line)
	if err != nil {
		t.Fatal(err)
	}
	if utime != 12 || stime != 3 {
		t.Fatalf("cpu ticks %d %d", utime, stime)
	}
	if rss != 42 {
		t.Fatalf("rss pages %d", rss)
	}
}

func TestTicksToNano(t *testing.T) {
	if ticksToNano(100) != 1e9 {
		t.Fatalf("100 ticks at 100Hz should be 1s, got %d", ticksToNano(100))
	}
}

func TestSampleProcessTreeSelf(t *testing.T) {
	s, err := sampleProcessTree(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if s.Pids < 1 {
		t.Fatal("expected at least this process")
	}
	if s.RSSBytes == 0 {
		t.Fatal("expected rss")
	}
}
