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

func (a *agent) prepareRootfs(payload []byte) (agentproto.PrepareRootfsResponse, error) {
	var req agentproto.PrepareRootfsRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return agentproto.PrepareRootfsResponse{}, err
	}
	if req.ContainerID == "" {
		return agentproto.PrepareRootfsResponse{}, fmt.Errorf("containerID required")
	}
	serial := req.Serial
	if serial == "" {
		serial = "userrootfs"
	}
	if err := os.MkdirAll(ctrRoot, 0755); err != nil {
		return agentproto.PrepareRootfsResponse{}, err
	}
	if !isMountPoint(ctrRoot) {
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
			return agentproto.PrepareRootfsResponse{}, fmt.Errorf("user-rootfs disk serial %q: %v", serial, last)
		}
		if !hasExtSuperblock(dev) {
			if err := mkfs(dev); err != nil {
				return agentproto.PrepareRootfsResponse{}, fmt.Errorf("mkfs %s (image %d bytes, disk %d bytes): %w", dev, req.ImageBytes, req.DiskBytes, err)
			}
		}
		if err := syscall.Mount(dev, ctrRoot, "ext4", 0, ""); err != nil {
			if err2 := syscall.Mount(dev, ctrRoot, "ext2", 0, ""); err2 != nil {
				if !isMountPoint(ctrRoot) {
					return agentproto.PrepareRootfsResponse{}, fmt.Errorf("mount %s on %s: %v; %v", dev, ctrRoot, err, err2)
				}
			}
		}
	}
	dest := rootfsDest(req.ContainerName, req.ContainerID)
	if err := os.MkdirAll(dest, 0755); err != nil {
		return agentproto.PrepareRootfsResponse{}, err
	}
	pop := rootfsPopulated(dest)
	a.mu.Lock()
	a.imageBytes[req.ContainerID] = req.ImageBytes
	a.diskBytes[req.ContainerID] = req.DiskBytes
	a.roots[req.ContainerID] = dest
	a.mu.Unlock()
	return agentproto.PrepareRootfsResponse{Populated: pop, Path: dest}, nil
}

func (a *agent) containerDir(id string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if d := a.roots[id]; d != "" {
		return d
	}
	return filepath.Join(ctrRoot, id, "root")
}

func rootfsDest(name, id string) string {
	if rootfsPopulated(ctrRoot) {
		return ctrRoot
	}
	key := name
	if key == "" {
		key = id
	}
	return filepath.Join(ctrRoot, key, "root")
}

func rootfsPopulated(dir string) bool {
	for _, rel := range []string{"bin/sh", "bin/bash", "bin/busybox", "usr/bin/sh"} {
		st, err := os.Lstat(filepath.Join(dir, rel))
		if err == nil && !st.IsDir() {
			return true
		}
	}
	return false
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
	// user-rootfs emptyDisk is vdb when serial is missing (old guests).
	if want == "userrootfs" {
		if _, err := os.Stat("/dev/vdb"); err == nil {
			return "/dev/vdb", nil
		}
	}
	return "", fmt.Errorf("not found")
}

func isMountPoint(path string) bool {
	var st, pst syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return false
	}
	if err := syscall.Stat(filepath.Dir(path), &pst); err != nil {
		return false
	}
	return st.Dev != pst.Dev
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
