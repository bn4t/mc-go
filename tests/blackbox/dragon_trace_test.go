//go:build blackbox

package blackbox

import (
	"bytes"
	"compress/gzip"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// requireBlackboxSlow is requireBlackboxFiles for a generated world
// whose mc-go side takes longer than the harness's 10 seconds to start
// (initial spawn search and End terrain under a loaded machine).
func requireBlackboxSlow(t *testing.T, vanillaProps, vanillaFiles map[string]string, mutate func(*server.Server)) targets {
	t.Helper()
	if os.Getenv(targetProp) != "" {
		t.Skipf("%s mode: %s needs in-process mc-go configuration", targetProp, t.Name())
	}
	jar := blackboxJar(t)
	vPort, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}
	vAddr, stop := startVanillaCfgFiles(t, jar, vPort, vanillaProps, vanillaFiles)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("bind blocked: %v", err)
	}
	addr := ln.Addr().String()
	s := server.New(addr)
	s.ViewDistance = 8
	s.SimulationDistance = 8
	mutate(s)
	if err := s.InitPlugins(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = s.Serve(ln) }()
	t.Cleanup(func() { _ = ln.Close(); s.Stop() })
	deadline := time.Now().Add(3 * time.Minute)
	for !probeStatus(addr) {
		if time.Now().After(deadline) {
			t.Fatalf("mc-go not ready on %s", addr)
		}
		time.Sleep(200 * time.Millisecond)
	}
	return targets{vanilla: vAddr, mcgo: addr, cleanup: stop}
}

// readGzipNBTBlackbox reads a gzipped named NBT file body.
func readGzipNBTBlackbox(r io.Reader) (any, error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	_, v, err := nbt.ReadNamed(zr)
	return v, err
}

// dfPkt is one recorded clientbound play packet.
type dfPkt struct {
	id   int32
	data []byte
	at   time.Duration
}

// dfTrace pumps a play connection for d (answering keep-alives, chunk
// batches and teleports) and records every packet. stop, when set, ends
// the pump early once it returns true for a recorded packet.
func dfTrace(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, d time.Duration, stop func(dfPkt) bool) []dfPkt {
	t.Helper()
	var out []dfPkt
	start := time.Now()
	deadline := start.Add(d)
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(deadline)
		p, err := pr.ReadPacket()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				break
			}
			t.Fatalf("trace read: %v", err)
		}
		switch p.ID {
		case server.IDPlayKeepAliveCB:
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
		case server.IDPlayChunkBatchDone:
			_ = ackChunkBatch(pw)
		case server.IDPlaySyncPos:
			r := protocol.NewPacketReader(p.Data)
			if id, err := r.VarInt(); err == nil {
				var b protocol.Buffer
				b.VarInt(id)
				_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
			}
		}
		k := dfPkt{id: p.ID, data: append([]byte(nil), p.Data...), at: time.Since(start)}
		out = append(out, k)
		if stop != nil && stop(k) {
			break
		}
	}
	_ = c.SetReadDeadline(time.Time{})
	return out
}

// dfSpawn is a decoded add_entity packet.
type dfSpawn struct {
	eid        int32
	uuid       [16]byte
	typ        int32
	x, y, z    float64
	pitch, yaw byte
	headYaw    byte
	data       int32
}

func dfDecodeSpawn(b []byte) (dfSpawn, bool) {
	r := protocol.NewPacketReader(b)
	var s dfSpawn
	var err error
	if s.eid, err = r.VarInt(); err != nil {
		return s, false
	}
	u, err := r.Read(16)
	if err != nil {
		return s, false
	}
	copy(s.uuid[:], u)
	if s.typ, err = r.VarInt(); err != nil {
		return s, false
	}
	s.x, _ = r.Double()
	s.y, _ = r.Double()
	s.z, _ = r.Double()
	// 26.2 add_entity: position, then the movement vector (lpvec), then
	// pitch, yaw, head yaw and data.
	if _, _, _, err := r.ReadLpVec3(); err != nil {
		return s, false
	}
	s.pitch, _ = r.Byte()
	s.yaw, _ = r.Byte()
	s.headYaw, _ = r.Byte()
	s.data, _ = r.VarInt()
	return s, true
}

// dfBoss is a decoded boss_event packet.
type dfBoss struct {
	uuid   [16]byte
	op     int32
	name   string
	prog   float32
	color  int32
	style  int32
	flags  byte
	hasAdd bool
}

func dfDecodeBoss(t *testing.T, b []byte) dfBoss {
	r := protocol.NewPacketReader(b)
	var e dfBoss
	u, _ := r.Read(16)
	copy(e.uuid[:], u)
	e.op, _ = r.VarInt()
	switch e.op {
	case 0:
		rest, _ := r.Read(r.Remaining())
		br := bytes.NewReader(rest)
		root, err := nbt.ReadUnnamed(br)
		if err == nil {
			e.name = fmt.Sprint(root)
		}
		tail := rest[len(rest)-br.Len():]
		tr := protocol.NewPacketReader(tail)
		e.prog, _ = tr.Float()
		e.color, _ = tr.VarInt()
		e.style, _ = tr.VarInt()
		e.flags, _ = tr.Byte()
		e.hasAdd = true
	case 2:
		e.prog, _ = r.Float()
	case 5:
		e.flags, _ = r.Byte()
	}
	return e
}

// dfNames names the clientbound ids the dragon traces care about.
var dfNames = map[int32]string{
	0x01: "add_entity", 0x02: "animate", 0x05: "block_destruction", 0x08: "block_update", 0x09: "boss_event",
	0x19: "damage_event", 0x22: "entity_event", 0x23: "entity_position_sync", 0x24: "explode", 0x26: "game_event",
	0x2A: "hurt_animation", 0x2E: "level_event", 0x2F: "level_particles", 0x35: "move_entity_pos",
	0x36: "move_entity_pos_rot", 0x38: "move_entity_rot", 0x4D: "remove_entities", 0x53: "rotate_head",
	0x54: "section_blocks_update", 0x63: "set_entity_data", 0x65: "set_entity_motion", 0x74: "sound_entity",
	0x75: "sound", 0x79: "system_chat", 0x06: "block_entity_data", 0x2D: "level_chunk_with_light",
	0x30: "light_update", 0x7C: "take_item_entity", 0x67: "set_experience", 0x68: "set_health",
}

// dfLine renders one packet for a measurement log.
func dfLine(t *testing.T, p dfPkt) string {
	name := dfNames[p.id]
	if name == "" {
		name = fmt.Sprintf("0x%02x", p.id)
	}
	switch p.id {
	case 0x01:
		s, _ := dfDecodeSpawn(p.data)
		return fmt.Sprintf("%6dms add_entity eid=%d type=%d pos=(%.3f,%.3f,%.3f) pitch=%d yaw=%d head=%d data=%d", p.at.Milliseconds(), s.eid, s.typ, s.x, s.y, s.z, s.pitch, s.yaw, s.headYaw, s.data)
	case 0x09:
		b := dfDecodeBoss(t, p.data)
		return fmt.Sprintf("%6dms boss_event op=%d name=%s prog=%v color=%d style=%d flags=%d", p.at.Milliseconds(), b.op, b.name, b.prog, b.color, b.style, b.flags)
	case 0x79:
		return fmt.Sprintf("%6dms system_chat %s", p.at.Milliseconds(), chatTextBlackbox(t, p.data))
	case 0x2D, 0x30:
		r := protocol.NewPacketReader(p.data)
		x, _ := r.Int()
		z, _ := r.Int()
		return fmt.Sprintf("%6dms %s %d,%d", p.at.Milliseconds(), name, x, z)
	}
	h := hex.EncodeToString(p.data)
	if len(h) > 200 {
		h = h[:200] + "..."
	}
	return fmt.Sprintf("%6dms %s %s", p.at.Milliseconds(), name, h)
}

func dfDump(t *testing.T, tag string, ps []dfPkt, skip map[int32]bool) {
	var sb strings.Builder
	for _, p := range ps {
		if skip[p.id] {
			continue
		}
		sb.WriteString(tag + " " + dfLine(t, p) + "\n")
	}
	t.Log("\n" + sb.String())
}
