package server_test

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"mc-go/api/frontend"
	"mc-go/server"
)

// jsonStatus is an external adapter: its entire implementation imports only
// public contracts and stdlib, and has no concrete server/session access.
type jsonStatus struct{}

func (jsonStatus) Name() string { return "json-status" }
func (jsonStatus) Serve(ctx context.Context, core frontend.Core, ln net.Listener) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		snapshot, err := core.Snapshot(ctx)
		if err == nil {
			_ = json.NewEncoder(conn).Encode(snapshot)
		}
		conn.Close()
	}
}

func TestExternalFrontendUsesPublicRuntime(t *testing.T) {
	s := server.New("127.0.0.1:0")
	s.MOTD = "shared runtime"
	s.StartFrozen = true
	defer s.Stop()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan error, 1)
	go func() { done <- s.Attach(jsonStatus{}, ln) }()
	conn, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	var snapshot frontend.Snapshot
	if err = json.NewDecoder(conn).Decode(&snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.MOTD != "shared runtime" || !snapshot.Frozen {
		t.Fatalf("public snapshot=%+v", snapshot)
	}
	ln.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("external frontend did not detach")
	}
	if err = s.Start(); err != nil {
		t.Fatalf("detach stopped runtime: %v", err)
	}
}
