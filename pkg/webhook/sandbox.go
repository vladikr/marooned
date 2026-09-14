package webhook

import (
	"strings"

	corev1 "k8s.io/api/core/v1"

	"maroonedpods.io/maroonedpods/pkg/sandbox"
	"maroonedpods.io/maroonedpods/pkg/util"
)

const (
	resourceCPU              = corev1.ResourceCPU
	resourceMemory           = corev1.ResourceMemory
	resourceEphemeralStorage = corev1.ResourceEphemeralStorage
	hugepagesPrefix          = "hugepages-"
)

// IsSandboxPod reports whether the pod uses RuntimeClass marooned.
func IsSandboxPod(pod *corev1.Pod) bool {
	if pod == nil || pod.Spec.RuntimeClassName == nil {
		return false
	}
	return *pod.Spec.RuntimeClassName == util.RuntimeClassName
}

// IsNodeModePod reports whether the pod uses the legacy guest-kubelet path.
func IsNodeModePod(pod *corev1.Pod) bool {
	if pod == nil || pod.Labels == nil {
		return false
	}
	if IsSandboxPod(pod) {
		return false
	}
	_, ok := pod.Labels[util.MaroonedPodLabel]
	return ok
}

// MutateSandboxPod applies the sandbox admission rules in place:
// persist stripped volume spec, set placement, finalizer, mode label,
// strip devices/hugepages/PVCs. CPU/memory requests stay.
func MutateSandboxPod(pod *corev1.Pod) error {
	if pod.Labels == nil {
		pod.Labels = map[string]string{}
	}
	pod.Labels[util.SandboxModeLabel] = util.SandboxModeSandbox

	if !hasFinalizer(pod.Finalizers, util.SandboxFinalizer) {
		pod.Finalizers = append(pod.Finalizers, util.SandboxFinalizer)
	}

	if err := persistVolumeSnapshot(pod); err != nil {
		return err
	}

	stripPodResourceClaims(pod)
	keptVolumes, droppedVolumeNames := filterVolumes(pod.Spec.Volumes)
	pod.Spec.Volumes = keptVolumes

	for i := range pod.Spec.Containers {
		stripContainer(&pod.Spec.Containers[i], droppedVolumeNames)
	}
	for i := range pod.Spec.InitContainers {
		stripContainer(&pod.Spec.InitContainers[i], droppedVolumeNames)
	}
	return nil
}

func persistVolumeSnapshot(pod *corev1.Pod) error {
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	if _, ok := pod.Annotations[util.VolumesAnnotation]; !ok {
		raw, err := sandbox.EncodeVolumeSnapshot(sandbox.CaptureStrippedVolumes(pod))
		if err != nil {
			return err
		}
		pod.Annotations[util.VolumesAnnotation] = raw
	}
	if pod.Annotations[util.PlacementAnnotation] == "" {
		pod.Annotations[util.PlacementAnnotation] = sandbox.PlacementForPod(pod)
	}
	return nil
}

func hasFinalizer(finalizers []string, name string) bool {
	for _, f := range finalizers {
		if f == name {
			return true
		}
	}
	return false
}

func stripPodResourceClaims(pod *corev1.Pod) {
	pod.Spec.ResourceClaims = nil
}

func stripContainer(c *corev1.Container, droppedVolumeNames map[string]struct{}) {
	c.Resources.Requests = keepCPUMemory(c.Resources.Requests)
	c.Resources.Limits = keepCPUMemory(c.Resources.Limits)
	c.Resources.Claims = nil
	c.VolumeMounts = filterVolumeMounts(c.VolumeMounts, droppedVolumeNames)
	c.VolumeDevices = filterVolumeDevices(c.VolumeDevices, droppedVolumeNames)
}

func keepCPUMemory(list corev1.ResourceList) corev1.ResourceList {
	if list == nil {
		return nil
	}
	out := corev1.ResourceList{}
	for name, qty := range list {
		if name == resourceCPU || name == resourceMemory || name == resourceEphemeralStorage {
			out[name] = qty
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func isWorkloadVolume(vol corev1.Volume) bool {
	src := vol.VolumeSource
	if src.PersistentVolumeClaim != nil {
		return true
	}
	if src.Ephemeral != nil {
		return true
	}
	if src.EmptyDir != nil {
		return true
	}
	return false
}

func keepProjectedSA(vol corev1.Volume) bool {
	if vol.VolumeSource.Projected == nil {
		return true
	}
	return true
}

func filterVolumes(volumes []corev1.Volume) ([]corev1.Volume, map[string]struct{}) {
	dropped := map[string]struct{}{}
	kept := make([]corev1.Volume, 0, len(volumes))
	for _, vol := range volumes {
		if isWorkloadVolume(vol) {
			dropped[vol.Name] = struct{}{}
			continue
		}
		if !keepProjectedSA(vol) {
			dropped[vol.Name] = struct{}{}
			continue
		}
		kept = append(kept, vol)
	}
	return kept, dropped
}

func filterVolumeMounts(mounts []corev1.VolumeMount, dropped map[string]struct{}) []corev1.VolumeMount {
	if len(mounts) == 0 {
		return mounts
	}
	kept := make([]corev1.VolumeMount, 0, len(mounts))
	for _, m := range mounts {
		if _, drop := dropped[m.Name]; drop {
			continue
		}
		kept = append(kept, m)
	}
	return kept
}

func filterVolumeDevices(devices []corev1.VolumeDevice, dropped map[string]struct{}) []corev1.VolumeDevice {
	if len(devices) == 0 {
		return devices
	}
	kept := make([]corev1.VolumeDevice, 0, len(devices))
	for _, d := range devices {
		if _, drop := dropped[d.Name]; drop {
			continue
		}
		kept = append(kept, d)
	}
	return kept
}

// IsHugepageResource reports hugepages-* resource names.
func IsHugepageResource(name corev1.ResourceName) bool {
	return strings.HasPrefix(string(name), hugepagesPrefix)
}

// IsExtendedResource reports non-core compute resources that belong on the VMI.
func IsExtendedResource(name corev1.ResourceName) bool {
	if name == resourceCPU || name == resourceMemory || name == resourceEphemeralStorage {
		return false
	}
	if IsHugepageResource(name) {
		return true
	}
	return true
}
