//go:build blackbox

package blackbox

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

// shapeCase lays blocks (relative setblocks) and then changes one cell
// with a relative setblock; the neighbours' updateShape answers.
type shapeCase struct {
	name   string
	setup  []string
	change string // "dx dy dz block"
}

var shapeCases = []shapeCase{
	{"torch loses floor", []string{"0 0 0 stone", "0 1 0 torch"}, "0 0 0 air"},
	{"torch keeps on side change", []string{"0 0 0 stone", "0 1 0 torch", "1 1 0 stone"}, "1 1 0 air"},
	{"wall torch loses wall", []string{"0 1 1 stone", "0 1 0 wall_torch[facing=north]"}, "0 1 1 air"},
	{"redstone wall torch loses wall", []string{"0 1 1 stone", "0 1 0 redstone_wall_torch[facing=north]"}, "0 1 1 air"},
	{"wall sign loses wall", []string{"0 1 1 stone", "0 1 0 oak_wall_sign[facing=north]"}, "0 1 1 air"},
	{"standing sign loses floor", []string{"0 0 0 stone", "0 1 0 oak_sign"}, "0 0 0 air"},
	{"hanging sign loses ceiling", []string{"0 2 0 stone", "0 1 0 oak_hanging_sign"}, "0 2 0 air"},
	{"wall hanging sign loses side", []string{"1 1 0 stone", "0 1 0 oak_wall_hanging_sign[facing=north]"}, "1 1 0 air"},
	{"banner loses floor", []string{"0 0 0 stone", "0 1 0 white_banner"}, "0 0 0 air"},
	{"wall banner loses wall", []string{"0 1 1 stone", "0 1 0 white_wall_banner[facing=north]"}, "0 1 1 air"},
	{"button loses wall", []string{"0 1 1 stone", "0 1 0 stone_button[face=wall,facing=north]"}, "0 1 1 air"},
	{"floor button loses floor", []string{"0 0 0 stone", "0 1 0 oak_button[face=floor]"}, "0 0 0 air"},
	{"lever ceiling loses ceiling", []string{"0 2 0 stone", "0 1 0 lever[face=ceiling]"}, "0 2 0 air"},
	{"ladder loses wall", []string{"0 1 1 stone", "0 1 0 ladder[facing=north]"}, "0 1 1 air"},
	{"tripwire hook loses wall", []string{"0 1 1 stone", "0 1 0 tripwire_hook[facing=north]"}, "0 1 1 air"},
	{"lantern loses ceiling", []string{"0 2 0 stone", "0 1 0 lantern[hanging=true]"}, "0 2 0 air"},
	{"lantern loses floor", []string{"0 0 0 stone", "0 1 0 lantern"}, "0 0 0 air"},
	{"pressure plate loses floor", []string{"0 0 0 stone", "0 1 0 stone_pressure_plate"}, "0 0 0 air"},
	{"carpet loses floor", []string{"0 0 0 stone", "0 1 0 red_carpet"}, "0 0 0 air"},
	{"flower loses soil", []string{"0 0 0 grass_block", "0 1 0 poppy"}, "0 0 0 stone"},
	{"sapling loses soil", []string{"0 0 0 dirt", "0 1 0 oak_sapling"}, "0 0 0 sand"},
	{"cake loses floor", []string{"0 0 0 stone", "0 1 0 cake"}, "0 0 0 air"},
	{"door lower removed", []string{"0 0 0 stone", "0 1 0 oak_door[half=lower]", "0 2 0 oak_door[half=upper]"}, "0 1 0 air"},
	{"door upper removed", []string{"0 0 0 stone", "0 1 0 oak_door[half=lower]", "0 2 0 oak_door[half=upper]"}, "0 2 0 air"},
	{"door floor removed", []string{"0 0 0 stone", "0 1 0 oak_door[half=lower]", "0 2 0 oak_door[half=upper]"}, "0 0 0 air"},
	{"door partner opens", []string{"0 0 0 stone", "0 1 0 oak_door[half=lower]", "0 2 0 oak_door[half=upper]"}, "0 1 0 oak_door[half=lower,open=true]"},
	{"tall grass lower removed", []string{"0 0 0 grass_block", "0 1 0 tall_grass[half=lower]", "0 2 0 tall_grass[half=upper]"}, "0 1 0 air"},
	{"tall grass upper removed", []string{"0 0 0 grass_block", "0 1 0 tall_grass[half=lower]", "0 2 0 tall_grass[half=upper]"}, "0 2 0 air"},
	{"bed head removed", []string{"0 1 0 red_bed[part=foot,facing=south]", "0 1 1 red_bed[part=head,facing=south]"}, "0 1 1 air"},
	{"bed foot removed", []string{"0 1 0 red_bed[part=foot,facing=south]", "0 1 1 red_bed[part=head,facing=south]"}, "0 1 0 air"},
	{"cocoa loses log", []string{"0 1 1 jungle_log", "0 1 0 cocoa[facing=south]"}, "0 1 1 air"},
	{"amethyst bud loses base", []string{"0 0 0 amethyst_block", "0 1 0 small_amethyst_bud[facing=up]"}, "0 0 0 air"},
	{"note block instrument", []string{"0 0 0 stone", "0 1 0 note_block"}, "0 0 0 oak_planks"},
	{"campfire signal", []string{"0 0 0 stone", "0 1 0 campfire"}, "0 0 0 hay_block"},
	{"fence gate in wall", []string{"0 1 0 oak_fence_gate[facing=north]"}, "1 1 0 cobblestone_wall"},
	{"bell floor loses floor", []string{"0 0 0 stone", "0 1 0 bell[attachment=floor]"}, "0 0 0 air"},
	{"bell single wall gains double", []string{"0 1 -1 stone", "0 1 0 bell[attachment=single_wall,facing=north]"}, "0 1 1 stone"},
	{"crop loses farmland", []string{"0 0 0 farmland", "0 1 0 wheat"}, "0 0 0 dirt"},
	{"mushroom block neighbour", []string{"0 1 0 red_mushroom_block"}, "1 1 0 red_mushroom_block"},
	{"bamboo sapling grows stalk above", []string{"0 0 0 dirt", "0 1 0 bamboo_sapling"}, "0 2 0 bamboo"},
	{"dirt path under solid", []string{"0 0 0 dirt_path"}, "0 1 0 stone"},
	{"spore blossom loses ceiling", []string{"0 2 0 stone", "0 1 0 spore_blossom"}, "0 2 0 air"},
}

func shapeOrigin(i int) (int, int, int) { return 100 + 6*(i%12), -60, 260 + 6*(i/12) }

// TestShapeUpdateParity changes one cell next to blocks whose shape
// follows their neighbours - supports going away, partner halves,
// rewritten properties - and diffs the block updates the change sends.
func TestShapeUpdateParity(t *testing.T) {
	packMeta := `{"pack":{"description":"zz","min_format":107,"max_format":107}}`
	files := map[string]string{"ops.json": opsJSONFor("Shaper"), "world/datapacks/zz/pack.mcmeta": packMeta}
	mcDir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(mcDir, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("datapacks/zz/pack.mcmeta", packMeta)
	rel := func(i int, spec string) string {
		x, y, z := shapeOrigin(i)
		var dx, dy, dz int
		var blk string
		fmt.Sscanf(spec, "%d %d %d %s", &dx, &dy, &dz, &blk)
		return fmt.Sprintf("setblock %d %d %d %s", x+dx, y+dy, z+dz, blk)
	}
	for i, c := range shapeCases {
		x, y, z := shapeOrigin(i)
		var b strings.Builder
		fmt.Fprintf(&b, "fill %d %d %d %d %d %d air\n", x-2, y, z-2, x+2, y+4, z+2)
		fmt.Fprintf(&b, "fill %d %d %d %d %d %d stone\n", x-2, y-1, z-2, x+2, y-1, z+2)
		for _, s := range c.setup {
			b.WriteString(rel(i, s) + "\n")
		}
		// The marker comes from the function itself, after its writes.
		fmt.Fprintf(&b, "say SETUP%dX\n", i)
		fn := b.String()
		files[fmt.Sprintf("world/datapacks/zz/data/zz/function/s%d.mcfunction", i)] = fn
		write(fmt.Sprintf("datapacks/zz/data/zz/function/s%d.mcfunction", i), fn)
	}
	tg := requireBlackboxFiles(t, nil, files, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
		s.WorldDir = mcDir
	})
	defer tg.cleanup()

	run := func(addr string) []string {
		c, pr, pw := joinPlayQuiet(t, addr, "Shaper")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		sink := newChatSinkTP(pr, pw)
		sink.cap = 1 << 16
		if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
			t.Fatal(err)
		}
		sendCommand(t, pw, "gamemode creative @s")
		sendCommand(t, pw, "gamerule random_tick_speed 0")
		sendCommand(t, pw, "tp @s 130 -50 280")
		time.Sleep(2 * time.Second)
		wait := func(marker string) []string {
			var lines []string
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				for _, l := range sink.take() {
					if strings.Contains(l, marker) {
						return lines
					}
					lines = append(lines, l)
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatalf("%s: marker %s never arrived", addr, marker)
			return nil
		}
		// Warm up: the first setup also streams the case area's
		// chunks; run it once and drop everything it sent.
		sendCommand(t, pw, "function zz:s0")
		wait("SETUP0X")
		time.Sleep(2 * time.Second)
		sink.take()
		var out []string
		for i, sc := range shapeCases {
			sendCommand(t, pw, fmt.Sprintf("function zz:s%d", i))
			wait(fmt.Sprintf("SETUP%dX", i))
			time.Sleep(time.Second)
			sink.take()
			sendCommand(t, pw, rel(i, sc.change))
			time.Sleep(300 * time.Millisecond)
			sendCommand(t, pw, fmt.Sprintf("say DONE%dX", i))
			updates := map[[3]int32]int32{}
			for _, l := range wait(fmt.Sprintf("DONE%dX", i)) {
				collectBlockUpdates(l, updates)
			}
			// Block updates flush at the end of the tick that made
			// them, after a chat marker from the same tick: give a
			// loaded server a moment, then take the stragglers.
			time.Sleep(time.Second)
			for _, l := range sink.take() {
				collectBlockUpdates(l, updates)
			}
			x, y, z := shapeOrigin(i)
			var cells []string
			for p, st := range updates {
				cells = append(cells, fmt.Sprintf("%d,%d,%d=%s", p[0]-int32(x), p[1]-int32(y), p[2]-int32(z), stateLabel(st)))
			}
			sort.Strings(cells)
			out = append(out, strings.Join(cells, " "))
		}
		return out
	}
	want := run(tg.vanilla)
	got := run(tg.mcgo)
	bad := 0
	for i, c := range shapeCases {
		if want[i] != got[i] {
			bad++
			t.Errorf("%s:\n  vanilla %s\n  mc-go   %s", c.name, want[i], got[i])
		} else {
			t.Logf("%s: %s", c.name, got[i])
		}
	}
	t.Logf("%d cases, %d differ", len(shapeCases), bad)
}
