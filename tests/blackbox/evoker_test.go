// Blackbox parity pin for the Evoker's spell goals and EvokerFangs
// (26.2 jar, monster/illager/Evoker + its Evoker*SpellGoal inner
// classes, SpellcasterIllager and its goals, projectile/EvokerFangs;
// javap -c). The evoker stands in a 1-deep 1×1 pit under a stone
// ceiling (it cannot jump out), its eye just above the rim:
// TargetingConditions keeps its line-of-sight check on for both the
// NearestAttackableTarget acquisition and the wololo sheep search
// (measured: a 2-deep pit hides a villager six blocks out). Every
// player is a spectator; the targets are NoAI villagers standing in
// 1-deep pits of their own (eye level with the evoker's), and the
// wololo sheep only counts while nothing is targeted.
//
//  1. mob_griefing=false gates EvokerWololoSpellGoal: no spell.
//  2. mob_griefing=true: DATA_SPELL_CASTING_ID (17) = 3 with
//     entity.evoker.prepare_wololo, then the BLUE sheep's wool byte
//     (18) turns RED (14) with entity.evoker.cast_spell.
//  3. A villager six blocks out: the summon spell (17 = 1, prepare_
//     summon) casts three vexes one block above the evoker's cell
//     within ±2 (cell bottom centres), then — with no 0 between — the
//     fang spell (17 = 2, prepare_attack): sixteen fangs in a line
//     toward the target at 1.25·(i+1), each biting (entity event 4)
//     in distance order, and the villager takes the bite.
//  4. A villager two blocks out (distSqr < 9): the next fang cast is
//     the 5 + 8 circle at radius 1.5 / 2.5, the inner ring biting
//     first.
package blackbox

import (
	"math"
	"os"
	"sort"
	"testing"
	"time"
)

func TestEvokerSpellParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB", "BOATC"),
	}, nil)
	defer targets.cleanup()

	type obs struct {
		gateSpell    bool    // a wololo spell started with mob_griefing off
		wololoSpell  bool    // 17 = 3 seen
		wololoSound  bool    // prepare_wololo
		sheepRed     bool    // sheep wool byte 14
		summonSpell  bool    // 17 = 1 then directly 2
		directToTwo  bool    // the value after 1 is 2
		vexes        int     // vex adds on the summon cast
		vexOffsetsOK bool    // cell-centred, y+1, within ±2
		lineFangs    int     // fang adds on the first fang cast
		lineGeomOK   bool    // spacing 1.25 along +x from the evoker
		lineBitesOK  bool    // every fang bit (event 4), nearer first
		villagerHit  bool    // the line villager took a damage event
		circleFangs  int     // fang adds on the circle cast
		circleRadii  [2]int  // fangs at r 1.5 / 2.5
		circleOrder  bool    // inner ring bites before the outer
		sounds       [4]bool // prepare_summon, prepare_attack, cast_spell, prepare_wololo
	}

	run := func(t *testing.T, name, addr string) obs {
		var o obs
		v := joinVehPair(t, addr)
		cc, prc, pwc := joinPlayBlackbox(t, addr, "BOATC")
		defer cc.Close()
		drainChunkWindow(t, "evoker", v.ca, v.pra, v.pwa, 0)
		_ = newChatSinkTP(prc, pwc)
		tp := startHostileTap(v)
		for _, p := range []string{"BOATA", "BOATB", "BOATC"} {
			sendCommand(t, v.pwa, "gamemode spectator "+p)
		}
		sendCommand(t, v.pwa, "gamerule do_mob_spawning false")
		sendCommand(t, v.pwa, "kill @e[type=!minecraft:player]")
		sendCommand(t, v.pwa, "tp BOATA 12.5 -58 2.5")
		sendCommand(t, v.pwa, "tp BOATB 12.5 -58 4.5")
		sendCommand(t, v.pwa, "tp BOATC 12.5 -58 6.5")
		sendCommand(t, v.pwa, "fill 8 -61 8 8 -61 8 minecraft:air")
		sendCommand(t, v.pwa, "setblock 8 -59 8 minecraft:stone")
		sendCommand(t, v.pwa, "setblock 14 -61 8 minecraft:air")
		sendCommand(t, v.pwa, "setblock 10 -61 8 minecraft:air")
		sendCommand(t, v.pwa, "gamerule mob_griefing false")
		hostileWatch(t, tp, nil, 800*time.Millisecond)

		lg := newHostileLog()
		sendCommand(t, v.pwa, "summon minecraft:evoker 8.5 -61 8.5")
		hostileWatchUntil(t, tp, lg, 5*time.Second, func(l *hostileLog) bool { return l.firstEid("evoker") != 0 })
		ev := lg.firstEid("evoker")
		if ev == 0 {
			t.Fatalf("%s: no evoker add_entity", name)
		}
		lg.refEid = ev
		hostileWatch(t, tp, lg, 500*time.Millisecond)

		// 1 — the mob_griefing gate.
		sendCommand(t, v.pwa, "summon minecraft:sheep 12.5 -60 12.5 {Color:11b,NoAI:1b}")
		mark := len(lg.events)
		hostileWatch(t, tp, lg, 4*time.Second)
		for _, e := range lg.events[mark:] {
			if e.kind == "meta" && e.eid == ev && e.idx == 17 && e.val == 3 {
				o.gateSpell = true
			}
		}
		sheep := lg.firstEid("sheep")

		// 2 — wololo.
		sendCommand(t, v.pwa, "gamerule mob_griefing true")
		mark = len(lg.events)
		hostileWatchUntil(t, tp, lg, 8*time.Second, func(l *hostileLog) bool {
			for _, m := range l.metas(sheep, 18) {
				if m.val&15 == 14 {
					return true
				}
			}
			return false
		})
		hostileWatch(t, tp, lg, 300*time.Millisecond)
		for _, e := range lg.events[mark:] {
			if e.kind == "meta" && e.eid == ev && e.idx == 17 && e.val == 3 {
				o.wololoSpell = true
			}
			if e.kind == "meta" && e.eid == sheep && e.idx == 18 && e.val&15 == 14 {
				o.sheepRed = true
			}
		}
		o.wololoSound = len(lg.sounds("entity.evoker.prepare_wololo")) > 0
		sendCommand(t, v.pwa, "kill @e[type=sheep]")
		// Let the wololo cast (60 ticks) run out.
		hostileWatch(t, tp, lg, 3500*time.Millisecond)

		// 3 — summon then the fang line toward a villager six out.
		sendCommand(t, v.pwa, "summon minecraft:villager 14.5 -61 8.5 {NoAI:1b}")
		mark = len(lg.events)
		vexKilled := false
		hostileWatchUntil(t, tp, lg, 15*time.Second, func(l *hostileLog) bool {
			if !vexKilled && len(l.adds("vex")) >= 3 {
				sendCommand(t, v.pwa, "kill @e[type=vex]")
				vexKilled = true
			}
			n := 0
			for _, e := range l.events[mark:] {
				if e.kind == "add" && e.typ == "evoker_fangs" {
					n++
				}
			}
			return n >= 16
		})
		hostileWatch(t, tp, lg, 2*time.Second)
		villager := lg.firstEid("villager")
		seq := []int64{}
		for _, e := range lg.events[mark:] {
			if e.kind == "meta" && e.eid == ev && e.idx == 17 {
				seq = append(seq, e.val)
			}
		}
		for i, s := range seq {
			if s == 1 {
				o.summonSpell = true
				o.directToTwo = i+1 < len(seq) && seq[i+1] == 2
				break
			}
		}
		vexes := lg.adds("vex")
		o.vexes = len(vexes)
		o.vexOffsetsOK = len(vexes) == 3
		for _, vx := range vexes {
			cx, cz := math.Floor(vx.ref[0]), math.Floor(vx.ref[2])
			dx, dz := vx.xyz[0]-cx-0.5, vx.xyz[2]-cz-0.5
			fx, fz := dx-math.Round(dx), dz-math.Round(dz)
			if math.Abs(fx) > 1e-6 || math.Abs(fz) > 1e-6 || math.Abs(dx) > 2.001 || math.Abs(dz) > 2.001 ||
				!approxEq(vx.xyz[1], math.Floor(vx.ref[1])+1, 1e-6) {
				o.vexOffsetsOK = false
				t.Logf("%s: vex at %v vs evoker %v", name, vx.xyz, vx.ref)
			}
		}
		var line []hostileEv
		for _, e := range lg.events[mark:] {
			if e.kind == "add" && e.typ == "evoker_fangs" {
				line = append(line, e)
			}
		}
		o.lineFangs = len(line)
		o.lineGeomOK, o.lineBitesOK = fangLineCheck(t, name, lg, line)
		o.villagerHit = len(lg.damages(villager)) > 0
		sendCommand(t, v.pwa, "kill @e[type=villager]")
		sendCommand(t, v.pwa, "kill @e[type=vex]")

		// 4 — the circle: a villager inside distSqr 9.
		sendCommand(t, v.pwa, "summon minecraft:villager 10.5 -61 8.5 {NoAI:1b}")
		mark = len(lg.events)
		hostileWatchUntil(t, tp, lg, 15*time.Second, func(l *hostileLog) bool {
			if len(l.adds("vex")) > 3 {
				sendCommand(t, v.pwa, "kill @e[type=vex]")
			}
			n := 0
			for _, e := range l.events[mark:] {
				if e.kind == "add" && e.typ == "evoker_fangs" {
					n++
				}
			}
			return n >= 13
		})
		hostileWatch(t, tp, lg, 2*time.Second)
		var ring []hostileEv
		for _, e := range lg.events[mark:] {
			if e.kind == "add" && e.typ == "evoker_fangs" {
				ring = append(ring, e)
			}
		}
		o.circleFangs = len(ring)
		innerLast, outerFirst := time.Time{}, time.Time{}
		for _, f := range ring {
			r := math.Hypot(f.xyz[0]-f.ref[0], f.xyz[2]-f.ref[2])
			bites := lg.entityEvents(f.eid, 4)
			switch {
			case approxEq(r, 1.5, 0.01):
				o.circleRadii[0]++
				if len(bites) > 0 && bites[0].at.After(innerLast) {
					innerLast = bites[0].at
				}
			case approxEq(r, 2.5, 0.01):
				o.circleRadii[1]++
				if len(bites) > 0 && (outerFirst.IsZero() || bites[0].at.Before(outerFirst)) {
					outerFirst = bites[0].at
				}
			default:
				t.Logf("%s: ring fang at r=%.4f (%v vs %v)", name, r, f.xyz, f.ref)
			}
		}
		o.circleOrder = !innerLast.IsZero() && !outerFirst.IsZero() && !outerFirst.Before(innerLast)
		for i, snd := range []string{"entity.evoker.prepare_summon", "entity.evoker.prepare_attack",
			"entity.evoker.cast_spell", "entity.evoker.prepare_wololo"} {
			o.sounds[i] = len(lg.sounds(snd)) > 0
		}
		if os.Getenv("MCGO_HOSTILE_DEBUG") != "" {
			t0 := lg.events[0].at
			for _, e := range lg.events {
				switch e.kind {
				case "add", "sound":
					t.Logf("%s dbg %6.2fs %s eid=%d typ=%s idx=%d xyz=%.2f", name, e.at.Sub(t0).Seconds(), e.kind, e.eid, e.typ, e.idx, e.xyz)
				case "meta":
					if e.eid == ev || e.eid == sheep {
						t.Logf("%s dbg %6.2fs meta eid=%d idx=%d val=%d", name, e.at.Sub(t0).Seconds(), e.eid, e.idx, e.val)
					}
				}
			}
		}
		sendCommand(t, v.pwa, "kill @e[type=!minecraft:player]")
		hostileWatch(t, tp, nil, 400*time.Millisecond)
		return o
	}

	check := func(t *testing.T, name string, o obs) {
		t.Logf("%s: %+v", name, o)
		if o.gateSpell {
			t.Errorf("%s: wololo started with mob_griefing=false", name)
		}
		if !o.wololoSpell || !o.wololoSound || !o.sheepRed {
			t.Errorf("%s: wololo spell=%v sound=%v red=%v, want all", name, o.wololoSpell, o.wololoSound, o.sheepRed)
		}
		if !o.summonSpell || !o.directToTwo {
			t.Errorf("%s: summon spell=%v then fangs directly=%v", name, o.summonSpell, o.directToTwo)
		}
		if o.vexes != 3 || !o.vexOffsetsOK {
			t.Errorf("%s: vexes=%d offsetsOK=%v, want 3 cell-centred at y+1 within ±2", name, o.vexes, o.vexOffsetsOK)
		}
		if o.lineFangs != 16 || !o.lineGeomOK || !o.lineBitesOK {
			t.Errorf("%s: line fangs=%d geom=%v bites=%v, want 16/true/true", name, o.lineFangs, o.lineGeomOK, o.lineBitesOK)
		}
		if !o.villagerHit {
			t.Errorf("%s: the line villager took no fang bite", name)
		}
		if o.circleFangs != 13 || o.circleRadii != [2]int{5, 8} || !o.circleOrder {
			t.Errorf("%s: circle fangs=%d radii=%v innerFirst=%v, want 13 [5 8] true", name, o.circleFangs, o.circleRadii, o.circleOrder)
		}
		for i, ok := range o.sounds {
			if !ok {
				t.Errorf("%s: evoker sound %d missing", name, i)
			}
		}
	}
	vo := run(t, "vanilla", targets.vanilla)
	mo := run(t, "mc-go", targets.mcgo)
	check(t, "vanilla", vo)
	check(t, "mc-go", mo)
}

// fangLineCheck verifies a sixteen-fang line: sorted by distance from
// the caster the fangs sit 1.25 apart starting at 1.25, all on one
// ray, and each bit (event 4) no earlier than the one before it.
func fangLineCheck(t *testing.T, name string, lg *hostileLog, line []hostileEv) (geom, bites bool) {
	if len(line) == 0 {
		return false, false
	}
	type fr struct {
		r  float64
		ev hostileEv
	}
	var fs []fr
	for _, f := range line {
		fs = append(fs, fr{math.Hypot(f.xyz[0]-f.ref[0], f.xyz[2]-f.ref[2]), f})
	}
	sort.Slice(fs, func(i, j int) bool { return fs[i].r < fs[j].r })
	geom, bites = true, true
	var last time.Time
	ux := (fs[0].ev.xyz[0] - fs[0].ev.ref[0]) / fs[0].r
	uz := (fs[0].ev.xyz[2] - fs[0].ev.ref[2]) / fs[0].r
	for i, f := range fs {
		// Perpendicular offset from the first fang's ray.
		perp := (f.ev.xyz[0]-f.ev.ref[0])*uz - (f.ev.xyz[2]-f.ev.ref[2])*ux
		if !approxEq(f.r, 1.25*float64(i+1), 0.01) || math.Abs(perp) > 0.01 {
			geom = false
			t.Logf("%s: fang %d at r=%.4f %v (caster %v)", name, i, f.r, f.ev.xyz, f.ev.ref)
		}
		b := lg.entityEvents(f.ev.eid, 4)
		if len(b) != 1 {
			bites = false
			continue
		}
		if b[0].at.Before(last) {
			bites = false
		}
		last = b[0].at
	}
	return geom, bites
}
