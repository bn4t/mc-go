// Projectile deflection parity — vanilla
// (Player.deflectProjectile / Projectile.deflect /
// ProjectileDeflection javap + entity_type tags): a player punch on
// a #redirectable_projectile entity (fireball, wind_charge,
// breeze_wind_charge) deflects it AIM-style — delta := the
// attacker's look angle, owner := the attacker, and
// AbstractHurtingProjectile.onDeflection(flag=true) resets
// accelerationPower to 0.1 — then plays PLAYER_ATTACK_NODAMAGE and
// the attack returns before any damage math. A wither_skull is not
// in the tag: the attack falls through to Projectile.hurtServer
// (always false) and the projectile flies on unchanged.
//
// Projectile.addAdditionalSaveData persists the shared tail:
// Owner as the [I;] uuid array while set, LeftOwner once latched
// (ownerless projectiles latch on their first tick — the expanded
// box never overlaps a missing owner), HasBeenShot unconditionally.
package blackbox

import (
	"math"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestProjectileDeflectParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Defl"),
	}, nil)
	defer targets.cleanup()

	type obs struct {
		fbMotions  []motionSample
		fbPowers   []float64
		skMotions  []motionSample
		fb2Motions []motionSample // breeze-deflected fireball
		fb2Powers  []float64
		removed    bool
		sounds     []int32 // PlaySound registry ids (soundID+1 on the wire)
	}
	perLeg := map[string]*obs{}
	ownerInts := map[string][]string{}

	for _, tg := range []struct{ tag, addr string }{
		{"vanilla", targets.vanilla},
		{"mc-go", targets.mcgo},
	} {
		conn, pr, pw := joinPlayBlackbox(t, tg.addr, "Defl")
		sink := newChatSinkTP(pr, pw)
		// client_information lifts vanilla's default view distance
		// (2 → 32-block tracking ceiling); see hurtinginertia_test.
		sendClientInformationVD(t, pw, 24) // view 24 keeps the +z runners ticketed
		sendCommand(t, pw, "tp @s 0 -59 0 0 0")
		// dataGetLine drains mc-go's synchronous command queue —
		// the sendWindow stall would otherwise burst the setup.
		dataGetLine(t, sink, pw, "@s", "Pos")

		// Clean corridors + entity sweep so no stale mob or block
		// eats a fireball mid-flight. The punch lane flies +z from
		// z=2, the breeze lane approaches -z from z=9 — both need
		// clear air along the z axis, not just x.
		sendCommand(t, pw, "fill -6 -61 0 40 -56 4 minecraft:air")
		sendCommand(t, pw, "fill -2 -61 4 2 -56 60 minecraft:air")
		sendCommand(t, pw, "kill @e[type=!minecraft:player,x=-8,y=-64,z=-2,dx=50,dy=10,dz=66]")
		dataGetLine(t, sink, pw, "@s", "Pos")

		// Still fireball in reach at z=2; a second skull lane at
		// x=-2 proves the non-redirectable branch.
		sendCommand(t, pw, "summon minecraft:fireball 0 -59 2 {Motion:[0.0d,0.0d,0.0d]}")
		sendCommand(t, pw, "summon minecraft:wither_skull -2 -59 2 {Motion:[0.0d,0.0d,0.0d]}")

		fbType, _ := server.EntityTypeID("fireball")
		skType, _ := server.EntityTypeID("wither_skull")
		var fbEid, skEid, fb2Eid int32
		o := &obs{}
		// collect parses every drained line — entity spawns feed the
		// eid harvest, wire packets feed the obs — so no take() can
		// silently discard an emit between phases.
		collect := func(l string) {
			pref, rest, ok := strings.Cut(l, ":")
			if !ok {
				return
			}
			r := protocol.NewPacketReader([]byte(rest))
			switch pref {
			case "0x1": // add_entity: id + UUID16 + type + pos + rot
				id, e1 := r.VarInt()
				_, e2 := r.Read(16)
				typ, e3 := r.VarInt()
				if e1 != nil || e2 != nil || e3 != nil {
					return
				}
				if typ == fbType && fbEid == 0 {
					fbEid = id
				} else if typ == fbType && fb2Eid == 0 {
					fb2Eid = id
				}
				if typ == skType && skEid == 0 {
					skEid = id
				}
			case "0x65": // set_entity_motion: id + LpVec3
				id, e1 := r.VarInt()
				mx, my, mz, _ := decodeLpVec3(r)
				if e1 != nil {
					return
				}
				if id == fbEid {
					o.fbMotions = append(o.fbMotions, motionSample{mx, my, mz})
				}
				if id == skEid {
					o.skMotions = append(o.skMotions, motionSample{mx, my, mz})
				}
				if id == fb2Eid {
					o.fb2Motions = append(o.fb2Motions, motionSample{mx, my, mz})
				}
			case "0x87": // projectile_power: id + double power
				id, e1 := r.VarInt()
				pw2, e2 := r.Double()
				if e1 != nil || e2 != nil {
					return
				}
				if id == fbEid {
					o.fbPowers = append(o.fbPowers, pw2)
				}
				if id == fb2Eid {
					o.fb2Powers = append(o.fb2Powers, pw2)
				}
			case "0x4d": // remove_entities
				for {
					id, e1 := r.VarInt()
					if e1 != nil {
						break
					}
					if id == fbEid || id == skEid || id == fb2Eid {
						o.removed = true
					}
				}
			case "0x75": // sound: VarInt id(+1), source, x*8, y*8, z*8, vol, pitch, seed
				snd, e1 := r.VarInt()
				if e1 != nil {
					return
				}
				o.sounds = append(o.sounds, snd)
			}
		}
		// dataGetLine would clear the sink and eat the add_entity
		// packets — harvest with take() until both eids land.
		deadline := time.Now().Add(5 * time.Second)
		for (fbEid == 0 || skEid == 0) && time.Now().Before(deadline) {
			for _, l := range sink.take() {
				collect(l)
			}
			if fbEid == 0 || skEid == 0 {
				time.Sleep(50 * time.Millisecond)
			}
		}
		if fbEid == 0 || skEid == 0 {
			conn.Close()
			t.Fatalf("%s: spawns missing (fb=%d sk=%d)", tg.tag, fbEid, skEid)
		}

		// Player at (0,-59,0) yaw 0 pitch 0 → look angle (0,0,1) —
		// the post-deflect delta both legs must emit exactly.
		_ = sendMovePosRotPitch(pw, 0, -59, 0, 0, 0)
		time.Sleep(150 * time.Millisecond)

		attack := func(eid int32) {
			var b protocol.Buffer
			b.VarInt(eid)
			_ = pw.WritePacket(server.IDPlayAttackSB, b.Bytes())
		}
		attack(fbEid)
		time.Sleep(120 * time.Millisecond)
		attack(skEid)
		// The deflect emit lands within a tracker tick; the drift
		// gates then keep emitting while |Δv| clears the floor —
		// ~2s covers several updateInterval-10 boundaries.
		time.Sleep(2000 * time.Millisecond)

		// Lane C: a breeze (the only deflects_projectiles member)
		// reverses an incoming fireball — hitTargetOrDeflectSelf
		// fires before onHit, so the fireball bounces instead of
		// exploding: delta *= -0.5 (z -0.5 -> +0.25), acceleration
		// power halves to 0.05, yaw flips ~180.
		sendCommand(t, pw, "summon minecraft:breeze 0 -59 4 {NoAI:1b}")
		sendCommand(t, pw, "summon minecraft:fireball 0 -59 9 {Motion:[0.0d,0.0d,-0.5d]}")
		deadline = time.Now().Add(5 * time.Second)
		for fb2Eid == 0 && time.Now().Before(deadline) {
			for _, l := range sink.take() {
				collect(l)
			}
			if fb2Eid == 0 {
				time.Sleep(50 * time.Millisecond)
			}
		}
		if fb2Eid == 0 {
			conn.Close()
			t.Fatalf("%s: lane-C fireball spawn missing", tg.tag)
		}
		// ~8 ticks to reach the breeze box plus drift-gate time.
		time.Sleep(2000 * time.Millisecond)
		for _, l := range sink.take() {
			collect(l)
		}
		perLeg[tg.tag] = o
		t.Logf("%s eids fb=%d sk=%d fb2=%d | fb motions=%v powers=%v | sk=%v | fb2 motions=%v powers=%v | sounds=%v removed=%v",
			tg.tag, fbEid, skEid, fb2Eid, o.fbMotions, o.fbPowers, o.skMotions, o.fb2Motions, o.fb2Powers, o.sounds, o.removed)

		// NBT tail: Owner [I;], LeftOwner/HasBeenShot bools on the
		// PUNCHED fireball — it outruns the reversed one, so
		// sort=furthest picks it deterministically. The skull's
		// Motion must still be zeroed.
		owner := dataGetLine(t, sink, pw, "@e[type=minecraft:fireball,limit=1,sort=furthest]", "Owner")
		shot := dataGetLine(t, sink, pw, "@e[type=minecraft:fireball,limit=1,sort=furthest]", "HasBeenShot")
		left := dataGetLine(t, sink, pw, "@e[type=minecraft:fireball,limit=1,sort=furthest]", "LeftOwner")
		motion := dataGetLine(t, sink, pw, "@e[type=minecraft:wither_skull,limit=1,sort=nearest]", "Motion")
		t.Logf("%s Owner=%q HasBeenShot=%q LeftOwner=%q skullMotion=%q", tg.tag, owner, shot, left, motion)
		// The [I;] reply renders [, I, ; as separate components —
		// compare the extracted int digits per leg instead; both
		// servers store the same offline-player uuid.
		intDigits := func(line string) []string {
			var out []string
			for m := 0; m+6 < len(line); m++ {
				if line[m:m+5] != "text\x00" {
					continue
				}
				j := m + 6
				start := j
				for j < len(line) && (line[j] == '-' || (line[j] >= '0' && line[j] <= '9')) {
					j++
				}
				if j-start >= 2 || (j-start == 1 && line[start] != '-') {
					out = append(out, line[start:j])
					m = j
				}
			}
			return out
		}
		ownerInts[tg.tag] = intDigits(owner)
		if len(ownerInts[tg.tag]) != 4 {
			t.Errorf("%s: fireball Owner not a 4-int uuid array (%q)", tg.tag, owner)
		}
		if !strings.Contains(shot, "text\x00\x011") || !strings.Contains(shot, "text\x00\x01b") {
			t.Errorf("%s: HasBeenShot not 1b (%q)", tg.tag, shot)
		}
		if !strings.Contains(left, "text\x00\x011") || !strings.Contains(left, "text\x00\x01b") {
			t.Errorf("%s: LeftOwner not 1b (%q)", tg.tag, left)
		}
		if strings.Contains(motion, "0.5") || strings.Contains(motion, "1.0d") {
			t.Errorf("%s: wither_skull was deflected (%q)", tg.tag, motion)
		}
		conn.Close()
	}

	mv, mg := perLeg["vanilla"], perLeg["mc-go"]

	// Owner uuid ints must be identical — same offline-player uuid
	// on both legs.
	if len(ownerInts["vanilla"]) == 4 && len(ownerInts["mc-go"]) == 4 {
		for i := 0; i < 4; i++ {
			if ownerInts["vanilla"][i] != ownerInts["mc-go"][i] {
				t.Errorf("Owner int %d: vanilla %s mc-go %s", i, ownerInts["vanilla"][i], ownerInts["mc-go"][i])
			}
		}
	}

	bad := false
	// The fireball's post-punch emits: the first is the deflect
	// delta itself — the attacker's look angle (0,0,1) — and every
	// later emit is a drift gate. The gates can land one tick
	// apart between legs (the punch is processed mid-tick against
	// each server's entity pass), so each leg's trajectory is
	// pinned against the analytic applyInertia sequence
	// v_n = 1.9 - 0.9·0.95ⁿ (delta unit + accel 0.1, inertia 0.95)
	// with strictly increasing step counts, and emit counts must
	// match.
	if len(mg.fbMotions) == 0 {
		t.Fatalf("mc-go emitted no fireball motion after the punch")
	}
	if len(mv.fbMotions) == 0 {
		t.Fatalf("vanilla emitted no fireball motion after the punch")
	}
	if math.Abs(mv.fbMotions[0].z-1.0) > 0.02 || math.Abs(mg.fbMotions[0].z-1.0) > 0.02 {
		t.Errorf("deflect delta not the look angle: vanilla %v mc-go %v", mv.fbMotions[0], mg.fbMotions[0])
		bad = true
	}
	if len(mg.fbPowers) == 0 || len(mv.fbPowers) == 0 {
		t.Errorf("missing projectile_power bundle (vanilla %d mc-go %d)", len(mv.fbPowers), len(mg.fbPowers))
		bad = true
	} else if math.Abs(mv.fbPowers[0]-0.1) > 1e-6 || math.Abs(mg.fbPowers[0]-0.1) > 1e-6 {
		t.Errorf("deflect power not reset to 0.1: vanilla %v mc-go %v", mv.fbPowers[0], mg.fbPowers[0])
		bad = true
	}
	// onCurve fits every emitted magnitude against the analytic
	// applyInertia sequence v_n = asym − (asym−v0)·0.95ⁿ where
	// asym = accel·19 (the fixed point of v += (unit·accel)·0.95);
	// each emit must match an n strictly past the previous hit —
	// drift gates may land one tick apart between legs.
	onCurve := func(samples []motionSample, tag string, v0, accel float64) {
		asym := accel * 19.0
		lastN := -1
		for i, s := range samples {
			mag := math.Sqrt(s.x*s.x + s.y*s.y + s.z*s.z)
			nHit := -1
			for n := lastN + 1; n < 200; n++ {
				v := asym - (asym-v0)*math.Pow(0.95, float64(n))
				if math.Abs(mag-v) < 0.02 {
					nHit = n
					break
				}
			}
			if nHit < 0 {
				t.Errorf("%s emit %d (%v) lies off the inertia curve", tag, i, s)
				bad = true
				return
			}
			lastN = nHit
		}
	}
	onCurve(mv.fbMotions, "vanilla", 1.0, 0.1)
	onCurve(mg.fbMotions, "mc-go", 1.0, 0.1)
	if len(mv.fbMotions) != len(mg.fbMotions) {
		t.Errorf("fireball emit count: vanilla %d mc-go %d", len(mv.fbMotions), len(mg.fbMotions))
		bad = true
	}
	if len(mv.skMotions) != len(mg.skMotions) {
		t.Errorf("wither_skull emit count: vanilla %d mc-go %d", len(mv.skMotions), len(mg.skMotions))
		bad = true
	}
	// NODAMAGE thud on the punch — one sound per attack, both legs.
	countSound := func(o *obs, id int32) int {
		c := 0
		for _, s := range o.sounds {
			if s == id {
				c++
			}
		}
		return c
	}
	if countSound(mv, 1305) != countSound(mg, 1305) {
		t.Errorf("nodamage sound count: vanilla %d mc-go %d", countSound(mv, 1305), countSound(mg, 1305))
		bad = true
	}

	// Lane C — the breeze reversal. Each leg's fb2 emit sequence
	// splits at the sign flip: approach on the −z curve (seed 0.5,
	// accel 0.1 → asymptote 1.9 magnitude) then post-reversal on
	// the +z curve (seed = 0.5·|approach at hit|, accel 0.05 →
	// asymptote 0.95). The reversal emit itself is the approach
	// delta × −0.5 — somewhere in +0.2..+0.6 — and the power
	// bundle halves to 0.05.
	revIdx := func(o *obs) int {
		for i, s := range o.fb2Motions {
			if s.z > 0.1 {
				return i
			}
		}
		return -1
	}
	checkLaneC := func(o *obs, tag string) int {
		rev := revIdx(o)
		if rev < 0 {
			t.Errorf("%s: fireball never reversed on the breeze (%v)", tag, o.fb2Motions)
			bad = true
			return -1
		}
		if o.fb2Motions[rev].z < 0.2 || o.fb2Motions[rev].z > 0.6 {
			t.Errorf("%s: reversal emit %v out of the ±0.5·approach bound", tag, o.fb2Motions[rev])
			bad = true
		}
		onCurve(o.fb2Motions[:rev], tag, 0.5, 0.1)
		onCurve(o.fb2Motions[rev:], tag, o.fb2Motions[rev].z, 0.05)
		half := false
		for _, p := range o.fb2Powers {
			if math.Abs(p-0.05) < 1e-6 {
				half = true
			}
		}
		if !half {
			t.Errorf("%s: no 0.05 power emit after breeze deflect (%v)", tag, o.fb2Powers)
			bad = true
		}
		return rev
	}
	rv := checkLaneC(mv, "vanilla")
	rg := checkLaneC(mg, "mc-go")
	if rv >= 0 && rg >= 0 && math.Abs(mv.fb2Motions[rv].z-mg.fb2Motions[rg].z) > 0.05 {
		t.Errorf("reversal emit diverged: vanilla %v mc-go %v", mv.fb2Motions[rv], mg.fb2Motions[rg])
		bad = true
	}

	if bad {
		t.FailNow()
	}
}
