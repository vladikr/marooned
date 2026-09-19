package cri

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"maroonedpods.io/maroonedpods/pkg/sandbox/agentproto"
)

func TestUnimplemented(t *testing.T) {
	r := UnimplementedRuntime{}
	_, err := r.RunPodSandbox(context.Background(), &RunPodSandboxRequest{PodName: "x"})
	if err != ErrUnimplemented {
		t.Fatalf("got %v", err)
	}
}

func TestStore(t *testing.T) {
	s := NewStore()
	s.PutSandbox(&PodSandbox{ID: "a", Name: "n"})
	if s.GetSandbox("a") == nil {
		t.Fatal("missing")
	}
	s.SetAgentAddr("a", "tcp:127.0.0.1:1024")
	if s.AgentAddr("a") != "tcp:127.0.0.1:1024" {
		t.Fatal("addr")
	}
	s.DeleteSandbox("a")
	if s.GetSandbox("a") != nil {
		t.Fatal("not deleted")
	}
}

func TestRunPodSandboxNotReadyUntilPing(t *testing.T) {
	store := NewStore()
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	r := NewRuntime(store, func(context.Context, string, string) (string, string, error) {
		return "vmi", "vsock:1:1024", nil
	}, func(string) (*agentproto.Client, error) {
		return nil, fmt.Errorf("protocol not supported")
	})
	_, err := r.RunPodSandbox(ctx, &RunPodSandboxRequest{PodName: "p", PodNamespace: "ns", PodUID: "uid"})
	if err == nil {
		t.Fatal("expected ping failure")
	}
	if sb := store.GetSandbox("uid"); sb != nil && sb.State == "SANDBOX_READY" {
		t.Fatal("sandbox must not be READY before a successful ping")
	}
}

func TestRunPodSandboxReadyAfterPing(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	go func() {
		defer a.Close()
		for {
			env, err := agentproto.ReadEnvelope(a)
			if err != nil {
				return
			}
			_ = agentproto.WriteEnvelope(a, agentproto.Envelope{ID: env.ID, Method: env.Method, OK: true})
		}
	}()
	store := NewStore()
	r := NewRuntime(store, func(context.Context, string, string) (string, string, error) {
		return "vmi", "vsock:1:1024", nil
	}, func(string) (*agentproto.Client, error) {
		return agentproto.NewClient(b), nil
	})
	sb, err := r.RunPodSandbox(context.Background(), &RunPodSandboxRequest{PodName: "p", PodNamespace: "ns", PodUID: "uid"})
	if err != nil {
		t.Fatal(err)
	}
	if sb.State != "SANDBOX_READY" {
		t.Fatalf("state %s", sb.State)
	}
	if store.GetSandbox("uid") == nil {
		t.Fatal("sandbox not stored")
	}
}
