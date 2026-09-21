package main

import "testing"

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
