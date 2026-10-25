package blackbox

import (
	"net"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/world"
)

// TestBlockDropParity digs real blocks in survival on both servers and
// compares the spawned item stacks (id multiset). The probes pin
// hasCorrectToolForDrops (requires-tool blocks + the tool rules:
// incorrect_for_<tier> denies, mineable/<kind> allows, shears claim
// only cobweb among these) and the datapack-table conditions:
// block_state_property (door half, crop age) and match_tool
// (shears, silk touch).
func TestBlockDropParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, func(s *server.Server) { s.GameMode = server.GameModeSurvival })
	defer targets.cleanup()

	// waitMs is the dig duration plus slack — vanilla breaks only once
	// the full tick count elapses, an early finish cancels the dig.
	// instabreaks (progress >= 1 on the first tick) break on the start
	// action itself.
	probes := []struct {
		name   string
		block  string
		under  string // optional support block below
		tool   string
		waitMs int
	}{
		{"dirt bare hand", "dirt", "", "air", 1300},
		{"stone bare hand -> gated, still breaks", "stone", "", "air", 8400},
		{"stone stone_pickaxe -> cobblestone", "stone", "", "stone_pickaxe", 1100},
		{"iron_ore wooden_pickaxe -> tier denied", "iron_ore", "", "wooden_pickaxe", 16500},
		{"glass diamond_pickaxe -> no silk", "glass", "", "diamond_pickaxe", 1000},
		{"acacia_leaves shears -> instabreak", "acacia_leaves", "", "shears", 400},
		{"oak_door half=lower", "oak_door[half=lower]", "", "air", 5300},
		{"oak_door half=upper -> no drop", "oak_door[half=upper]", "", "air", 5300},
		{"wheat age=0 -> seeds only", "wheat[age=0]", "farmland", "air", 400},
		{"wheat age=7 -> crop", "wheat[age=7]", "farmland", "air", 400},
		{"cobweb shears -> cobweb", "cobweb", "", "shears", 800},
		{"cobweb bare hand -> gated", "cobweb", "", "air", 22000},
	}

	itemType, _ := server.EntityTypeID("item")

	// dig watches packets until the probe block turns to air, then
	// keeps reading for a grace window to collect item entities. Chunk
	// batches are acknowledged as they arrive — vanilla does not count
	// a chunk as watched (no block updates, no entity spawns reach the
	// client) until the client answers chunk_batch_finished.
	dig := func(c net.Conn, pr *protocol.Reader, pw *protocol.Writer, pos world.Pos, waitMs int) []string {
		// Item stack keyed by entity id: vanilla may push an entity's
		// metadata more than once, which must not count as more drops.
		items := map[int32][]string{}
		var itemEIDs = map[int32]bool{}
		broke := false
		deadline := time.Now().Add(60 * time.Second)
		stopAt := time.Now().Add(time.Duration(waitMs) * time.Millisecond)
		stopped := false
		for time.Now().Before(deadline) {
			if !stopped && !time.Now().Before(stopAt) {
				// A finish after the measured time commits the dig; when
				// the block already broke it is a no-op on both servers.
				sendDig(t, pw, 2, int32(pos.X), int32(pos.Y), int32(pos.Z), 2)
				stopped = true
			}
			_ = c.SetReadDeadline(time.Now().Add(80 * time.Millisecond))
			p, err := pr.ReadPacket()
			_ = c.SetReadDeadline(time.Time{})
			if err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					continue
				}
				break
			}
			switch p.ID {
			case server.IDPlayKeepAliveCB:
				var kb protocol.Buffer
				kb.Raw(p.Data)
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, kb.Bytes())
			case server.IDPlayChunkBatchDone:
				var ack protocol.Buffer
				ack.Float(64)
				_ = pw.WritePacket(server.IDPlayChunkBatchRecvSB, ack.Bytes())
			case server.IDPlayBlockUpdateCB:
				r := protocol.NewPacketReader(p.Data)
				x, y, z, _ := r.Position()
				st, _ := r.VarInt()
				if int(x) == pos.X && int(y) == pos.Y && int(z) == pos.Z && st == int32(world.Air) && !broke {
					broke = true
					deadline = time.Now().Add(2500 * time.Millisecond)
				}
			case server.IDPlaySpawnEntity:
				r := protocol.NewPacketReader(p.Data)
				eid, _ := r.VarInt()
				_, _ = r.UUID()
				typ, _ := r.VarInt()
				if typ == itemType {
					itemEIDs[eid] = true
				}
			case server.IDPlayEntityMetadata:
				r := protocol.NewPacketReader(p.Data)
				eid, _ := r.VarInt()
				if !itemEIDs[eid] {
					continue
				}
				idx, err1 := r.Byte()
				typ, err2 := r.Byte()
				if err1 != nil || err2 != nil || idx != 8 || typ != 7 {
					continue
				}
				cnt, _ := r.VarInt()
				id, _ := r.VarInt()
				if cnt > 0 {
					if nm, ok := world.ItemName(id); ok {
						items[eid] = []string{nm}
					}
				}
			}
		}
		if !broke {
			t.Logf("probe at %v produced no block update", pos)
		}
		var out []string
		for _, v := range items {
			out = append(out, v...)
		}
		sort.Strings(out)
		return out
	}

	// ackBatches drains whatever has arrived and answers
	// chunk_batch_finished so vanilla counts the chunks as watched —
	// without the ack no block updates or entity spawns reach us.
	ackBatches := func(c net.Conn, pr *protocol.Reader, pw *protocol.Writer) {
		end := time.Now().Add(600 * time.Millisecond)
		for time.Now().Before(end) {
			_ = c.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			p, err := pr.ReadPacket()
			if err != nil {
				continue
			}
			switch p.ID {
			case server.IDPlayKeepAliveCB:
				var kb protocol.Buffer
				kb.Raw(p.Data)
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, kb.Bytes())
			case server.IDPlayChunkBatchDone:
				var ack protocol.Buffer
				ack.Float(64)
				_ = pw.WritePacket(server.IDPlayChunkBatchRecvSB, ack.Bytes())
			}
		}
		_ = c.SetReadDeadline(time.Time{})
	}

	run := func(addr string) [][]string {
		c, pr, pw := joinPlayQuiet(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		sendClientInformation(t, pw)
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sendCommand(t, pw, "gamemode survival @s")
		ackBatches(c, pr, pw)
		var out [][]string
		for i, p := range probes {
			pos := world.Pos{X: 40, Y: -60, Z: i * 4}
			if p.under != "" {
				sendCommand(t, pw, "setblock 40 -61 "+strconv.Itoa(pos.Z)+" minecraft:"+p.under)
			}
			// Solid ground under the player — an airborne dig runs at
			// a fifth of the speed on vanilla.
			sendCommand(t, pw, "setblock 41 -61 "+strconv.Itoa(pos.Z)+" minecraft:dirt")
			sendCommand(t, pw, "setblock 40 -60 "+strconv.Itoa(pos.Z)+" minecraft:"+p.block)
			sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:"+p.tool)
			time.Sleep(200 * time.Millisecond)
			// Stand just beside the probe and dig down on it. The move
			// has to be a command: a client move packet this far is
			// rejected by vanilla's moved-too-quickly check, leaving the
			// player at spawn out of reach of the probe.
			sendCommand(t, pw, "tp @s 41.5 -60 "+strconv.Itoa(pos.Z)+".5 0 90")
			ackBatches(c, pr, pw)
			sendDig(t, pw, 0, int32(pos.X), int32(pos.Y), int32(pos.Z), 1)
			got := dig(c, pr, pw, pos, p.waitMs)
			t.Logf("%s -> %v", p.name, got)
			out = append(out, got)
		}
		return out
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	for i, p := range probes {
		if strings.Join(want[i], ",") != strings.Join(got[i], ",") {
			t.Errorf("probe %q: vanilla %v\n  mc-go %v", p.name, want[i], got[i])
		}
	}
}
