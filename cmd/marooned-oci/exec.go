package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
)

type ociProcess struct {
	Terminal bool     `json:"terminal"`
	Args     []string `json:"args"`
	Cwd      string   `json:"cwd"`
	Env      []string `json:"env"`
}

func loadExec(args []string) (id string, proc ociProcess, pidFile string) {
	processFile := ""
	tty := false
	needVal := map[string]bool{
		"--cwd": true, "--user": true, "--process": true, "--pid-file": true,
		"--apparmor": true, "--cgroup": true, "--preserve-fds": true,
		"-e": true, "--env": true,
	}
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--process" && i+1 < len(args):
			processFile = args[i+1]
			i++
		case strings.HasPrefix(a, "--process="):
			processFile = strings.TrimPrefix(a, "--process=")
		case a == "--pid-file" && i+1 < len(args):
			pidFile = args[i+1]
			i++
		case strings.HasPrefix(a, "--pid-file="):
			pidFile = strings.TrimPrefix(a, "--pid-file=")
		case a == "-t" || a == "--tty":
			tty = true
		case a == "--":
			rest = append(rest, args[i+1:]...)
			i = len(args)
		case strings.HasPrefix(a, "-"):
			name := a
			if j := strings.IndexByte(a, '='); j >= 0 {
				name = a[:j]
			} else if needVal[a] && i+1 < len(args) {
				i++
			}
			_ = name
		default:
			rest = append(rest, a)
		}
	}
	if len(rest) > 0 {
		id = rest[0]
		proc.Args = rest[1:]
	}
	if processFile != "" {
		if p, err := readOCIProcess(processFile); err == nil {
			if len(p.Args) > 0 {
				proc.Args = p.Args
			}
			proc.Terminal = proc.Terminal || p.Terminal
			if p.Cwd != "" {
				proc.Cwd = p.Cwd
			}
		}
	}
	if tty {
		proc.Terminal = true
	}
	_ = os.MkdirAll("/run/marooned-oci", 0755)
	dbg, _ := json.Marshal(map[string]interface{}{"id": id, "processFile": processFile, "pidFile": pidFile, "tty": proc.Terminal, "args": proc.Args})
	_ = os.WriteFile("/run/marooned-oci/last-exec.json", dbg, 0644)
	return id, proc, pidFile
}

func writeExecPidFile(path string) {
	if path == "" {
		return
	}
	_ = os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())+"\n"), 0644)
}

func readOCIProcess(path string) (ociProcess, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return ociProcess{}, err
	}
	var p ociProcess
	if err := json.Unmarshal(b, &p); err != nil {
		return ociProcess{}, err
	}
	if len(p.Args) == 0 {
		var wrap struct {
			Process ociProcess `json:"process"`
		}
		if err := json.Unmarshal(b, &wrap); err == nil && len(wrap.Process.Args) > 0 {
			return wrap.Process, nil
		}
	}
	return p, nil
}

func doExecTTY(id string, cmd []string) int {
	raw, _ := json.Marshal(map[string]interface{}{"id": id, "command": cmd, "timeout": 0})
	conn, err := net.Dial("unix", shimSock)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer conn.Close()
	req, err := http.NewRequest(http.MethodPost, "http://shim/v1/ExecTTY", strings.NewReader(string(raw)))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	req.Header.Set("Content-Type", "application/json")
	if err := req.Write(conn); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintln(os.Stderr, strings.TrimSpace(string(body)))
		return 1
	}
	// CRI-O's ttyCmd already put a host PTY on our stdio. Copy user→guest in
	// the background; wait for guest→user so a closed stdin does not kill the
	// shell (that was "exec -it does nothing").
	go func() {
		_, _ = io.Copy(conn, os.Stdin)
	}()
	_, _ = io.Copy(os.Stdout, br)
	return 0
}
