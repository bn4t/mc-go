package server

// dispense.go — DispenserBlock + DropperBlock: the TRIGGERED latch on
// neighborChanged (a rising edge while untriggered schedules the
// dispense 4 ticks out; falling edge unlatches), the tick's
// dispenseFrom — a uniform pick among non-empty slots — and the two
// eject paths: a dropper pushes one item into the container at its
// mouth (HopperBlockEntity.addItem), otherwise both throw the item
// as an entity (DefaultDispenseItemBehavior). The per-item
// DispenseItemBehavior table (projectiles, buckets, boats, spawn
// eggs, ...) is not modelled — every item takes the default path.

import (
	"math/rand"
	"mc-go/internal/world"
	"strings"
)

// rsDispenserNeighbor is DispenserBlock.neighborChanged, which
// DropperBlock inherits: powered covers the QC cell above too.
func (s *Server) rsDispenserNeighbor(d *dimension, pos world.Pos, blk world.Block) {
	powered := rsHasNeighborSignal(d, pos) ||
		rsHasNeighborSignal(d, world.Pos{X: pos.X, Y: pos.Y + 1, Z: pos.Z})
	triggered := rsPropsOf(blk)("triggered") == "true"
	switch {
	case powered && !triggered:
		s.ticks.schedule(schedKey{dim: d.name, pos: pos, kind: tickBlock},
			s.TickCount()+4)
		if nb, ok := world.WithProperty(blk, "triggered", "true"); ok {
			s.rsWriteBlockFlag2(d, pos, nb)
		}
	case !powered && triggered:
		if nb, ok := world.WithProperty(blk, "triggered", "false"); ok {
			s.rsWriteBlockFlag2(d, pos, nb)
		}
	}
}

// dispenseFrom is DispenserBlock.tick's body (and DropperBlock's
// override of it): an empty store fails with the 1001 click; a filled
// one picks a random non-empty slot and acts on one item.
func (s *Server) dispenseFrom(d *dimension, pos world.Pos, blk world.Block) {
	owner, _ := world.StateOwner(blk)
	name := strings.TrimPrefix(owner, "minecraft:")
	c := s.containerAt(d, pos)
	if c == nil {
		return
	}
	d.containers.mu.Lock()
	filled := 0
	for i := range c.slots {
		if !c.slots[i].Empty() {
			filled++
		}
	}
	slot := -1
	if filled > 0 {
		k := rand.Intn(filled)
		for i := range c.slots {
			if c.slots[i].Empty() {
				continue
			}
			if k == 0 {
				slot = i
				break
			}
			k--
		}
	}
	if slot < 0 {
		d.containers.mu.Unlock()
		// Empty store: the fail click (levelEvent 1001), nothing else.
		s.broadcastLevelEvent(d.name, 1001, pos, 0)
		return
	}
	st := c.slots[slot]
	d.containers.mu.Unlock()
	if st.Empty() {
		return
	}
	facing, ok := rsDirOfName[rsPropsOf(blk)("facing")]
	if !ok {
		return
	}
	front := rsShiftPos(pos, facing)
	if name == "dropper" {
		// DropperBlock.dispenseFrom: a container at the mouth eats one
		// item via the hopper insert path (sided rules included) —
		// quietly, no dispense sound — and only falls back to the
		// entity throw when the mouth has no container.
		fb := d.w.GetBlock(front)
		_, isCont := containerSpecFor(fb)
		isCont = isCont || isChestBlock(fb) || isHopperBlock(fb) ||
			isFurnaceBlock(fb)
		if isCont {
			one := st
			one.Count = 1
			if s.hopperInsert(d, front, pos, one) {
				d.containers.mu.Lock()
				removeOne(&c.slots[slot])
				d.containers.mu.Unlock()
				s.rsContainerChanged(d, pos)
				s.rsContainerChanged(d, front)
			}
			return
		}
	}
	// DefaultDispenseItemBehavior: one item thrown out of the mouth as
	// an item entity, with the 1000 click + 2000 stream animation.
	one := st
	one.Count = 1
	s.spawnItemDrop(d.name, front, one)
	d.containers.mu.Lock()
	removeOne(&c.slots[slot])
	d.containers.mu.Unlock()
	s.rsContainerChanged(d, pos)
	s.broadcastLevelEvent(d.name, 1000, pos, 0)
	s.broadcastLevelEvent(d.name, 2000, pos, int32(facing))
}
