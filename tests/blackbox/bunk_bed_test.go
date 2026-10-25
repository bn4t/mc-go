package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestBunkBedStandUpParity pins BedBlock's isBunkBed branch, measured
// off the 26.2 bytecode: a bed cell directly under the sleeping head
// swaps the stand-up scan — surround@head strict, surround@bunk-level
// strict, above@head strict, then the loose repeats — instead of the
// normal strict-all-then-loose-all. The observable split: with the
// upper bunk's own ring walled off, the sleeper walks down and wakes
// BESIDE THE LOWER BUNK on the ground; without the branch the search
// dead-ends onto the top bunk's cells (the above offsets) and the
// sleeper wakes on the bunk.
func TestBunkBedStandUpParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("BuA")}, nil)
	defer targets.cleanup()

	for _, s := range []struct{ label, addr string }{
		{"vanilla", targets.vanilla}, {"mcgo", targets.mcgo},
	} {
		cA, prA, pwA := joinPlayBlackbox(t, s.addr, "BuA")
		defer cA.Close()
		sendClientInformation(t, pwA)
		drainChunkWindow(t, s.label, cA, prA, pwA, 400*time.Millisecond)
		sinkA := newChatSinkTP(prA, pwA)
		time.Sleep(500 * time.Millisecond)

		posOf := func(name string) (x, y, z float64) {
			l := dataGetLine(t, sinkA, pwA, "@e[type=minecraft:player,name="+name+",limit=1]", "Pos")
			nums := sleepPosRe.FindAllString(l, -1)
			if len(nums) < 3 {
				t.Fatalf("%s: no %s Pos in %q", s.label, name, l)
			}
			fmt.Sscanf(nums[len(nums)-3], "%f", &x)
			fmt.Sscanf(nums[len(nums)-2], "%f", &y)
			fmt.Sscanf(nums[len(nums)-1], "%f", &z)
			return
		}

		// Lower bunk on the ground at (44,-60,44/45), upper bunk one
		// cell up — both south-facing (head +1z of foot).
		sendCommand(t, pwA, "setblock 44 -60 44 minecraft:red_bed[part=foot,facing=south]")
		sendCommand(t, pwA, "setblock 44 -60 45 minecraft:red_bed[part=head,facing=south]")
		sendCommand(t, pwA, "setblock 44 -59 44 minecraft:red_bed[part=foot,facing=south]")
		sendCommand(t, pwA, "setblock 44 -59 45 minecraft:red_bed[part=head,facing=south]")
		// Roof the bunk + its surround ring at head space (y=-58):
		// every upper-level candidate collides at head height, so
		// the vanilla scan must drop to the surround ring around
		// the LOWER bunk at y=-60. The two cells directly above
		// the bed itself stay air or vanilla reports bed_obstructed.
		sendCommand(t, pwA, "fill 43 -58 43 45 -58 46 minecraft:stone")
		sendCommand(t, pwA, "setblock 44 -58 44 minecraft:air")
		sendCommand(t, pwA, "setblock 44 -58 45 minecraft:air")
		// flat-world slimes would veto the sleep with bed.not_safe.
		sendCommand(t, pwA, "gamerule doMobSpawning false")
		sendCommand(t, pwA, "kill @e[type=!minecraft:player]")
		sendCommand(t, pwA, "time set 13000")
		sendCommand(t, pwA, "tp BuA 44.5 -60 42.5")
		// The SyncPos ack rides the sink's own handler; give it a
		// beat and verify the teleport landed before clicking — a
		// stale position makes the use_item_on a silent reach miss.
		_ = takePkts(sinkA, 700*time.Millisecond)
		if px, py, pz := posOf("BuA"); !near(px, 44.5) || !near(py, -60) || !near(pz, 42.5) {
			t.Fatalf("%s: tp didn't land, Pos (%.3f,%.3f,%.3f)", s.label, px, py, pz)
		}

		// Click the top face of the upper bunk's head.
		sendUseItemOn(t, pwA, 44, -59, 45, 1, 1)
		// Night skip fires once everyone is asleep (~5.1s); the wake
		// teleport lands inside it. Poll Pos until it settles off-bed.
		var wx, wy, wz float64
		deadline := time.Now().Add(12 * time.Second)
		for time.Now().Before(deadline) {
			wx, wy, wz = posOf("BuA")
			// Off the tp spot means the wake teleport landed; the
			// tp pos itself already satisfies the y test, and the
			// asleep pos is up on the bunk — only the stand-up
			// target clears both.
			if wy < -59.5 && !(near(wx, 44.5) && near(wz, 42.5)) {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		if !near(wx, 43.5) || !near(wy, -60) || !near(wz, 45.5) {
			sendCommand(t, pwA, "execute if block 44 -59 45 minecraft:red_bed[part=head] run say BEDHEAD_YES")
			sendCommand(t, pwA, "execute unless block 44 -59 45 minecraft:red_bed[part=head] run say BEDHEAD_NO")
			sendCommand(t, pwA, "execute if block 44 -60 45 minecraft:red_bed run say LOWERBED_YES")
			sendCommand(t, pwA, "execute if block 44 -59 43 minecraft:stone run say STONE59")
			sendCommand(t, pwA, "execute if block 44 -58 45 minecraft:air run say AIR58")
			time.Sleep(500 * time.Millisecond)
			for _, line := range sinkA.take() {
				if strings.Contains(line, "bed") || strings.Contains(line, "sleep") ||
					strings.Contains(line, "YES") || strings.Contains(line, "_NO") || strings.Contains(line, "AIR") || strings.Contains(line, "STONE") {
					t.Logf("%s: %s", s.label, line)
				}
			}
			t.Fatalf("%s: bunk wake Pos (%.3f,%.3f,%.3f), want beside lower bunk (43.5,-60,45.5)", s.label, wx, wy, wz)
		}
		// The expected pick: west-of-head on the LOWER bunk's ring —
		// (43,-60,45) → stand (43.5,-60,45.5) on the ground.
	}
}
