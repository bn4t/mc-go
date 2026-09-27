package blackbox

import (
	"regexp"
	"strconv"
	"testing"
	"time"

	"mc-go/internal/protocol"
)

var flopNumRe = regexp.MustCompile(`-?\d+(?:\.\d+)?`)

// flopQueryNum runs `data get entity <sel> <path>` and returns the
// last number in the data.entity.query reply.
func flopQueryNum(t *testing.T, sink *chatSink, pw *protocol.Writer, sel, path string) (float64, bool) {
	t.Helper()
	l := dataGetLine(t, sink, pw, sel, path)
	if l == "" {
		return 0, false
	}
	ms := flopNumRe.FindAllString(l, -1)
	if len(ms) == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(ms[len(ms)-1], 64)
	return v, err == nil
}

// flopHop samples Pos[1] of sel until it rises >0.3 above base or the
// deadline passes, and reports the apex.
func flopHop(t *testing.T, sink *chatSink, pw *protocol.Writer, sel string, base, budget float64) float64 {
	t.Helper()
	apex := base
	deadline := time.Now().Add(time.Duration(budget) * time.Second)
	for time.Now().Before(deadline) {
		if y, ok := flopQueryNum(t, sink, pw, sel, "Pos[1]"); ok && y > apex {
			apex = y
		}
		if apex-base > 0.3 {
			break
		}
		time.Sleep(120 * time.Millisecond)
	}
	return apex
}

// TestGuardianFlopParity pins Guardian.aiStep's land flop, measured off
// the 26.2 jar: out of water and on the ground the guardian hops every
// tick — delta += (rand·2−1)·0.4, 0.5, (rand·2−1)·0.4, a random yaw,
// grounded cleared — and in water the air resets to 300 every tick.
// The NBT pins: Pos[1] reaches >0.3 above the floor on some sample,
// Motion has a nonzero component while airborne, and Rotation[0]
// jitters with the random yaw. The elder guardian flops identically.
// The flop fires under NoAI too (isEffectiveAi gates only the goal
// chain), so the land leg uses a plain summon placed well outside
// follow_range to keep the beam out of the run.
func TestGuardianFlopParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("FlA")}, nil)
	defer targets.cleanup()

	for _, s := range []struct{ label, addr string }{
		{"vanilla", targets.vanilla}, {"mcgo", targets.mcgo},
	} {
		cA, prA, pwA := joinPlayBlackbox(t, s.addr, "FlA")
		defer cA.Close()
		sendClientInformation(t, pwA)
		sinkA := newChatSinkTP(prA, pwA)
		time.Sleep(600 * time.Millisecond)

		// Land leg: guardians on dry flat ground ~45 blocks out — far
		// outside follow_range (16), so no beam acquires the player.
		sendCommand(t, pwA, "summon minecraft:guardian 40.5 -60 40.5")
		sendCommand(t, pwA, "summon minecraft:elder_guardian 43.5 -60 40.5")
		gSel := "@e[type=minecraft:guardian,limit=1]"
		eSel := "@e[type=minecraft:elder_guardian,limit=1]"
		time.Sleep(700 * time.Millisecond)

		// The floor tops at -60; the first Pos read may already catch
		// the guardian mid-arc, so the hop assert is absolute.
		ok := false
		for try := 0; try < 8; try++ {
			if _, ok2 := flopQueryNum(t, sinkA, pwA, gSel, "Pos[1]"); ok2 {
				ok = true
				break
			}
			time.Sleep(400 * time.Millisecond)
		}
		if !ok {
			t.Fatalf("%s: guardian Pos[1] never answered", s.label)
		}
		yaw0, _ := flopQueryNum(t, sinkA, pwA, gSel, "Rotation[0]")

		var apex = -60.0
		motionSeen := false
		yawChanged := false
		deadline := time.Now().Add(6 * time.Second)
		for time.Now().Before(deadline) {
			if y, ok2 := flopQueryNum(t, sinkA, pwA, gSel, "Pos[1]"); ok2 && y > apex {
				apex = y
			}
			if my, ok2 := flopQueryNum(t, sinkA, pwA, gSel, "Motion[1]"); ok2 && my != 0 {
				motionSeen = true
			}
			if mx, ok2 := flopQueryNum(t, sinkA, pwA, gSel, "Motion[0]"); ok2 && mx != 0 {
				motionSeen = true
			}
			if yaw, ok2 := flopQueryNum(t, sinkA, pwA, gSel, "Rotation[0]"); ok2 && yaw != yaw0 {
				yawChanged = true
			}
			if apex > -59.7 && motionSeen && yawChanged {
				break
			}
			time.Sleep(120 * time.Millisecond)
		}
		if apex <= -59.7 {
			t.Fatalf("%s: guardian never hopped — apex %v, floor -60", s.label, apex)
		}
		if !motionSeen {
			t.Fatalf("%s: guardian Motion stayed zero through the hop", s.label)
		}
		if !yawChanged {
			t.Fatalf("%s: guardian yaw never randomized on flop", s.label)
		}

		// The elder flops identically. The base may already be sampled
		// mid-arc, so the assert is absolute: the floor tops at -60.
		if _, ok := flopQueryNum(t, sinkA, pwA, eSel, "Pos[1]"); !ok {
			t.Fatalf("%s: elder guardian Pos[1] never answered", s.label)
		}
		if eApex := flopHop(t, sinkA, pwA, eSel, -60.0, 5); eApex <= -59.7 {
			t.Fatalf("%s: elder guardian never hopped — apex %v, floor -60", s.label, eApex)
		}

		// Water leg: a guardian submerged keeps air pinned at the 300
		// max every tick (Guardian.aiStep's water arm). Kill the land
		// pair first so limit=1 can't resolve to either of them.
		sendCommand(t, pwA, "kill "+gSel)
		sendCommand(t, pwA, "kill "+eSel)
		sendCommand(t, pwA, "setblock 47 -60 40 water")
		sendCommand(t, pwA, "summon minecraft:guardian 47.5 -60 40.5")
		wSel := "@e[type=minecraft:guardian,limit=1]"
		time.Sleep(700 * time.Millisecond)
		air, ok := flopQueryNum(t, sinkA, pwA, wSel, "Air")
		if !ok || air != 300 {
			t.Fatalf("%s: water guardian Air = %v, want 300", s.label, air)
		}
	}
}
