package session

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/mc-go/server/internal/packets/v1211"
	"github.com/mc-go/server/internal/proto"
	"go.uber.org/zap"
)

// Test that writePacketNow flushes frames for Status without needing the async writer.
func TestStatusWritePacketNowFlushes(t *testing.T) {
	srv, cli := net.Pipe()
	defer srv.Close()
	defer cli.Close()

	log, _ := zap.NewDevelopment()
	s := &Session{
		conn: srv,
		br:   bufio.NewReader(srv),
		bw:   bufio.NewWriter(srv),
		log:  log,
		motd: "test",
		maxP: 5,
		out:  make(chan []byte, 16),
	}

	// Reader goroutine: read two frames (StatusResponse then Pong)
	done := make(chan struct{})
	errc := make(chan error, 1)
	go func() {
		defer close(done)
		br := bufio.NewReader(cli)
		// Status response
		body1, _, err := proto.ReadFrame(br)
		if err != nil {
			errc <- err
			return
		}
		pbr := bufio.NewReader(bytes.NewReader(body1))
		pid1, _, err := proto.ReadVarInt(pbr)
		if err != nil {
			errc <- err
			return
		}
		if pid1 != v1211.PIDSStatusResponse {
			errc <- fmt.Errorf("got pid1=0x%x", pid1)
			return
		}
		// Pong
		body2, _, err := proto.ReadFrame(br)
		if err != nil {
			errc <- err
			return
		}
		pbr2 := bufio.NewReader(bytes.NewReader(body2))
		pid2, _, err := proto.ReadVarInt(pbr2)
		if err != nil {
			errc <- err
			return
		}
		if pid2 != v1211.PIDSPong {
			errc <- fmt.Errorf("got pid2=0x%x", pid2)
			return
		}
	}()

	// Write response + pong synchronously
	resp := v1211.SStatusResponse{JSON: `{"version":{"name":"1.21.1","protocol":767},"players":{"max":5,"online":1},"description":{"text":"test"}}`}
	if err := s.writePacketNow(resp.ID(), resp.EncodeBody()); err != nil {
		t.Fatalf("write status: %v", err)
	}
	pong := v1211.SPong{Payload: 123}
	if err := s.writePacketNow(pong.ID(), pong.EncodeBody()); err != nil {
		t.Fatalf("write pong: %v", err)
	}

	select {
	case <-done:
	case err := <-errc:
		t.Fatalf("read error: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for frames")
	}
}

// no extra helpers
