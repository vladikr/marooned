package main

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"maroonedpods.io/maroonedpods/pkg/sandbox"
)

func runPauseForwards(dir string) {
	var stop chan struct{}
	var last string
	for {
		b, err := os.ReadFile(filepath.Join(dir, sandbox.ForwardFile))
		cur := ""
		if err == nil {
			cur = string(b)
		}
		if cur != last {
			if stop != nil {
				close(stop)
				stop = nil
			}
			if cur != "" {
				spec, err := sandbox.ParsePortForward(b)
				if err == nil && spec.Dest != "" && len(spec.Ports) > 0 {
					stop = make(chan struct{})
					go listenForwards(spec, stop)
				}
			}
			last = cur
		}
		time.Sleep(time.Second)
	}
}

func listenForwards(spec sandbox.PortForwardSpec, stop <-chan struct{}) {
	var lns []net.Listener
	defer func() {
		for _, ln := range lns {
			_ = ln.Close()
		}
	}()
	for _, p := range spec.Ports {
		ln, err := net.Listen("tcp", ":"+strconv.Itoa(p))
		if err != nil {
			continue
		}
		lns = append(lns, ln)
		go acceptForward(ln, spec.Dest, p, stop)
	}
	<-stop
}

func acceptForward(ln net.Listener, dest string, port int, stop <-chan struct{}) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		select {
		case <-stop:
			_ = c.Close()
			return
		default:
		}
		go proxyTCP(c, net.JoinHostPort(dest, strconv.Itoa(port)))
	}
}

func proxyTCP(c net.Conn, addr string) {
	defer c.Close()
	d, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return
	}
	defer d.Close()
	go func() { _, _ = io.Copy(d, c) }()
	_, _ = io.Copy(c, d)
}
