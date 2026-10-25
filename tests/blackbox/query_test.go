package blackbox

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"testing"
	"time"

	"mc-go/internal/server"
)

// queryExchange sends one UDP datagram and returns the reply, or nil.
func queryExchange(t *testing.T, addr string, pkt []byte) []byte {
	t.Helper()
	c, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatalf("query dial %s: %v", addr, err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := c.Write(pkt); err != nil {
		t.Fatalf("query write: %v", err)
	}
	buf := make([]byte, 65536)
	n, err := c.Read(buf)
	if err != nil {
		return nil
	}
	return append([]byte(nil), buf[:n]...)
}

func queryStatPkt(sid, tok int32, full bool) []byte {
	pkt := []byte{0xfe, 0xfd, 0}
	pkt = binary.BigEndian.AppendUint32(pkt, uint32(sid))
	pkt = binary.BigEndian.AppendUint32(pkt, uint32(tok))
	if full {
		pkt = append(pkt, 0, 0, 0, 0)
	}
	return pkt
}

// maskQueryPort replaces the two host-port encodings (BE short in the
// basic stat, decimal string in the full stat) so servers on different
// ports compare.
func maskQueryPort(b []byte) []byte {
	out := append([]byte(nil), b...)
	// basic tail: <port BE short><ip>\x00 — the ip is printable; find
	// "127.0.0.1" and mask the two bytes right before it
	if i := bytes.Index(out, []byte("127.0.0.1")); i >= 2 {
		out[i-2], out[i-1] = 'P', 'P'
	}
	// full stat: "hostport\x00<digits>\x00" — mask the digits
	if i := bytes.Index(out, []byte("hostport\x00")); i >= 0 {
		j := i + len("hostport\x00")
		for j < len(out) && out[j] != 0 {
			out[j] = 'P'
			j++
		}
	}
	return out
}

// TestQueryParity exercises the GameSpy4 query protocol on both
// servers: the challenge handshake, basic stat, the 15-byte full stat,
// a rejected bad token, and dropped garbage.
func TestQueryParity(t *testing.T) {
	vPort, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked")
	}
	mQPort, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked")
	}
	targets := requireBlackboxFiles(t,
		map[string]string{
			"enable-query": "true",
			"query.port":   fmt.Sprint(vPort),
			"motd":         "A Minecraft Server",
		},
		nil,
		func(s *server.Server) {
			s.EnableQuery = true
			s.QueryAddr = fmt.Sprintf("127.0.0.1:%d", mQPort)
			s.MOTD = "A Minecraft Server"
		})
	defer targets.cleanup()

	handshake := func(addr string, sid uint32) []byte {
		pkt := []byte{0xfe, 0xfd, 9}
		pkt = binary.BigEndian.AppendUint32(pkt, sid)
		return queryExchange(t, addr, pkt)
	}

	vAddr := fmt.Sprintf("127.0.0.1:%d", vPort)
	mAddr := fmt.Sprintf("127.0.0.1:%d", mQPort)
	vHS, mHS := handshake(vAddr, 0x11223344), handshake(mAddr, 0x11223344)
	// type 9, ident echoed verbatim, decimal token + NUL
	if len(vHS) < 6 || len(mHS) < 6 || vHS[0] != 9 || mHS[0] != 9 {
		t.Fatalf("handshake: vanilla %x mc-go %x", vHS, mHS)
	}
	if string(vHS[1:5]) != string(mHS[1:5]) {
		t.Fatalf("handshake ident: vanilla %x mc-go %x", vHS[1:5], mHS[1:5])
	}
	tokenOf := func(resp []byte) uint32 {
		var tok uint32
		if _, err := fmt.Sscanf(string(resp[5:]), "%d", &tok); err != nil {
			t.Fatalf("token parse %q: %v", resp, err)
		}
		return tok
	}
	vTok, mTok := tokenOf(vHS), tokenOf(mHS)

	// The stat response echoes the HANDSHAKE ident, not the request sid:
	want := queryExchange(t, vAddr, queryStatPkt(0x99, int32(vTok), false))
	got := queryExchange(t, mAddr, queryStatPkt(0x99, int32(mTok), false))
	if w, g := maskQueryPort(want), maskQueryPort(got); !bytes.Equal(w, g) {
		t.Errorf("basic stat:\n  vanilla %q\n  mc-go   %q", w, g)
	}

	want = queryExchange(t, vAddr, queryStatPkt(0x99, int32(vTok), true))
	got = queryExchange(t, mAddr, queryStatPkt(0x99, int32(mTok), true))
	if w, g := maskQueryPort(want), maskQueryPort(got); !bytes.Equal(w, g) {
		t.Errorf("full stat:\n  vanilla %q\n  mc-go   %q", w, g)
	}

	// 8-byte padding is NOT a full-stat request — answers basic.
	want = queryExchange(t, vAddr, append(queryStatPkt(0x99, int32(vTok), true), 0, 0, 0, 0))
	got = queryExchange(t, mAddr, append(queryStatPkt(0x99, int32(mTok), true), 0, 0, 0, 0))
	if w, g := maskQueryPort(want), maskQueryPort(got); !bytes.Equal(w, g) {
		t.Errorf("pad-8 stat:\n  vanilla %q\n  mc-go   %q", w, g)
	}

	for _, addr := range []string{vAddr, mAddr} {
		if r := queryExchange(t, addr, queryStatPkt(0x99, 1, false)); r != nil {
			t.Errorf("%s answered a bad token: %x", addr, r)
		}
		if r := queryExchange(t, addr, []byte{1, 2, 3, 4}); r != nil {
			t.Errorf("%s answered garbage: %x", addr, r)
		}
	}
}
