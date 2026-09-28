// TestAzaleaParity pins azalea_tree against vanilla (26.2
// net.minecraft.world.level.levelgen.feature.trunkplacers.
// BendingTrunkPlacer + foliageplacers.RandomSpreadFoliagePlacer +
// block.AzaleaBlock): the trunk wobbles sideways each level, bends
// fully sideways for bend_length+1 cells at the top, spread foliage
// samples the 3:1 azalea/flowering provider per cell, and
// below_trunk_provider roots grass into rooted_dirt. Bushes grow
// only through bonemeal — never the random tick. Floors, not
// equality — each placement rolls its own RNG on each server.
package blackbox

import (
	"strings"
	"testing"
	"time"
)

type azaleaRun struct {
	logs       int
	leaves     int
	flowering  int
	rootedDirt int
	bushGrew   bool
}

func TestAzaleaParity(t *testing.T) {
	const probe = "AzaleaParity"
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

	want := runAzaleaScenario(t, tg.vanilla, probe)
	got := runAzaleaScenario(t, tg.mcgo, probe)

	floors := map[string]int{
		"logs": 4, "leaves": 30, "flowering": 3, "rootedDirt": 2,
	}
	for name, w := range map[string][2]int{
		"logs":       {want.logs, got.logs},
		"leaves":     {want.leaves, got.leaves},
		"flowering":  {want.flowering, got.flowering},
		"rootedDirt": {want.rootedDirt, got.rootedDirt},
	} {
		if w[0] < floors[name] || w[1] < floors[name] {
			t.Errorf("%s: vanilla=%d mc-go=%d (want >=%d)", name, w[0], w[1], floors[name])
		}
	}
	if want.bushGrew != got.bushGrew {
		t.Errorf("bonemeal azalea bush: vanilla grew=%v mc-go grew=%v", want.bushGrew, got.bushGrew)
	}
	t.Logf("azalea: logs v=%d g=%d, leaves v=%d g=%d, flowering v=%d g=%d, rooted v=%d g=%d, bushGrew v=%v g=%v",
		want.logs, got.logs, want.leaves, got.leaves,
		want.flowering, got.flowering, want.rootedDirt, got.rootedDirt,
		want.bushGrew, got.bushGrew)
}

// runAzaleaScenario /places four azalea trees on a grass pad
// (grass is outside cannot_replace_below_tree_trunk, so the provider
// writes rooted_dirt) and bonemeals one azalea bush, then counts via
// fill-replace.
func runAzaleaScenario(t *testing.T, addr, name string) azaleaRun {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, name)
	defer c.Close()
	_ = c.SetDeadline(time.Time{})
	sink := newChatSinkTP(pr, pw)
	time.Sleep(3 * time.Second)
	sink.take()

	sendCmd(t, pw, "time set day")
	sendCmd(t, pw, "gamemode survival @s")

	// Grass pad — outside #cannot_replace_below_tree_trunk so the
	// tree converts its substrate cell to rooted_dirt.
	sendCmd(t, pw, "fill -2 -61 -62 40 -61 -28 minecraft:grass_block")
	for _, x := range []int{0, 10, 20, 30} {
		sendCmd(t, pw, "place feature minecraft:azalea_tree "+itoa(int32(x))+" -60 -50")
	}
	// The bonemeal-driven bush at (36,-60,-50).
	sendCmd(t, pw, "setblock 36 -60 -50 minecraft:azalea")
	sendCmd(t, pw, "item replace entity @s hotbar.0 with minecraft:bone_meal 64")
	sendCmd(t, pw, "tp @s 36.5 -59 -47.5")
	waitDrained(t, pw, sink)

	var out azaleaRun
	// AzaleaBlock.isBonemealSuccess rolls 45% per click — keep
	// clicking until a grown tree roots the grass cell below the
	// bush (each click's RNG is independent per server, so pin only
	// "grew within 10 clicks" — P(10 misses) ≈ 0.0025).
	seq := int32(1)
	for click := 0; click < 10 && !out.bushGrew; click++ {
		sendUseItemOn(t, pw, 36, -60, -50, 1, seq)
		seq++
		time.Sleep(400 * time.Millisecond)
		below := scanNames(t, pw, sink, []string{
			"minecraft:rooted_dirt", "minecraft:grass_block", "minecraft:dirt",
		}, [][3]int{{36, -61, -50}})
		out.bushGrew = below[[3]int{36, -61, -50}] == "minecraft:rooted_dirt"
	}
	time.Sleep(time.Second)

	out.logs = countFill(t, pw, sink, "minecraft:oak_log",
		-2, -61, -62, 40, -40, -28)
	out.leaves = countFill(t, pw, sink, "minecraft:azalea_leaves",
		-2, -61, -62, 40, -40, -28)
	out.flowering = countFill(t, pw, sink, "minecraft:flowering_azalea_leaves",
		-2, -61, -62, 40, -40, -28)
	out.rootedDirt = countFill(t, pw, sink, "minecraft:rooted_dirt",
		-2, -62, -62, 40, -40, -28)
	for _, l := range sink.take() {
		if strings.Contains(l, "Failed to place feature") {
			t.Logf("place feature failed: %s", l)
		}
	}
	return out
}
