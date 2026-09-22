package sandbox

import (
	"k8s.io/apimachinery/pkg/api/resource"
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
	n := imageBytes + userRootfsSlack
	if n < minUserRootfs {
		n = minUserRootfs
	}
	return *resource.NewQuantity(n, resource.BinarySI)
}
