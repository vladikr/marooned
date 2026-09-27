package sandbox

import (
	"archive/tar"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// SkipRootfsRel skips host kernel mounts that are not the image.
func SkipRootfsRel(rel string) bool {
	first, _, _ := strings.Cut(rel, string(os.PathSeparator))
	switch first {
	case "proc", "sys", "dev", "run", "tmp":
		return true
	}
	return false
}

func fileDev(info os.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return st.Dev
	}
	return 0
}

// DirSize is the unpacked image size (hardlinks once, host mounts skipped).
func DirSize(root string) int64 {
	root = filepath.Clean(root)
	st, err := os.Stat(root)
	if err != nil {
		return 0
	}
	rootDev := fileDev(st)
	seen := map[uint64]struct{}{}
	var n int64
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if rel != "." && SkipRootfsRel(rel) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if rootDev != 0 && fileDev(info) != 0 && fileDev(info) != rootDev {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Nlink > 1 {
			if _, dup := seen[st.Ino]; dup {
				return nil
			}
			seen[st.Ino] = struct{}{}
		}
		n += info.Size()
		return nil
	})
	return n
}

// TarTree writes an OCI-rootfs tar of root to w (runc's tree, not a registry pull).
func TarTree(w io.Writer, root string) error {
	tw := tar.NewWriter(w)
	defer tw.Close()
	root = filepath.Clean(root)
	st, err := os.Stat(root)
	if err != nil {
		return err
	}
	srcDev := fileDev(st)
	hardlinks := map[uint64]string{}
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if SkipRootfsRel(rel) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if srcDev != 0 && fileDev(info) != 0 && fileDev(info) != srcDev {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = rel
		if st, ok := info.Sys().(*syscall.Stat_t); ok && info.Mode().IsRegular() && st.Nlink > 1 {
			if first, ok := hardlinks[st.Ino]; ok {
				hdr.Typeflag = tar.TypeLink
				hdr.Linkname = first
				hdr.Size = 0
				return tw.WriteHeader(hdr)
			}
			hardlinks[st.Ino] = rel
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			hdr.Linkname = link
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tw, in)
		_ = in.Close()
		return copyErr
	})
}
