package server

import (
	"mc-go/internal/world"
	"testing"
)

func TestTrappedChestSignalDirectionsAndClamp(t *testing.T) {
	s := New("unused")
	d := s.dims["minecraft:overworld"]
	pos := world.Pos{X: 1, Y: 5, Z: 2}
	block, _ := world.BlockState("trapped_chest")
	d.w.SetBlock(pos, block)
	storage := s.chestAt(d, pos)
	storage.openers = 17
	for direction := rsDown; direction <= rsEast; direction++ {
		if got := rsGetSignal(d, pos, direction); got != 15 {
			t.Errorf("weak direction%d=%d", direction, got)
		}
		want := 0
		if direction == rsUp {
			want = 15
		}
		if got := rsGetDirectSignal(d, pos, direction); got != want {
			t.Errorf("strong direction%d=%d want%d", direction, got, want)
		}
	}
	storage.openers = -1
	if got := trappedChestPower(d, pos); got != 0 {
		t.Errorf("negative opener count powered%d", got)
	}
}
