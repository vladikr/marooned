package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

const (
	tapMACFile      = "run/marooned-tap.mac"
	maroonedQemuRel = "run/marooned-qemu"
)

var qemuEmulatorRels = []string{
	"usr/libexec/qemu-kvm",
	"usr/bin/qemu-kvm",
	"usr/libexec/qemu-system-x86_64",
	"usr/bin/qemu-system-x86_64",
}

// plantTapEmulator wraps virt-launcher's qemu so the guest gets tap0.
// HandleStandaloneMode calls SyncVMI with nil VirtualMachineOptions, so
// the converter leaves interface type="" and libvirt rejects the domain.
// The real binary is copied to /run/marooned-qemu (tmpfs, not a libvirt
// emulator name). Binding it next to qemu-kvm as *.marooned made libvirt
// probe the wrapper as $0.marooned; bind then WriteFile also truncated
// the same inode, so both paths became the wrapper.
func plantTapEmulator(rootfs, mac string) error {
	if rootfs == "" || mac == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Join(rootfs, "run"), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(rootfs, tapMACFile), []byte(mac+"\n"), 0644); err != nil {
		return err
	}
	src := findRealQemu(rootfs)
	if src == "" {
		return os.ErrNotExist
	}
	dst := filepath.Join(rootfs, maroonedQemuRel)
	if !isELF(dst) {
		if err := copyQemuReal(src, dst); err != nil {
			return err
		}
	}
	script := []byte(tapEmulatorScript())
	n := 0
	for _, rel := range qemuEmulatorRels {
		p := filepath.Join(rootfs, filepath.FromSlash(rel))
		if !fileExists(p) {
			continue
		}
		if err := os.WriteFile(p, script, 0755); err != nil {
			return err
		}
		n++
	}
	if n == 0 {
		return os.ErrNotExist
	}
	return nil
}

func findRealQemu(rootfs string) string {
	if isELF(filepath.Join(rootfs, maroonedQemuRel)) {
		return filepath.Join(rootfs, maroonedQemuRel)
	}
	for _, rel := range qemuEmulatorRels {
		p := filepath.Join(rootfs, filepath.FromSlash(rel))
		if isELF(p) {
			return p
		}
	}
	hasEmulator := false
	for _, rel := range qemuEmulatorRels {
		if fileExists(filepath.Join(rootfs, filepath.FromSlash(rel))) {
			hasEmulator = true
			break
		}
	}
	if !hasEmulator {
		return ""
	}
	for _, rel := range qemuEmulatorRels {
		p := filepath.Join(virtLauncherRootfs, filepath.FromSlash(rel))
		if isELF(p) {
			return p
		}
	}
	return ""
}

func copyQemuReal(src, dst string) error {
	if src == dst {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	_ = unix.Unmount(dst, unix.MNT_DETACH)
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	cerr := out.Close()
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if cerr != nil {
		_ = os.Remove(tmp)
		return cerr
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func isELF(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var hdr [4]byte
	if _, err := f.Read(hdr[:]); err != nil {
		return false
	}
	return hdr[0] == 0x7f && hdr[1] == 'E' && hdr[2] == 'L' && hdr[3] == 'F'
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func tapEmulatorScript() string {
	return `#!/bin/sh
REAL=/run/marooned-qemu
MACFILE=/run/marooned-tap.mac
if [ ! -x "$REAL" ]; then
  echo "marooned tap emulator missing: $REAL" >&2
  exit 127
fi
inject=
if [ -s "$MACFILE" ]; then
  inject=1
  prev=
  for a in "$@"; do
    case "$a" in
      -help|--help|-h|-version|--version) inject= ;;
      none|none,*) inject= ;;
    esac
    case "$prev" in
      -machine|-M)
        case "$a" in
          none|none,*) inject= ;;
        esac
        ;;
    esac
    prev=$a
  done
fi
if [ -n "$inject" ]; then
  MAC=$(cat "$MACFILE" | tr -d ' \n')
  exec "$REAL" "$@" -netdev tap,id=maroonednet0,ifname=tap0,script=no,downscript=no,vhost=on -device virtio-net-pci,netdev=maroonednet0,mac="$MAC",id=ua-default
fi
exec "$REAL" "$@"
`
}

func qemuRealBindPath(rootfs string) string {
	if rootfs == "" {
		return ""
	}
	return filepath.Join(rootfs, maroonedQemuRel)
}

func tapEmulatorWrapped(script string) bool {
	return bytes.Contains([]byte(script), []byte("maroonednet0")) &&
		bytes.Contains([]byte(script), []byte("/run/marooned-qemu"))
}
