package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// startGuestWait kills the host pause process when the guest workload
// exits so CRI-O/kubelet stop reporting Running.
func startGuestWait(root, ociID string) {
	go func() {
		dir := filepath.Join(root, ociID)
		criID := strings.TrimSpace(string(mustRead(filepath.Join(dir, "criid"))))
		if criID == "" {
			return
		}
		_ = shimJSONWait("/v1/WaitContainer", map[string]string{"id": criID})
		st, err := readState(dir)
		if err == nil && st.PID > 1 {
			_ = syscall.Kill(st.PID, syscall.SIGTERM)
			time.Sleep(200 * time.Millisecond)
			_ = syscall.Kill(st.PID, syscall.SIGKILL)
		}
		if st, err := readState(dir); err == nil {
			st.Status = "stopped"
			writeState(dir, st)
		}
	}()
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
