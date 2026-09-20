package cri

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"k8s.io/klog/v2"

	"maroonedpods.io/maroonedpods/pkg/sandbox"
	"maroonedpods.io/maroonedpods/pkg/sandbox/agentproto"
)

// ErrUnimplemented is returned by the Phase 0 empty shim.
var ErrUnimplemented = fmt.Errorf("unimplemented")

// RuntimeService is the CRI subset kubelet uses for a single-container pod.
type RuntimeService interface {
	RunPodSandbox(ctx context.Context, req *RunPodSandboxRequest) (*PodSandbox, error)
	StopPodSandbox(ctx context.Context, id string) error
	RemovePodSandbox(ctx context.Context, id string) error
	PodSandboxStatus(ctx context.Context, id string) (*PodSandbox, error)
	CreateContainer(ctx context.Context, sandboxID string, req *CreateContainerRequest) (*Container, error)
	StartContainer(ctx context.Context, id string) error
	StopContainer(ctx context.Context, id string, timeout time.Duration) error
	RemoveContainer(ctx context.Context, id string) error
	ContainerStatus(ctx context.Context, id string) (*Container, error)
	ExecSync(ctx context.Context, id string, cmd []string, timeout time.Duration) (stdout, stderr []byte, exitCode int32, err error)
	Logs(ctx context.Context, id string) (string, error)
	ExecTTY(ctx context.Context, id string, cmd []string, conn net.Conn) error
	ListPodSandbox(ctx context.Context) ([]*PodSandbox, error)
	ListContainers(ctx context.Context) ([]*Container, error)
}

type RunPodSandboxRequest struct {
	PodName      string
	PodNamespace string
	PodUID       string
	Attempt      uint32
	RootfsBytes  int64
}

type CreateContainerRequest struct {
	Name       string
	Image      string
	Command    []string
	Args       []string
	Env        []string
	WorkDir    string
	RootfsPath  string
	RootfsBytes int64
}

type PodSandbox struct {
	ID        string
	Name      string
	Namespace string
	UID       string
	State     string
	VMI       string
}

type Container struct {
	ID        string
	SandboxID string
	Name      string
	Image     string
	Command   []string
	Args      []string
	Env       []string
	WorkDir    string
	RootfsPath  string
	RootfsBytes int64
	State       string
}

// UnimplementedRuntime is the Phase 0 shim.
type UnimplementedRuntime struct{}

func (UnimplementedRuntime) RunPodSandbox(context.Context, *RunPodSandboxRequest) (*PodSandbox, error) {
	return nil, ErrUnimplemented
}
func (UnimplementedRuntime) StopPodSandbox(context.Context, string) error   { return ErrUnimplemented }
func (UnimplementedRuntime) RemovePodSandbox(context.Context, string) error { return ErrUnimplemented }
func (UnimplementedRuntime) PodSandboxStatus(context.Context, string) (*PodSandbox, error) {
	return nil, ErrUnimplemented
}
func (UnimplementedRuntime) CreateContainer(context.Context, string, *CreateContainerRequest) (*Container, error) {
	return nil, ErrUnimplemented
}
func (UnimplementedRuntime) StartContainer(context.Context, string) error { return ErrUnimplemented }
func (UnimplementedRuntime) StopContainer(context.Context, string, time.Duration) error {
	return ErrUnimplemented
}
func (UnimplementedRuntime) RemoveContainer(context.Context, string) error { return ErrUnimplemented }
func (UnimplementedRuntime) ContainerStatus(context.Context, string) (*Container, error) {
	return nil, ErrUnimplemented
}
func (UnimplementedRuntime) ExecSync(context.Context, string, []string, time.Duration) ([]byte, []byte, int32, error) {
	return nil, nil, 0, ErrUnimplemented
}
func (UnimplementedRuntime) Logs(context.Context, string) (string, error) { return "", ErrUnimplemented }
func (UnimplementedRuntime) ExecTTY(context.Context, string, []string, net.Conn) error {
	return ErrUnimplemented
}
func (UnimplementedRuntime) ListPodSandbox(context.Context) ([]*PodSandbox, error) {
	return nil, ErrUnimplemented
}
func (UnimplementedRuntime) ListContainers(context.Context) ([]*Container, error) {
	return nil, ErrUnimplemented
}

// Store holds in-memory sandbox and container records.
type Store struct {
	mu         sync.Mutex
	sandboxes  map[string]*PodSandbox
	containers map[string]*Container
	agentAddrs map[string]string
	logBuffers map[string][]byte
}

func NewStore() *Store {
	return &Store{
		sandboxes:  map[string]*PodSandbox{},
		containers: map[string]*Container{},
		agentAddrs: map[string]string{},
		logBuffers: map[string][]byte{},
	}
}

func (s *Store) PutSandbox(sb *PodSandbox) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sandboxes[sb.ID] = sb
}

func (s *Store) GetSandbox(id string) *PodSandbox {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sandboxes[id]
}

func (s *Store) DeleteSandbox(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sandboxes, id)
	delete(s.agentAddrs, id)
}

func (s *Store) SetAgentAddr(id, addr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agentAddrs[id] = addr
}

func (s *Store) AgentAddr(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.agentAddrs[id]
}

func (s *Store) PutContainer(c *Container) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.containers[c.ID] = c
}

func (s *Store) GetContainer(id string) *Container {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.containers[id]
}

func (s *Store) DeleteContainer(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.containers, id)
	delete(s.logBuffers, id)
}

func (s *Store) AppendLog(id string, b []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logBuffers[id] = append(s.logBuffers[id], b...)
}

func (s *Store) Log(id string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.logBuffers[id]...)
}

func (s *Store) ListSandboxes() []*PodSandbox {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*PodSandbox, 0, len(s.sandboxes))
	for _, sb := range s.sandboxes {
		out = append(out, sb)
	}
	return out
}

func (s *Store) ListContainers() []*Container {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Container, 0, len(s.containers))
	for _, c := range s.containers {
		out = append(out, c)
	}
	return out
}

// AgentDialer opens a connection to the guest agent for a sandbox.
type AgentDialer func(sandboxID string) (*agentproto.Client, error)

// Runtime is the Phase 2 CRI implementation.
type Runtime struct {
	store    *Store
	dial     AgentDialer
	waitVM   func(ctx context.Context, podNamespace, podName string) (vmiRef, agentAddr string, err error)
	noteSize func(ns, name string, n int64)
}

func NewRuntime(store *Store, waitVM func(context.Context, string, string) (string, string, error), dial AgentDialer) *Runtime {
	return &Runtime{store: store, waitVM: waitVM, dial: dial}
}

func (r *Runtime) SetNoteSize(fn func(ns, name string, n int64)) {
	r.noteSize = fn
}

func (r *Runtime) RunPodSandbox(ctx context.Context, req *RunPodSandboxRequest) (*PodSandbox, error) {
	id := req.PodUID
	if id == "" {
		id = req.PodNamespace + "-" + req.PodName
	}
	klog.Infof("RunPodSandbox %s/%s", req.PodNamespace, req.PodName)
	if r.noteSize != nil && req.RootfsBytes > 0 {
		r.noteSize(req.PodNamespace, req.PodName, req.RootfsBytes)
	}
	if existing := r.store.GetSandbox(id); existing != nil && existing.State == "SANDBOX_READY" && r.store.AgentAddr(id) != "" {
		return existing, nil
	}
	var last error
	n := 0
	for {
		vmi, addr, err := r.waitVM(ctx, req.PodNamespace, req.PodName)
		if err != nil {
			if last != nil {
				return nil, fmt.Errorf("agent ping: %v; %w", last, err)
			}
			return nil, err
		}
		r.store.SetAgentAddr(id, addr)
		if r.dial == nil {
			sb := &PodSandbox{ID: id, Name: req.PodName, Namespace: req.PodNamespace, UID: req.PodUID, State: "SANDBOX_READY", VMI: vmi}
			r.store.PutSandbox(sb)
			return sb, nil
		}
		cli, err := r.dial(id)
		if err == nil {
			err = cli.Ping(3 * time.Second)
			_ = cli.Close()
			if err == nil {
				sb := &PodSandbox{ID: id, Name: req.PodName, Namespace: req.PodNamespace, UID: req.PodUID, State: "SANDBOX_READY", VMI: vmi}
				r.store.PutSandbox(sb)
				return sb, nil
			}
		}
		last = err
		n++
		if n == 1 || n%5 == 0 {
			klog.Infof("waiting for guest agent at %s (%d): %v", addr, n, last)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("agent ping: %w", last)
		case <-time.After(time.Second):
		}
	}
}

func (r *Runtime) StopPodSandbox(_ context.Context, id string) error {
	sb := r.store.GetSandbox(id)
	if sb != nil {
		sb.State = "SANDBOX_NOTREADY"
		r.store.PutSandbox(sb)
	}
	return nil
}

func (r *Runtime) RemovePodSandbox(_ context.Context, id string) error {
	r.store.DeleteSandbox(id)
	return nil
}

func (r *Runtime) PodSandboxStatus(_ context.Context, id string) (*PodSandbox, error) {
	sb := r.store.GetSandbox(id)
	if sb == nil {
		return nil, fmt.Errorf("sandbox %s not found", id)
	}
	return sb, nil
}

func (r *Runtime) CreateContainer(_ context.Context, sandboxID string, req *CreateContainerRequest) (*Container, error) {
	id := sandboxID + "-" + req.Name
	c := &Container{ID: id, SandboxID: sandboxID, Name: req.Name, Image: req.Image, Command: req.Command, Args: req.Args, Env: req.Env, WorkDir: req.WorkDir, RootfsPath: req.RootfsPath, RootfsBytes: req.RootfsBytes, State: "CONTAINER_CREATED"}
	r.store.PutContainer(c)
	return c, nil
}

func (r *Runtime) StartContainer(ctx context.Context, id string) error {
	c := r.store.GetContainer(id)
	if c == nil {
		return fmt.Errorf("container %s not found", id)
	}
	if r.dial == nil {
		c.State = "CONTAINER_RUNNING"
		r.store.PutContainer(c)
		return nil
	}
	cli, err := r.dial(c.SandboxID)
	if err != nil {
		return err
	}
	defer cli.Close()
	imageBytes := c.RootfsBytes
	if imageBytes == 0 && c.RootfsPath != "" {
		if st, err := os.Stat(c.RootfsPath); err == nil {
			imageBytes = st.Size()
		}
	}
	qty := sandbox.UserRootfsCapacity(imageBytes)
	disk := qty.Value()
	if _, err := cli.Call(agentproto.MethodPrepareRootfs, agentproto.PrepareRootfsRequest{
		ContainerID: id,
		Serial:      sandbox.UserRootfsSerial,
		ImageBytes:  imageBytes,
		DiskBytes:   disk,
	}, 90*time.Second); err != nil {
		return fmt.Errorf("prepare user-rootfs (image %d bytes, disk %d bytes): %w", imageBytes, disk, err)
	}
	if c.RootfsPath != "" {
		f, err := os.Open(c.RootfsPath)
		if err != nil {
			return fmt.Errorf("rootfs %s: %w", c.RootfsPath, err)
		}
		putErr := cli.PutRootfs(id, f, 3*time.Minute)
		_ = f.Close()
		if putErr != nil {
			return fmt.Errorf("rootfs upload: %w", putErr)
		}
	}
	_, err = cli.Call(agentproto.MethodStart, agentproto.StartRequest{
		ContainerID: id,
		Image:       c.Image,
		Command:     c.Command,
		Args:        c.Args,
		Env:         c.Env,
		WorkDir:     c.WorkDir,
	}, 60*time.Second)
	if err != nil {
		return err
	}
	c.State = "CONTAINER_RUNNING"
	r.store.PutContainer(c)
	return nil
}

func (r *Runtime) StopContainer(_ context.Context, id string, _ time.Duration) error {
	c := r.store.GetContainer(id)
	if c == nil {
		return nil
	}
	if r.dial != nil {
		if cli, err := r.dial(c.SandboxID); err == nil {
			_, _ = cli.Call(agentproto.MethodStop, map[string]string{"containerID": id}, 15*time.Second)
			_ = cli.Close()
		}
	}
	c.State = "CONTAINER_EXITED"
	r.store.PutContainer(c)
	return nil
}

func (r *Runtime) RemoveContainer(_ context.Context, id string) error {
	r.store.DeleteContainer(id)
	return nil
}

func (r *Runtime) ContainerStatus(_ context.Context, id string) (*Container, error) {
	c := r.store.GetContainer(id)
	if c == nil {
		return nil, fmt.Errorf("container %s not found", id)
	}
	return c, nil
}

func (r *Runtime) ExecSync(_ context.Context, id string, cmd []string, timeout time.Duration) ([]byte, []byte, int32, error) {
	c := r.store.GetContainer(id)
	if c == nil {
		return nil, nil, 1, fmt.Errorf("container %s not found", id)
	}
	if r.dial == nil {
		return nil, nil, 1, ErrUnimplemented
	}
	cli, err := r.dial(c.SandboxID)
	if err != nil {
		return nil, nil, 1, err
	}
	defer cli.Close()
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	resp, err := cli.Call(agentproto.MethodExec, agentproto.ExecRequest{ContainerID: id, Command: cmd, TimeoutSec: int64(timeout.Seconds())}, timeout)
	if err != nil {
		return nil, nil, 1, err
	}
	var out agentproto.ExecResponse
	if len(resp.Payload) > 0 {
		if err := json.Unmarshal(resp.Payload, &out); err != nil {
			return nil, nil, 1, err
		}
	}
	return []byte(out.Stdout), []byte(out.Stderr), out.ExitCode, nil
}

func (r *Runtime) Logs(_ context.Context, id string) (string, error) {
	c := r.store.GetContainer(id)
	if c == nil {
		return "", fmt.Errorf("container %s not found", id)
	}
	if r.dial == nil {
		return "", ErrUnimplemented
	}
	cli, err := r.dial(c.SandboxID)
	if err != nil {
		return "", err
	}
	defer cli.Close()
	resp, err := cli.Call(agentproto.MethodLogs, agentproto.LogsRequest{ContainerID: id}, 10*time.Second)
	if err != nil {
		return "", err
	}
	var out agentproto.LogsResponse
	if len(resp.Payload) > 0 {
		_ = json.Unmarshal(resp.Payload, &out)
	}
	return out.Data, nil
}

func (r *Runtime) ExecTTY(_ context.Context, id string, cmd []string, conn net.Conn) error {
	c := r.store.GetContainer(id)
	if c == nil {
		return fmt.Errorf("container %s not found", id)
	}
	if r.dial == nil {
		return ErrUnimplemented
	}
	cli, err := r.dial(c.SandboxID)
	if err != nil {
		return err
	}
	defer cli.Close()
	if err := agentproto.WriteEnvelope(cli.Conn(), agentproto.Envelope{ID: "1", Method: agentproto.MethodExecTTY, Payload: mustJSON(agentproto.ExecRequest{ContainerID: id, Command: cmd})}); err != nil {
		return err
	}
	ack, err := agentproto.ReadEnvelope(cli.Conn())
	if err != nil {
		return err
	}
	if !ack.OK {
		return fmt.Errorf("ExecTTY: %s", ack.Error)
	}
	errc := make(chan error, 2)
	go func() {
		_, e := io.Copy(cli.Conn(), conn)
		errc <- e
	}()
	go func() {
		_, e := io.Copy(conn, cli.Conn())
		errc <- e
	}()
	<-errc
	return nil
}

func mustJSON(v interface{}) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func (r *Runtime) ListPodSandbox(context.Context) ([]*PodSandbox, error) {
	return r.store.ListSandboxes(), nil
}

func (r *Runtime) ListContainers(context.Context) ([]*Container, error) {
	return r.store.ListContainers(), nil
}
