package server

import (
	"mc-go/internal/world"
	"reflect"
	"testing"
)

func queuedBlockKey(dim string, x int, name string) schedKey {
	return schedKey{dim: dim, pos: world.Pos{X: x}, kind: tickBlock, block: name}
}
func TestCloneBlockTicksDeadlinesPriorityAndDomains(t *testing.T) {
	q := newTickScheduler()
	a := queuedBlockKey("src", 1, "stone_button")
	b := queuedBlockKey("src", 2, "oak_button")
	outside := queuedBlockKey("src", 9, "stone_button")
	fluid := schedKey{dim: "src", pos: world.Pos{X: 1}, kind: tickFluid}
	q.schedulePrio(a, 20, tickPrioHigh)
	q.schedulePrio(b, 25, tickPrioExtremelyHigh)
	q.schedule(outside, 30)
	q.schedule(fluid, 5)
	existing := queuedBlockKey("dst", 11, "stone_button")
	q.schedulePrio(existing, 27, tickPrioVeryHigh)
	q.copyBlockTicks("src", "dst", world.Pos{X: 1}, world.Pos{X: 2}, world.Pos{X: 10})
	if at, _ := q.queuedAt(existing); at != 27 || q.prio[existing] != tickPrioVeryHigh {
		t.Fatal("copied entry replaced existing destination")
	}
	dest := queuedBlockKey("dst", 12, "oak_button")
	if at, _ := q.queuedAt(dest); at != 25 || q.prio[dest] != tickPrioExtremelyHigh {
		t.Fatal("deadline/priority not preserved")
	}
	if q.hasScheduled(queuedBlockKey("dst", 19, "stone_button")) || q.hasScheduled(schedKey{dim: "dst", pos: world.Pos{X: 11}, kind: tickFluid}) {
		t.Fatal("copied outside box or fluid domain")
	}
	q.copyBlockTicks("src", "other", world.Pos{X: 1}, world.Pos{X: 1}, world.Pos{X: 20})
	dest = queuedBlockKey("other", 21, "stone_button")
	if at, _ := q.queuedAt(dest); at != 20 || q.prio[dest] != tickPrioHigh {
		t.Fatal("new destination lost original metadata")
	}
	q.schedule(queuedBlockKey("other", 0, "stone_button"), 20)
	var order []int
	for _, k := range q.poll(20) {
		if k.dim == "other" {
			order = append(order, k.pos.X)
		}
	}
	if !reflect.DeepEqual(order, []int{21, 0}) {
		t.Fatalf("copied priority dispatch%v", order)
	}
}
func TestCloneBlockTicksOverlapUsesSourceSnapshot(t *testing.T) {
	q := newTickScheduler()
	q.schedule(queuedBlockKey("src", 1, "stone_button"), 20)
	q.schedule(queuedBlockKey("src", 2, "oak_button"), 37)
	q.copyBlockTicks("src", "src", world.Pos{X: 1}, world.Pos{X: 2}, world.Pos{X: 1})
	if q.pending() != 4 || q.hasScheduled(queuedBlockKey("src", 3, "stone_button")) {
		t.Fatal("new target entries recursively entered source snapshot")
	}
	if at, _ := q.queuedAt(queuedBlockKey("src", 2, "stone_button")); at != 20 {
		t.Fatal("overlap stone deadline")
	}
	if at, _ := q.queuedAt(queuedBlockKey("src", 3, "oak_button")); at != 37 {
		t.Fatal("overlap oak deadline")
	}
}
func TestCloneBlockTicksActiveClearAndRemainingOrder(t *testing.T) {
	q := newTickScheduler()
	var blocks []schedKey
	for x := 1; x <= 5; x++ {
		k := queuedBlockKey("src", x, "stone_button")
		blocks = append(blocks, k)
		priority := tickPrioNormal
		if x == 1 {
			priority = tickPrioHigh
		}
		q.schedulePrio(k, 5, priority)
	}
	future := queuedBlockKey("src", 3, "oak_button")
	q.schedule(future, 9)
	fluid := schedKey{dim: "src", pos: world.Pos{X: 3}, kind: tickFluid}
	futureFluid := schedKey{dim: "src", pos: world.Pos{X: 3, Y: 1}, kind: tickFluid}
	q.schedule(fluid, 5)
	q.schedule(futureFluid, 9)
	due := q.poll(5)
	if !q.claimBlockTick(blocks[0]) || !q.claimBlockTick(blocks[1]) {
		t.Fatal("could not enter first callbacks")
	}
	q.clearBlockArea("src", world.Pos{X: 2}, world.Pos{X: 3, Y: 1})
	q.copyBlockTicks("src", "dst", world.Pos{X: 1}, world.Pos{X: 5, Y: 1}, world.Pos{X: 10})
	copiedDone := queuedBlockKey("dst", 11, "stone_button")
	if at, _ := q.queuedAt(copiedDone); at != 5 || q.prio[copiedDone] != tickPrioHigh {
		t.Fatal("already-run metadata not captured")
	}
	for _, x := range []int{14, 15} {
		k := queuedBlockKey("dst", x, "stone_button")
		if at, ok := q.queuedAt(k); !ok || at != 5 || q.prio[k] != tickPrioNormal {
			t.Fatalf("remaining collected callback %d not copied with metadata", x)
		}
	}
	for _, k := range []schedKey{queuedBlockKey("dst", 12, "stone_button"), queuedBlockKey("dst", 13, "stone_button"), queuedBlockKey("dst", 13, "oak_button")} {
		if q.hasScheduled(k) {
			t.Fatal("canceled entry revived in copy")
		}
	}
	var order []int
	for _, k := range due {
		if k.kind != tickFluid && q.claimBlockTick(k) {
			order = append(order, k.pos.X)
		}
	}
	if !reflect.DeepEqual(order, []int{4, 5}) {
		t.Fatalf("remaining dispatch order%v", order)
	}
	if !q.claimBlockTick(fluid) || !q.hasScheduled(futureFluid) {
		t.Fatal("block clear canceled fluid work")
	}
	q.finishBlockTicks()
	q.copyBlockTicks("src", "later", world.Pos{X: 1}, world.Pos{X: 5, Y: 1}, world.Pos{X: 20})
	if q.hasScheduled(queuedBlockKey("later", 21, "stone_button")) {
		t.Fatal("active window survived tick completion")
	}
}
