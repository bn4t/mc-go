package server

import (
	"context"

	"errors"
	"io"
	"mc-go/api/frontend"
	"net"
	"sync"
	"testing"
	"time"
)

// delayedAcceptListener models Accept obtaining a connection before shutdown
// while the acceptor has not yet registered it. Close cannot revoke that result.
type delayedAcceptListener struct {
	conn     net.Conn
	accepted chan struct{}
	release  chan struct{}
	closed   chan struct{}
	once     sync.Once
}

func (l *delayedAcceptListener) Accept() (net.Conn, error) {
	close(l.accepted)
	<-l.release
	return l.conn, nil
}
func (l *delayedAcceptListener) Close() error   { l.once.Do(func() { close(l.closed) }); return nil }
func (l *delayedAcceptListener) Addr() net.Addr { return &net.TCPAddr{} }

func TestRconShutdownJoinsDelayedAccept(t *testing.T) {
	s := New("unused")
	defer s.Stop()
	client, server := net.Pipe()
	defer client.Close()
	ln := &delayedAcceptListener{conn: server, accepted: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{})}
	r := &rconListener{srv: s, ln: ln, stop: make(chan struct{})}
	r.wg.Add(1)
	go func() { defer r.wg.Done(); r.accept() }()
	<-ln.accepted
	done := make(chan struct{})
	go func() { r.close(); close(done) }()
	<-ln.closed
	select {
	case <-done:
		t.Fatal("close returned before accepted connection was retired")
	default:
	}
	close(ln.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("late accept survived shutdown")
	}
	client.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := client.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("late accepted socket not closed: %v", err)
	}
	r.close() // joined shutdown remains idempotent
}

func TestRuntimeStopClosesIdleHandshake(t *testing.T) {
	s := New("unused")
	s.StartFrozen = true
	defer s.Stop()
	ln := runtimeListener(t)
	done := make(chan error, 1)
	go func() { done <- s.Serve(ln) }()
	client, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		tracked := false
		s.sessions.Range(func(_, _ any) bool { tracked = true; return false })
		if tracked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("connection was not tracked")
		}
		time.Sleep(time.Millisecond)
	}
	stopped := make(chan struct{})
	go func() { s.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("idle handshake blocked Stop")
	}
	awaitServeExit(t, done)
	client.SetReadDeadline(time.Now().Add(time.Second))
	if _, err = client.Read(make([]byte, 1)); err == nil {
		t.Fatal("idle handshake socket survived Stop")
	}
}

func TestRuntimeStopDrainsExternalFrontendAction(t *testing.T) {
	s := New("unused")
	defer s.Stop()
	entered, release := make(chan struct{}), make(chan struct{})
	s.commands.Register("hold_runtime", func(string, []string) string {
		close(entered)
		<-release
		return "finished"
	})
	actionDone := make(chan struct{})
	go func() {
		_, _ = (frontendCore{s}).Execute(context.Background(), frontend.Action{Kind: frontend.Command, Command: "hold_runtime"})
		close(actionDone)
	}()
	<-entered
	stopped := make(chan struct{})
	go func() { s.Stop(); close(stopped) }()
	deadline := time.Now().Add(time.Second)
	for !s.ShuttingDown() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	// No tick loop or Java connection is present to provide an incidental join.
	// The simulation barrier must retain the world until this frontend action ends.
	select {
	case <-stopped:
		t.Fatal("Stop retired world during external action")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case <-actionDone:
	case <-time.After(time.Second):
		t.Fatal("external action blocked")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not finish after action")
	}
}
