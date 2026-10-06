package main

import (
	"os"
	"strings"
	"testing"
)

func TestEntrypointHostNsenter(t *testing.T) {
	b, err := os.ReadFile("entrypoint.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, "--pid=") {
		t.Fatal("host_nsenter must not join the host pid ns: setns to an ancestor pid ns is EINVAL when HostPID is false")
	}
	if !strings.Contains(s, "--root=/host-proc/1/root") {
		t.Fatal("host_nsenter needs --root so systemctl is the host binary after the mount ns switch")
	}
	if !strings.Contains(s, "systemctl restart crio") {
		t.Fatal("reload is not enough for a new CRI-O runtime handler")
	}
}
