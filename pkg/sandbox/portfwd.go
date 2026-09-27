package sandbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

const ForwardFile = "forward.json"

// PortForwardSpec tells the host pause process to listen on Pod ports and
// proxy to Dest (virt-launcher / guest). status.podIP stays kubelet's CNI
// address; traffic to that IP:containerPort reaches the guest.
type PortForwardSpec struct {
	Dest  string `json:"dest"`
	Ports []int  `json:"ports"`
}

func ParsePortForward(b []byte) (PortForwardSpec, error) {
	var s PortForwardSpec
	if err := json.Unmarshal(b, &s); err != nil {
		return s, err
	}
	return s, nil
}

func FormatPortForward(s PortForwardSpec) []byte {
	b, _ := json.Marshal(s)
	return b
}

func ContainerListenPorts(pod *corev1.Pod) []int {
	if pod == nil {
		return nil
	}
	seen := map[int]struct{}{}
	var out []int
	for _, c := range pod.Spec.Containers {
		for _, p := range c.Ports {
			if p.ContainerPort <= 0 {
				continue
			}
			n := int(p.ContainerPort)
			if _, ok := seen[n]; ok {
				continue
			}
			seen[n] = struct{}{}
			out = append(out, n)
		}
	}
	return out
}

func FindSandboxDir(root, podUID string) string {
	if podUID == "" {
		return ""
	}
	ents, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		if _, err := os.Stat(filepath.Join(dir, "sandbox")); err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, "poduid"))
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(b)) == podUID {
			return dir
		}
	}
	return ""
}
