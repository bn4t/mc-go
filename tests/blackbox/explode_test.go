// explode_test.go pins the clientbound explode packet (0x24) for wind
// charges: burst geometry, the inline knockback field, the blast
// sampler's block count, and interactWithBlocks' TRIGGER_BLOCK flips.

package blackbox

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/world"
)

// explodePkt decodes the 26.2 explode packet: centre, blast radius,
// blast-sampler block count, an optional inline knockback (present only
// for players the burst pushed), particle id, and the one-based sound
// holder.
type explodePkt struct {
	x, y, z     float64
	radius      float32
	blockCount  int32
	hasKB       bool
	kb          [3]float64
	particleID  int32
	soundHolder int32
}

func decodeExplode(t *testing.T, data []byte) explodePkt {
	t.Helper()
	r := protocol.NewPacketReader(data)
	var ep explodePkt
	ep.x, _ = r.Double()
	ep.y, _ = r.Double()
	ep.z, _ = r.Double()
	ep.radius, _ = r.Float()
	ep.blockCount, _ = r.Int()
	ep.hasKB, _ = r.Bool()
	if ep.hasKB {
		ep.kb[0], _ = r.Double()
		ep.kb[1], _ = r.Double()
		ep.kb[2], _ = r.Double()
	}
	ep.particleID, _ = r.VarInt()
	ep.soundHolder, _ = r.VarInt()
	return ep
}

// TestWindChargeExplodeParity fires a wind_charge at a survival victim
// with a creative watcher alongside and pins the explode packet's shape
// on both targets: same radius/particle/sound, nonzero sampled block
// count, victim knockback inline while the observer's copy carries
// none, and explode -> remove_entities order.
func TestWindChargeExplodeParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BoomV", "BoomW"),
	}, func(s *server.Server) {})
	defer targets.cleanup()

	type capture struct {
		victim, watcher             explodePkt
		victimMotion, watcherMotion bool
		burstOrder                  []int32 // 0x24 explode / 0x4d remove
	}
	run := func(addr string) capture {
		cv, prv, pwv := joinPlayBlackbox(t, addr, "BoomV")
		defer cv.Close()
		cw, prw, pww := joinPlayBlackbox(t, addr, "BoomW")
		defer cw.Close()
		_ = cv.SetDeadline(time.Time{})
		_ = cw.SetDeadline(time.Time{})
		_ = pwv.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		_ = pww.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		// BoomV stands in the burst in survival; BoomW watches in
		// creative from 4 blocks west.
		sendCommand(t, pwv, "gamemode survival @s")
		sendCommand(t, pwv, "tp @s 8.5 -60 8.5 0 0")
		_ = sendMovePosRot(pwv, 8.5, -60, 8.5)
		sendCommand(t, pww, "gamemode creative @s")
		sendCommand(t, pww, "tp @s 4.5 -60 8.5 90 0")
		_ = sendMovePosRot(pww, 4.5, -60, 8.5)
		// The chunk window drains must ack batches and confirm teleports —
		// vanilla's tracker suppresses tracker-emitted packets until acked.
		drainChunkWindow(t, "boomv", cv, prv, pwv, 400*time.Millisecond)
		drainChunkWindow(t, "boomw", cw, prw, pww, 400*time.Millisecond)

		sendCommand(t, pww, "summon minecraft:wind_charge 8.5 -60.2 8.5 {Motion:[0d,-0.2d,0d]}")

		var got capture
		var victimEID int32
		playerType, _ := server.EntityTypeID("player")
		end := time.Now().Add(8 * time.Second)
		for time.Now().Before(end) && (got.victim.radius == 0 || got.watcher.radius == 0) {
			_ = cv.SetReadDeadline(time.Now().Add(800 * time.Millisecond))
			for {
				p, err := prv.ReadPacket()
				if err != nil {
					break
				}
				switch p.ID {
				case server.IDPlayKeepAliveCB:
					_ = pwv.WritePacket(server.IDPlayKeepAliveSB, p.Data)
				case server.IDPlayChunkBatchDone:
					_ = ackChunkBatch(pwv)
				case 0x24:
					got.victim = decodeExplode(t, p.Data)
					got.burstOrder = append(got.burstOrder, 0x24)
				case server.IDPlayRemoveEntity:
					got.burstOrder = append(got.burstOrder, 0x4d)
				case 0x65:
					got.victimMotion = true
				}
			}
			_ = cw.SetReadDeadline(time.Now().Add(800 * time.Millisecond))
			for {
				p, err := prw.ReadPacket()
				if err != nil {
					break
				}
				switch p.ID {
				case server.IDPlayKeepAliveCB:
					_ = pww.WritePacket(server.IDPlayKeepAliveSB, p.Data)
				case server.IDPlayChunkBatchDone:
					_ = ackChunkBatch(pww)
				case server.IDPlaySpawnEntity:
					r := protocol.NewPacketReader(p.Data)
					id, _ := r.VarInt()
					_, _ = r.Read(16)
					typ, _ := r.VarInt()
					if typ == playerType {
						victimEID = id
					}
				case 0x24:
					got.watcher = decodeExplode(t, p.Data)
				case 0x65:
					r := protocol.NewPacketReader(p.Data)
					if eid, _ := r.VarInt(); eid == victimEID {
						got.watcherMotion = true
					}
				}
			}
		}
		return got
	}

	van := run(targets.vanilla)
	got := run(targets.mcgo)

	if van.victim.radius == 0 || van.watcher.radius == 0 {
		if b, err := os.ReadFile(filepath.Join(vanillaDirForDebug, "vanilla.log")); err == nil {
			t.Logf("vanilla log:\n%s", b)
		}
		t.Fatalf("vanilla explode not captured: victim=%+v watcher=%+v", van.victim, van.watcher)
	}
	if got.victim.radius == 0 || got.watcher.radius == 0 {
		t.Fatalf("mc-go explode missing: victim=%+v watcher=%+v", got.victim, got.watcher)
	}
	if got.victim.radius != van.victim.radius {
		t.Fatalf("radius %v, want %v", got.victim.radius, van.victim.radius)
	}
	if got.victim.particleID != van.victim.particleID || got.victim.soundHolder != van.victim.soundHolder {
		t.Fatalf("particle/sound = %d/%d, want %d/%d", got.victim.particleID, got.victim.soundHolder, van.victim.particleID, van.victim.soundHolder)
	}
	// The blast sampler is randomized per explosion — both counts just
	// have to land in the flat-ground band.
	if got.victim.blockCount <= 0 || van.victim.blockCount <= 0 {
		t.Fatalf("blockCount mc=%d van=%d, want >0", got.victim.blockCount, van.victim.blockCount)
	}
	// The victim carries the knockback inline; an unhit observer gets
	// no field.
	if !van.victim.hasKB || !got.victim.hasKB {
		t.Fatalf("victim knockback field missing (van=%v mc=%v)", van.victim.hasKB, got.victim.hasKB)
	}
	if van.watcher.hasKB || got.watcher.hasKB {
		t.Fatalf("observer should carry no knockback (van=%v mc=%v)", van.watcher.hasKB, got.watcher.hasKB)
	}
	// Player directly under the burst: knockback is mostly up, of the
	// (1-dist)*1.22 order — compare loosely across the differing
	// contact points.
	if got.victim.kb[1] <= 0.5 || math.Abs(got.victim.kb[1]-van.victim.kb[1]) > 0.5 {
		t.Fatalf("victim kb.y mc=%v van=%v", got.victim.kb[1], van.victim.kb[1])
	}
	if got.victimMotion != van.victimMotion || got.watcherMotion != van.watcherMotion {
		t.Fatalf("motion packets differ (mc victim=%v watcher=%v, van victim=%v watcher=%v)",
			got.victimMotion, got.watcherMotion, van.victimMotion, van.watcherMotion)
	}
	// Explode precedes the charge's remove_entities on the victim conn.
	for i := 1; i < len(van.burstOrder); i++ {
		if van.burstOrder[i-1] == 0x24 && van.burstOrder[i] == 0x4d {
			goto vanOrderOK
		}
	}
	t.Fatalf("vanilla explode/remove order: %v", van.burstOrder)
vanOrderOK:
	for i := 1; i < len(got.burstOrder); i++ {
		if got.burstOrder[i-1] == 0x24 && got.burstOrder[i] == 0x4d {
			return
		}
	}
	t.Fatalf("mc-go explode/remove order: %v", got.burstOrder)
}

// sectionBlocksUpdate decodes vanilla's batched chunk-section block
// change packet (0x54): ChunkHolder accumulates changed positions per
// section and flushes them at tick end — one change rides a plain
// block_update, several ride this.
type sectionBlockChange struct {
	pos   [3]int32
	state int32
}

func decodeSectionBlocksUpdate(t *testing.T, data []byte) []sectionBlockChange {
	t.Helper()
	r := protocol.NewPacketReader(data)
	sec, _ := r.Long()
	secX := sec >> 42
	secZ := (sec >> 20) & 0x3fffff
	secY := sec & 0xfffff
	if secY >= 0x80000 {
		secY -= 0x100000
	}
	if secZ >= 0x200000 {
		secZ -= 0x400000
	}
	count, _ := r.VarInt()
	out := make([]sectionBlockChange, 0, count)
	for i := int32(0); i < count; i++ {
		packed, _ := r.VarLong()
		lx := (packed >> 8) & 0xf
		lz := (packed >> 4) & 0xf
		ly := packed & 0xf
		out = append(out, sectionBlockChange{
			pos:   [3]int32{int32(secX*16 + lx), int32(secY*16 + ly), int32(secZ*16 + lz)},
			state: int32(packed >> 12),
		})
	}
	return out
}

// TestWindChargeTriggerBlocksParity pins interactWithBlocks for a
// TRIGGER_BLOCK burst: every sampled block position runs its
// onExplosionHit — the lever flips on, the button presses and releases
// on its scheduled tick (stone: 20), the trapdoor and fence gate toggle
// open, the lit candle extinguishes. The door sits next to the button:
// its powered flag tracks the press, so the release re-closes it.
//
// Wire shape differs between targets and is accepted: mc-go emits one
// block_update per flipped position mid-burst while vanilla batches
// them into section_blocks_update at tick end — same state changes,
// different carrier.
func TestWindChargeTriggerBlocksParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Trig"),
	}, func(s *server.Server) {})
	defer targets.cleanup()

	type trig struct {
		explode bool
		pkt     explodePkt
		states  map[[3]int32][]int32 // block pos -> state ids, arrival order
		sounds  map[int32]bool       // sound_event registry ids (wire id - 1)
	}
	run := func(addr string) trig {
		c, pr, pw := joinPlayBlackbox(t, addr, "Trig")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sendCommand(t, pw, "gamemode creative @s")
		sendCommand(t, pw, "tp @s 12.5 -60 8.5 90 0")
		// All within ~2.3 blocks of the burst centre so the sampler
		// collects them every time; the door is powered-adjacent to the
		// button (press powers it, release re-closes it), the rest are
		// isolated so order across positions can't change the outcome.
		for _, cmd := range []string{
			"setblock 8 -60 7 minecraft:lever[face=floor]",
			"setblock 9 -60 8 minecraft:stone_button[face=floor]",
			"setblock 8 -60 8 minecraft:candle[candles=1,lit=true]",
			"setblock 10 -60 8 minecraft:oak_door[facing=north,half=lower]",
			"setblock 10 -59 8 minecraft:oak_door[facing=north,half=upper]",
			"setblock 7 -60 9 minecraft:oak_trapdoor[half=bottom]",
			"setblock 10 -60 10 minecraft:oak_fence_gate",
		} {
			sendCommand(t, pw, cmd)
		}
		// The chunk window is what lets vanilla's block updates out: the
		// tracker suppresses them until the batch is acked.
		drainChunkWindow(t, "trig", c, pr, pw, 500*time.Millisecond)
		sendCommand(t, pw, "summon minecraft:breeze_wind_charge 8.5 -58.8 8.5 {Motion:[0d,-0.5d,0d]}")

		var out trig
		out.states = map[[3]int32][]int32{}
		out.sounds = map[int32]bool{}
		buttonPos := [3]int32{9, -60, 8}
		// Enough room for the 20-tick button release plus the powered
		// door's close that rides the same tick.
		end := time.Now().Add(4500 * time.Millisecond)
		for time.Now().Before(end) {
			_ = c.SetReadDeadline(time.Now().Add(800 * time.Millisecond))
			for {
				p, err := pr.ReadPacket()
				if err != nil {
					break
				}
				switch p.ID {
				case server.IDPlayKeepAliveCB:
					_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
				case server.IDPlayChunkBatchDone:
					_ = ackChunkBatch(pw)
				case server.IDPlaySyncPos:
					if id, err := protocol.NewPacketReader(p.Data).VarInt(); err == nil {
						var tb protocol.Buffer
						tb.VarInt(id)
						_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, tb.Bytes())
					}
				case server.IDPlayBlockUpdateCB:
					r := protocol.NewPacketReader(p.Data)
					x, y, z, _ := r.Position()
					if st, err := r.VarInt(); err == nil {
						out.states[[3]int32{x, y, z}] = append(out.states[[3]int32{x, y, z}], st)
					}
				case 0x54: // section_blocks_update
					for _, ch := range decodeSectionBlocksUpdate(t, p.Data) {
						out.states[ch.pos] = append(out.states[ch.pos], ch.state)
					}
				case 0x75: // sound (holder id is one-based)
					if id, err := protocol.NewPacketReader(p.Data).VarInt(); err == nil {
						out.sounds[id-1] = true
					}
				case 0x24:
					out.pkt = decodeExplode(t, p.Data)
					out.explode = true
				}
			}
			// Done once the burst landed, the stone button released
			// (20 scheduled ticks) and the powered door fell shut again.
			door := [3]int32{10, -60, 8}
			lastDoorClosed := false
			if st := out.states[door]; len(st) > 0 {
				v, _ := world.StateProperty(world.Block(st[len(st)-1]), "open")
				lastDoorClosed = v == "false"
			}
			if out.explode && len(out.states[buttonPos]) >= 2 && lastDoorClosed {
				break
			}
		}
		return out
	}

	van := run(targets.vanilla)
	got := run(targets.mcgo)
	if !van.explode || !got.explode {
		if b, err := os.ReadFile(filepath.Join(vanillaDirForDebug, "vanilla.log")); err == nil {
			t.Logf("vanilla log:\n%s", b)
		}
		t.Fatalf("explode missing (van=%v mc=%v)", van.explode, got.explode)
	}
	t.Logf("vanilla pkt=%+v states=%v sounds=%v", van.pkt, van.states, van.sounds)
	t.Logf("mc-go   pkt=%+v states=%v sounds=%v", got.pkt, got.states, got.sounds)

	prop := func(ids []int32, name string) string {
		if len(ids) == 0 {
			return ""
		}
		v, _ := world.StateProperty(world.Block(ids[len(ids)-1]), name)
		return v
	}
	wantFinal := []struct {
		pos   [3]int32
		props map[string]string
	}{
		{[3]int32{8, -60, 7}, map[string]string{"powered": "true"}},                    // lever flips on
		{[3]int32{10, -60, 8}, map[string]string{"open": "false", "powered": "false"}}, // powered door shut again
		{[3]int32{10, -59, 8}, map[string]string{"open": "false", "powered": "false"}}, // upper mirrors the lower
		{[3]int32{7, -60, 9}, map[string]string{"open": "true"}},                       // trapdoor open
		{[3]int32{10, -60, 10}, map[string]string{"open": "true"}},                     // gate open
		{[3]int32{8, -60, 8}, map[string]string{"lit": "false"}},                       // candle out
	}
	for _, w := range wantFinal {
		for name, want := range w.props {
			if p := prop(van.states[w.pos], name); p != want {
				if b, err := os.ReadFile(filepath.Join(vanillaDirForDebug, "vanilla.log")); err == nil {
					t.Logf("vanilla log:\n%s", b)
				}
				t.Fatalf("vanilla %v %s=%q want %q (states=%v)", w.pos, name, p, want, van.states[w.pos])
			}
			if p := prop(got.states[w.pos], name); p != want {
				t.Fatalf("mc-go %v %s=%q want %q (states=%v)", w.pos, name, p, want, got.states[w.pos])
			}
		}
	}
	// The door must have opened at some point before being re-closed —
	// either by the burst flip or by the button's power.
	door := [3]int32{10, -60, 8}
	sawOpen := false
	for _, st := range van.states[door] {
		if v, _ := world.StateProperty(world.Block(st), "open"); v == "true" {
			sawOpen = true
		}
	}
	if !sawOpen {
		t.Fatalf("vanilla door never opened (states=%v)", van.states[door])
	}
	sawOpen = false
	for _, st := range got.states[door] {
		if v, _ := world.StateProperty(world.Block(st), "open"); v == "true" {
			sawOpen = true
		}
	}
	if !sawOpen {
		t.Fatalf("mc-go door never opened (states=%v)", got.states[door])
	}
	// The stone button presses and releases on its 20-tick schedule.
	buttonPos := [3]int32{9, -60, 8}
	for tag, st := range map[string][]int32{"vanilla": van.states[buttonPos], "mc-go": got.states[buttonPos]} {
		if len(st) < 2 || prop(st[:1], "powered") != "true" || prop(st, "powered") != "false" {
			t.Fatalf("%s button never pressed+released (states=%v)", tag, st)
		}
	}
	// The toggle sounds each block plays — set-compared, the flip order
	// across positions is shuffled.
	wantSounds := []int32{
		935,  // block.lever.click
		1598, // block.stone_button.click_on
		1597, // block.stone_button.click_off
		1846, // block.wooden_door.open
		1845, // block.wooden_door.close (release re-shuts it)
		1848, // block.wooden_trapdoor.open
		625,  // block.fence_gate.open
		274,  // block.candle.extinguish
	}
	for _, id := range wantSounds {
		if !van.sounds[id] {
			t.Fatalf("vanilla missing sound %d (sounds=%v)", id, van.sounds)
		}
		if !got.sounds[id] {
			t.Fatalf("mc-go missing sound %d (sounds=%v)", id, got.sounds)
		}
	}
}
