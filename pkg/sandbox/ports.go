package sandbox

import (
	corev1 "k8s.io/api/core/v1"
	virtv1 "kubevirt.io/api/core/v1"
)

// WorkloadPorts is the user container ports plus the agent port for masquerade.
func WorkloadPorts(pod *corev1.Pod) []virtv1.Port {
	seen := map[int32]bool{DefaultAgentPort: true}
	out := []virtv1.Port{{Name: "agent", Port: int32(DefaultAgentPort), Protocol: "TCP"}}
	if pod == nil {
		return out
	}
	for _, c := range pod.Spec.Containers {
		for _, p := range c.Ports {
			if p.ContainerPort <= 0 || seen[p.ContainerPort] {
				continue
			}
			seen[p.ContainerPort] = true
			proto := string(p.Protocol)
			if proto == "" {
				proto = "TCP"
			}
			name := p.Name
			if name == "" {
				name = proto
			}
			out = append(out, virtv1.Port{Name: name, Port: p.ContainerPort, Protocol: proto})
		}
	}
	return out
}
