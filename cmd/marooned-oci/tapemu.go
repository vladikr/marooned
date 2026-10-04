package main

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

const tapMACFile = "run/marooned-tap.mac"

var qemuEmulatorRels = []string{
	"usr/libexec/qemu-kvm",
	"usr/bin/qemu-kvm",
	"usr/libexec/qemu-system-x86_64",
	"usr/bin/qemu-system-x86_64",
}

// plantTapEmulator wraps every qemu binary virt-launcher might exec so the
// guest gets tap0. HandleStandaloneMode calls SyncVMI with nil
// VirtualMachineOptions, so the converter leaves interface type="" and
// libvirt rejects the domain. We strip the NIC from STANDALONE_VMI and
// attach tap0 on the qemu command line instead.
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
	n := 0
	var last error
	for _, rel := range qemuEmulatorRels {
		ok, err := wrapQemuEmulator(filepath.Join(rootfs, filepath.FromSlash(rel)))
		if err != nil {
			last = err
			continue
		}
		if ok {
			n++
		}
	}
	if n == 0 {
		if last != nil {
			return last
		}
		return os.ErrNotExist
	}
	return nil
}

func wrapQemuEmulator(real string) (bool, error) {
	st, err := os.Stat(real)
	if err != nil {
		return false, nil
	}
	if st.IsDir() {
		return false, nil
	}
	hidden := real + ".marooned"
	if err := os.WriteFile(hidden, []byte{}, 0755); err != nil {
		return false, err
	}
	_ = unix.Unmount(hidden, unix.MNT_DETACH)
	if err := unix.Mount(real, hidden, "", unix.MS_BIND, ""); err != nil {
		return false, err
	}
	script := tapEmulatorScript()
	if err := os.WriteFile(real, []byte(script), 0755); err != nil {
		_ = unix.Unmount(hidden, unix.MNT_DETACH)
		return false, err
	}
	return true, nil
}

func tapEmulatorScript() string {
	return `#!/bin/sh
REAL="$0.marooned"
MACFILE=/run/marooned-tap.mac
if [ ! -x "$REAL" ]; then
  echo "marooned tap emulator missing: $REAL" >&2
  exit 127
fi
if [ -s "$MACFILE" ]; then
  MAC=$(cat "$MACFILE")
  MAC=$(echo "$MAC" | tr -d ' \n')
  exec "$REAL" "$@" -netdev tap,id=maroonednet0,ifname=tap0,script=no,downscript=no,vhost=on -device virtio-net-pci,netdev=maroonednet0,mac="$MAC",id=ua-default
fi
exec "$REAL" "$@"
`
}

func qemuEmulatorHiddenPaths(rootfs string) []string {
	if rootfs == "" {
		return nil
	}
	var out []string
	for _, rel := range qemuEmulatorRels {
		out = append(out, filepath.Join(rootfs, filepath.FromSlash(rel)+".marooned"))
	}
	return out
}

func tapEmulatorWrapped(script string) bool {
	return strings.Contains(script, "maroonednet0") && strings.Contains(script, "tap0")
}
