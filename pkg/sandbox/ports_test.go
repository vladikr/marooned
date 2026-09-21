package sandbox

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestWorkloadPortsIncludesContainerPorts(t *testing.T) {
	pod := &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{{
		Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: 8080, Protocol: corev1.ProtocolTCP}},
	}}}}
	ports := WorkloadPorts(pod)
	if len(ports) != 2 {
		t.Fatalf("got %d ports: %+v", len(ports), ports)
	}
	if ports[0].Port != DefaultAgentPort {
		t.Fatalf("agent %d", ports[0].Port)
	}
	if ports[1].Name != "http" || ports[1].Port != 8080 {
		t.Fatalf("workload %+v", ports[1])
	}
}

func TestWorkloadPortsNilPod(t *testing.T) {
	ports := WorkloadPorts(nil)
	if len(ports) != 1 || ports[0].Port != DefaultAgentPort {
		t.Fatalf("%+v", ports)
	}
}
