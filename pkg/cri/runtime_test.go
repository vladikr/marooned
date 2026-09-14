package cri

import (
	"context"
	"testing"
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
