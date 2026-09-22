package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

func runContainerInit() int {
	root, argv, err := parseContainerInitArgs(os.Args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if err := unix.Chroot(root); err != nil {
		fmt.Fprintln(os.Stderr, "chroot:", err)
		return 1
	}
	if err := os.Chdir("/"); err != nil {
		fmt.Fprintln(os.Stderr, "chdir:", err)
		return 1
	}
	if os.Getenv("MAROONED_SKIP_PROC") == "" {
		_ = unix.Unmount("/proc", unix.MNT_DETACH)
		if err := unix.Mount("proc", "/proc", "proc", 0, ""); err != nil {
			fmt.Fprintln(os.Stderr, "mount proc:", err)
			return 1
		}
	}
	if wd := os.Getenv("MAROONED_WORKDIR"); wd != "" {
		if err := os.Chdir(wd); err != nil {
			fmt.Fprintln(os.Stderr, "workdir:", err)
			return 1
		}
	}
	if err := unix.Exec(argv[0], argv, os.Environ()); err != nil {
		fmt.Fprintln(os.Stderr, "exec:", err)
		return 1
	}
	return 1
}

func parseContainerInitArgs(args []string) (root string, argv []string, err error) {
	if len(args) < 4 {
		return "", nil, fmt.Errorf("usage: marooned-agent container-init ROOT -- CMD...")
	}
	root = args[2]
	dash := -1
	for i, a := range args {
		if a == "--" {
			dash = i
			break
		}
	}
	if dash < 0 || dash+1 >= len(args) {
		return "", nil, fmt.Errorf("container-init: missing -- CMD")
	}
	return root, args[dash+1:], nil
}

func containerInitCmd(root string, argv, env []string) (*exec.Cmd, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	args := append([]string{"container-init", root, "--"}, argv...)
	cmd := exec.Command(self, args...)
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWPID,
		Setpgid:    true,
	}
	return cmd, nil
}

func nsenterExecCmd(hostPid int, root string, argv []string) *exec.Cmd {
	self, err := os.Executable()
	if err != nil {
		self = "/usr/local/bin/marooned-agent"
	}
	args := append([]string{"pidns-exec", strconv.Itoa(hostPid), root, "--"}, argv...)
	return exec.Command(self, args...)
}

func parsePidnsExecArgs(args []string) (hostPid int, root string, argv []string, err error) {
	if len(args) < 5 {
		return 0, "", nil, fmt.Errorf("usage: marooned-agent pidns-exec HOSTPID ROOT -- CMD...")
	}
	hostPid, err = strconv.Atoi(args[2])
	if err != nil {
		return 0, "", nil, err
	}
	root = args[3]
	dash := -1
	for i, a := range args {
		if a == "--" {
			dash = i
			break
		}
	}
	if dash < 0 || dash+1 >= len(args) {
		return 0, "", nil, fmt.Errorf("pidns-exec: missing -- CMD")
	}
	return hostPid, root, args[dash+1:], nil
}

// runPidnsExec joins the workload PID namespace then chroot+exec. busybox
// nsenter -p does not fork, so kill(1) ran in the VM namespace (init ignores
// SIGTERM) while ps read the container /proc.
func runPidnsExec() int {
	hostPid, root, argv, err := parsePidnsExecArgs(os.Args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	fd, err := unix.Open(fmt.Sprintf("/proc/%d/ns/pid", hostPid), unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open pid ns:", err)
		return 1
	}
	if err := unix.Setns(fd, unix.CLONE_NEWPID); err != nil {
		_ = unix.Close(fd)
		fmt.Fprintln(os.Stderr, "setns pid:", err)
		return 1
	}
	_ = unix.Close(fd)
	attr := &syscall.ProcAttr{
		Env:   os.Environ(),
		Files: []uintptr{0, 1, 2},
		Sys:   &syscall.SysProcAttr{Chroot: root},
	}
	pid, err := syscall.ForkExec(argv[0], argv, attr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fork/exec:", err)
		return 1
	}
	var w syscall.WaitStatus
	if _, err := syscall.Wait4(pid, &w, 0, nil); err != nil {
		fmt.Fprintln(os.Stderr, "wait:", err)
		return 1
	}
	if w.Exited() {
		return w.ExitStatus()
	}
	if w.Signaled() {
		return 128 + int(w.Signal())
	}
	return 1
}
