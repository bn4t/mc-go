package blackbox

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestInteractMountOwnStreamParity pins ServerPlayer.startRiding's
// own-connection answer for an INTERACT mount (vanilla
// ServerPlayer.startRiding, javap offsets 10-85): positionRider welds
// the seat, then the rider's own connection receives the
// rotation-masked player_position carrying the welded seat
// (PositionMoveRotation(position, ZERO, 0f, 0f), Set.of(ROTATION)), then
// — when the vehicle is a living entity — PlayerList.sendActiveEffects
// ships one update_mob_effect per live vehicle effect
// (ClientboundUpdateMobEffectPacket(vehicle.getId(), instance, false),
// blend never set), then the direct set_passengers. The tracker pass
// does not rebroadcast set_passengers to the rider:
// ServerEntity.sendChanges' passenger filter skips viewers whose own
// membership changed, so the direct send is the rider's only copy.
//
// The answer's ARRIVAL is not tick-atomic with the interact: measured
// live, the sync/effect/set_passengers answer lands either within the
// same tick batch as the processed interact or slips one batch behind
// it (a /ride @s dismount state probe proves the mount always took
// effect either way — the drop theory was a harness artifact of
// closing the window at the /say receipt). The window below therefore
// keeps collecting past the say marker until the answer shape is
// complete instead of closing at the marker.
//
// Leg 1 interact-mounts a grounded oak_boat (a non-living vehicle: no
// effect arm; the seat is coarsely pinned because the boat settles and
// drifts after its summon — exact boat rest geometry is its own
// parity question). Leg 2 interact-mounts a tamed adult horse carrying
// an infinite Speed II effect (a living vehicle: the effect arm must
// answer, exact seat +0.84375 above the horse). Leg 3 mounts the same
// horse through the /ride mount command: the command route runs the
// same ServerPlayer.startRiding answer.

func TestInteractMountOwnStreamParity(t *testing.T) {
	targets := requireBlackboxFiles(t,
		map[string]string{"command-spam-threshold-seconds": "0"},
		map[string]string{"ops.json": opsJSONFor("MI_Rider")},
		nil)
	defer targets.cleanup()

	// miSink drains the rider's own stream, records every small packet
	// body, and keeps the session alive without echoing movement — the
	// rider stays exactly where each mount welded it.
	type miSink struct {
		mu    sync.Mutex
		lines []string
		err   error
	}
	newSink := func(pr *protocol.Reader, pw *protocol.Writer, writes *sync.Mutex) *miSink {
		s := &miSink{}
		go func() {
			for {
				p, err := pr.ReadPacket()
				if err != nil {
					s.mu.Lock()
					s.err = err
					s.mu.Unlock()
					return
				}
				s.mu.Lock()
				if len(p.Data) < 1<<20 {
					s.lines = append(s.lines, fmt.Sprintf("%#x:%s", p.ID, string(p.Data)))
				}
				s.mu.Unlock()
				switch p.ID {
				case server.IDPlayKeepAliveCB:
					writes.Lock()
					err = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
					writes.Unlock()
				case server.IDPlayChunkBatchDone:
					writes.Lock()
					err = ackChunkBatch(pw)
					writes.Unlock()
				case server.IDPlaySyncPos:
					id, derr := protocol.NewPacketReader(p.Data).VarInt()
					if derr == nil {
						var b protocol.Buffer
						b.VarInt(id)
						writes.Lock()
						err = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
						writes.Unlock()
					}
				}
				if err != nil {
					s.mu.Lock()
					s.err = err
					s.mu.Unlock()
					return
				}
			}
		}()
		return s
	}
	take := func(s *miSink) []string {
		s.mu.Lock()
		defer s.mu.Unlock()
		out := s.lines
		s.lines = nil
		return out
	}
	waitSay := func(s *miSink, marker string) []string {
		deadline := time.Now().Add(30 * time.Second)
		var lines []string
		for time.Now().Before(deadline) {
			lines = append(lines, take(s)...)
			for _, l := range lines {
				if strings.Contains(l, marker) {
					return lines
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("say marker %q never arrived", marker)
		return lines
	}

	// syncRec is one own-stream position sync decoded for assertions:
	// pos, velocity, rotations and the Relative flags byte.
	type syncRec struct {
		pos        [3]float64
		vel        [3]float64
		yaw, pitch float32
		flags      int32
		line       string
	}
	type mountWindow struct {
		sync   []syncRec
		effect []string
		pass   []string
	}

	decode := func(lines []string, veh int32, rider int32, ref [3]float64) mountWindow {
		var w mountWindow
		for _, l := range lines {
			idStr, raw, ok := strings.Cut(l, ":")
			if !ok {
				continue
			}
			id, err := strconv.ParseInt(idStr, 0, 32)
			if err != nil {
				continue
			}
			body := []byte(raw)
			switch int32(id) {
			case server.IDPlaySyncPos:
				r := protocol.NewPacketReader(body)
				_, _ = r.VarInt() // teleport id: run-dependent
				var rec syncRec
				for i := range rec.pos {
					rec.pos[i], _ = r.Double()
				}
				for i := range rec.vel {
					rec.vel[i], _ = r.Double()
				}
				rec.yaw, _ = r.Float()
				rec.pitch, _ = r.Float()
				flags, ferr := r.Int()
				if ferr != nil {
					continue
				}
				rec.flags = flags
				rec.line = fmt.Sprintf("pos %.4f %.4f %.4f (+%.4f +%.4f +%.4f) vel %.2f %.2f %.2f rot %.2f %.2f flags %#04x",
					rec.pos[0], rec.pos[1], rec.pos[2], rec.pos[0]-ref[0], rec.pos[1]-ref[1], rec.pos[2]-ref[2],
					rec.vel[0], rec.vel[1], rec.vel[2], rec.yaw, rec.pitch, rec.flags)
				w.sync = append(w.sync, rec)
			case server.IDPlayUpdateMobEffect:
				r := protocol.NewPacketReader(body)
				eid, _ := r.VarInt()
				effect, _ := r.VarInt()
				amp, _ := r.VarInt()
				dur, _ := r.VarInt()
				flags, ferr := r.Byte()
				if ferr != nil {
					continue
				}
				who := "veh"
				if eid == rider {
					who = "rider"
				}
				w.effect = append(w.effect, fmt.Sprintf("%s effect %d amp %d dur %d flags %#04x", who, effect, amp, dur, flags))
			case server.LatestVersion().IDs.PlaySetPassengers:
				r := protocol.NewPacketReader(body)
				eid, _ := r.VarInt()
				n, _ := r.VarInt()
				var riders []string
				for i := int32(0); i < n; i++ {
					pid, perr := r.VarInt()
					if perr != nil {
						riders = append(riders, "?")
						continue
					}
					if pid == rider {
						riders = append(riders, "rider")
					} else if pid == veh {
						riders = append(riders, "veh")
					} else {
						riders = append(riders, strconv.FormatInt(int64(pid), 10))
					}
				}
				who := "veh"
				if eid == rider {
					who = "rider"
				}
				w.pass = append(w.pass, fmt.Sprintf("%s [%s]", who, strings.Join(riders, ",")))
			}
		}
		return w
	}

	// waitMountAnswer collects the rider's own stream from the interact
	// or mount command until the answer is complete: the say receipt
	// proves the action was processed, and the sync plus the
	// set_passengers add prove the mount answer arrived — whether
	// inline with the receipt or a tick behind it. Effect packets sit
	// between the sync and the set_passengers on the wire, so they are
	// already collected by the time the passengers add lands.
	waitMountAnswer := func(s *miSink, marker string, veh, rider int32, wantEffect bool) []string {
		deadline := time.Now().Add(5 * time.Second)
		var lines []string
		sawSay := false
		for time.Now().Before(deadline) {
			lines = append(lines, take(s)...)
			for _, l := range lines {
				if !sawSay && strings.Contains(l, marker) {
					sawSay = true
				}
			}
			if sawSay {
				w := decode(lines, veh, rider, [3]float64{})
				if len(w.sync) >= 1 && len(w.pass) >= 1 && (!wantEffect || len(w.effect) >= 1) {
					return lines
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		return lines
	}

	// syncLines flattens the decoded sync records for log lines.
	syncLines := func(rs []syncRec) []string {
		out := make([]string, len(rs))
		for i, r := range rs {
			out[i] = r.line
		}
		return out
	}

	run := func(addr string) map[string]mountWindow {
		out := map[string]mountWindow{}
		c, pr, pw, rider := joinPlayEid(t, addr, "MI_Rider")
		defer c.Close()
		awaitPlayReadyBlackbox(t, addr, c, pr, pw)
		var writes sync.Mutex
		sink := newSink(pr, pw, &writes)

		seq := 0
		cmd := func(s string) []string {
			seq++
			marker := fmt.Sprintf("MI_RECEIPT_%d", seq)
			writes.Lock()
			sendCommand(t, pw, s)
			sendCommand(t, pw, "say "+marker)
			writes.Unlock()
			return waitSay(sink, marker)
		}
		sendInteract := func(eid int32) {
			var ib protocol.Buffer
			ib.VarInt(eid)
			ib.VarInt(0) // main hand
			ib.Byte(0)   // zero LpVec3
			ib.Byte(0)   // not sneaking
			writes.Lock()
			if err := pw.WritePacket(server.IDPlayInteractSB, ib.Bytes()); err != nil {
				t.Fatal(err)
			}
			writes.Unlock()
		}
		spawnPosOf := func(lines []string, typeName string) (int32, [3]float64) {
			want, _ := server.EntityTypeID(typeName)
			prefix := fmt.Sprintf("%#x:", server.IDPlaySpawnEntity)
			for _, l := range lines {
				if !strings.HasPrefix(l, prefix) {
					continue
				}
				r := protocol.NewPacketReader([]byte(l[len(prefix):]))
				eid, e1 := r.VarInt()
				_, e2 := r.UUID()
				typ, e3 := r.VarInt()
				if e1 != nil || e2 != nil || e3 != nil || typ != want {
					continue
				}
				x, e4 := r.Double()
				y, e5 := r.Double()
				z, e6 := r.Double()
				if e4 != nil || e5 != nil || e6 != nil {
					continue
				}
				return eid, [3]float64{x, y, z}
			}
			return 0, [3]float64{}
		}

		const boatSpot = "2.5 -60 0.5"
		const horseSpot = "10.5 -60 0.5"

		// Leg 1: interact-mount a grounded oak_boat.
		lines := cmd("tp @s 0.5 -60 0.5")
		lines = cmd("summon minecraft:oak_boat " + boatSpot)
		boatEid, boatPos := spawnPosOf(lines, "oak_boat")
		if boatEid == 0 {
			t.Fatalf("%s: boat spawn not captured", addr)
		}
		time.Sleep(2 * time.Second) // let the summon settle onGround
		take(sink)
		sendInteract(boatEid)
		marker := "MI_BOAT_MOUNT"
		writes.Lock()
		sendCommand(t, pw, "say "+marker)
		writes.Unlock()
		bwin := waitMountAnswer(sink, marker, boatEid, rider, false)
		for _, l := range bwin {
			t.Logf("%s boat-raw: %s", addr, l)
		}
		out["boat"] = decode(bwin, boatEid, rider, boatPos)

		// Leave the boat and settle the dismount's own-stream answer
		// before the horse leg opens its window.
		cmd("ride @s dismount")
		lines = cmd("tp @s 8.5 -60 0.5")
		lines = cmd("summon minecraft:horse " + horseSpot + " {NoAI:1b,Tame:1b}")
		horseEid, horsePos := spawnPosOf(lines, "horse")
		if horseEid == 0 {
			t.Fatalf("%s: horse spawn not captured", addr)
		}
		lines = cmd("effect give @e[type=minecraft:horse,limit=1] minecraft:speed infinite 1")
		time.Sleep(500 * time.Millisecond)
		take(sink)

		// Leg 2: interact-mount the horse carrying the live effect.
		sendInteract(horseEid)
		marker = "MI_HORSE_MOUNT"
		writes.Lock()
		sendCommand(t, pw, "say "+marker)
		writes.Unlock()
		hwin := waitMountAnswer(sink, marker, horseEid, rider, true)
		for _, l := range hwin {
			t.Logf("%s horse-raw: %s", addr, l)
		}
		out["horse"] = decode(hwin, horseEid, rider, horsePos)

		// Leg 3: the same horse through the /ride mount command —
		// the command route answers with the same startRiding arm.
		cmd("ride @s dismount")
		time.Sleep(500 * time.Millisecond)
		take(sink)
		marker = "MI_CMD_MOUNT"
		writes.Lock()
		sendCommand(t, pw, "ride @s mount @e[type=minecraft:horse,limit=1]")
		sendCommand(t, pw, "say "+marker)
		writes.Unlock()
		cwin := waitMountAnswer(sink, marker, horseEid, rider, true)
		for _, l := range cwin {
			t.Logf("%s cmdmount-raw: %s", addr, l)
		}
		out["cmdmount"] = decode(cwin, horseEid, rider, horsePos)

		for k, w := range out {
			t.Logf("%s %s: sync=%v eff=%v pass=%v", addr, k, syncLines(w.sync), w.effect, w.pass)
		}
		return out
	}

	v := run(targets.vanilla)
	m := run(targets.mcgo)

	for _, c := range []struct {
		name        string
		v, m        mountWindow
		wantEffects int
		exactPos    bool
	}{
		{"boat", v["boat"], m["boat"], 0, false},
		{"horse", v["horse"], m["horse"], 1, true},
		{"cmdmount", v["cmdmount"], m["cmdmount"], 1, true},
	} {
		if len(c.v.sync) != 1 || len(c.m.sync) != 1 {
			t.Errorf("%s: own-stream position syncs vanilla=%d mc-go=%d (want exactly 1): vanilla %v / mc-go %v",
				c.name, len(c.v.sync), len(c.m.sync), syncLines(c.v.sync), syncLines(c.m.sync))
			continue
		}
		vs, ms := c.v.sync[0], c.m.sync[0]
		for _, s := range []struct {
			who string
			rec syncRec
		}{{"vanilla", vs}, {"mc-go", ms}} {
			if s.rec.flags != 0x18 {
				t.Errorf("%s: %s mount sync flags %#04x, want 0x18 (Set.of(ROTATION))", c.name, s.who, s.rec.flags)
			}
			for i, g := range s.rec.vel {
				if g != 0 {
					t.Errorf("%s: %s mount sync velocity axis %d = %g, want 0", c.name, s.who, i, g)
				}
			}
			if s.rec.yaw != 0 || s.rec.pitch != 0 {
				t.Errorf("%s: %s mount sync rot %.2f %.2f, want 0 0 (PositionMoveRotation ZERO)", c.name, s.who, s.rec.yaw, s.rec.pitch)
			}
		}
		if c.exactPos {
			if vs.line != ms.line {
				t.Errorf("%s: mount sync mismatch\n vanilla: %s\n mc-go:  %s", c.name, vs.line, ms.line)
			}
		} else {
			// The boat settles below its summon spot and drifts while
			// grounded (measured live), so the seat is only coarsely
			// pinned against the summon coordinates.
			for i := range vs.pos {
				if math.Abs(vs.pos[i]-ms.pos[i]) > 1.5 {
					t.Errorf("%s: mount sync axis %d vanilla %g vs mc-go %g, want |Δ| ≤ 1.5",
						c.name, i, vs.pos[i], ms.pos[i])
				}
			}
		}
		if len(c.v.effect) != c.wantEffects || len(c.m.effect) != c.wantEffects {
			t.Errorf("%s: vehicle-effect packets vanilla=%v mc-go=%v (want %d)",
				c.name, c.v.effect, c.m.effect, c.wantEffects)
		} else if c.wantEffects > 0 && c.v.effect[0] != c.m.effect[0] {
			t.Errorf("%s: vehicle effect mismatch\n vanilla: %s\n mc-go:  %s", c.name, c.v.effect[0], c.m.effect[0])
		}
		if len(c.v.pass) != 1 || len(c.m.pass) != 1 {
			t.Errorf("%s: set_passengers vanilla=%v mc-go=%v (want exactly one)", c.name, c.v.pass, c.m.pass)
		} else if c.v.pass[0] != c.m.pass[0] {
			t.Errorf("%s: set_passengers mismatch\n vanilla: %s\n mc-go:  %s", c.name, c.v.pass[0], c.m.pass[0])
		}
	}
}
