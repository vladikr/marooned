package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTapEmulatorScript(t *testing.T) {
	s := tapEmulatorScript()
	if !tapEmulatorWrapped(s) {
		t.Fatalf("script %s", s)
	}
	if !strings.Contains(s, "/run/marooned-tap.mac") {
		t.Fatal("mac file")
	}
	if strings.Contains(s, "$0.marooned") {
		t.Fatal("suffix real path is probed by libvirt")
	}
	if strings.Contains(s, "/run/marooned-qemu") {
		t.Fatal("run path loses bios-256k.bin")
	}
	if !strings.Contains(s, "-L") || !strings.Contains(s, "/usr/share/seabios") {
		t.Fatal("seabios datadir")
	}
	if !strings.Contains(s, "vhost=on") {
		t.Fatal("vhost")
	}
	if !strings.Contains(s, "none|none,*)") {
		t.Fatal("skip libvirt -machine none probe")
	}
	if !strings.HasPrefix(s, "#!/bin/sh\n") {
		t.Fatal("shebang")
	}
}

func TestPlantTapEmulatorWritesMAC(t *testing.T) {
	root := t.TempDir()
	if err := plantTapEmulator(root, "0a:58:0a:f4:00:6d"); err != os.ErrNotExist {
		t.Fatalf("no qemu binary: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(root, tapMACFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(b)) != "0a:58:0a:f4:00:6d" {
		t.Fatalf("mac %q", b)
	}
}

func TestPlantTapEmulatorEmpty(t *testing.T) {
	if err := plantTapEmulator("", "aa:bb:cc:dd:ee:ff"); err != nil {
		t.Fatal(err)
	}
	if err := plantTapEmulator(t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
}

func TestIsELF(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "qemu")
	if isELF(p) {
		t.Fatal("missing")
	}
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if isELF(p) {
		t.Fatal("script")
	}
	if err := os.WriteFile(p, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1}, 0755); err != nil {
		t.Fatal(err)
	}
	if !isELF(p) {
		t.Fatal("want elf")
	}
}

func TestFindRealQemuPrefersCopiedELF(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "usr/libexec"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "usr/libexec/qemu-kvm"), []byte(tapEmulatorScript()), 0755); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(root, maroonedQemuRel)
	if err := os.WriteFile(real, []byte{0x7f, 'E', 'L', 'F'}, 0755); err != nil {
		t.Fatal(err)
	}
	got := findRealQemu(root)
	if got != real {
		t.Fatalf("got %s want %s", got, real)
	}
}

func TestPlantTapEmulatorCopiesELF(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "usr/libexec"), 0755); err != nil {
		t.Fatal(err)
	}
	elf := []byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 9, 9, 9}
	emu := filepath.Join(root, "usr/libexec/qemu-kvm")
	if err := os.WriteFile(emu, elf, 0755); err != nil {
		t.Fatal(err)
	}
	if err := plantTapEmulator(root, "0a:58:0a:f4:00:6d"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, maroonedQemuRel))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(elf) {
		t.Fatalf("real qemu overwritten: %q", got)
	}
	wrap, err := os.ReadFile(emu)
	if err != nil {
		t.Fatal(err)
	}
	if !tapEmulatorWrapped(string(wrap)) {
		t.Fatalf("wrapper %s", wrap)
	}
	if isELF(emu) {
		t.Fatal("emulator still elf")
	}
}

func TestQemuRealBindPath(t *testing.T) {
	if qemuRealBindPath("") != "" {
		t.Fatal("empty")
	}
	if qemuRealBindPath("/overlay") != "/overlay/usr/libexec/marooned-qemu-real" {
		t.Fatal(qemuRealBindPath("/overlay"))
	}
}

func TestTapEmulatorScriptSkipsProbe(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "qemu")
	if err := os.WriteFile(real, []byte("#!/bin/sh\nprintf 'ARGS:%s\\n' \"$*\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	mac := filepath.Join(dir, "mac")
	if err := os.WriteFile(mac, []byte("aa:bb:cc:dd:ee:ff\n"), 0644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "wrap")
	body := strings.ReplaceAll(tapEmulatorScript(), "/usr/libexec/marooned-qemu-real", real)
	body = strings.ReplaceAll(body, "/run/marooned-tap.mac", mac)
	if err := os.WriteFile(script, []byte(body), 0755); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(script, "-S", "-nodefaults", "-machine", "none,accel=kvm:tcg").CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	if strings.Contains(string(out), "maroonednet0") {
		t.Fatalf("probe grabbed tap: %s", out)
	}
	out, err = exec.Command(script, "-name", "guest=box", "-machine", "pc,accel=kvm").CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	if !strings.Contains(string(out), "maroonednet0") || !strings.Contains(string(out), "aa:bb:cc:dd:ee:ff") {
		t.Fatalf("vm missing tap: %s", out)
	}
}
