package sandbox

import (
	corev1 "k8s.io/api/core/v1"
)

// GuestMount is a volume the agent should set up inside the user rootfs.
type GuestMount struct {
	VolumeName string `json:"volumeName"`
	GuestPath  string `json:"guestPath"`
	Kind       string `json:"kind"` // tmpfs, virtio-blk, files
	ReadOnly   bool   `json:"readOnly"`
}

// GuestMounts returns mounts for stripped emptyDir (guest tmpfs) and PVC
// (mkdir only until virtio-blk is mounted). Uses the webhook snapshot when
// the live spec has already been stripped.
func GuestMounts(pod *corev1.Pod) []GuestMount {
	if pod == nil {
		return nil
	}
	src := RestoreVolumes(pod)
	volByName := map[string]corev1.Volume{}
	for _, v := range src.Spec.Volumes {
		volByName[v.Name] = v
	}
	var out []GuestMount
	for _, c := range src.Spec.Containers {
		for _, m := range c.VolumeMounts {
			vol, ok := volByName[m.Name]
			if !ok {
				continue
			}
			kind := ""
			switch {
			case vol.EmptyDir != nil:
				kind = "tmpfs"
			case vol.PersistentVolumeClaim != nil || vol.Ephemeral != nil:
				kind = "virtio-blk"
			default:
				continue
			}
			out = append(out, GuestMount{
				VolumeName: vol.Name,
				GuestPath:  m.MountPath,
				Kind:       kind,
				ReadOnly:   m.ReadOnly || (vol.PersistentVolumeClaim != nil && vol.PersistentVolumeClaim.ReadOnly),
			})
		}
	}
	return out
}
