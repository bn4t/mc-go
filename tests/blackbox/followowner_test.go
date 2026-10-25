// Blackbox parity pin for FollowOwnerGoal + TamableAnimal's owner
// teleport (decompile ai/goal/FollowOwnerGoal.java +
// TamableAnimal.{tryToTeleportToOwner,shouldTryTeleportToOwner,
// unableToMoveToOwner,teleportToAroundBlockPos}):
//
//	goal priority 6 on wolf/cat/parrot, speedModifier 1.0,
//	startDistance 10, stopDistance 2, MOVE|LOOK. canUse needs a
//	resolvable same-dimension owner and !unableToMoveToOwner —
//	orderedToSit || isPassenger || leashed || owner-spectator —
//	and dist² >= 10²; canContinueToUse keeps it while past 2²
//	(the 4..100 band is hysteresis: a parked pet stays parked).
//	At dist² >= 12² the 10-tick recalc cadence runs
//	tryToTeleportToOwner: 10 draws of xd,zd ∈ [-3,3] with one axis
//	|d|>=2 plus yd ∈ [-1,1] around the owner's block pos, landing on
//	the first walkable, leaf-free, collision-free cell — snapTo +
//	navigation.stop.
//
// Probes: (1) walk-follow at 11 blocks lands the wolf at ~2-4 of
// its owner; (2) at 22 blocks the teleport cadence lands it near
// the owner far sooner than a 22-block walk could; (3) Sitting:1b
// never follows; (4) a spectator owner unlocks nothing; (5) a
// fence-leashed tame can't follow either. Final positions come
// from /data get entity Pos rather than packet accumulation —
// teleports and walks both land on the same absolute read.
package blackbox

import (
	"fmt"
	"math"
	"net"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestFollowOwnerParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB", "BOATC"),
	}, nil)
	defer targets.cleanup()

	type obs struct {
		followDist float64
		teleDist   float64
		teleJumps  int
		sitDist    float64
		specDist   float64
		leashDist  float64
	}

	run := func(t *testing.T, name, addr string) obs {
		t.Helper()
		o := obs{followDist: -1, teleDist: -1, sitDist: -1, specDist: -1, leashDist: -1}
		v := joinVehPair(t, addr)
		defer v.ca.Close()
		defer v.cb.Close()
		cc, prc, pwc := joinPlayBlackbox(t, addr, "BOATC")
		defer cc.Close()
		drainChunkWindow(t, name, v.ca, v.pra, v.pwa, 0)
		// The command replies ride BOATC's conn: a chatSink owns that
		// socket's read loop (it would steal the add_entity packet
		// summonTrackedNBT hunts on conn A).
		sinkC := newChatSinkTP(prc, pwc)

		sendCommand(t, v.pwa, "gamemode spectator BOATC")
		// BOATA stays survival — unableToMoveToOwner refuses a
		// spectator owner outright.
		sendCommand(t, v.pwa, "gamemode survival BOATA")
		sendCommand(t, v.pwa, "gamemode spectator BOATB")
		sendCommand(t, v.pwa, "gamerule do_mob_spawning false")
		sendCommand(t, v.pwa, "gamerule randomtickspeed 0")
		clear := func() {
			sendCommand(t, v.pwa, "kill @e[type=!minecraft:player]")
		}
		clear()
		// Owner anchor — the wolf's target for every probe.
		sendCommand(t, v.pwa, "tp BOATA 0.5 -60 0.5")
		ownerA := uuidIntArray(offlineUUIDBlackbox("BOATA"))
		ownerC := uuidIntArray(offlineUUIDBlackbox("BOATC"))

		wait := func(dur time.Duration) {
			deadline := time.Now().Add(dur)
			for time.Now().Before(deadline) {
				_ = vehWatch(t, v, false, 0, 400*time.Millisecond)
			}
		}
		// teleJumps counts >=6-block single-packet moves — a walk's
		// per-packet delta never reaches that; only
		// tryToTeleportToOwner's snapTo produces it. The trail the
		// spawn hunt collects is measured against the summon coords,
		// so an absolute-position packet only reads as a jump when
		// the entity actually left them.
		teleJumps := func(eid int32, trail map[int32][][2]float64) int {
			jumps := 0
			for _, tr := range trail[eid] {
				if math.Hypot(tr[0], tr[1]) > 6 {
					jumps++
				}
			}
			return jumps
		}
		// distTo reads the wolf's absolute Pos through /data get —
		// the same reply shape on both servers.
		distTo := func(x, z float64) float64 {
			st, px := dataGetSel(t, sinkC, pwc, "@e[type=wolf,limit=1]", "Pos[0]")
			if st != "query" {
				return -1
			}
			st, pz := dataGetSel(t, sinkC, pwc, "@e[type=wolf,limit=1]", "Pos[2]")
			if st != "query" {
				return -1
			}
			return math.Hypot(px-x, pz-z)
		}

		// Phase 1 — walk-follow: 11 blocks out (>10 start radius,
		// <12 teleport radius) the wolf closes to the stop radius.
		w1 := summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa,
			"wolf", 11.5, -60, 0.5, "{Owner:"+ownerA+"}")
		if w1 == 0 {
			t.Fatalf("%s: follow summon failed", name)
		}
		wait(10 * time.Second)
		o.followDist = distTo(0.5, 0.5)
		clear()

		// Phase 2 — teleport: 22 blocks out the 10-tick recalc fires
		// tryToTeleportToOwner — far faster than any walk. The jump
		// lands inside the spawn hunt itself, so the hunt keeps a
		// posTrail for the wolf from the first packet.
		var trail map[int32][][2]float64
		w2 := summonTrackedNBTTrail(t, v.ca, v.pra, v.pwa, v.pwa,
			"wolf", 22.5, -60, 0.5, "{Owner:"+ownerA+"}", &trail, 6*time.Second)
		if w2 == 0 {
			t.Fatalf("%s: teleport summon failed", name)
		}
		o.teleJumps = teleJumps(w2, trail)
		o.teleDist = distTo(0.5, 0.5)
		clear()

		// Phase 3 — orderedToSit gate: same owner, 15 blocks, but
		// Sitting:1b → unableToMoveToOwner → never moves.
		w3 := summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa,
			"wolf", 15.5, -60, 0.5,
			"{Owner:"+ownerA+",Sitting:1b}")
		if w3 == 0 {
			t.Fatalf("%s: sit summon failed", name)
		}
		wait(5 * time.Second)
		o.sitDist = distTo(0.5, 0.5)
		clear()

		// Phase 4 — spectator owner: unableToMoveToOwner sees the
		// owner's gamemode, not its position. The untamed-at-first
		// wolf still strolls, so the bar is looser than the sit
		// probe's — the follow would close well under 5.
		sendCommand(t, v.pwa, "tp BOATC 0.5 -60 0.5")
		w4 := summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa,
			"wolf", 15.5, -60, 0.5, "{Owner:"+ownerC+"}")
		if w4 == 0 {
			t.Fatalf("%s: spectator-owner summon failed", name)
		}
		wait(5 * time.Second)
		o.specDist = distTo(0.5, 0.5)
		clear()

		// Phase 5 — leashed tame: mayBeLeashed blocks the goal even
		// with a resolvable owner in range.
		sendCommand(t, v.pwa, "setblock 15 -60 5 oak_fence")
		w5 := summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa,
			"wolf", 15.5, -60, 5.5,
			"{Owner:"+ownerA+",leash:[I;15,-60,5]}")
		if w5 == 0 {
			t.Fatalf("%s: leash summon failed", name)
		}
		wait(5 * time.Second)
		o.leashDist = distTo(0.5, 0.5)
		sendCommand(t, v.pwa, "setblock 15 -60 5 air")
		return o
	}

	want := run(t, "vanilla", targets.vanilla)
	got := run(t, "mc-go", targets.mcgo)

	check := func(name string, o obs) {
		t.Helper()
		if o.followDist < 0.5 || o.followDist > 8.0 {
			t.Errorf("%s: walk-follow dist %.2f, want inside [0.5,8]", name, o.followDist)
		}
		if o.teleJumps < 1 {
			t.Errorf("%s: no owner-teleport packet jump seen", name)
		}
		if o.teleDist < 0 || o.teleDist > 8 {
			t.Errorf("%s: teleport dist %.2f, want <=8", name, o.teleDist)
		}
		if o.sitDist < 10 {
			t.Errorf("%s: sitting tame followed to %.2f", name, o.sitDist)
		}
		if o.specDist < 5 {
			t.Errorf("%s: spectator-owned tame followed to %.2f", name, o.specDist)
		}
		if o.leashDist < 10 {
			t.Errorf("%s: leashed tame followed to %.2f", name, o.leashDist)
		}
		t.Logf("%s: follow=%.2f tele=%.2f jumps=%d sit=%.2f spec=%.2f leash=%.2f",
			name, o.followDist, o.teleDist, o.teleJumps, o.sitDist, o.specDist, o.leashDist)
	}
	check("vanilla", want)
	check("mc-go", got)
}

// summonTrackedNBTTrail is summonTrackedNBT plus a posTrail collector:
// the follow-owner teleport lands inside the spawn-hunt window, so the
// trail has to start accumulating at the hunt's first packet — and keep
// collecting for watchDur after the add_entity arrives. trail[eid]
// carries every move-delta sample the hunt and retries saw, measured
// against the summon coords for absolute-position packets.
func summonTrackedNBTTrail(t *testing.T, c net.Conn, pr *protocol.Reader, pw, pwc *protocol.Writer, mob string, x, y, z float64, nbt string, trail *map[int32][][2]float64, watchDur time.Duration) int32 {
	t.Helper()
	if *trail == nil {
		*trail = map[int32][][2]float64{}
	}
	typeID, ok := server.EntityTypeID(strings.TrimPrefix(mob, "minecraft:"))
	if !ok {
		t.Fatalf("unknown entity type %s", mob)
	}
	for tries := 0; tries < 8; tries++ {
		sendCommand(t, pwc, fmt.Sprintf("summon %s %v %v %v %s", mob, x, y, z, nbt))
		deadline := time.Now().Add(2500 * time.Millisecond)
		found := int32(-1)
		for time.Now().Before(deadline) {
			_ = c.SetReadDeadline(time.Now().Add(time.Until(deadline)))
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
				r := protocol.NewPacketReader(p.Data)
				if id, err := r.VarInt(); err == nil {
					var b protocol.Buffer
					b.VarInt(id)
					_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
				}
			case 53, 54: // move_entity_pos / _pos_rot — delta shorts /4096
				r := protocol.NewPacketReader(p.Data)
				peid, _ := r.VarInt()
				dx, _ := r.Short()
				_, _ = r.Short()
				dz, _ := r.Short()
				(*trail)[peid] = append((*trail)[peid], [2]float64{float64(dx) / 4096, float64(dz) / 4096})
			case 125: // teleport_entity — absolute doubles, a jump anyway
				r := protocol.NewPacketReader(p.Data)
				peid, _ := r.VarInt()
				tx, _ := r.Double()
				_, _ = r.Double()
				tz, _ := r.Double()
				(*trail)[peid] = append((*trail)[peid], [2]float64{tx - x, tz - z})
			case 35: // entity_position_sync — the real teleport packet:
				// VarInt eid, 3 pos doubles, 3 velocity doubles, 2 rot floats, bool
				r := protocol.NewPacketReader(p.Data)
				peid, _ := r.VarInt()
				tx, _ := r.Double()
				_, _ = r.Double()
				tz, _ := r.Double()
				(*trail)[peid] = append((*trail)[peid], [2]float64{tx - x, tz - z})
			case server.IDPlaySpawnEntity:
				r := protocol.NewPacketReader(p.Data)
				eid, e1 := r.VarInt()
				_, e2 := r.Read(16)
				typ, e3 := r.VarInt()
				if e1 == nil && e2 == nil && e3 == nil && typ == typeID {
					found = eid
					// keep collecting the trail for the
					// watch window — the teleport cadence
					// lands after the spawn packet.
					deadline = time.Now().Add(watchDur)
				}
			}
		}
		if found >= 0 {
			_ = c.SetReadDeadline(time.Time{})
			return found
		}
	}
	_ = c.SetReadDeadline(time.Time{})
	t.Fatalf("no %s spawn packet", mob)
	return 0
}
