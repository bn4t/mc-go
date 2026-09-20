package blackbox

import (
	"net"
	"strconv"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// csObs is one scripted swing's wire-visible aftermath: attack sounds
// both clients observe, the level_particles emitted, the victim's
// health metadata, animate actions (vanilla's crit marker), which
// entities took a damage event, and entity motion.
type csObs struct {
	sounds    []int32 // sound_event registry ids (attack subset)
	particles []int32 // particle registry ids
	animates  [][2]int32
	damaged   []int32
	health    map[int32]float32
	motion    map[int32][3]float64
}

// drainAttack collects the post-swing packets on one conn: attack
// sounds (holder-encoded ids, so the wire value is the registry id + 1),
// level particles, animate actions, damage events, health metadata, and
// entity motion.
func drainAttack(t *testing.T, c net.Conn, pr *protocol.Reader, d time.Duration, ph *csObs) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(time.Now().Add(time.Until(deadline)))
		p, err := pr.ReadPacket()
		if err != nil {
			return
		}
		switch p.ID {
		case 0x75, 0x74: // sound, sound_entity: Holder<SoundEvent> = id+1
			r := protocol.NewPacketReader(p.Data)
			if id, err := r.VarInt(); err == nil {
				ph.sounds = append(ph.sounds, id-1)
			}
		case 0x2f: // level_particles
			r := protocol.NewPacketReader(p.Data)
			if _, err := r.Byte(); err != nil { // overrideLimiter
				continue
			}
			if _, err := r.Byte(); err != nil { // alwaysShow
				continue
			}
			ok := true
			for i := 0; i < 3 && ok; i++ {
				_, err := r.Double()
				ok = err == nil
			}
			for i := 0; i < 3 && ok; i++ {
				_, err := r.Float()
				ok = err == nil
			}
			if _, err := r.Float(); err != nil { // maxSpeed
				ok = false
			}
			if _, err := r.Int(); err != nil { // count
				ok = false
			}
			if id, err := r.VarInt(); ok && err == nil {
				ph.particles = append(ph.particles, id)
			}
		case server.IDPlayEntityMetadata:
			r := protocol.NewPacketReader(p.Data)
			eid, err := r.VarInt()
			if err != nil {
				continue
			}
			idx, err := r.Byte()
			if err != nil || idx != 9 {
				continue
			}
			ty, _ := r.VarInt()
			if ty != 3 {
				continue
			}
			if v, err := r.Float(); err == nil {
				if ph.health == nil {
					ph.health = map[int32]float32{}
				}
				ph.health[eid] = v
			}
		case server.IDPlayEntityMotion:
			r := protocol.NewPacketReader(p.Data)
			eid, err := r.VarInt()
			if err != nil {
				continue
			}
			x, y, z, _ := decodeLpVec3(r)
			if ph.motion == nil {
				ph.motion = map[int32][3]float64{}
			}
			ph.motion[eid] = [3]float64{x, y, z}
		case server.IDPlayEntityAnimation: // animate: VarInt eid + byte action
			r := protocol.NewPacketReader(p.Data)
			eid, err1 := r.VarInt()
			act, err2 := r.Byte()
			if err1 == nil && err2 == nil {
				ph.animates = append(ph.animates, [2]int32{eid, int32(act)})
			}
		case 0x19: // damage_event
			r := protocol.NewPacketReader(p.Data)
			if eid, err := r.VarInt(); err == nil {
				ph.damaged = append(ph.damaged, eid)
			}
		}
	}
}

// runCritSweepScenario scripts the attack variants that change the
// sound/particle/damage shape on one server: sweep (charged standing
// sword hit with a neighbor pig), crit (falling hit), weak (uncharged),
// sprint-knockback, and strong (charged fist hit, no sword so no
// sweep). Each variant lands on a fresh NoAI pig so health deltas stay
// exact.
func runCritSweepScenario(t *testing.T, addr, victim, attacker string) []csObs {
	t.Helper()
	ca, pra, pwa := joinPlayBlackbox(t, addr, attacker)
	defer ca.Close()
	cv, prv, pwv := joinPlayBlackbox(t, addr, victim)
	defer cv.Close()
	_ = ca.SetDeadline(time.Now().Add(120 * time.Second))
	_ = cv.SetDeadline(time.Now().Add(120 * time.Second))
	var posA, posV [3]float64
	obsA, obsV := wearObs{}, wearObs{}

	drainWear(t, cv, prv, pwv, &posV, 1200*time.Millisecond, &obsV, nil)
	spA := map[int32]int32{}
	drainWear(t, ca, pra, pwa, &posA, 1200*time.Millisecond, &obsA, spA)
	var attackerEID int32
	for eid, sp := range obsV.spawnedPos {
		if int32(sp[0]) == server.EntityTypePlayer {
			attackerEID = eid
		}
	}

	sendCommand(t, pwv, "tp @s 20 -60 20 0 0")
	sendCommand(t, pwa, "tp @s 4 -60 2 -90 0")
	drainWear(t, cv, prv, pwv, &posV, 800*time.Millisecond, &obsV, nil)
	drainWear(t, ca, pra, pwa, &posA, 800*time.Millisecond, &obsA, nil)
	// Ground the attacker: a real client confirms the teleport with a
	// position packet, and onGround gates the crit/sweep branches.
	movePos := func(x, y, z float64, ground bool) {
		var b protocol.Buffer
		b.Double(x)
		b.Double(y)
		b.Double(z)
		if ground {
			b.Byte(0x01)
		} else {
			b.Byte(0x00)
		}
		if err := pwa.WritePacket(server.IDPlayMovePosSB, b.Bytes()); err != nil {
			t.Fatal(err)
		}
	}
	movePos(4.5, -60, 2.5, true)

	attack := func(eid int32) {
		var atk protocol.Buffer
		atk.VarInt(eid)
		if err := pwa.WritePacket(server.IDPlayAttackSB, atk.Bytes()); err != nil {
			t.Fatal(err)
		}
	}
	sprint := func(action int32) {
		var b protocol.Buffer
		b.VarInt(attackerEID)
		b.VarInt(action)
		b.VarInt(0)
		if err := pwa.WritePacket(server.IDPlayPlayerCommandSB, b.Bytes()); err != nil {
			t.Fatal(err)
		}
	}
	equip := func(item string) {
		sendCommand(t, pwa, "item replace entity @s weapon.mainhand with "+item)
	}
	pigType, _ := server.EntityTypeID("pig")
	pigIdx := 0
	// freshPig summons a NoAI pig at (x, -60, z) and returns its
	// entity id. NoAI keeps it airborne + stationary so it takes
	// knockback as a projectile and never wanders.
	freshPig := func(x, z float64) int32 {
		pigIdx++
		sendCommand(t, pwa, "summon minecraft:pig "+ftoa(x)+" -60 "+ftoa(z)+" {NoAI:1b}")
		sp := map[int32]int32{}
		drainWear(t, ca, pra, pwa, &posA, 900*time.Millisecond, &obsA, sp)
		eid := sp[pigType]
		if eid == 0 {
			t.Fatalf("%s: pig %d never spawned", addr, pigIdx)
		}
		return eid
	}
	swing := func(eid int32) csObs {
		attack(eid)
		po := csObs{}
		drainAttack(t, ca, pra, 700*time.Millisecond, &po)
		drainAttack(t, cv, prv, 150*time.Millisecond, &csObs{})
		return po
	}

	phases := []csObs{}

	// sweep: charged standing sword hit; the neighbor pig sits inside
	// the victim's bbox inflated by (1, 0.25, 1) AND inside 3 blocks of
	// the attacker — vanilla's two sweep gates.
	equip("minecraft:stone_sword")
	time.Sleep(400 * time.Millisecond)
	victimPig := freshPig(7, 2)
	freshPig(6, 3) // neighbor: inside the box and 3 blocks
	phases = append(phases, swing(victimPig))
	time.Sleep(600 * time.Millisecond)

	// crit: rise then descend so fallDistance > 0 at contact; the hit
	// lands while still airborne (landing would reset the distance).
	critPig := freshPig(5, 3)
	movePos(4.5, -59.3, 2.5, false)
	time.Sleep(120 * time.Millisecond)
	movePos(4.5, -59.7, 2.5, false)
	time.Sleep(120 * time.Millisecond)
	phases = append(phases, swing(critPig))
	movePos(4.5, -60, 2.5, true)
	time.Sleep(600 * time.Millisecond)

	// weak: the second swing lands mid-recharge on a fresh pig (a
	// re-hit on the same pig would die to the invulnerability window
	// before weak damage is ever possible).
	weakA := freshPig(6, 4)
	weakB := freshPig(5, 4)
	time.Sleep(400 * time.Millisecond)
	attack(weakA)
	obs0 := csObs{}
	drainAttack(t, ca, pra, 280*time.Millisecond, &obs0) // opener aftermath drains here
	attack(weakB)
	po := csObs{}
	drainAttack(t, ca, pra, 700*time.Millisecond, &po)
	drainAttack(t, cv, prv, 150*time.Millisecond, &csObs{})
	phases = append(phases, po)
	time.Sleep(700 * time.Millisecond)

	// sprint knockback: sprint flag set, charged sword hit — vanilla
	// stops the swing being a sweep when sprinting.
	sprint(1)
	time.Sleep(150 * time.Millisecond)
	phases = append(phases, swing(freshPig(7, 3)))
	sprint(2)
	time.Sleep(600 * time.Millisecond)

	// strong: charged fist hit while standing — not a sword, so the
	// strong sound plays instead of a sweep.
	equip("minecraft:air")
	time.Sleep(700 * time.Millisecond)
	phases = append(phases, swing(freshPig(5, 3.4)))
	return phases
}

// attackSounds filters a drained sound list down to the
// entity.player.attack.* range (1302-1307) — the pig's own hurt/death
// sounds are entity-bound and ride a different packet.
func attackSounds(in []int32) []int32 {
	var out []int32
	for _, id := range in {
		if id >= 1302 && id <= 1307 {
			out = append(out, id)
		}
	}
	return out
}

// TestCritSweepParity compares the attack-variant wire trace: which
// attack sound each variant plays, the sweep/damage-indicator
// particles, the crit animate, the damage events, the health deltas,
// and the neighbor pig's sweep damage.
func TestCritSweepParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{
		"gamemode": "survival",
	}, map[string]string{
		"ops.json": opsJSONFor("CAtt", "CVic"),
	}, func(s *server.Server) { s.GameMode = server.GameModeSurvival })
	defer tg.cleanup()

	want := runCritSweepScenario(t, tg.vanilla, "CVic", "CAtt")
	got := runCritSweepScenario(t, tg.mcgo, "CVic", "CAtt")
	if len(want) != len(got) {
		t.Fatalf("phase count: vanilla %d mc-go %d", len(want), len(got))
	}
	names := []string{"sweep", "crit", "weak", "sprint", "strong"}
	for i := range want {
		w, g := want[i], got[i]
		ws, gs := attackSounds(w.sounds), attackSounds(g.sounds)
		t.Logf("%s vanilla sounds=%v particles=%v animates=%v damaged=%v health=%v motion=%v",
			names[i], ws, w.particles, w.animates, w.damaged, w.health, w.motion)
		t.Logf("%s mc-go   sounds=%v particles=%v animates=%v damaged=%v health=%v motion=%v",
			names[i], gs, g.particles, g.animates, g.damaged, g.health, g.motion)
		if !sameI32(ws, gs) {
			t.Errorf("%s: attack sounds vanilla %v mc-go %v", names[i], ws, gs)
		}
		if !sameI32(w.particles, g.particles) {
			t.Errorf("%s: particles vanilla %v mc-go %v", names[i], w.particles, g.particles)
		}
		wActs := make([]int32, len(w.animates))
		for i, a := range w.animates {
			wActs[i] = a[1]
		}
		gActs := make([]int32, len(g.animates))
		for i, a := range g.animates {
			gActs[i] = a[1]
		}
		if !sameI32(wActs, gActs) {
			t.Errorf("%s: animate actions vanilla %v mc-go %v", names[i], wActs, gActs)
		}
		if len(w.damaged) != len(g.damaged) {
			t.Errorf("%s: damage events vanilla %v mc-go %v", names[i], w.damaged, g.damaged)
		}
		// Entity ids differ across the two servers, so compare health
		// values as multisets rather than by eid key.
		if len(w.health) != len(g.health) {
			t.Errorf("%s: health update count vanilla %v mc-go %v", names[i], w.health, g.health)
		}
		if names[i] == "weak" {
			// The weak swing lands mid-recharge — the dealt amount is
			// quantized by which 50ms tick boundary each server crossed
			// inside the scripted gap, so assert a partial hit on both
			// sides rather than an identical health value.
			for _, wh := range w.health {
				if wh >= 10 || wh <= 5 {
					t.Errorf("weak: vanilla health %v not a partial hit", wh)
				}
			}
			for _, gh := range g.health {
				if gh >= 10 || gh <= 5 {
					t.Errorf("weak: mc-go health %v not a partial hit", gh)
				}
			}
			continue
		}
		for _, wh := range w.health {
			found := false
			for _, gh := range g.health {
				if abs32(wh-gh) < 0.01 {
					found = true
				}
			}
			if !found {
				t.Errorf("%s: health value %v absent from mc-go %v", names[i], wh, g.health)
			}
		}
	}
}

func sameI32(a, b []int32) bool {
	if len(a) != len(b) {
		return false
	}
	am := map[int32]int{}
	for _, v := range a {
		am[v]++
	}
	for _, v := range b {
		am[v]--
	}
	for _, c := range am {
		if c != 0 {
			return false
		}
	}
	return true
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

func ftoa(v float64) string {
	i := int64(v)
	if float64(i) == v {
		return itoa(int32(i))
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}
