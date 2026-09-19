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
	"os/signal"
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
	case "pause-daemon":
		os.Exit(runPauseDaemon())
	case "pause":
		// Do not use select{}: the runtime treats "all goroutines asleep"
		// as a deadlock and exits 2. Block on SIGTERM so CRI-O kill works.
		signal.Ignore(syscall.SIGHUP, syscall.SIGPIPE)
		detachFromRuntimeCgroup(os.Getpid())
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
		<-ch
		os.Exit(0)
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
	pid := startPause(id)
	st := state{OCIVersion: "1.0.2", ID: id, Status: "created", PID: pid, Bundle: bundle}
	writeState(dir, st)
	if pidFile != "" {
		_ = os.WriteFile(pidFile, []byte(strconv.Itoa(st.PID)), 0644)
		_ = os.WriteFile(filepath.Join(dir, "pidfile"), []byte(pidFile), 0644)
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
	st, err := readState(dir)
	if err != nil {
		fatal("start: %v", err)
	}
	if st.PID <= 1 || !pidAlive(st.PID) {
		st.PID = startPause(id)
		if pf := strings.TrimSpace(string(mustRead(filepath.Join(dir, "pidfile")))); pf != "" {
			_ = os.WriteFile(pf, []byte(strconv.Itoa(st.PID)), 0644)
		}
	}
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
	st, err := readState(filepath.Join(root, id))
	if err != nil {
		fatal("state: %v", err)
	}
	if st.PID > 1 && !pidAlive(st.PID) {
		st.Status = "stopped"
	}
	enc := json.NewEncoder(os.Stdout)
	_ = enc.Encode(st)
	return 0
}

func doKill(root string, args []string) int {
	id, sig, all := parseKillArgs(args)
	dir := filepath.Join(root, id)
	st, err := readState(dir)
	if err != nil {
		return 0
	}
	_ = exec.Command("systemctl", "stop", pauseUnit(id)).Run()
	if st.PID > 1 {
		target := st.PID
		if all {
			target = -st.PID
		}
		if err := syscall.Kill(target, sig); err != nil && err != syscall.ESRCH {
			if all {
				_ = syscall.Kill(st.PID, sig)
			}
		}
	}
	st.Status = "stopped"
	writeState(dir, st)
	return 0
}

func doDelete(root string, args []string) int {
	id := lastID(args)
	dir := filepath.Join(root, id)
	_ = exec.Command("systemctl", "stop", pauseUnit(id)).Run()
	st, err := readState(dir)
	if err == nil && st.PID > 1 && pidAlive(st.PID) {
		_ = syscall.Kill(-st.PID, syscall.SIGKILL)
		_ = syscall.Kill(st.PID, syscall.SIGKILL)
	}
	_ = os.RemoveAll(dir)
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
	id := firstPositional(args)
	if id == "" {
		fatal("missing id")
	}
	return id
}

// parseKillArgs implements runc's kill CLI: kill [-a] <id> [<signal>]
// CRI-O calls: marooned-oci kill <container-id> 15
func parseKillArgs(args []string) (id string, sig syscall.Signal, all bool) {
	sig = syscall.SIGTERM
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-a" || a == "--all":
			all = true
		case a == "--force" || a == "--systemd-cgroup":
		case strings.HasPrefix(a, "-"):
			if !strings.Contains(a, "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
			}
		default:
			pos = append(pos, a)
		}
	}
	if len(pos) == 0 {
		fatal("kill: missing id")
	}
	id = pos[0]
	if len(pos) > 1 {
		sig = parseSignal(pos[1])
	}
	return id, sig, all
}

func firstPositional(args []string) string {
	skipVal := false
	id := ""
	for _, a := range args {
		if skipVal {
			skipVal = false
			continue
		}
		if a == "--" {
			break
		}
		if strings.HasPrefix(a, "-") {
			if !strings.Contains(a, "=") && a != "--systemd-cgroup" && a != "--force" && a != "-a" && a != "--all" {
				skipVal = true
			}
			continue
		}
		if id == "" {
			id = a
		}
	}
	return id
}

func parseSignal(s string) syscall.Signal {
	s = strings.TrimPrefix(strings.ToUpper(s), "SIG")
	if n, err := strconv.Atoi(s); err == nil {
		return syscall.Signal(n)
	}
	switch s {
	case "KILL":
		return syscall.SIGKILL
	case "TERM":
		return syscall.SIGTERM
	case "INT":
		return syscall.SIGINT
	case "HUP":
		return syscall.SIGHUP
	case "QUIT":
		return syscall.SIGQUIT
	default:
		return syscall.SIGTERM
	}
}

func pidAlive(pid int) bool {
	if pid <= 1 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

func startPause(id string) int {
	if pid := startPauseSystemd(id); pid > 1 && pidAlive(pid) {
		return pid
	}
	return startPauseFork()
}

func pauseUnit(id string) string {
	return "marooned-oci-" + id + ".service"
}

func startPauseSystemd(id string) int {
	self, err := os.Executable()
	if err != nil {
		self = os.Args[0]
	}
	unit := pauseUnit(id)
	_ = exec.Command("systemctl", "stop", unit).Run()
	cmd := exec.Command("systemd-run", "--unit="+unit, "--collect", self, "pause")
	if err := cmd.Run(); err != nil {
		return 0
	}
	for i := 0; i < 20; i++ {
		out, err := exec.Command("systemctl", "show", "-p", "MainPID", "--value", unit).Output()
		if err == nil {
			pid, _ := strconv.Atoi(strings.TrimSpace(string(out)))
			if pid > 1 && pidAlive(pid) {
				return pid
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return 0
}

func startPauseFork() int {
	self, err := os.Executable()
	if err != nil {
		self = os.Args[0]
	}
	cmd := exec.Command(self, "pause-daemon")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	out, err := cmd.Output()
	if err != nil {
		fatal("pause-daemon: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || pid <= 1 {
		fatal("pause-daemon pid %q", strings.TrimSpace(string(out)))
	}
	detachFromRuntimeCgroup(pid)
	if !pidAlive(pid) {
		fatal("pause pid %d not alive after start", pid)
	}
	return pid
}

func runPauseDaemon() int {
	self, err := os.Executable()
	if err != nil {
		self = os.Args[0]
	}
	inner := exec.Command(self, "pause")
	inner.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	inner.Stdin = nil
	inner.Stdout = nil
	inner.Stderr = nil
	if err := inner.Start(); err != nil {
		fatal("pause: %v", err)
	}
	pid := inner.Process.Pid
	_ = inner.Process.Release()
	detachFromRuntimeCgroup(pid)
	fmt.Println(pid)
	return 0
}

// Move the dummy out of the runtime's transient systemd cgroup so it
// survives marooned-oci create exiting.
func detachFromRuntimeCgroup(pid int) {
	if pid <= 1 {
		return
	}
	b := []byte(strconv.Itoa(pid))
	for _, p := range []string{"/sys/fs/cgroup/cgroup.procs", "/sys/fs/cgroup/unified/cgroup.procs"} {
		if err := os.WriteFile(p, b, 0644); err == nil {
			return
		}
	}
}

func writeState(dir string, st state) {
	_ = os.MkdirAll(dir, 0755)
	b, _ := json.Marshal(st)
	_ = os.WriteFile(filepath.Join(dir, "state.json"), b, 0644)
}

func readState(dir string) (state, error) {
	b, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		return state{}, err
	}
	var st state
	_ = json.Unmarshal(b, &st)
	return st, nil
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
