package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"k8s.io/klog/v2"

	"maroonedpods.io/maroonedpods/pkg/sandbox/vsock"
)

func main() {
	klog.InitFlags(nil)
	uid := flag.String("uid", "", "user Pod UID (directory under host-dir)")
	hostDir := flag.String("host-dir", vsock.DefaultHostDir, "hostPath directory shared with the shim")
	port := flag.Uint("port", vsock.AgentPort, "guest agent vsock port")
	flag.Parse()
	if *uid == "" {
		klog.Fatalf("-uid is required")
	}
	if err := run(*uid, *hostDir, uint32(*port)); err != nil {
		klog.Fatalf("vsockfwd: %v", err)
	}
}

func run(uid, hostDir string, port uint32) error {
	dir := vsock.DirFor(hostDir, uid)
	if err := os.MkdirAll(dir, 0777); err != nil {
		return err
	}
	_ = os.Chmod(dir, 0777)
	sock := vsock.AgentSockPath(hostDir, uid)
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return fmt.Errorf("listen %s: %w", sock, err)
	}
	_ = os.Chmod(sock, 0666)
	klog.Infof("marooned-vsockfwd listening %s (ns_mode=%q)", sock, vsock.ReadNSMode())
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go handle(c, hostDir, uid, port)
	}
}

func handle(unixConn net.Conn, hostDir, uid string, port uint32) {
	defer unixConn.Close()
	vs, err := dialGuest(hostDir, uid, port)
	if err != nil {
		klog.Infof("vsock dial: %v", err)
		return
	}
	defer vs.Close()
	go func() {
		_, _ = io.Copy(vs, unixConn)
		_ = vs.Close()
	}()
	_, _ = io.Copy(unixConn, vs)
}

func dialGuest(hostDir, uid string, port uint32) (net.Conn, error) {
	var last error
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		cid, err := vsock.ResolveGuestCID(hostDir, uid)
		if err != nil {
			last = err
			time.Sleep(time.Second)
			continue
		}
		conn, err := vsock.Dial(fmt.Sprintf("vsock:%d:%d", cid, port), 5*time.Second)
		if err == nil {
			return conn, nil
		}
		last = err
		time.Sleep(time.Second)
	}
	if last == nil {
		last = fmt.Errorf("timeout")
	}
	return nil, last
}
