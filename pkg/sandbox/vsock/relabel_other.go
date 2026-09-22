//go:build !linux

package vsock

func Relabel(path, context string) error {
	return nil
}
