package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

var virtLauncherBins = []string{
	"/usr/local/bin/virt-launcher",
	"/opt/marooned/virt-launcher",
	"/usr/bin/virt-launcher",
}

func findVirtLauncher() string {
	for _, p := range virtLauncherBins {
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0111 != 0 {
			return p
		}
	}
	return ""
}

func standaloneVMISpec(dir, bundle string) string {
	if b := strings.TrimSpace(string(mustRead(filepath.Join(dir, "standalone-vmi.json")))); b != "" {
		return b
	}
	if b := strings.TrimSpace(string(mustRead(filepath.Join(dir, "standalone-vmi.yaml")))); b != "" {
		return b
	}
	if bundle == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(bundle, "config.json"))
	if err != nil {
		return ""
	}
	var cfg struct {
		Annotations map[string]string `json:"annotations"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return ""
	}
	for _, k := range []string{
		"maroonedpods.io/standalone-vmi",
		"io.kubernetes.cri-o.Annotations/maroonedpods.io/standalone-vmi",
	} {
		if v := strings.TrimSpace(cfg.Annotations[k]); v != "" {
			return v
		}
	}
	return ""
}

// startVirtLauncherPause runs KubeVirt virt-launcher --standalone as the
// CRI-O pause PID so qemu lives in the user Pod cgroup. Returns 0 when the
// binary or STANDALONE_VMI spec is missing (caller falls back to dummy pause).
func startVirtLauncherPause(id, dir string) int {
	bin := findVirtLauncher()
	if bin == "" {
		return 0
	}
	bundle := strings.TrimSpace(string(mustRead(filepath.Join(dir, "bundle"))))
	spec := standaloneVMISpec(dir, bundle)
	if spec == "" {
		return 0
	}
	logf := filepath.Join(dir, "virt-launcher.log")
	log, err := os.OpenFile(logf, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log = nil
	}
	cmd := exec.Command(bin, "--standalone")
	cmd.Env = append(os.Environ(), "STANDALONE_VMI="+spec)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if log != nil {
		cmd.Stdout = log
		cmd.Stderr = log
	}
	if err := cmd.Start(); err != nil {
		if log != nil {
			_, _ = log.WriteString("start: " + err.Error() + "\n")
			_ = log.Close()
		}
		return 0
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	if log != nil {
		_ = log.Close()
	}
	if pid <= 1 || !pidAlive(pid) {
		return 0
	}
	detachFromRuntimeCgroup(pid)
	_ = os.WriteFile(filepath.Join(dir, "virt-launcher.pid"), []byte(strconv.Itoa(pid)), 0644)
	_ = id
	return pid
}
