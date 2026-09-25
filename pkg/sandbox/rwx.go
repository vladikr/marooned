package sandbox

import (
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

// ErrRWXUnsupported is returned when a sandbox Pod asks for ReadWriteMany.
// Virtiofs would be the KubeVirt share path, but full RWX virtiofsd needs a
// privileged/root virt-launcher, which is a non-goal.
var ErrRWXUnsupported = errors.New("ReadWriteMany volumes are not supported")

// AccessModesRWX reports a ReadWriteMany access mode.
func AccessModesRWX(modes []corev1.PersistentVolumeAccessMode) bool {
	for _, m := range modes {
		if m == corev1.ReadWriteMany {
			return true
		}
	}
	return false
}

// RWXUnsupportedError names the volume that cannot be attached.
func RWXUnsupportedError(volumeName string) error {
	return fmt.Errorf("sandbox volume %q is ReadWriteMany: %w (virtiofs skipped; would need privileged virt-launcher)", volumeName, ErrRWXUnsupported)
}

// IsRWXUnsupported reports ErrRWXUnsupported.
func IsRWXUnsupported(err error) bool {
	return errors.Is(err, ErrRWXUnsupported)
}

// PVCAccessModes looks up a claim's access modes. Nil skips PVC checks
// (ephemeral VolumeClaimTemplate is still inspected).
type PVCAccessModes func(namespace, claimName string) ([]corev1.PersistentVolumeAccessMode, error)

// RejectRWXVolumes fails if any stripped volume is ReadWriteMany.
func RejectRWXVolumes(pod *corev1.Pod, lookup PVCAccessModes) error {
	if pod == nil {
		return nil
	}
	src := RestoreVolumes(pod)
	for _, vol := range src.Spec.Volumes {
		if vol.Ephemeral != nil && vol.Ephemeral.VolumeClaimTemplate != nil {
			if AccessModesRWX(vol.Ephemeral.VolumeClaimTemplate.Spec.AccessModes) {
				return RWXUnsupportedError(vol.Name)
			}
		}
		if vol.PersistentVolumeClaim == nil || lookup == nil {
			continue
		}
		modes, err := lookup(src.Namespace, vol.PersistentVolumeClaim.ClaimName)
		if err != nil {
			return err
		}
		if AccessModesRWX(modes) {
			return RWXUnsupportedError(vol.Name)
		}
	}
	return nil
}
