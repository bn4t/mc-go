package blackbox

import (
	"net"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// vehPair joins two sessions side by side so the driver can ride
// while the watcher sees the shared entity stream.
type vehPair struct {
	ca, cb net.Conn
	pra    *protocol.Reader
	prb    *protocol.Reader
	pwa    *protocol.Writer
	pwb    *protocol.Writer
}

func joinVehPair(t *testing.T, addr string) *vehPair {
	t.Helper()
	ca, pra, pwa := joinPlayBlackbox(t, addr, "BOATA")
	cb, prb, pwb := joinPlayBlackbox(t, addr, "BOATB")
	v := &vehPair{ca: ca, cb: cb, pra: pra, prb: prb, pwa: pwa, pwb: pwb}
	t.Cleanup(func() {
		v.ca.Close()
		v.cb.Close()
	})
	return v
}

// sendMoveVehicle writes the driver-side move_vehicle packet.
func sendMoveVehicle(pw *protocol.Writer, x, y, z float64) {
	var b protocol.Buffer
	b.Double(x)
	b.Double(y)
	b.Double(z)
	b.Float(0)
	b.Float(0)
	b.Bool(true)
	_ = pw.WritePacket(server.IDPlayMoveVehicleSB, b.Bytes())
}

// sendAttackEntity writes the dedicated attack packet (target id only).
func sendAttackEntity(pw *protocol.Writer, eid int32) {
	var b protocol.Buffer
	b.VarInt(eid)
	_ = pw.WritePacket(server.IDPlayAttackSB, b.Bytes())
}

// sendInteractEntity writes a plain right-click interact on eid.
func sendInteractEntity(pw *protocol.Writer, eid int32) {
	var b protocol.Buffer
	b.VarInt(eid)
	b.VarInt(0) // interact
	b.VarInt(0) // main hand
	b.Bool(false)
	_ = pw.WritePacket(server.IDPlayInteractSB, b.Bytes())
}

// sendPlayerInput writes the input-flags packet (0x2B).
func sendPlayerInput(pw *protocol.Writer, flags byte) {
	var b protocol.Buffer
	b.Byte(flags)
	_ = pw.WritePacket(server.IDPlayPlayerInputSB, b.Bytes())
}

// sendPaddleBoat writes a serverbound paddle_boat (left/right bools).
func sendPaddleBoat(pw *protocol.Writer, left, right bool) {
	var b protocol.Buffer
	b.Bool(left)
	b.Bool(right)
	_ = pw.WritePacket(server.IDPlayPaddleBoatSB, b.Bytes())
}

// vehObs is what vehWatch collected over a window: every observable
// wire fact about one entity, keyed the way assertions read it.
type vehObs struct {
	moved      bool            // any position/delta/teleport packet for eid
	moveVeh    bool            // move_vehicle rubber-band to the driver
	passengers int             // latest set_passengers count, -1 unseen
	boolMeta   map[int]bool    // serializer-8 metadata entries seen
	intMeta    map[int]int32   // serializer-1 (VarInt) entries seen
	intMetaMax map[int]int32   // largest int value seen per index
	floatMeta  map[int]float32 // serializer-3 (Float) entries seen
	floatMax   map[int]float32 // largest float value seen per index
	removed    map[int32]bool  // ids seen in remove_entities
	itemEIDs   []int32         // eids of add_entity spawns of type "item"
	disconnect bool            // a play-stage disconnect packet arrived
	syncSeen   bool            // a sync_pos (player teleport) arrived on either conn
	sync       [3]float64      // last sync_pos coordinates
}

// vehWatch collects packets matching (eid, kind) over the window on
// the chosen connection, while servicing BOTH sockets — the other
// conn still needs keepalives, chunk acks and teleport confirms
// answered or vanilla gates the driver's next move_vehicle behind
// updateAwaitingTeleport. Metadata entries {index, serializer, value}
// are captured for the serializers this suite reads (byte=0, int=1,
// float=3, bool=8).
func vehWatch(t *testing.T, v *vehPair, watchB bool, eid int32, window time.Duration) vehObs {
	t.Helper()
	o := vehObs{
		passengers: -1,
		boolMeta:   map[int]bool{},
		intMeta:    map[int]int32{},
		intMetaMax: map[int]int32{},
		floatMeta:  map[int]float32{},
		floatMax:   map[int]float32{},
		removed:    map[int32]bool{},
	}
	poll := func(c net.Conn, pr *protocol.Reader, pw *protocol.Writer, observe bool) {
		_ = c.SetReadDeadline(time.Now().Add(30 * time.Millisecond))
		for {
			p, err := pr.ReadPacket()
			if err != nil {
				return
			}
			switch int32(p.ID) {
			case server.IDPlayKeepAliveCB:
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
			case server.IDPlayChunkBatchDone:
				_ = ackChunkBatch(pw)
			case server.IDPlaySyncPos:
				r := protocol.NewPacketReader(p.Data)
				id, _ := r.VarInt()
				x, _ := r.Double()
				y, _ := r.Double()
				z, _ := r.Double()
				o.syncSeen = true
				o.sync = [3]float64{x, y, z}
				var b protocol.Buffer
				b.VarInt(id)
				_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
			default:
				if !observe {
					continue
				}
				switch int32(p.ID) {
				case 0x6B: // set_passengers
					r := protocol.NewPacketReader(p.Data)
					veid, _ := r.VarInt()
					if veid == eid {
						n, _ := r.VarInt()
						o.passengers = int(n)
					}
				case 0x39: // move_vehicle rubber-band to the driver
					o.moveVeh = true
				case 0x4D: // remove_entities
					r := protocol.NewPacketReader(p.Data)
					n, _ := r.VarInt()
					for i := int32(0); i < n; i++ {
						rid, _ := r.VarInt()
						o.removed[rid] = true
					}
				case 0x20: // disconnect
					o.disconnect = true
				case 1: // add_entity — count item-entity spawns
					r := protocol.NewPacketReader(p.Data)
					peid, _ := r.VarInt()
					r.UUID()
					ty, _ := r.VarInt()
					if n, _ := server.EntityTypeName(ty); n == "item" {
						o.itemEIDs = append(o.itemEIDs, peid)
					}
				case 35, 53, 54, 125: // pos sync / delta / teleport
					r := protocol.NewPacketReader(p.Data)
					peid, _ := r.VarInt()
					if peid == eid {
						o.moved = true
					}
				case server.IDPlayEntityMetadata:
					r := protocol.NewPacketReader(p.Data)
					peid, _ := r.VarInt()
					if peid != eid {
						continue
					}
					for {
						idx, err := r.Byte()
						if err != nil || idx == 0xFF {
							break
						}
						ser, err := r.VarInt()
						if err != nil {
							break
						}
						switch ser {
						case 0:
							_, _ = r.Byte()
						case 1: // int
							vv, err := r.VarInt()
							if err != nil {
								break
							}
							o.intMeta[int(idx)] = vv
							if vv > o.intMetaMax[int(idx)] {
								o.intMetaMax[int(idx)] = vv
							}
						case 3: // float
							vv, err := r.Float()
							if err != nil {
								break
							}
							o.floatMeta[int(idx)] = vv
							if vv > o.floatMax[int(idx)] {
								o.floatMax[int(idx)] = vv
							}
						case 8: // boolean
							vv, err := r.Bool()
							if err != nil {
								break
							}
							o.boolMeta[int(idx)] = vv
						default:
							goto done
						}
					}
				done:
				}
			}
		}
	}
	end := time.Now().Add(window)
	for time.Now().Before(end) {
		poll(v.ca, v.pra, v.pwa, !watchB)
		poll(v.cb, v.prb, v.pwb, watchB)
	}
	return o
}

// boatEID waits for the watcher's oak_boat add_entity, servicing both
// sockets so the driver's pending teleport confirms don't stall the
// move gate.
func boatEID(t *testing.T, v *vehPair, window time.Duration) int32 {
	t.Helper()
	found := int32(0)
	poll := func(c net.Conn, pr *protocol.Reader, pw *protocol.Writer, observe bool) {
		_ = c.SetReadDeadline(time.Now().Add(30 * time.Millisecond))
		for {
			p, err := pr.ReadPacket()
			if err != nil {
				return
			}
			switch int32(p.ID) {
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
			default:
				if !observe {
					continue
				}
				if int32(p.ID) == 1 {
					r := protocol.NewPacketReader(p.Data)
					eid, _ := r.VarInt()
					r.UUID()
					ty, _ := r.VarInt()
					if n, _ := server.EntityTypeName(ty); n == "oak_boat" {
						found = eid
					}
				}
			}
		}
	}
	end := time.Now().Add(window)
	for time.Now().Before(end) && found == 0 {
		poll(v.ca, v.pra, v.pwa, false)
		poll(v.cb, v.prb, v.pwb, true)
	}
	if found == 0 {
		t.Fatalf("no oak_boat spawn seen within %s", window)
	}
	return found
}

// TestVehicleMoveParity pins the ride + move_vehicle pipeline:
// interact mounts the driver (set_passengers), an in-range
// move_vehicle relocates the vehicle for other viewers, a >10-block
// jump rubber-bands the driver back, and sneak dismounts.
func TestVehicleMoveParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB"),
	}, nil)
	defer targets.cleanup()

	run := func(t *testing.T, tag, addr string) {
		v := joinVehPair(t, addr)
		drainChunkWindow(t, tag, v.ca, v.pra, v.pwa, 400*time.Millisecond)
		drainChunkWindow(t, tag, v.cb, v.prb, v.pwb, 400*time.Millisecond)
		sendCommand(t, v.pwa, "tp @s 4.5 -60 8.5 0 0")
		sendCommand(t, v.pwb, "tp @s 4.5 -60 10.5 0 0")
		var mv protocol.Buffer
		mv.Double(4.5)
		mv.Double(-60)
		mv.Double(8.5)
		mv.Byte(0x01)
		_ = v.pwa.WritePacket(server.IDPlayMovePosSB, mv.Bytes())
		time.Sleep(300 * time.Millisecond)
		sendCommand(t, v.pwa, "summon minecraft:oak_boat 8 -60 8")

		eid := boatEID(t, v, 3*time.Second)
		sendInteractEntity(v.pwa, eid)
		if o := vehWatch(t, v, true, eid, 2*time.Second); o.passengers != 1 {
			t.Fatalf("%s: interact mounted %d passengers, want 1", tag, o.passengers)
		}

		// A sane move (3 blocks) applies — watchers see the vehicle go.
		sendMoveVehicle(v.pwa, 11, -60, 8)
		if o := vehWatch(t, v, true, eid, 2*time.Second); !o.moved {
			t.Fatalf("%s: in-range move_vehicle never reached the watcher", tag)
		}

		// A 12-block jump overshoots the 100 dist² gate: the driver
		// gets a move_vehicle rubber-band, the watcher sees nothing.
		sendMoveVehicle(v.pwa, 23, -60, 8)
		if o := vehWatch(t, v, false, eid, 2*time.Second); !o.moveVeh {
			t.Fatalf("%s: oversized move_vehicle drew no rubber-band", tag)
		}

		// Paddle input: the driver raises DATA_ID_PADDLE_LEFT on the
		// boat — watchers see a set_entity_data dirty pack.
		sendPaddleBoat(v.pwa, true, true)
		if o := vehWatch(t, v, true, eid, 2*time.Second); !o.boolMeta[11] || !o.boolMeta[12] {
			t.Fatalf("%s: paddle input produced metadata %v, want indices 11/12 true", tag, o.boolMeta)
		}
		sendPaddleBoat(v.pwa, false, false)
		o := vehWatch(t, v, true, eid, 2*time.Second)
		if v, ok := o.boolMeta[11]; !ok || v {
			t.Fatalf("%s: paddle release metadata %v, want index 11 false", tag, o.boolMeta)
		}

		// Sneak dismounts: passenger count drops to zero and the
		// rider teleports to the side-escape dismount spot — a
		// getCollisionHorizontalEscapeVector step (boat at (11,-60,8),
		// rider yaw 0 -> +1.2723z) landing on the air floor at bpY.
		sendPlayerInput(v.pwa, 0x20)
		o = vehWatch(t, v, true, eid, 2*time.Second)
		if o.passengers != 0 {
			t.Fatalf("%s: sneak dismount left %d passengers", tag, o.passengers)
		}
		if !o.syncSeen {
			t.Fatalf("%s: dismount sent the rider no position sync", tag)
		}
		want := [3]float64{11, -60, 8 + 1.272276811962001}
		if d := o.sync[0] - want[0]; d*d > 0.01 {
			t.Fatalf("%s: dismount x %v, want %v", tag, o.sync, want)
		}
		if d := o.sync[1] - want[1]; d*d > 0.01 {
			t.Fatalf("%s: dismount y %v, want %v", tag, o.sync, want)
		}
		if d := o.sync[2] - want[2]; d*d > 0.01 {
			t.Fatalf("%s: dismount z %v, want %v", tag, o.sync, want)
		}
		sendCommand(t, v.pwa, "kill @e[type=minecraft:oak_boat]")
		time.Sleep(1200 * time.Millisecond)
	}

	for _, tc := range []struct{ tag, addr string }{
		{"vanilla", targets.vanilla},
		{"mc-go", targets.mcgo},
	} {
		t.Run("ride_move_"+tc.tag, func(t *testing.T) { run(t, tc.tag, tc.addr) })
	}
}

// TestVehicleHurtParity pins VehicleEntity.hurtServer: a melee hit on a
// boat emits a set_entity_data dirty pack carrying the wobble state —
// DATA_ID_HURT(8)=10, DATA_ID_HURTDIR(9) sign-flipped from +1, and
// DATA_ID_DAMAGE(10) growing by amount*10 per hit. Past damage 40 the
// boat is destroyed: remove_entities plus an item-entity spawn of the
// boat item. A creative (instabuild) hit instead discards it with no
// drop.
func TestVehicleHurtParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB"),
	}, nil)
	defer targets.cleanup()

	run := func(t *testing.T, tag, addr string) {
		v := joinVehPair(t, addr)
		drainChunkWindow(t, tag, v.ca, v.pra, v.pwa, 400*time.Millisecond)
		drainChunkWindow(t, tag, v.cb, v.prb, v.pwb, 400*time.Millisecond)
		sendCommand(t, v.pwa, "gamemode survival BOATA")
		sendCommand(t, v.pwb, "gamemode creative BOATB")
		sendCommand(t, v.pwa, "tp @s 6 -60 8 0 0")
		sendCommand(t, v.pwb, "tp @s 6 -60 10.5 0 0")
		time.Sleep(300 * time.Millisecond)
		sendCommand(t, v.pwa, "summon minecraft:oak_boat 8 -60 8")

		eid := boatEID(t, v, 3*time.Second)

		// One unarmed hit: the dirty pack carries hurt=10, hurtDir
		// flipped to -1, damage 1*10=10.
		sendAttackEntity(v.pwa, eid)
		o := vehWatch(t, v, true, eid, 1500*time.Millisecond)
		if o.intMeta[9] != -1 {
			t.Fatalf("%s: hurt hit 1: hurtDir=%d, want -1", tag, o.intMeta[9])
		}
		if o.intMetaMax[8] <= 0 {
			t.Fatalf("%s: hurt hit 1: hurtTime never rose (max=%d)", tag, o.intMetaMax[8])
		}
		if o.floatMax[10] < 9 {
			t.Fatalf("%s: hurt hit 1: damage max=%v, want ~10", tag, o.floatMax[10])
		}

		// Hit 2: hurtDir flips back to +1 and damage re-sets to ~10 —
		// the first hit's 10 points fully decayed during the watch
		// window, so the bank restarts at zero each idle gap.
		sendAttackEntity(v.pwa, eid)
		o = vehWatch(t, v, true, eid, 1500*time.Millisecond)
		if o.intMeta[9] != 1 {
			t.Fatalf("%s: hurt hit 2: hurtDir=%d, want +1", tag, o.intMeta[9])
		}
		if o.floatMax[10] < 9 {
			t.Fatalf("%s: hurt hit 2: damage max=%v, want ~10", tag, o.floatMax[10])
		}

		// Banked damage decays 1/tick, so unarmed pokes never add up.
		// A diamond sword hits for 8 → +80 damage in one blow: the boat
		// breaks — watcher sees remove_entities plus the item spawn.
		sendCommand(t, v.pwa, "item replace entity BOATA weapon.mainhand with minecraft:diamond_sword")
		time.Sleep(600 * time.Millisecond)
		sendAttackEntity(v.pwa, eid)
		o = vehWatch(t, v, true, eid, 2*time.Second)
		if !o.removed[eid] {
			t.Fatalf("%s: a sword hit never removed the boat (damage max=%v)", tag, o.floatMax[10])
		}
		if len(o.itemEIDs) == 0 {
			t.Fatalf("%s: destroyed boat dropped no item entity", tag)
		}
		itemEID := o.itemEIDs[0]

		// Creative hit on a fresh boat: instabuild discards it —
		// remove_entities without an item drop.
		sendCommand(t, v.pwa, "gamemode creative BOATA")
		sendCommand(t, v.pwa, "summon minecraft:oak_boat 8 -60 8")
		eid = boatEID(t, v, 3*time.Second)
		sendAttackEntity(v.pwa, eid)
		o = vehWatch(t, v, true, eid, 1500*time.Millisecond)
		if !o.removed[eid] {
			t.Fatalf("%s: creative hit left the boat in place", tag)
		}
		if len(o.itemEIDs) != 0 {
			t.Fatalf("%s: creative discard spawned %d item entities, want 0", tag, len(o.itemEIDs))
		}

		// Attacking an ItemEntity (or xp orb / yourself / a
		// non-attackable arrow) is invalid: vanilla disconnects with
		// multiplayer.disconnect.invalid_entity_attacked, reach-gated.
		// The sword leg's drop may be picked up already, so summon a
		// fresh stack with a huge pickup delay.
		sendCommand(t, v.pwa, "gamemode survival BOATA")
		sendCommand(t, v.pwa, `summon minecraft:item 7 -60 9 {Item:{id:"minecraft:oak_boat",count:1},PickupDelay:32766}`)
		found := int32(0)
		o = vehWatch(t, v, true, eid, 2*time.Second)
		for _, ie := range o.itemEIDs {
			found = ie
		}
		if found == 0 {
			t.Fatalf("%s: summoned item entity never showed", tag)
		}
		sendAttackEntity(v.pwa, found)
		o = vehWatch(t, v, false, eid, 2*time.Second)
		if !o.disconnect {
			t.Fatalf("%s: attacking an item entity did not disconnect the attacker", tag)
		}
		_ = itemEID
	}

	for _, tc := range []struct{ tag, addr string }{
		{"vanilla", targets.vanilla},
		{"mc-go", targets.mcgo},
	} {
		t.Run("vehicle_hurt_"+tc.tag, func(t *testing.T) { run(t, tc.tag, tc.addr) })
	}
}
