// TestFireSpreadParity pins FireBlock.tick (26.2
// net.minecraft.world.level.block.FireBlock): on a 30-40 tick
// reschedule cadence the fire ages up, burns its six neighbours out
// through checkBurnOut (base 300 horizontal, 250 vertical), and
// spreads into empty cells whose own ignite odds exceed zero at
// (odds+40+difficulty*7)/(age+30) against the height-scaled
// threshold. An infiniburn floor keeps the source alive forever so
// both legs accumulate spread and consumed planks across the sprint.
// Floors only — every fire tick rolls its own RNG.
package blackbox

import (
	"testing"
	"time"
)

type fireRun struct {
	fireCells  int
	planksLeft int
}

func TestFireSpreadParity(t *testing.T) {
	const probe = "FireParity"
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

	want := runFireSpread(t, tg.vanilla, probe)
	got := runFireSpread(t, tg.mcgo, probe)

	// One infiniburn source over ~1000 ticks: spread must have
	// produced extra fires and checkBurnOut must have eaten planks on
	// both legs.
	if want.fireCells < 3 || got.fireCells < 3 {
		t.Errorf("fire cells: vanilla=%d mc-go=%d (want >=3)", want.fireCells, got.fireCells)
	}
	// 35 planks written; at least one must burn out per leg.
	if want.planksLeft >= 35 || got.planksLeft >= 35 {
		t.Errorf("planks: vanilla=%d mc-go=%d (want <35)", want.planksLeft, got.planksLeft)
	}
	t.Logf("fire: cells v=%d g=%d, planks left v=%d g=%d",
		want.fireCells, got.fireCells, want.planksLeft, got.planksLeft)
}

func runFireSpread(t *testing.T, addr, name string) fireRun {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, name)
	defer c.Close()
	_ = c.SetDeadline(time.Time{})
	sink := newChatSinkTP(pr, pw)
	time.Sleep(3 * time.Second)
	sink.take()

	sendCmd(t, pw, "time set day")
	sendCmd(t, pw, "tp @s 10.5 -60 -40.5")
	// Netherrack floor: the single fire cell is infiniburn and never
	// burns out; the plank wall behind it burns and spreads.
	for _, cmd := range []string{
		"fill 8 -61 -52 16 -61 -48 minecraft:netherrack",
		"fill 10 -60 -52 10 -56 -48 minecraft:oak_planks",
		"setblock 11 -60 -50 minecraft:fire",
	} {
		sendCmd(t, pw, cmd)
	}
	waitDrained(t, pw, sink)

	sprintTicksLoose(t, pw, sink, 1000)
	time.Sleep(1500 * time.Millisecond)

	var out fireRun
	out.fireCells = countFill(t, pw, sink, "minecraft:fire",
		6, -62, -54, 18, -48, -46)
	out.planksLeft = countFill(t, pw, sink, "minecraft:oak_planks",
		6, -62, -54, 18, -48, -46)
	return out
}
