package vsock

import (
	"fmt"
	"net"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

type vsockAddr struct {
	cid  uint32
	port uint32
}

func (a vsockAddr) Network() string { return "vsock" }
func (a vsockAddr) String() string  { return fmt.Sprintf("%d:%d", a.cid, a.port) }

func dialVsock(rest string, timeout time.Duration) (net.Conn, error) {
	cid, port, err := parseHostPort(rest)
	if err != nil {
		return nil, err
	}
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("vsock socket: %w", err)
	}
	sa := &unix.SockaddrVM{CID: cid, Port: port}
	if timeout > 0 {
		deadline := time.Now().Add(timeout)
		_ = unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_SNDTIMEO, &unix.Timeval{Sec: int64(time.Until(deadline).Seconds())})
	}
	if err := unix.Connect(fd, sa); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("vsock connect %d:%d: %w", cid, port, err)
	}
	f := os.NewFile(uintptr(fd), fmt.Sprintf("vsock:%d:%d", cid, port))
	if f == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("vsock file from fd")
	}
	fconn, err := net.FileConn(f)
	_ = f.Close()
	if err != nil {
		return nil, err
	}
	return fconn, nil
}
