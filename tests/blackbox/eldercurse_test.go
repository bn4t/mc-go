package blackbox

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// ElderGuardian.customServerAiStep: when (tickCount+entityId)%1200 hits
// zero — every 60 s — each survival-mode player inside 50 blocks gets
// Mining Fatigue III (amplifier 2, 6000 ticks) pushed as
// update_mob_effect, plus the GUARDIAN_ELDER_EFFECT game event (type
// 10, param 1.0 — 0.0 only when the elder is silent). The elder sits
// ~20 blocks out: inside the curse radius but past follow_range 16 so
// the attack beam never arms during the wait.
func TestElderCurseParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	type result struct {
		event  bool
		param  float32
		effect bool
		amp    int
		dur    int
	}
	fatigueID, _ := server.MobEffectID("mining_fatigue")

	run := func(addr string) result {
		var r result
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(120 * time.Second))
		obs := &fleeObs{}

		sendCommand(t, pw, "gamemode creative P")
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		sendCommand(t, pw, "tp P 0.5 -60 0.5")
		fleePump(t, c, pr, pw, obs, 600*time.Millisecond)
		sendCommand(t, pw, "summon minecraft:elder_guardian 20.5 -59 0.5")
		sendCommand(t, pw, "gamemode survival P")

		start := time.Now()
		for time.Since(start) < 72*time.Second {
			_ = c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
			p, err := pr.ReadPacket()
			if err != nil {
				continue
			}
			switch p.ID {
			case server.IDPlayKeepAliveCB:
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
			case server.IDPlaySyncPos:
				rd := protocol.NewPacketReader(p.Data)
				if id, err := rd.VarInt(); err == nil {
					var b protocol.Buffer
					b.VarInt(id)
					_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
				}
			case server.IDPlayGameEvent:
				rd := protocol.NewPacketReader(p.Data)
				typ, _ := rd.Byte()
				v, _ := rd.Float()
				if typ == 10 && !r.event {
					r.event = true
					r.param = v
				}
			case server.IDPlayUpdateMobEffect:
				rd := protocol.NewPacketReader(p.Data)
				rd.VarInt() // entity id
				eff, _ := rd.VarInt()
				amp, _ := rd.Byte()
				dur, _ := rd.VarInt()
				if eff == fatigueID && !r.effect {
					r.effect = true
					r.amp = int(amp)
					r.dur = int(dur)
				}
			}
			if r.event && r.effect {
				break
			}
		}
		return r
	}

	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	t.Logf("van %+v", van)
	t.Logf("mc  %+v", mc)
	for name, r := range map[string]result{"vanilla": van, "mc-go": mc} {
		if !r.event {
			t.Errorf("%s: no GUARDIAN_ELDER_EFFECT game event within 72s", name)
		} else if r.param < 0.9 || r.param > 1.1 {
			t.Errorf("%s: curse event param %v, want 1.0", name, r.param)
		}
		if !r.effect {
			t.Errorf("%s: no mining_fatigue update_mob_effect within 72s", name)
		} else {
			if r.amp != 2 {
				t.Errorf("%s: mining_fatigue amplifier %d, want 2", name, r.amp)
			}
			if r.dur < 5900 || r.dur > 6000 {
				t.Errorf("%s: mining_fatigue duration %d, want 6000", name, r.dur)
			}
		}
	}
}

// ElderGuardian.customServerAiStep's tail: an elder without a home
// plants it at its block position with radius 16 on the first tick —
// hasHome latches, so a summon carrying the home pair keeps its own
// values. Observed via data get on the save keys home_radius/home_pos
// (Mob.addAdditionalSaveData emits them only while hasHome).
func TestElderHomeParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("Cap2")}, nil)
	defer targets.cleanup()

	// Vanilla's flattened data.entity.query reply drops every
	// punctuation component, so an IntArray would come back as an
	// unparseable digit run — ask for each element by index
	// (home_pos[0]) and read the trailing int instead.
	trailIntRe := regexp.MustCompile(`(-?\d+)\s*$`)

	run := func(addr string) (autoPos, taggedPos [3]int, autoR, taggedR int, ok bool) {
		obs, pump, pw, done := patrolLeg(t, addr)
		defer done()
		patrolSetup(t, pw, pump)
		sendCommand(t, pw, "gamemode creative Cap2")
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		sendCommand(t, pw, "tp Cap2 0.5 -60 0.5")
		pump(600 * time.Millisecond)
		// The plain summon gets the auto-planted home on its first
		// customServerAiStep; the tagged one must keep its NBT.
		// NoGravity keeps y at -59 so the planted block pos is
		// deterministic whichever way gravity and the ai step order.
		sendCommand(t, pw, "summon minecraft:elder_guardian 20.5 -59 0.5 {NoGravity:1b}")
		sendCommand(t, pw, "summon minecraft:elder_guardian 30.5 -59 0.5 {NoGravity:1b,home_radius:5,home_pos:[I;9,10,11]}")
		pump(1500 * time.Millisecond) // ≥1 tick of customServerAiStep

		getInt := func(sel, path string) (int, bool) {
			start := len(obs.chat)
			sendCommand(t, pw, "data get entity "+sel+" "+path)
			sendCommand(t, pw, "say ZEND")
			deadline := time.Now().Add(8 * time.Second)
			idx := start
			for time.Now().Before(deadline) {
				pump(300 * time.Millisecond)
				end := false
				for ; idx < len(obs.chat); idx++ {
					line := obs.chat[idx]
					if strings.Contains(line, "data.entity.query") {
						if m := trailIntRe.FindStringSubmatch(line); m != nil {
							v, _ := strconv.Atoi(m[1])
							return v, true
						}
					}
					if strings.Contains(line, "ZEND") {
						end = true
					}
				}
				if end {
					break
				}
			}
			return 0, false
		}

		getPos := func(radius int) ([3]int, bool) {
			var out [3]int
			sel := "@e[type=minecraft:elder_guardian,nbt={home_radius:" + strconv.Itoa(radius) + "},limit=1]"
			for i := 0; i < 3; i++ {
				v, ok := getInt(sel, "home_pos["+strconv.Itoa(i)+"]")
				if !ok {
					return out, false
				}
				out[i] = v
			}
			return out, true
		}

		autoSel := "@e[type=minecraft:elder_guardian,nbt={home_radius:16},limit=1]"
		taggedSel := "@e[type=minecraft:elder_guardian,nbt={home_radius:5},limit=1]"
		var okAuto, okTag bool
		autoPos, okAuto = getPos(16)
		taggedPos, okTag = getPos(5)
		autoR, _ = getInt(autoSel, "home_radius")
		taggedR, _ = getInt(taggedSel, "home_radius")
		return autoPos, taggedPos, autoR, taggedR, okAuto && okTag
	}

	vanAuto, vanTagged, vanR, vanTR, vanOK := run(targets.vanilla)
	mcAuto, mcTagged, mcR, mcTR, mcOK := run(targets.mcgo)
	t.Logf("van auto=%v r=%d tagged=%v r=%d ok=%v", vanAuto, vanR, vanTagged, vanTR, vanOK)
	t.Logf("mc  auto=%v r=%d tagged=%v r=%d ok=%v", mcAuto, mcR, mcTagged, mcTR, mcOK)
	for name, res := range map[string]struct {
		auto, tagged [3]int
		r, tr        int
		ok           bool
	}{"vanilla": {vanAuto, vanTagged, vanR, vanTR, vanOK},
		"mc-go": {mcAuto, mcTagged, mcR, mcTR, mcOK}} {
		if !res.ok {
			t.Errorf("%s: home_pos data get returned no IntArray reply", name)
			continue
		}
		if res.auto != [3]int{20, -59, 0} {
			t.Errorf("%s: auto home_pos %v, want [20 -59 0] (summon block pos)", name, res.auto)
		}
		if res.tagged != [3]int{9, 10, 11} {
			t.Errorf("%s: tagged home_pos %v, want [9 10 11] (NBT latched)", name, res.tagged)
		}
		if res.r != 16 || res.tr != 5 {
			t.Errorf("%s: home_radius auto=%d tagged=%d, want 16/5", name, res.r, res.tr)
		}
	}
}
