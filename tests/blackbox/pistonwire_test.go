package blackbox

import (
	"bytes"
	"net"
	"strings"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/world"
)

// TestPistonWireParity exercises the full moving-block wire surface a
// piston move emits — the block_event packet (0x07 with the piston's
// pos, action param, facing's 3D data value, block registry id), the
// transient moving_piston cells with their block_entity_data payloads
// (beType 11, blockState/facing/extending/source), the settle writes
// when the 2-tick interlude ends, and the entity push.
func TestPistonWireParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("PistWire"),
	}, nil)
	defer targets.cleanup()

	for _, tc := range []struct{ tag, addr string }{
		{"vanilla", targets.vanilla},
		{"mc-go", targets.mcgo},
	} {
		t.Run(tc.tag, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, "PistWire")
			defer c.Close()
			drainToJoinBlackbox(t, tc.tag, pr)
			sendCommand(t, pw, "gamemode creative @s")
			sendCommand(t, pw, "tp @s 92.5 -60 35.5 0 0")
			drainChunkWindow(t, tc.tag, c, pr, pw, 400*time.Millisecond)
			wireDrain(t, c, pr, pw, 300*time.Millisecond)

			// Fixture A — a plain piston pushing one stone up. The
			// zombie stands where the stone lands so the move's
			// destination cell sweeps through its hitbox.
			for _, cmd := range []string{
				"setblock 90 -60 40 minecraft:piston[facing=up]",
				"setblock 90 -59 40 minecraft:stone",
				"summon minecraft:zombie 90.5 -58.0 40.5 {NoAI:1b}",
				// Fixture B — sticky piston for the retract leg.
				"setblock 93 -60 40 minecraft:sticky_piston[facing=up]",
				"setblock 93 -59 40 minecraft:stone",
			} {
				sendCommand(t, pw, cmd)
			}
			wireDrain(t, c, pr, pw, 400*time.Millisecond)

			// Extend: torch beside the piston powers it.
			sendCommand(t, pw, "setblock 89 -60 40 minecraft:redstone_torch")
			ext := wireDrain(t, c, pr, pw, 800*time.Millisecond)
			settleA := wireDrain(t, c, pr, pw, 700*time.Millisecond)

			assertBlockEvent(t, tc.tag, ext, 90, -60, 40, 0, 1, 138)
			assertChange(t, tc.tag, ext, 90, -60, 40, "piston")
			// Vanilla writes the moving_piston cells with UPDATE_INVISIBLE
			// — no set_block and no beType-11 block_entity_data ever hit
			// the wire; the client re-simulates the slide from the event.
			assertNoMovingPiston(t, tc.tag, append(ext, settleA...))
			// The settle window lands the carried states: head at the
			// front cell, stone one up.
			assertChange(t, tc.tag, append(ext, settleA...), 90, -58, 40, "stone")
			assertChange(t, tc.tag, append(ext, settleA...), 90, -59, 40, "piston_head")
			// The zombie's hitbox overlapped the stone's destination
			// cell: it slides up one block over the two BE ticks.
			assertEntityPush(t, tc.tag, append(ext, settleA...))

			// Extend the sticky piston.
			sendCommand(t, pw, "setblock 94 -60 40 minecraft:redstone_block")
			extB := wireDrain(t, c, pr, pw, 800*time.Millisecond)
			settleB := wireDrain(t, c, pr, pw, 700*time.Millisecond)
			assertBlockEvent(t, tc.tag, extB, 93, -60, 40, 0, 1, 128)
			assertChange(t, tc.tag, extB, 93, -60, 40, "sticky_piston")
			assertNoMovingPiston(t, tc.tag, append(extB, settleB...))
			assertChange(t, tc.tag, append(extB, settleB...), 93, -59, 40, "piston_head")
			assertChange(t, tc.tag, append(extB, settleB...), 93, -58, 40, "stone")

			// Retract: pull the stone back into the head cell. The
			// block event carries param 1 (no in-flight head), the
			// front cell gets a retracting moving_piston carrying the
			// stone, the stone's cell goes air, and the settle lands
			// the stone back where it started.
			sendCommand(t, pw, "setblock 94 -60 40 minecraft:air")
			ret := wireDrain(t, c, pr, pw, 800*time.Millisecond)
			settleC := wireDrain(t, c, pr, pw, 700*time.Millisecond)
			assertBlockEvent(t, tc.tag, ret, 93, -60, 40, 1, 1, 128)
			assertChange(t, tc.tag, ret, 93, -60, 40, "sticky_piston")
			assertNoMovingPiston(t, tc.tag, append(ret, settleC...))
			assertChange(t, tc.tag, ret, 93, -58, 40, "air")
			assertChange(t, tc.tag, append(ret, settleC...), 93, -59, 40, "stone")
		})
	}
}

// wireEvent is one decoded world-write a move emitted: a block state
// change, a block event, a piston BE payload, a level event, or an
// entity move.
type wireEvent struct {
	kind string // "block", "bevent", "be", "level", "move", "syncpos"
	pos  [3]int32
	name string
	b0   int32 // block_event param / BE type
	b1   int32 // block_event facing / level_event data
	ext  bool
	src  bool
	dy   float64 // move_entity_pos delta (packed) or syncpos absolute y
	eid  int32
}

// wireDrain collects and decodes play packets until a quiet period.
// Keep-alives, chunk batch acks, and teleport confirms are answered so
// the capture doesn't stall the connection.
func wireDrain(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, quiet time.Duration) []wireEvent {
	t.Helper()
	var out []wireEvent
	// Ambient entity/time packets may keep arriving indefinitely. Bound the
	// capture window while retaining every decoded world-write assertion.
	started := time.Now()
	deadline := started.Add(max(4*time.Second, 4*quiet))
	bounded := false
	packets := map[int32]int{}
	for {
		now := time.Now()
		if !now.Before(deadline) {
			bounded = true
			break
		}
		readUntil := now.Add(quiet)
		if readUntil.After(deadline) {
			readUntil = deadline
		}
		_ = c.SetReadDeadline(readUntil)
		p, err := pr.ReadPacket()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				bounded = !time.Now().Before(deadline)
				break
			}
			t.Fatalf("wire drain: %v", err)
		}
		packets[p.ID]++
		switch p.ID {
		case server.IDPlayKeepAliveCB:
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
		case server.IDPlayChunkBatchDone:
			_ = ackChunkBatch(pw)
		case server.IDPlaySyncPos:
			if id, err := protocol.NewPacketReader(p.Data).VarInt(); err == nil {
				var b protocol.Buffer
				b.VarInt(id)
				_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
			}
		case 0x07: // block_event: Pos + byte b0 + byte b1 + VarInt block registry id
			r := protocol.NewPacketReader(p.Data)
			x, y, z, e1 := r.Position()
			b0, e2 := r.Byte()
			b1, e3 := r.Byte()
			blk, e4 := r.VarInt()
			if e1 == nil && e2 == nil && e3 == nil && e4 == nil {
				out = append(out, wireEvent{kind: "bevent",
					pos: [3]int32{x, y, z}, b0: int32(b0), b1: int32(b1), eid: blk})
			}
		case server.IDPlayBlockUpdateCB:
			r := protocol.NewPacketReader(p.Data)
			x, y, z, e1 := r.Position()
			st, e2 := r.VarInt()
			if e1 == nil && e2 == nil {
				out = append(out, wireEvent{kind: "block", pos: [3]int32{x, y, z},
					name: wireStateName(st)})
			}
		case 0x54: // section_blocks_update
			for _, ch := range decodeSectionBlocksUpdate(t, p.Data) {
				out = append(out, wireEvent{kind: "block", pos: ch.pos,
					name: wireStateName(ch.state)})
			}
		case server.IDPlayBlockEntityData:
			r := protocol.NewPacketReader(p.Data)
			x, y, z, e1 := r.Position()
			beType, e2 := r.VarInt()
			if e1 != nil || e2 != nil || beType != 11 {
				break
			}
			rest := p.Data[len(p.Data)-r.Remaining():]
			root, err := nbt.ReadUnnamed(bytes.NewReader(rest))
			if err != nil {
				break
			}
			if m, ok := root.(map[string]any); ok {
				out = append(out, wirePistonBE([3]int32{x, y, z}, beType, m))
			}
		case server.IDPlayLevelEvent:
			r := protocol.NewPacketReader(p.Data)
			ev, _ := r.Int()
			x, y, z, _ := r.Position()
			data, _ := r.Int()
			out = append(out, wireEvent{kind: "level", pos: [3]int32{x, y, z},
				b0: ev, b1: data})
		case server.IDPlayEntityUpdatePos, 0x36: // move_entity_pos / pos_rot
			r := protocol.NewPacketReader(p.Data)
			eid, _ := r.VarInt()
			_, _ = r.Short() // dx
			dy, _ := r.Short()
			_, _ = r.Short() // dz
			out = append(out, wireEvent{kind: "move", eid: eid, dy: float64(dy)})
		case server.IDPlayEntityTeleportCB: // entity_position_sync
			r := protocol.NewPacketReader(p.Data)
			eid, _ := r.VarInt()
			_, _ = r.Double()
			y, _ := r.Double()
			_, _ = r.Double()
			out = append(out, wireEvent{kind: "syncpos", eid: eid, dy: y})
		}
	}
	_ = c.SetReadDeadline(time.Time{})
	if bounded {
		t.Logf("wire drain bounded at %s: packets=%v decoded-world-events=%d", time.Since(started), packets, len(out))
	}
	return out
}

// wireStateName resolves a block-state palette index to its short
// name for readable assertions.
func wireStateName(st int32) string {
	name, ok := world.StateOwner(world.Block(st))
	if !ok {
		return "?"
	}
	return strings.TrimPrefix(name, "minecraft:")
}

// wirePistonBE decodes a moving-piston Block Entity Data payload into
// a wireEvent: the carried state's name, facing, extending, source.
func wirePistonBE(pos [3]int32, beType int32, m map[string]any) wireEvent {
	ev := wireEvent{kind: "be", pos: pos, b0: beType}
	if bs, ok := m["blockState"].(map[string]any); ok {
		if nm, ok := bs["Name"].(string); ok {
			ev.name = strings.TrimPrefix(nm, "minecraft:")
		}
	}
	if f, ok := wireNbtNum(m["facing"]); ok {
		ev.b1 = int32(f)
	}
	if v, ok := wireNbtNum(m["extending"]); ok {
		ev.ext = v != 0
	}
	if v, ok := wireNbtNum(m["source"]); ok {
		ev.src = v != 0
	}
	return ev
}

func wireNbtNum(v any) (int64, bool) {
	switch n := v.(type) {
	case int8:
		return int64(n), true
	case int16:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case float32:
		return int64(n), true
	case float64:
		return int64(n), true
	}
	return 0, false
}

// assertBlockEvent requires exactly one block_event packet at pos
// with the given params and block registry id (piston=138, sticky=128).
func assertBlockEvent(t *testing.T, tag string, evs []wireEvent, x, y, z, b0, b1, regID int32) {
	t.Helper()
	var hits int
	for _, e := range evs {
		if e.kind == "bevent" && e.pos == [3]int32{x, y, z} {
			hits++
			if e.b0 != b0 || e.b1 != b1 || e.eid != regID {
				t.Fatalf("%s: block_event@%v params b0=%d b1=%d reg=%d, want b0=%d b1=%d reg=%d",
					tag, e.pos, e.b0, e.b1, e.eid, b0, b1, regID)
			}
		}
	}
	if hits != 1 {
		t.Fatalf("%s: %d block_event packets at (%d,%d,%d), want 1; events: %v",
			tag, hits, x, y, z, evs)
	}
}

// assertChange requires at least one block change to name at pos.
func assertChange(t *testing.T, tag string, evs []wireEvent, x, y, z int32, name string) {
	t.Helper()
	for _, e := range evs {
		if e.kind == "block" && e.pos == [3]int32{x, y, z} && e.name == name {
			return
		}
	}
	t.Fatalf("%s: no block change to %s at (%d,%d,%d); events: %v", tag, name, x, y, z, evs)
}

// assertNoMovingPiston requires the moving_piston intermediates to
// stay off the wire entirely: no block change to moving_piston and
// no beType-11 block_entity_data anywhere in the window.
func assertNoMovingPiston(t *testing.T, tag string, evs []wireEvent) {
	t.Helper()
	for _, e := range evs {
		if e.kind == "block" && e.name == "moving_piston" {
			t.Fatalf("%s: moving_piston block write at %v on wire; events: %v", tag, e.pos, evs)
		}
		if e.kind == "be" {
			t.Fatalf("%s: moving-piston BE data at %v on wire; events: %v", tag, e.pos, evs)
		}
	}
}

// assertEntityPush requires the swept zombie to have been moved up
// one block: the per-tick pushes ride relative move_entity_pos
// packets (packed 1/4096 units — 4096 = one block), so their dy sums
// to ~4096.
func assertEntityPush(t *testing.T, tag string, evs []wireEvent) {
	t.Helper()
	var sum float64
	for _, e := range evs {
		if e.kind == "move" && e.dy > 0 {
			sum += e.dy
		}
		if e.kind == "syncpos" && e.dy > -57.6 && e.dy < -56.4 {
			return // absolute y≈-57 after the +1 push
		}
	}
	if sum < 4000 || sum > 4400 {
		t.Fatalf("%s: entity push dy sum %.0f (want ~4096 = +1 block); events: %v", tag, sum, evs)
	}
}
