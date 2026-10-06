package sandbox

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

// DiskMountRoot is where kubelet attaches PVC/ephemeral claims on the user
// Pod so qemu can use them as disks. The user's volumeMount.mountPath is the
// guest path (kept in the volumes annotation).
const DiskMountRoot = "/run/marooned/disks"

// VirtPVCDisksDir is where virt-launcher looks for filesystem PVC disk.img
// files (GetFilesystemVolumePath).
const VirtPVCDisksDir = "/var/run/kubevirt-private/vmi-disks"

// DiskImgName is the raw image KubeVirt expects at the root of a filesystem PVC.
const DiskImgName = "disk.img"

// DiskMountPath is the dummy-container dest kubelet bind-mounts a claim into.
func DiskMountPath(volumeName string) string {
	if volumeName == "" {
		return DiskMountRoot
	}
	return DiskMountRoot + "/" + volumeName
}

// VMIDiskVolumeName is the VMI volume/disk name for a Pod PVC/ephemeral volume.
func VMIDiskVolumeName(podVolume string) string {
	return "vol-" + podVolume
}

// PodVolumeFromVMIDisk reverses VMIDiskVolumeName. Other VMI volumes
// (user-rootfs) keep their name as the Pod volume name.
func PodVolumeFromVMIDisk(vmiVolume string) string {
	if strings.HasPrefix(vmiVolume, "vol-") {
		return strings.TrimPrefix(vmiVolume, "vol-")
	}
	return vmiVolume
}

// VirtPVCDiskOverlay is virt-launcher's directory for one filesystem PVC.
func VirtPVCDiskOverlay(rootfs, vmiVolume string) string {
	return filepath.Join(rootfs, "var/run/kubevirt-private/vmi-disks", vmiVolume)
}

// FindClaimVolumePath is the kubelet NodePublish directory for a Pod volume.
// CSI uses <plugin>/<name>/mount; in-tree local/hostPath uses <plugin>/<name>.
func FindClaimVolumePath(podsDir, podUID, volumeName string) string {
	if podsDir == "" || podUID == "" || volumeName == "" {
		return ""
	}
	root := filepath.Join(podsDir, podUID, "volumes")
	ents, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		cand := filepath.Join(root, e.Name(), volumeName)
		st, err := os.Stat(cand)
		if err != nil {
			continue
		}
		mount := filepath.Join(cand, "mount")
		if mst, err := os.Stat(mount); err == nil && mst.IsDir() {
			return mount
		}
		if st.IsDir() {
			return cand
		}
		return cand
	}
	return ""
}

// KubeletClaimVolumePath is FindClaimVolumePath under /var/lib/kubelet/pods.
func KubeletClaimVolumePath(podUID, volumeName string) string {
	return FindClaimVolumePath(kubeletPodsDir, podUID, volumeName)
}

// DropFromLivePod reports volumes that must not be on the user Pod.
// emptyDir stays guest tmpfs. PVC/ephemeral stay so kubelet attaches once.
func DropFromLivePod(vol corev1.Volume) bool {
	return vol.EmptyDir != nil
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
		byName := map[string]int{}
		for j, vm := range containers[i].VolumeMounts {
			byName[vm.Name] = j
		}
		for _, vm := range m.VolumeMounts {
			if j, ok := byName[vm.Name]; ok {
				containers[i].VolumeMounts[j] = vm
				continue
			}
			containers[i].VolumeMounts = append(containers[i].VolumeMounts, vm)
		}
		if len(m.VolumeDevices) > 0 {
			drop := map[string]struct{}{}
			for _, vd := range m.VolumeDevices {
				drop[vd.Name] = struct{}{}
			}
			kept := make([]corev1.VolumeMount, 0, len(containers[i].VolumeMounts))
			for _, vm := range containers[i].VolumeMounts {
				if _, ok := drop[vm.Name]; ok {
					continue
				}
				kept = append(kept, vm)
			}
			if len(kept) == 0 {
				containers[i].VolumeMounts = nil
			} else {
				containers[i].VolumeMounts = kept
			}
		}
		byDev := map[string]int{}
		for j, vd := range containers[i].VolumeDevices {
			byDev[vd.Name] = j
		}
		for _, vd := range m.VolumeDevices {
			if j, ok := byDev[vd.Name]; ok {
				containers[i].VolumeDevices[j] = vd
				continue
			}
			containers[i].VolumeDevices = append(containers[i].VolumeDevices, vd)
		}
	}
}
