package main

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
)

func TestMarshalVMIRequestTap(t *testing.T) {
	spec := []byte(`{"kind":"VirtualMachineInstance"}`)
	b := marshalVMIRequest(spec, map[string]string{"default": "tap"})
	if !bytes.Contains(b, spec) {
		t.Fatal("missing vmi json")
	}
	if !bytes.Contains(b, []byte("default")) || !bytes.Contains(b, []byte("tap")) {
		t.Fatalf("missing attachment %q", b)
	}
	if !bytes.Contains(b, spec) {
		t.Fatal("vmi")
	}
}

func TestMarshalVMIRequestFields(t *testing.T) {
	spec := []byte(`{"kind":"VirtualMachineInstance"}`)
	b := marshalVMIRequest(spec, map[string]string{"default": "tap"})
	vmiMsg, rest, ok := protoField(b, 1)
	if !ok {
		t.Fatal("vmi")
	}
	got, extra, ok := protoField(vmiMsg, 1)
	if !ok || !bytes.Equal(got, spec) || len(extra) != 0 {
		t.Fatalf("vmiJson %q extra %d", got, len(extra))
	}
	opts, rest, ok := protoField(rest, 2)
	if !ok || len(rest) != 0 {
		t.Fatal("options")
	}
	entry, rest, ok := protoField(opts, 8)
	if !ok || len(rest) != 0 {
		t.Fatal("attachment")
	}
	k, rest, ok := protoField(entry, 1)
	if !ok || string(k) != "default" {
		t.Fatalf("key %q", k)
	}
	v, rest, ok := protoField(rest, 2)
	if !ok || string(v) != "tap" || len(rest) != 0 {
		t.Fatalf("val %q", v)
	}
}

func protoField(b []byte, want int) (payload, rest []byte, ok bool) {
	key, n := consumeVarint(b)
	if n <= 0 {
		return nil, b, false
	}
	field, wt := int(key>>3), int(key&7)
	if field != want || wt != 2 {
		return nil, b, false
	}
	ln, n2 := consumeVarint(b[n:])
	if n2 <= 0 {
		return nil, b, false
	}
	start := n + n2
	end := start + int(ln)
	if end > len(b) {
		return nil, b, false
	}
	return b[start:end], b[end:], true
}

func TestMarshalVMIRequestNoAttach(t *testing.T) {
	spec := []byte(`{"kind":"VirtualMachineInstance"}`)
	b := marshalVMIRequest(spec, nil)
	if bytes.Contains(b, []byte("tap")) {
		t.Fatal("unexpected tap")
	}
	if !bytes.Contains(b, spec) {
		t.Fatal("vmi")
	}
}

func TestParseCmdResponse(t *testing.T) {
	ok, msg, err := parseCmdResponse(nil)
	if err != nil || !ok || msg != "" {
		t.Fatalf("empty %v %v %q", err, ok, msg)
	}
	// success=false (field 1 varint 0), message "boom" (field 2)
	fail := append(protoVarint(1<<3|0), 0)
	fail = append(fail, protoBytes(2, []byte("boom"))...)
	ok, msg, err = parseCmdResponse(fail)
	if err != nil || ok || msg != "boom" {
		t.Fatalf("fail %v ok=%v msg=%q", err, ok, msg)
	}
	okb := append(protoVarint(1<<3|0), 1)
	ok, msg, err = parseCmdResponse(okb)
	if err != nil || !ok || msg != "" {
		t.Fatalf("ok %v %v %q", err, ok, msg)
	}
}

func TestLauncherSockPath(t *testing.T) {
	if launcherSockPath("/overlay") != "/overlay/run/kubevirt/sockets/launcher-sock" {
		t.Fatal(launcherSockPath("/overlay"))
	}
	got := launcherSockViaProc(100482)
	want := "/proc/100482/root/run/kubevirt/sockets/launcher-sock"
	if got != want {
		t.Fatal(got)
	}
	if len(got) > 107 {
		t.Fatalf("AF_UNIX path %d", len(got))
	}
	if launcherSockViaProc(0) != "" || launcherSockViaProc(1) != "" {
		t.Fatal("pid <=1")
	}
}

func TestWaitForSockTimeout(t *testing.T) {
	err := waitForSock(filepath.Join(t.TempDir(), "missing.sock"), 0)
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("err %v", err)
	}
}

func TestWaitForSockFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s")
	if err := os.WriteFile(p, []byte{}, 0600); err != nil {
		t.Fatal(err)
	}
	if err := waitForSock(p, time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestWaitForSockUnix(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.sock")
	l, err := net.Listen("unix", p)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := waitForSock(p, time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestCmdInvoke(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "s.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	s := grpc.NewServer(
		grpc.CustomCodec(bytesCodec{}),
		grpc.UnknownServiceHandler(func(_ interface{}, stream grpc.ServerStream) error {
			var in []byte
			if err := stream.RecvMsg(&in); err != nil {
				return err
			}
			if !bytes.Contains(in, []byte("tap")) {
				return fmt.Errorf("missing tap")
			}
			return stream.SendMsg(append(protoVarint(1<<3|0), 1))
		}),
	)
	go s.Serve(l)
	defer s.Stop()
	req := marshalVMIRequest([]byte(`{"kind":"VirtualMachineInstance"}`), map[string]string{"default": "tap"})
	resp, err := cmdInvoke(sock, cmdSyncMethod, req, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ok, msg, err := parseCmdResponse(resp)
	if err != nil || !ok || msg != "" {
		t.Fatalf("resp %v ok=%v msg=%q", err, ok, msg)
	}
}
