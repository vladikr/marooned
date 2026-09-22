package vsock

import (
	"os"
	"strings"
)

const nsModePath = "/proc/sys/net/vsock/ns_mode"

// ReadNSMode returns the vsock namespace mode ("local", "global", …).
// Missing file means global / un-namespaced vsock.
func ReadNSMode() string {
	b, err := os.ReadFile(nsModePath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
