package server

import (
	"mc-go/internal/world"
	"testing"
)

func TestHopperSetblockIngressDefaultsAndReplacement(t *testing.T) {
	s := New("unused")
	d := s.dims[dimOverworld]
	pos := world.Pos{X: 8, Y: -60, Z: 8}
	place := func(spec string) { t.Helper(); s.runSetblock("", []string{"8", "-60", "8", spec}) }
	place(`hopper{TransferCooldown:23,Items:[{Slot:0b,id:"minecraft:stone",count:2},{Slot:4b,id:"minecraft:apple",count:3},{Slot:5b,id:"minecraft:emerald",count:11}]}`)
	h := s.hopperAt(d, pos)
	if h.cooldown != 23 || h.slots[0].ID != world.ItemIDs["stone"] || h.slots[0].Count != 2 || h.slots[4].ID != world.ItemIDs["apple"] || h.slots[4].Count != 3 {
		t.Fatalf("explicit placed hopper=%+v", h)
	}
	for i := 1; i < 4; i++ {
		if !h.slots[i].Empty() {
			t.Fatalf("unexpected slot%d=%+v", i, h.slots[i])
		}
	}
	place(`hopper{Items:[{Slot:4b,id:"minecraft:apple",count:3}]}`)
	h = s.hopperAt(d, pos)
	if h.cooldown != -1 || !h.slots[0].Empty() || h.slots[4].Count != 3 {
		t.Fatalf("same-state omitted cooldown/replace=%+v", h)
	}
	place(`hopper{TransferCooldown:0,Items:[]}`)
	h = s.hopperAt(d, pos)
	if h.cooldown != 0 {
		t.Fatalf("explicit zero cooldown=%d", h.cooldown)
	}
	for i, item := range h.slots {
		if !item.Empty() {
			t.Fatalf("empty replacement slot%d=%+v", i, item)
		}
	}
	place(`hopper{}`)
	if h = s.hopperAt(d, pos); h.cooldown != -1 {
		t.Fatalf("empty compound default=%d", h.cooldown)
	}
	place("air")
	place("hopper")
	if h = s.hopperAt(d, pos); h.cooldown != -1 {
		t.Fatalf("fresh no-NBT default=%d", h.cooldown)
	}
}
