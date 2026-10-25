// TestRootedAzaleaParity pins rooted_azalea_tree (26.2
// net.minecraft.world.level.levelgen.feature.RootSystemFeature):
// the probe walks up from an air origin staying under the
// WORLD_SURFACE heightmap until a cell is air|replaceable_by_trees
// over #azalea_grows_on with 3 clear cells above it (water allowed
// only at +1) and solid non-lava ground below; azalea_tree then
// places at the probe, the root column scatters rooted_dirt through
// #azalea_root_replaceable cells inside ±3xz, and hanging_roots drop
// into empty ±3xz/±2y cells under sturdy faces. Floors on each leg —
// each placement rolls its own RNG.
package blackbox

import (
	"testing"
	"time"
)

func TestRootedAzaleaParity(t *testing.T) {
	const probe = "AzRootParity"
	tg := requireBlackboxFiles(t,
		map[string]string{
			"command-spam-threshold-seconds": "0",
		},
		map[string]string{"ops.json": opsJSONFor(probe)},
		nil)
	defer tg.cleanup()
	defer func() {
		if t.Failed() {
			vanillaLogTail(t)
		}
	}()

	want := runRootedAzalea(t, tg.vanilla, probe)
	got := runRootedAzalea(t, tg.mcgo, probe)

	floors := map[string]int{
		"logs": 4, "leaves": 20, "rootedDirt": 30, "hangingRoots": 1,
	}
	for name, w := range map[string][2]int{
		"logs":         {want.logs, got.logs},
		"leaves":       {want.leaves, got.leaves},
		"rootedDirt":   {want.rootedDirt, got.rootedDirt},
		"hangingRoots": {want.hangingRoots, got.hangingRoots},
	} {
		if w[0] < floors[name] || w[1] < floors[name] {
			t.Errorf("%s: vanilla=%d mc-go=%d (want >=%d)", name, w[0], w[1], floors[name])
		}
	}
	t.Logf("rooted azalea: logs v=%d g=%d, leaves v=%d g=%d, rooted v=%d g=%d, roots v=%d g=%d",
		want.logs, got.logs, want.leaves, got.leaves,
		want.rootedDirt, got.rootedDirt, want.hangingRoots, got.hangingRoots)
}

// runRootedAzalea builds a layered pocket: stone ground and two stone
// roofs around an air origin under a dirt tower whose top lets the
// probe place azalea_tree, then counts the writes.
func runRootedAzalea(t *testing.T, addr, name string) azaleaRun {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, name)
	defer c.Close()
	_ = c.SetDeadline(time.Time{})
	sink := newChatSinkTP(pr, pw)
	time.Sleep(3 * time.Second)
	sink.take()

	sendCmd(t, pw, "time set day")
	// Column layout (x 2..8, z -55..-45): -61 stone ground, -60 air,
	// -59 stone roof, -58 air, -57 stone roof, -56..-51 dirt tower.
	// The air cells each hang under a sturdy roof → hanging-root
	// candidates; the dirt top gives the probe its tree spot at -50.
	for _, cmd := range []string{
		"fill 2 -61 -55 8 -61 -45 minecraft:stone",
		"fill 2 -59 -55 8 -59 -45 minecraft:stone",
		"fill 2 -57 -55 8 -57 -45 minecraft:stone",
		"fill 2 -56 -55 8 -51 -45 minecraft:dirt",
		"setblock 5 -60 -50 air",
		"place feature minecraft:rooted_azalea_tree 5 -60 -50",
	} {
		sendCmd(t, pw, cmd)
	}
	waitDrained(t, pw, sink)
	time.Sleep(time.Second)

	var out azaleaRun
	out.logs = countFill(t, pw, sink, "minecraft:oak_log",
		0, -62, -56, 10, -40, -44)
	out.leaves = countFill(t, pw, sink, "minecraft:azalea_leaves",
		0, -62, -56, 10, -40, -44)
	out.rootedDirt = countFill(t, pw, sink, "minecraft:rooted_dirt",
		0, -62, -56, 10, -40, -44)
	out.hangingRoots = countFill(t, pw, sink, "minecraft:hanging_roots",
		0, -62, -56, 10, -40, -44)
	return out
}
