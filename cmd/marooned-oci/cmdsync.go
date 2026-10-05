package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	launcherSockRel = "run/kubevirt/sockets/launcher-sock"
	cmdSyncMethod   = "/kubevirt.cmd.v1.Cmd/SyncVirtualMachine"
	cmdSyncWait     = 60 * time.Second
	// DomainDefineXML is ~60-90s (isolated-busybox-wait started at 81s).
	// waitForSock returns as soon as launcher-sock exists, so create is
	// typically this call, under kubelet's 2m runtime timeout.
	cmdSyncCall          = 2 * time.Minute
	tapDomainAttachment  = "tap"
	defaultInterfaceName = "default"
)

type bytesCodec struct{}

func (bytesCodec) Marshal(v interface{}) ([]byte, error) {
	b, ok := v.([]byte)
	if !ok {
		return nil, fmt.Errorf("cmd codec: want []byte")
	}
	return b, nil
}

func (bytesCodec) Unmarshal(data []byte, v interface{}) error {
	p, ok := v.(*[]byte)
	if !ok {
		return fmt.Errorf("cmd codec: want *[]byte")
	}
	*p = append([]byte(nil), data...)
	return nil
}

func (bytesCodec) Name() string   { return "proto" }
func (bytesCodec) String() string { return "proto" }

func launcherSockPath(rootfs string) string {
	if rootfs == "" {
		return "/" + launcherSockRel
	}
	return filepath.Join(rootfs, launcherSockRel)
}

// launcherSockViaProc is short enough for AF_UNIX (sun_path 108).
// Overlay path /run/marooned-oci/<64-id>/vl-rootfs/run/kubevirt/sockets/launcher-sock is 127 bytes; Dial then retries until context deadline.
func launcherSockViaProc(pid int) string {
	if pid <= 1 {
		return ""
	}
	return fmt.Sprintf("/proc/%d/root/%s", pid, launcherSockRel)
}

// syncStandaloneVMI is virt-handler's SyncVirtualMachine for this Pod:
// virt-launcher v1.9 HandleStandaloneMode passes nil VirtualMachineOptions,
// so we start without STANDALONE_VMI and send domainAttachments plus host
// CPU topology (dedicatedCPUPlacement vcpupin panics if topology is nil).
func syncStandaloneVMI(rootfs, spec string, attachTap bool, pid int) error {
	sock := launcherSockViaProc(pid)
	if sock == "" {
		sock = launcherSockPath(rootfs)
	}
	if err := waitForSock(sock, cmdSyncWait); err != nil {
		return err
	}
	attachments := map[string]string{}
	if attachTap {
		attachments[defaultInterfaceName] = tapDomainAttachment
	}
	req := marshalVMIRequest([]byte(spec), attachments, hostTopologyProto())
	resp, err := cmdInvoke(sock, cmdSyncMethod, req, cmdSyncCall)
	if err != nil {
		return err
	}
	ok, msg, err := parseCmdResponse(resp)
	if err != nil {
		return err
	}
	if !ok {
		if msg == "" {
			msg = "SyncVirtualMachine failed"
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

func waitForSock(path string, d time.Duration) error {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		st, err := os.Stat(path)
		if err == nil && st.Mode()&os.ModeSocket != 0 {
			return nil
		}
		// overlay tmpfs can report a unix socket as a regular file
		// after rename; still try if it exists.
		if err == nil && !st.IsDir() {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for %s", path)
}

func cmdInvoke(socket, method string, req []byte, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	conn, err := grpc.DialContext(ctx, "passthrough:///cmd",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		}),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	var out []byte
	if err := conn.Invoke(ctx, method, req, &out, grpc.ForceCodec(bytesCodec{})); err != nil {
		return nil, err
	}
	return out, nil
}

func marshalVMIRequest(vmiJSON []byte, attachments map[string]string, topology []byte) []byte {
	vmi := protoBytes(1, vmiJSON)
	req := protoBytes(1, vmi)
	var opts []byte
	if topology != nil {
		opts = append(opts, protoBytes(4, topology)...)
	}
	for k, v := range attachments {
		entry := protoBytes(1, []byte(k))
		entry = append(entry, protoBytes(2, []byte(v))...)
		opts = append(opts, protoBytes(8, entry)...)
	}
	if len(opts) == 0 {
		return req
	}
	return append(req, protoBytes(2, opts)...)
}

func protoBytes(field int, payload []byte) []byte {
	out := protoVarint(uint64(field<<3 | 2))
	out = append(out, protoVarint(uint64(len(payload)))...)
	return append(out, payload...)
}

func protoVarint(x uint64) []byte {
	var b []byte
	for x >= 0x80 {
		b = append(b, byte(x)|0x80)
		x >>= 7
	}
	return append(b, byte(x))
}

func parseCmdResponse(b []byte) (ok bool, msg string, err error) {
	// proto3 omits success=false; only success=true encodes field 1.
	for len(b) > 0 {
		key, n := consumeVarint(b)
		if n <= 0 {
			return false, "", fmt.Errorf("truncated response")
		}
		b = b[n:]
		field, wt := int(key>>3), int(key&7)
		switch wt {
		case 0:
			v, n := consumeVarint(b)
			if n <= 0 {
				return false, "", fmt.Errorf("truncated varint")
			}
			b = b[n:]
			if field == 1 {
				ok = v != 0
			}
		case 2:
			ln, n := consumeVarint(b)
			if n <= 0 || uint64(len(b)-n) < ln {
				return false, "", fmt.Errorf("truncated bytes")
			}
			payload := b[n : n+int(ln)]
			b = b[n+int(ln):]
			if field == 2 {
				msg = string(payload)
			}
		default:
			return false, "", fmt.Errorf("wire type %d", wt)
		}
	}
	return ok, msg, nil
}

func consumeVarint(b []byte) (uint64, int) {
	var x uint64
	var s uint
	for i, c := range b {
		if c < 0x80 {
			if i > 9 {
				return 0, 0
			}
			return x | uint64(c)<<s, i + 1
		}
		x |= uint64(c&0x7f) << s
		s += 7
		if i > 9 {
			return 0, 0
		}
	}
	return 0, 0
}
