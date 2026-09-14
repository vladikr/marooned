package sandbox

import (
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"maroonedpods.io/maroonedpods/pkg/util"
)

// VolumeSnapshot is the workload volume list the webhook strips from the user Pod.
type VolumeSnapshot struct {
	Volumes []corev1.Volume   `json:"volumes,omitempty"`
	Mounts  []ContainerMounts `json:"mounts,omitempty"`
}

// ContainerMounts records volumeMounts/volumeDevices for one container.
type ContainerMounts struct {
	Container     string                `json:"container"`
	Init          bool                  `json:"init,omitempty"`
	VolumeMounts  []corev1.VolumeMount  `json:"volumeMounts,omitempty"`
	VolumeDevices []corev1.VolumeDevice `json:"volumeDevices,omitempty"`
}

// CaptureStrippedVolumes copies PVC/ephemeral/emptyDir volumes and their mounts.
func CaptureStrippedVolumes(pod *corev1.Pod) VolumeSnapshot {
	dropped := map[string]struct{}{}
	var vols []corev1.Volume
	for _, vol := range pod.Spec.Volumes {
		if isStrippedVolume(vol) {
			dropped[vol.Name] = struct{}{}
			vols = append(vols, vol)
		}
	}
	snap := VolumeSnapshot{Volumes: vols}
	for _, c := range pod.Spec.Containers {
		if m := captureContainerMounts(c.Name, false, c.VolumeMounts, c.VolumeDevices, dropped); m != nil {
			snap.Mounts = append(snap.Mounts, *m)
		}
	}
	for _, c := range pod.Spec.InitContainers {
		if m := captureContainerMounts(c.Name, true, c.VolumeMounts, c.VolumeDevices, dropped); m != nil {
			snap.Mounts = append(snap.Mounts, *m)
		}
	}
	return snap
}

func captureContainerMounts(name string, init bool, mounts []corev1.VolumeMount, devices []corev1.VolumeDevice, dropped map[string]struct{}) *ContainerMounts {
	out := ContainerMounts{Container: name, Init: init}
	for _, m := range mounts {
		if _, ok := dropped[m.Name]; ok {
			out.VolumeMounts = append(out.VolumeMounts, m)
		}
	}
	for _, d := range devices {
		if _, ok := dropped[d.Name]; ok {
			out.VolumeDevices = append(out.VolumeDevices, d)
		}
	}
	if len(out.VolumeMounts) == 0 && len(out.VolumeDevices) == 0 {
		return nil
	}
	return &out
}

func isStrippedVolume(vol corev1.Volume) bool {
	return vol.PersistentVolumeClaim != nil || vol.Ephemeral != nil || vol.EmptyDir != nil
}

// VolumeNeedsUserNamespace is true when the volume is a namespaced claim KubeVirt must attach.
func VolumeNeedsUserNamespace(vol corev1.Volume) bool {
	return vol.PersistentVolumeClaim != nil || vol.Ephemeral != nil
}

// EncodeVolumeSnapshot marshals the snapshot and rejects oversized annotations.
func EncodeVolumeSnapshot(snap VolumeSnapshot) (string, error) {
	b, err := json.Marshal(snap)
	if err != nil {
		return "", err
	}
	if len(b) > util.MaxVolumesAnnotationBytes {
		return "", fmt.Errorf("%s annotation is %d bytes, limit %d; reduce volumes", util.VolumesAnnotation, len(b), util.MaxVolumesAnnotationBytes)
	}
	return string(b), nil
}

// DecodeVolumeSnapshot reads the webhook-persisted volume list.
func DecodeVolumeSnapshot(pod *corev1.Pod) (VolumeSnapshot, bool, error) {
	if pod == nil || pod.Annotations == nil {
		return VolumeSnapshot{}, false, nil
	}
	raw, ok := pod.Annotations[util.VolumesAnnotation]
	if !ok || raw == "" {
		return VolumeSnapshot{}, false, nil
	}
	var snap VolumeSnapshot
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		return VolumeSnapshot{}, false, fmt.Errorf("decode %s: %w", util.VolumesAnnotation, err)
	}
	return snap, true, nil
}

// VolumesOf returns stripped volumes from the annotation, else the live spec.
func VolumesOf(pod *corev1.Pod) []corev1.Volume {
	snap, ok, err := DecodeVolumeSnapshot(pod)
	if err == nil && ok {
		return snap.Volumes
	}
	if pod == nil {
		return nil
	}
	return pod.Spec.Volumes
}

// RestoreVolumes returns a copy of the pod with stripped volumes and mounts put back.
func RestoreVolumes(pod *corev1.Pod) *corev1.Pod {
	if pod == nil {
		return nil
	}
	out := pod.DeepCopy()
	snap, ok, err := DecodeVolumeSnapshot(out)
	if err != nil || !ok {
		return out
	}
	have := map[string]struct{}{}
	for _, v := range out.Spec.Volumes {
		have[v.Name] = struct{}{}
	}
	for _, v := range snap.Volumes {
		if _, exists := have[v.Name]; exists {
			continue
		}
		out.Spec.Volumes = append(out.Spec.Volumes, v)
	}
	for _, m := range snap.Mounts {
		if m.Init {
			restoreMounts(out.Spec.InitContainers, m)
			continue
		}
		restoreMounts(out.Spec.Containers, m)
	}
	return out
}

func restoreMounts(containers []corev1.Container, m ContainerMounts) {
	for i := range containers {
		if containers[i].Name != m.Container {
			continue
		}
		have := map[string]struct{}{}
		for _, vm := range containers[i].VolumeMounts {
			have[vm.Name] = struct{}{}
		}
		for _, vm := range m.VolumeMounts {
			if _, ok := have[vm.Name]; !ok {
				containers[i].VolumeMounts = append(containers[i].VolumeMounts, vm)
			}
		}
		haveDev := map[string]struct{}{}
		for _, vd := range containers[i].VolumeDevices {
			haveDev[vd.Name] = struct{}{}
		}
		for _, vd := range m.VolumeDevices {
			if _, ok := haveDev[vd.Name]; !ok {
				containers[i].VolumeDevices = append(containers[i].VolumeDevices, vd)
			}
		}
	}
}
