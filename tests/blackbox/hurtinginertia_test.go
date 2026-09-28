// Hurting-projectile inertia parity — vanilla
// (AbstractHurtingProjectile.applyInertia javap) scales
// delta+normalize(delta)*accelerationPower by getInertia: 0.95 base,
// 1.0 for the wind-charge pair, 0.73 for a dangerous WitherSkull, and
// getLiquidInertia 0.8 (1.0 for wind charges) while isInWater. The
// bytecode's four BUBBLE addParticle calls are a dedicated-server
// no-op, so only the delta profile reaches the wire.
//
// WitherSkull.dangerous is entity metadata index 8 (BOOLEAN) plus the
// "dangerous" NBT key — read via getBooleanOr and written
// unconditionally by addAdditionalSaveData, so `data get` answers it
// for every skull.
package blackbox

import (
	"math"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
)

// motionSample is one decoded set_entity_motion delta for a hurting
// projectile (blocks/tick). 26.2 ships deltas as LpVec3 — decoded by
// the shared decodeLpVec3 in wear_parity_test.go.
type motionSample struct {
	x, y, z float64
}

func TestHurtingInertiaParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Hurt"),
	}, nil)
	defer targets.cleanup()

	type track struct {
		metadata string // raw entity_data payloads seen at pairing
		motions  []motionSample
		removed  bool // remove_entities seen for this skull
	}

	perLeg := map[string]map[string]*track{}

	for _, tg := range []struct{ tag, addr string }{
		{"vanilla", targets.vanilla},
		{"mc-go", targets.mcgo},
	} {
		conn, pr, pw := joinPlayBlackbox(t, tg.addr, "Hurt")
		sink := newChatSinkTP(pr, pw)
		// joinPlayBlackbox never sends client_information, so
		// vanilla falls back to the default view distance of 2 —
		// min(effectiveRange, 2*16) = 32 horizontal blocks — and
		// untracks (remove_entities) any skull that flies more
		// than ~11 blocks +x of the player. Sending the real
		// packet lifts the ceiling to the full 64-block
		// clientTrackingRange so removals reflect behavior, not
		// a stub client's defaults.
		sendClientInformation(t, pw)
		sendCommand(t, pw, "tp @s 4 -60 -30 0 0")
		// mc-go's sendWindow work (light init + section warm) runs on
		// the packet-handler thread: a client_information widening to
		// view 8 costs ~2s, during which every later command queues in
		// TCP and lands as one burst. dataGetLine waits on the reply —
		// the reply can only come back once everything ahead of it in
		// the socket has been handled — so it is the natural drain
		// point for the backlog.
		dataGetLine(t, sink, pw, "@s", "Pos")

		// Water lane: the flying skulls move +x through a filled
		// pool spanning both lane rows plus a still row at z=6;
		// two air lanes above reference the dry inertias. The pool
		// covers the whole flight window so no skull reaches the
		// water/air boundary — liquid inertia applies the entire
		// run regardless of what stale fluid or terrain the
		// accumulated test world holds past the probe cells.
		sendCommand(t, pw, "fill 0 -61 -1 45 -58 6 minecraft:water")
		// Air lanes get a carved corridor for the same reason:
		// without it, a leftover wall somewhere past x≈15 lets
		// one leg's skull explode mid-flight while the other's
		// keeps emitting — a world-history artifact, not a
		// behavior difference.
		sendCommand(t, pw, "fill -1 -41 -1 45 -38 6 minecraft:air")
		// …and sweep entities: the accumulated test worlds carry
		// leftovers from earlier tests, and a flying skull
		// explodes on contact with any pickable entity (leftover
		// mob, item, or orb) drifting through the corridor.
		sendCommand(t, pw, "kill @e[type=!minecraft:player,x=-5,y=-64,z=-5,dx=55,dy=30,dz=15]")
		// Same drain: the fills+kill must be in the world before the
		// first summon leaves the handler thread.
		dataGetLine(t, sink, pw, "@s", "Pos")

		// Sequential single-lane summons — one confirmed-air
		// corridor row serves every probe; each skull clears the
		// spawn cell before the next arrives. (Parallel lanes at
		// z=4 die instantly: that row is solid tunnel wall — a
		// skull summoned inside a block explodes on its first
		// tick and never reaches the tracker.)
		laneNames := []string{"water_dangerous", "water_plain", "air_dangerous", "air_plain", "still_dangerous", "still_plain"}
		sendCommand(t, pw, "summon minecraft:wither_skull 2 -59 0 {Motion:[0.5d,0.0d,0.0d],dangerous:1b}")
		time.Sleep(800 * time.Millisecond)
		sendCommand(t, pw, "summon minecraft:wither_skull 2 -59 0 {Motion:[0.5d,0.0d,0.0d]}")
		time.Sleep(800 * time.Millisecond)
		sendCommand(t, pw, "summon minecraft:wither_skull 2 -40 0 {Motion:[0.5d,0.0d,0.0d],dangerous:1b}")
		time.Sleep(800 * time.Millisecond)
		sendCommand(t, pw, "summon minecraft:wither_skull 2 -40 0 {Motion:[0.5d,0.0d,0.0d]}")
		time.Sleep(800 * time.Millisecond)
		// The still skulls hover inside the pool at the proven
		// spawn cell, offset in x for the data-get selectors.
		sendCommand(t, pw, "summon minecraft:wither_skull 2 -59 0 {Motion:[0.0d,0.0d,0.0d],dangerous:1b}")
		sendCommand(t, pw, "summon minecraft:wither_skull 12 -59 0 {Motion:[0.0d,0.0d,0.0d]}")
		// The drift emit fires at each updateInterval gate while
		// |delta-lastSent|² > 1e-7 (|Δv| > ~3.2e-4): water lanes emit
		// three times (gates ~10/20/30 — the 0.8-inertia convergence
		// then drops under the drift floor), air lanes too while the
		// flight lasts. Give every skull at least ~40 ticks so the
		// third gate lands before the window closes.
		time.Sleep(2200 * time.Millisecond)

		tracks := map[string]*track{}
		for i, lane := range laneNames {
			tracks[lane] = &track{}
			_ = i
		}
		var eids [6]int32
		n := 0
		var replies []string
		for _, l := range sink.take() {
			if strings.Contains(l, "summon") || strings.Contains(l, "error") || strings.Contains(l, "data.") ||
				strings.Contains(l, "Invalid") || strings.Contains(l, "Expected") || strings.Contains(l, "whitespace") ||
				strings.Contains(l, "Cannot") {
				replies = append(replies, l)
			}
			pref, rest, ok := strings.Cut(l, ":")
			if !ok {
				continue
			}
			switch pref {
			case "0x1": // add_entity: id + UUID16 + type + pos + rot
				r := protocol.NewPacketReader([]byte(rest))
				id, e1 := r.VarInt()
				_, e2 := r.Read(16)
				typ, e3 := r.VarInt()
				px, _ := r.Double()
				py, _ := r.Double()
				pz, _ := r.Double()
				// wither_skull is entity type 148 in 26.2.
				if e1 == nil && e2 == nil && e3 == nil {
					t.Logf("%s spawn: eid=%d type=%d pos=%v,%v,%v", tg.tag, id, typ, px, py, pz)
					if typ == 148 && n < len(eids) {
						eids[n] = id
						n++
					}
				}
			case "0x65": // set_entity_motion: id + LpVec3
				r := protocol.NewPacketReader([]byte(rest))
				id, e1 := r.VarInt()
				mx, my, mz, _ := decodeLpVec3(r)
				if e1 != nil {
					continue
				}
				for i := 0; i < n; i++ {
					if id == eids[i] {
						tracks[laneNames[i]].motions = append(tracks[laneNames[i]].motions, motionSample{
							x: mx, y: my, z: mz,
						})
					}
				}
			case "0x63": // set_entity_data: id + entries + 0xFF
				r := protocol.NewPacketReader([]byte(rest))
				id, e1 := r.VarInt()
				if e1 != nil {
					continue
				}
				for i := 0; i < n; i++ {
					if id == eids[i] {
						tracks[laneNames[i]].metadata += rest
					}
				}
			case "0x4d": // remove_entities: int count + ids
				r := protocol.NewPacketReader([]byte(rest))
				for {
					id, e1 := r.VarInt()
					if e1 != nil {
						break
					}
					for i := 0; i < n; i++ {
						if id == eids[i] {
							tracks[laneNames[i]].removed = true
						}
					}
				}
			}
		}
		if n < len(eids) {
			for _, l := range replies {
				t.Logf("%s reply: %q", tg.tag, l)
			}
			conn.Close()
			t.Fatalf("%s: expected 6 wither_skull spawns, saw %d", tg.tag, n)
		}

		sendCommand(t, pw, "execute as @e[type=minecraft:wither_skull] run data get entity @s Pos")
		time.Sleep(300 * time.Millisecond)
		for _, l := range sink.take() {
			if strings.Contains(l, "data.entity.query") {
				t.Logf("%s pos: %q", tg.tag, l)
			}
		}
		// `data get` on the two stationary skulls — the save writes
		// dangerous unconditionally, so the plain skull must answer
		// 0b too, not "found no elements". limit=1+sort=nearest
		// keeps each query to a single entity.
		dangerLine := dataGetLine(t, sink, pw, "@e[type=minecraft:wither_skull,x=2,y=-59,z=0,distance=..3,sort=nearest,limit=1]", "dangerous")
		plainLine := dataGetLine(t, sink, pw, "@e[type=minecraft:wither_skull,x=12,y=-59,z=0,distance=..3,sort=nearest,limit=1]", "dangerous")
		if !strings.Contains(dangerLine, "data.entity.query") ||
			!(strings.Contains(dangerLine, "text\x00\x011") && strings.Contains(dangerLine, "text\x00\x01b")) {
			conn.Close()
			t.Fatalf("%s: dangerous skull data get = %q", tg.tag, dangerLine)
		}
		if !strings.Contains(plainLine, "data.entity.query") ||
			!(strings.Contains(plainLine, "text\x00\x010") && strings.Contains(plainLine, "text\x00\x01b")) {
			conn.Close()
			t.Fatalf("%s: plain skull data get = %q", tg.tag, plainLine)
		}

		perLeg[tg.tag] = tracks
		conn.Close()
	}

	var bad bool
	for _, lane := range []string{"water_dangerous", "water_plain", "air_dangerous", "air_plain", "still_dangerous", "still_plain"} {
		v, g := perLeg["vanilla"][lane], perLeg["mc-go"][lane]
		t.Logf("%s: vanilla %d emits | mc-go %d emits (removed %v|%v)", lane, len(v.motions), len(g.motions), v.removed, g.removed)
		t.Logf("  vanilla: %+v", v.motions)
		t.Logf("  mc-go  : %+v", g.motions)
		if len(v.motions) == 0 && len(g.motions) == 0 {
			continue
		}
		if len(v.motions) != len(g.motions) {
			t.Errorf("%s: emit counts diverge (vanilla %d, mc-go %d)", lane, len(v.motions), len(g.motions))
			bad = true
			continue
		}
		for i := range v.motions {
			dx := v.motions[i].x - g.motions[i].x
			dy := v.motions[i].y - g.motions[i].y
			dz := v.motions[i].z - g.motions[i].z
			if math.Abs(dx) > 0.02 || math.Abs(dy) > 0.02 || math.Abs(dz) > 0.02 {
				t.Errorf("%s: motion[%d] diverges (vanilla %+v, mc-go %+v)", lane, i, v.motions[i], g.motions[i])
				bad = true
			}
		}
	}
	if bad {
		t.FailNow()
	}

	// End-state ordering from the asymptotes — v∞ = accel·i/(1−i)
	// with accel=0.1: air plain climbs toward 1.9, water (any
	// skull, getLiquidInertia doesn't read the flag) settles at
	// 0.4, air dangerous decays toward 0.27. So a dangerous skull
	// is FASTER in water than in air, while a plain skull is
	// slower in water than in air.
	for _, leg := range []string{"vanilla", "mc-go"} {
		d := perLeg[leg]["air_dangerous"].motions
		p := perLeg[leg]["air_plain"].motions
		if len(d) == 0 || len(p) == 0 || d[len(d)-1].x >= p[len(p)-1].x {
			t.Fatalf("%s: air dangerous skull not slower than plain (d %+v, p %+v)", leg, d[len(d)-1], p[len(p)-1])
		}
		wd := perLeg[leg]["water_dangerous"].motions
		ad := perLeg[leg]["air_dangerous"].motions
		wp := perLeg[leg]["water_plain"].motions
		ap := perLeg[leg]["air_plain"].motions
		if len(wd) == 0 || len(ad) == 0 || wd[len(wd)-1].x <= ad[len(ad)-1].x {
			t.Fatalf("%s: water dangerous not faster than air dangerous (w %+v, a %+v)", leg, wd[len(wd)-1], ad[len(ad)-1])
		}
		if len(wp) == 0 || len(ap) == 0 || wp[len(wp)-1].x >= ap[len(ap)-1].x {
			t.Fatalf("%s: water plain not slower than air plain (w %+v, a %+v)", leg, wp[len(wp)-1], ap[len(ap)-1])
		}
	}

	// Pairing metadata: only the dangerous skulls carry the
	// {index 8, BOOLEAN serializer 8, 1} entry.
	for _, leg := range []string{"vanilla", "mc-go"} {
		for _, lane := range []string{"water_dangerous", "air_dangerous", "still_dangerous"} {
			md := perLeg[leg][lane].metadata
			if !strings.Contains(md, "\x08\x08\x01") {
				t.Fatalf("%s %s: dangerous skull missing metadata entry 8 BOOLEAN=1 (%q)", leg, lane, md)
			}
		}
		for _, lane := range []string{"water_plain", "air_plain", "still_plain"} {
			md := perLeg[leg][lane].metadata
			if strings.Contains(md, "\x08\x08\x01") {
				t.Fatalf("%s %s: plain skull emits dangerous entry (%q)", leg, lane, md)
			}
		}
	}
}
