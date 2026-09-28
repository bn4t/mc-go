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
		fbMotions []motionSample
		fbPowers  []float64
		skMotions []motionSample
		removed   bool
		sounds    []int32 // PlaySound registry ids (soundID+1 on the wire)
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
		sendClientInformation(t, pw)
		sendCommand(t, pw, "tp @s 0 -59 0 0 0")
		// dataGetLine drains mc-go's synchronous command queue —
		// the sendWindow stall would otherwise burst the setup.
		dataGetLine(t, sink, pw, "@s", "Pos")

		// Clean corridor + entity sweep so no stale mob or block
		// eats the fireball mid-flight.
		sendCommand(t, pw, "fill -6 -61 0 40 -56 4 minecraft:air")
		sendCommand(t, pw, "kill @e[type=!minecraft:player,x=-8,y=-64,z=-2,dx=50,dy=10,dz=8]")
		dataGetLine(t, sink, pw, "@s", "Pos")

		// Still fireball in reach at z=2; a second skull lane at
		// x=-2 proves the non-redirectable branch.
		sendCommand(t, pw, "summon minecraft:fireball 0 -59 2 {Motion:[0.0d,0.0d,0.0d]}")
		sendCommand(t, pw, "summon minecraft:wither_skull -2 -59 2 {Motion:[0.0d,0.0d,0.0d]}")

		fbType, _ := server.EntityTypeID("fireball")
		skType, _ := server.EntityTypeID("wither_skull")
		var fbEid, skEid int32
		// dataGetLine would clear the sink and eat the add_entity
		// packets — harvest with take() until both eids land.
		deadline := time.Now().Add(5 * time.Second)
		for (fbEid == 0 || skEid == 0) && time.Now().Before(deadline) {
			for _, l := range sink.take() {
				pref, rest, ok := strings.Cut(l, ":")
				if !ok || pref != "0x1" {
					continue
				}
				r := protocol.NewPacketReader([]byte(rest))
				id, e1 := r.VarInt()
				_, e2 := r.Read(16)
				typ, e3 := r.VarInt()
				if e1 != nil || e2 != nil || e3 != nil {
					continue
				}
				if typ == fbType {
					fbEid = id
				}
				if typ == skType {
					skEid = id
				}
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

		o := &obs{}
		for _, l := range sink.take() {
			pref, rest, ok := strings.Cut(l, ":")
			if !ok {
				continue
			}
			r := protocol.NewPacketReader([]byte(rest))
			switch pref {
			case "0x65": // set_entity_motion: id + LpVec3
				id, e1 := r.VarInt()
				mx, my, mz, _ := decodeLpVec3(r)
				if e1 != nil {
					continue
				}
				if id == fbEid {
					o.fbMotions = append(o.fbMotions, motionSample{mx, my, mz})
				}
				if id == skEid {
					o.skMotions = append(o.skMotions, motionSample{mx, my, mz})
				}
			case "0x87": // projectile_power: id + double power
				id, e1 := r.VarInt()
				pw2, e2 := r.Double()
				if e1 != nil || e2 != nil {
					continue
				}
				if id == fbEid {
					o.fbPowers = append(o.fbPowers, pw2)
				}
			case "0x4d": // remove_entities
				for {
					id, e1 := r.VarInt()
					if e1 != nil {
						break
					}
					if id == fbEid || id == skEid {
						o.removed = true
					}
				}
			case "0x75": // sound: VarInt id(+1), source, x*8, y*8, z*8, vol, pitch, seed
				snd, e1 := r.VarInt()
				if e1 != nil {
					continue
				}
				o.sounds = append(o.sounds, snd)
			}
		}
		perLeg[tg.tag] = o

		// NBT tail: Owner [I;], LeftOwner/HasBeenShot bools on the
		// fireball; the skull's Motion must still be zeroed.
		owner := dataGetLine(t, sink, pw, "@e[type=minecraft:fireball,limit=1,sort=nearest]", "Owner")
		shot := dataGetLine(t, sink, pw, "@e[type=minecraft:fireball,limit=1,sort=nearest]", "HasBeenShot")
		left := dataGetLine(t, sink, pw, "@e[type=minecraft:fireball,limit=1,sort=nearest]", "LeftOwner")
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
	onCurve := func(samples []motionSample, tag string) {
		lastN := -1
		for i, s := range samples {
			if i == 0 {
				continue // the look-angle emit is not on the curve
			}
			mag := math.Sqrt(s.x*s.x + s.y*s.y + s.z*s.z)
			nHit := -1
			for n := lastN + 1; n < 200; n++ {
				v := 1.9 - 0.9*math.Pow(0.95, float64(n))
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
	onCurve(mv.fbMotions, "vanilla")
	onCurve(mg.fbMotions, "mc-go")
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
	if bad {
		t.FailNow()
	}
}
