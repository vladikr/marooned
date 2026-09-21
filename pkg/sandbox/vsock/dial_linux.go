package vsock

import (
	"fmt"
	"io"
	"net"
	"time"

	"golang.org/x/sys/unix"
)

type vsockAddr struct {
	cid  uint32
	port uint32
}

func (a vsockAddr) Network() string { return "vsock" }
func (a vsockAddr) String() string  { return fmt.Sprintf("%d:%d", a.cid, a.port) }

type vsockConn struct {
	fd     int
	la, ra vsockAddr
}

func (c *vsockConn) Read(b []byte) (int, error) {
	for {
		n, err := unix.Read(c.fd, b)
		if err == unix.EINTR {
			continue
		}
		if n == 0 && err == nil {
			return 0, io.EOF
		}
		return n, err
	}
}

func (c *vsockConn) Write(b []byte) (int, error) {
	for {
		n, err := unix.Write(c.fd, b)
		if err == unix.EINTR {
			continue
		}
		return n, err
	}
}

func (c *vsockConn) Close() error { return unix.Close(c.fd) }
func (c *vsockConn) LocalAddr() net.Addr         { return c.la }
func (c *vsockConn) RemoteAddr() net.Addr        { return c.ra }
func (c *vsockConn) SetDeadline(t time.Time) error {
	if err := c.SetReadDeadline(t); err != nil {
		return err
	}
	return c.SetWriteDeadline(t)
}
func (c *vsockConn) SetReadDeadline(t time.Time) error {
	return unix.SetsockoptTimeval(c.fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, timeToTimeval(t))
}
func (c *vsockConn) SetWriteDeadline(t time.Time) error {
	return unix.SetsockoptTimeval(c.fd, unix.SOL_SOCKET, unix.SO_SNDTIMEO, timeToTimeval(t))
}

func timeToTimeval(t time.Time) *unix.Timeval {
	if t.IsZero() {
		return &unix.Timeval{}
	}
	d := time.Until(t)
	if d < 0 {
		d = 0
	}
	return &unix.Timeval{Sec: int64(d / time.Second), Usec: int64(d%time.Second) / 1000}
}

type vsockListener struct {
	fd   int
	port uint32
}

func (l *vsockListener) Accept() (net.Conn, error) {
	nfd, rsa, err := unix.Accept(l.fd)
	if err != nil {
		return nil, err
	}
	cid := uint32(0)
	rport := uint32(0)
	if sa, ok := rsa.(*unix.SockaddrVM); ok {
		cid = sa.CID
		rport = sa.Port
	}
	return &vsockConn{
		fd: nfd,
		la: vsockAddr{cid: unix.VMADDR_CID_ANY, port: l.port},
		ra: vsockAddr{cid: cid, port: rport},
	}, nil
}

func (l *vsockListener) Close() error { return unix.Close(l.fd) }

func (l *vsockListener) Addr() net.Addr {
	return vsockAddr{cid: unix.VMADDR_CID_ANY, port: l.port}
}

func listenVsock(port uint32) (net.Listener, error) {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("vsock socket: %w", err)
	}
	_ = unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_REUSEADDR, 1)
	sa := &unix.SockaddrVM{CID: unix.VMADDR_CID_ANY, Port: port}
	if err := unix.Bind(fd, sa); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("vsock bind :%d: %w", port, err)
	}
	if err := unix.Listen(fd, 128); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("vsock listen: %w", err)
	}
	if err := unix.SetNonblock(fd, false); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	return &vsockListener{fd: fd, port: port}, nil
}

func dialVsock(rest string, timeout time.Duration) (net.Conn, error) {
	cid, port, err := parseHostPort(rest)
	if err != nil {
		return nil, err
	}
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("vsock socket: %w", err)
	}
	if timeout > 0 {
		_ = unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_SNDTIMEO, timeToTimeval(time.Now().Add(timeout)))
	}
	sa := &unix.SockaddrVM{CID: cid, Port: port}
	if err := unix.Connect(fd, sa); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("vsock connect %d:%d: %w", cid, port, err)
	}
	// net.FileConn rejects AF_VSOCK ("protocol not supported"); keep the raw fd.
	_ = unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_SNDTIMEO, &unix.Timeval{})
	la := vsockAddr{}
	if lsa, err := unix.Getsockname(fd); err == nil {
		if vm, ok := lsa.(*unix.SockaddrVM); ok {
			la = vsockAddr{cid: vm.CID, port: vm.Port}
		}
	}
	return &vsockConn{
		fd: fd,
		la: la,
		ra: vsockAddr{cid: cid, port: port},
	}, nil
}
