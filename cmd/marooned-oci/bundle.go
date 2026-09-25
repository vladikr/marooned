package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"maroonedpods.io/maroonedpods/pkg/sandbox"
)

type processSpec struct {
	Args  []string
	Env   []string
	Cwd   string
	Root  string
	Image string
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
		Annotations map[string]string `json:"annotations"`
	}
	_ = json.Unmarshal(b, &cfg)
	root := cfg.Root.Path
	if root == "" {
		root = "rootfs"
	}
	if !filepath.IsAbs(root) {
		root = filepath.Join(bundle, root)
	}
	img := cfg.Annotations["io.kubernetes.cri-o.ImageName"]
	if img == "" {
		img = cfg.Annotations["io.kubernetes.cri-o.Image"]
	}
	return processSpec{Args: cfg.Process.Args, Env: cfg.Process.Env, Cwd: cfg.Process.Cwd, Root: root, Image: img}
}

func dirSize(root string) int64 {
	return sandbox.DirSize(root)
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
	return sandbox.TarTree(f, src)
}
