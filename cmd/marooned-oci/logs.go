package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func startLogPump(root, ociID string) {
	self, err := os.Executable()
	if err != nil {
		self = os.Args[0]
	}
	cmd := exec.Command(self, "--root", root, "log-pump", ociID)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout = nil
	cmd.Stderr = nil
	_ = cmd.Start()
	if cmd.Process != nil {
		_ = cmd.Process.Release()
	}
}

func doLogPump(root string, args []string) int {
	id := lastID(args)
	criID := strings.TrimSpace(string(mustRead(filepath.Join(root, id, "criid"))))
	if criID == "" {
		criID = id
	}
	pod := strings.TrimSpace(string(mustRead(filepath.Join(root, id, "pod"))))
	uid := strings.TrimSpace(string(mustRead(filepath.Join(root, id, "poduid"))))
	ctr := strings.TrimSpace(string(mustRead(filepath.Join(root, id, "ctrname"))))
	if ctr == "" {
		ctr = "box"
	}
	ns, name, _ := strings.Cut(pod, "/")
	logPath := filepath.Join("/var/log/pods", ns+"_"+name+"_"+uid, ctr, "0.log")
	_ = os.MkdirAll(filepath.Dir(logPath), 0755)
	seen := 0
	for {
		if !pidAlive(readStatePID(root, id)) {
			return 0
		}
		body, err := shimJSON("POST", "/v1/Logs", map[string]string{"id": criID})
		if err == nil {
			var out struct {
				Data string `json:"data"`
			}
			_ = json.Unmarshal(body, &out)
			if len(out.Data) > seen {
				chunk := out.Data[seen:]
				seen = len(out.Data)
				appendK8sLog(logPath, chunk)
			}
		}
		time.Sleep(400 * time.Millisecond)
	}
}

func readStatePID(root, id string) int {
	st, err := readState(filepath.Join(root, id))
	if err != nil {
		return 0
	}
	return st.PID
}

func appendK8sLog(path, data string) {
	if data == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	ts := time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z")
	for _, line := range strings.Split(strings.TrimSuffix(data, "\n"), "\n") {
		_, _ = fmt.Fprintf(f, "%s stdout F %s\n", ts, line)
	}
}
