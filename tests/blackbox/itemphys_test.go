//go:build blackbox

package blackbox

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
)

// TestItemPhysParity pins Entity.move()'s tail for dropped items and XP
// orbs — the part of the physics vanilla runs inside move() plus the
// per-entity tick friction behind it: the collide, the
// restitution (slime_block's bounceRestitution 1.0 damped to 0.8 for
// non-living, honey_block's SUPPRESSES_BOUNCE tag, the -dy < gravity
// gate, the BOUNCE game event sculk hears at frequency 2), the block
// speed factor (honey_block/soul_sand 0.4 via the in-cell-then-below
// lookup of getBlockSpeedFactor), and the tick tail (delta *=
// (f3,airDrag,f3) for items, .scale(f) for orbs — f3/f = 0.98 *
// blockFriction when grounded, plus the item's grounded -0.5
// rest-bounce and the orb's -incoming*0.4 override).
func TestItemPhysParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	// probeVec reads a 3-double NBT list (Pos, Motion) off an entity via
	// /data get — signed values, so bounces read positive. The system
	// chat packet carries the component as binary NBT: the SNBT list
	// renders as a run of text-parts ("[" + "65.97"(gold) + "d"(red) +
	// "," + ... + "]"), so the parse pulls every "text" string inside
	// the SNBT's bracket run and rebuilds "[x,y,z]".
	probeVec := func(sink *chatSink, pw *protocol.Writer, addr, sel, path string) [3]float64 {
		line := dataGetLine(t, sink, pw, sel, path)
		var out [3]float64
		i := strings.LastIndex(line, "\x01[")
		j := -1
		if i >= 0 {
			if k := strings.Index(line[i:], "\x01]"); k >= 0 {
				j = i + k
			}
		}
		if i < 0 || j <= i+1 {
			t.Logf("%s probeVec %s %s raw: %q", addr, sel, path, line)
			return out
		}
		var snbt strings.Builder
		seg := line[i:j]
		for k := 0; k+10 < len(seg); k++ {
			// NBT string: \x08 \x00\x04 text \x00\x11 len utf8
			if seg[k] == '\x08' && seg[k+1] == '\x00' && seg[k+2] == '\x04' &&
				seg[k+3:k+7] == "text" {
				l := int(seg[k+7])<<8 | int(seg[k+8])
				if k+9+l <= len(seg) {
					snbt.WriteString(seg[k+9 : k+9+l])
					k += 8 + l
				}
			}
		}
		s := strings.Trim(snbt.String(), "[]")
		// The numbers render as bare `d`-suffixed runs ("14.5d-58.6d23.5d")
		// — the bracket and separator components are a different tag
		// shape this scan skips — so split on the suffix itself.
		parts := strings.Split(s, "d")
		for k, part := range parts {
			if k > 2 {
				break
			}
			v, _ := strconv.ParseFloat(strings.TrimSpace(part), 64)
			out[k] = v
		}
		return out
	}

	run := func(addr string) []string {
		cv, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer cv.Close()
		sink := newChatSinkTP(pr, pw)
		var out []string

		// blockProbe asks the target whether the exact block state is
		// live via execute if/unless block.
		blockProbe := func(spec string, i int) string {
			sendCommand(t, pw, fmt.Sprintf("execute if block %s run say ZIP-ok-%d", spec, i))
			sendCommand(t, pw, fmt.Sprintf("execute unless block %s run say ZIP-miss-%d", spec, i))
			sendCommand(t, pw, fmt.Sprintf("say ZIK%d", i))
			hit, marker := "none", fmt.Sprintf("ZIK%d", i)
			deadline := time.Now().Add(4 * time.Second)
			for time.Now().Before(deadline) {
				for _, line := range sink.take() {
					if strings.Contains(line, marker) {
						return hit
					}
					if strings.Contains(line, fmt.Sprintf("ZIP-ok-%d", i)) {
						hit = "hit"
					}
					if strings.Contains(line, fmt.Sprintf("ZIP-miss-%d", i)) {
						hit = "miss"
					}
				}
				time.Sleep(40 * time.Millisecond)
			}
			return hit
		}

		// Strip floors on the flat surface (top faces at y=-59): slide
		// strips on stone/honey/soul_sand and a bounce pad each on
		// slime and honey. Everything stays inside vanilla's spawn
		// chunk radius (~2 → ±48) or /fill errors on ungenerated
		// chunks. A sensor beside the slime pad hears the BOUNCE game
		// event at frequency 2.
		sendCommand(t, pw, "fill 8 -60 10 22 -60 12 minecraft:stone")
		sendCommand(t, pw, "fill 8 -60 14 22 -60 16 minecraft:honey_block")
		sendCommand(t, pw, "fill 8 -60 18 22 -60 20 minecraft:soul_sand")
		sendCommand(t, pw, "fill 8 -60 22 22 -60 24 minecraft:slime_block")
		sendCommand(t, pw, "fill 8 -60 26 22 -60 28 minecraft:honey_block")
		sendCommand(t, pw, "setblock 16 -60 25 minecraft:sculk_sensor")
		sendCommand(t, pw, "tp P 30 0 30 0 0")
		out = append(out, "floor stone: "+blockProbe("10 -60 11 minecraft:stone", 1))
		out = append(out, "floor honey: "+blockProbe("10 -60 15 minecraft:honey_block", 2))
		out = append(out, "floor soul: "+blockProbe("10 -60 19 minecraft:soul_sand", 3))
		out = append(out, "floor slime: "+blockProbe("10 -60 23 minecraft:slime_block", 4))
		out = append(out, "sensor: "+blockProbe("16 -60 25 minecraft:sculk_sensor", 5))

		item := "@e[type=minecraft:item,limit=1]"
		// Leg A — the slide: a 0.5x toss lands on the strip and decays.
		// Stone: friction 0.6 → ~0.588/tick plus the -0.5 rest-bounce
		// never fires (bounce==0 zeroes delta.y). Honey/soul_sand: the
		// speed factor eats another 0.4x every move tick, and their
		// collision tops sit at 15/16 and 14/16 respectively. Only the
		// settled position is probed — per-server tick phase makes
		// mid-flight comparisons meaningless.
		for _, c := range []struct {
			name string
			x, z float64
		}{
			{"stone slide", 9.5, 11.5},
			{"honey slide", 9.5, 15.5},
			{"soul slide", 9.5, 19.5},
		} {
			sendCommand(t, pw, "kill @e[type=minecraft:item]")
			sendCommand(t, pw, fmt.Sprintf(
				"summon minecraft:item %.1f -56.5 %.1f {Item:{id:\"minecraft:stone\",count:1},Motion:[0.5d,0.0d,0.0d]}",
				c.x, c.z))
			sprintTicks(t, pw, sink, 40)
			pos := probeVec(sink, pw, addr, item, "Pos")
			out = append(out, fmt.Sprintf("%s: x%.2f y%.2f", c.name, pos[0], pos[1]))
		}

		// Leg B — the bounce: a straight drop from 5.5 up. Slime flips
		// the incoming delta to ~(ratio*g - dy)*lerp(ratio,1,0.98)*0.8
		// (~0.44 up, apex ~2.4 high); honey suppresses the bounce
		// entirely. Tick phases skew by a tick or two between servers,
		// so instead of fixed-tick motion reads the bounce is pinned by
		// a y-band entity predicate: the first arc's apex re-enters the
		// band only if the restitution fired at near-parity strength —
		// and the band is only sampled after the initial fall has
		// passed it, so the drop itself can't trip it.
		here := func(q string) string {
			sendCommand(t, pw, fmt.Sprintf("execute if entity @e[%s] run say ZHIT", q))
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				for _, line := range sink.take() {
					if strings.Contains(line, "ZHIT") {
						return "hit"
					}
				}
				time.Sleep(40 * time.Millisecond)
			}
			return "none"
		}
		for _, c := range []struct {
			name string
			z    float64
			apex float64 // y-band center just under the first bounce apex
		}{
			{"slime", 23.5, -58.5},
			{"honey drop", 27.5, -58.5},
			{"stone drop", 11.5, -58.5},
		} {
			sendCommand(t, pw, "kill @e[type=minecraft:item]")
			sendCommand(t, pw, fmt.Sprintf(
				`summon minecraft:item 14.5 -53.5 %.1f {Item:{id:"minecraft:stone",count:1}}`, c.z))
			sprintTicks(t, pw, sink, 14) // past the initial fall
			apexHit := "none"
			for i := 0; i < 18 && apexHit != "hit"; i++ {
				sprintTicks(t, pw, sink, 1)
				apexHit = here(fmt.Sprintf(
					"type=minecraft:item,x=14.5,y=%.2f,z=%.1f,distance=..0.65", c.apex, c.z))
			}
			sprintTicks(t, pw, sink, 60)
			p := probeVec(sink, pw, addr, item, "Pos")
			out = append(out, fmt.Sprintf("%s: apex %s rest y%.2f", c.name, apexHit, p[1]))
		}

		// Leg C — the orb: its own -incoming*0.4 bounce on any surface
		// (apex ~0.66 over the slime), slime's restitution also applies
		// inside move() first. P must stay >8 away or the orb homes —
		// but close enough that vanilla keeps the pad inside its
		// entity-ticking/simulation distance: park it ~30 blocks out or
		// the summoned orb freezes mid-air and then goes unselectable.
		sendCommand(t, pw, "tp P 45 -60 23.5")
		sendCommand(t, pw, `summon minecraft:experience_orb 14.5 -53.5 23.5 {Value:5}`)
		out = append(out, "orb exists: "+here("type=minecraft:experience_orb"))
		sprintTicks(t, pw, sink, 14)
		orbHigh := "none"
		for i := 0; i < 18 && orbHigh != "hit"; i++ {
			sprintTicks(t, pw, sink, 1)
			// The band spans every transit and apex a slime bounce can
			// reach: a narrow band misses fast transits when the two
			// servers' real-time loops sample mid-flight at different
			// phases.
			orbHigh = here("type=minecraft:experience_orb,x=14.5,y=-57.5,z=23.5,distance=..1.9")
		}
		out = append(out, "orb bounced: "+orbHigh)
		sprintTicks(t, pw, sink, 60)
		out = append(out, "orb rest: "+here("type=minecraft:experience_orb,x=14.5,y=-58.6,z=23.5,distance=..0.7"))

		// Leg D — the game event: every landed bounce posts BOUNCE at
		// frequency 2. The orb's own slime bounces keep the sensor
		// cycling, so kill it and let the sensor finish its
		// active→cooldown window (30+10 ticks) cold first — then one
		// fresh drop re-fires it deterministically.
		sendCommand(t, pw, "kill @e[type=minecraft:experience_orb]")
		sprintTicks(t, pw, sink, 45)
		sendCommand(t, pw, "tp P 30 0 30")
		sendCommand(t, pw, "kill @e[type=minecraft:item]")
		sendCommand(t, pw, `summon minecraft:item 14.5 -53.5 23.5 {Item:{id:"minecraft:stone",count:1}}`)
		sprintTicks(t, pw, sink, 30)
		out = append(out, "bounce sensor: "+blockProbe("16 -60 25 minecraft:sculk_sensor[sculk_sensor_phase=active]", 0))

		return out
	}

	vOut := run(targets.vanilla)
	mOut := run(targets.mcgo)
	if len(vOut) != len(mOut) {
		t.Fatalf("probe count mismatch vanilla=%d mcgo=%d", len(vOut), len(mOut))
	}
	for i := range vOut {
		if vOut[i] != mOut[i] {
			t.Errorf("probe %d: vanilla=%q mcgo=%q", i, vOut[i], mOut[i])
		}
	}
	if t.Failed() {
		for i := range vOut {
			t.Logf("all %02d: vanilla=%q mcgo=%q", i, vOut[i], mOut[i])
		}
	}
}
