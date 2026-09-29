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
	moved      bool                   // any position/delta/teleport packet for eid
	moveVeh    bool                   // move_vehicle rubber-band to the driver
	passengers int                    // latest set_passengers count, -1 unseen
	boolMeta   map[int]bool           // serializer-8 metadata entries seen
	intMeta    map[int]int32          // serializer-1 (VarInt) entries seen
	intMetaMax map[int]int32          // largest int value seen per index
	floatMeta  map[int]float32        // serializer-3 (Float) entries seen
	floatMax   map[int]float32        // largest float value seen per index
	removed    map[int32]bool         // ids seen in remove_entities
	itemEIDs   []int32                // eids of add_entity spawns of type "item"
	disconnect bool                   // a play-stage disconnect packet arrived
	syncSeen   bool                   // a sync_pos (player teleport) arrived on either conn
	sync       [3]float64             // last sync_pos coordinates
	levelEv    [][2]int32             // level_event {event,data} pairs seen
	entEv      []byte                 // entity_event statuses seen on the watched eid
	adds       map[string]int         // add_entity counts by type name
	sounds     int                    // positioned sound (0x75) packets seen
	blockUps   map[[3]int32]int       // block_update counts by position
	motion     map[int32]int          // set_entity_motion counts by eid
	motVec     map[int32][][3]float64 // set_entity_motion velocity triples by eid
	lastPos    map[int32][3]float64   // tracked entity position base
	posNet     map[int32]float64      // net |dx|+|dz| traveled (pos packets + teleports)
	uuidMeta   map[[2]int32][16]byte  // serializer-13 present-uuid entries by {eid,index}
	hookMeta   map[int32]int32        // unscoped: eid → last nonzero idx8 int metadata
	hookHist   []string               // ordered idx8 emits as "eid:v" (spans the window)
	hookEvent  int32                  // eid that carried entity_event status 31 (0 unseen)
	bobberEIDs []int32                // add_entity eids of type fishing_bobber
	bitHist    []string               // unscoped idx9 ser-8 bool emits as "eid:v" (fishing bite)
	metaHist   []string               // every metadata packet as "eid:i1/i2/..."
	partHist   []int32                // particle_type ids from level_particles packets
	soundPitch []float64              // pitch values from named_sound packets
	soundIds   []int32                // sound ids from named_sound packets
	tongueHist []string               // unscoped (idx19, ser19) emits "eid:v" — frog tongue target
	pose9Eid   int32                  // eid that emitted (idx6, ser20) pose 9 — USING_TONGUE
	poseHist   []string               // unscoped (idx6, ser20) emits "eid:v" — DATA_POSE values
	flagsHist  []string               // unscoped (idx15, ser0) emits "eid:v" — DATA_MOB_FLAGS bytes
	beeFlHist  []string               // unscoped (idx18, ser0) emits "eid:v" — bee DATA_FLAGS bytes
	eqFlHist   []string               // unscoped (idx18, ser0) emits "eid:v" — AbstractHorse DATA_ID_FLAGS bytes
	dashHist   []string               // unscoped (idx19, ser8) emits "eid:v" — camel DATA_DASH bools
	angerHist  []string               // unscoped (idx19, ser2) emits "eid:v" — anger_end_time varlongs
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
		adds:       map[string]int{},
		blockUps:   map[[3]int32]int{},
		motion:     map[int32]int{},
		motVec:     map[int32][][3]float64{},
		lastPos:    map[int32][3]float64{},
		posNet:     map[int32]float64{},
		uuidMeta:   map[[2]int32][16]byte{},
		hookMeta:   map[int32]int32{},
	}
	poll := func(c net.Conn, pr *protocol.Reader, pw *protocol.Writer, observe bool) {
		defer c.SetReadDeadline(time.Time{})
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
				case 1: // add_entity — count spawns by type; items keep eids
					r := protocol.NewPacketReader(p.Data)
					peid, _ := r.VarInt()
					r.UUID()
					ty, _ := r.VarInt()
					n, _ := server.EntityTypeName(ty)
					o.adds[n]++
					if n == "item" {
						o.itemEIDs = append(o.itemEIDs, peid)
					}
					if n == "fishing_bobber" {
						o.bobberEIDs = append(o.bobberEIDs, peid)
					}
				case 0x08: // block_update — count writes by position
					r := protocol.NewPacketReader(p.Data)
					x, y, z, err := r.Position()
					if err == nil {
						o.blockUps[[3]int32{x, y, z}]++
					}
				case 0x65: // set_entity_motion — count by eid + vy values
					r := protocol.NewPacketReader(p.Data)
					if meid, err := r.VarInt(); err == nil {
						o.motion[meid]++
						// LpVec3-packed velocity triple — vy above
						// ~0.3 is a hop/jump signature (airborne drift
						// rarely re-emits past that).
						vx, vy, vz, _ := r.ReadLpVec3()
						o.motVec[meid] = append(o.motVec[meid], [3]float64{vx, vy, vz})
					}
				case server.IDPlayEntityEvent: // 0x22 — Int eid + status
					r := protocol.NewPacketReader(p.Data)
					peid, _ := r.Int()
					st, _ := r.Byte()
					if peid == eid {
						o.entEv = append(o.entEv, st)
					}
					if st == 31 {
						o.hookEvent = peid
					}
				case server.IDPlayLevelEvent: // 0x2E — record event+data
					r := protocol.NewPacketReader(p.Data)
					ev, _ := r.Int()
					r.Position()
					data, _ := r.Int()
					o.levelEv = append(o.levelEv, [2]int32{ev, data})
				case 0x2F: // level_particles — particle type id
					if len(p.Data) >= 47 {
						r := protocol.NewPacketReader(p.Data[46:])
						if pid, err := r.VarInt(); err == nil {
							o.partHist = append(o.partHist, pid)
						}
					}
				case 0x75: // sound — count positioned-sound packets
					o.sounds++
					r := protocol.NewPacketReader(p.Data)
					if sid, err := r.VarInt(); err == nil {
						o.soundIds = append(o.soundIds, sid)
						_, _ = r.VarInt()  // source category
						_, _ = r.Int()     // x*8
						_, _ = r.Int()     // y*8
						_, _ = r.Int()     // z*8
						_, _ = r.Float()   // volume
						pf, _ := r.Float() // pitch
						o.soundPitch = append(o.soundPitch, float64(pf))
					}
				case 53, 54: // move_entity_pos / _pos_rot — delta shorts /4096
					r := protocol.NewPacketReader(p.Data)
					peid, _ := r.VarInt()
					if peid == eid {
						o.moved = true
					}
					dx, _ := r.Short()
					dy, _ := r.Short()
					dz, _ := r.Short()
					lp := o.lastPos[peid]
					lp[0] += float64(dx) / 4096
					lp[1] += float64(dy) / 4096
					lp[2] += float64(dz) / 4096
					o.lastPos[peid] = lp
					o.posNet[peid] += math.Hypot(float64(dx)/4096, float64(dz)/4096)
				case 125: // teleport_entity — VarInt eid + absolute doubles
					r := protocol.NewPacketReader(p.Data)
					peid, _ := r.VarInt()
					if peid == eid {
						o.moved = true
					}
					tx, _ := r.Double()
					ty, _ := r.Double()
					tz, _ := r.Double()
					lp := o.lastPos[peid]
					o.posNet[peid] += math.Hypot(tx-lp[0], tz-lp[2])
					o.lastPos[peid] = [3]float64{tx, ty, tz}
				case 35: // entity_position_sync — player-relative, mark moved only
					r := protocol.NewPacketReader(p.Data)
					peid, _ := r.VarInt()
					if peid == eid {
						o.moved = true
					}
				case server.IDPlayEntityMetadata:
					r := protocol.NewPacketReader(p.Data)
					peid, _ := r.VarInt()
					// uuidMeta records ser-13 entries for ANY eid
					// (trusted slots land on a spawned baby whose
					// eid the watcher can't know ahead); the rest
					// stay scoped to the watched entity.
					watchThis := peid == eid
					var idxs []int
					for {
						idx, err := r.Byte()
						if err != nil || idx == 0xFF {
							parts := make([]string, 0, len(idxs))
							for _, i := range idxs {
								parts = append(parts, strconv.Itoa(i))
							}
							o.metaHist = append(o.metaHist,
								strconv.Itoa(int(peid))+":"+strings.Join(parts, "/"))
							break
						}
						ser, err := r.VarInt()
						if err != nil {
							break
						}
						idxs = append(idxs, int(idx))
						switch ser {
						case 0:
							bv, _ := r.Byte()
							if int(idx) == 15 {
								o.flagsHist = append(o.flagsHist,
									strconv.Itoa(int(peid))+":"+strconv.Itoa(int(bv)))
							}
							if int(idx) == 18 {
								o.beeFlHist = append(o.beeFlHist,
									strconv.Itoa(int(peid))+":"+strconv.Itoa(int(bv)))
							}
							if int(idx) == 18 {
								o.eqFlHist = append(o.eqFlHist,
									strconv.Itoa(int(peid))+":"+strconv.Itoa(int(bv)))
							}
						case 1: // int
							vv, err := r.VarInt()
							if err != nil {
								break
							}
							if watchThis {
								o.intMeta[int(idx)] = vv
								if vv > o.intMetaMax[int(idx)] {
									o.intMetaMax[int(idx)] = vv
								}
							}
							// hookMeta is unscoped like uuidMeta: the
							// bobber's eid isn't known ahead of the
							// emit that carries DATA_HOOKED_ENTITY —
							// and the idx8 → 0 emit on unhook is a
							// fact too, so zeros are kept.
							if int(idx) == 8 {
								o.hookMeta[peid] = vv
								o.hookHist = append(o.hookHist,
									strconv.Itoa(int(peid))+":"+strconv.Itoa(int(vv)))
							}
						case 3: // float
							vv, err := r.Float()
							if err != nil {
								break
							}
							if watchThis {
								o.floatMeta[int(idx)] = vv
								if vv > o.floatMax[int(idx)] {
									o.floatMax[int(idx)] = vv
								}
							}
						case 8: // boolean
							vv, err := r.Bool()
							if err != nil {
								break
							}
							if watchThis {
								o.boolMeta[int(idx)] = vv
							}
							if int(idx) == 9 {
								v := "0"
								if vv {
									v = "1"
								}
								o.bitHist = append(o.bitHist,
									strconv.Itoa(int(peid))+":"+v)
							}
							if int(idx) == 19 {
								v := "0"
								if vv {
									v = "1"
								}
								o.dashHist = append(o.dashHist,
									strconv.Itoa(int(peid))+":"+v)
							}
						case 13: // optional_living_entity_reference — bool + uuid
							present, err := r.Bool()
							if err != nil {
								break
							}
							if present {
								var u [16]byte
								if ub, err := r.Read(16); err == nil {
									copy(u[:], ub)
								}
								o.uuidMeta[[2]int32{peid, int32(idx)}] = u
							}
						case 2: // long — VarLong-encoded
							lv, err := r.VarLong()
							if err != nil {
								break
							}
							if int(idx) == 19 {
								o.angerHist = append(o.angerHist,
									strconv.Itoa(int(peid))+":"+strconv.FormatInt(lv, 10))
							}
						case 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31:
							// optional_int / pose / holder-variant
							// serializers (cat/frog/wolf/pig/cow/
							// chicken + sound variants) — VarInt.
							vv, err := r.VarInt()
							if err != nil {
								break
							}
							if watchThis {
								o.intMeta[int(idx)] = vv
								if vv > o.intMetaMax[int(idx)] {
									o.intMetaMax[int(idx)] = vv
								}
							}
							// (19, OPTIONAL_UNSIGNED_INT) is the
							// frog's DATA_TONGUE_TARGET_ID —
							// eid+1 or 0 for empty.
							if int(idx) == 19 && ser == 19 {
								o.tongueHist = append(o.tongueHist,
									strconv.Itoa(int(peid))+":"+strconv.Itoa(int(vv)))
							}
							// (6, POSE) value 9 is USING_TONGUE.
							if int(idx) == 6 {
								o.poseHist = append(o.poseHist,
									strconv.Itoa(int(peid))+":"+strconv.Itoa(int(vv)))
								if vv == 9 {
									o.pose9Eid = peid
								}
							}
							// idx8 with serializer 19 is the
							// bobber's DATA_HOOKED_ENTITY (eid+1).
							if int(idx) == 8 {
								o.hookMeta[peid] = vv
								o.hookHist = append(o.hookHist,
									strconv.Itoa(int(peid))+":"+strconv.Itoa(int(vv)))
							}
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
	// poll leaves the last armed deadline on the sockets; expired deadlines
	// would kill a concurrent reader goroutine on its next read.
	_ = v.ca.SetReadDeadline(time.Time{})
	_ = v.cb.SetReadDeadline(time.Time{})
	return o
}

// boatEID waits for the watcher's oak_boat add_entity, servicing both
// sockets so the driver's pending teleport confirms don't stall the
// move gate.
func boatEID(t *testing.T, v *vehPair, window time.Duration) int32 {
	t.Helper()
	found := int32(0)
	poll := func(c net.Conn, pr *protocol.Reader, pw *protocol.Writer, observe bool) {
		defer c.SetReadDeadline(time.Time{})
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
					_ = s.c.SetReadDeadline(time.Time{})
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
				_ = s.c.SetReadDeadline(time.Time{})
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
				_ = s.c.SetReadDeadline(time.Time{})
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
			_ = s.c.SetReadDeadline(time.Time{})
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
			_ = s.c.SetReadDeadline(time.Time{})
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
		defer c.SetReadDeadline(time.Time{})
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

// steerableEID watches the observer's add_entity stream for a spawn of
// the named type at (x,y,z) within a tight tolerance — flat worlds
// keep ambient pigs popping in, so the summon goes in at y=-58 where
// no ambient spawn can land.
func steerableEID(t *testing.T, v *vehPair, typeName string, x, y, z float64, window time.Duration) int32 {
	t.Helper()
	found := int32(0)
	end := time.Now().Add(window)
	for time.Now().Before(end) && found == 0 {
		poll := func(c net.Conn, pr *protocol.Reader, pw *protocol.Writer, observe bool) {
			defer c.SetReadDeadline(time.Time{})
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
						sx, _ := r.Double()
						sy, _ := r.Double()
						sz, _ := r.Double()
						if n, _ := server.EntityTypeName(ty); n == typeName {
							t.Logf("%s spawn eid=%d at (%.3f,%.3f,%.3f)", typeName, eid, sx, sy, sz)
							if math.Abs(sx-x) < 0.3 && math.Abs(sy-y) < 0.05 && math.Abs(sz-z) < 0.3 {
								found = eid
								return
							}
						}
					}
				}
			}
		}
		poll(v.ca, v.pra, v.pwa, false)
		poll(v.cb, v.prb, v.pwb, true)
	}
	if found == 0 {
		t.Fatalf("no %s spawn at (%.1f,%.1f,%.1f) in %s", typeName, x, y, z, window)
	}
	return found
}

// sendMoveVehicleRot writes serverbound move_vehicle (0x22) carrying
// the vehicle's reported position and rotation — the packet a riding
// client sends each tick it simulates its mount.
func sendMoveVehicleRot(pw *protocol.Writer, x, y, z, yaw, pitch float64, grounded bool) {
	var b protocol.Buffer
	b.Double(x)
	b.Double(y)
	b.Double(z)
	b.Float(float32(yaw))
	b.Float(float32(pitch))
	b.Bool(grounded)
	_ = pw.WritePacket(server.IDPlayMoveVehicleSB, b.Bytes())
}

// TestRiddenSteerableParity pins the SERVER half of
// LivingEntity.travelRidden for the stick-steerable mobs (26.2
// bytecode). A saddled steerable whose first passenger is a player
// holding the matching *_on_a_stick has a controlling rider, which
// makes the vehicle client-authoritative: on the server, aiStep's
// ridden branch only welds the vehicle's rotation to the rider's
// (tickRidden: yRot=rider.yRot, xRot=rider.xRot·0.5) and zeroes its
// delta — the vehicle freezes mid-air, and its position advances
// only when the driver's serverbound move_vehicle packet carries it.
// Without the held stick there is no controlling passenger: no weld,
// and move_vehicle drops silently (no correction packet).
func TestRiddenSteerableParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB"),
	}, nil)
	defer targets.cleanup()

	// ride runs the full mount → weld → drive → reject sequence on
	// one server leg and returns the three measured readings:
	// welded pitch, driven z, and the z after the reject probe.
	ride := func(t *testing.T, tag, addr, mob, stick string) (weldPitch, zDriven, zRejected float64) {
		v := joinVehPair(t, addr)
		drainChunkWindow(t, tag, v.ca, v.pra, v.pwa, 400*time.Millisecond)
		drainChunkWindow(t, tag, v.cb, v.prb, v.pwb, 400*time.Millisecond)
		sendCommand(t, v.pwa, "tp BOATA 4.5 -60 4.5 0 0")
		sendCommand(t, v.pwb, "tp BOATB 4.5 -60 20.5 0 0")
		sendCommand(t, v.pwa, "kill @e[type=minecraft:"+mob+"]")
		time.Sleep(400 * time.Millisecond)
		// y=-58 spawn: above the -60 floor, where ambient pig spawns
		// can never land — the tight match in steerableEID pins our
		// mob's eid instead of an ambient one's.
		sendCommand(t, v.pwa, "summon minecraft:"+mob+" 8.5 -58 8.5 {NoAI:1b,Tags:[\"steer\"],equipment:{saddle:{id:\"minecraft:saddle\",count:1}}}")
		eid := steerableEID(t, v, mob, 8.5, -58, 8.5, 3*time.Second)
		sendInteractEntity(v.pwa, eid)
		if o := vehWatch(t, v, true, eid, 2*time.Second); o.passengers != 1 {
			t.Fatalf("%s %s: mount left passengers=%d", tag, mob, o.passengers)
		}
		sendCommand(t, v.pwa, "item replace entity BOATA weapon.mainhand with minecraft:"+stick)
		// Rider yaw -160 / pitch 20 — distinct from any spawn yaw;
		// a rotation-only move packet welds it the way a real
		// client's camera turn does.
		var rb protocol.Buffer
		rb.Float(-160)
		rb.Float(20)
		rb.Byte(0x01)
		_ = v.pwa.WritePacket(server.IDPlayMoveRotSB, rb.Bytes())
		sendCommand(t, v.pwa, "tick sprint 10")
		time.Sleep(500 * time.Millisecond)
		sel := "@e[type=minecraft:" + mob + ",tag=steer,limit=1]"
		yaw, _ := dataGetFloat(t, v, "data get entity "+sel+" Rotation[0]")
		pitch, _ := dataGetFloat(t, v, "data get entity "+sel+" Rotation[1]")
		x0, _ := dataGetFloat(t, v, "data get entity "+sel+" Pos[0]")
		y0, _ := dataGetFloat(t, v, "data get entity "+sel+" Pos[1]")
		z0, _ := dataGetFloat(t, v, "data get entity "+sel+" Pos[2]")
		t.Logf("%s %s: welded rot (%.2f,%.2f) pos (%.2f,%.2f,%.2f)", tag, mob, yaw, pitch, x0, y0, z0)
		// The frozen NoAI vehicle keeps its exact spawn pos — a
		// client-authoritative mount gets zero server travel.
		if math.Abs(x0-8.5) > 0.05 || math.Abs(y0+58) > 0.05 || math.Abs(z0-8.5) > 0.05 {
			t.Fatalf("%s %s: ridden vehicle drifted to (%.3f,%.3f,%.3f)", tag, mob, x0, y0, z0)
		}
		if math.Abs(yaw+160) > 1.0 {
			t.Fatalf("%s %s: vehicle yaw %.2f not welded to rider -160", tag, mob, yaw)
		}
		if math.Abs(pitch-10) > 1.0 {
			t.Fatalf("%s %s: vehicle pitch %.2f, want rider 20·0.5=10", tag, mob, pitch)
		}
		// The driver moves its mount only through move_vehicle:
		// report z=10.0 — the server applies the delta from the
		// last-good anchor.
		sendMoveVehicleRot(v.pwa, 8.5, -58, 10.0, -160, 10, true)
		time.Sleep(300 * time.Millisecond)
		zDriven, _ = dataGetFloat(t, v, "data get entity "+sel+" Pos[2]")
		// Now drop the stick: the controlling passenger is gone, so
		// the weld releases (a new rider yaw must not weld) and a
		// move_vehicle probe is silently rejected.
		sendCommand(t, v.pwa, "item replace entity BOATA weapon.mainhand with minecraft:dirt")
		var rb2 protocol.Buffer
		rb2.Float(-80)
		rb2.Float(0)
		rb2.Byte(0x01)
		_ = v.pwa.WritePacket(server.IDPlayMoveRotSB, rb2.Bytes())
		sendCommand(t, v.pwa, "tick sprint 5")
		time.Sleep(400 * time.Millisecond)
		yaw2, _ := dataGetFloat(t, v, "data get entity "+sel+" Rotation[0]")
		if math.Abs(yaw2+80) < 1.0 {
			t.Fatalf("%s %s: vehicle still welded after stick dropped (yaw %.2f)", tag, mob, yaw2)
		}
		sendMoveVehicleRot(v.pwa, 8.5, -58, 11.5, -160, 10, true)
		time.Sleep(300 * time.Millisecond)
		zRejected, _ = dataGetFloat(t, v, "data get entity "+sel+" Pos[2]")
		v.ca.Close()
		v.cb.Close()
		return pitch, zDriven, zRejected
	}

	for _, tc := range []struct{ mob, stick string }{
		{"pig", "carrot_on_a_stick"},
		{"strider", "warped_fungus_on_a_stick"},
	} {
		vp, zv, zrej := ride(t, "vanilla", targets.vanilla, tc.mob, tc.stick)
		mp, zm, zrem := ride(t, "mc-go", targets.mcgo, tc.mob, tc.stick)
		if math.Abs(vp-10) > 1.0 || math.Abs(mp-10) > 1.0 {
			t.Fatalf("%s: welded pitch v=%.2f m=%.2f, want ~10", tc.mob, vp, mp)
		}
		if math.Abs(zv-10.0) > 0.3 || math.Abs(zm-10.0) > 0.3 {
			t.Fatalf("%s: move_vehicle drive z v=%.3f m=%.3f, want ~10", tc.mob, zv, zm)
		}
		if math.Abs(zrej-zv) > 0.05 || math.Abs(zrem-zm) > 0.05 {
			t.Fatalf("%s: move_vehicle without stick moved v %.3f→%.3f, m %.3f→%.3f",
				tc.mob, zv, zrej, zm, zrem)
		}
	}
}
