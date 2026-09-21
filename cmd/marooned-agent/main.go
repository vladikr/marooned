package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"k8s.io/klog/v2"

	"maroonedpods.io/maroonedpods/pkg/sandbox/agentproto"
	"maroonedpods.io/maroonedpods/pkg/sandbox/vsock"
)

type container struct {
	id       string
	cmd      *exec.Cmd
	stdout   bytes.Buffer
	stderr   bytes.Buffer
	mu       sync.Mutex
	exited   bool
	code     int32
	restarts uint32
	done     chan struct{}
}

type agent struct {
	mu         sync.Mutex
	ctrs       map[string]*container
	unpackers  map[string]*unpackJob
	imageBytes map[string]int64
	diskBytes  map[string]int64
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "container-init" {
		os.Exit(runContainerInit())
	}
	if len(os.Args) > 1 && os.Args[1] == "pidns-exec" {
		os.Exit(runPidnsExec())
	}
	klog.InitFlags(nil)
	listen := flag.String("listen", "vsock://:1024", "listen address: vsock://:port, tcp://host:port, or unix:///path")
	flag.Parse()

	a := &agent{
		ctrs:       map[string]*container{},
		unpackers:  map[string]*unpackJob{},
		imageBytes: map[string]int64{},
		diskBytes:  map[string]int64{},
	}
	var ln net.Listener
	var err error
	for i := 0; i < 50; i++ {
		ln, err = vsock.Listen(*listen)
		if err == nil {
			break
		}
		klog.Warningf("listen %s: %v (retry)", *listen, err)
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		klog.Fatalf("listen %s: %v (not falling back to TCP; vsockfwd cannot use it)", *listen, err)
	}
	klog.Infof("marooned-agent listening on %s (%T)", ln.Addr().String(), ln)
	for {
		conn, err := ln.Accept()
		if err != nil {
			klog.Errorf("accept: %v", err)
			continue
		}
		go a.serve(conn)
	}
}

func (a *agent) serve(conn net.Conn) {
	defer conn.Close()
	defer func() {
		if rec := recover(); rec != nil {
			klog.Errorf("agent serve panic (pid 1 must not exit): %v", rec)
		}
	}()
	for {
		env, err := agentproto.ReadEnvelope(conn)
		if err != nil {
			if err != io.EOF {
				klog.V(4).Infof("read: %v", err)
			}
			return
		}
		if env.Method == agentproto.MethodExecTTY {
			a.execTTY(conn, env)
			return
		}
		resp := a.handle(env)
		if err := agentproto.WriteEnvelope(conn, resp); err != nil {
			return
		}
	}
}

func (a *agent) handle(env agentproto.Envelope) agentproto.Envelope {
	out := agentproto.Envelope{ID: env.ID, Method: env.Method, OK: true}
	var err error
	switch env.Method {
	case agentproto.MethodPing:
	case agentproto.MethodPrepareRootfs:
		err = a.prepareRootfs(env.Payload)
	case agentproto.MethodRootfs:
		err = a.rootfs(env.Payload)
	case agentproto.MethodStart:
		err = a.start(env.Payload)
	case agentproto.MethodStop:
		err = a.stop(env.Payload)
	case agentproto.MethodWait:
		err = a.wait(env.Payload)
	case agentproto.MethodStatus:
		var resp agentproto.StatusResponse
		resp, err = a.status(env.Payload)
		if err == nil {
			out.Payload, _ = json.Marshal(resp)
		}
	case agentproto.MethodExec:
		var resp agentproto.ExecResponse
		resp, err = a.exec(env.Payload)
		if err == nil {
			out.Payload, _ = json.Marshal(resp)
		}
	case agentproto.MethodLogs:
		var resp agentproto.LogsResponse
		resp, err = a.logs(env.Payload)
		if err == nil {
			out.Payload, _ = json.Marshal(resp)
		}
	case agentproto.MethodStats:
		var resp agentproto.StatsResponse
		resp, err = a.stats(env.Payload)
		if err == nil {
			out.Payload, _ = json.Marshal(resp)
		}
	case agentproto.MethodMountTable:
		err = a.mounts(env.Payload)
	case agentproto.MethodAttest:
		err = fmt.Errorf("attest is Phase 6")
	default:
		err = fmt.Errorf("unknown method %s", env.Method)
	}
	if err != nil {
		out.OK = false
		out.Error = err.Error()
	}
	return out
}

func (a *agent) start(payload json.RawMessage) error {
	var req agentproto.StartRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return err
	}
	for _, m := range req.Mounts {
		if err := ensureMount(m); err != nil {
			return err
		}
	}
	root := filepath.Join(ctrRoot, req.ContainerID, "root")
	var cmd *exec.Cmd
	if st, err := os.Stat(root); err == nil && st.IsDir() {
		cmd, err = startInRoot(root, req)
		if err != nil {
			return err
		}
	} else {
		argv := append(append([]string{}, req.Command...), req.Args...)
		if len(argv) == 0 {
			argv = []string{"/bin/sh", "-c", "sleep infinity"}
		}
		cmd = exec.Command(argv[0], argv[1:]...)
		if req.WorkDir != "" {
			cmd.Dir = req.WorkDir
		}
		cmd.Env = append(os.Environ(), req.Env...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	ctr := &container{id: req.ContainerID, cmd: cmd, done: make(chan struct{})}
	cmd.Stdout = &ctr.stdout
	cmd.Stderr = &ctr.stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	klog.Infof("started %s chroot=%s argv=%v pid=%d", req.ContainerID, root, cmd.Args, cmd.Process.Pid)
	a.mu.Lock()
	if prev := a.ctrs[req.ContainerID]; prev != nil {
		prev.mu.Lock()
		ctr.restarts = prev.restarts
		if prev.cmd != nil {
			ctr.restarts++
		}
		prev.mu.Unlock()
	}
	a.ctrs[req.ContainerID] = ctr
	a.mu.Unlock()
	go func() {
		err := cmd.Wait()
		ctr.mu.Lock()
		ctr.exited = true
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				ctr.code = int32(ee.ExitCode())
			} else {
				ctr.code = 1
			}
		}
		ctr.mu.Unlock()
		close(ctr.done)
	}()
	return nil
}

func (a *agent) ctrHostPid(id string) int {
	a.mu.Lock()
	ctr := a.ctrs[id]
	a.mu.Unlock()
	if ctr == nil || ctr.cmd == nil || ctr.cmd.Process == nil {
		return 0
	}
	pid := ctr.cmd.Process.Pid
	if err := syscall.Kill(pid, 0); err != nil {
		return 0
	}
	return pid
}

func (a *agent) status(payload json.RawMessage) (agentproto.StatusResponse, error) {
	var req agentproto.StatusRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return agentproto.StatusResponse{}, err
	}
	a.mu.Lock()
	ctr := a.ctrs[req.ContainerID]
	a.mu.Unlock()
	if ctr == nil {
		return agentproto.StatusResponse{}, fmt.Errorf("not found")
	}
	ctr.mu.Lock()
	defer ctr.mu.Unlock()
	out := agentproto.StatusResponse{ExitCode: ctr.code, Restarts: ctr.restarts}
	if ctr.cmd != nil && ctr.cmd.Process != nil {
		out.Pid = ctr.cmd.Process.Pid
	}
	if ctr.exited {
		return out, nil
	}
	if out.Pid > 0 {
		if err := syscall.Kill(out.Pid, 0); err != nil {
			out.ExitCode = 255
			return out, nil
		}
	}
	out.Running = true
	return out, nil
}

func (a *agent) stop(payload json.RawMessage) error {
	var req struct {
		ContainerID string `json:"containerID"`
	}
	if err := json.Unmarshal(payload, &req); err != nil {
		return err
	}
	a.mu.Lock()
	ctr := a.ctrs[req.ContainerID]
	a.mu.Unlock()
	if ctr == nil || ctr.cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-ctr.cmd.Process.Pid, syscall.SIGTERM)
}

func (a *agent) wait(payload json.RawMessage) error {
	var req struct {
		ContainerID string `json:"containerID"`
	}
	if err := json.Unmarshal(payload, &req); err != nil {
		return err
	}
	a.mu.Lock()
	ctr := a.ctrs[req.ContainerID]
	a.mu.Unlock()
	if ctr == nil || ctr.done == nil {
		return fmt.Errorf("not found")
	}
	<-ctr.done
	return nil
}

func (a *agent) exec(payload json.RawMessage) (agentproto.ExecResponse, error) {
	var req agentproto.ExecRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return agentproto.ExecResponse{}, err
	}
	if len(req.Command) == 0 {
		return agentproto.ExecResponse{ExitCode: 1, Stderr: "empty command"}, nil
	}
	a.mu.Lock()
	ctr := a.ctrs[req.ContainerID]
	a.mu.Unlock()
	if ctr != nil {
		ctr.mu.Lock()
		dead := ctr.exited
		ctr.mu.Unlock()
		if dead || a.ctrHostPid(req.ContainerID) == 0 {
			return agentproto.ExecResponse{ExitCode: 1, Stderr: "container process has exited"}, nil
		}
	}
	argv := append([]string{}, req.Command...)
	root := filepath.Join(ctrRoot, req.ContainerID, "root")
	cmd := exec.Command(argv[0], argv[1:]...)
	if st, err := os.Stat(root); err == nil && st.IsDir() && a.ctrHostPid(req.ContainerID) > 0 {
		argv[0] = lookPathInRoot(root, argv[0], nil)
		cmd = nsenterExecCmd(a.ctrHostPid(req.ContainerID), root, argv)
		cmd.Dir = "/"
		cmd.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "MAROONED_SKIP_PROC=1"}
	} else if st, err := os.Stat(root); err == nil && st.IsDir() {
		argv[0] = lookPathInRoot(root, argv[0], nil)
		cmd = exec.Command(argv[0], argv[1:]...)
		cmd.Dir = "/"
		cmd.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}
		cmd.SysProcAttr = &syscall.SysProcAttr{Chroot: root}
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	resp := agentproto.ExecResponse{Stdout: stdout.String(), Stderr: stderr.String()}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			resp.ExitCode = int32(ee.ExitCode())
		} else {
			resp.ExitCode = 1
			resp.Stderr += err.Error()
		}
	}
	return resp, nil
}

func (a *agent) logs(payload json.RawMessage) (agentproto.LogsResponse, error) {
	var req agentproto.LogsRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return agentproto.LogsResponse{}, err
	}
	a.mu.Lock()
	ctr := a.ctrs[req.ContainerID]
	a.mu.Unlock()
	if ctr == nil {
		return agentproto.LogsResponse{}, fmt.Errorf("not found")
	}
	return agentproto.LogsResponse{Stream: "stdout", Data: ctr.stdout.String() + ctr.stderr.String()}, nil
}

func (a *agent) mounts(payload json.RawMessage) error {
	var mounts []agentproto.Mount
	if err := json.Unmarshal(payload, &mounts); err != nil {
		return err
	}
	for _, m := range mounts {
		if err := ensureMount(m); err != nil {
			return err
		}
	}
	return nil
}

func ensureMount(m agentproto.Mount) error {
	if m.GuestPath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(m.GuestPath), 0755); err != nil {
		return err
	}
	switch m.Kind {
	case "tmpfs":
		if err := os.MkdirAll(m.GuestPath, 0755); err != nil {
			return err
		}
		return syscall.Mount("tmpfs", m.GuestPath, "tmpfs", 0, "")
	default:
		return os.MkdirAll(m.GuestPath, 0755)
	}
}
