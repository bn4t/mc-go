//go:build blackbox

package blackbox

import (
	"net"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestEggThrowParity exercises the hand-thrown-projectile wire surface:
// EggItem.use / SnowballItem.use — the unconditional block_change_ack,
// the named throw sound at the player, spawn_entity carrying the owner
// id, the DATA_ITEM_STACK set_entity_data, the survival consume's
// container_set_slot — and ThrownEgg/Snowball.onHit: broadcastEntityEvent
// status 3 + remove_entities, plus the egg's 1/8 chick hatch (1/32 of
// those hatching four) as a rate assertion over a bounded volley.
func TestEggThrowParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("EggWire"),
	}, nil)
	defer targets.cleanup()

	for _, tc := range []struct{ tag, addr string }{
		{"vanilla", targets.vanilla},
		{"mc-go", targets.mcgo},
	} {
		t.Run(tc.tag, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, "EggWire")
			defer c.Close()
			drainToJoinBlackbox(t, tc.tag, pr)
			sendCommand(t, pw, "gamemode survival @s")
			sendCommand(t, pw, "tp @s 60.5 -60 60.5 0 -35")
			drainChunkWindow(t, tc.tag, c, pr, pw, 400*time.Millisecond)
			sendCommand(t, pw, "give @s minecraft:egg 64")
			// Egg in hand = hotbar slot 0.
			eggDrainFor(t, c, pr, pw, 700*time.Millisecond)

			// Leg 1 — a single hand throw: the deterministic packet
			// set around spawn_entity.
			sendUseItem(t, pw, 0, 1)
			ev := eggDrainFor(t, c, pr, pw, 2500*time.Millisecond)
			assertHasAck(t, tc.tag, ev, 1)
			assertHasSound(t, tc.tag, ev, "entity.egg.throw")
			eggID := assertThrowSpawn(t, tc.tag, ev, "egg")
			assertHasMeta(t, tc.tag, ev, eggID)
			assertHasSlotShrink(t, tc.tag, ev)

			// Leg 2 — a snowball throw: same shape, NEUTRAL category.
			sendCommand(t, pw, "clear @s")
			sendCommand(t, pw, "give @s minecraft:snowball 16")
			sendCommand(t, pw, "kill @e[type=minecraft:chicken]")
			eggDrainFor(t, c, pr, pw, 900*time.Millisecond)
			sendUseItem(t, pw, 0, 2)
			ev = eggDrainFor(t, c, pr, pw, 1500*time.Millisecond)
			assertHasAck(t, tc.tag, ev, 2)
			assertHasSound(t, tc.tag, ev, "entity.snowball.throw")
			assertThrowSpawn(t, tc.tag, ev, "snowball")

			// Leg 3 — impact: pitch up so the egg falls back to ground.
			// onHit's broadcastEntityEvent(3) is tracker-scoped and
			// never actually ships (same-tick discard) — the measured
			// vanilla wire is remove_entities alone.
			sendCommand(t, pw, "clear @s")
			sendCommand(t, pw, "give @s minecraft:egg 64")
			sendCommand(t, pw, "kill @e[type=minecraft:chicken]")
			eggDrainFor(t, c, pr, pw, 900*time.Millisecond)
			sendUseItem(t, pw, 0, 3)
			ev = eggDrainFor(t, c, pr, pw, 6*time.Second)
			egg2 := assertThrowSpawn(t, tc.tag, ev, "egg")
			assertNoEntityEvent(t, tc.tag, ev, egg2)
			assertRemoved(t, tc.tag, ev, egg2)

			// Leg 4 — top-face veto: pitch a volley up over flat ground.
			// Vanilla's fudgePositionAfterSizeChange never finds a free
			// spot for the zero-size chick on a ground impact — measured
			// 0 chicks per 64 eggs — so nothing may hatch here on either
			// server. give is re-issued per volley because max_stack_size
			// 16 empties the held stack after 16 throws.
			sendCommand(t, pw, "tp @s 0.5 -60 0.5 -90 0")
			sendCommand(t, pw, "kill @e[type=minecraft:chicken]")
			eggDrainFor(t, c, pr, pw, 900*time.Millisecond)
			flatEv := []eggEvent{}
			for v := 0; v < 3; v++ {
				sendCommand(t, pw, "clear @s")
				sendCommand(t, pw, "give @s minecraft:egg 16")
				eggDrainFor(t, c, pr, pw, 300*time.Millisecond)
				for i := 0; i < 16; i++ {
					sendUseItemAim(t, pw, 0, int32(100+v*16+i), -90, -35)
				}
				flatEv = append(flatEv, eggDrainFor(t, c, pr, pw, 3500*time.Millisecond)...)
			}
			if n := countChickenSpawns(flatEv); n != 0 {
				t.Fatalf("%s: %d chicks hatched off ground impacts — vanilla vetoes them all (0/64 measured)", tc.tag, n)
			}

			// Leg 5 — side-face hatch: eggs into a wall face die in free
			// air, so the 1/8 roll applies: 48 throws ⇒ mean 6, [1,16]
			// brackets the binomial tails on both servers.
			sendCommand(t, pw, "tp @s 60.5 -60 60.5 -90 0")
			sendCommand(t, pw, "fill 63 -61 58 63 -58 63 minecraft:stone")
			sendCommand(t, pw, "kill @e[type=minecraft:chicken]")
			eggDrainFor(t, c, pr, pw, 900*time.Millisecond)
			wallEv := []eggEvent{}
			for v := 0; v < 3; v++ {
				sendCommand(t, pw, "clear @s")
				sendCommand(t, pw, "give @s minecraft:egg 16")
				eggDrainFor(t, c, pr, pw, 300*time.Millisecond)
				for i := 0; i < 16; i++ {
					sendUseItemAim(t, pw, 0, int32(200+v*16+i), -90, 0)
				}
				wallEv = append(wallEv, eggDrainFor(t, c, pr, pw, 2500*time.Millisecond)...)
			}
			n := countChickenSpawns(wallEv)
			if n < 1 || n > 16 {
				t.Fatalf("%s: %d chicks hatched from 48 wall-face eggs — outside the 1/8 binomial band [1,16]", tc.tag, n)
			}
			t.Logf("%s: %d chicks from 48 wall-face eggs", tc.tag, n)
		})
	}
}

// eggEvent is one decoded clientbound packet relevant to this test.
type eggEvent struct {
	kind    string // "ack", "sound", "spawn", "meta", "event", "remove", "slot", "content", "level"
	eid     int32  // entity id (spawn/event/remove/meta) or slot number
	b0      int32  // seq (ack), status (event), count (slot), event id (level)
	etype   int32  // spawn packet's entity-type registry id
	data    int32  // spawn packet's data field (owner id) or level_event data
	oval    int32  // orb value decoded from an index-8 VarInt meta entry
	hasOval bool
}

func eggDrain(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, quiet time.Duration) []eggEvent {
	t.Helper()
	return eggDrainCollect(t, c, pr, pw, quiet, 0)
}

// eggDrainFor reads for a fixed wall-clock duration — needed once live
// chicks are on the field, since their wander packets never let the
// wire go quiet.
func eggDrainFor(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, dur time.Duration) []eggEvent {
	t.Helper()
	return eggDrainCollect(t, c, pr, pw, 500*time.Millisecond, dur)
}

func eggDrainCollect(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, quiet, dur time.Duration) []eggEvent {
	t.Helper()
	var out []eggEvent
	deadline := time.Now().Add(dur)
	for {
		if dur > 0 && time.Now().After(deadline) {
			break
		}
		_ = c.SetReadDeadline(time.Now().Add(quiet))
		p, err := pr.ReadPacket()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				if dur > 0 {
					continue
				}
				break
			}
			t.Fatalf("egg drain: %v", err)
		}
		switch p.ID {
		case server.IDPlayKeepAliveCB:
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
		case server.IDPlayChunkBatchDone:
			_ = ackChunkBatch(pw)
		case server.IDPlaySyncPos:
			r := protocol.NewPacketReader(p.Data)
			id, e1 := r.VarInt()
			x, e2 := r.Double()
			y, e3 := r.Double()
			z, e4 := r.Double()
			if e1 == nil {
				var b protocol.Buffer
				b.VarInt(id)
				_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
				// Echo the position like a real client; vanilla gates
				// later block actions on the acknowledged teleport.
				var mb protocol.Buffer
				mb.Double(x)
				mb.Double(y)
				mb.Double(z)
				mb.Byte(0)
				_ = pw.WritePacket(server.IDPlayMovePosSB, mb.Bytes())
			}
			if e1 == nil && e2 == nil && e3 == nil && e4 == nil {
				out = append(out, eggEvent{kind: "syncpos", etype: int32(x * 100), data: int32(y * 100), b0: int32(z * 100)})
			}
		case 0x04: // block_changed_ack
			if seq, err := protocol.NewPacketReader(p.Data).VarInt(); err == nil {
				out = append(out, eggEvent{kind: "ack", b0: seq})
			}
		case 0x75: // sound (named): VarInt id, VarInt category, pos, vol, pitch, seed
			r := protocol.NewPacketReader(p.Data)
			id, e1 := r.VarInt()
			cat, e2 := r.VarInt()
			if e1 == nil && e2 == nil {
				out = append(out, eggEvent{kind: "sound", eid: id, b0: cat})
			}
		case 0x01: // spawn_entity
			r := protocol.NewPacketReader(p.Data)
			eid, e1 := r.VarInt()
			_, e2 := r.UUID()
			et, e3 := r.VarInt()
			if e1 != nil || e2 != nil || e3 != nil {
				break
			}
			_, _ = r.Double()
			_, _ = r.Double()
			_, _ = r.Double()
			// velocity rides the variable-length LpVec3 — skip it
			// properly or a moving entity's bytes leak into the
			// pitch/yaw/data reads below.
			_, _, _, _ = r.ReadLpVec3()
			_, _ = r.Byte() // pitch
			_, _ = r.Byte() // yaw
			_, _ = r.Byte() // head yaw
			data, _ := r.VarInt()
			out = append(out, eggEvent{kind: "spawn", eid: eid, etype: et, data: data})
		case 0x63: // set_entity_data
			r := protocol.NewPacketReader(p.Data)
			if eid, err := r.VarInt(); err == nil {
				ev := eggEvent{kind: "meta", eid: eid}
				// An experience orb's DATA_VALUE is index 8, serializer
				// 1 (VarInt); pull it out when present. The index-0
				// shared-flags byte (serializer 0) rides b0 — it
				// carries on_fire(0x01)/glowing(0x40) for the fireball
				// hit. Other entry types aren't length-skippable, so
				// stop after the first entry either way.
				if idx, e1 := r.Byte(); e1 == nil {
					if ty, e2 := r.VarInt(); e2 == nil {
						if idx == 8 && ty == 1 {
							if v, e3 := r.VarInt(); e3 == nil {
								ev.oval = v
								ev.hasOval = true
							}
						}
						if idx == 9 && ty == 3 {
							// LivingEntity DATA_HEALTH_ID: index 9,
							// float serializer — oval is hp*100.
							if f, e3 := r.Float(); e3 == nil {
								ev.oval = int32(f * 100)
								ev.hasOval = true
							}
						}
						if idx == 0 && ty == 0 {
							if v, e3 := r.Byte(); e3 == nil {
								ev.b0 = int32(v)
								ev.hasOval = true // reuse as "flag byte present"
							}
						}
					}
				}
				out = append(out, ev)
			}
		case 0x22: // entity_event: Int eid + byte status
			r := protocol.NewPacketReader(p.Data)
			eid, e1 := r.Int()
			st, e2 := r.Byte()
			if e1 == nil && e2 == nil {
				out = append(out, eggEvent{kind: "event", eid: eid, b0: int32(st)})
			}
		case 0x4D: // remove_entities
			r := protocol.NewPacketReader(p.Data)
			n, err := r.VarInt()
			if err != nil {
				break
			}
			for i := int32(0); i < n; i++ {
				if id, err := r.VarInt(); err == nil {
					out = append(out, eggEvent{kind: "remove", eid: id})
				}
			}
		case 0x14: // container_set_slot: window VarInt, state VarInt, slot Short, item
			r := protocol.NewPacketReader(p.Data)
			win, e1 := r.VarInt()
			_, e2 := r.VarInt()
			slot, e3 := r.Short()
			if e1 == nil && e2 == nil && e3 == nil {
				out = append(out, eggEvent{kind: "slot", eid: win, b0: int32(slot)})
			}
		case 0x12: // container_set_content — vanilla's player-inventory
			// sync batches the shrink into a full refresh.
			out = append(out, eggEvent{kind: "content"})
		case 0x2E: // level_event: Int event, Long pos, Int data, Bool global
			r := protocol.NewPacketReader(p.Data)
			evID, e1 := r.Int()
			_, _ = r.Long() // packed position
			dat, e3 := r.Int()
			if e1 == nil && e3 == nil {
				out = append(out, eggEvent{kind: "level", b0: evID, data: dat})
			}
		case 0x19: // damage_event: VarInt victim, type, cause+1, direct+1, pos?
			r := protocol.NewPacketReader(p.Data)
			vid, e1 := r.VarInt()
			ty, e2 := r.VarInt()
			cz, e3 := r.VarInt()
			dr, e4 := r.VarInt()
			if e1 == nil && e2 == nil && e3 == nil && e4 == nil {
				out = append(out, eggEvent{kind: "dmg", eid: vid, etype: ty, b0: cz, data: dr})
			}
		case 0x65: // set_entity_motion
			if eid, err := protocol.NewPacketReader(p.Data).VarInt(); err == nil {
				out = append(out, eggEvent{kind: "motion", eid: eid})
			}
		case 0x67: // set_experience: Float bar, VarInt level, VarInt total
			r := protocol.NewPacketReader(p.Data)
			bar, e1 := r.Float()
			lvl, e2 := r.VarInt()
			tot, e3 := r.VarInt()
			if e1 == nil && e2 == nil && e3 == nil {
				out = append(out, eggEvent{kind: "xp", eid: int32(bar * 100), etype: lvl, data: tot})
			}
		case 0x2F: // level_particles: Bool overrideLimiter, Bool
			// alwaysShow, 3xDouble pos, 3xFloat spread, Float speed,
			// Int count, VarInt particle id, data…
			r := protocol.NewPacketReader(p.Data)
			_, _ = r.Bool()
			_, _ = r.Bool()
			px, _ := r.Double()
			py, _ := r.Double()
			pz, _ := r.Double()
			_, _ = r.Float()
			_, _ = r.Float()
			_, _ = r.Float()
			_, _ = r.Float()
			_, _ = r.Int()
			pid, _ := r.VarInt()
			out = append(out, eggEvent{kind: "particles", etype: pid, eid: int32(px * 100), b0: int32(py * 100), data: int32(pz * 100)})
		case 0x24: // explode: 3 doubles center, Float radius, Int
			// blockCount, Bool hasKB [+3 doubles knockback], VarInt
			// particle, VarInt sound, VarInt weighted-block-particles
			r := protocol.NewPacketReader(p.Data)
			_, _ = r.Double()
			_, _ = r.Double()
			_, _ = r.Double()
			rad, e1 := r.Float()
			cnt, e2 := r.Int()
			kb, e3 := r.Bool()
			if e3 == nil && kb {
				_, _ = r.Double()
				_, _ = r.Double()
				_, _ = r.Double()
			}
			_, e4 := r.VarInt() // particle
			snd, e5 := r.VarInt()
			if e1 == nil && e2 == nil && e3 == nil && e4 == nil && e5 == nil {
				b := int32(0)
				if kb {
					b = 1
				}
				out = append(out, eggEvent{kind: "explode", etype: int32(rad * 100), data: cnt, b0: b, oval: snd, hasOval: true})
			}
		}
	}
	_ = c.SetReadDeadline(time.Time{})
	return out
}

// sendUseItem encodes Serverbound Use Item: VarInt hand, VarInt
// sequence, Float yaw, Float pitch. yaw 0 pitch -35 pitches the
// projectile up so it falls back to ground.
func sendUseItem(t *testing.T, pw *protocol.Writer, hand, seq int32) {
	t.Helper()
	sendUseItemAim(t, pw, hand, seq, 0, -35)
}

func sendUseItemAim(t *testing.T, pw *protocol.Writer, hand, seq int32, yaw, pitch float32) {
	t.Helper()
	var b protocol.Buffer
	b.VarInt(hand)
	b.VarInt(seq)
	b.Float(yaw)
	b.Float(pitch)
	if err := pw.WritePacket(server.IDPlayUseItemSB, b.Bytes()); err != nil {
		t.Fatalf("use_item: %v", err)
	}
}

func assertHasAck(t *testing.T, tag string, ev []eggEvent, seq int32) {
	t.Helper()
	for _, e := range ev {
		if e.kind == "ack" && e.b0 == seq {
			return
		}
	}
	t.Fatalf("%s: no block_changed_ack for seq %d", tag, seq)
}

// assertAckCovers passes when any block_changed_ack carries seq >=
// the request — vanilla's ackBlockChangesUpTo batches to one packet
// per tick carrying the max sequence seen, so back-to-back use_item
// packets produce a single ack for the higher seq.
func assertAckCovers(t *testing.T, tag string, ev []eggEvent, seq int32) {
	t.Helper()
	for _, e := range ev {
		if e.kind == "ack" && e.b0 >= seq {
			return
		}
	}
	t.Fatalf("%s: no block_changed_ack covering seq %d", tag, seq)
}

func assertHasSound(t *testing.T, tag string, ev []eggEvent, name string) {
	t.Helper()
	want, _ := server.SoundEventID(name)
	for _, e := range ev {
		// named_sound carries the registry holder id — soundEventIDs
		// is zero-based so the wire value is id+1.
		if e.kind == "sound" && e.eid == want+1 {
			return
		}
	}
	var got []int32
	for _, e := range ev {
		if e.kind == "sound" {
			got = append(got, e.eid)
		}
	}
	t.Fatalf("%s: no %s sound (wire id %d) — got %v", tag, name, want+1, got)
}

// assertThrowSpawn finds the thrown projectile's spawn_entity and
// pins its owner data field nonzero; the entity type id is asserted
// only by name-lookup consistency across both legs.
func assertThrowSpawn(t *testing.T, tag string, ev []eggEvent, entName string) int32 {
	t.Helper()
	want, _ := server.EntityTypeID(entName)
	for _, e := range ev {
		if e.kind == "spawn" && e.etype == want {
			if e.data == 0 {
				t.Fatalf("%s: %s spawn carried data=0 — vanilla sets the owner id", tag, entName)
			}
			return e.eid
		}
	}
	t.Fatalf("%s: no spawn_entity of type %s (%d)", tag, entName, want)
	return 0
}

func assertHasMeta(t *testing.T, tag string, ev []eggEvent, eid int32) {
	t.Helper()
	for _, e := range ev {
		if e.kind == "meta" && e.eid == eid {
			return
		}
	}
	t.Fatalf("%s: no set_entity_data for thrown entity %d — the DATA_ITEM_STACK entry is missing", tag, eid)
}

// assertHasSlotShrink accepts either inventory-sync shape: vanilla
// batches the shrink into container_set_content, mc-go sends the
// single container_set_slot. Both are the same semantic wire.
func assertHasSlotShrink(t *testing.T, tag string, ev []eggEvent) {
	t.Helper()
	for _, e := range ev {
		if e.kind == "slot" || e.kind == "content" {
			return
		}
	}
	t.Fatalf("%s: no inventory sync packet after the survival throw", tag)
}

// assertNoEntityEvent pins the measured vanilla behavior: the
// broadcastEntityEvent(3) call in onHit never produces a wire packet
// for a same-tick-discarded projectile.
func assertNoEntityEvent(t *testing.T, tag string, ev []eggEvent, eid int32) {
	t.Helper()
	for _, e := range ev {
		if e.kind == "event" && e.eid == eid {
			t.Fatalf("%s: unexpected entity_event for %d — vanilla ships remove_entities alone", tag, eid)
		}
	}
}

func assertRemoved(t *testing.T, tag string, ev []eggEvent, eid int32) {
	t.Helper()
	for _, e := range ev {
		if e.kind == "remove" && e.eid == eid {
			return
		}
	}
	t.Fatalf("%s: thrown entity %d never left via remove_entities", tag, eid)
}

func countChickenSpawns(ev []eggEvent) int {
	ct, _ := server.EntityTypeID("chicken")
	n := 0
	for _, e := range ev {
		if e.kind == "spawn" && e.etype == ct {
			n++
		}
	}
	return n
}
