package blackbox

import (
	"fmt"
	"math"
	"net"
	"regexp"
	"strconv"
	"strings"
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

// TestMinecartDismountParity pins AbstractMinecart's dismount scan: a
// plain minecart is rideable via interact (container carts are not),
// and sneak-dismount walks the direction-offset table measured live —
// for a cart summoned facing each cardinal yaw the rider lands on the
// block BEHIND the facing (the first preferred offset).
func TestMinecartDismountParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB"),
	}, nil)
	defer targets.cleanup()

	// Measured on vanilla: yaw -> landing block (cart at 8.5,-60,8.5).
	legs := []struct {
		yaw  string
		want [3]float64
	}{
		{"0", [3]float64{8.5, -60, 7.5}},
		{"90", [3]float64{9.5, -60, 8.5}},
		{"180", [3]float64{8.5, -60, 9.5}},
		{"270", [3]float64{7.5, -60, 8.5}},
	}

	run := func(t *testing.T, tag, addr string) {
		v := joinVehPair(t, addr)
		drainChunkWindow(t, tag, v.ca, v.pra, v.pwa, 300*time.Millisecond)
		drainChunkWindow(t, tag, v.cb, v.prb, v.pwb, 300*time.Millisecond)
		sendCommand(t, v.pwa, "tp @s 6 -60 8 0 0")
		sendCommand(t, v.pwb, "tp @s 6 -60 10.5 0 0")
		time.Sleep(300 * time.Millisecond)
		for _, leg := range legs {
			sendCommand(t, v.pwa, "summon minecraft:minecart 8 -60 8 {Rotation:["+leg.yaw+".0,0.0]}")
			// The watcher waits for the minecart add_entity.
			eid := int32(0)
			end := time.Now().Add(3 * time.Second)
			for time.Now().Before(end) && eid == 0 {
				for _, s := range []struct {
					c  net.Conn
					pr *protocol.Reader
					pw *protocol.Writer
					b  bool
				}{{v.ca, v.pra, v.pwa, false}, {v.cb, v.prb, v.pwb, true}} {
					_ = s.c.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
					for {
						p, err := s.pr.ReadPacket()
						if err != nil {
							break
						}
						switch int32(p.ID) {
						case server.IDPlayKeepAliveCB:
							_ = s.pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
						case server.IDPlayChunkBatchDone:
							_ = ackChunkBatch(s.pw)
						case server.IDPlaySyncPos:
							r := protocol.NewPacketReader(p.Data)
							id, _ := r.VarInt()
							var bb protocol.Buffer
							bb.VarInt(id)
							_ = s.pw.WritePacket(server.IDPlayTeleportConfirmSB, bb.Bytes())
						case 1:
							if !s.b {
								break
							}
							r := protocol.NewPacketReader(p.Data)
							e, _ := r.VarInt()
							r.UUID()
							ty, _ := r.VarInt()
							if n, _ := server.EntityTypeName(ty); n == "minecart" {
								eid = e
							}
						}
					}
				}
			}
			if eid == 0 {
				t.Fatalf("%s yaw=%s: no minecart spawn seen", tag, leg.yaw)
			}
			sendInteractEntity(v.pwa, eid)
			if o := vehWatch(t, v, true, eid, 1500*time.Millisecond); o.passengers != 1 {
				t.Fatalf("%s yaw=%s: minecart mount passengers=%d", tag, leg.yaw, o.passengers)
			}
			sendPlayerInput(v.pwa, 0x20)
			o := vehWatch(t, v, true, eid, 2*time.Second)
			if o.passengers != 0 {
				t.Fatalf("%s yaw=%s: dismount left %d passengers", tag, leg.yaw, o.passengers)
			}
			if !o.syncSeen {
				t.Fatalf("%s yaw=%s: dismount sent no position sync", tag, leg.yaw)
			}
			for i := 0; i < 3; i++ {
				if d := o.sync[i] - leg.want[i]; d*d > 0.01 {
					t.Fatalf("%s yaw=%s: dismount at %v, want %v", tag, leg.yaw, o.sync, leg.want)
				}
			}
			sendCommand(t, v.pwa, "kill @e[type=minecraft:minecart]")
			time.Sleep(400 * time.Millisecond)
		}
	}

	for _, tc := range []struct{ tag, addr string }{
		{"vanilla", targets.vanilla},
		{"mc-go", targets.mcgo},
	} {
		t.Run("cart_dismount_"+tc.tag, func(t *testing.T) { run(t, tc.tag, tc.addr) })
	}
}

// TestMinecartRollParity pins OldMinecartBehavior's off-rail roll
// (26.2's default, MINECART_IMPROVEMENTS off): a cart summoned with
// Motion slides along the ground decaying x0.5/tick until it rests at
// the 0.3/(1-0.5)=0.6-block asymptote, reporting yaw atan2(dx,dz)
// while moving, and a rider's forward input does NOT move a flat-
// ground cart at all (the intent kick is rail-only).
func TestMinecartRollParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB"),
	}, nil)
	defer targets.cleanup()

	run := func(t *testing.T, tag, addr string) {
		v := joinVehPair(t, addr)
		drainChunkWindow(t, tag, v.ca, v.pra, v.pwa, 300*time.Millisecond)
		drainChunkWindow(t, tag, v.cb, v.prb, v.pwb, 300*time.Millisecond)
		sendCommand(t, v.pwa, "tp @s 6 -60 8 0 0")
		sendCommand(t, v.pwb, "tp @s 6 -60 10.5 0 0")
		time.Sleep(300 * time.Millisecond)
		sendCommand(t, v.pwa, "summon minecraft:minecart 8 -60 8 {Motion:[0.3,0.0,0.0]}")
		eid := int32(0)
		end := time.Now().Add(3 * time.Second)
		for time.Now().Before(end) && eid == 0 {
			for _, s := range []struct {
				c  net.Conn
				pr *protocol.Reader
				pw *protocol.Writer
				b  bool
			}{{v.ca, v.pra, v.pwa, false}, {v.cb, v.prb, v.pwb, true}} {
				_ = s.c.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
				for {
					p, err := s.pr.ReadPacket()
					if err != nil {
						break
					}
					switch int32(p.ID) {
					case server.IDPlayKeepAliveCB:
						_ = s.pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
					case server.IDPlayChunkBatchDone:
						_ = ackChunkBatch(s.pw)
					case server.IDPlaySyncPos:
						r := protocol.NewPacketReader(p.Data)
						id, _ := r.VarInt()
						var bb protocol.Buffer
						bb.VarInt(id)
						_ = s.pw.WritePacket(server.IDPlayTeleportConfirmSB, bb.Bytes())
					case 1:
						if !s.b {
							break
						}
						r := protocol.NewPacketReader(p.Data)
						e, _ := r.VarInt()
						r.UUID()
						ty, _ := r.VarInt()
						if n, _ := server.EntityTypeName(ty); n == "minecart" {
							eid = e
						}
					}
				}
			}
		}
		if eid == 0 {
			t.Fatalf("%s: no minecart spawn seen", tag)
		}
		// Slide window: sum deltas and catch the absolute sync.
		var dx, dz, syncX float64
		moveN := 0
		start := time.Now()
		end = start.Add(4 * time.Second)
		for time.Now().Before(end) {
			for _, s := range []struct {
				c  net.Conn
				pr *protocol.Reader
				pw *protocol.Writer
				b  bool
			}{{v.ca, v.pra, v.pwa, false}, {v.cb, v.prb, v.pwb, true}} {
				_ = s.c.SetReadDeadline(time.Now().Add(30 * time.Millisecond))
				for {
					p, err := s.pr.ReadPacket()
					if err != nil {
						break
					}
					switch int32(p.ID) {
					case server.IDPlayKeepAliveCB:
						_ = s.pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
					case server.IDPlayChunkBatchDone:
						_ = ackChunkBatch(s.pw)
					case server.IDPlaySyncPos:
						r := protocol.NewPacketReader(p.Data)
						id, _ := r.VarInt()
						var bb protocol.Buffer
						bb.VarInt(id)
						_ = s.pw.WritePacket(server.IDPlayTeleportConfirmSB, bb.Bytes())
					default:
						if !s.b {
							break
						}
						switch int32(p.ID) {
						case 53, 54: // move_entity_pos / _pos_rot
							r := protocol.NewPacketReader(p.Data)
							peid, _ := r.VarInt()
							if peid != eid {
								break
							}
							sx, _ := r.Short()
							_, _ = r.Short()
							sz, _ := r.Short()
							dx += float64(sx) / 4096
							dz += float64(sz) / 4096
							moveN++
						case 35: // entity_position_sync
							r := protocol.NewPacketReader(p.Data)
							peid, _ := r.VarInt()
							if peid != eid {
								break
							}
							syncX, _ = r.Double()
							_, _ = r.Double()
							_, _ = r.Double()
						}
					}
				}
			}
		}
		if moveN == 0 {
			t.Fatalf("%s: pushed cart never moved", tag)
		}
		if dz*dz > 0.01 {
			t.Fatalf("%s: cart drifted %.4f on Z, want ~0", tag, dz)
		}
		_ = syncX
		// Asymptote: 0.3/(1-0.5) = 0.6 blocks east of the 8.5 spawn →
		// rest ≈9.1. /data get is the authoritative read — packet
		// capture starts after the first moves already ran.
		x, ok := dataGetFloat(t, v, "data get entity @e[type=minecart,limit=1] Pos[0]")
		if !ok {
			t.Fatalf("%s: Pos[0] query gave no number", tag)
		}
		if d := x - 9.1; d*d > 0.01 {
			t.Fatalf("%s: cart rested at X=%.4f, want ~9.1", tag, x)
		}
		yaw, ok := dataGetFloat(t, v, "data get entity @e[type=minecart,limit=1] Rotation[0]")
		if !ok {
			t.Fatalf("%s: Rotation[0] query gave no number", tag)
		}
		// Vanilla's yaw chases the NEGATED displacement: a +X slide
		// gives 180, the 170-degree flip adds 180 and frem-360 leaves
		// the cart at ~0 (probe: vanilla Rotation[0]=0.0).
		d := yaw
		for d > 180 {
			d -= 360
		}
		for d < -180 {
			d += 360
		}
		if d*d > 100 {
			t.Fatalf("%s: sliding +X left yaw %.1f, want ~0", tag, yaw)
		}
		// A rider's forward input must NOT move a flat-ground cart —
		// vanilla's intent kick lives inside moveAlongTrack (rails).
		sendInteractEntity(v.pwa, eid)
		if o := vehWatch(t, v, true, eid, 1500*time.Millisecond); o.passengers != 1 {
			t.Fatalf("%s: mount passengers=%d", tag, o.passengers)
		}
		sendPlayerInput(v.pwa, 0x01)
		_ = vehWatch(t, v, true, eid, 2*time.Second)
		// Position is the proof: periodic position syncs ride the wire
		// for a parked cart too, so only /data get separates a real move.
		x2, ok := dataGetFloat(t, v, "data get entity @e[type=minecart,limit=1] Pos[0]")
		if !ok || (x2-x)*(x2-x) > 0.0025 {
			t.Fatalf("%s: ridden cart at X=%.4f (was %.4f)", tag, x2, x)
		}
		sendPlayerInput(v.pwa, 0x00)
	}

	for _, tc := range []struct{ tag, addr string }{
		{"vanilla", targets.vanilla},
		{"mc-go", targets.mcgo},
	} {
		t.Run("cart_roll_"+tc.tag, func(t *testing.T) { run(t, tc.tag, tc.addr) })
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

// dataGetFloat issues a /data get query on the driver conn and returns
// the last SNBT number its reply renders ("9.0995d", "90.0f"). The
// query's reply text is the measure: a failed query still replies, but
// carries an error component with no d/f-suffixed number.
var dataNumRe = regexp.MustCompile(`(-?\d+(?:\.\d+)?)[df]`)

func dataGetFloat(t *testing.T, v *vehPair, query string) (float64, bool) {
	t.Helper()
	var b protocol.Buffer
	b.String(query)
	if err := v.pwa.WritePacket(server.IDPlayChatCommandSB, b.Bytes()); err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, s := range []struct {
			c  net.Conn
			pr *protocol.Reader
			pw *protocol.Writer
			a  bool
		}{{v.ca, v.pra, v.pwa, true}, {v.cb, v.prb, v.pwb, false}} {
			_ = s.c.SetReadDeadline(time.Now().Add(30 * time.Millisecond))
			for {
				p, err := s.pr.ReadPacket()
				if err != nil {
					break
				}
				switch int32(p.ID) {
				case server.IDPlayKeepAliveCB:
					_ = s.pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
				case server.IDPlayChunkBatchDone:
					_ = ackChunkBatch(s.pw)
				case server.IDPlaySyncPos:
					r := protocol.NewPacketReader(p.Data)
					id, _ := r.VarInt()
					var bb protocol.Buffer
					bb.VarInt(id)
					_ = s.pw.WritePacket(server.IDPlayTeleportConfirmSB, bb.Bytes())
				case server.IDPlaySystemChatCB:
					if s.a {
						text.WriteString(chatTextBlackbox(t, p.Data))
					}
				}
			}
		}
		if strings.Contains(text.String(), "commands.data.entity.query") {
			break
		}
	}
	ms := dataNumRe.FindAllStringSubmatch(text.String(), -1)
	if len(ms) == 0 {
		return 0, false
	}
	f, err := strconv.ParseFloat(ms[len(ms)-1][1], 64)
	return f, err == nil
}

var dataNumLooseRe = regexp.MustCompile(`(-?\d+(?:\.\d+)?)[bBsSiIlLfFdD]?`)

// dataGetNum reads the last tagged-or-plain number in a data-get
// reply — bytes (0b), shorts (0s) and ints (-1) all count, unlike
// dataGetFloat's d/f-only parse.
func dataGetNum(t *testing.T, v *vehPair, query string) (float64, bool) {
	t.Helper()
	r := dataGetReply(t, v, query)
	ms := dataNumLooseRe.FindAllStringSubmatch(r, -1)
	if len(ms) == 0 {
		return 0, false
	}
	f, err := strconv.ParseFloat(ms[len(ms)-1][1], 64)
	return f, err == nil
}

// dataGetReply is dataGetFloat without the number extraction — for
// presence assertions on keys whose value isn't numeric (Items[],
// Command "", SpawnPotentials). An absent key replies
// arguments.nbtpath.nothing_found.
func dataGetReply(t *testing.T, v *vehPair, query string) string {
	t.Helper()
	var b protocol.Buffer
	b.String(query)
	if err := v.pwa.WritePacket(server.IDPlayChatCommandSB, b.Bytes()); err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, s := range []struct {
			c  net.Conn
			pr *protocol.Reader
			pw *protocol.Writer
			a  bool
		}{{v.ca, v.pra, v.pwa, true}, {v.cb, v.prb, v.pwb, false}} {
			_ = s.c.SetReadDeadline(time.Now().Add(30 * time.Millisecond))
			for {
				p, err := s.pr.ReadPacket()
				if err != nil {
					break
				}
				switch int32(p.ID) {
				case server.IDPlayKeepAliveCB:
					_ = s.pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
				case server.IDPlayChunkBatchDone:
					_ = ackChunkBatch(s.pw)
				case server.IDPlaySyncPos:
					r := protocol.NewPacketReader(p.Data)
					id, _ := r.VarInt()
					var bb protocol.Buffer
					bb.VarInt(id)
					_ = s.pw.WritePacket(server.IDPlayTeleportConfirmSB, bb.Bytes())
				case server.IDPlaySystemChatCB:
					if s.a {
						text.WriteString(chatTextBlackbox(t, p.Data))
					}
				}
			}
		}
		if strings.Contains(text.String(), "commands.data.entity.query") || strings.Contains(text.String(), "nothing_found") {
			break
		}
	}
	return text.String()
}

// TestMinecartPushPickupParity covers pushAndPickupEntities and its
// neighbors on flat ground: a rideable moving cart scoops a pushable
// mob into its seat (startRiding) and welds it per-tick; a ridden
// cart or a cart-vs-cart overlap instead pushes; and a ramming cart
// sheds momentum into the parked one.
func TestMinecartPushPickupParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB"),
	}, nil)
	defer targets.cleanup()
	run := func(t *testing.T, tag, addr string) {
		v := joinVehPair(t, addr)
		drainChunkWindow(t, tag, v.ca, v.pra, v.pwa, 300*time.Millisecond)
		drainChunkWindow(t, tag, v.cb, v.prb, v.pwb, 300*time.Millisecond)
		sendCommand(t, v.pwa, "tp @s 6 -60 6 0 0")
		sendCommand(t, v.pwb, "tp @s 6 -60 16 0 0")
		sendCommand(t, v.pwa, "kill @e[type=minecart]")
		sendCommand(t, v.pwa, "kill @e[type=pig]")
		sendCommand(t, v.pwb, "kill @e[type=minecart]")
		sendCommand(t, v.pwb, "kill @e[type=pig]")
		time.Sleep(400 * time.Millisecond)

		// Leg A: a moving plain cart scoops the pig in its path and
		// welds it to the seat each tick.
		sendCommand(t, v.pwb, "summon minecraft:pig 9.0 -60 10 {Tags:[\"pa\"],NoAI:1b}")
		sendCommand(t, v.pwb, "summon minecraft:minecart 8 -60 10 {Motion:[0.5,0.0,0.0],Tags:[\"ca\"]}")
		// Leg B: two overlapped stationary carts repel apart.
		sendCommand(t, v.pwa, "summon minecraft:minecart 8 -60 8 {Tags:[\"cb1\"]}")
		sendCommand(t, v.pwa, "summon minecraft:minecart 9.0 -60 8 {Tags:[\"cb2\"]}")
		// Leg C: a ridden cart pushes the second pig aside instead of
		// scooping it.
		sendCommand(t, v.pwb, "summon minecraft:pig 9.0 -60 14 {Tags:[\"pc\"],NoAI:1b}")
		sendCommand(t, v.pwb, "summon minecraft:minecart 8 -60 14 {Motion:[0.5,0.0,0.0],Passengers:[{id:\"minecraft:pig\",NoAI:1b}],Tags:[\"cc\"]}")
		// Leg D: a sliding cart rams a parked one and sheds momentum.
		sendCommand(t, v.pwa, "summon minecraft:minecart 10 -60 12 {Tags:[\"cd\"]}")
		sendCommand(t, v.pwa, "summon minecraft:minecart 9.4 -60 12 {Motion:[0.4,0.0,0.0],Tags:[\"ce\"]}")
		time.Sleep(3 * time.Second)

		pigX, okP := dataGetFloat(t, v, "data get entity @e[type=pig,tag=pa,limit=1] Pos[0]")
		pigY, _ := dataGetFloat(t, v, "data get entity @e[type=pig,tag=pa,limit=1] Pos[1]")
		scoopX, _ := dataGetFloat(t, v, "data get entity @e[type=minecart,tag=ca,limit=1] Pos[0]")
		if !okP {
			t.Fatalf("%s: leg A pig query failed", tag)
		}
		if math.Abs(pigY+59.8125) > 0.02 {
			t.Fatalf("%s: leg A pig not on the seat (y=%.4f)", tag, pigY)
		}
		if math.Abs(pigX-scoopX) > 0.05 {
			t.Fatalf("%s: leg A rider not welded to cart (pig=%.4f cart=%.4f)", tag, pigX, scoopX)
		}

		lo, okL := dataGetFloat(t, v, "data get entity @e[type=minecart,tag=cb1,limit=1] Pos[0]")
		hi, okH := dataGetFloat(t, v, "data get entity @e[type=minecart,tag=cb2,limit=1] Pos[0]")
		if !okL || !okH {
			t.Fatalf("%s: leg B cart queries failed", tag)
		}
		// vanilla rests at ~(8.15, 9.43); spawned at 8.5/9.0.
		if math.Abs(lo-8.15) > 0.15 || math.Abs(hi-9.43) > 0.15 || hi-lo < 1.0 {
			t.Fatalf("%s: leg B repel rests off (lo=%.4f hi=%.4f)", tag, lo, hi)
		}

		// A pushed NoAI mob never integrates the delta into its
		// position (travel never runs) — Motion[0] is where the push
		// lands on both servers.
		p2m, _ := dataGetFloat(t, v, "data get entity @e[type=pig,tag=pc,limit=1] Motion[0]")
		p2y, _ := dataGetFloat(t, v, "data get entity @e[type=pig,tag=pc,limit=1] Pos[1]")
		// The ridden cart plows through and rests right of the pig,
		// so the accumulated push is -X on both servers (it keeps
		// growing while the boxes overlap — pin the direction).
		if p2m > -0.02 {
			t.Fatalf("%s: leg C second pig not pushed -X (motion=%.4f)", tag, p2m)
		}
		if math.Abs(p2y+60.0) > 0.02 {
			t.Fatalf("%s: leg C second pig wrongly riding (y=%.4f)", tag, p2y)
		}
		riderX, _ := dataGetFloat(t, v, "execute positioned 9 -60 14 run data get entity @e[type=pig,tag=!pc,sort=nearest,limit=1] Pos[0]")
		riderY, _ := dataGetFloat(t, v, "execute positioned 9 -60 14 run data get entity @e[type=pig,tag=!pc,sort=nearest,limit=1] Pos[1]")
		riddenX, _ := dataGetFloat(t, v, "data get entity @e[type=minecart,tag=cc,limit=1] Pos[0]")
		if math.Abs(riderY+59.8125) > 0.02 || math.Abs(riderX-riddenX) > 0.05 {
			t.Fatalf("%s: leg C rider not on seat (pig=(%.4f,%.4f) cart=%.4f)", tag, riderX, riderY, riddenX)
		}

		still, okS := dataGetFloat(t, v, "data get entity @e[type=minecart,tag=cd,limit=1] Pos[0]")
		ram, okR := dataGetFloat(t, v, "data get entity @e[type=minecart,tag=ce,limit=1] Pos[0]")
		if !okS || !okR {
			t.Fatalf("%s: leg D cart queries failed", tag)
		}
		if still < 10.65 {
			t.Fatalf("%s: leg D parked cart not displaced (x=%.4f)", tag, still)
		}
		if ram >= still || ram < 9.0 {
			t.Fatalf("%s: leg D rammer rest off (ram=%.4f still=%.4f)", tag, ram, still)
		}
		t.Logf("%s: A pig/cart %.4f/%.4f B %.4f/%.4f C pig2m %.4f ridden %.4f D %.4f/%.4f",
			tag, pigX, scoopX, lo, hi, p2m, riddenX, ram, still)
	}
	run(t, "vanilla", targets.vanilla)
	run(t, "mc-go", targets.mcgo)
}

// TestMinecartRailParity runs moveAlongTrack end-to-end on both
// servers: the powered-rail boost caps at 0.4, the unpowered
// powered rail brakes to a stop, a plain rail decays at x0.96,
// a south_west curve turns the cart, and ascending_east climbs a
// block onto the upper track.
func TestMinecartRailParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB"),
	}, nil)
	defer targets.cleanup()
	for _, tag := range []string{"vanilla", "mc-go"} {
		addr := targets.vanilla
		if tag == "mc-go" {
			addr = targets.mcgo
		}
		v := joinVehPair(t, addr)
		drainChunkWindow(t, tag, v.ca, v.pra, v.pwa, 300*time.Millisecond)
		sendCommand(t, v.pwa, "tp @s 6 -60 36 0 0")
		sendCommand(t, v.pwa, "kill @e[type=minecart]")
		for x := 20; x <= 34; x++ {
			// A setblock'd powered=true flips back on the next
			// neighbor update without real redstone — a
			// redstone_block under the rail keeps it live.
			sendCommand(t, v.pwa, fmt.Sprintf("setblock %d -61 20 minecraft:redstone_block", x))
			sendCommand(t, v.pwa, fmt.Sprintf("setblock %d -60 20 minecraft:powered_rail[shape=east_west,powered=true]", x))
			sendCommand(t, v.pwa, fmt.Sprintf("setblock %d -60 22 minecraft:powered_rail[shape=east_west,powered=false]", x))
			sendCommand(t, v.pwa, fmt.Sprintf("setblock %d -60 24 minecraft:rail[shape=east_west]", x))
		}
		for x := 20; x <= 24; x++ {
			sendCommand(t, v.pwa, fmt.Sprintf("setblock %d -60 26 minecraft:rail[shape=east_west]", x))
		}
		sendCommand(t, v.pwa, "setblock 25 -60 26 minecraft:rail[shape=south_west]")
		for z := 27; z <= 34; z++ {
			sendCommand(t, v.pwa, fmt.Sprintf("setblock 25 -60 %d minecraft:rail[shape=north_south]", z))
		}
		for x := 20; x <= 22; x++ {
			sendCommand(t, v.pwa, fmt.Sprintf("setblock %d -60 28 minecraft:rail[shape=east_west]", x))
		}
		sendCommand(t, v.pwa, "setblock 23 -60 28 minecraft:rail[shape=ascending_east]")
		sendCommand(t, v.pwa, "setblock 24 -60 28 minecraft:stone")
		sendCommand(t, v.pwa, "setblock 24 -59 28 minecraft:rail[shape=east_west]")
		sendCommand(t, v.pwa, "setblock 25 -60 28 minecraft:stone")
		sendCommand(t, v.pwa, "setblock 25 -59 28 minecraft:rail[shape=east_west]")
		sendCommand(t, v.pwa, "setblock 26 -59 28 minecraft:rail[shape=east_west]")
		time.Sleep(400 * time.Millisecond)
		sendCommand(t, v.pwa, `summon minecraft:minecart 20 -60 20 {Motion:[0.05,0.0,0.0],Tags:["ta"]}`)
		sendCommand(t, v.pwa, `summon minecraft:minecart 20 -60 22 {Motion:[0.4,0.0,0.0],Tags:["tb"]}`)
		sendCommand(t, v.pwa, `summon minecraft:minecart 20 -60 24 {Motion:[0.4,0.0,0.0],Tags:["tc"]}`)
		sendCommand(t, v.pwa, `summon minecraft:minecart 20 -60 26 {Motion:[0.3,0.0,0.0],Tags:["td"]}`)
		sendCommand(t, v.pwa, `summon minecraft:minecart 20 -60 28 {Motion:[0.3,0.0,0.0],Tags:["te"]}`)
		time.Sleep(2500 * time.Millisecond)
		ax, _ := dataGetFloat(t, v, "data get entity @e[type=minecart,tag=ta,limit=1] Pos[0]")
		bx, _ := dataGetFloat(t, v, "data get entity @e[type=minecart,tag=tb,limit=1] Pos[0]")
		bm, _ := dataGetFloat(t, v, "data get entity @e[type=minecart,tag=tb,limit=1] Motion[0]")
		cx, _ := dataGetFloat(t, v, "data get entity @e[type=minecart,tag=tc,limit=1] Pos[0]")
		dx, _ := dataGetFloat(t, v, "data get entity @e[type=minecart,tag=td,limit=1] Pos[0]")
		dz, _ := dataGetFloat(t, v, "data get entity @e[type=minecart,tag=td,limit=1] Pos[2]")
		ex, _ := dataGetFloat(t, v, "data get entity @e[type=minecart,tag=te,limit=1] Pos[0]")
		ey, _ := dataGetFloat(t, v, "data get entity @e[type=minecart,tag=te,limit=1] Pos[1]")
		// A: +0.06/tick boost to the 0.4 cap — exits the track's end
		// at 34.5 and coasts on.
		if ax < 33 {
			t.Fatalf("%s: leg A powered cart short (x=%.4f)", tag, ax)
		}
		// B: unpowered powered_rail brakes x0.5/tick then zeroes at
		// <0.03 — the cart stops almost immediately.
		if bx < 20.4 || bx > 22.5 {
			t.Fatalf("%s: leg B braked cart at %.4f", tag, bx)
		}
		if math.Abs(bm) > 0.02 {
			t.Fatalf("%s: leg B cart still moving (%.4f)", tag, bm)
		}
		// C: plain rail decays x0.96 — asymptote ~10 blocks in.
		if cx < 29 || cx > 31.5 {
			t.Fatalf("%s: leg C plain-rail rest at %.4f", tag, cx)
		}
		// D: the south_west corner turns the cart south onto the
		// north_south run.
		if dz < 27.5 || dx < 24.9 || dx > 26.1 {
			t.Fatalf("%s: leg D curve at (%.4f, %.4f)", tag, dx, dz)
		}
		// E: ascending_east lifts the cart a full block onto the top
		// rail (surface -59+0.0625 = -58.9375).
		if ex < 23.4 || ey > -58.6 || ey < -59.3 {
			t.Fatalf("%s: leg E climb at (%.4f, %.4f)", tag, ex, ey)
		}
		t.Logf("%s: A=%.3f B=(%.3f,m%.3f) C=%.3f D=(%.3f,%.3f) E=(%.3f,%.3f)",
			tag, ax, bx, bm, cx, dx, dz, ex, ey)
		v.ca.Close()
		v.cb.Close()
	}
}

// cartEID waits for the watcher's minecart add_entity, servicing both
// sockets like boatEID.
func cartEID(t *testing.T, v *vehPair, window time.Duration) int32 {
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
					if n, _ := server.EntityTypeName(ty); n == "minecart" {
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
		t.Fatalf("no minecart spawn seen within %s", window)
	}
	return found
}

// TestMinecartRiderKickParity pins the two rider-driven rail
// behaviors: a powered activator_rail ejects its passenger
// (activateMinecart), and a player rider's forward input kicks a
// near-stopped cart +0.001/tick while bypassing the unpowered
// powered_rail brake (getLastClientMoveIntent).
func TestMinecartRiderKickParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB"),
	}, nil)
	defer targets.cleanup()
	for _, tag := range []string{"vanilla", "mc-go"} {
		addr := targets.vanilla
		if tag == "mc-go" {
			addr = targets.mcgo
		}
		v := joinVehPair(t, addr)
		drainChunkWindow(t, tag, v.ca, v.pra, v.pwa, 300*time.Millisecond)
		drainChunkWindow(t, tag, v.cb, v.prb, v.pwb, 300*time.Millisecond)
		sendCommand(t, v.pwa, "tp @s 21 -60 30.5 -90 0")
		sendCommand(t, v.pwb, "tp @s 21.5 -60 33.2 -90 0")
		sendCommand(t, v.pwa, "kill @e[type=minecart]")
		// Activator eject: powered activator_rail on a redstone block.
		sendCommand(t, v.pwa, "setblock 20 -61 32 minecraft:redstone_block")
		sendCommand(t, v.pwa, "setblock 20 -60 32 minecraft:activator_rail[shape=east_west,powered=true]")
		for x := 21; x <= 22; x++ {
			sendCommand(t, v.pwa, fmt.Sprintf("setblock %d -60 32 minecraft:rail[shape=east_west]", x))
		}
		// Rider kick: a dead powered_rail run that normally brakes a
		// cart dead — the intent kick skips that brake.
		for x := 20; x <= 25; x++ {
			sendCommand(t, v.pwa, fmt.Sprintf("setblock %d -60 34 minecraft:powered_rail[shape=east_west,powered=false]", x))
		}
		for x := 26; x <= 34; x++ {
			sendCommand(t, v.pwa, fmt.Sprintf("setblock %d -60 34 minecraft:rail[shape=east_west]", x))
		}
		time.Sleep(400 * time.Millisecond)

		// Leg A: mount the activator cart via right-click interact —
		// the powered rail ejects within a tick and stamps
		// DATA_ID_DAMAGE=50 (activateMinecart is isVehicle-gated, so
		// the ~50-decaying damage metadata + cleared RootVehicle
		// prove mount AND eject).
		sendCommand(t, v.pwa, `summon minecraft:minecart 20.5 -60 32 {Tags:["ac"]}`)
		acEID := cartEID(t, v, 3*time.Second)
		sendInteractEntity(v.pwa, acEID)
		o := vehWatch(t, v, true, acEID, 2*time.Second)
		if o.floatMax[10] < 45 {
			t.Fatalf("%s: activator never stamped damage (meta %v)", tag, o.floatMax)
		}
		// The eject clears the rider's RootVehicle — player passengers
		// never appear in the vehicle's Passengers NBT, so the mount
		// state is read off the rider instead.
		if _, riding := dataGetFloat(t, v, "data get entity @e[type=player,name=BOATA,limit=1] RootVehicle"); riding {
			t.Fatalf("%s: activator rail did not eject the rider", tag)
		}

		// Leg B: mount the dead-powered-rail cart and hold forward —
		// the intent kick launches it past the brake run. Player B
		// rides (A's 60-tick boardingCooldown from the leg-A eject
		// would block a fresh mount for 3s).
		sendCommand(t, v.pwa, `summon minecraft:minecart 21.5 -60 34 {Tags:["bc"]}`)
		bcEID := cartEID(t, v, 3*time.Second)
		sendInteractEntity(v.pwb, bcEID)
		time.Sleep(400 * time.Millisecond)
		if _, ok := dataGetFloat(t, v, "data get entity @e[type=player,name=BOATB,limit=1] RootVehicle"); !ok {
			t.Fatalf("%s: interact never mounted cart bc", tag)
		}
		sendPlayerInput(v.pwb, 0x01) // forward, rider yaw -90 -> +X
		time.Sleep(3500 * time.Millisecond)
		sendPlayerInput(v.pwb, 0x00)
		bx, _ := dataGetFloat(t, v, "data get entity @e[type=minecart,tag=bc,limit=1] Pos[0]")
		// +0.001/tick kick on the 0.96 slowdown — a multi-block creep
		// vs dead stop without input.
		if bx < 21.8 {
			t.Fatalf("%s: rider input never moved the cart (x=%.4f)", tag, bx)
		}
		t.Logf("%s: activator eject ok; rider-kick cart at %.4f", tag, bx)
		v.ca.Close()
		v.cb.Close()
	}
}

// TestMinecartVariantNBTParity pins every minecart variant's
// saveWithoutId shape — the shared cart fields plus each subtype's
// addAdditionalSaveData keys — and that carts carry no living fields
// (Health, NoAI, ...) at all.
func TestMinecartVariantNBTParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB"),
	}, nil)
	defer targets.cleanup()
	for _, tag := range []string{"vanilla", "mc-go"} {
		addr := targets.vanilla
		if tag == "mc-go" {
			addr = targets.mcgo
		}
		v := joinVehPair(t, addr)
		drainChunkWindow(t, tag, v.ca, v.pra, v.pwa, 300*time.Millisecond)
		drainChunkWindow(t, tag, v.cb, v.prb, v.pwb, 300*time.Millisecond)
		sendCommand(t, v.pwa, "tp @s 20 -60 40")
		z := 40
		summon := func(ty string) {
			sendCommand(t, v.pwa, "summon minecraft:"+ty+" 20.5 -60 "+itoa(int32(z)))
			z += 2
		}
		sel := func(ty string) string { return "@e[type=" + ty + ",limit=1,sort=nearest]" }
		summon("minecart")
		summon("chest_minecart")
		summon("furnace_minecart")
		summon("hopper_minecart")
		summon("tnt_minecart")
		summon("command_block_minecart")
		summon("spawner_minecart")
		summon("oak_boat")
		time.Sleep(500 * time.Millisecond)

		// Shared cart fields — every variant carries them.
		for _, vn := range []string{"minecart", "chest_minecart", "furnace_minecart", "hopper_minecart", "tnt_minecart", "command_block_minecart", "spawner_minecart"} {
			for _, key := range []string{"FlippedRotation", "HasTicked"} {
				if val, ok := dataGetNum(t, v, "data get entity "+sel(vn)+" "+key); !ok || val != 0 {
					t.Fatalf("%s: %s %s = %v ok=%v, want 0", tag, vn, key, val, ok)
				}
			}
			// No living fields on carts.
			if val, ok := dataGetNum(t, v, "data get entity "+sel(vn)+" Health"); ok {
				t.Fatalf("%s: %s leaked living field Health=%v", tag, vn, val)
			}
		}
		// Variant keys.
		checks := []struct {
			vn, key string
			want    float64
		}{
			{"furnace_minecart", "Fuel", 0},
			{"furnace_minecart", "PushX", 0},
			{"furnace_minecart", "PushZ", 0},
			{"hopper_minecart", "Enabled", 1},
			{"tnt_minecart", "fuse", -1},
			{"command_block_minecart", "SuccessCount", 0},
			{"command_block_minecart", "UpdateLastExecution", 1},
			{"command_block_minecart", "TrackOutput", 1},
			{"spawner_minecart", "MinSpawnDelay", 200},
			{"spawner_minecart", "MaxSpawnDelay", 800},
			{"spawner_minecart", "SpawnCount", 4},
			{"spawner_minecart", "MaxNearbyEntities", 6},
			{"spawner_minecart", "RequiredPlayerRange", 16},
			{"spawner_minecart", "SpawnRange", 4},
		}
		for _, c := range checks {
			if val, ok := dataGetNum(t, v, "data get entity "+sel(c.vn)+" "+c.key); !ok || val != c.want {
				t.Fatalf("%s: %s %s = %v ok=%v, want %v", tag, c.vn, c.key, val, ok, c.want)
			}
		}
		// Delay counts down on a live spawner — vanilla randomizes
		// it into [MinSpawnDelay, MaxSpawnDelay] on the first tick.
		// mc-go's static extras value sits inside that window; the
		// countdown semantics themselves stay a spawner-behavior gap.
		if val, ok := dataGetNum(t, v, "data get entity "+sel("spawner_minecart")+" Delay"); !ok || val < 0 || val > 800 {
			t.Fatalf("%s: spawner Delay = %v ok=%v, want 0..800", tag, val, ok)
		}
		// Presence-only keys (non-numeric values).
		for _, q := range []struct{ vn, key string }{
			{"chest_minecart", "Items"},
			{"hopper_minecart", "Items"},
			{"command_block_minecart", "Command"},
			{"spawner_minecart", "SpawnPotentials"},
		} {
			if r := dataGetReply(t, v, "data get entity "+sel(q.vn)+" "+q.key); strings.Contains(r, "nothing_found") {
				t.Fatalf("%s: %s missing %s (%q)", tag, q.vn, q.key, r)
			}
		}
		// Boats are non-living too — no living keys on the save.
		if val, ok := dataGetNum(t, v, "data get entity "+sel("oak_boat")+" Health"); ok {
			t.Fatalf("%s: boat leaked living field Health=%v", tag, val)
		}
		t.Logf("%s: variant NBT shapes ok", tag)
		v.ca.Close()
		v.cb.Close()
	}
}
