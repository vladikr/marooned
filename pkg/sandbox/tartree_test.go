package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirSizeSkipsProc(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "proc"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "proc", "x"), []byte("nope"), 0644); err != nil {
		t.Fatal(err)
	}
	if DirSize(dir) != 5 {
		t.Fatalf("size %d want 5", DirSize(dir))
	}
}
