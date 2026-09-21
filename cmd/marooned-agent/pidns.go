package main

import (
	"fmt"
	"os"
	"os/exec"
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
	tail := append([]string{"-t", strconv.Itoa(hostPid), "-p", "--", "/usr/local/bin/marooned-agent", "container-init", root, "--"}, argv...)
	if _, err := os.Stat("/usr/bin/nsenter"); err == nil {
		return exec.Command("/usr/bin/nsenter", tail...)
	}
	return exec.Command("/bin/busybox", append([]string{"nsenter"}, tail...)...)
}
