//go:build !linux

package vsock

import (
	"fmt"
	"net"
	"time"
)

func listenVsock(port uint32) (net.Listener, error) {
	return nil, fmt.Errorf("vsock is only supported on linux")
}

func dialVsock(rest string, timeout time.Duration) (net.Conn, error) {
	_, _, err := parseHostPort(rest)
	if err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("vsock is only supported on linux")
}
