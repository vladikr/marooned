package vsock

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	DefaultHostDir = "/var/run/marooned"
	AgentSockName  = "agent.sock"
	CIDFileName    = "cid"
	AgentPort      = 1024
	// LocalCID is unix.VMADDR_CID_LOCAL; used when vsock ns_mode is "local".
	LocalCID uint32 = 1
	// QEMUUser is KubeVirt's compute uid; the vsockfwd sidecar runs as this.
	QEMUUser = 107
)

func DirFor(hostDir, uid string) string {
	if hostDir == "" {
		hostDir = DefaultHostDir
	}
	return filepath.Join(hostDir, uid)
}

func CIDPath(hostDir, uid string) string {
	return filepath.Join(DirFor(hostDir, uid), CIDFileName)
}

func AgentSockPath(hostDir, uid string) string {
	return filepath.Join(DirFor(hostDir, uid), AgentSockName)
}

// UnixDialAddr is the shim-side agent address: "unix:/path/agent.sock".
func UnixDialAddr(hostDir, uid string) string {
	return "unix:" + AgentSockPath(hostDir, uid)
}

func mkdirWorld(dir string) error {
	if err := os.MkdirAll(dir, 0777); err != nil {
		return err
	}
	return os.Chmod(dir, 0777)
}

// EnsureSandboxDir creates the per-pod directory as root and gives uid 107
// ownership so the virt-launcher sidecar can bind agent.sock. The sidecar
// must not mkdir: /var/run/marooned is root 0755.
func EnsureSandboxDir(hostDir, uid string) error {
	dir := DirFor(hostDir, uid)
	if err := mkdirWorld(dir); err != nil {
		return err
	}
	_ = os.Chown(dir, QEMUUser, QEMUUser)
	return nil
}

// WriteCIDFile stores the guest CID as plain-text uint32. The node-local
// shim writes this after the adaptor copies vmi.status.VSOCKCID onto the
// user Pod; the virt-launcher sidecar cannot see the API and tails the file.
func WriteCIDFile(hostDir, uid, cid string) error {
	cid = strings.TrimSpace(cid)
	if cid == "" {
		return fmt.Errorf("empty cid")
	}
	if _, err := strconv.ParseUint(cid, 10, 32); err != nil {
		return fmt.Errorf("cid %q: %w", cid, err)
	}
	if err := EnsureSandboxDir(hostDir, uid); err != nil {
		return err
	}
	path := CIDPath(hostDir, uid)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(cid+"\n"), 0644); err != nil {
		return err
	}
	_ = os.Chmod(tmp, 0644)
	return os.Rename(tmp, path)
}

func ReadCIDFile(path string) (uint32, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return 0, fmt.Errorf("cid file empty")
	}
	n, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0, err
	}
	return uint32(n), nil
}

// ResolveGuestCIDWithMode picks LocalCID when ns_mode is "local", otherwise
// the cid file. Empty ns_mode means global (host) vsock.
func ResolveGuestCIDWithMode(nsMode, cidFile string) (uint32, error) {
	if nsMode == "local" {
		return LocalCID, nil
	}
	return ReadCIDFile(cidFile)
}

func ResolveGuestCID(hostDir, uid string) (uint32, error) {
	return ResolveGuestCIDWithMode(ReadNSMode(), CIDPath(hostDir, uid))
}

// RelabelTree sets the virt-launcher file context on dir and its children
// so container_t can bind agent.sock and read cid.
func RelabelTree(dir, context string) error {
	if err := Relabel(dir, context); err != nil {
		return err
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var last error
	for _, e := range ents {
		if err := Relabel(filepath.Join(dir, e.Name()), context); err != nil {
			last = err
		}
	}
	return last
}
