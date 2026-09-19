package main

import (
	"archive/tar"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"maroonedpods.io/maroonedpods/pkg/sandbox/agentproto"
)

const ctrRoot = "/run/marooned"

type unpackJob struct {
	w   *io.PipeWriter
	err chan error
}

func (a *agent) rootfs(payload []byte) error {
	var req agentproto.RootfsChunk
	if err := json.Unmarshal(payload, &req); err != nil {
		return err
	}
	if req.ContainerID == "" {
		return fmt.Errorf("containerID required")
	}
	dir := filepath.Join(ctrRoot, req.ContainerID)
	dest := filepath.Join(dir, "root")
	a.mu.Lock()
	job := a.unpackers[req.ContainerID]
	a.mu.Unlock()
	if job == nil {
		if err := os.MkdirAll(dest, 0755); err != nil {
			return err
		}
		pr, pw := io.Pipe()
		errc := make(chan error, 1)
		go func() {
			errc <- unpackTar(pr, dest)
			_ = pr.Close()
		}()
		job = &unpackJob{w: pw, err: errc}
		a.mu.Lock()
		a.unpackers[req.ContainerID] = job
		a.mu.Unlock()
	}
	if len(req.Data) > 0 {
		if _, err := job.w.Write(req.Data); err != nil {
			return err
		}
	}
	if !req.EOF {
		return nil
	}
	_ = job.w.Close()
	err := <-job.err
	a.mu.Lock()
	img, disk := a.imageBytes[req.ContainerID], a.diskBytes[req.ContainerID]
	delete(a.unpackers, req.ContainerID)
	a.mu.Unlock()
	if err != nil && isENOSPC(err) {
		return fmt.Errorf("no space left unpacking image (%d bytes) onto user-rootfs disk (%d bytes): %w", img, disk, err)
	}
	return err
}

func isENOSPC(err error) bool {
	return errors.Is(err, syscall.ENOSPC)
}

func unpackTar(r io.Reader, dest string) error {
	tr := tar.NewReader(r)
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
