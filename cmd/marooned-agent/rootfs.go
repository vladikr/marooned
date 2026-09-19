package main

import (
	"archive/tar"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"maroonedpods.io/maroonedpods/pkg/sandbox/agentproto"
)

const ctrRoot = "/var/lib/marooned"

func (a *agent) rootfs(payload []byte) error {
	var req agentproto.RootfsChunk
	if err := json.Unmarshal(payload, &req); err != nil {
		return err
	}
	if req.ContainerID == "" {
		return fmt.Errorf("containerID required")
	}
	dir := filepath.Join(ctrRoot, req.ContainerID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tarPath := filepath.Join(dir, "rootfs.tar")
	a.mu.Lock()
	f := a.tarFiles[req.ContainerID]
	a.mu.Unlock()
	if f == nil {
		var err error
		f, err = os.OpenFile(tarPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			return err
		}
		a.mu.Lock()
		a.tarFiles[req.ContainerID] = f
		a.mu.Unlock()
	}
	if len(req.Data) > 0 {
		if _, err := f.Write(req.Data); err != nil {
			return err
		}
	}
	if !req.EOF {
		return nil
	}
	_ = f.Close()
	a.mu.Lock()
	delete(a.tarFiles, req.ContainerID)
	a.mu.Unlock()
	dest := filepath.Join(dir, "rootfs")
	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}
	return unpackTar(tarPath, dest)
}

func unpackTar(tarPath, dest string) error {
	f, err := os.Open(tarPath)
	if err != nil {
		return err
	}
	defer f.Close()
	tr := tar.NewReader(f)
	dest = filepath.Clean(dest)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target := filepath.Join(dest, hdr.Name)
		if !strings.HasPrefix(filepath.Clean(target)+string(os.PathSeparator), dest+string(os.PathSeparator)) && filepath.Clean(target) != dest {
			return fmt.Errorf("tar path escapes root: %s", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(out, tr)
			_ = out.Close()
			if copyErr != nil {
				return copyErr
			}
		case tar.TypeSymlink:
			_ = os.Remove(target)
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		case tar.TypeLink:
			_ = os.Remove(target)
			if err := os.Link(filepath.Join(dest, hdr.Linkname), target); err != nil {
				return err
			}
		}
	}
}

func lookPathInRoot(root, name string, env []string) string {
	if filepath.IsAbs(name) {
		return name
	}
	pathEnv := "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			pathEnv = strings.TrimPrefix(e, "PATH=")
			break
		}
	}
	for _, dir := range strings.Split(pathEnv, ":") {
		if dir == "" {
			continue
		}
		host := filepath.Join(root, dir, name)
		if st, err := os.Stat(host); err == nil && !st.IsDir() {
			return filepath.Join(dir, name)
		}
	}
	return name
}

func prepareChroot(root string) error {
	for _, d := range []string{"proc", "sys", "dev", "tmp"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0755); err != nil {
			return err
		}
	}
	_ = syscall.Mount("proc", filepath.Join(root, "proc"), "proc", 0, "")
	_ = syscall.Mount("sysfs", filepath.Join(root, "sys"), "sysfs", 0, "")
	_ = syscall.Mount("/dev", filepath.Join(root, "dev"), "", syscall.MS_BIND, "")
	_ = syscall.Mount("tmpfs", filepath.Join(root, "tmp"), "tmpfs", 0, "")
	return nil
}

func startInRoot(root string, req agentproto.StartRequest) (*exec.Cmd, error) {
	argv := append(append([]string{}, req.Command...), req.Args...)
	if len(argv) == 0 {
		argv = []string{"/bin/sh", "-c", "sleep infinity"}
	}
	argv[0] = lookPathInRoot(root, argv[0], req.Env)
	cmd := exec.Command(argv[0], argv[1:]...)
	cwd := req.WorkDir
	if cwd == "" {
		cwd = "/"
	}
	cmd.Dir = cwd
	if len(req.Env) > 0 {
		cmd.Env = req.Env
	} else {
		cmd.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}
	}
	if err := prepareChroot(root); err != nil {
		return nil, err
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Chroot: root}
	return cmd, nil
}
