package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"maroonedpods.io/maroonedpods/pkg/sandbox/agentproto"
)

func (a *agent) prepareRootfs(payload []byte) error {
	var req agentproto.PrepareRootfsRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return err
	}
	if req.ContainerID == "" {
		return fmt.Errorf("containerID required")
	}
	serial := req.Serial
	if serial == "" {
		serial = "userrootfs"
	}
	dest := filepath.Join(ctrRoot, req.ContainerID, "root")
	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}
	var dev string
	var last error
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		dev, last = findDiskBySerial(serial)
		if last == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if dev == "" {
		return fmt.Errorf("user-rootfs disk serial %q: %v", serial, last)
	}
	if !hasExtSuperblock(dev) {
		if err := mkfs(dev); err != nil {
			return fmt.Errorf("mkfs %s (image %d bytes, disk %d bytes): %w", dev, req.ImageBytes, req.DiskBytes, err)
		}
	}
	if err := syscall.Mount(dev, dest, "ext4", 0, ""); err != nil {
		if err2 := syscall.Mount(dev, dest, "ext2", 0, ""); err2 != nil {
			return fmt.Errorf("mount %s on %s: %v; %v", dev, dest, err, err2)
		}
	}
	a.mu.Lock()
	a.imageBytes[req.ContainerID] = req.ImageBytes
	a.diskBytes[req.ContainerID] = req.DiskBytes
	a.mu.Unlock()
	return nil
}

func findDiskBySerial(want string) (string, error) {
	ents, err := os.ReadDir("/sys/block")
	if err != nil {
		return "", err
	}
	for _, e := range ents {
		name := e.Name()
		if !strings.HasPrefix(name, "vd") && !strings.HasPrefix(name, "sd") && !strings.HasPrefix(name, "xvd") {
			continue
		}
		for _, p := range []string{
			filepath.Join("/sys/block", name, "serial"),
			filepath.Join("/sys/block", name, "device", "serial"),
		} {
			b, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			if strings.TrimSpace(string(b)) == want {
				return "/dev/" + name, nil
			}
		}
	}
	if _, err := os.Stat("/dev/vdb"); err == nil {
		return "/dev/vdb", nil
	}
	return "", fmt.Errorf("not found")
}

func hasExtSuperblock(dev string) bool {
	f, err := os.Open(dev)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 2)
	if _, err := f.ReadAt(buf, 1080); err != nil {
		return false
	}
	return bytes.Equal(buf, []byte{0x53, 0xef})
}

func mkfs(dev string) error {
	cmds := [][]string{
		{"/bin/busybox", "mkfs.ext2", "-F", dev},
		{"mkfs.ext2", "-F", dev},
		{"mkfs.ext4", "-F", dev},
		{"mke2fs", "-t", "ext4", "-F", dev},
	}
	var last error
	for _, argv := range cmds {
		cmd := exec.Command(argv[0], argv[1:]...)
		out, err := cmd.CombinedOutput()
		if err == nil {
			return nil
		}
		last = fmt.Errorf("%s: %v (%s)", argv[0], err, bytes.TrimSpace(out))
	}
	return last
}
