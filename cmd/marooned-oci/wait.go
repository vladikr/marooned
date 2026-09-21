package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// startGuestWait forks a helper (start itself exits; a goroutine would die).
func startGuestWait(root, ociID string) {
	self, err := os.Executable()
	if err != nil {
		self = os.Args[0]
	}
	cmd := exec.Command(self, "--root", root, "guest-wait", ociID)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout = nil
	cmd.Stderr = nil
	_ = cmd.Start()
	if cmd.Process != nil {
		_ = cmd.Process.Release()
	}
}

func doGuestWait(root string, args []string) int {
	id := lastID(args)
	dir := filepath.Join(root, id)
	criID := strings.TrimSpace(string(mustRead(filepath.Join(dir, "criid"))))
	if criID == "" {
		return 1
	}
	_ = shimJSONWait("/v1/WaitContainer", map[string]string{"id": criID})
	st, err := readState(dir)
	if err == nil && st.PID > 1 {
		_ = exec.Command("systemctl", "stop", pauseUnit(id)).Run()
		_ = syscall.Kill(st.PID, syscall.SIGTERM)
		time.Sleep(200 * time.Millisecond)
		_ = syscall.Kill(st.PID, syscall.SIGKILL)
	}
	if st, err := readState(dir); err == nil {
		st.Status = "stopped"
		writeState(dir, st)
	}
	return 0
}

func shimJSONWait(path string, payload interface{}) error {
	b, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, "http://shim"+path, strings.NewReader(string(b)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{
		Transport: &http.Transport{
			DisableKeepAlives: true,
			Dial: func(network, addr string) (net.Conn, error) {
				return net.Dial("unix", shimSock)
			},
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return err
	}
	return nil
}
