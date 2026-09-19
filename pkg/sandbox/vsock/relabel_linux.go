package vsock

import (
	"golang.org/x/sys/unix"
)

// Relabel sets security.selinux on path. context is vmi.status.selinuxContext
// (container_file_t + the virt-launcher MCS). No-op if context is empty or
// the fs does not support SELinux.
func Relabel(path, context string) error {
	if path == "" || context == "" {
		return nil
	}
	err := unix.Lsetxattr(path, "security.selinux", []byte(context), 0)
	if err == unix.EOPNOTSUPP || err == unix.ENOTSUP {
		return nil
	}
	return err
}
