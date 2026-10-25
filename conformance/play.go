package conformance

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net"
	"sync"
	"time"
)

// Packet is a decoded body owned by the caller. IDs are factual Java wire IDs,
// never implementation constants; data excludes framing and compression.
type Packet struct {
	ID   int32
	Data []byte
}

// PlayClient is a scripted offline-mode Java client for protocol experiments.
// It validates login/configuration/Join Game with the same independent profile
// as Run, then services teleport, chunk-batch and keepalive acknowledgements.
// It is not a renderer, authentication service or full gameplay implementation.
// Send and read may run concurrently; reads are serialized, never duplicated.
type PlayClient struct {
	wire          *wire
	config        Config
	readMu        sync.Mutex
	stopContext   func() bool
	initial       []Packet
	configuration []Packet
	registries    map[string][]string
	// EntityID is the authoritative entity assigned by Play Login.
	EntityID int32
}

// OpenPlay establishes an offline session and waits for the initial player
// attribute sync, a measured marker after the initial chunk burst. Timeout
// bounds the entire join, not each individual packet. Caller context also owns
// the returned connection's lifetime; Close releases it explicitly.
func OpenPlay(ctx context.Context, c Config) (client *PlayClient, err error) {
	c = normalizeConfig(c)
	if len(c.Capabilities) == 0 {
		c.Capabilities = []string{"configuration"}
	}
	if err = validate(c); err != nil {
		return nil, err
	}
	if _, ok := profileFor(c.Protocol); !ok {
		return nil, fmt.Errorf("unsupported play profile %d", c.Protocol)
	}
	conn, err := (&net.Dialer{Timeout: c.Timeout}).DialContext(ctx, "tcp", c.Endpoint)
	if err != nil {
		return nil, err
	}
	events := []Event{}
	budget := c.TranscriptLimit
	client = &PlayClient{config: c, registries: map[string][]string{}}
	client.wire = &wire{conn: conn, threshold: -1, events: &events, budget: &budget}
	client.stopContext = context.AfterFunc(ctx, func() { _ = conn.Close() })
	success := false
	defer func() {
		if !success {
			_ = client.Close()
			client = nil
		}
	}()
	joinCtx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	deadline, _ := joinCtx.Deadline()
	_ = conn.SetDeadline(deadline)
	stopJoin := context.AfterFunc(joinCtx, func() { _ = conn.Close() })
	defer stopJoin()
	retained := 0
	var captureErr error
	capture := func(p packet) {
		if captureErr != nil {
			return
		}
		retained += len(p.data)
		if retained > c.TranscriptLimit {
			captureErr = errors.New("initial packet capture exceeds configured byte limit")
			return
		}
		item := Packet{ID: p.id, Data: p.data}
		if p.id == 0x31 {
			client.initial = append(client.initial, item)
			client.EntityID = int32(binary.BigEndian.Uint32(p.data[:4]))
			return
		}
		client.configuration = append(client.configuration, item)
		if p.id == 7 {
			name, names, e := registryNames(p.data)
			if e == nil {
				client.registries[name] = names
			}
		}
	}
	if err = configurePlay(client.wire, c, capture); err != nil {
		return client, err
	}
	if captureErr != nil {
		return client, captureErr
	}
	for {
		p, e := client.ReadPacket(joinCtx)
		if e != nil {
			return client, fmt.Errorf("play readiness: %w", e)
		}
		retained += len(p.Data)
		if retained > c.TranscriptLimit {
			return client, errors.New("initial packet capture exceeds configured byte limit")
		}
		client.initial = append(client.initial, p)
		if p.ID == 0x83 {
			r := bytes.NewReader(p.Data)
			id, e := readInt(r)
			if e != nil {
				return client, e
			}
			if id == client.EntityID {
				break
			}
		}
	}
	if err = client.SendPacket(0x2c, nil); err != nil {
		return client, err
	} // player_loaded
	_ = conn.SetDeadline(time.Time{})
	success = true
	return client, nil
}

func registryNames(data []byte) (string, []string, error) {
	f := bodyFields(data)
	name := f.identifier()
	n := f.count()
	var names []string
	nodes := 0
	for i := int32(0); i < n && f.err == nil; i++ {
		names = append(names, f.identifier())
		if f.boolean() {
			tag := f.byte()
			skipNBT(f, tag, 0, &nodes)
		}
	}
	return name, names, f.complete()
}

func (c *PlayClient) Close() error {
	if c == nil || c.wire == nil {
		return nil
	}
	if c.stopContext != nil {
		c.stopContext()
	}
	return c.wire.conn.Close()
}
func (c *PlayClient) Protocol() int32  { return c.config.Protocol }
func (c *PlayClient) Username() string { return c.config.Username }

// SendPacket writes one complete frame; concurrent control replies cannot split
// it. The configured timeout bounds each write even after the join deadline.
func (c *PlayClient) SendPacket(id int32, data []byte) error {
	if id < 0 {
		return errors.New("negative packet ID")
	}
	_ = c.wire.conn.SetWriteDeadline(time.Now().Add(c.config.Timeout))
	return c.wire.send(id, data)
}

// ReadPacket returns every packet, including control packets it acknowledged.
// On read failure the framed stream must be closed; it cannot safely resume at
// an assumed next frame after a timeout or cancellation.
func (c *PlayClient) ReadPacket(ctx context.Context) (Packet, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	deadline := time.Now().Add(c.config.Timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = c.wire.conn.SetReadDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = c.wire.conn.Close() })
	defer stop()
	p, err := c.wire.read()
	if err != nil {
		_ = c.Close()
		if ctx.Err() != nil {
			return Packet{}, ctx.Err()
		}
		return Packet{}, err
	}
	switch p.id {
	case 0x2c: // clientbound keep_alive
		if len(p.data) != 8 {
			err = errors.New("malformed play keepalive")
		} else {
			err = c.SendPacket(0x1c, p.data)
		}
	case 0x48: // player_position
		id, e := readInt(bytes.NewReader(p.data))
		if e != nil {
			err = e
		} else {
			err = c.SendPacket(0, appendInt(nil, id))
		}
	case 0x0b: // chunk_batch_finished
		err = c.SendPacket(0x0b, binary.BigEndian.AppendUint32(nil, math.Float32bits(64)))
	}
	if err != nil {
		_ = c.Close()
		return Packet{}, err
	}
	return Packet{ID: p.id, Data: p.data}, nil
}

// WaitPacket has one total deadline even while unrelated packets keep arriving.
func (c *PlayClient) WaitPacket(ctx context.Context, match func(Packet) bool) (Packet, error) {
	if match == nil {
		return Packet{}, errors.New("nil packet matcher")
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.config.Timeout)
		defer cancel()
	}
	for {
		p, err := c.ReadPacket(ctx)
		if err != nil {
			return Packet{}, err
		}
		if match(p) {
			return p, nil
		}
	}
}
func clonePackets(in []Packet) []Packet {
	out := make([]Packet, len(in))
	for i, p := range in {
		out[i] = Packet{ID: p.ID, Data: append([]byte(nil), p.Data...)}
	}
	return out
}
func (c *PlayClient) InitialPackets() []Packet       { return clonePackets(c.initial) }
func (c *PlayClient) ConfigurationPackets() []Packet { return clonePackets(c.configuration) }
func (c *PlayClient) RegistryEntries(name string) []string {
	return append([]string(nil), c.registries[name]...)
}
func (c *PlayClient) Transcript() []Event {
	c.wire.recordMu.Lock()
	defer c.wire.recordMu.Unlock()
	return append([]Event(nil), (*c.wire.events)...)
}
func (c *PlayClient) Command(command string) error { return c.SendPacket(7, str(nil, command)) }
func (c *PlayClient) Move(x, y, z float64, yaw, pitch float32, onGround bool) error {
	var b []byte
	for _, v := range []float64{x, y, z} {
		b = binary.BigEndian.AppendUint64(b, math.Float64bits(v))
	}
	b = binary.BigEndian.AppendUint32(b, math.Float32bits(yaw))
	b = binary.BigEndian.AppendUint32(b, math.Float32bits(pitch))
	flag := byte(0)
	if onGround {
		flag = 1
	}
	b = append(b, flag)
	return c.SendPacket(0x1f, b)
}
