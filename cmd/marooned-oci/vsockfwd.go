package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"syscall"
	"time"

	"maroonedpods.io/maroonedpods/pkg/sandbox/vsock"
)

func startVsockFwd(uid, netns string) {
	if uid == "" {
		return
	}
	self, err := os.Executable()
	if err != nil {
		self = os.Args[0]
	}
	args := []string{"vsockfwd", uid}
	var cmd *exec.Cmd
	if netns != "" {
		cmd = exec.Command("nsenter", append([]string{"--net=" + netns, "--", self}, args...)...)
	} else {
		cmd = exec.Command(self, args...)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return
	}
	_ = cmd.Process.Release()
}

func runVsockFwd(uid string) int {
	if uid == "" {
		return 1
	}
	if err := vsock.EnsureSandboxDir(vsock.DefaultHostDir, uid); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	sock := vsock.AgentSockPath(vsock.DefaultHostDir, uid)
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	_ = os.Chmod(sock, 0666)
	for {
		c, err := ln.Accept()
		if err != nil {
			return 1
		}
		go proxyVsock(c, uid)
	}
}

func proxyVsock(unixConn net.Conn, uid string) {
	defer unixConn.Close()
	var last error
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		cid, err := vsock.ResolveGuestCID(vsock.DefaultHostDir, uid)
		if err != nil {
			last = err
			time.Sleep(400 * time.Millisecond)
			continue
		}
		vs, err := vsock.Dial(fmt.Sprintf("vsock:%d:%d", cid, vsock.AgentPort), 2*time.Second)
		if err != nil {
			last = err
			time.Sleep(400 * time.Millisecond)
			continue
		}
		go func() {
			_, _ = io.Copy(vs, unixConn)
			_ = vs.Close()
		}()
		_, _ = io.Copy(unixConn, vs)
		_ = vs.Close()
		return
	}
	_ = last
}
