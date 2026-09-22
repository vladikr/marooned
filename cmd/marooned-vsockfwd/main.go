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
	klog.Infof("waiting for sandbox dir %s (uid 107 cannot mkdir on the hostPath)", dir)
	for {
		st, err := os.Stat(dir)
		if err == nil && st.IsDir() {
			break
		}
		time.Sleep(time.Second)
	}
	sock := vsock.AgentSockPath(hostDir, uid)
	var ln net.Listener
	for {
		_ = os.Remove(sock)
		l, err := net.Listen("unix", sock)
		if err == nil {
			ln = l
			break
		}
		klog.Infof("listen %s: %v (waiting for dir to be writable)", sock, err)
		time.Sleep(time.Second)
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
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		cid, err := vsock.ResolveGuestCID(hostDir, uid)
		if err != nil {
			last = err
			time.Sleep(time.Second)
			continue
		}
		conn, err := vsock.Dial(fmt.Sprintf("vsock:%d:%d", cid, port), 2*time.Second)
		if err == nil {
			return conn, nil
		}
		last = err
		time.Sleep(400 * time.Millisecond)
	}
	if last == nil {
		last = fmt.Errorf("timeout")
	}
	return nil, last
}
