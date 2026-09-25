package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// CRI-O watches this dir (conmon --exit-dir). Writing <id> with the
// exit code is what flips the Pod off Running. Killing our pause PID
// is not enough: pause is systemd-run/Setsid, so conmon is not its
// parent and never waitpid()s it.
var crioExitDirs = []string{"/var/run/crio/exits", "/run/crio/exits"}

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
	gwlog(dir, "wait "+criID)
	_ = shimJSONWait("/v1/WaitContainer", map[string]string{"id": criID})
	code := guestExitCode(criID)
	gwlog(dir, fmt.Sprintf("guest exited code=%d", code))
	finishOCIContainer(dir, id, code)
	gwlog(dir, "notified crio exit")
	return 0
}

// finishOCIContainer records the guest exit and SIGTERMs the host pause so
// it exits with that code. Do not systemctl-stop/SIGKILL: that made conmon
// report -1 and a zero FinishedAt, so init never went Succeeded.
func finishOCIContainer(dir, id string, code int) {
	_ = os.WriteFile(filepath.Join(dir, "guest-exited"), []byte("1"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "exitcode"), []byte(strconv.Itoa(code)+"\n"), 0644)
	if err := writeCrioExit(id, code); err != nil {
		gwlog(dir, "crio exit file: "+err.Error())
	}
	st, err := readState(dir)
	if err == nil {
		st.Status = "stopped"
		writeState(dir, st)
	}
	if st.PID > 1 {
		_ = syscall.Kill(st.PID, syscall.SIGTERM)
	}
}

func writeCrioExit(id string, code int) error {
	body := []byte(strconv.Itoa(code) + "\n")
	var last error
	wrote := false
	for _, dir := range crioExitDirs {
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, id), body, 0644); err != nil {
			last = err
			continue
		}
		wrote = true
	}
	if wrote {
		return nil
	}
	if last != nil {
		return last
	}
	return fmt.Errorf("no crio exit dir")
}

func pauseExitCode(dir string) int {
	if dir == "" {
		return 0
	}
	b, err := os.ReadFile(filepath.Join(dir, "exitcode"))
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0
	}
	return n
}

func guestExitCode(criID string) int {
	body, err := shimJSON("POST", "/v1/ContainerStatus", map[string]string{"id": criID})
	if err != nil {
		return 1
	}
	var st struct {
		ExitCode int32 `json:"ExitCode"`
	}
	if err := json.Unmarshal(body, &st); err != nil {
		return 1
	}
	return int(st.ExitCode)
}

func gwlog(dir, msg string) {
	f, err := os.OpenFile(filepath.Join(dir, "guest-wait.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "%s %s\n", time.Now().UTC().Format(time.RFC3339), msg)
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
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}
