package world

import (
	"testing"

	"mc-go/internal/event"
)

func journalWorld(t *testing.T) *World {
	t.Helper()
	w := NewGen(event.New(), 4, func(Pos) Block { return Stone })
	t.Cleanup(w.Stop)
	return w
}

func TestBlockChangesInterestUnionAndFinalRead(t *testing.T) {
	w := journalWorld(t)
	min, max := Pos{-17, 3, -1}, Pos{-15, 4, 1}
	first, err := w.AddBlockInterest(min, max)
	if err != nil {
		t.Fatal(err)
	}
	second, err := w.AddBlockInterest(Pos{-16, 3, -1}, Pos{-14, 4, 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(w.changes.cells) != 24 {
		t.Fatalf("interest union cells=%d", len(w.changes.cells))
	}
	pos := Pos{-16, 3, -1}
	for i := 0; i < 1000; i++ {
		w.SetBlock(pos, Dirt)
		w.SetBlock(pos, Air)
	}
	w.SetBlock(Pos{-18, 3, -1}, Dirt)
	if len(w.changes.dirty) != 1 {
		t.Fatalf("dirty=%d, want one coalesced interested cell", len(w.changes.dirty))
	}
	changes := w.DrainBlockChanges()
	if len(changes) != 1 || changes[0] != (BlockChange{Position: pos, State: Air}) {
		t.Fatalf("final read=%+v", changes)
	}
	if len(w.DrainBlockChanges()) != 0 {
		t.Fatal("drain replayed changes")
	}
	w.SetBlock(pos, Dirt)
	w.SetBlock(Pos{-17, 3, -1}, Air)
	w.RemoveBlockInterest(first)
	if len(w.changes.cells) != 18 || len(w.changes.dirty) != 1 {
		t.Fatalf("remove lost shared interest or retained stale dirt: cells%d dirty%d", len(w.changes.cells), len(w.changes.dirty))
	}
	changes = w.DrainBlockChanges()
	if len(changes) != 1 || changes[0].Position != pos || changes[0].State != Dirt {
		t.Fatalf("shared interest changes=%+v", changes)
	}
	w.RemoveBlockInterest(second)
	if w.changes.enabled.Load() || len(w.changes.cells) != 0 || len(w.changes.dirty) != 0 {
		t.Fatal("closed watches retained journal storage")
	}
	w.SetBlock(pos, Air)
	if len(w.DrainBlockChanges()) != 0 {
		t.Fatal("write retained without any interest")
	}
}

func TestBlockChangesRestoreRecordsDeletedOverridesAndFinalDefaults(t *testing.T) {
	w := journalWorld(t)
	deleted, replaced, added := Pos{-1, 4, 0}, Pos{0, 4, 0}, Pos{1, 4, 0}
	w.SetBlock(deleted, Air)
	w.SetBlock(replaced, Air)
	id, err := w.AddBlockInterest(deleted, added)
	if err != nil {
		t.Fatal(err)
	}
	defer w.RemoveBlockInterest(id)
	w.Restore(map[Pos]Block{replaced: Dirt, added: Air})
	changes := w.DrainBlockChanges()
	if len(changes) != 3 {
		t.Fatalf("restore changes=%+v", changes)
	}
	want := map[Pos]Block{deleted: Stone, replaced: Dirt, added: Air}
	for _, change := range changes {
		if change.State != want[change.Position] {
			t.Fatalf("restore change=%+v", change)
		}
		delete(want, change.Position)
	}
	if len(want) != 0 {
		t.Fatalf("missing restore mutations=%v", want)
	}
}

func TestBlockChangesRawNoopsAndRevertsAreCoalescedForBaselineFiltering(t *testing.T) {
	w := journalWorld(t)
	pos := Pos{-16, 1, -17}
	id, err := w.AddBlockInterest(pos, pos)
	if err != nil {
		t.Fatal(err)
	}
	defer w.RemoveBlockInterest(id)
	w.SetBlock(pos, Stone)
	w.SetBlock(pos, Dirt)
	w.SetBlock(pos, Stone)
	changes := w.DrainBlockChanges()
	if len(changes) != 1 || changes[0].State != Stone {
		t.Fatalf("net unchanged final value=%+v", changes)
	}
	// The server's watch baseline suppresses this net unchanged cell; the
	// storage journal intentionally records invalidation, not wire events.
}

func TestBlockChangesRejectInvalidInterestBeforeAllocating(t *testing.T) {
	w := journalWorld(t)
	maxInt := int(^uint(0) >> 1)
	for _, bounds := range [][2]Pos{
		{{1, 0, 0}, {0, 0, 0}},
		{{maxInt, 0, 0}, {-maxInt - 1, 0, 0}},
		{{0, 0, 0}, {65536, 0, 0}},
		{{0, 0, 0}, {255, 255, 1}},
	} {
		if _, err := w.AddBlockInterest(bounds[0], bounds[1]); err == nil {
			t.Fatalf("accepted bounds=%v", bounds)
		}
	}
	if len(w.changes.cells) != 0 || w.changes.enabled.Load() {
		t.Fatal("invalid interest allocated journal storage")
	}
}

func TestReadBlocksSparseBarrierAcrossShards(t *testing.T) {
	w := journalWorld(t)
	positions := make([]Pos, 127)
	for i := range positions {
		positions[i] = Pos{X: i*17 - 900, Y: i % 7, Z: -i * 13}
		w.SetBlock(positions[i], Block(i%3))
	}
	out := make([]Block, len(positions))
	w.ReadBlocks(positions, out)
	for i, b := range out {
		if b != Block(i%3) {
			t.Fatalf("sparse read%d=%d", i, b)
		}
	}
}
