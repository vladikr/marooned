package vsock

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// Dial opens a connection to the guest agent.
// addr is "tcp:host:port" or "vsock:cid:port".
func Dial(addr string, timeout time.Duration) (net.Conn, error) {
	kind, rest, ok := strings.Cut(addr, ":")
	if !ok {
		return nil, fmt.Errorf("invalid agent address %q", addr)
	}
	d := net.Dialer{Timeout: timeout}
	switch kind {
	case "tcp":
		return d.Dial("tcp", rest)
	case "unix":
		return d.Dial("unix", rest)
	case "vsock":
		return dialVsock(rest, timeout)
	default:
		return nil, fmt.Errorf("unsupported agent transport %s", kind)
	}
}

func parseHostPort(rest string) (uint32, uint32, error) {
	host, portStr, ok := strings.Cut(rest, ":")
	if !ok {
		return 0, 0, fmt.Errorf("invalid vsock address %q", rest)
	}
	cid, err := strconv.ParseUint(host, 10, 32)
	if err != nil {
		return 0, 0, err
	}
	port, err := strconv.ParseUint(portStr, 10, 32)
	if err != nil {
		return 0, 0, err
	}
	return uint32(cid), uint32(port), nil
}
