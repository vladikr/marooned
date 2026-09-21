package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func startGuestStart(root, ociID string) {
	self, err := os.Executable()
	if err != nil {
		self = os.Args[0]
	}
	cmd := exec.Command(self, "--root", root, "guest-start", ociID)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout = nil
	cmd.Stderr = nil
	_ = cmd.Start()
	if cmd.Process != nil {
		_ = cmd.Process.Release()
	}
}

func doGuestStart(root string, args []string) int {
	id := lastID(args)
	dir := filepath.Join(root, id)
	if err := runGuestStart(root, id, dir); err != nil {
		gslog(dir, err.Error())
		_ = os.WriteFile(filepath.Join(dir, "guest-exited"), []byte("1"), 0644)
		stopHostPause(dir, id)
		writeCrioExit(id, 1)
		if st, err := readState(dir); err == nil {
			st.Status = "stopped"
			writeState(dir, st)
		}
		return 1
	}
	return 0
}

func runGuestStart(root, id, dir string) error {
	bundle := strings.TrimSpace(string(mustRead(filepath.Join(dir, "bundle"))))
	spec := readProcessSpec(bundle)
	podUID := strings.TrimSpace(string(mustRead(filepath.Join(dir, "poduid"))))
	ctrName := strings.TrimSpace(string(mustRead(filepath.Join(dir, "ctrname"))))
	if ctrName == "" {
		ctrName = "box"
	}
	if len(spec.Args) == 0 || podUID == "" {
		return fmt.Errorf("missing process spec or pod uid")
	}
	pod := strings.TrimSpace(string(mustRead(filepath.Join(dir, "pod"))))
	ns, name, _ := strings.Cut(pod, "/")
	gslog(dir, "RunPodSandbox "+ns+"/"+name)
	if ns != "" && name != "" {
		hint := map[string]interface{}{
			"podName": name, "podNamespace": ns, "podUID": podUID,
		}
		if spec.Root != "" {
			hint["rootfsBytes"] = dirSize(spec.Root)
		}
		if _, err := shimJSON("POST", "/v1/RunPodSandbox", hint); err != nil {
			return fmt.Errorf("RunPodSandbox: %w", err)
		}
	}
	criID := podUID + "-" + ctrName
	_ = os.WriteFile(filepath.Join(dir, "criid"), []byte(criID), 0644)
	ctr := map[string]interface{}{"name": ctrName, "command": spec.Args, "env": spec.Env, "workDir": spec.Cwd}
	if spec.Root != "" {
		ctr["rootfsBytes"] = dirSize(spec.Root)
		tarPath := filepath.Join("/var/run/marooned", podUID, "rootfs-"+ctrName+".tar")
		if err := tarDirectory(spec.Root, tarPath); err != nil {
			return fmt.Errorf("tar rootfs: %w", err)
		}
		ctr["rootfsPath"] = tarPath
	}
	if _, err := shimJSON("POST", "/v1/CreateContainer", map[string]interface{}{
		"sandboxID": podUID,
		"container": ctr,
	}); err != nil {
		return fmt.Errorf("CreateContainer: %w", err)
	}
	gslog(dir, "StartContainer "+criID)
	if _, err := shimJSON("POST", "/v1/StartContainer", map[string]string{"id": criID}); err != nil {
		return fmt.Errorf("StartContainer: %w", err)
	}
	_ = os.WriteFile(filepath.Join(dir, "guest-started"), []byte("1"), 0644)
	ns, pname, _ := strings.Cut(pod, "/")
	appendK8sLog(currentPodLog(ns, pname, podUID, ctrName), "marooned: guest container started")
	startLogPump(root, id)
	startGuestWait(root, id)
	gslog(dir, "guest running")
	return nil
}

func gslog(dir, msg string) {
	f, err := os.OpenFile(filepath.Join(dir, "guest-start.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "%s %s\n", time.Now().UTC().Format(time.RFC3339), msg)
}
