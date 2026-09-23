package blackbox

import (
	"fmt"
	"math"
	"net"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// wearObs is the observable client-side trace of one wear phase: the
// slot resyncs that arrived (window-0 only), the entity-event status
// bytes, and the Award Statistics rows the next request produced.
type wearObs struct {
	slots []string
	evs   []int32
	stats []statEntry
	// spawnedPos records every add_entity the drain saw:
	// eid -> type, x, y, z — for picking the entity a summon just
	// produced out of ambient spawn traffic.
	spawnedPos map[int32][4]float64
	// motions records the last set_entity_motion delta (decoded
	// LpVec3) per entity; flags the last index-0 shared-flags
	// metadata byte.
	motions map[int32][3]float64
	flags   map[int32]byte
}

// drainWear reads server traffic for d, keeping the session alive and
// folding container-slot resyncs, entity events and spawned entity
// ids (keyed by entity type) into obs.
func drainWear(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, pos *[3]float64, d time.Duration, obs *wearObs, spawned map[int32]int32) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(time.Now().Add(time.Until(deadline)))
		p, err := pr.ReadPacket()
		if err != nil {
			break
		}
		switch p.ID {
		case server.IDPlayKeepAliveCB:
			var kb protocol.Buffer
			kb.Raw(p.Data)
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, kb.Bytes())
		case server.IDPlayChunkBatchDone:
			// Vanilla does not count a chunk as watched — no entity
			// spawns reach the client — until it answers
			// chunk_batch_finished.
			_ = ackChunkBatch(pw)
		case server.IDPlaySyncPos:
			r := protocol.NewPacketReader(p.Data)
			if id, err := r.VarInt(); err == nil {
				if x, err := r.Double(); err == nil {
					y, _ := r.Double()
					z, _ := r.Double()
					pos[0], pos[1], pos[2] = x, y, z
				}
				var b protocol.Buffer
				b.VarInt(id)
				_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
			}
		case server.IDPlayContainerSlot:
			r := protocol.NewPacketReader(p.Data)
			wid, e1 := r.VarInt()
			_, e2 := r.VarInt()
			slot, e3 := r.Short()
			it, e4 := readSlotBB(r)
			if e1 != nil || e2 != nil || e3 != nil || e4 != nil || wid != 0 {
				continue
			}
			obs.slots = append(obs.slots, fmt.Sprintf("%d=%s", slot, stackKey(it)))
		case server.IDPlayEntityMotion:
			r := protocol.NewPacketReader(p.Data)
			eid, err := r.VarInt()
			if err != nil {
				continue
			}
			x, y, z, ok := decodeLpVec3(r)
			if !ok {
				continue
			}
			if obs.motions == nil {
				obs.motions = map[int32][3]float64{}
			}
			obs.motions[eid] = [3]float64{x, y, z}
		case server.IDPlayEntityMetadata:
			r := protocol.NewPacketReader(p.Data)
			eid, err := r.VarInt()
			if err != nil {
				continue
			}
			idx, err := r.Byte()
			if err != nil || idx != 0 {
				continue
			}
			ty, _ := r.VarInt()
			if ty != 0 {
				continue
			}
			if v, err := r.Byte(); err == nil {
				if obs.flags == nil {
					obs.flags = map[int32]byte{}
				}
				obs.flags[eid] = v
			}
		case server.IDPlayEntityEvent:
			r := protocol.NewPacketReader(p.Data)
			if _, err := r.Int(); err != nil {
				continue
			}
			if status, err := r.Byte(); err == nil {
				obs.evs = append(obs.evs, int32(status))
			}
		case server.IDPlaySpawnEntity:
			r := protocol.NewPacketReader(p.Data)
			eid, err := r.VarInt()
			if err != nil {
				continue
			}
			if _, err := r.UUID(); err != nil {
				continue
			}
			ty, err := r.VarInt()
			if err != nil {
				continue
			}
			x, _ := r.Double()
			y, _ := r.Double()
			z, _ := r.Double()
			if spawned != nil {
				spawned[ty] = eid
			}
			if obs.spawnedPos == nil {
				obs.spawnedPos = map[int32][4]float64{}
			}
			obs.spawnedPos[eid] = [4]float64{float64(ty), x, y, z}
		}
	}
}

// runWearScenario executes the tool-wear script on one target: mine
// dirt twice, mine a torch (instant: no wear, still a use), mine one
// more dirt to break the pickaxe, then hit a summoned pig with a
// sword.
func runWearScenario(t *testing.T, addr, name string) []wearObs {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, name)
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(120 * time.Second))
	var pos [3]float64
	obs := wearObs{}

	// Setup: known spot, a damaged pick and a fresh sword.
	drainWear(t, c, pr, pw, &pos, 1200*time.Millisecond, &obs, nil)
	sendCommand(t, pw, "tp @s 6 -60 2 0 0")
	drainWear(t, c, pr, pw, &pos, 400*time.Millisecond, &obs, nil)
	sendCommand(t, pw, "item replace entity @s hotbar.0 with minecraft:stone_pickaxe[minecraft:damage=128]")
	sendCommand(t, pw, "item replace entity @s hotbar.1 with minecraft:stone_sword")
	drainWear(t, c, pr, pw, &pos, 400*time.Millisecond, &obs, nil)

	x, y, z := int(math.Floor(pos[0])), int(math.Floor(pos[1])), int(math.Floor(pos[2]))
	// All dirt digs mine the same adjacent cell, walled on three
	// sides so vanilla's random toss can only slide the drop toward
	// the player; the client also steps forward to meet it, keeping
	// pickup inside either implementation's reach. The torch digs a
	// supported instant-break block four blocks out whose drop lands
	// too far to be collected by anyone.
	sendCommand(t, pw, fmt.Sprintf("fill %d %d %d %d %d %d dirt", x-1, y, z+1, x+1, y+1, z+2))
	sendCommand(t, pw, fmt.Sprintf("setblock %d %d %d dirt", x, y, z+1))
	sendCommand(t, pw, fmt.Sprintf("setblock %d %d %d dirt", x, y, z+4))
	sendCommand(t, pw, fmt.Sprintf("setblock %d %d %d torch", x, y+1, z+4))
	drainWear(t, c, pr, pw, &pos, 400*time.Millisecond, &obs, nil)

	var phases []wearObs
	dig := func(bx, by, bz int, resetDirt bool) {
		sendDig(t, pw, 0, int32(bx), int32(by), int32(bz), 1)
		sendDig(t, pw, 2, int32(bx), int32(by), int32(bz), 1)
		po := wearObs{}
		if bz == z+1 {
			// Step toward the drop — close enough to reach every toss
			// landing, short of overlapping the block being mined.
			_ = sendMovePosRot(pw, pos[0], pos[1], float64(z)+0.6)
		}
		// The dig takes ~15 ticks and the drop's pickup delay another
		// 10 before it lands in the inventory; 2s covers both.
		drainWear(t, c, pr, pw, &pos, 1200*time.Millisecond, &po, nil)
		if bz == z+1 {
			_ = sendMovePosRot(pw, pos[0], pos[1], float64(z)+0.5)
		}
		drainWear(t, c, pr, pw, &pos, 800*time.Millisecond, &po, nil)
		po.stats = drainStats(t, c, pr, pw, &pos, 5*time.Second)
		if resetDirt {
			// Restore the adjacent dirt for the next phase; the
			// command traffic folds into this phase's drain tail.
			sendCommand(t, pw, fmt.Sprintf("setblock %d %d %d dirt", x, y, z+1))
			drainWear(t, c, pr, pw, &pos, 300*time.Millisecond, &po, nil)
		}
		phases = append(phases, po)
	}
	dig(x, y, z+1, true)    // -> damage 129
	dig(x, y+1, z+4, false) // torch: instant, used++ but no wear
	dig(x, y, z+1, true)    // -> damage 130
	dig(x, y, z+1, false)   // -> damage 131: break

	// Sword phase: hotbar slot 2 (index 1), a pig on top of us, one
	// swing.
	var hb protocol.Buffer
	hb.Short(1) // set_carried_item carries a Short slot
	if err := pw.WritePacket(server.IDPlayHeldChangeSB, hb.Bytes()); err != nil {
		t.Fatal(err)
	}
	sendCommand(t, pw, "summon minecraft:pig ~ ~ ~")
	sp := map[int32]int32{}
	po := wearObs{}
	drainWear(t, c, pr, pw, &pos, 1500*time.Millisecond, &po, sp)
	pigType, _ := server.EntityTypeID("pig")
	pigEID := sp[pigType]
	if pigEID == 0 {
		t.Fatalf("%s: pig never spawned", name)
	}
	var atk protocol.Buffer
	atk.VarInt(pigEID)
	if err := pw.WritePacket(server.IDPlayAttackSB, atk.Bytes()); err != nil {
		t.Fatal(err)
	}
	drainWear(t, c, pr, pw, &pos, 1200*time.Millisecond, &po, nil)
	po.stats = drainStats(t, c, pr, pw, &pos, 5*time.Second)
	phases = append(phases, po)
	return phases
}

// TestToolWearParity pins durability behaviour end to end: each mined
// non-instant block costs the tool its damage_per_block, an
// instant-mine block costs nothing but still counts a use, reaching
// max damage broadcasts the break event and empties the slot, and a
// landed hit wears a weapon by item_damage_per_attack. The emitted
// packets and the statistics rows must equal vanilla's.
func TestToolWearParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "survival"}, map[string]string{
		"ops.json": opsJSONFor("WearV", "WearM"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer tg.cleanup()
	defer func() {
		if t.Failed() {
			vanillaLogTail(t)
		}
	}()

	want := runWearScenario(t, tg.vanilla, "WearV")
	got := runWearScenario(t, tg.mcgo, "WearM")
	compareWearObs(t, "self", want, got)
}

// runArmorScenario has a victim in near-broken iron armor take two
// stone-sword hits from an attacker and returns both clients' per-hit
// observations. Hit one breaks helmet, leggings and boots (wear 1 on
// raw 5 damage, 164+224+194 pieces one point from max); hit two
// breaks the chestplate.
func runArmorScenario(t *testing.T, addr, victim, attacker string) (victimObs, attackerObs []wearObs) {
	t.Helper()
	// The attacker joins first so the victim's spawn packet lands in
	// the attacker's own drain and teaches the test its entity id.
	ca, pra, pwa := joinPlayBlackbox(t, addr, attacker)
	defer ca.Close()
	cv, prv, pwv := joinPlayBlackbox(t, addr, victim)
	defer cv.Close()
	_ = ca.SetDeadline(time.Now().Add(120 * time.Second))
	_ = cv.SetDeadline(time.Now().Add(120 * time.Second))
	var posA, posV [3]float64
	obsA, obsV := wearObs{}, wearObs{}

	drainWear(t, cv, prv, pwv, &posV, 1200*time.Millisecond, &obsV, nil)
	sp := map[int32]int32{}
	drainWear(t, ca, pra, pwa, &posA, 1200*time.Millisecond, &obsA, sp)
	victimEID := sp[server.EntityTypePlayer]
	if victimEID == 0 {
		t.Fatalf("%s: %s never saw %s spawn", addr, attacker, victim)
	}

	// Vanilla spreads fresh joins around the world spawn; the hit
	// needs the victim inside the 3-block attack reach, so bring her
	// to the attacker's synced position first.
	sendCommand(t, pwv, fmt.Sprintf("tp @s %v %v %v", posA[0], posA[1], posA[2]))

	// The victim's own replaces settle on her client; the attacker
	// just needs the sword.
	for _, cmd := range []string{
		"item replace entity @s armor.head with minecraft:iron_helmet[minecraft:damage=164]",
		"item replace entity @s armor.chest with minecraft:iron_chestplate[minecraft:damage=238]",
		"item replace entity @s armor.legs with minecraft:iron_leggings[minecraft:damage=224]",
		"item replace entity @s armor.feet with minecraft:iron_boots[minecraft:damage=194]",
	} {
		sendCommand(t, pwv, cmd)
	}
	sendCommand(t, pwa, "item replace entity @s hotbar.0 with minecraft:stone_sword")
	drainWear(t, cv, prv, pwv, &posV, 400*time.Millisecond, &obsV, nil)
	drainWear(t, ca, pra, pwa, &posA, 400*time.Millisecond, &obsA, nil)

	for hit := 0; hit < 2; hit++ {
		var atk protocol.Buffer
		atk.VarInt(victimEID)
		if err := pwa.WritePacket(server.IDPlayAttackSB, atk.Bytes()); err != nil {
			t.Fatal(err)
		}
		poA, poV := wearObs{}, wearObs{}
		// 2.4s keeps the hits a full sword-cooldown and the victim's
		// twenty-tick invulnerability apart.
		drainWear(t, cv, prv, pwv, &posV, 2200*time.Millisecond, &poV, nil)
		poV.stats = drainStats(t, cv, prv, pwv, &posV, 5*time.Second)
		drainWear(t, ca, pra, pwa, &posA, 300*time.Millisecond, &poA, nil)
		poA.stats = drainStats(t, ca, pra, pwa, &posA, 5*time.Second)
		victimObs = append(victimObs, poV)
		attackerObs = append(attackerObs, poA)
	}
	return victimObs, attackerObs
}

// compareWearObs asserts a client-side trace matches the other
// implementation's, phase by phase.
func compareWearObs(t *testing.T, side string, want, got []wearObs) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("%s phase count: vanilla %d mc-go %d", side, len(want), len(got))
	}
	for i := range want {
		w, g := want[i], got[i]
		if len(w.slots) != len(g.slots) {
			t.Errorf("%s phase %d slot resyncs: vanilla %v mc-go %v", side, i, w.slots, g.slots)
		} else {
			for j := range w.slots {
				if w.slots[j] != g.slots[j] {
					t.Errorf("%s phase %d slot[%d]: vanilla %s mc-go %s", side, i, j, w.slots[j], g.slots[j])
				}
			}
		}
		if len(w.evs) != len(g.evs) {
			t.Errorf("%s phase %d entity events: vanilla %v mc-go %v", side, i, w.evs, g.evs)
		} else {
			for j := range w.evs {
				if w.evs[j] != g.evs[j] {
					t.Errorf("%s phase %d event[%d]: vanilla %d mc-go %d", side, i, j, w.evs[j], g.evs[j])
				}
			}
		}
		if len(w.stats) != len(g.stats) {
			t.Errorf("%s phase %d stats rows: vanilla %v mc-go %v", side, i, w.stats, g.stats)
			continue
		}
		for j := range w.stats {
			wr, gr := w.stats[j], g.stats[j]
			if wr.cat != gr.cat || wr.id != gr.id {
				t.Errorf("%s phase %d stat %d key: vanilla %d:%d mc-go %d:%d", side, i, j, wr.cat, wr.id, gr.cat, gr.id)
				continue
			}
			if isTimedStat(wr) {
				continue
			}
			if wr.val != gr.val {
				t.Errorf("%s phase %d stat[%d] %d:%d value: vanilla %d mc-go %d", side, i, j, wr.cat, wr.id, wr.val, gr.val)
			}
		}
	}
}

// TestArmorWearParity pins armor durability on the wire: a hit that
// armor can absorb costs each equipped damageable piece
// max(1, raw/4) — on the raw amount before reduction — and an
// exhausted piece empties its slot, snaps its break event, awards
// the broken stat, and reduces the next hit less.
func TestArmorWearParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "survival", "pvp": "true"}, map[string]string{
		"ops.json": opsJSONFor("ArmV", "ArmM", "AtkV", "AtkM"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer tg.cleanup()
	defer func() {
		if t.Failed() {
			vanillaLogTail(t)
		}
	}()

	wantV, wantA := runArmorScenario(t, tg.vanilla, "ArmV", "AtkV")
	gotV, gotA := runArmorScenario(t, tg.mcgo, "ArmM", "AtkM")

	compareWearObs(t, "victim", wantV, gotV)
	compareWearObs(t, "attacker", wantA, gotA)
}

// runCooldownScenario swings a stone sword at a summoned NoAI iron
// golem (it cannot wander off or retaliate) three times: charged,
// ~11 ticks later — past the mob's post-hit grace but still inside
// the 12.5-tick sword cooldown — then recharged. Stats are drained
// once at the end: the cumulative damage_dealt row is the verdict —
// a server without the cooldown scale reports 150, vanilla ~144.
func runCooldownScenario(t *testing.T, addr, name string) []wearObs {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, name)
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(120 * time.Second))
	var pos [3]float64
	obs := wearObs{}
	drainWear(t, c, pr, pw, &pos, 1200*time.Millisecond, &obs, nil)
	sendCommand(t, pw, "tp @s 6 -60 2 0 0")
	sendCommand(t, pw, "item replace entity @s hotbar.0 with minecraft:stone_sword")
	var hb protocol.Buffer
	hb.Short(0)
	if err := pw.WritePacket(server.IDPlayHeldChangeSB, hb.Bytes()); err != nil {
		t.Fatal(err)
	}
	drainWear(t, c, pr, pw, &pos, 400*time.Millisecond, &obs, nil)
	sendCommand(t, pw, "summon minecraft:iron_golem ~ ~ ~ {NoAI:1b}")
	golemType, _ := server.EntityTypeID("iron_golem")
	drainWear(t, c, pr, pw, &pos, 1500*time.Millisecond, &obs, nil)
	// Ambient world-gen can also produce golem spawns — ours is the
	// type-70 entity that appeared on top of the player.
	golemEID := int32(0)
	best := math.MaxFloat64
	for eid, sp := range obs.spawnedPos {
		if int32(sp[0]) != golemType {
			continue
		}
		dx, dy, dz := sp[1]-pos[0], sp[2]-pos[1], sp[3]-pos[2]
		if d := dx*dx + dy*dy + dz*dz; d < best {
			best, golemEID = d, eid
		}
	}
	if golemEID == 0 || best > 25 {
		t.Fatalf("%s: no nearby iron golem spawn (spawned=%v)", name, obs.spawnedPos)
	}
	swing := func() {
		var atk protocol.Buffer
		atk.VarInt(golemEID)
		if err := pw.WritePacket(server.IDPlayAttackSB, atk.Bytes()); err != nil {
			t.Fatal(err)
		}
	}
	swing()
	time.Sleep(560 * time.Millisecond)
	swing()
	time.Sleep(1500 * time.Millisecond)
	swing()
	po := wearObs{}
	drainWear(t, c, pr, pw, &pos, 1200*time.Millisecond, &po, nil)
	po.stats = drainStats(t, c, pr, pw, &pos, 5*time.Second)
	return []wearObs{po}
}

// TestAttackCooldownParity pins vanilla's attack-strength cooldown:
// damage scales by 0.2 + 0.8*scale² with
// scale = (ticker + 0.5) / (20 / attackSpeed) — a mid-recharge hit on
// a mob whose i-frames just expired lands for ~88% of a stone sword,
// and each landed hit is awarded damage_dealt = round(dealt * 10).
func TestAttackCooldownParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "survival"}, map[string]string{
		"ops.json": opsJSONFor("CoolV", "CoolM"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer tg.cleanup()
	defer func() {
		if t.Failed() {
			vanillaLogTail(t)
		}
	}()

	want := runCooldownScenario(t, tg.vanilla, "CoolV")
	got := runCooldownScenario(t, tg.mcgo, "CoolM")

	compareWearObs(t, "attacker", want, got)
}

// decodeLpVec3 reads vanilla's 26.2 LpVec3 wire form (the
// set_entity_motion delta): byte0==0 means the zero vector; otherwise
// two low bytes plus a big-endian int make the packed word with a
// 2-bit scale (+VarInt extension) and 15-bit quantized axes at 3/18/33.
func decodeLpVec3(r *protocol.PacketReader) (x, y, z float64, ok bool) {
	b0, err := r.Byte()
	if err != nil {
		return 0, 0, 0, false
	}
	if b0 == 0 {
		return 0, 0, 0, true
	}
	b1, err := r.Byte()
	if err != nil {
		return 0, 0, 0, false
	}
	hi, err := r.Int()
	if err != nil {
		return 0, 0, 0, false
	}
	word := uint64(uint32(hi))<<16 | uint64(b1)<<8 | uint64(b0)
	scale := int64(b0 & 3)
	if b0&4 != 0 {
		ext, err := r.VarInt()
		if err != nil {
			return 0, 0, 0, false
		}
		scale |= int64(ext) << 2
	}
	unpack := func(shift uint) float64 {
		u := (word >> shift) & 32767
		if u > 32766 {
			u = 32766
		}
		return (float64(u)*2/32766 - 1) * float64(scale)
	}
	return unpack(3), unpack(18), unpack(33), true
}
