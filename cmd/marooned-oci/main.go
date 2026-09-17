// marooned-oci is a tiny OCI runtime CRI-O can invoke for handler "marooned".
// It keeps a dummy process for conmon, and forwards start/exec to marooned-shim.
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

const (
	defaultRoot = "/run/marooned-oci"
	shimSock    = "/var/run/marooned/cri.sock"
)

type state struct {
	OCIVersion string `json:"ociVersion"`
	ID         string `json:"id"`
	Status     string `json:"status"`
	PID        int    `json:"pid"`
	Bundle     string `json:"bundle"`
}

func main() {
	args := os.Args[1:]
	root := defaultRoot
	filtered := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--root" && i+1 < len(args) {
			root = args[i+1]
			i++
			continue
		}
		if strings.HasPrefix(a, "--root=") {
			root = strings.TrimPrefix(a, "--root=")
			continue
		}
		// CRI-O passes these; we ignore them.
		if a == "--systemd-cgroup" || a == "--log" || a == "--log-format" {
			if a != "--systemd-cgroup" && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
			}
			continue
		}
		filtered = append(filtered, a)
	}
	if len(filtered) < 1 {
		fatal("usage: marooned-oci create|start|state|kill|delete|exec ...")
	}
	cmd := filtered[0]
	rest := filtered[1:]
	switch cmd {
	case "create":
		os.Exit(doCreate(root, rest))
	case "start":
		os.Exit(doStart(root, rest))
	case "state":
		os.Exit(doState(root, rest))
	case "kill":
		os.Exit(doKill(root, rest))
	case "delete":
		os.Exit(doDelete(root, rest))
	case "exec":
		os.Exit(doExec(root, rest))
	default:
		fatal("unknown command %s", cmd)
	}
}

func doCreate(root string, args []string) int {
	bundle, id, pidFile := "", "", ""
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--bundle" && i+1 < len(args):
			bundle = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--bundle="):
			bundle = strings.TrimPrefix(args[i], "--bundle=")
		case args[i] == "--pid-file" && i+1 < len(args):
			pidFile = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--pid-file="):
			pidFile = strings.TrimPrefix(args[i], "--pid-file=")
		case !strings.HasPrefix(args[i], "-"):
			id = args[i]
		}
	}
	if id == "" {
		fatal("create: missing id")
	}
	dir := filepath.Join(root, id)
	_ = os.MkdirAll(dir, 0755)
	sleep := exec.Command("sleep", "infinity")
	sleep.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := sleep.Start(); err != nil {
		fatal("sleep: %v", err)
	}
	st := state{OCIVersion: "1.0.2", ID: id, Status: "created", PID: sleep.Process.Pid, Bundle: bundle}
	writeState(dir, st)
	if pidFile != "" {
		_ = os.WriteFile(pidFile, []byte(strconv.Itoa(st.PID)), 0644)
	}
	_ = os.WriteFile(filepath.Join(dir, "bundle"), []byte(bundle), 0644)
	ns, name, sandbox := podFromBundle(bundle)
	if ns != "" {
		_ = os.WriteFile(filepath.Join(dir, "pod"), []byte(ns+"/"+name), 0644)
	}
	if sandbox && ns != "" {
		go func() {
			_, _ = shimJSON("POST", "/v1/RunPodSandbox", map[string]string{
				"podName": name, "podNamespace": ns, "podUID": id,
			})
		}()
	}
	return 0
}

func doStart(root string, args []string) int {
	id := lastID(args)
	dir := filepath.Join(root, id)
	st := readState(dir)
	st.Status = "running"
	writeState(dir, st)
	bundle := strings.TrimSpace(string(mustRead(filepath.Join(dir, "bundle"))))
	argsCmd, _ := processArgs(bundle)
	pod := strings.TrimSpace(string(mustRead(filepath.Join(dir, "pod"))))
	if len(argsCmd) > 0 && pod != "" {
		go func() {
			cid := id
			_, _ = shimJSON("POST", "/v1/CreateContainer", map[string]interface{}{
				"sandboxID": pod,
				"container": map[string]interface{}{"name": "box", "command": argsCmd},
			})
			_, _ = shimJSON("POST", "/v1/StartContainer", map[string]string{"id": cid})
		}()
	}
	return 0
}

func doState(root string, args []string) int {
	id := lastID(args)
	st := readState(filepath.Join(root, id))
	enc := json.NewEncoder(os.Stdout)
	_ = enc.Encode(st)
	return 0
}

func doKill(root string, args []string) int {
	id := lastID(args)
	st := readState(filepath.Join(root, id))
	if st.PID > 1 {
		_ = syscall.Kill(st.PID, syscall.SIGKILL)
	}
	st.Status = "stopped"
	writeState(filepath.Join(root, id), st)
	return 0
}

func doDelete(root string, args []string) int {
	id := lastID(args)
	_ = os.RemoveAll(filepath.Join(root, id))
	return 0
}

func doExec(root string, args []string) int {
	id := lastID(args)
	var cmd []string
	for i, a := range args {
		if a == "--" && i+1 < len(args) {
			cmd = args[i+1:]
			break
		}
	}
	if len(cmd) == 0 {
		cmd = []string{"true"}
	}
	body, err := shimJSON("POST", "/v1/ExecSync", map[string]interface{}{
		"id": id, "command": cmd, "timeout": 30,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	var out struct {
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
		ExitCode int    `json:"exitCode"`
	}
	_ = json.Unmarshal(body, &out)
	fmt.Fprint(os.Stdout, out.Stdout)
	fmt.Fprint(os.Stderr, out.Stderr)
	return out.ExitCode
}

func lastID(args []string) string {
	id := ""
	skipVal := false
	for _, a := range args {
		if skipVal {
			skipVal = false
			continue
		}
		if a == "--" {
			break
		}
		if strings.HasPrefix(a, "-") {
			if !strings.Contains(a, "=") && a != "--systemd-cgroup" && a != "--force" {
				skipVal = true
			}
			continue
		}
		id = a
	}
	if id == "" {
		fatal("missing id")
	}
	return id
}

func writeState(dir string, st state) {
	_ = os.MkdirAll(dir, 0755)
	b, _ := json.Marshal(st)
	_ = os.WriteFile(filepath.Join(dir, "state.json"), b, 0644)
}

func readState(dir string) state {
	b, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		fatal("state: %v", err)
	}
	var st state
	_ = json.Unmarshal(b, &st)
	return st
}

func mustRead(p string) []byte {
	b, _ := os.ReadFile(p)
	return b
}

func podFromBundle(bundle string) (ns, name string, sandbox bool) {
	b, err := os.ReadFile(filepath.Join(bundle, "config.json"))
	if err != nil {
		return "", "", false
	}
	var cfg struct {
		Annotations map[string]string `json:"annotations"`
	}
	_ = json.Unmarshal(b, &cfg)
	ns = firstAnno(cfg.Annotations, "io.kubernetes.cri.sandbox-namespace", "io.kubernetes.pod.namespace")
	name = firstAnno(cfg.Annotations, "io.kubernetes.cri.sandbox-name", "io.kubernetes.pod.name")
	ct := firstAnno(cfg.Annotations, "io.kubernetes.cri-o.ContainerType", "io.kubernetes.cri.container-type")
	sandbox = ct == "sandbox"
	return ns, name, sandbox
}

func firstAnno(m map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := m[k]; v != "" {
			return v
		}
	}
	return ""
}

func processArgs(bundle string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(bundle, "config.json"))
	if err != nil {
		return nil, err
	}
	var cfg struct {
		Process struct {
			Args []string `json:"args"`
		} `json:"process"`
	}
	_ = json.Unmarshal(b, &cfg)
	return cfg.Process.Args, nil
}

func shimJSON(method, path string, payload interface{}) ([]byte, error) {
	var rdr io.Reader
	if payload != nil {
		b, _ := json.Marshal(payload)
		rdr = strings.NewReader(string(b))
	}
	req, err := http.NewRequest(method, "http://shim"+path, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{
		Transport: &http.Transport{
			Dial: func(network, addr string) (net.Conn, error) {
				return net.Dial("unix", shimSock)
			},
		},
		Timeout: 90 * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func fatal(f string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, f+"\n", a...)
	os.Exit(1)
}
