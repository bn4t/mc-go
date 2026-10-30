// Inside-block effect parity (26.2), measured off the jar:
//   - PowderSnowBlock.entityInside: runBefore EXTINGUISH melts the
//     snow when a burning entity steps in (mob_griefing-gated for
//     mobs), FREEZE sets isInPowderSnow and accrues TicksFrozen by
//     one toward the 140-tick requirement (freeze_immune_entity_types
//     skip the accrual), EXTINGUISH clears the burn.
//   - BaseFireBlock.entityInside: CLEAR_FREEZE + FIRE_IGNITE (the
//     eight-second ignite) + the runAfter in_fire hurt — 1.0 on fire,
//     2.0 on soul_fire.
//   - LavaFluid / LavaCauldronBlock.entityInside: CLEAR_FREEZE +
//     LAVA_IGNITE (15s) + lavaHurt (4.0, fireImmune-gated).
//   - LayeredCauldronBlock.entityInside (water/powder-snow cauldron):
//     EXTINGUISH (+ level drop on a doused burn — not modeled).
//   - WaterFluid.entityInside: EXTINGUISH.
//
// LivingEntity.baseTick: out-of-snow / freeze-immune entities thaw at
// 2/tick; a fully-frozen freezable mob takes freeze damage every 40
// ticks (freeze_hurts_extra_types ×5). Entity.baseTick: the on_fire
// countdown skips its burn hit while isInLava.
package blackbox

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
)

// The chat-text SNBT dump arrives delimiter-stripped ("TicksFrozen140",
// "Fire160s", "Health5.0f") — keys pair directly with their value and a
// type suffix, so match key + numeric run rather than key:value.
func snbtNum(t *testing.T, dump, key string) (string, bool) {
	t.Helper()
	re := regexp.MustCompile(regexp.QuoteMeta(key) + `(-?[0-9]+(?:\.[0-9]+)?)[bsilfdBSILFD]?`)
	m := re.FindStringSubmatch(dump)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// snbtInt pulls a numeric NBT value out of a /data get dump.
func snbtInt(t *testing.T, dump, key string) (int64, bool) {
	t.Helper()
	s, ok := snbtNum(t, dump, key)
	if !ok {
		return 0, false
	}
	v, err := strconv.ParseInt(strings.TrimSuffix(s, ".0"), 10, 64)
	return v, err == nil
}

// snbtFloat pulls a float NBT value (Health is stored as a float).
func snbtFloat(t *testing.T, dump, key string) (float64, bool) {
	t.Helper()
	s, ok := snbtNum(t, dump, key)
	if !ok {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil
}

func hasBlockCond(t *testing.T, sink *chatSink, pw *protocol.Writer, x, y, z int, name string) bool {
	t.Helper()
	sendCommand(t, pw, fmt.Sprintf("execute if block %d %d %d minecraft:%s", x, y, z, name))
	sendCommand(t, pw, "say ZEND")
	deadline := time.Now().Add(5 * time.Second)
	out := ""
	for time.Now().Before(deadline) {
		for _, line := range sink.take() {
			out += line
			if strings.Contains(line, "ZEND") {
				break
			}
		}
		if strings.Contains(out, "ZEND") {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	return strings.Contains(out, "conditional.pass")
}

func TestInsideEffectsParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"gamemode": "creative", "view-distance": "2", "simulation-distance": "2"},
		map[string]string{"ops.json": opsJSONFor("IE")}, nil)
	defer targets.cleanup()

	type leg struct {
		name    string
		setup   []string // block placement before the summon
		summon  string
		steps   int
		pick    string // entity selector for /data get
		intKeys []string
		fltKeys []string
		blockAt [4]int // x,y,z and air-check flag for the melt leg
	}
	legs := []leg{
		{
			name: "powder_snow freezes",
			setup: []string{
				"setblock 10 -61 10 minecraft:stone",
				"setblock 10 -60 10 minecraft:powder_snow",
			},
			summon:  `summon minecraft:pig 10.5 -60 10.5 {NoAI:1b}`,
			steps:   150,
			pick:    "@e[type=minecraft:pig,x=10.5,y=-60,z=10.5,distance=..1.5,limit=1]",
			intKeys: []string{"TicksFrozen"},
		},
		{
			name: "freeze-immune stray",
			setup: []string{
				"setblock 12 -61 10 minecraft:stone",
				"setblock 12 -60 10 minecraft:powder_snow",
			},
			summon:  `summon minecraft:stray 12.5 -60 10.5 {NoAI:1b}`,
			steps:   150,
			pick:    "@e[type=minecraft:stray,x=12.5,y=-60,z=10.5,distance=..1.5,limit=1]",
			intKeys: []string{"TicksFrozen"},
		},
		{
			name: "fire ignites and hurts",
			setup: []string{
				"setblock 14 -61 10 minecraft:netherrack",
				"setblock 14 -60 10 minecraft:fire",
			},
			summon:  `summon minecraft:pig 14.5 -60 10.5 {NoAI:1b}`,
			steps:   20,
			pick:    "@e[type=minecraft:pig,x=14.5,y=-60,z=10.5,distance=..1.5,limit=1]",
			intKeys: []string{"Fire"},
			fltKeys: []string{"Health"},
		},
		{
			name: "soul fire hurts double",
			setup: []string{
				"setblock 16 -61 10 minecraft:soul_soil",
				"setblock 16 -60 10 minecraft:soul_fire",
			},
			summon:  `summon minecraft:pig 16.5 -60 10.5 {NoAI:1b}`,
			steps:   10,
			pick:    "@e[type=minecraft:pig,x=16.5,y=-60,z=10.5,distance=..1.5,limit=1]",
			intKeys: []string{"Fire"},
			fltKeys: []string{"Health"},
		},
		{
			name: "lava ignites and hurts",
			setup: []string{
				"setblock 18 -61 10 minecraft:stone",
				"setblock 18 -60 10 minecraft:lava_cauldron",
			},
			summon:  `summon minecraft:pig 18.5 -60 10.5 {NoAI:1b}`,
			steps:   1,
			pick:    "@e[type=minecraft:pig,x=18.5,y=-60,z=10.5,distance=..1.5,limit=1]",
			intKeys: []string{"Fire"},
			fltKeys: []string{"Health"},
		},
		{
			name: "fire-immune blaze shrugs lava",
			setup: []string{
				"setblock 20 -61 10 minecraft:stone",
				"setblock 20 -60 10 minecraft:lava",
			},
			summon:  `summon minecraft:blaze 20.5 -60 10.5 {NoAI:1b}`,
			steps:   15,
			pick:    "@e[type=minecraft:blaze,x=20.5,y=-60,z=10.5,distance=..1.5,limit=1]",
			intKeys: []string{"Fire"},
			fltKeys: []string{"Health"},
		},
		{
			name: "water cauldron extinguishes",
			setup: []string{
				"setblock 22 -61 10 minecraft:stone",
				"setblock 22 -60 10 minecraft:water_cauldron[level=3]",
			},
			summon:  `summon minecraft:pig 22.5 -60 10.5 {NoAI:1b,Fire:200s}`,
			steps:   20,
			pick:    "@e[type=minecraft:pig,x=22.5,y=-60,z=10.5,distance=..1.5,limit=1]",
			intKeys: []string{"Fire"},
			fltKeys: []string{"Health"},
		},
		{
			name: "burning mob melts snow",
			setup: []string{
				"setblock 24 -61 10 minecraft:stone",
				"setblock 24 -60 10 minecraft:powder_snow",
			},
			summon:  `summon minecraft:pig 24.5 -60 10.5 {NoAI:1b,Fire:200s}`,
			steps:   20,
			pick:    "@e[type=minecraft:pig,x=24.5,y=-60,z=10.5,distance=..1.5,limit=1]",
			intKeys: []string{"Fire", "TicksFrozen"},
			blockAt: [4]int{24, -60, 10, 1},
		},
	}

	results := map[string]map[string]map[string]string{}
	blockCheck := map[string]bool{}

	for _, s := range []struct{ label, addr string }{
		{"vanilla", targets.vanilla}, {"mcgo", targets.mcgo},
	} {
		t.Run(s.label, func(t *testing.T) {
			ca, pra, pwA, _ := joinPlayEid(t, s.addr, "IE")
			defer ca.Close()
			awaitPlayReadyBlackbox(t, s.addr, ca, pra, pwA)
			sink := newChatSinkTP(pra, pwA)
			sink.cap = 1 << 20
			collect := func(marker string) []string {
				var lines []string
				deadline := time.Now().Add(15 * time.Second)
				for !structureSaidBB(lines, marker) && time.Now().Before(deadline) {
					lines = append(lines, sink.take()...)
					time.Sleep(10 * time.Millisecond)
				}
				if !structureSaidBB(lines, marker) {
					t.Fatal("inside receipt absent", marker)
				}
				return lines
			}
			expect := func(condition, marker string) {
				sendCommand(t, pwA, condition+" run say "+marker)
				sendCommand(t, pwA, "say "+marker+"END")
				if !structureSaidBB(collect(marker+"END"), marker) {
					t.Fatal("inside setup rejected", marker)
				}
			}
			dumpEntity := func(selector, marker string) string {
				sendCommand(t, pwA, "data get entity "+selector)
				sendCommand(t, pwA, "say "+marker)
				for _, line := range collect(marker) {
					if strings.HasPrefix(line, "0x79:") && strings.Contains(line, "commands.data.entity.query") {
						return snbtFromChat(line)
					}
				}
				return ""
			}
			for _, cmd := range []string{"gamemode creative @s", "gamerule minecraft:spawn_mobs false", "tick freeze", "scoreboard objectives add IEStart dummy", "scoreboard objectives add IEGoal dummy", "scoreboard objectives add IENow dummy"} {
				sendCommand(t, pwA, cmd)
			}
			sendCommand(t, pwA, "say IEREADY")
			collect("IEREADY")
			sendCommand(t, pwA, "tp IE 0.5 -60.0 0.5")
			sendCommand(t, pwA, "time set noon")
			sendCommand(t, pwA, "weather clear")
			results[s.label] = map[string]map[string]string{}

			for legIndex, lg := range legs {
				marker := fmt.Sprintf("IELEG%d", legIndex)
				for blockIndex, cmd := range lg.setup {
					sendCommand(t, pwA, cmd)
					parts := strings.Fields(cmd)
					expect("execute if block "+strings.Join(parts[1:5], " "), fmt.Sprintf("%sBLOCK%d", marker, blockIndex))
				}
				sendCommand(t, pwA, lg.summon)
				expect("execute if entity "+lg.pick, marker+"SPAWN")
				sendCommand(t, pwA, "execute store result score @s IEStart run time query gametime")
				sendCommand(t, pwA, "scoreboard players operation @s IEGoal = @s IEStart")
				sendCommand(t, pwA, fmt.Sprintf("scoreboard players add @s IEGoal %d", lg.steps))
				sendCommand(t, pwA, fmt.Sprintf("tick step %d", lg.steps))
				complete := false
				deadline := time.Now().Add(25 * time.Second)
				for !complete && time.Now().Before(deadline) {
					sendCommand(t, pwA, "execute store result score @s IENow run time query gametime")
					sendCommand(t, pwA, "execute if score @s IENow = @s IEGoal run say "+marker+"DONE")
					sendCommand(t, pwA, "say "+marker+"POLL")
					complete = structureSaidBB(collect(marker+"POLL"), marker+"DONE")
				}
				if !complete {
					t.Fatal("inside world steps incomplete", lg.name)
				}
				dump := dumpEntity(lg.pick, marker+"DUMP")
				t.Logf("%s %s: %s", s.label, lg.name, dump)

				row := map[string]string{}
				for _, k := range lg.intKeys {
					if v, ok := snbtInt(t, dump, k); ok {
						row[k] = strconv.FormatInt(v, 10)
					} else {
						row[k] = "<absent>"
					}
				}
				for _, k := range lg.fltKeys {
					if v, ok := snbtFloat(t, dump, k); ok {
						row[k] = strconv.FormatFloat(v, 'f', 2, 64)
					} else {
						row[k] = "<absent>"
					}
				}
				results[s.label][lg.name] = row

				if lg.blockAt[3] == 1 {
					sendCommand(t, pwA, fmt.Sprintf("execute if block %d %d %d minecraft:air run say %sMELTED", lg.blockAt[0], lg.blockAt[1], lg.blockAt[2], marker))
					sendCommand(t, pwA, "say "+marker+"MELTEND")
					blockCheck[s.label] = structureSaidBB(collect(marker+"MELTEND"), marker+"MELTED")
				}
				// Sweep the leg's entity so legs don't pollute each other.
				sendCommand(t, pwA, "kill "+lg.pick)
				sendCommand(t, pwA, "say "+marker+"RETIRED")
				collect(marker + "RETIRED")
			}

			// Corpse-selector leg: kill a pig, then /data get inside the
			// 20-tick corpse window — vanilla's @e never matches a dying
			// entity even before removal (measured on 26.2).
			pick := "@e[type=minecraft:pig,x=26.5,y=-60,z=10.5,distance=..2,limit=1]"
			sendCommand(t, pwA, "setblock 26 -61 10 minecraft:stone")
			sendCommand(t, pwA, "summon minecraft:pig 26.5 -60 10.5 {NoAI:1b}")
			expect("execute if entity "+pick, "IECORPSESPAWN")
			sendCommand(t, pwA, "kill "+pick)
			deadDump := dumpEntity(pick, "IECORPSEDUMP")
			t.Logf("%s corpse-selector: %s", s.label, deadDump)
			// A dead entity matches in vanilla only if the selector still
			// sees it — record whether any dump came back at all.
			results[s.label]["corpse selectable"] = map[string]string{
				"matched": map[bool]string{true: "yes", false: "no"}[deadDump != ""],
			}
		})
	}

	for _, lg := range legs {
		vrow := results["vanilla"][lg.name]
		mrow := results["mcgo"][lg.name]
		for k, vv := range vrow {
			if mv := mrow[k]; mv != vv {
				t.Errorf("%s %s: vanilla=%s mcgo=%s", lg.name, k, vv, mv)
			}
		}
		t.Logf("%s: vanilla=%v mcgo=%v", lg.name, vrow, mrow)
	}
	if blockCheck["vanilla"] != blockCheck["mcgo"] {
		t.Errorf("melt leg block: vanilla air=%v mcgo air=%v", blockCheck["vanilla"], blockCheck["mcgo"])
	}
	vm, mm := results["vanilla"]["corpse selectable"], results["mcgo"]["corpse selectable"]
	if vm["matched"] != mm["matched"] {
		t.Errorf("corpse selectable: vanilla=%s mcgo=%s", vm["matched"], mm["matched"])
	}
}
