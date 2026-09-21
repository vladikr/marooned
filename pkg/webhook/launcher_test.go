package webhook

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	virtv1 "kubevirt.io/api/core/v1"

	"maroonedpods.io/maroonedpods/pkg/util"
)

func TestIsMaroonedVirtLauncher(t *testing.T) {
	plain := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "virt-launcher-vm-xyz", Labels: map[string]string{virtv1.AppLabel: "virt-launcher"}}}
	if IsMaroonedVirtLauncher(plain) {
		t.Fatal("plain virt-launcher must not be mutated")
	}
	labeled := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "virt-launcher-marooned-uid-xyz",
		Labels: map[string]string{
			virtv1.AppLabel:      "virt-launcher",
			util.SandboxVMILabel: "true",
			util.SandboxIDLabel:  "pod-uid-1",
		},
	}}
	if !IsMaroonedVirtLauncher(labeled) {
		t.Fatal("sandbox-labeled launcher should match")
	}
	user := &corev1.Pod{Spec: corev1.PodSpec{RuntimeClassName: strPtr(util.RuntimeClassName)}}
	if IsMaroonedVirtLauncher(user) {
		t.Fatal("user sandbox pod is not a virt-launcher")
	}
}

func TestMutateVirtLauncherInjectsSidecar(t *testing.T) {
	u := int64(107)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "virt-launcher-marooned-aaaa-xyz",
			Labels: map[string]string{
				virtv1.AppLabel:      "virt-launcher",
				util.SandboxVMILabel: "true",
				util.SandboxIDLabel:  "aaaa-bbbb",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name: "compute",
				SecurityContext: &corev1.SecurityContext{
					RunAsUser: &u,
				},
			}},
		},
	}
	if err := MutateVirtLauncher(pod, "registry:5000/marooned-vsockfwd:latest"); err != nil {
		t.Fatal(err)
	}
	if !hasVsockfwd(pod) {
		t.Fatal("missing sidecar")
	}
	foundVol := false
	for _, v := range pod.Spec.Volumes {
		if v.Name == vsockfwdVolumeName && v.HostPath != nil {
			foundVol = true
		}
	}
	if !foundVol {
		t.Fatal("missing hostPath volume")
	}
	side := pod.Spec.Containers[1]
	if side.Name != vsockfwdContainerName {
		t.Fatalf("sidecar %s", side.Name)
	}
	if side.ImagePullPolicy != corev1.PullIfNotPresent {
		t.Fatalf("pull policy %s", side.ImagePullPolicy)
	}
	if side.SecurityContext == nil || side.SecurityContext.RunAsUser == nil || *side.SecurityContext.RunAsUser != 107 {
		t.Fatal("sidecar must copy compute uid 107")
	}
	if side.SecurityContext.Privileged != nil && *side.SecurityContext.Privileged {
		t.Fatal("sidecar must not be privileged")
	}
	if side.SecurityContext.SELinuxOptions != nil {
		t.Fatal("do not set seLinuxOptions.type; inherit virt-launcher container_t+MCS")
	}
	if pod.Spec.ShareProcessNamespace != nil && *pod.Spec.ShareProcessNamespace {
		t.Fatal("must not set shareProcessNamespace")
	}
	// idempotent
	if err := MutateVirtLauncher(pod, "registry:5000/marooned-vsockfwd:latest"); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range pod.Spec.Containers {
		if c.Name == vsockfwdContainerName {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("sidecar count %d", n)
	}
}

func TestMutateVirtLauncherSkipsDeleting(t *testing.T) {
	now := metav1.Now()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "virt-launcher-x",
			DeletionTimestamp: &now,
			Labels: map[string]string{
				virtv1.AppLabel:      "virt-launcher",
				util.SandboxVMILabel: "true",
				util.SandboxIDLabel:  "uid",
			},
		},
	}
	if err := MutateVirtLauncher(pod, "img"); err != nil {
		t.Fatal(err)
	}
	if hasVsockfwd(pod) {
		t.Fatal("must not inject on deleting launcher")
	}
}

func strPtr(s string) *string { return &s }
