package sandbox

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"maroonedpods.io/maroonedpods/pkg/util"
)

const (
	// UserRootfsVolume is the VMI emptyDisk that holds the user container rootfs.
	UserRootfsVolume = "user-rootfs"
	// UserRootfsSerial is the virtio serial the guest agent looks up.
	UserRootfsSerial = "userrootfs"
	minUserRootfs    = 256 * 1024 * 1024
	userRootfsSlack  = 128 * 1024 * 1024
)

// UserRootfsCapacity is pulled_image_size + 128Mi, floored at 256Mi.
func UserRootfsCapacity(imageBytes int64) resource.Quantity {
	return UserRootfsCapacityN(imageBytes, 1)
}

// UserRootfsCapacityN sizes the shared emptyDisk for n guest containers.
func UserRootfsCapacityN(imageBytes int64, n int) resource.Quantity {
	if n < 1 {
		n = 1
	}
	min := minUserRootfs * int64(n)
	sized := imageBytes + userRootfsSlack
	if sized < min {
		sized = min
	}
	return *resource.NewQuantity(sized, resource.BinarySI)
}

// RootfsVolumeName is the Pod volume used as the guest rootfs disk.
func RootfsVolumeName(pod *corev1.Pod) string {
	if pod == nil || pod.Annotations == nil {
		return ""
	}
	return strings.TrimSpace(pod.Annotations[util.RootfsVolumeAnnotation])
}

// RootfsPVC is the claim for RootfsVolumeName, if it is a PVC.
func RootfsPVC(pod *corev1.Pod) *corev1.PersistentVolumeClaimVolumeSource {
	name := RootfsVolumeName(pod)
	if name == "" || pod == nil {
		return nil
	}
	src := RestoreVolumes(pod)
	for _, v := range src.Spec.Volumes {
		if v.Name == name && v.PersistentVolumeClaim != nil {
			return v.PersistentVolumeClaim
		}
	}
	return nil
}
