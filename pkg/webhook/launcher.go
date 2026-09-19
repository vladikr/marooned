package webhook

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	virtv1 "kubevirt.io/api/core/v1"

	"maroonedpods.io/maroonedpods/pkg/util"
)

const (
	vsockfwdContainerName = "marooned-vsockfwd"
	vsockfwdVolumeName    = "marooned-run"
	DefaultVsockfwdImage  = "quay.io/vladikr/marooned-vsockfwd:latest"
	qemuUser              = int64(107)
)

// IsMaroonedVirtLauncher reports a stock KubeVirt virt-launcher Pod for a
// marooned sandbox VMI. KubeVirt copies VMI labels onto the launcher.
func IsMaroonedVirtLauncher(pod *corev1.Pod) bool {
	if pod == nil {
		return false
	}
	if !isVirtLauncher(pod) {
		return false
	}
	if pod.Labels != nil {
		if pod.Labels[util.SandboxVMILabel] == "true" {
			return true
		}
		if pod.Labels[util.SandboxModeLabel] == util.SandboxModeSandbox {
			return true
		}
	}
	domain := launcherDomain(pod)
	return strings.HasPrefix(domain, util.UserNamespaceVMIPrefix)
}

func isVirtLauncher(pod *corev1.Pod) bool {
	if pod.Labels != nil && pod.Labels[virtv1.AppLabel] == "virt-launcher" {
		return true
	}
	return strings.HasPrefix(pod.Name, "virt-launcher-")
}

func launcherDomain(pod *corev1.Pod) string {
	if pod.Labels != nil {
		if d := pod.Labels["kubevirt.io/domain"]; d != "" {
			return d
		}
	}
	if pod.Annotations != nil {
		if d := pod.Annotations["kubevirt.io/domain"]; d != "" {
			return d
		}
	}
	return ""
}

func sandboxPodUID(pod *corev1.Pod) string {
	if pod.Labels != nil {
		if id := pod.Labels[util.SandboxIDLabel]; id != "" {
			return id
		}
	}
	domain := launcherDomain(pod)
	if strings.HasPrefix(domain, util.UserNamespaceVMIPrefix) {
		return strings.TrimPrefix(domain, util.UserNamespaceVMIPrefix)
	}
	return ""
}

// MutateVirtLauncher injects marooned-vsockfwd into a marooned virt-launcher
// Pod. It does not touch user-Pod sandbox finalizers.
func MutateVirtLauncher(pod *corev1.Pod, image string) error {
	if pod.DeletionTimestamp != nil && !pod.DeletionTimestamp.IsZero() {
		return nil
	}
	if image == "" {
		image = DefaultVsockfwdImage
	}
	uid := sandboxPodUID(pod)
	if uid == "" {
		return nil
	}
	ensureMaroonedRunVolume(pod)
	if hasVsockfwd(pod) {
		return nil
	}
	pod.Spec.ShareProcessNamespace = nil
	pod.Spec.Containers = append(pod.Spec.Containers, vsockfwdContainer(pod, image, uid))
	return nil
}

func hasVsockfwd(pod *corev1.Pod) bool {
	for _, c := range pod.Spec.Containers {
		if c.Name == vsockfwdContainerName {
			return true
		}
	}
	return false
}

func ensureMaroonedRunVolume(pod *corev1.Pod) {
	for _, v := range pod.Spec.Volumes {
		if v.Name == vsockfwdVolumeName {
			return
		}
	}
	dir := corev1.HostPathDirectoryOrCreate
	pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
		Name: vsockfwdVolumeName,
		VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{Path: "/var/run/marooned", Type: &dir},
		},
	})
}

func vsockfwdContainer(pod *corev1.Pod, image, uid string) corev1.Container {
	return corev1.Container{
		Name:            vsockfwdContainerName,
		Image:           image,
		ImagePullPolicy: corev1.PullAlways,
		Args:            []string{"-uid", uid, "-host-dir", "/var/run/marooned"},
		SecurityContext: vsockfwdSecurityContext(pod),
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10m"),
				corev1.ResourceMemory: resource.MustParse("16Mi"),
			},
		},
		VolumeMounts: []corev1.VolumeMount{{
			Name:      vsockfwdVolumeName,
			MountPath: "/var/run/marooned",
		}},
	}
}

func vsockfwdSecurityContext(pod *corev1.Pod) *corev1.SecurityContext {
	for i := range pod.Spec.Containers {
		c := &pod.Spec.Containers[i]
		if c.Name == "compute" && c.SecurityContext != nil {
			sc := c.SecurityContext.DeepCopy()
			sc.Privileged = nil
			if sc.Capabilities == nil {
				sc.Capabilities = &corev1.Capabilities{}
			}
			sc.Capabilities.Add = nil
			return sc
		}
	}
	u := qemuUser
	priv := false
	return &corev1.SecurityContext{
		RunAsUser:  &u,
		Privileged: &priv,
	}
}
