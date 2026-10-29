//go:build blackbox

package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestLocateStructureParity pins /locate structure in a generated
// world with structures on: findNearestMapStructure's random-spread
// ring search (first hit per placement ring, nearest across
// placements), structure tags printed with the found member, the locate
// position (start chunk corner, y "~") and the floored horizontal
// distance, from several sources via execute positioned. Only the
// jigsaw structures mc-go generates are located. /locate biome runs
// findClosestBiome3d: the spiral of 32-block columns, the heights out
// from the source y, the reported y and 3D distance.
func TestLocateStructureParity(t *testing.T) {
	const seed = 12345
	props := map[string]string{
		"level-type":          "minecraft:normal",
		"generator-settings":  "{}",
		"level-seed":          fmt.Sprint(seed),
		"generate-structures": "true",
		"view-distance":       "2",
	}
	files := map[string]string{"ops.json": opsJSONFor("LocateProbe")}
	tg := requireBlackboxFiles(t, props, files, func(s *server.Server) {
		s.LevelType = server.LevelTypeNormal
		s.WorldSeed = seed
		s.GenerateStructures = true
		s.ViewDistance = 2
	})
	defer tg.cleanup()
	defer func(d time.Duration) { clientDeadline = d }(clientDeadline)
	clientDeadline = 3 * time.Minute

	var cmds []string
	for _, at := range [][2]int{{0, 0}, {1500, -2200}, {-3100, 700}} {
		for _, st := range []string{
			"minecraft:village_plains", "#minecraft:village", "minecraft:pillager_outpost",
			"minecraft:trail_ruins", "minecraft:trial_chambers", "minecraft:ancient_city",
			"minecraft:village_snowy",
		} {
			cmds = append(cmds, fmt.Sprintf("execute positioned %d 70 %d run locate structure %s", at[0], at[1], st))
		}
	}
	// A joining player lands at a random spot near spawn, so every
	// search names its source.
	cmds = append(cmds, "execute positioned 37 64 -12 run locate structure minecraft:village_desert",
		"locate structure minecraft:bastion_remnant",
		"execute positioned 300 70 -900 run locate biome minecraft:jungle",
		"execute positioned 300 70 -900 run locate biome #minecraft:is_ocean",
		"execute positioned -2000 -20 4000 run locate biome minecraft:deep_dark",
		"execute positioned 37 64 -12 run locate biome minecraft:mushroom_fields",
		"execute positioned 37 30 -12 run locate biome minecraft:lush_caves",
		"locate biome minecraft:nether_wastes",
		"locate biome #minecraft:is_nether")

	run := func(addr string) []string {
		c, pr, pw := joinPlayBlackbox(t, addr, "LocateProbe")
		defer c.Close()
		sink := newChatSink(pr, pw)
		sink.cap = 1 << 20
		var out []string
		for i, cmd := range cmds {
			sink.take()
			var b protocol.Buffer
			b.String(cmd)
			if err := pw.WritePacket(server.IDPlayChatCommandSB, b.Bytes()); err != nil {
				t.Fatal(err)
			}
			// A far search generates structure starts: allow time.
			deadline := time.Now().Add(150 * time.Second)
			var got string
			for got == "" && time.Now().Before(deadline) {
				for _, line := range sink.take() {
					if tree, ok := componentTree(line, true); ok && strings.Contains(tree, "commands.locate") {
						got = tree
					}
				}
				time.Sleep(50 * time.Millisecond)
			}
			if got == "" {
				t.Fatalf("%s: case %d %q: no reply", addr, i, cmd)
			}
			out = append(out, got)
		}
		return out
	}
	want := run(tg.vanilla)
	got := run(tg.mcgo)
	for i := range cmds {
		if want[i] != got[i] {
			t.Errorf("%s:\n  vanilla %s\n  mc-go   %s", cmds[i], want[i], got[i])
		} else {
			t.Logf("%s: %s", cmds[i], got[i])
		}
	}
}
