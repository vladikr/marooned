package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
	"k8s.io/klog/v2"

	"maroonedpods.io/maroonedpods/pkg/sandbox/agentproto"
)

func (a *agent) execTTY(conn net.Conn, env agentproto.Envelope) {
	fail := func(err error) {
		_ = agentproto.WriteEnvelope(conn, agentproto.Envelope{ID: env.ID, Method: env.Method, Error: err.Error()})
	}
	var req agentproto.ExecRequest
	if err := json.Unmarshal(env.Payload, &req); err != nil {
		fail(err)
		return
	}
	if len(req.Command) == 0 {
		req.Command = []string{"/bin/sh", "-i"}
	}
	ptmx, slave, err := openPTY()
	if err != nil {
		fail(err)
		return
	}
	defer ptmx.Close()
	argv := append([]string{}, req.Command...)
	root := filepath.Join(ctrRoot, req.ContainerID, "root")
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if st, err := os.Stat(root); err == nil && st.IsDir() {
		argv[0] = lookPathInRoot(root, argv[0], nil)
		cmd = exec.Command(argv[0], argv[1:]...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
		cmd.Dir = "/"
		cmd.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "TERM=xterm", "PS1=# "}
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Chroot: root}
	}
	klog.Infof("exec tty %s argv=%v", req.ContainerID, argv)
	if err := cmd.Start(); err != nil {
		_ = slave.Close()
		fail(err)
		return
	}
	_ = slave.Close()
	if err := agentproto.WriteEnvelope(conn, agentproto.Envelope{ID: env.ID, Method: env.Method, OK: true}); err != nil {
		_ = cmd.Process.Kill()
		return
	}
	errc := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(ptmx, conn)
		errc <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(conn, ptmx)
		errc <- struct{}{}
	}()
	<-errc
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
}

func openPTY() (ptmx, slave *os.File, err error) {
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	ptmx = os.NewFile(uintptr(fd), "/dev/ptmx")
	unlock := int(0)
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, unlock); err != nil {
		_ = ptmx.Close()
		return nil, nil, err
	}
	n, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		_ = ptmx.Close()
		return nil, nil, err
	}
	name := fmt.Sprintf("/dev/pts/%d", n)
	sfd, err := unix.Open(name, unix.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		_ = ptmx.Close()
		return nil, nil, err
	}
	return ptmx, os.NewFile(uintptr(sfd), name), nil
}
