package conformance_test

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"mc-go/conformance"
)

// This TCP fixture deliberately uses its own framing and pinned bytes rather
// than the runner codec or production server/protocol packages.
func vint(n int) []byte {
	var b []byte
	for {
		v := byte(n & 127)
		n >>= 7
		if n != 0 {
			v |= 128
		}
		b = append(b, v)
		if n == 0 {
			return b
		}
	}
}
func readN(c net.Conn) (int, error) {
	n := 0
	for i := 0; i < 5; i++ {
		var b [1]byte
		if _, e := io.ReadFull(c, b[:]); e != nil {
			return 0, e
		}
		n |= int(b[0]&127) << uint(i*7)
		if b[0] < 128 {
			return n, nil
		}
	}
	return 0, io.ErrUnexpectedEOF
}
func receive(c net.Conn, compressed bool) ([]byte, error) {
	n, e := readN(c)
	if e != nil {
		return nil, e
	}
	if n < 1 || n > 2<<20 {
		return nil, io.ErrUnexpectedEOF
	}
	b := make([]byte, n)
	_, e = io.ReadFull(c, b)
	if e == nil && compressed {
		size, k := binary.Uvarint(b)
		if k <= 0 {
			return nil, io.ErrUnexpectedEOF
		}
		if size == 0 {
			b = b[k:]
		} else {
			zr, err := zlib.NewReader(bytes.NewReader(b[k:]))
			if err != nil {
				return nil, err
			}
			b, err = io.ReadAll(zr)
			_ = zr.Close()
			if err != nil {
				return nil, err
			}
		}
	}
	return b, e
}
func text(s string) []byte { return append(vint(len(s)), s...) }
func reply(c net.Conn, id byte, p []byte, compression int) {
	body := append([]byte{id}, p...)
	if compression >= 0 {
		if len(body) >= compression {
			var b bytes.Buffer
			z := zlib.NewWriter(&b)
			_, _ = z.Write(body)
			_ = z.Close()
			body = append(vint(len(body)), b.Bytes()...)
		} else {
			body = append([]byte{0}, body...)
		}
	}
	frame := append(vint(len(body)), body...)
	_, _ = c.Write(frame)
}

type behavior struct {
	corrupt     string
	compression int
	cancel      context.CancelFunc
}

func endpoint(t *testing.T, b behavior) string {
	t.Helper()
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Skipf("TCP fixture: %v", e)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var clients []net.Conn
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			mu.Lock()
			clients = append(clients, c)
			mu.Unlock()
			wg.Add(1)
			go func() { defer wg.Done(); defer c.Close(); serve(c, b) }()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		for _, c := range clients {
			_ = c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	return ln.Addr().String()
}
func serve(c net.Conn, b behavior) {
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	h, e := receive(c, false)
	if e != nil {
		if b.corrupt == "accept-oversize" {
			var one [1]byte
			_, _ = c.Read(one[:])
		}
		return
	}
	// Last field is the handshake's next state. Invalid state must close.
	next := h[len(h)-1]
	if next == 99 {
		if b.cancel != nil {
			b.cancel()
			var one [1]byte
			_, _ = c.Read(one[:])
			return
		}
		if b.corrupt == "accept-invalid-state" {
			var one [1]byte
			_, _ = c.Read(one[:])
		}
		return
	}
	if next == 1 {
		p, e := receive(c, false)
		if e != nil {
			return
		}
		if p[0] == 1 {
			payload := p[1:]
			if b.corrupt == "pong" {
				payload = make([]byte, 8)
			}
			reply(c, 1, payload, -1)
			return
		}
		s := `{"version":{"name":"26.2","protocol":776},"players":{"max":20,"online":0},"description":{"text":"fixture"}}`
		switch b.corrupt {
		case "schema":
			s = `{"version":{"name":"26.2","protocol":776}}`
		case "protocol":
			s = strings.Replace(s, "776", "775", 1)
		case "json":
			s = "{"
		case "varint":
			_, _ = c.Write([]byte{0x80, 0x80, 0x80, 0x80, 0x10})
			return
		case "long-frame-prefix":
			_, _ = c.Write([]byte{0x81, 0x80, 0x80, 0, 0})
			return
		case "frame":
			_, _ = c.Write([]byte{0xff, 0xff, 0xff, 0x7f})
			return
		case "timeout":
			var one [1]byte
			_, _ = c.Read(one[:])
			return
		}
		payload := text(s)
		if b.corrupt == "trailing-status" {
			payload = append(payload, 0)
		}
		id := byte(0)
		if b.corrupt == "status-id" {
			id = 9
		}
		reply(c, id, payload, -1)
		return
	}
	if next != 2 {
		return
	}
	if _, e = receive(c, false); e != nil {
		return
	}
	if b.compression >= 0 {
		reply(c, 3, vint(b.compression), -1)
	}
	// MD5 OfflinePlayer:Conformance with UUID v3 bits; properties=0,
	// then the independently specified 26.2 session UUID.
	id, _ := hex.DecodeString("eb0450abdedd37c9a7c9fbd5b33bd860")
	payload := append(id, text("Conformance")...)
	payload = append(payload, 0)
	payload = append(payload, make([]byte, 16)...)
	if b.corrupt == "uuid" {
		payload[0] ^= 1
	}
	if b.corrupt == "session" {
		payload = payload[:len(payload)-16]
	}
	if b.corrupt == "compression-length" {
		_, _ = c.Write([]byte{2, 127, 0})
		return
	}
	reply(c, 2, payload, b.compression)
	if _, e = receive(c, b.compression >= 0); e != nil {
		return
	}
	// Registry entries use independently authored empty compound NBT.
	registry := func(name string) []byte {
		p := append(text(name), 1)
		p = append(p, text("minecraft:overworld")...)
		return append(p, 1, 10, 0)
	}
	feature := append([]byte{1}, text("minecraft:vanilla")...)
	if b.corrupt == "features-empty" {
		feature = nil
	}
	if b.corrupt == "features-count" {
		feature = []byte{1}
	}
	if b.corrupt == "features-trailing" {
		feature = append(feature, 0)
	}
	reply(c, 0x0c, feature, b.compression)
	if b.corrupt == "registry-order" {
		reply(c, 7, registry("minecraft:dimension_type"), b.compression)
	}
	reply(c, 0x0e, []byte{0}, b.compression)
	if _, e = receive(c, b.compression >= 0); e != nil {
		return
	}
	if b.corrupt != "registry" {
		reg := registry("minecraft:dimension_type")
		switch b.corrupt {
		case "registry-entry":
			reg = append(text("minecraft:dimension_type"), 1)
		case "registry-nbt":
			reg = reg[:len(reg)-1]
		case "registry-bool":
			reg[len(reg)-3] = 2
		case "registry-trailing":
			reg = append(reg, 0)
		}
		reply(c, 7, reg, b.compression)
		reply(c, 7, registry("minecraft:worldgen/biome"), b.compression)
		if b.corrupt == "registry-duplicate" {
			reply(c, 7, reg, b.compression)
		}
	}
	tag := []byte{0}
	switch b.corrupt {
	case "tags-empty":
		tag = nil
	case "tags-entry":
		tag = append([]byte{1}, text("minecraft:block")...)
		tag = append(tag, 1)
	case "tags-trailing":
		tag = append(tag, 0)
	}
	reply(c, 0x0d, tag, b.compression)
	if b.corrupt == "finish" {
		return
	}
	finish := []byte(nil)
	if b.corrupt == "finish-body" {
		finish = []byte{1}
	}
	reply(c, 3, finish, b.compression)
	if _, e = receive(c, b.compression >= 0); e != nil {
		return
	}
	join := make([]byte, 4)
	binary.BigEndian.PutUint32(join, 42)
	join = append(join, 0, 1)
	join = append(join, text("minecraft:overworld")...)
	join = append(join, 20, 2, 2, 0, 1, 0, 0)
	join = append(join, text("minecraft:overworld")...)
	join = append(join, make([]byte, 8)...)
	join = append(join, 1, 255, 0, 1, 0, 0, 63, 0, 0)
	switch b.corrupt {
	case "join":
		join = join[:2]
	case "join-five":
		join = join[:5]
	case "join-tail":
		join = join[:len(join)-1]
	case "join-trailing":
		join = append(join, 0)
	case "join-bool":
		join[4] = 2
	}
	reply(c, 0x31, join, b.compression)
}
func config(addr string, caps ...string) conformance.Config {
	return conformance.Config{Endpoint: addr, Protocol: 776, Version: "26.2", Capabilities: caps, Timeout: 150 * time.Millisecond}
}
func find(t *testing.T, r conformance.Report, name string) conformance.Result {
	t.Helper()
	for _, v := range r.Results {
		if v.Scenario == name {
			return v
		}
	}
	t.Fatalf("missing scenario %s", name)
	return conformance.Result{}
}
func TestTCPProfileAndTranscripts(t *testing.T) {
	for _, compression := range []int{-1, 0, 256} {
		t.Run(fmt.Sprintf("threshold-%d", compression), func(t *testing.T) {
			addr := endpoint(t, behavior{compression: compression})
			c := config(addr, "status", "ping", "offline-login", "configuration", "malformed")
			c.Timeout = time.Second
			r := conformance.Run(context.Background(), c)
			if r.ExitCode() != 0 {
				t.Fatalf("report: %+v", r.Results)
			}
			for _, v := range r.Results {
				if v.Status != conformance.Pass || len(v.Transcript) < 2 {
					t.Fatalf("result: %+v", v)
				}
			}
			p := find(t, r, "status.ping-echo")
			if !strings.HasSuffix(p.Transcript[1].WireHex, "0901fedcba9876543210") {
				t.Fatalf("ping capture: %+v", p.Transcript)
			}
		})
	}
}
func TestBadEndpointFailsContracts(t *testing.T) {
	cases := []struct{ bad, cap, scenario string }{
		{"schema", "status", "status.schema"}, {"protocol", "status", "status.schema"}, {"json", "status", "status.schema"}, {"status-id", "status", "status.schema"}, {"trailing-status", "status", "status.schema"}, {"varint", "status", "status.schema"}, {"frame", "status", "status.schema"}, {"timeout", "status", "status.schema"},
		{"pong", "ping", "status.ping-echo"}, {"uuid", "offline-login", "login.offline-identity"}, {"session", "offline-login", "login.offline-identity"}, {"compression-length", "offline-login", "login.offline-identity"},
		{"registry", "configuration", "configuration.finish-and-join"}, {"finish", "configuration", "configuration.finish-and-join"}, {"finish-body", "configuration", "configuration.finish-and-join"}, {"join", "configuration", "configuration.finish-and-join"},

		{"long-frame-prefix", "status", "status.schema"},
		{"registry-order", "configuration", "configuration.finish-and-join"},
		{"registry-entry", "configuration", "configuration.finish-and-join"},
		{"registry-nbt", "configuration", "configuration.finish-and-join"},
		{"registry-bool", "configuration", "configuration.finish-and-join"},
		{"registry-trailing", "configuration", "configuration.finish-and-join"},
		{"registry-duplicate", "configuration", "configuration.finish-and-join"},
		{"features-empty", "configuration", "configuration.finish-and-join"},
		{"features-count", "configuration", "configuration.finish-and-join"},
		{"features-trailing", "configuration", "configuration.finish-and-join"},
		{"tags-empty", "configuration", "configuration.finish-and-join"},
		{"tags-entry", "configuration", "configuration.finish-and-join"},
		{"tags-trailing", "configuration", "configuration.finish-and-join"},
		{"join-five", "configuration", "configuration.finish-and-join"},
		{"join-tail", "configuration", "configuration.finish-and-join"},
		{"join-trailing", "configuration", "configuration.finish-and-join"},
		{"join-bool", "configuration", "configuration.finish-and-join"},
		{"accept-oversize", "malformed", "framing.reject-oversize"}, {"accept-invalid-state", "malformed", "framing.reject-invalid-state"},
	}
	for _, tc := range cases {
		t.Run(tc.bad, func(t *testing.T) {
			compression := -1
			if tc.bad == "compression-length" {
				compression = 0
			}
			addr := endpoint(t, behavior{corrupt: tc.bad, compression: compression})
			r := conformance.Run(context.Background(), config(addr, tc.cap))
			v := find(t, r, tc.scenario)
			if v.Status != conformance.Fail || r.ExitCode() != 1 {
				t.Fatalf("bad endpoint passed: %+v", r)
			}
			if len(v.Transcript) == 0 {
				t.Fatal("failure lost transcript")
			}
		})
	}
}
func TestUnsupportedInfrastructureAndTruncation(t *testing.T) {
	addr := endpoint(t, behavior{compression: -1})
	r := conformance.Run(context.Background(), config(addr, "status"))
	if find(t, r, "configuration.finish-and-join").Status != conformance.Unsupported {
		t.Fatal(r)
	}
	c := config(addr, "offline-login")
	c.Protocol = 775
	r = conformance.Run(context.Background(), c)
	if find(t, r, "login.offline-identity").Status != conformance.Unsupported {
		t.Fatal(r)
	}
	c = config(addr, "status")
	c.TranscriptLimit = 1
	r = conformance.Run(context.Background(), c)
	if !r.Results[0].Transcript[0].Truncated || len(r.Results[0].Transcript[0].WireHex) != 2 {
		t.Fatal(r)
	}
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	closed := ln.Addr().String()
	_ = ln.Close()
	r = conformance.Run(context.Background(), config(closed, "status"))
	if r.ExitCode() != 2 || r.Results[0].Status != conformance.Infrastructure {
		t.Fatal(r)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r = conformance.Run(ctx, config(addr, "status"))
	if r.ExitCode() != 2 {
		t.Fatal(r)
	}
	c = config(addr, "invented")
	r = conformance.Run(context.Background(), c)
	if r.ExitCode() != 2 {
		t.Fatal(r)
	}
}

func TestCanceledRejectionIsInfrastructure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr := endpoint(t, behavior{compression: -1, cancel: cancel})
	report := conformance.Run(ctx, config(addr, "malformed"))
	result := find(t, report, "framing.reject-invalid-state")
	if result.Status != conformance.Infrastructure || report.ExitCode() != 2 {
		t.Fatalf("cancellation became a protocol pass: %+v", report)
	}
}

func TestDeclaredUnsupportedProfileCannotPassCI(t *testing.T) {
	report := conformance.Run(context.Background(), conformance.Config{Endpoint: "example.invalid:25565", Protocol: 775, Capabilities: []string{"configuration"}})
	result := find(t, report, "configuration.finish-and-join")
	if result.Status != conformance.Unsupported || !result.Declared || report.ExitCode() != 2 || report.Reason == "" {
		t.Fatalf("unsupported profile passed: %+v", report)
	}
	partial := conformance.Report{Results: []conformance.Result{{Status: conformance.Pass, Declared: true}, {Status: conformance.Unsupported, Declared: true}}}
	if partial.ExitCode() != 2 {
		t.Fatal("an unsupported declared check was hidden by a supported pass")
	}
	partial.Results[1].Declared = false
	if partial.ExitCode() != 0 {
		t.Fatal("omitted capabilities failed a supported run")
	}
}
