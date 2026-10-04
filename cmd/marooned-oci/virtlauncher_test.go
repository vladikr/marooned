package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStandaloneVMISpecFromFile(t *testing.T) {
	dir := t.TempDir()
	want := `{"kind":"VirtualMachineInstance"}`
	if err := os.WriteFile(filepath.Join(dir, "standalone-vmi.json"), []byte(want), 0644); err != nil {
		t.Fatal(err)
	}
	got := standaloneVMISpec(dir, "")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestStandaloneVMISpecMissing(t *testing.T) {
	dir := t.TempDir()
	if got := standaloneVMISpec(dir, ""); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestStartVirtLauncherPauseWithoutBinary(t *testing.T) {
	dir := t.TempDir()
	if pid := startVirtLauncherPause("id", dir); pid != 0 {
		t.Fatalf("pid %d without binary or spec", pid)
	}
	got := strings.TrimSpace(string(mustRead(filepath.Join(dir, "virt-launcher.err"))))
	if got != "no STANDALONE_VMI spec" {
		t.Fatalf("err %q", got)
	}
}

func TestVirtLauncherRunning(t *testing.T) {
	root := t.TempDir()
	if virtLauncherRunning(root, "uid-1") {
		t.Fatal("empty root")
	}
	d := filepath.Join(root, "ctr")
	if err := os.Mkdir(d, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "poduid"), []byte("uid-1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "virt-launcher.pid"), []byte("1"), 0644); err != nil {
		t.Fatal(err)
	}
	if virtLauncherRunning(root, "uid-1") {
		t.Fatal("pid 1 is not a running virt-launcher")
	}
	if virtLauncherRunning(root, "other") {
		t.Fatal("other uid")
	}
}

func TestBundleNetNS(t *testing.T) {
	dir := t.TempDir()
	if got := bundleNetNS(dir); got != "" {
		t.Fatalf("empty bundle %q", got)
	}
	cfg := []byte(`{
		"linux": {
			"namespaces": [
				{"type": "pid"},
				{"type": "network", "path": "/var/run/netns/cni-abc"},
				{"type": "uts", "path": "/proc/123/ns/uts"}
			]
		}
	}`)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), cfg, 0644); err != nil {
		t.Fatal(err)
	}
	if got := bundleNetNS(dir); got != "/var/run/netns/cni-abc" {
		t.Fatalf("netns %q", got)
	}
}

func TestImageVolumeBinds(t *testing.T) {
	binds := imageVolumeBinds("/overlay", "uid-1", "")
	if len(binds) != 2 {
		t.Fatalf("binds %d", len(binds))
	}
	if binds[0][0] != "/var/lib/kubelet/pods/uid-1/volumes/kubernetes.io~image/rootdisk" {
		t.Fatalf("disk src %s", binds[0][0])
	}
	if binds[0][1] != "/overlay/var/run/kubevirt-image-volume/disk_0" {
		t.Fatalf("disk dst %s", binds[0][1])
	}
	if binds[1][1] != "/overlay/var/run/kubevirt-kernel-boot" {
		t.Fatalf("kernel dst %s", binds[1][1])
	}
}

func TestOverlayLowerDir(t *testing.T) {
	got := overlayLowerDir([]string{
		"rw",
		"lowerdir=/var/lib/containers/storage/overlay/abc/merged:/var/run/crio/image-volumes",
	})
	want := "/var/lib/containers/storage/overlay/abc/merged"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestBundleImageVolumeSources(t *testing.T) {
	dir := t.TempDir()
	cfg := []byte(`{
		"mounts": [
			{"destination": "/proc", "type": "proc", "source": "proc"},
			{
				"destination": "/run/marooned/image-volume/rootdisk",
				"type": "overlay",
				"source": "overlay",
				"options": ["lowerdir=/var/lib/containers/storage/overlay/diskimg/merged:/var/run/crio/image-volumes"]
			},
			{
				"destination": "/run/marooned/image-volume/kernel-boot",
				"type": "overlay",
				"source": "overlay",
				"options": ["lowerdir=/var/lib/containers/storage/overlay/kernimg/merged:/var/run/crio/image-volumes"]
			}
		]
	}`)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), cfg, 0644); err != nil {
		t.Fatal(err)
	}
	disk, kernel := bundleImageVolumeSources(dir)
	if disk != "/var/lib/containers/storage/overlay/diskimg/merged" {
		t.Fatalf("disk %s", disk)
	}
	if kernel != "/var/lib/containers/storage/overlay/kernimg/merged" {
		t.Fatalf("kernel %s", kernel)
	}
	binds := imageVolumeBinds("/overlay", "uid-1", dir)
	if binds[0][0] != disk {
		t.Fatalf("prefer CRI-O overlay, got %s", binds[0][0])
	}
}

func TestBindCgroupFSDest(t *testing.T) {
	rootfs := t.TempDir()
	want := filepath.Join(rootfs, "sys/fs/cgroup")
	bindCgroupFS(rootfs)
	st, err := os.Stat(want)
	if err != nil {
		t.Fatal(err)
	}
	if !st.IsDir() {
		t.Fatalf("%s is not a dir", want)
	}
}

func TestEnsureLibvirtRunDirs(t *testing.T) {
	rootfs := t.TempDir()
	if err := os.MkdirAll(filepath.Join(rootfs, "run"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(rootfs, "var"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../run", filepath.Join(rootfs, "var/run")); err != nil {
		t.Fatal(err)
	}
	ensureLibvirtRunDirs(rootfs)
	for _, p := range []string{
		"run/libvirt",
		"run/libvirt/qemu",
		"run/kubevirt",
		"run/kubevirt-private",
		"var/run/libvirt",
		"var/log/libvirt",
	} {
		st, err := os.Stat(filepath.Join(rootfs, p))
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if !st.IsDir() {
			t.Fatalf("%s is not a dir", p)
		}
	}
}

func TestProcRootMatches(t *testing.T) {
	overlay := "/run/marooned-oci/abc/vl-rootfs"
	if !procRootMatches(overlay, overlay) {
		t.Fatal("exact overlay")
	}
	if !procRootMatches(overlay+"/var/run/libvirt", overlay) {
		t.Fatal("path under overlay")
	}
	if procRootMatches("/", overlay) {
		t.Fatal("host root")
	}
	if procRootMatches("/run/marooned-oci/other/vl-rootfs", overlay) {
		t.Fatal("other overlay")
	}
	if procRootMatches(overlay, "") {
		t.Fatal("empty overlay")
	}
}

func TestKillLeftoverVirtLauncherEmpty(t *testing.T) {
	dir := t.TempDir()
	killLeftoverVirtLauncher(dir, "uid-1")
	killLeftoverVirtLauncher(dir, "")
}

func TestVirtLauncherCmdChrootAndNetNS(t *testing.T) {
	cmd := virtLauncherCmd("/opt/marooned/virt-launcher-rootfs", "/var/run/netns/cni-x", []string{"--name", "box"})
	if cmd == nil {
		t.Fatal("nil cmd")
	}
	got := strings.Join(cmd.Args, " ")
	want := "nsenter --net=/var/run/netns/cni-x -- chroot /opt/marooned/virt-launcher-rootfs /usr/bin/virt-launcher --name box"
	if got != want {
		t.Fatalf("args %q want %q", got, want)
	}
}
