package main

import (
	"archive/tar"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type processSpec struct {
	Args []string
	Env  []string
	Cwd  string
	Root string
}

func readProcessSpec(bundle string) processSpec {
	b, err := os.ReadFile(filepath.Join(bundle, "config.json"))
	if err != nil {
		return processSpec{}
	}
	var cfg struct {
		Root struct {
			Path string `json:"path"`
		} `json:"root"`
		Process struct {
			Args []string `json:"args"`
			Env  []string `json:"env"`
			Cwd  string   `json:"cwd"`
		} `json:"process"`
	}
	_ = json.Unmarshal(b, &cfg)
	root := cfg.Root.Path
	if root == "" {
		root = "rootfs"
	}
	if !filepath.IsAbs(root) {
		root = filepath.Join(bundle, root)
	}
	return processSpec{Args: cfg.Process.Args, Env: cfg.Process.Env, Cwd: cfg.Process.Cwd, Root: root}
}

func skipRootfsPath(rel string) bool {
	first, _, _ := strings.Cut(rel, string(os.PathSeparator))
	switch first {
	case "proc", "sys", "dev", "run", "tmp":
		return true
	}
	return false
}

func fileDev(info os.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return st.Dev
	}
	return 0
}

func dirSize(root string) int64 {
	root = filepath.Clean(root)
	st, err := os.Stat(root)
	if err != nil {
		return 0
	}
	rootDev := fileDev(st)
	var n int64
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if rel != "." && skipRootfsPath(rel) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if rootDev != 0 && fileDev(info) != 0 && fileDev(info) != rootDev {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Mode().IsRegular() {
			n += info.Size()
		}
		return nil
	})
	return n
}

func tarDirectory(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	tw := tar.NewWriter(f)
	defer tw.Close()
	src = filepath.Clean(src)
	st, err := os.Stat(src)
	if err != nil {
		return err
	}
	srcDev := fileDev(st)
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if skipRootfsPath(rel) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if srcDev != 0 && fileDev(info) != 0 && fileDev(info) != srcDev {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = rel
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			hdr.Linkname = link
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tw, in)
		_ = in.Close()
		return copyErr
	})
}
