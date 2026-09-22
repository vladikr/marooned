package main

import "testing"

func TestParsePidnsExecArgs(t *testing.T) {
	pid, root, argv, err := parsePidnsExecArgs([]string{"marooned-agent", "pidns-exec", "395", "/run/marooned/x/root", "--", "/bin/kill", "1"})
	if err != nil {
		t.Fatal(err)
	}
	if pid != 395 || root != "/run/marooned/x/root" {
		t.Fatalf("pid %d root %s", pid, root)
	}
	if len(argv) != 2 || argv[1] != "1" {
		t.Fatalf("argv %v", argv)
	}
}

func TestParseContainerInitArgs(t *testing.T) {
	root, argv, err := parseContainerInitArgs([]string{"marooned-agent", "container-init", "/run/marooned/x/root", "--", "/bin/kill", "1"})
	if err != nil {
		t.Fatal(err)
	}
	if root != "/run/marooned/x/root" {
		t.Fatalf("root %s", root)
	}
	if len(argv) != 2 || argv[0] != "/bin/kill" || argv[1] != "1" {
		t.Fatalf("argv %v", argv)
	}
}
