package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const cgroupFSRoot = "/sys/fs/cgroup"

func bundleCgroupsPath(bundle string) string {
	if bundle == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(bundle, "config.json"))
	if err != nil {
		return ""
	}
	var cfg struct {
		Linux struct {
			CgroupsPath string `json:"cgroupsPath"`
		} `json:"linux"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return ""
	}
	return strings.TrimSpace(cfg.Linux.CgroupsPath)
}

// expandSlice turns a systemd slice name into a cgroupfs path under
// /sys/fs/cgroup. "a-b-c.slice" -> "a.slice/a-b.slice/a-b-c.slice".
func expandSlice(slice string) string {
	slice = strings.TrimSpace(slice)
	if slice == "" {
		return ""
	}
	slice = strings.Trim(slice, "/")
	if strings.Contains(slice, "/") {
		return slice
	}
	if !strings.HasSuffix(slice, ".slice") {
		return slice
	}
	name := strings.TrimSuffix(slice, ".slice")
	if name == "" {
		return slice
	}
	parts := strings.Split(name, "-")
	var segs []string
	acc := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		if acc == "" {
			acc = p
		} else {
			acc = acc + "-" + p
		}
		segs = append(segs, acc+".slice")
	}
	return strings.Join(segs, "/")
}

// resolveCgroupDir maps an OCI linux.cgroupsPath to a cgroup v2 directory.
// Systemd driver: "slice:prefix:name" -> .../expanded-slice/prefix-name.scope
// cgroupfs driver: a path under /sys/fs/cgroup.
// Never returns /sys/fs/cgroup itself: that is the root cgroup qemu
// escaped into via detachFromRuntimeCgroup.
func resolveCgroupDir(cgroupsPath string) string {
	cgroupsPath = strings.TrimSpace(cgroupsPath)
	if cgroupsPath == "" {
		return ""
	}
	if strings.Contains(cgroupsPath, ":") {
		parts := strings.SplitN(cgroupsPath, ":", 3)
		if len(parts) != 3 || parts[2] == "" {
			return ""
		}
		slice, prefix, name := parts[0], parts[1], parts[2]
		exp := strings.Trim(expandSlice(slice), "/")
		scope := prefix + "-" + name + ".scope"
		if prefix == "" {
			scope = name + ".scope"
		}
		if exp == "" {
			return cgroupFSRoot + "/" + scope
		}
		return cgroupFSRoot + "/" + exp + "/" + scope
	}
	p := strings.TrimPrefix(cgroupsPath, cgroupFSRoot)
	p = strings.Trim(p, "/")
	if p == "" {
		return ""
	}
	return cgroupFSRoot + "/" + p
}

func joinCgroup(pid int, dir string) {
	if pid <= 1 || dir == "" {
		return
	}
	body := []byte(strconv.Itoa(pid) + "\n")
	for p := dir; p != "" && p != "/" && p != "."; p = filepath.Dir(p) {
		if p == cgroupFSRoot {
			return
		}
		_ = os.MkdirAll(p, 0755)
		if err := os.WriteFile(filepath.Join(p, "cgroup.procs"), body, 0644); err == nil {
			return
		}
		if filepath.Dir(p) == p {
			return
		}
	}
}

func placeInPodCgroup(pid int, dir string) {
	if pid <= 1 || dir == "" {
		return
	}
	bundle := strings.TrimSpace(string(mustRead(filepath.Join(dir, "bundle"))))
	joinCgroup(pid, resolveCgroupDir(bundleCgroupsPath(bundle)))
}

// placeOverlayInPodCgroup puts virt-launcher and every process chrooted
// into the overlay (virtqemud, qemu) into the user Pod cgroup from the
// OCI spec. Do not write /sys/fs/cgroup/cgroup.procs (root): that was
// how qemu escaped kubelet/CPU manager.
func placeOverlayInPodCgroup(dir string, pid int) {
	if dir == "" {
		return
	}
	bundle := strings.TrimSpace(string(mustRead(filepath.Join(dir, "bundle"))))
	cg := resolveCgroupDir(bundleCgroupsPath(bundle))
	if cg == "" {
		return
	}
	if pid > 1 {
		joinCgroup(pid, cg)
	}
	if vl, err := strconv.Atoi(strings.TrimSpace(string(mustRead(filepath.Join(dir, "virt-launcher.pid"))))); err == nil && vl > 1 {
		joinCgroup(vl, cg)
	}
	overlay := filepath.Join(dir, vlRootfsDir)
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return
	}
	for _, e := range ents {
		p, err := strconv.Atoi(e.Name())
		if err != nil || p <= 1 {
			continue
		}
		root, err := os.Readlink(filepath.Join("/proc", e.Name(), "root"))
		if err != nil || !procRootMatches(root, overlay) {
			continue
		}
		joinCgroup(p, cg)
	}
}
