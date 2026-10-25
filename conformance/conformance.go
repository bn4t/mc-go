// Package conformance probes a Minecraft Java TCP endpoint without importing
// server code, launching a local implementation, or requiring a vanilla jar.
package conformance

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

type Status string

const (
	Pass           Status = "pass"
	Fail           Status = "fail"
	Unsupported    Status = "unsupported"
	Infrastructure Status = "infrastructure"
)

// Config declares the endpoint contract. Capabilities opt into scenarios;
// absent capabilities are reported as unsupported, never counted as passes.
// Supported names: status, ping, offline-login, configuration, malformed.
type Config struct {
	Endpoint        string        `json:"endpoint"`
	Protocol        int32         `json:"protocol"`
	Version         string        `json:"version,omitempty"`
	HandshakeHost   string        `json:"handshake_host,omitempty"`
	Capabilities    []string      `json:"capabilities"`
	Username        string        `json:"username,omitempty"`
	Timeout         time.Duration `json:"-"`
	TimeoutMS       int           `json:"timeout_ms,omitempty"`
	TranscriptLimit int           `json:"transcript_limit_bytes,omitempty"`
}
type Event struct {
	Direction string `json:"direction"`
	PacketID  int32  `json:"packet_id"`
	WireBytes int    `json:"wire_bytes"`
	WireHex   string `json:"wire_hex,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Error     string `json:"error,omitempty"`
}
type Result struct {
	Scenario   string  `json:"scenario"`
	Declared   bool    `json:"declared"`
	Status     Status  `json:"status"`
	Detail     string  `json:"detail"`
	DurationMS int64   `json:"duration_ms"`
	Transcript []Event `json:"transcript,omitempty"`
}
type Report struct {
	Schema  string    `json:"schema"`
	Reason  string    `json:"reason,omitempty"`
	Target  Config    `json:"target"`
	Started time.Time `json:"started"`
	Results []Result  `json:"results"`
}

// ExitCode is 2 for configuration/infrastructure errors, 1 for protocol
// failures, and 0 when every declared scenario passed. An unsupported declared
// profile or a run with no executed checks returns 2. Omitted capabilities stay
// visible as unsupported without failing an otherwise passing run.
func (r Report) ExitCode() int {
	code, executed, unsupportedDeclared := 0, false, false
	for _, v := range r.Results {
		switch v.Status {
		case Infrastructure:
			return 2
		case Fail:
			code, executed = 1, true
		case Pass:
			executed = true
		case Unsupported:
			unsupportedDeclared = unsupportedDeclared || v.Declared
		}
	}
	if unsupportedDeclared || !executed {
		return 2
	}
	return code
}

type scenario struct {
	name, cap       string
	profileRequired bool
	run             func(*wire, Config) error
}

var scenarios = []scenario{
	{"status.schema", "status", false, status},
	{"status.ping-echo", "ping", false, ping},
	{"login.offline-identity", "offline-login", true, func(w *wire, c Config) error { return login(w, c) }},
	{"configuration.finish-and-join", "configuration", true, configuration},
	{"framing.reject-invalid-state", "malformed", false, invalidState},
	{"framing.reject-oversize", "malformed", false, oversize},
	{"framing.status-after-rejections", "malformed", false, status},
}

// Run executes isolated TCP sessions sequentially. Dial/setup errors are
// infrastructure errors; after connection, missing/malformed replies fail the
// declared protocol contract, including read timeouts.
func Run(ctx context.Context, c Config) Report {
	if c.Protocol == 0 {
		c.Protocol = 776
	}
	if c.Username == "" {
		c.Username = "Conformance"
	}
	if c.Timeout <= 0 {
		c.Timeout = time.Duration(c.TimeoutMS) * time.Millisecond
	}
	if c.Timeout <= 0 {
		c.Timeout = 10 * time.Second
	}
	if c.TranscriptLimit <= 0 {
		c.TranscriptLimit = 16 * 1024 * 1024
	}
	c.TimeoutMS = int(c.Timeout / time.Millisecond)
	r := Report{Schema: "mc-conformance/v1", Target: c, Started: time.Now().UTC()}
	if e := validate(c); e != nil {
		r.Results = []Result{{Scenario: "configuration", Status: Infrastructure, Detail: e.Error()}}
		return r
	}
	caps := map[string]bool{}
	for _, v := range c.Capabilities {
		caps[v] = true
	}
	for _, s := range scenarios {
		out := Result{Scenario: s.name, Declared: caps[s.cap], Status: Unsupported, Detail: "capability not declared: " + s.cap}
		if !caps[s.cap] {
			r.Results = append(r.Results, out)
			continue
		}
		if _, supported := profileFor(c.Protocol); s.profileRequired && !supported {
			out.Detail = "no scenario profile for declared protocol (supported: 775 and 776)"
			r.Results = append(r.Results, out)
			continue
		}
		start := time.Now()
		conn, e := (&net.Dialer{Timeout: c.Timeout}).DialContext(ctx, "tcp", c.Endpoint)
		if e != nil {
			out.Status = Infrastructure
			out.Detail = "dial: " + e.Error()
		} else {
			deadline := start.Add(c.Timeout)
			if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
				deadline = d
			}
			_ = conn.SetDeadline(deadline)
			stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
			budget := c.TranscriptLimit
			w := &wire{conn: conn, threshold: -1, events: &out.Transcript, budget: &budget}
			e = s.run(w, c)
			stop()
			_ = conn.Close()
			out.Status = Pass
			out.Detail = "contract satisfied"
			if e != nil {
				out.Status = Fail
				out.Detail = e.Error()
			}
			if ctx.Err() != nil {
				out.Status = Infrastructure
				out.Detail = ctx.Err().Error()
			}
		}
		out.DurationMS = time.Since(start).Milliseconds()
		r.Results = append(r.Results, out)
	}
	executed := false
	for _, result := range r.Results {
		if result.Status == Pass || result.Status == Fail {
			executed = true
		}
		if result.Status == Unsupported && result.Declared {
			r.Reason = "a declared scenario has no supported protocol profile"
		}
	}
	if !executed && r.Reason == "" {
		r.Reason = "no protocol scenarios executed"
	}
	return r
}
func validate(c Config) error {
	if _, _, e := net.SplitHostPort(c.Endpoint); e != nil {
		return fmt.Errorf("endpoint must be host:port: %w", e)
	}
	if c.Protocol < 1 {
		return fmt.Errorf("protocol must be positive")
	}
	if len(c.Username) > 16 || len(c.Username) < 1 {
		return fmt.Errorf("username must have 1..16 ASCII letters, digits or underscores")
	}
	for _, v := range c.Username {
		if !(v >= 'a' && v <= 'z' || v >= 'A' && v <= 'Z' || v >= '0' && v <= '9' || v == '_') {
			return fmt.Errorf("invalid username")
		}
	}
	if len(c.Capabilities) == 0 {
		return fmt.Errorf("declare at least one capability")
	}
	for _, v := range c.Capabilities {
		switch v {
		case "status", "ping", "offline-login", "configuration", "malformed":
		default:
			return fmt.Errorf("unknown capability %q", v)
		}
	}
	return nil
}
func expect(w *wire, id int32) (packet, error) {
	p, e := w.read()
	if e != nil {
		return p, e
	}
	if p.id != id {
		return p, fmt.Errorf("packet ID %#x, want %#x", p.id, id)
	}
	return p, nil
}
func status(w *wire, c Config) error {
	if e := w.handshake(c, 1); e != nil {
		return e
	}
	if e := w.send(0, nil); e != nil {
		return e
	}
	p, e := expect(w, 0)
	if e != nil {
		return e
	}
	r := bytes.NewReader(p.data)
	s, e := takeString(r)
	if e != nil {
		return e
	}
	if r.Len() != 0 {
		return fmt.Errorf("trailing status bytes")
	}
	var v struct {
		Version *struct {
			Name     string `json:"name"`
			Protocol *int32 `json:"protocol"`
		} `json:"version"`
		Players *struct {
			Max    *int `json:"max"`
			Online *int `json:"online"`
		} `json:"players"`
		Description json.RawMessage `json:"description"`
	}
	if e = json.Unmarshal([]byte(s), &v); e != nil {
		return fmt.Errorf("status JSON: %w", e)
	}
	if v.Version == nil || v.Version.Protocol == nil || v.Version.Name == "" || v.Players == nil || v.Players.Max == nil || v.Players.Online == nil || len(v.Description) == 0 || bytes.Equal(v.Description, []byte("null")) {
		return fmt.Errorf("missing required status fields")
	}
	if *v.Version.Protocol != c.Protocol {
		return fmt.Errorf("advertised protocol %d, want %d", *v.Version.Protocol, c.Protocol)
	}
	if c.Version != "" && v.Version.Name != c.Version {
		return fmt.Errorf("advertised version %q, want %q", v.Version.Name, c.Version)
	}
	if *v.Players.Max < 0 || *v.Players.Online < 0 {
		return fmt.Errorf("negative player counts")
	}
	return nil
}
func ping(w *wire, c Config) error {
	if e := w.handshake(c, 1); e != nil {
		return e
	}
	// A negative, byte-order-sensitive signed 64-bit value catches truncated,
	// byte-swapped and constant pongs. Servers may close after one pong.
	b := binary.BigEndian.AppendUint64(nil, 0xfedcba9876543210)
	if e := w.send(1, b); e != nil {
		return e
	}
	p, e := expect(w, 1)
	if e != nil {
		return e
	}
	if !bytes.Equal(p.data, b) {
		return fmt.Errorf("pong payload %x, want %x", p.data, b)
	}
	return nil
}
func login(w *wire, c Config) error {
	if e := w.handshake(c, 2); e != nil {
		return e
	}
	b := str(nil, c.Username)
	b = append(b, make([]byte, 16)...)
	if e := w.send(0, b); e != nil {
		return e
	}
	p, e := w.read()
	if e != nil {
		return e
	}
	if p.id == 3 {
		r := bytes.NewReader(p.data)
		n, e := readInt(r)
		if e != nil || n < 0 || r.Len() != 0 {
			return fmt.Errorf("invalid compression threshold")
		}
		w.threshold = n
		p, e = w.read()
		if e != nil {
			return e
		}
	}
	if p.id != 2 {
		return fmt.Errorf("offline login returned %#x, want success 0x02 (online auth requires a different profile)", p.id)
	}
	r := bytes.NewReader(p.data)
	var id [16]byte
	if _, e = io.ReadFull(r, id[:]); e != nil {
		return e
	}
	want := md5.Sum([]byte("OfflinePlayer:" + c.Username))
	want[6] = (want[6] & 15) | 0x30
	want[8] = (want[8] & 63) | 0x80
	if id != want {
		return fmt.Errorf("offline UUID %x, want %x", id, want)
	}
	name, e := takeString(r)
	if e != nil {
		return e
	}
	if name != c.Username {
		return fmt.Errorf("login username %q, want %q", name, c.Username)
	}
	n, e := readInt(r)
	if e != nil || n < 0 || n > 1024 {
		return fmt.Errorf("invalid login property count")
	}
	for i := int32(0); i < n; i++ {
		if _, e = takeString(r); e != nil {
			return e
		}
		if _, e = takeString(r); e != nil {
			return e
		}
		signed, e := r.ReadByte()
		if e != nil {
			return e
		}
		if signed > 1 {
			return fmt.Errorf("invalid signed-property boolean")
		}
		if signed == 1 {
			if _, e = takeString(r); e != nil {
				return e
			}
		}
	}
	profile, ok := profileFor(c.Protocol)
	if !ok {
		return fmt.Errorf("unsupported login protocol %d", c.Protocol)
	}
	tail := 0
	if profile.loginSessionUUID {
		tail = 16
	}
	if r.Len() != tail {
		return fmt.Errorf("protocol %d login needs %d trailing session UUID bytes, got %d", c.Protocol, tail, r.Len())
	}
	return nil
}
func configuration(w *wire, c Config) error {
	if e := login(w, c); e != nil {
		return e
	}
	if e := w.send(3, nil); e != nil {
		return e
	}
	registries, known, features, tags := map[string]int32{}, false, false, false
	for i := 0; i < 256; i++ {
		p, e := w.read()
		if e != nil {
			return e
		}
		switch p.id {
		case 0x02:
			return fmt.Errorf("configuration disconnected: %x", p.data)
		case 0x04:
			if len(p.data) != 8 {
				return fmt.Errorf("invalid config keepalive")
			}
			if e = w.send(4, p.data); e != nil {
				return e
			}
		case 0x05:
			if len(p.data) != 4 {
				return fmt.Errorf("invalid config ping")
			}
			if e = w.send(5, p.data); e != nil {
				return e
			}
		case 0x0e:
			if known {
				return fmt.Errorf("duplicate known-pack negotiation")
			}
			known = true
			r := bytes.NewReader(p.data)
			n, e := readInt(r)
			if e != nil || n < 0 || n > 1024 {
				return fmt.Errorf("invalid known-pack count")
			}
			for j := int32(0); j < n; j++ {
				for k := 0; k < 3; k++ {
					if _, e = takeString(r); e != nil {
						return e
					}
				}
			}
			if r.Len() != 0 {
				return fmt.Errorf("trailing known-pack bytes")
			}
			if e = w.send(7, []byte{0}); e != nil {
				return e
			}
		case 0x07:
			if !known || tags {
				return fmt.Errorf("registry arrived outside known-packs/registry phase")
			}
			name, count, err := registryJava(p.data)
			if err != nil {
				return fmt.Errorf("registry: %w", err)
			}
			if _, exists := registries[name]; exists {
				return fmt.Errorf("duplicate registry %q", name)
			}
			registries[name] = count
		case 0x0c:
			if features {
				return fmt.Errorf("duplicate feature packet")
			}
			if err := featuresJava(p.data); err != nil {
				return fmt.Errorf("features: %w", err)
			}
			features = true
		case 0x0d:
			if !known || len(registries) == 0 || tags {
				return fmt.Errorf("tags arrived outside registry completion phase")
			}
			if err := tagsJava(p.data); err != nil {
				return fmt.Errorf("tags: %w", err)
			}
			tags = true
		case 0x03:
			if len(p.data) != 0 {
				return fmt.Errorf("finish configuration has trailing bytes")
			}
			if !known || !features || !tags || registries["minecraft:dimension_type"] == 0 || registries["minecraft:worldgen/biome"] == 0 {
				return fmt.Errorf("incomplete config: known=%v features=%v tags=%v registries=%v", known, features, tags, registries)
			}
			if e = w.send(3, nil); e != nil {
				return e
			}
			join, e := expect(w, 0x31)
			if e != nil {
				return e
			}
			if err := joinJava(join.data, registries, c.Protocol); err != nil {
				return fmt.Errorf("play login: %w", err)
			}
			return nil
		}
	}
	return fmt.Errorf("configuration exceeded 256 packets without finish")
}
func peerClosed(w *wire) error {
	var b [1]byte
	n, e := w.conn.Read(b[:])
	w.record("clientbound", b[:n], -1, e)
	if e == nil {
		return fmt.Errorf("unexpected response instead of connection close")
	}
	if ne, ok := e.(net.Error); ok && ne.Timeout() {
		return fmt.Errorf("peer waited instead of rejecting malformed input: %w", e)
	}
	if errors.Is(e, io.EOF) {
		return nil
	}
	var op *net.OpError
	if errors.As(e, &op) {
		return nil
	}
	return fmt.Errorf("expected peer close: %w", e)
}
func invalidState(w *wire, c Config) error {
	if e := w.handshake(c, 99); e != nil {
		return e
	}
	return peerClosed(w)
}
func oversize(w *wire, c Config) error {
	if e := w.writeRaw(appendInt(nil, 1<<30), -1); e != nil {
		return e
	}
	return peerClosed(w)
}
