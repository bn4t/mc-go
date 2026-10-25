package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestHorseRollAttrParity: a bare /summon of the equine family runs
// AbstractHorse.finalizeSpawn's randomizeAttributes — Horse rolls all
// three stats, the chested family only max_health, skeleton_horse only
// jump_strength, zombie_horse jump_strength + movement_speed on its own
// formulas, and camel (not an AbstractHorse) nothing. The attribute
// save pack then carries exactly the rolled set plus Mob.finalizeSpawn's
// follow_range (random_spawn_bonus), and a rolled max_health resets the
// spawn Health to match. Vanilla formulas (26.2 javap):
//
//	generateMaxHealth        = 15 + nextInt(8) + nextInt(9)  -> [15,30]
//	generateJumpStrength     = 0.4 + 0.2·(r1+r2+r3)          -> [0.4,1.0]
//	generateSpeed            = (0.45 + 0.3·(r1+r2+r3))·0.25  -> [0.1125,0.3375]
//	generateZombieHorseJump  = 0.5 + (r1+r2+r3)/15           -> [0.5,0.7]
//	generateZombieHorseSpeed = (9 + r1+r2+r3)/42.16          -> [0.2135,0.2847]
func TestHorseRollAttrParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("HR")}, nil)
	defer targets.cleanup()

	type spec struct {
		typ    string
		rolled map[string][2]float64 // attr name -> [lo, hi]
	}
	specs := []spec{
		{"horse", map[string][2]float64{
			"minecraft:max_health":     {15, 30},
			"minecraft:jump_strength":  {0.4, 1.0},
			"minecraft:movement_speed": {0.1125, 0.3375},
		}},
		{"donkey", map[string][2]float64{
			"minecraft:max_health": {15, 30},
		}},
		{"mule", map[string][2]float64{
			"minecraft:max_health": {15, 30},
		}},
		{"llama", map[string][2]float64{
			"minecraft:max_health": {15, 30},
		}},
		{"trader_llama", map[string][2]float64{
			"minecraft:max_health": {15, 30},
		}},
		{"skeleton_horse", map[string][2]float64{
			"minecraft:jump_strength": {0.4, 1.0},
		}},
		{"zombie_horse", map[string][2]float64{
			"minecraft:jump_strength":  {0.5, 0.7},
			"minecraft:movement_speed": {0.2135, 0.2847},
		}},
		{"camel", map[string][2]float64{}},
	}

	for _, s := range []struct{ label, addr string }{
		{"vanilla", targets.vanilla}, {"mcgo", targets.mcgo},
	} {
		t.Run(s.label, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, s.addr, "HR")
			defer c.Close()
			sink := newChatSink(pr, pw)
			sink.cap = 1 << 20
			time.Sleep(400 * time.Millisecond)
			sendCommand(t, pw, "gamerule do_mob_spawning false")
			sendCommand(t, pw, "tick freeze")
			sendCommand(t, pw, "kill @e[type=!minecraft:player]")
			sink.take()
			x := 0.5
			for _, sp := range specs {
				sendCommand(t, pw, "summon minecraft:"+sp.typ+" "+f3(x)+" -60 "+f3(x+10))
				// Verify the summon landed before reading — vanilla
				// drops summons silently at a low rate.
				for attempt := 0; attempt < 5; attempt++ {
					time.Sleep(400 * time.Millisecond)
					line := dataGetLine(t, sink, pw,
						"@e[type=minecraft:"+sp.typ+",limit=1]", "")
					v, ok := parseSNBT(line)
					if !ok {
						sendCommand(t, pw, "summon minecraft:"+sp.typ+" "+f3(x)+" -60 "+f3(x+10))
						continue
					}
					m, ok := v.(map[string]any)
					if !ok {
						sendCommand(t, pw, "summon minecraft:"+sp.typ+" "+f3(x)+" -60 "+f3(x+10))
						continue
					}
					attrs := map[string]float64{}
					if l, ok := m["attributes"]; ok {
						for _, it := range hrList(l) {
							am, ok := it.(map[string]any)
							if !ok {
								continue
							}
							id, _ := am["id"].(string)
							if b, ok := hrNum(am["base"]); ok {
								attrs[id] = b
							}
						}
					}
					// Every rolled attribute is in the pack (finalizeSpawn
					// touched it) inside its measured band.
					for attr, band := range sp.rolled {
						v, ok := attrs[attr]
						if !ok {
							t.Errorf("%s %s: attributes pack lacks %s (have %v)",
								s.label, sp.typ, attr, attrs)
							continue
						}
						if v < band[0]-1e-6 || v > band[1]+1e-6 {
							t.Errorf("%s %s: %s=%v, want [%v,%v]",
								s.label, sp.typ, attr, v, band[0], band[1])
						}
					}
					// Nothing else rolls — the pack holds only the rolled
					// set plus follow_range's random_spawn_bonus.
					for id := range attrs {
						if _, ok := sp.rolled[id]; ok {
							continue
						}
						if id != "minecraft:follow_range" {
							t.Errorf("%s %s: unexpected attribute %s=%v in pack",
								s.label, sp.typ, id, attrs[id])
						}
					}
					// A rolled max_health carries Health = MaxHealth.
					if _, ok := sp.rolled["minecraft:max_health"]; ok {
						health, _ := hrNum(m["Health"])
						if health != attrs["minecraft:max_health"] {
							t.Errorf("%s %s: Health=%v != rolled max_health %v",
								s.label, sp.typ, health, attrs["minecraft:max_health"])
						}
					}
					break
				}
				x += 4
			}
		})
	}
}

// f3 prints a float for command args.
func f3(v float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", v), "0"), ".")
}

// hrList unwraps an SNBT list value.
func hrList(v any) []any {
	l, ok := v.([]any)
	if !ok {
		return nil
	}
	return l
}

// hrNum unwraps an SNBT number (the snbtReader emits float64).
func hrNum(v any) (float64, bool) {
	n, ok := v.(float64)
	return n, ok
}
