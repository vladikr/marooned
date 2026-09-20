package main

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"k8s.io/klog/v2"

	"maroonedpods.io/maroonedpods/pkg/sandbox/agentproto"
)

// execTTY streams stdio over the vsock. CRI-O already wrapped marooned-oci
// in a host PTY (pty.Start); a second guest PTY hid the prompt and hung.
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
	argv := append([]string{}, req.Command...)
	if len(argv) == 1 && (argv[0] == "/bin/sh" || argv[0] == "sh" || argv[0] == "/bin/ash" || argv[0] == "ash") {
		argv = []string{argv[0], "-i"}
	}
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	root := filepath.Join(ctrRoot, req.ContainerID, "root")
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdinR, stdoutW, stdoutW
	if st, err := os.Stat(root); err == nil && st.IsDir() {
		argv[0] = lookPathInRoot(root, argv[0], nil)
		cmd = exec.Command(argv[0], argv[1:]...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = stdinR, stdoutW, stdoutW
		cmd.Dir = "/"
		cmd.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "TERM=xterm", "PS1=# "}
		cmd.SysProcAttr = &syscall.SysProcAttr{Chroot: root}
	}
	klog.Infof("exec tty %s argv=%v", req.ContainerID, argv)
	if err := cmd.Start(); err != nil {
		_ = stdinR.Close()
		_ = stdoutW.Close()
		fail(err)
		return
	}
	if err := agentproto.WriteEnvelope(conn, agentproto.Envelope{ID: env.ID, Method: env.Method, OK: true}); err != nil {
		_ = cmd.Process.Kill()
		return
	}
	go func() {
		_, _ = io.Copy(stdinW, conn)
		_ = stdinW.Close()
	}()
	_, _ = io.Copy(conn, stdoutR)
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	_ = stdoutW.Close()
}
