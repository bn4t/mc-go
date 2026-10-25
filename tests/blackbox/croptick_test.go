package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

// TestCropGrowthParity drives the random-tick crop family end to end:
// probes are planted with the sampler frozen (random_tick_speed 0), the
// speed is cranked high so every roll converges, the sampler is frozen
// again, and each probe is read back with `execute if block`. Since the
// mechanics are probabilistic the comparison pins the converged state —
// max age, farmland moisture end-state, dirt/nylium reversion, turf
// spread — which is identical on both servers regardless of draw order.
func TestCropGrowthParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{
		// The probe strip spans chunks 1-6; the player sits at chunk 3,
		// so distance 3 covers every probe while keeping the saturated
		// tick loop cheap on both servers (hits per cell = elapsed
		// ticks, and slow ticks are the bottleneck here).
		"simulation-distance": "3",
	}, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, func(s *server.Server) {
		s.SimulationDistance = 3
	})
	defer targets.cleanup()

	setup := []string{
		`gamerule random_tick_speed 0`,
		`gamerule advance_weather false`,
		`weather clear`,
		// Wheat on watered farmland -> age 7, moisture pinned at 7.
		`setblock 30 -61 44 minecraft:farmland`,
		`setblock 31 -61 44 minecraft:water`,
		`setblock 30 -60 44 minecraft:wheat[age=0]`,
		// Wheat on dry farmland -> still reaches 7; farmland stays at 0
		// because the crop above maintains it. x=25 keeps its farmland
		// water box clear of the probe water at x=31 and sits outside
		// the roof slab (a roof edge leak still lights vanilla crops one
		// cell in, while mc-go reads any overhead cap as dark).
		`setblock 25 -61 44 minecraft:farmland`,
		`setblock 25 -60 44 minecraft:wheat[age=0]`,
		// Bare dry farmland behind a stone moat reverts to dirt and
		// stays dirt (no grass neighbor can reach it).
		`fill 39 -61 43 41 -61 45 minecraft:stone`,
		`setblock 40 -61 44 minecraft:farmland`,
		// Covered wheat never grows. A single cap is not enough for
		// vanilla: sky light leaks in sideways, so the roof has to extend
		// 7+ cells past the crop to pull brightness below 9. The same slab
		// also roofs the nether wart probe, which ignores light anyway.
		`setblock 44 -61 44 minecraft:farmland`,
		`setblock 44 -60 44 minecraft:wheat[age=0]`,
		`fill 36 -59 36 52 -59 52 minecraft:stone`,
		// Nether wart grows in the dark.
		`setblock 48 -61 44 minecraft:soul_sand`,
		`setblock 48 -60 44 minecraft:nether_wart[age=0]`,
		// Cocoa on a jungle log grows without a light check.
		`setblock 52 -60 45 minecraft:jungle_log`,
		`setblock 52 -60 44 minecraft:cocoa[facing=south,age=0]`,
		// Sweet berry bush on dirt, lit and shaded variants.
		`setblock 56 -61 44 minecraft:dirt`,
		`setblock 56 -60 44 minecraft:sweet_berry_bush[age=0]`,
		`setblock 60 -61 44 minecraft:dirt`,
		`setblock 60 -60 44 minecraft:sweet_berry_bush[age=0]`,
		`setblock 60 -59 44 minecraft:stone`,
		// A maxed melon stem fruits onto the one dirt-supported side.
		`setblock 64 -61 44 minecraft:farmland`,
		`setblock 64 -60 44 minecraft:melon_stem[age=7]`,
		`setblock 63 -61 44 minecraft:stone`,
		`setblock 65 -61 44 minecraft:stone`,
		`setblock 64 -61 45 minecraft:stone`,
		`setblock 64 -61 43 minecraft:dirt`,
		`setblock 68 -61 44 minecraft:farmland`,
		`setblock 68 -60 44 minecraft:pumpkin_stem[age=7]`,
		`setblock 67 -61 44 minecraft:stone`,
		`setblock 69 -61 44 minecraft:stone`,
		`setblock 68 -61 45 minecraft:stone`,
		`setblock 68 -61 43 minecraft:dirt`,
		// Pitcher crop grows into its own upper half.
		`setblock 72 -61 44 minecraft:farmland`,
		`setblock 72 -60 44 minecraft:pitcher_crop[age=0,half=lower]`,
		// The remaining crops on hydrated and dry farmland.
		`setblock 76 -61 44 minecraft:farmland`,
		`setblock 77 -61 44 minecraft:water`,
		`setblock 76 -60 44 minecraft:beetroots[age=0]`,
		`setblock 80 -60 44 minecraft:carrots[age=0]`,
		`setblock 80 -61 44 minecraft:farmland`,
		`setblock 84 -61 44 minecraft:farmland`,
		`setblock 84 -60 44 minecraft:potatoes[age=0]`,
		`setblock 88 -61 44 minecraft:farmland`,
		`setblock 88 -60 44 minecraft:torchflower_crop[age=0]`,
		// Turf spread: dirt raised one block into the air layer still
		// converts when a grass neighbor can reach it.
		`setblock 20 -60 44 minecraft:dirt`,
		// Shaded turf dies back to dirt.
		`setblock 96 -61 44 minecraft:grass_block`,
		`setblock 96 -60 44 minecraft:stone`,
		// Nylium under cover reverts to netherrack.
		`setblock 100 -61 44 minecraft:crimson_nylium`,
		`setblock 100 -60 44 minecraft:stone`,
		// A control grass block in the open keeps its turf.
		`setblock 104 -61 44 minecraft:grass_block`,
	}

	probes := []struct{ name, spec string }{
		{"wet wheat reaches 7", "30 -60 44 minecraft:wheat[age=7]"},
		{"wet farmland pins moisture 7", "30 -61 44 minecraft:farmland[moisture=7]"},
		{"dry wheat reaches 7", "25 -60 44 minecraft:wheat[age=7]"},
		{"dry farmland stays 0 under crop", "25 -61 44 minecraft:farmland[moisture=0]"},
		{"bare dry farmland reverts to dirt", "40 -61 44 minecraft:dirt"},
		{"covered wheat stays 0", "44 -60 44 minecraft:wheat[age=0]"},
		{"nether wart reaches 3 in dark", "48 -60 44 minecraft:nether_wart[age=3]"},
		{"cocoa reaches 2", "52 -60 44 minecraft:cocoa[facing=south,age=2]"},
		{"berry bush reaches 3", "56 -60 44 minecraft:sweet_berry_bush[age=3]"},
		{"shaded berry stays 0", "60 -60 44 minecraft:sweet_berry_bush[age=0]"},
		{"melon stem attaches north", "64 -60 44 minecraft:attached_melon_stem[facing=north]"},
		{"melon fruit lands on dirt", "64 -60 43 minecraft:melon"},
		{"pumpkin stem attaches north", "68 -60 44 minecraft:attached_pumpkin_stem[facing=north]"},
		{"pumpkin fruit lands on dirt", "68 -60 43 minecraft:pumpkin"},
		{"pitcher lower reaches 4", "72 -60 44 minecraft:pitcher_crop[age=4,half=lower]"},
		{"pitcher upper appears at 4", "72 -59 44 minecraft:pitcher_crop[age=4,half=upper]"},
		{"beetroots reach 3", "76 -60 44 minecraft:beetroots[age=3]"},
		{"carrots reach 7", "80 -60 44 minecraft:carrots[age=7]"},
		{"potatoes reach 7", "84 -60 44 minecraft:potatoes[age=7]"},
		// Vanilla matures the crop into the torchflower flower (AGE_1
		// tops out at 1 while getMaxAge is 2).
		{"torchflower crop matures", "88 -60 44 minecraft:torchflower"},
		{"raised dirt grassifies", "20 -60 44 minecraft:grass_block"},
		{"shaded grass reverts to dirt", "96 -61 44 minecraft:dirt"},
		{"shaded nylium reverts", "100 -61 44 minecraft:netherrack"},
		{"open grass keeps turf", "104 -61 44 minecraft:grass_block"},
	}

	run := func(addr string) []string {
		c, pr, pw := joinPlayQuiet(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		sendClientInformation(t, pw)
		sink := newChatSinkTP(pr, pw)
		sink.cap = 1 << 20
		// Drain the join chunk stream before commands.
		time.Sleep(4 * time.Second)
		sink.take()
		for _, cmd := range setup {
			sendCommand(t, pw, cmd)
		}
		time.Sleep(400 * time.Millisecond)
		sendCommand(t, pw, `tp @s 62.5 -60 44.5`)
		sendCommand(t, pw, `gamerule random_tick_speed 4096`)
		// The window: hits per cell equal elapsed ticks, not the speed
		// value, so mc-go's slower saturated tick needs real wall-clock
		// time to converge; vanilla converges far earlier and idles.
		// Keepalives and teleports are answered by the sink goroutine.
		time.Sleep(45 * time.Second)
		sendCommand(t, pw, `gamerule random_tick_speed 0`)
		time.Sleep(500 * time.Millisecond)
		sink.take()

		var out []string
		for i, p := range probes {
			sendCommand(t, pw, fmt.Sprintf("execute if block %s run say ZC-ok-%d", p.spec, i))
			sendCommand(t, pw, fmt.Sprintf("execute unless block %s run say ZC-miss-%d", p.spec, i))
			sendCommand(t, pw, fmt.Sprintf("say ZMK%d", i))
			marker := fmt.Sprintf("ZMK%d", i)
			hit := "none"
			deadline := time.Now().Add(4 * time.Second)
		probe:
			for time.Now().Before(deadline) {
				for _, line := range sink.take() {
					if strings.Contains(line, marker) {
						break probe
					}
					if strings.Contains(line, fmt.Sprintf("ZC-ok-%d", i)) {
						hit = "hit"
					}
					if strings.Contains(line, fmt.Sprintf("ZC-miss-%d", i)) {
						hit = "miss"
					}
				}
				time.Sleep(40 * time.Millisecond)
			}
			out = append(out, hit)
		}
		return out
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	for i, p := range probes {
		t.Logf("%-34s vanilla=%s mcgo=%s", p.name, want[i], got[i])
		if want[i] != got[i] {
			t.Errorf("probe %q: vanilla %s, mc-go %s", p.name, want[i], got[i])
		}
	}
}
