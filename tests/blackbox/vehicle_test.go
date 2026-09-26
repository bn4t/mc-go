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

// vehWatch collects packets matching (eid, kind) over the window on
// the chosen connection, while servicing BOTH sockets — the other
// conn still needs keepalives, chunk acks and teleport confirms
// answered or vanilla gates the driver's next move_vehicle behind
// updateAwaitingTeleport. boolMeta captures boolean metadata entries
// (serializer 8) seen for eid in set_entity_data dirty packs.
func vehWatch(t *testing.T, v *vehPair, watchB bool, eid int32, window time.Duration) (moved bool, moveVeh bool, passengers int, boolMeta map[int]bool) {
	t.Helper()
	passengers = -1
	boolMeta = map[int]bool{}
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
				switch int32(p.ID) {
				case 0x6B: // set_passengers
					r := protocol.NewPacketReader(p.Data)
					veid, _ := r.VarInt()
					if veid == eid {
						n, _ := r.VarInt()
						passengers = int(n)
					}
				case 0x39: // move_vehicle rubber-band to the driver
					moveVeh = true
				case 35, 53, 54, 125: // pos sync / delta / teleport
					r := protocol.NewPacketReader(p.Data)
					peid, _ := r.VarInt()
					if peid == eid {
						moved = true
					}
				case server.IDPlayEntityMetadata:
					r := protocol.NewPacketReader(p.Data)
					peid, _ := r.VarInt()
					if peid != eid {
						continue
					}
					// Entries are {index, serializer, value}; walk
					// only the types needed (bool=8) — anything else
					// ends the scan, the boat pack is tiny.
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
						case 8: // boolean
							vv, err := r.Bool()
							if err != nil {
								break
							}
							boolMeta[int(idx)] = vv
						case 0:
							_, _ = r.Byte()
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
	return moved, moveVeh, passengers, boolMeta
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
		_, _, passengers, _ := vehWatch(t, v, true, eid, 2*time.Second)
		if passengers != 1 {
			t.Fatalf("%s: interact mounted %d passengers, want 1", tag, passengers)
		}

		// A sane move (3 blocks) applies — watchers see the vehicle go.
		sendMoveVehicle(v.pwa, 11, -60, 8)
		moved, _, _, _ := vehWatch(t, v, true, eid, 2*time.Second)
		if !moved {
			t.Fatalf("%s: in-range move_vehicle never reached the watcher", tag)
		}

		// A 12-block jump overshoots the 100 dist² gate: the driver
		// gets a move_vehicle rubber-band, the watcher sees nothing.
		sendMoveVehicle(v.pwa, 23, -60, 8)
		_, band, _, _ := vehWatch(t, v, false, eid, 2*time.Second)
		if !band {
			t.Fatalf("%s: oversized move_vehicle drew no rubber-band", tag)
		}

		// Paddle input: the driver raises DATA_ID_PADDLE_LEFT on the
		// boat — watchers see a set_entity_data dirty pack.
		sendPaddleBoat(v.pwa, true, true)
		_, _, _, meta := vehWatch(t, v, true, eid, 2*time.Second)
		if !meta[11] || !meta[12] {
			t.Fatalf("%s: paddle input produced metadata %v, want indices 11/12 true", tag, meta)
		}
		sendPaddleBoat(v.pwa, false, false)
		_, _, _, meta = vehWatch(t, v, true, eid, 2*time.Second)
		if v, ok := meta[11]; !ok || v {
			t.Fatalf("%s: paddle release metadata %v, want index 11 false", tag, meta)
		}

		// Sneak dismounts: passenger count drops to zero.
		sendPlayerInput(v.pwa, 0x20)
		_, _, passengers, _ = vehWatch(t, v, true, eid, 2*time.Second)
		if passengers != 0 {
			t.Fatalf("%s: sneak dismount left %d passengers", tag, passengers)
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
