package vsock

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUnixDialAddr(t *testing.T) {
	got := UnixDialAddr("/var/run/marooned", "abc")
	want := "unix:/var/run/marooned/abc/agent.sock"
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestEnsureSandboxDirIs0777(t *testing.T) {
	root := t.TempDir()
	if err := EnsureSandboxDir(root, "uid-1"); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(DirFor(root, "uid-1"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0777 {
		t.Fatalf("perm %o", st.Mode().Perm())
	}
}

func TestWriteReadCIDFile(t *testing.T) {
	dir := t.TempDir()
	if err := WriteCIDFile(dir, "uid-1", "517921949"); err != nil {
		t.Fatal(err)
	}
	n, err := ReadCIDFile(CIDPath(dir, "uid-1"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 517921949 {
		t.Fatalf("cid %d", n)
	}
}

func TestWriteCIDFileRejectsGarbage(t *testing.T) {
	dir := t.TempDir()
	if err := WriteCIDFile(dir, "uid-1", "nope"); err == nil {
		t.Fatal("expected error")
	}
}

func TestResolveGuestCIDLocalIgnoresFile(t *testing.T) {
	n, err := ResolveGuestCIDWithMode("local", filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatal(err)
	}
	if n != LocalCID {
		t.Fatalf("got %d want LocalCID", n)
	}
}

func TestResolveGuestCIDGlobalWaitsForFile(t *testing.T) {
	_, err := ResolveGuestCIDWithMode("", filepath.Join(t.TempDir(), "missing"))
	if err == nil {
		t.Fatal("expected missing file")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cid"), []byte("42\n"), 0644); err != nil {
		t.Fatal(err)
	}
	n, err := ResolveGuestCIDWithMode("global", filepath.Join(dir, "cid"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 42 {
		t.Fatalf("got %d", n)
	}
}
