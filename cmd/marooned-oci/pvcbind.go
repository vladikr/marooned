package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
	"k8s.io/apimachinery/pkg/api/resource"

	"maroonedpods.io/maroonedpods/pkg/sandbox"
)

const (
	qemuDiskUID         = 107
	qemuDiskGID         = 107
	defaultDiskImgBytes = int64(1) << 30
)

type vmiClaimDisk struct {
	name    string
	maxSize int64
}

type vmiSpecVolumes struct {
	Spec struct {
		Volumes []struct {
			Name                  string `json:"name"`
			PersistentVolumeClaim *struct {
				ClaimName string `json:"claimName"`
			} `json:"persistentVolumeClaim"`
			HostDisk *struct {
				Path     string            `json:"path"`
				Capacity resource.Quantity `json:"capacity"`
			} `json:"hostDisk"`
		} `json:"volumes"`
	} `json:"spec"`
}

func vmiClaimDisks(spec string) []vmiClaimDisk {
	if spec == "" {
		return nil
	}
	var vmi vmiSpecVolumes
	if json.Unmarshal([]byte(spec), &vmi) != nil {
		return nil
	}
	var out []vmiClaimDisk
	for _, vol := range vmi.Spec.Volumes {
		if vol.Name == "" {
			continue
		}
		d := vmiClaimDisk{name: vol.Name, maxSize: defaultDiskImgBytes}
		switch {
		case vol.HostDisk != nil:
			if n, ok := vol.HostDisk.Capacity.AsInt64(); ok && n >= 1<<20 {
				d.maxSize = n
			}
		case vol.PersistentVolumeClaim != nil:
		default:
			continue
		}
		out = append(out, d)
	}
	return out
}

func vmiPVCNames(spec string) []string {
	var names []string
	for _, d := range vmiClaimDisks(spec) {
		names = append(names, d.name)
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

type pvcBind struct {
	src, dst string
	maxSize  int64
}

func pvcDiskBinds(rootfs, podUID, bundle, spec string) []pvcBind {
	disks := vmiClaimDisks(spec)
	if len(disks) == 0 {
		return nil
	}
	fromBundle := bundlePVCSources(bundle)
	var binds []pvcBind
	for _, d := range disks {
		podVol := sandbox.PodVolumeFromVMIDisk(d.name)
		src := fromBundle[podVol]
		if src == "" && podUID != "" {
			src = sandbox.KubeletClaimVolumePath(podUID, podVol)
		}
		dst := sandbox.VirtPVCDiskOverlay(rootfs, d.name)
		binds = append(binds, pvcBind{src: src, dst: dst, maxSize: d.maxSize})
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
	wait := make([][2]string, len(binds))
	for i, p := range binds {
		wait[i] = [2]string{p.src, p.dst}
	}
	waitImageVolumeDirs(wait, 15*time.Second)
	for _, p := range binds {
		if p.src == "" {
			return fmt.Errorf("PVC disk source missing for %s", p.dst)
		}
		if err := bindPVCDir(p.src, p.dst); err != nil {
			return err
		}
		if err := ensureDiskImg(p.dst, p.maxSize); err != nil {
			return fmt.Errorf("disk.img in %s: %w", p.dst, err)
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

func ensureDiskImg(dir string, maxSize int64) error {
	p := filepath.Join(dir, sandbox.DiskImgName)
	size := diskImgSize(dir, maxSize)
	if size < 1<<20 {
		return fmt.Errorf("available space %d too small", size)
	}
	if st, err := os.Stat(p); err == nil && st.Size() > 0 {
		_ = os.Chmod(p, 0666)
		_ = os.Chown(p, qemuDiskUID, qemuDiskGID)
		return nil
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

func diskImgSize(dir string, maxSize int64) int64 {
	if maxSize < 1<<20 {
		maxSize = defaultDiskImgBytes
	}
	var st unix.Statfs_t
	avail := maxSize
	if err := unix.Statfs(dir, &st); err == nil {
		n := int64(st.Bavail) * int64(st.Bsize)
		reserve := int64(1 << 20)
		if n > reserve {
			n -= reserve
		}
		if n < avail {
			avail = n
		}
	}
	return avail &^ ((1 << 20) - 1)
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
