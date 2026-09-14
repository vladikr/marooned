package agentproto

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

const (
	MethodPing       = "Ping"
	MethodStart      = "Start"
	MethodStop       = "Stop"
	MethodWait       = "Wait"
	MethodExec       = "Exec"
	MethodLogs       = "Logs"
	MethodMountTable = "MountTable"
	MethodAttest     = "Attest"
)

// Envelope is a length-prefixed JSON request or response.
type Envelope struct {
	ID      string          `json:"id"`
	Method  string          `json:"method"`
	OK      bool            `json:"ok"`
	Error   string          `json:"error,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// StartRequest starts a workload process in the guest.
type StartRequest struct {
	ContainerID string   `json:"containerID"`
	Image       string   `json:"image"`
	Command     []string `json:"command"`
	Args        []string `json:"args"`
	Env         []string `json:"env"`
	WorkDir     string   `json:"workDir"`
	Mounts      []Mount  `json:"mounts"`
	Privileged  bool     `json:"privileged"`
	Stdin       bool     `json:"stdin"`
}

// Mount is a guest path already backed by a virtio disk or tmpfs.
type Mount struct {
	VolumeName string `json:"volumeName"`
	GuestPath  string `json:"guestPath"`
	Kind       string `json:"kind"`
	ReadOnly   bool   `json:"readOnly"`
}

// ExecRequest runs a command in the container namespace.
type ExecRequest struct {
	ContainerID string   `json:"containerID"`
	Command     []string `json:"command"`
	TimeoutSec  int64    `json:"timeoutSec"`
}

// ExecResponse is the result of Exec.
type ExecResponse struct {
	ExitCode int32  `json:"exitCode"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

// LogsRequest streams or dumps container logs.
type LogsRequest struct {
	ContainerID string `json:"containerID"`
	Follow      bool   `json:"follow"`
}

// LogsResponse is a log chunk.
type LogsResponse struct {
	Stream string `json:"stream"` // stdout|stderr
	Data   string `json:"data"`
}

// WriteEnvelope writes a 4-byte big-endian length then JSON.
func WriteEnvelope(w io.Writer, env Envelope) error {
	b, err := json.Marshal(env)
	if err != nil {
		return err
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(b)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// ReadEnvelope reads one length-prefixed JSON envelope.
func ReadEnvelope(r io.Reader) (Envelope, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return Envelope{}, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 || n > 16*1024*1024 {
		return Envelope{}, fmt.Errorf("invalid envelope size %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return Envelope{}, err
	}
	var env Envelope
	if err := json.Unmarshal(buf, &env); err != nil {
		return Envelope{}, err
	}
	return env, nil
}

// Client talks to the guest agent over a reliable stream (vsock or tcp).
type Client struct {
	conn net.Conn
	mu   sync.Mutex
	seq  uint64
}

func NewClient(conn net.Conn) *Client {
	return &Client{conn: conn}
}

func (c *Client) Close() error {
	return c.conn.Close()
}

func (c *Client) Call(method string, payload interface{}, timeout time.Duration) (Envelope, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	id := fmt.Sprintf("%d", c.seq)
	var raw json.RawMessage
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return Envelope{}, err
		}
		raw = b
	}
	if timeout > 0 {
		_ = c.conn.SetDeadline(time.Now().Add(timeout))
		defer c.conn.SetDeadline(time.Time{})
	}
	if err := WriteEnvelope(c.conn, Envelope{ID: id, Method: method, Payload: raw}); err != nil {
		return Envelope{}, err
	}
	resp, err := ReadEnvelope(c.conn)
	if err != nil {
		return Envelope{}, err
	}
	if !resp.OK {
		return resp, fmt.Errorf("%s: %s", method, resp.Error)
	}
	return resp, nil
}

func (c *Client) Ping(timeout time.Duration) error {
	_, err := c.Call(MethodPing, nil, timeout)
	return err
}
