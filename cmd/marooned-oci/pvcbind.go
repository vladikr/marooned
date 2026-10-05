package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"maroonedpods.io/maroonedpods/pkg/sandbox"
)

const (
	qemuDiskUID = 107
	qemuDiskGID = 107
)

type vmiSpecVolumes struct {
	Spec struct {
		Volumes []struct {
			Name                  string `json:"name"`
			PersistentVolumeClaim *struct {
				ClaimName string `json:"claimName"`
			} `json:"persistentVolumeClaim"`
		} `json:"volumes"`
	} `json:"spec"`
}

func vmiPVCNames(spec string) []string {
	if spec == "" {
		return nil
	}
	var vmi vmiSpecVolumes
	if json.Unmarshal([]byte(spec), &vmi) != nil {
		return nil
	}
	var names []string
	for _, vol := range vmi.Spec.Volumes {
		if vol.PersistentVolumeClaim == nil || vol.Name == "" {
			continue
		}
		names = append(names, vol.Name)
	}
	return names
}

func bundlePVCSources(bundle string) map[string]string {
	out := map[string]string{}
	if bundle == "" {
		return out
	}
	b, err := os.ReadFile(filepath.Join(bundle, "config.json"))
	if err != nil {
		return out
	}
	var cfg struct {
		Mounts []ociMount `json:"mounts"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return out
	}
	prefix := sandbox.DiskMountRoot + "/"
	for _, m := range cfg.Mounts {
		if !strings.HasPrefix(m.Destination, prefix) {
			continue
		}
		name := strings.TrimPrefix(m.Destination, prefix)
		if name == "" || strings.Contains(name, "/") {
			continue
		}
		src := m.Source
		if m.Type == "overlay" || src == "" || src == "overlay" {
			if lower := overlayLowerDir(m.Options); lower != "" {
				src = lower
			}
		}
		if src != "" {
			out[name] = src
		}
	}
	return out
}

func pvcDiskBinds(rootfs, podUID, bundle, spec string) [][2]string {
	names := vmiPVCNames(spec)
	if len(names) == 0 {
		return nil
	}
	fromBundle := bundlePVCSources(bundle)
	var binds [][2]string
	for _, vmiVol := range names {
		podVol := sandbox.PodVolumeFromVMIDisk(vmiVol)
		src := fromBundle[podVol]
		if src == "" && podUID != "" {
			src = sandbox.KubeletClaimVolumePath(podUID, podVol)
		}
		dst := sandbox.VirtPVCDiskOverlay(rootfs, vmiVol)
		binds = append(binds, [2]string{src, dst})
	}
	return binds
}

func plantPVCDisks(rootfs, podUID, bundle, spec string) error {
	if rootfs == "" {
		return nil
	}
	binds := pvcDiskBinds(rootfs, podUID, bundle, spec)
	if len(binds) == 0 {
		return nil
	}
	waitImageVolumeDirs(binds, 15*time.Second)
	for _, p := range binds {
		if p[0] == "" {
			return fmt.Errorf("PVC disk source missing for %s", p[1])
		}
		if err := bindPVCDir(p[0], p[1]); err != nil {
			return err
		}
		if err := ensureDiskImg(p[1]); err != nil {
			return fmt.Errorf("disk.img in %s: %w", p[1], err)
		}
	}
	return nil
}

func bindPVCDir(src, dst string) error {
	st, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("pvc source %s: %w", src, err)
	}
	if !st.IsDir() {
		return fmt.Errorf("pvc source %s is not a directory", src)
	}
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}
	if err := unix.Mount(src, dst, "", unix.MS_BIND, ""); err != nil {
		return fmt.Errorf("bind %s -> %s: %w", src, dst, err)
	}
	return nil
}

func ensureDiskImg(dir string) error {
	p := filepath.Join(dir, sandbox.DiskImgName)
	if st, err := os.Stat(p); err == nil && st.Size() > 0 {
		_ = os.Chmod(p, 0666)
		_ = os.Chown(p, qemuDiskUID, qemuDiskGID)
		return nil
	}
	size := diskImgSize(dir)
	if size < 1<<20 {
		return fmt.Errorf("available space %d too small", size)
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0666)
	if err != nil {
		return err
	}
	_, err = f.WriteAt([]byte{0}, size-1)
	_ = f.Close()
	if err != nil {
		_ = os.Remove(p)
		return err
	}
	_ = os.Chmod(p, 0666)
	_ = os.Chown(p, qemuDiskUID, qemuDiskGID)
	return nil
}

func diskImgSize(dir string) int64 {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0
	}
	avail := int64(st.Bavail) * int64(st.Bsize)
	reserve := int64(1 << 20)
	if avail > reserve {
		avail -= reserve
	}
	return avail &^ (reserve - 1)
}

func unmountPVCDisks(rootfs string) {
	dir := filepath.Join(rootfs, "var/run/kubevirt-private/vmi-disks")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		_ = unix.Unmount(filepath.Join(dir, e.Name()), unix.MNT_DETACH)
	}
}
