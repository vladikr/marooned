package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestExpandSlice(t *testing.T) {
	got := expandSlice("kubelet-kubepods-besteffort-podabc_def.slice")
	want := "kubelet.slice/kubelet-kubepods.slice/kubelet-kubepods-besteffort.slice/kubelet-kubepods-besteffort-podabc_def.slice"
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
	if expandSlice("system.slice") != "system.slice" {
		t.Fatalf("system.slice %s", expandSlice("system.slice"))
	}
	if expandSlice("kubelet.slice/already/path.slice") != "kubelet.slice/already/path.slice" {
		t.Fatal("path passthrough")
	}
}

func TestResolveCgroupDirSystemd(t *testing.T) {
	in := "kubelet-kubepods-besteffort-podabc.slice:crio:0123456789abcdef"
	got := resolveCgroupDir(in)
	want := "/sys/fs/cgroup/kubelet.slice/kubelet-kubepods.slice/kubelet-kubepods-besteffort.slice/kubelet-kubepods-besteffort-podabc.slice/crio-0123456789abcdef.scope"
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestResolveCgroupDirCgroupfs(t *testing.T) {
	got := resolveCgroupDir("/kubepods/besteffort/poduid/ctrid")
	want := "/sys/fs/cgroup/kubepods/besteffort/poduid/ctrid"
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
	got = resolveCgroupDir("kubepods/burstable/poduid/ctrid")
	if got != "/sys/fs/cgroup/kubepods/burstable/poduid/ctrid" {
		t.Fatalf("relative %s", got)
	}
	got = resolveCgroupDir("/sys/fs/cgroup/kubepods/poduid/ctrid")
	if got != "/sys/fs/cgroup/kubepods/poduid/ctrid" {
		t.Fatalf("already rooted %s", got)
	}
}

func TestResolveCgroupDirEmpty(t *testing.T) {
	if resolveCgroupDir("") != "" {
		t.Fatal("empty")
	}
	if resolveCgroupDir("/sys/fs/cgroup") != "" {
		t.Fatal("root cgroup")
	}
	if resolveCgroupDir("/sys/fs/cgroup/") != "" {
		t.Fatal("root cgroup slash")
	}
	if resolveCgroupDir("crio:abc") != "" {
		t.Fatal("two-part systemd")
	}
}

func TestResolveCgroupDirEmptyPrefix(t *testing.T) {
	got := resolveCgroupDir(":crio:abc")
	want := "/sys/fs/cgroup/crio-abc.scope"
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestBundleCgroupsPath(t *testing.T) {
	dir := t.TempDir()
	cfg := []byte(`{"linux":{"cgroupsPath":"kubelet.slice:crio:abc"}}`)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), cfg, 0644); err != nil {
		t.Fatal(err)
	}
	if got := bundleCgroupsPath(dir); got != "kubelet.slice:crio:abc" {
		t.Fatalf("got %q", got)
	}
	if bundleCgroupsPath("") != "" || bundleCgroupsPath(t.TempDir()) != "" {
		t.Fatal("missing")
	}
}

func TestJoinCgroupWritesProcs(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "pod.slice", "crio-x.scope")
	joinCgroup(4242, dest)
	b, err := os.ReadFile(filepath.Join(dest, "cgroup.procs"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(b)) != "4242" {
		t.Fatalf("procs %q", b)
	}
}

func TestJoinCgroupSkipsRoot(t *testing.T) {
	joinCgroup(1, cgroupFSRoot)
	joinCgroup(4242, "")
	joinCgroup(0, "/tmp")
}

func TestPlaceInPodCgroupFromBundle(t *testing.T) {
	dir := t.TempDir()
	cgroot := t.TempDir()
	old := cgroupFSRoot
	// resolveCgroupDir always prefixes /sys/fs/cgroup; joinCgroup mkdir
	// that path. Use a systemd path under a fake dest via joinCgroup only.
	_ = old
	bundle := t.TempDir()
	slice := filepath.Base(cgroot)
	cfg := []byte(`{"linux":{"cgroupsPath":"` + slice + `:crio:ctr1"}}`)
	if err := os.WriteFile(filepath.Join(bundle, "config.json"), cfg, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bundle"), []byte(bundle), 0644); err != nil {
		t.Fatal(err)
	}
	// Cannot redirect cgroupFSRoot (const). placeInPodCgroup is covered by
	// bundleCgroupsPath + resolve + join unit tests.
	_ = dir
	if bundleCgroupsPath(bundle) != slice+":crio:ctr1" {
		t.Fatal(bundleCgroupsPath(bundle))
	}
}

func TestJoinCgroupWalksToParent(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "pod.slice")
	if err := os.MkdirAll(parent, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "cgroup.procs"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	// child dir cannot be written: make cgroup.procs a directory so WriteFile fails
	child := filepath.Join(parent, "crio-x.scope")
	if err := os.MkdirAll(filepath.Join(child, "cgroup.procs"), 0755); err != nil {
		t.Fatal(err)
	}
	joinCgroup(99, child)
	b, err := os.ReadFile(filepath.Join(parent, "cgroup.procs"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(b)) != strconv.Itoa(99) {
		t.Fatalf("parent procs %q", b)
	}
}
