package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"maroonedpods.io/maroonedpods/pkg/sandbox"
	"maroonedpods.io/maroonedpods/pkg/sandbox/vsock"
)

const (
	virtLauncherRootfs = "/opt/marooned/virt-launcher-rootfs"
	standaloneVMIFile  = "standalone-vmi.json"
	vlRootfsDir        = "vl-rootfs"
	vlUpperDir         = "vl-upper"
	vlWorkDir          = "vl-work"
)

var virtLauncherBins = []string{
	"/usr/local/bin/virt-launcher",
	"/opt/marooned/virt-launcher",
	"/usr/bin/virt-launcher",
	virtLauncherRootfs + "/usr/bin/virt-launcher",
}

func findVirtLauncher() string {
	for _, p := range virtLauncherBins {
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0111 != 0 {
			return p
		}
	}
	return ""
}

func standaloneVMISpec(dir, bundle string) string {
	if b := strings.TrimSpace(string(mustRead(filepath.Join(dir, standaloneVMIFile)))); b != "" {
		return b
	}
	if bundle == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(bundle, "config.json"))
	if err != nil {
		return ""
	}
	var cfg struct {
		Annotations map[string]string `json:"annotations"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return ""
	}
	return strings.TrimSpace(cfg.Annotations["maroonedpods.io/standalone-vmi"])
}

func bundleNetNS(bundle string) string {
	if bundle == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(bundle, "config.json"))
	if err != nil {
		return ""
	}
	var cfg struct {
		Linux struct {
			Namespaces []struct {
				Type string `json:"type"`
				Path string `json:"path"`
			} `json:"namespaces"`
		} `json:"linux"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return ""
	}
	for _, ns := range cfg.Linux.Namespaces {
		if ns.Type == "network" && ns.Path != "" {
			return ns.Path
		}
	}
	return ""
}

type vmiIdentity struct {
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
		UID       string `json:"uid"`
	} `json:"metadata"`
	Status struct {
		VSOCKCID *uint32 `json:"VSOCKCID"`
	} `json:"status"`
}

func fetchStandaloneVMI(dir string, meta bundleMeta) {
	if meta.Namespace == "" || meta.Name == "" {
		return
	}
	body, err := shimJSON("POST", "/v1/RenderStandaloneVMI", map[string]string{
		"podName":      meta.Name,
		"podNamespace": meta.Namespace,
		"podUID":       meta.UID,
	})
	if err != nil {
		_ = os.WriteFile(filepath.Join(dir, "standalone-vmi.err"), []byte(err.Error()+"\n"+string(body)), 0644)
		return
	}
	var resp struct {
		VMI json.RawMessage `json:"vmi"`
	}
	if json.Unmarshal(body, &resp) != nil || len(resp.VMI) == 0 {
		_ = os.WriteFile(filepath.Join(dir, standaloneVMIFile), body, 0644)
		return
	}
	_ = os.WriteFile(filepath.Join(dir, standaloneVMIFile), resp.VMI, 0644)
}

func hostVirtLauncherRootfs() string {
	if st, err := os.Stat(filepath.Join(virtLauncherRootfs, "usr/bin/virt-launcher")); err != nil || st.IsDir() {
		return ""
	}
	return virtLauncherRootfs
}

// prepareVirtLauncherRootfs overlays a per-sandbox upper dir on the shared
// virt-launcher image so concurrent guests do not share /run and libvirt state.
func prepareVirtLauncherRootfs(dir string) (rootfs string, private bool) {
	lower := hostVirtLauncherRootfs()
	if lower == "" {
		return "", false
	}
	merged := filepath.Join(dir, vlRootfsDir)
	upper := filepath.Join(dir, vlUpperDir)
	work := filepath.Join(dir, vlWorkDir)
	for _, d := range []string{merged, upper, work} {
		if err := os.MkdirAll(d, 0755); err != nil {
			return lower, false
		}
	}
	opts := "lowerdir=" + lower + ",upperdir=" + upper + ",workdir=" + work
	if err := unix.Mount("overlay", merged, "overlay", 0, opts); err != nil {
		return lower, false
	}
	return merged, true
}

func bindRuntimeFS(rootfs string, private bool) {
	for _, d := range []string{"proc", "sys", "run", "tmp", "dev", "dev/net", "dev/pts"} {
		_ = os.MkdirAll(filepath.Join(rootfs, d), 0755)
	}
	if private {
		_ = unix.Mount("proc", filepath.Join(rootfs, "proc"), "proc", 0, "")
		_ = unix.Mount("sysfs", filepath.Join(rootfs, "sys"), "sysfs", 0, "")
		_ = unix.Mount("tmpfs", filepath.Join(rootfs, "run"), "tmpfs", 0, "mode=0755")
		_ = unix.Mount("tmpfs", filepath.Join(rootfs, "tmp"), "tmpfs", 0, "mode=1777")
		_ = unix.Mount("devpts", filepath.Join(rootfs, "dev/pts"), "devpts", 0, "newinstance,ptmxmode=0666")
	}
	ensureLibvirtRunDirs(rootfs)
	bindKVM(rootfs)
}

// ensureLibvirtRunDirs plants the dirs SetupLibvirt writes into. The
// virt-launcher image has /var/run -> ../run, and bindRuntimeFS tmpfs-mounts
// run, so /var/run/libvirt does not exist until we mkdir it. copyFile in
// kubevirt SetupLibvirt does not MkdirAll.
func ensureLibvirtRunDirs(rootfs string) {
	if rootfs == "" {
		return
	}
	for _, d := range []string{
		"run/libvirt",
		"run/libvirt/qemu",
		"run/kubevirt",
		"run/kubevirt/container-disks",
		"run/kubevirt/hotplug-disks",
		"run/kubevirt-private",
		"run/kubevirt-private/libvirt",
		"run/kubevirt-ephemeral-disks",
		"var/log/libvirt",
		"var/log/libvirt/qemu",
	} {
		_ = os.MkdirAll(filepath.Join(rootfs, d), 0755)
	}
}

func unmountVirtLauncherRootfs(dir string) {
	merged := filepath.Join(dir, vlRootfsDir)
	for _, p := range []string{
		"var/run/kubevirt-image-volume/disk_0",
		"var/run/kubevirt-kernel-boot",
		"dev/kvm", "dev/net/tun", "dev/vhost-net", "dev/vhost-vsock",
		"dev/null", "dev/zero", "dev/urandom", "dev/random", "dev/pts",
		"proc", "sys", "run", "tmp",
	} {
		_ = unix.Unmount(filepath.Join(merged, p), unix.MNT_DETACH)
	}
	_ = unix.Unmount(merged, unix.MNT_DETACH)
}

func bindDir(src, dst string) {
	st, err := os.Stat(src)
	if err != nil || !st.IsDir() {
		return
	}
	_ = os.MkdirAll(dst, 0755)
	_ = unix.Mount(src, dst, "", unix.MS_BIND, "")
}

func imageVolumeBinds(rootfs, podUID string) [][2]string {
	return [][2]string{
		{sandbox.KubeletImageVolumePath(podUID, sandbox.ImageVolumeDiskName), sandbox.ImageVolumeDiskOverlay(rootfs)},
		{sandbox.KubeletImageVolumePath(podUID, sandbox.ImageVolumeKernelName), sandbox.ImageVolumeKernelOverlay(rootfs)},
	}
}

func plantImageVolumes(rootfs, podUID string) {
	if rootfs == "" || podUID == "" {
		return
	}
	waitImageVolumeDirs(podUID, 15*time.Second)
	_ = os.MkdirAll(filepath.Join(rootfs, "var/run/kubevirt/container-disks"), 0755)
	for _, p := range imageVolumeBinds(rootfs, podUID) {
		bindDir(p[0], p[1])
	}
}

func waitImageVolumeDirs(podUID string, d time.Duration) {
	deadline := time.Now().Add(d)
	for {
		ready := true
		for _, p := range imageVolumeBinds("", podUID) {
			st, err := os.Stat(p[0])
			if err != nil || !st.IsDir() {
				ready = false
				break
			}
		}
		if ready || !time.Now().Before(deadline) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func bindKVM(rootfs string) {
	dev := filepath.Join(rootfs, "dev")
	_ = os.MkdirAll(dev, 0755)
	for _, n := range []string{"kvm", "net/tun", "vhost-net", "vhost-vsock", "null", "zero", "urandom", "random"} {
		src := "/dev/" + n
		dst := filepath.Join(dev, n)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		_ = os.MkdirAll(filepath.Dir(dst), 0755)
		if _, err := os.Stat(dst); err != nil {
			f, err := os.OpenFile(dst, os.O_CREATE, 0666)
			if err == nil {
				_ = f.Close()
			}
		}
		if err := unix.Mount(src, dst, "", unix.MS_BIND, ""); err != nil {
			_ = os.Remove(dst)
			_ = copyDevNode(src, dst)
		}
	}
}

func copyDevNode(src, dst string) error {
	st, err := os.Stat(src)
	if err != nil {
		return err
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	_ = unix.Mknod(dst, uint32(st.Mode()), int(sys.Rdev))
	return nil
}

func virtLauncherCmd(rootfs, netns string, args []string) *exec.Cmd {
	var prefix []string
	if netns != "" {
		prefix = append(prefix, "nsenter", "--net="+netns, "--")
	}
	if rootfs != "" {
		prefix = append(prefix, "chroot", rootfs, "/usr/bin/virt-launcher")
		return exec.Command(prefix[0], append(prefix[1:], args...)...)
	}
	bin := findVirtLauncher()
	if bin == "" {
		return nil
	}
	prefix = append(prefix, bin)
	return exec.Command(prefix[0], append(prefix[1:], args...)...)
}

// startVirtLauncherPause runs virt-launcher with STANDALONE_VMI as the CRI-O
// pause PID so qemu lives in the user Pod cgroup. There is no --standalone
// flag; HandleStandaloneMode keys off the env (kubevirt-vm-to-pod / VEP-359).
// Returns 0 when the rootfs/binary or spec is missing (dummy pause).
func skipVirtLauncher(dir, reason string) int {
	if dir != "" {
		_ = os.WriteFile(filepath.Join(dir, "virt-launcher.err"), []byte(reason+"\n"), 0644)
	}
	return 0
}

func virtLauncherRunning(root, uid string) bool {
	if root == "" || uid == "" {
		return false
	}
	ents, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		d := filepath.Join(root, e.Name())
		if strings.TrimSpace(string(mustRead(filepath.Join(d, "poduid")))) != uid {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(mustRead(filepath.Join(d, "virt-launcher.pid")))))
		if err == nil && pidAlive(pid) {
			return true
		}
	}
	return false
}

func startVirtLauncherPause(id, dir string) int {
	bundle := strings.TrimSpace(string(mustRead(filepath.Join(dir, "bundle"))))
	spec := standaloneVMISpec(dir, bundle)
	if spec == "" {
		return skipVirtLauncher(dir, "no STANDALONE_VMI spec")
	}
	var ident vmiIdentity
	_ = json.Unmarshal([]byte(spec), &ident)
	uid := ident.Metadata.UID
	if uid == "" {
		uid = strings.TrimSpace(string(mustRead(filepath.Join(dir, "poduid"))))
	}
	args := []string{
		"--qemu-timeout", "5m",
		"--allow-emulation",
		"--image-volume",
	}
	if ident.Metadata.Name != "" {
		args = append(args, "--name", ident.Metadata.Name)
	}
	if uid != "" {
		args = append(args, "--uid", uid)
	}
	if ident.Metadata.Namespace != "" {
		args = append(args, "--namespace", ident.Metadata.Namespace)
	}

	cid := uint32(0)
	if ident.Status.VSOCKCID != nil {
		cid = *ident.Status.VSOCKCID
	}
	if cid < 3 {
		cid = vsock.CIDForUID(uid)
	}
	if uid != "" {
		_ = vsock.WriteCIDFile(vsock.DefaultHostDir, uid, strconv.FormatUint(uint64(cid), 10))
	}

	rootfs, private := prepareVirtLauncherRootfs(dir)
	if rootfs != "" {
		bindRuntimeFS(rootfs, private)
		plantImageVolumes(rootfs, uid)
	}
	netns := bundleNetNS(bundle)
	startVsockFwd(uid, netns)
	cmd := virtLauncherCmd(rootfs, netns, args)
	if cmd == nil {
		unmountVirtLauncherRootfs(dir)
		return skipVirtLauncher(dir, "virt-launcher binary missing")
	}
	logf := filepath.Join(dir, "virt-launcher.log")
	log, err := os.OpenFile(logf, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log = nil
	}
	cmd.Env = append(os.Environ(), "STANDALONE_VMI="+spec)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if log != nil {
		cmd.Stdout = log
		cmd.Stderr = log
	}
	if err := cmd.Start(); err != nil {
		if log != nil {
			_, _ = log.WriteString("start: " + err.Error() + "\n")
			_ = log.Close()
		}
		unmountVirtLauncherRootfs(dir)
		return skipVirtLauncher(dir, "start: "+err.Error())
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	if log != nil {
		_ = log.Close()
	}
	if pid <= 1 || !pidAlive(pid) {
		unmountVirtLauncherRootfs(dir)
		return skipVirtLauncher(dir, "virt-launcher pid died immediately")
	}
	detachFromRuntimeCgroup(pid)
	_ = os.WriteFile(filepath.Join(dir, "virt-launcher.pid"), []byte(strconv.Itoa(pid)), 0644)
	_ = id
	return pid
}
