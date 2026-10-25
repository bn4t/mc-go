//go:build blackbox

// Mob ground-move parity: vanilla's LivingEntity.travelInAir gives a
// walking mob getFrictionInfluencedSpeed — on blocks whose friction
// exceeds 0.6 the navigation input shrinks to speed*0.216/f³ (slime
// ≈0.42×, blue_ice ≈0.23×) while soul_sand's 0.4 speedFactor only
// drains carried momentum, not the recomputed input — so a tempting
// mob walks at full speed across soul sand. The grounded delta tail
// is blockFriction*0.91 horizontal and 0.98 vertical, not a flat
// decay.

package blackbox

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMobGroundParity(t *testing.T) {
	// The test lives on command packets — fills, replaces, summons,
	// sprints and ~40 data-gets — which would trip vanilla's
	// command-spam throttler (20 per packet, decays 1/tick, kicks at
	// 200). Threshold 0 disables it (TickThrottler.isUnderThreshold is
	// always true when threshold <= 0).
	targets := requireBlackboxFiles(t,
		map[string]string{"command-spam-threshold-seconds": "0"},
		map[string]string{"ops.json": opsJSONFor("P")}, nil)

	type probeResult struct {
		ss  float64 // mean advance per 3-tick sprint over the last probes
		mot float64 // terminal stored horizontal delta
		alt float64 // terminal feet y (-999 when unprobed)
	}
	run := func(addr string) ([]string, map[string]probeResult) {
		cv, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer cv.Close()
		sink := newChatSinkTP(pr, pw)
		var out []string
		results := map[string]probeResult{}

		cmd := func(c string) { sendCommand(t, pw, c) }

		// mobPos reads a scalar NBT path (Pos[0]) off the nearest match
		// via /data get. The system chat reply is binary NBT: the value
		// renders as text-parts inside the component, so pull every
		// "text" string out of the packet and rebuild the number.
		mobPos := func(sel, path string) float64 {
			line := dataGetLine(t, sink, pw, "@e["+sel+",sort=nearest,limit=1]", path)
			if line == "" {
				return -999
			}
			var snbt strings.Builder
			for k := 0; k+10 < len(line); k++ {
				if line[k] == '\x08' && line[k+1] == '\x00' && line[k+2] == '\x04' &&
					line[k+3:k+7] == "text" {
					l := int(line[k+7])<<8 | int(line[k+8])
					if k+9+l <= len(line) {
						snbt.WriteString(line[k+9 : k+9+l])
						k += 9 + l - 1
					}
				}
			}
			// The rebuild holds the whole reply text — entity name
			// parts included — so pull the last SNBT-typed number
			// (digit run ending in a d/f/b/s/l suffix) out of it; a
			// bare trailing number would catch the mob's id or the
			// selector coords, not the value.
			s := snbt.String()
			isNum := func(c byte) bool {
				return c >= '0' && c <= '9' || c == '.' || c == '-' || c == '+' || c == 'e' || c == 'E'
			}
			v := -999.0
			for i := 0; i < len(s); i++ {
				c := s[i]
				if c != 'd' && c != 'f' && c != 'b' && c != 's' && c != 'l' && c != 'D' && c != 'F' {
					continue
				}
				end := i
				start := i
				for start-1 >= 0 && isNum(s[start-1]) {
					start--
				}
				if start < end {
					if parsed, err := strconv.ParseFloat(s[start:end], 64); err == nil {
						v = parsed
					}
				}
			}
			return v
		}

		cmd("item replace entity P hotbar.0 with minecraft:wheat")
		cmd("tp P 24.5 -59 16")
		// The tp streams a fresh chunk batch; the sink pump acks each
		// batch as it lands — give the window a beat to finish so the
		// throttle isn't holding the later replies.
		time.Sleep(400 * time.Millisecond)
		// Frozen + explicit sprint ticks: wall-clock drift between
		// probes can't smuggle extra ticks into the measurements.
		cmd("tick freeze")
		defer cmd("tick unfreeze")

		// The selector sorts by distance to the spawn point (x/y/z
		// args recenter it) so a lingering pre-kill cow near the
		// executor never out-sorts the fresh one.
		sel := "type=minecraft:cow,x=15.5,y=-59,z=16"

		for _, c := range []struct {
			name  string
			block string
		}{
			{"stone", "minecraft:stone"},
			{"slime", "minecraft:slime_block"},
			{"ice", "minecraft:ice"},
			{"soul", "minecraft:soul_sand"},
			{"honey", "minecraft:honey_block"},
			{"blueice", "minecraft:blue_ice"},
		} {
			// The whole lane floor is the material under test — a pad
			// inside a walkable moat lets the pathfinder cut the corner
			// off-material and the probe reads stone speed either way.
			cmd("fill 4 -59 8 28 -55 24 minecraft:air")
			cmd(fmt.Sprintf("fill 4 -60 8 28 -60 24 %s", c.block))
			cmd("kill @e[type=minecraft:cow]")
			cmd("summon minecraft:cow 15.5 -59 16 {NoAI:0b}")
			sprintTicks(t, pw, sink, 3)
			// Gate on the fresh cow: command processing lags a few
			// ticks behind the chat write, so the first probes would
			// otherwise read the previous leg's mob. A fresh cow sits
			// within ~2 of the summon point until tempt carries it.
			for i := 0; i < 30; i++ {
				if v := mobPos(sel, "Pos[0]"); v > 15.0 && v < 17.0 {
					break
				}
				sprintTicks(t, pw, sink, 1)
			}
			sprintTicks(t, pw, sink, 2)
			// Probe every sprint batch: |ΔPos| and |Motion| on the
			// horizontal plane — vanilla's navigation curves (the ice
			// leg even walks backward at first), so axis components
			// alone can't measure walk speed; magnitudes can.
			px := make([]float64, 0, 8)
			pz := make([]float64, 0, 8)
			mx := make([]float64, 0, 8)
			mz := make([]float64, 0, 8)
			var alt []float64
			// slime and honey legs also read Pos[1] — bounce and sink
			// live on the vertical axis.
			altAxis := ""
			switch c.name {
			case "slime", "honey":
				altAxis = "Pos[1]"
			}
			for i := 0; i < 8; i++ {
				sprintTicks(t, pw, sink, 2)
				px = append(px, mobPos(sel, "Pos[0]"))
				pz = append(pz, mobPos(sel, "Pos[2]"))
				mx = append(mx, mobPos(sel, "Motion[0]"))
				mz = append(mz, mobPos(sel, "Motion[2]"))
				if altAxis != "" {
					alt = append(alt, mobPos(sel, altAxis))
				}
			}
			spd := make([]float64, 0, 7)
			for i := 1; i < 8; i++ {
				dx := px[i] - px[i-1]
				dz := pz[i] - pz[i-1]
				spd = append(spd, math.Hypot(dx, dz))
			}
			mag := make([]float64, 0, 8)
			for i := 0; i < 8; i++ {
				mag = append(mag, math.Hypot(mx[i], mz[i]))
			}
			out = append(out, fmt.Sprintf("%s spd: %.2f %.2f %.2f %.2f %.2f %.2f %.2f", c.name,
				spd[0], spd[1], spd[2], spd[3], spd[4], spd[5], spd[6]))
			out = append(out, fmt.Sprintf("%s mot: %.2f %.2f %.2f %.2f %.2f %.2f %.2f %.2f", c.name,
				mag[0], mag[1], mag[2], mag[3], mag[4], mag[5], mag[6], mag[7]))
			out = append(out, fmt.Sprintf("%s pos: %.2f %.2f %.2f %.2f %.2f %.2f %.2f %.2f", c.name,
				px[0], px[1], px[2], px[3], px[4], px[5], px[6], px[7]))
			altVal := -999.0
			if altAxis != "" {
				altVal = alt[7]
				out = append(out, fmt.Sprintf("%s alt: %.2f %.2f %.2f %.2f %.2f %.2f %.2f %.2f", c.name,
					alt[0], alt[1], alt[2], alt[3], alt[4], alt[5], alt[6], alt[7]))
			}
			// Steady-state magnitudes: the mean advance over the last
			// three probes (per 3-tick sprint) and the terminal stored
			// delta. The first probes can't be pinned — each summon
			// snaps to a random yaw and MOVE_TO rotlerps the body at
			// ≤90°/tick, so early legs curve differently every run.
			results[c.name] = probeResult{
				ss:  (spd[4] + spd[5] + spd[6]) / 3,
				mot: mag[7],
				alt: altVal,
			}
		}
		cmd("kill @e[type=minecraft:cow]")
		return out, results
	}

	type leg struct {
		ssLo, ssHi float64 // mean-advance band per 3-tick sprint
		motLo      float64 // minimum terminal stored delta
		alt        float64 // expected feet y (-999 skips the check)
	}
	// Model values from the measured chain: ss advance = input/(1-T)
	// with input = zza·f (zza = 1.25·0.2 = 0.25 tempting drive),
	// T = sf·friction·0.91 (·stepOn scale on slime). Vanilla's random
	// spawn yaw stretches the ramp, so the band sits a touch low.
	legs := map[string]leg{
		"stone":   {0.30, 0.45, 0.06, -999},
		"slime":   {0.08, 0.16, 0.008, -59.0},
		"ice":     {0.25, 0.45, 0.08, -999},
		"soul":    {0.18, 0.30, 0.012, -999},
		"honey":   {0.18, 0.30, 0.012, -59.06},
		"blueice": {0.25, 0.45, 0.09, -999},
	}

	t.Log("=== vanilla ===")
	vOut, vRes := run(targets.vanilla)
	for i, l := range vOut {
		t.Logf("vanilla %02d: %s", i, l)
	}
	t.Log("=== mcgo ===")
	mOut, mRes := run(targets.mcgo)
	for i, l := range mOut {
		t.Logf("mcgo    %02d: %s", i, l)
	}
	for side, res := range map[string]map[string]probeResult{"vanilla": vRes, "mcgo": mRes} {
		for name, want := range legs {
			r, ok := res[name]
			if !ok {
				t.Errorf("%s %s: no probes", side, name)
				continue
			}
			if r.ss < want.ssLo || r.ss > want.ssHi {
				t.Errorf("%s %s: steady advance %.3f/3t outside [%.2f,%.2f]", side, name, r.ss, want.ssLo, want.ssHi)
			}
			if r.mot < want.motLo {
				t.Errorf("%s %s: terminal |Motion| %.3f below %.3f — the delta chain isn't carrying momentum", side, name, r.mot, want.motLo)
			}
			if want.alt > -900 && (r.alt < want.alt-0.05 || r.alt > want.alt+0.05) {
				t.Errorf("%s %s: feet at %.3f, want %.3f±0.05", side, name, r.alt, want.alt)
			}
		}
	}
}
