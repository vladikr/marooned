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
	id     string
	cmd    *exec.Cmd
	stdout bytes.Buffer
	stderr bytes.Buffer
	mu     sync.Mutex
	exited bool
	code   int32
}

type agent struct {
	mu   sync.Mutex
	ctrs map[string]*container
}

func main() {
	klog.InitFlags(nil)
	listen := flag.String("listen", "vsock://:1024", "listen address: vsock://:port, tcp://host:port, or unix:///path")
	flag.Parse()

	a := &agent{ctrs: map[string]*container{}}
	ln, err := vsock.Listen(*listen)
	if err != nil {
		klog.Warningf("listen %s: %v; falling back to tcp://0.0.0.0:1024", *listen, err)
		ln, err = vsock.Listen("tcp://0.0.0.0:1024")
	}
	if err != nil {
		klog.Fatalf("listen: %v", err)
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
	for {
		env, err := agentproto.ReadEnvelope(conn)
		if err != nil {
			if err != io.EOF {
				klog.V(4).Infof("read: %v", err)
			}
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
	case agentproto.MethodStart:
		err = a.start(env.Payload)
	case agentproto.MethodStop:
		err = a.stop(env.Payload)
	case agentproto.MethodWait:
		err = a.wait(env.Payload)
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
	argv := append(append([]string{}, req.Command...), req.Args...)
	if len(argv) == 0 {
		argv = []string{"/bin/sh", "-c", "sleep infinity"}
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	if req.WorkDir != "" {
		cmd.Dir = req.WorkDir
	}
	cmd.Env = append(os.Environ(), req.Env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	ctr := &container{id: req.ContainerID, cmd: cmd}
	cmd.Stdout = &ctr.stdout
	cmd.Stderr = &ctr.stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	a.mu.Lock()
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
	}()
	return nil
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
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		a.mu.Lock()
		ctr := a.ctrs[req.ContainerID]
		a.mu.Unlock()
		if ctr == nil {
			return fmt.Errorf("not found")
		}
		ctr.mu.Lock()
		done := ctr.exited
		ctr.mu.Unlock()
		if done {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("timeout")
}

func (a *agent) exec(payload json.RawMessage) (agentproto.ExecResponse, error) {
	var req agentproto.ExecRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return agentproto.ExecResponse{}, err
	}
	if len(req.Command) == 0 {
		return agentproto.ExecResponse{ExitCode: 1, Stderr: "empty command"}, nil
	}
	cmd := exec.Command(req.Command[0], req.Command[1:]...)
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
