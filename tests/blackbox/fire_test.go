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

// TestFireParity drives FireBlock's scheduled tick: onPlace pops an
// unsupported fire outright and arms a 30-40-tick tick that re-arms
// itself, ages the fire (min(15, age + nextInt(3)/2)), removes it when
// the site is invalid (no sturdy floor, no flammable neighbour, age>3)
// or aged out (15 + nextInt(4)==0 + unburnable floor), rolls burnout
// on the six neighbours (300 sides / 250 below+above), and spreads
// into empty cells of the [-1,1]x[-1,4]x[-1,1] box whose neighbours
// carry ignite odds. updateShape refolds the five face properties and
// pops unsupported fire on any neighbour write, command or gameplay.
// Rain and spread probes are count-based: per-cell rolls are odds-
// driven, so the assertions are bands a correct implementation lands
// inside on both servers.
func TestFireParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{
		"simulation-distance": "3",
	}, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, func(s *server.Server) {
		s.SimulationDistance = 3
	})
	defer targets.cleanup()

	setup := []string{
		`weather clear`,
		`time set noon`,
		`fill 44 -60 60 76 -60 90 minecraft:stone`,
		`fill 44 -59 60 76 -50 90 minecraft:air`,
		// Floating fire pops inside the write (BaseFireBlock.onPlace).
		`setblock 46 -57 61 minecraft:fire`,
		// Age-15 fire on plain stone dies at its first tick: invalid
		// site + age > 3. Netherrack's is infiniburn and lives on.
		`setblock 48 -59 61 minecraft:fire[age=15]`,
		`setblock 50 -60 61 minecraft:netherrack`,
		`setblock 50 -59 61 minecraft:fire[age=15]`,
		// Soul fire survives only on its base blocks.
		`setblock 55 -60 61 minecraft:soul_sand`,
		`setblock 55 -59 61 minecraft:soul_fire`,
		`setblock 56 -59 61 minecraft:soul_fire`,
		// Floating fire with two flammable faces survives; losing them
		// pops it via updateShape.
		`setblock 62 -58 61 minecraft:oak_planks`,
		`setblock 64 -58 61 minecraft:oak_leaves[persistent=true]`,
		`setblock 63 -58 61 minecraft:fire`,
		// One plank of support, instantly removed.
		`setblock 66 -58 61 minecraft:oak_planks`,
		`setblock 66 -57 61 minecraft:fire`,
		`setblock 66 -58 61 minecraft:air`,
		// Face fold: fire floats next to a plank; a later write beside
		// it refolds east=true.
		`setblock 69 -58 61 minecraft:oak_planks`,
		`setblock 68 -58 61 minecraft:fire`,
		`setblock 67 -58 61 minecraft:stone`,
	}
	// Burnout row: fire on netherrack next to grass — burn odds 100
	// out of 300 consume it in ~10 ticks; the cell relights to fire
	// half the time, so probes assert "not grass". A stone control
	// never burns.
	for i := 0; i < 8; i++ {
		x := 60 + 2*i
		setup = append(setup,
			fmt.Sprintf(`setblock %d -60 71 minecraft:dirt`, x),
			fmt.Sprintf(`setblock %d -59 71 minecraft:short_grass`, x),
			fmt.Sprintf(`setblock %d -60 72 minecraft:netherrack`, x),
			fmt.Sprintf(`setblock %d -59 72 minecraft:fire`, x),
		)
	}
	setup = append(setup,
		`setblock 60 -60 74 minecraft:stone`,
		`setblock 60 -59 74 minecraft:fire`,
		`setblock 60 -59 73 minecraft:stone`,
	)
	// Spread row: two logs feed each fire so the spread race outlives
	// one fuel burnout; each probes an air cell above one log.
	for i := 0; i < 8; i++ {
		x := 60 + 2*i
		setup = append(setup,
			fmt.Sprintf(`setblock %d -60 80 minecraft:netherrack`, x),
			fmt.Sprintf(`setblock %d -59 80 minecraft:fire`, x),
			fmt.Sprintf(`setblock %d -59 79 minecraft:oak_log`, x),
			fmt.Sprintf(`setblock %d -59 81 minecraft:oak_log`, x),
		)
	}
	// Rain row: fire on stone beside two coals (valid site, not
	// infiniburn) under open sky. The world is the_void, whose
	// precipitation is NONE — /weather rain must not kill fires on
	// either server; two supports keep fuel-burnout flakes ~zero.
	for i := 0; i < 8; i++ {
		x := 60 + 2*i
		setup = append(setup,
			fmt.Sprintf(`setblock %d -59 86 minecraft:fire`, x),
			fmt.Sprintf(`setblock %d -59 85 minecraft:coal_block`, x),
			fmt.Sprintf(`setblock %d -59 87 minecraft:coal_block`, x),
		)
	}
	for i := 0; i < 4; i++ {
		x := 60 + 2*i
		setup = append(setup,
			fmt.Sprintf(`setblock %d -59 90 minecraft:fire`, x),
			fmt.Sprintf(`setblock %d -59 89 minecraft:coal_block`, x),
			fmt.Sprintf(`setblock %d -59 91 minecraft:coal_block`, x),
			fmt.Sprintf(`fill %d -57 88 %d -57 91 minecraft:stone`, x-1, x+1),
		)
	}
	setup = append(setup, `tp @s 63 -55 75`)

	// want is the expected probe verdict: hit (state matches), miss
	// (anything else — a consumed cell may be air OR relit fire), or
	// "" for count-banded cells asserted by group, not per probe.
	type probeDef struct{ name, spec, want string }
	early := []probeDef{
		{"face folded east", "68 -58 61 minecraft:fire[east=true]", "hit"},
		{"face not west", "68 -58 61 minecraft:fire[west=true]", "miss"},
		{"two-support fire sits", "63 -58 61 minecraft:fire", "hit"},
		{"floating fire pops", "46 -57 61 minecraft:air", "hit"},
		{"netherrack fire stays 15", "50 -59 61 minecraft:fire[age=15]", "hit"},
		{"soul fire on base sits", "55 -59 61 minecraft:soul_fire", "hit"},
		{"soul fire off base pops", "56 -59 61 minecraft:air", "hit"},
		{"deprived fire pops", "66 -57 61 minecraft:air", "hit"},
	}
	mid := []probeDef{
		{"stone fire aged out", "48 -59 61 minecraft:air", "hit"},
		{"netherrack fire persists", "50 -59 61 minecraft:fire[age=15]", "hit"},
	}
	burnout := []probeDef{}
	for i := 0; i < 8; i++ {
		burnout = append(burnout, probeDef{
			fmt.Sprintf("grass %d burned", i),
			fmt.Sprintf("%d -59 71 minecraft:short_grass", 60+2*i), "miss"})
	}
	burnout = append(burnout, probeDef{"stone control holds", "60 -59 73 minecraft:stone", "hit"})
	spread := []probeDef{}
	for i := 0; i < 8; i++ {
		x := 60 + 2*i
		spread = append(spread, probeDef{
			fmt.Sprintf("spread %d up-n", i),
			fmt.Sprintf("%d -58 79 minecraft:fire", x), ""})
		spread = append(spread, probeDef{
			fmt.Sprintf("spread %d west-n", i),
			fmt.Sprintf("%d -59 79 minecraft:fire", x-1), ""})
		spread = append(spread, probeDef{
			fmt.Sprintf("spread %d up-s", i),
			fmt.Sprintf("%d -58 81 minecraft:fire", x), ""})
		spread = append(spread, probeDef{
			fmt.Sprintf("spread %d east-s", i),
			fmt.Sprintf("%d -59 81 minecraft:fire", x+1), ""})
	}
	rain := []probeDef{}
	for i := 0; i < 8; i++ {
		rain = append(rain, probeDef{
			fmt.Sprintf("void-rain holds %d", i),
			fmt.Sprintf("%d -59 86 minecraft:fire", 60+2*i), ""})
	}
	for i := 0; i < 4; i++ {
		rain = append(rain, probeDef{
			fmt.Sprintf("roofed %d holds", i),
			fmt.Sprintf("%d -59 90 minecraft:fire", 60+2*i), "hit"})
	}

	probe := func(pw *protocol.Writer, sink *chatSink, spec string, i int) string {
		sendCommand(t, pw, fmt.Sprintf("execute if block %s run say ZF-ok-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("execute unless block %s run say ZF-miss-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("say ZFK%d", i))
		hit := "none"
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, fmt.Sprintf("ZFK%d", i)) {
					return hit
				}
				if strings.Contains(line, fmt.Sprintf("ZF-ok-%d", i)) {
					hit = "hit"
				}
				if strings.Contains(line, fmt.Sprintf("ZF-miss-%d", i)) {
					hit = "miss"
				}
			}
			time.Sleep(40 * time.Millisecond)
		}
		return hit
	}

	run := func(addr string) map[string]string {
		c, pr, pw := joinPlayQuiet(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		sendClientInformation(t, pw)
		sink := newChatSinkTP(pr, pw)
		sink.cap = 1 << 20
		time.Sleep(4 * time.Second)
		sink.take()
		for _, cmd := range setup {
			sendCommand(t, pw, cmd)
		}
		sendCommand(t, pw, `say ZSYNC`)
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			found := false
			for _, line := range sink.take() {
				if strings.Contains(line, "ZSYNC") {
					found = true
				}
			}
			if found {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		out := map[string]string{}
		// Early probes run inside the first tick window — fires arm at
		// 30-40 ticks, so ~2s of probing is safe.
		time.Sleep(1500 * time.Millisecond)
		for i, p := range early {
			out[p.name] = probe(pw, sink, p.spec, i)
		}
		// ~50s of real ticks covers ~25 fire ticks.
		time.Sleep(48 * time.Second)
		n := len(early)
		for i, p := range mid {
			out[p.name] = probe(pw, sink, p.spec, n+i)
		}
		n += len(mid)
		for i, p := range burnout {
			out[p.name] = probe(pw, sink, p.spec, n+i)
		}
		n += len(burnout)
		for i, p := range spread {
			out[p.name] = probe(pw, sink, p.spec, n+i)
		}
		// Rain last: it sweeps every open-sky fire — except here the
		// biome is the_void (precipitation NONE), so a correct server
		// kills nothing. Rewriting age 0 keeps the fixtures under the
		// sturdy-floor grace (age<=3) for a ~3-tick window.
		n += len(spread)
		for i := 0; i < 8; i++ {
			sendCommand(t, pw, fmt.Sprintf(`setblock %d -59 86 minecraft:fire[age=0]`, 60+2*i))
		}
		for i := 0; i < 4; i++ {
			sendCommand(t, pw, fmt.Sprintf(`setblock %d -59 90 minecraft:fire[age=0]`, 60+2*i))
		}
		sendCommand(t, pw, `weather rain`)
		time.Sleep(6 * time.Second)
		for i, p := range rain {
			out[p.name] = probe(pw, sink, p.spec, n+i)
		}
		sendCommand(t, pw, `weather clear`)
		return out
	}

	vanilla := run(targets.vanilla)
	mcgo := run(targets.mcgo)

	groups := [][]probeDef{early, mid, burnout, spread, rain}
	for _, g := range groups {
		for _, p := range g {
			v, m := vanilla[p.name], mcgo[p.name]
			t.Logf("%-28s vanilla=%-4s mcgo=%-4s want=%s", p.name, v, m, p.want)
			if p.want != "" && (v != p.want || m != p.want) {
				t.Errorf("probe %q: want %s, vanilla=%s mc-go=%s", p.name, p.want, v, m)
			}
		}
	}
	// Count-banded rows: per-cell rolls are odds-driven, so each server
	// must land inside the band rather than per-probe equality.
	count := func(out map[string]string, rows []probeDef, want string) int {
		n := 0
		for _, p := range rows {
			if out[p.name] == want {
				n++
			}
		}
		return n
	}
	for _, res := range []struct {
		name string
		out  map[string]string
	}{
		{"vanilla", vanilla}, {"mc-go", mcgo},
	} {
		// Grass burnout: ~99.9% consumed per cell over ~25 ticks —
		// allow one straggler.
		if n := count(res.out, burnout[:8], "miss"); n < 7 {
			t.Errorf("%s: %d/8 grass cells burned, want >=7", res.name, n)
		}
		// Spread: each of the 32 targets ignites at ~1.5%/tick while a
		// fuel neighbor lives — band brackets nobody-lit/all-lit flakes.
		if n := count(res.out, spread, "hit"); n < 2 || n > 31 {
			t.Errorf("%s: %d/32 spread targets lit, want 2..31", res.name, n)
		}
		// Void-rain row: the_void has no precipitation so fires must
		// all survive; two supports leave ~0 residual flake — allow
		// one unlucky support-burnout pop.
		if n := count(res.out, rain[:8], "hit"); n < 7 {
			t.Errorf("%s: %d/8 void-rain fires survived, want >=7", res.name, n)
		}
	}
}
