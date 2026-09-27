package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestContainerListenPorts(t *testing.T) {
	p := &corev1.Pod{Spec: corev1.PodSpec{
		Containers: []corev1.Container{{
			Ports: []corev1.ContainerPort{{ContainerPort: 8080}, {ContainerPort: 8080}},
		}},
	}}
	got := ContainerListenPorts(p)
	if len(got) != 1 || got[0] != 8080 {
		t.Fatalf("%v", got)
	}
}

func TestFindSandboxDir(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "abc")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sandbox"), []byte("1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "poduid"), []byte("uid-1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if FindSandboxDir(root, "uid-1") != dir {
		t.Fatal(FindSandboxDir(root, "uid-1"))
	}
	if FindSandboxDir(root, "other") != "" {
		t.Fatal("expected miss")
	}
}

func TestPortForwardRoundTrip(t *testing.T) {
	s := PortForwardSpec{Dest: "10.0.0.1", Ports: []int{80, 8080}}
	got, err := ParsePortForward(FormatPortForward(s))
	if err != nil || got.Dest != s.Dest || len(got.Ports) != 2 {
		t.Fatalf("%v %+v", err, got)
	}
}
