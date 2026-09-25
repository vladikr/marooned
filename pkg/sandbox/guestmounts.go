package sandbox

import (
	corev1 "k8s.io/api/core/v1"
)

// GuestMount is a volume the agent should set up inside the user rootfs.
type GuestMount struct {
	VolumeName string `json:"volumeName"`
	GuestPath  string `json:"guestPath"`
	Kind       string `json:"kind"` // tmpfs, virtio-blk, block, files
	Serial     string `json:"serial,omitempty"`
	ReadOnly   bool   `json:"readOnly"`
}

// DiskSerial is the virtio serial (max 20 alphanum) for a named volume.
func DiskSerial(volumeName string) string {
	b := make([]byte, 0, 20)
	b = append(b, 'v')
	for i := 0; i < len(volumeName) && len(b) < 20; i++ {
		c := volumeName[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			b = append(b, c)
		}
	}
	if len(b) == 1 {
		return "vdisk"
	}
	return string(b)
}

// GuestMounts returns mounts for stripped emptyDir (guest tmpfs) and PVC
// (filesystem volumeMounts → virtio-blk mount; volumeDevices → raw bind).
// Uses the webhook snapshot when the live spec has already been stripped.
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
			if gm := guestMountFor(volByName, m.Name, m.MountPath, m.ReadOnly, false); gm != nil {
				out = append(out, *gm)
			}
		}
		for _, d := range c.VolumeDevices {
			if gm := guestMountFor(volByName, d.Name, d.DevicePath, false, true); gm != nil {
				out = append(out, *gm)
			}
		}
	}
	return out
}

func guestMountFor(volByName map[string]corev1.Volume, name, path string, readOnly, block bool) *GuestMount {
	vol, ok := volByName[name]
	if !ok {
		return nil
	}
	kind := ""
	switch {
	case vol.EmptyDir != nil:
		if block {
			return nil
		}
		kind = "tmpfs"
	case vol.PersistentVolumeClaim != nil || vol.Ephemeral != nil:
		if block {
			kind = "block"
		} else {
			kind = "virtio-blk"
		}
	default:
		return nil
	}
	gm := GuestMount{
		VolumeName: vol.Name,
		GuestPath:  path,
		Kind:       kind,
		ReadOnly:   readOnly || (vol.PersistentVolumeClaim != nil && vol.PersistentVolumeClaim.ReadOnly),
	}
	if kind == "virtio-blk" || kind == "block" {
		gm.Serial = DiskSerial(vol.Name)
	}
	return &gm
}
