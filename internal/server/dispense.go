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
	itemName, _ := world.ItemName(st.ID)
	if itemName == "armor_stand" {
		// DispenseItemBehavior$1: the stand spawns IN the mouth cell
		// facing the way the dispenser faces, one item consumed.
		s.spawnDispensedArmorStand(d, front, facing)
		s.dispenseConsume(d, pos, c, slot)
		s.broadcastLevelEvent(d.name, 1000, pos, 0)
		s.broadcastLevelEvent(d.name, 2000, pos, int32(facing))
		return
	}
	if entName, ok := dispenseProjectileEntity[itemName]; ok {
		// ProjectileDispenseBehavior: asProjectile at
		// center + facing*0.7 + (0,0.1,0), shot along the facing at
		// power 1.1, and the sound is event 1002 (or the item's
		// override — fire_charge/wind_charge take 1018, rockets 1004).
		s.spawnDispensedProjectile(d, pos, facing, entName, st)
		s.dispenseConsume(d, pos, c, slot)
		ev := dispenseProjectileEvent[itemName]
		if ev == 0 {
			ev = 1002
		}
		s.broadcastLevelEvent(d.name, ev, pos, 0)
		s.broadcastLevelEvent(d.name, 2000, pos, int32(facing))
		return
	}
	// DefaultDispenseItemBehavior: one item thrown out of the mouth as
	// an item entity, with the 1000 click + 2000 stream animation.
	one := st
	one.Count = 1
	s.spawnItemDrop(d.name, front, one)
	s.dispenseConsume(d, pos, c, slot)
	s.rsContainerChanged(d, pos)
	s.broadcastLevelEvent(d.name, 1000, pos, 0)
	s.broadcastLevelEvent(d.name, 2000, pos, int32(facing))
}

// dispenseConsume shrinks the picked slot under the container lock —
// ItemStack.split(1) in the behavior contract.
func (s *Server) dispenseConsume(d *dimension, pos world.Pos, c *containerData, slot int) {
	d.containers.mu.Lock()
	removeOne(&c.slots[slot])
	d.containers.mu.Unlock()
	s.rsContainerChanged(d, pos)
}

// dispenseProjectileEntity maps the registerProjectileBehavior'd
// items to their projectile entity types (ThrowablePotion splits by
// potion kind — splash vs lingering — the same way
// ThrowablePotionItem.createPotion picks the entity class).
var dispenseProjectileEntity = map[string]string{
	"arrow":             "arrow",
	"tipped_arrow":      "arrow",
	"spectral_arrow":    "spectral_arrow",
	"egg":               "egg",
	"blue_egg":          "egg",
	"brown_egg":         "egg",
	"snowball":          "snowball",
	"experience_bottle": "experience_bottle",
	"splash_potion":     "splash_potion",
	"lingering_potion":  "lingering_potion",
	"fire_charge":       "small_fireball",
	"wind_charge":       "wind_charge",
	"firework_rocket":   "firework_rocket",
}

// dispenseProjectileEvent is ProjectileDispenseBehavior.playSound:
// overrideDispenseEvent.orElse(1002).
var dispenseProjectileEvent = map[string]int32{
	"fire_charge":     1018,
	"wind_charge":     1018,
	"firework_rocket": 1004,
}

// spawnDispensedProjectile is Projectile.spawnProjectileUsingShoot:
// the entity appears at the mouth position flying along the facing at
// power 1.1 with the 6.0-uncertainty triangular jitter.
func (s *Server) spawnDispensedProjectile(d *dimension, pos world.Pos, facing rsDir, entName string, st ItemStack) {
	et, ok := EntityTypeID(entName)
	if !ok {
		return
	}
	o := rsOff[facing]
	px := float64(pos.X) + 0.5 + float64(o.X)*0.7
	py := float64(pos.Y) + 0.5 + float64(o.Y)*0.7 + 0.1
	pz := float64(pos.Z) + 0.5 + float64(o.Z)*0.7
	// Projectile.shoot: normalize(dir + triangle(0.0172275*6) jitter)
	// * 1.1 — the facing is axis-aligned so it is already a unit vec.
	const u = 0.0172275 * 6.0
	vx := (float64(o.X) + (rand.Float64()-rand.Float64())*u) * 1.1
	vy := (float64(o.Y) + (rand.Float64()-rand.Float64())*u) * 1.1
	vz := (float64(o.Z) + (rand.Float64()-rand.Float64())*u) * 1.1
	var fuse int32
	if entName == "firework_rocket" {
		fuse = 40
	}
	e := s.entities.Spawn(&Entity{
		Type:    et,
		Dim:     d.name,
		X:       px,
		Y:       py,
		Z:       pz,
		MotionX: vx,
		MotionY: vy,
		MotionZ: vz,
		Fuse:    fuse,
	})
	s.broadcastEntitySpawn(nil, *e)
	// ThrownItemEntity carries its item for rendering; the arrow
	// types take no entity data.
	switch entName {
	case "egg", "snowball", "experience_bottle",
		"splash_potion", "lingering_potion":
		one := st
		one.Count = 1
		e.Item = one
		s.broadcastEntityMetadata(e.ID, encodeItemMetadata(one))
	}
}

// spawnDispensedArmorStand is DispenseItemBehavior$1: the stand lands
// in the cell the dispenser faces, yaw aligned to the dispenser.
func (s *Server) spawnDispensedArmorStand(d *dimension, front world.Pos, facing rsDir) {
	et, ok := EntityTypeID("armor_stand")
	if !ok {
		return
	}
	e := s.entities.Spawn(&Entity{
		Type: et,
		Dim:  d.name,
		X:    float64(front.X) + 0.5,
		Y:    float64(front.Y),
		Z:    float64(front.Z) + 0.5,
		Yaw:  rsDirToYRot(facing),
	})
	s.broadcastEntitySpawn(nil, *e)
}

// rsDirToYRot is Direction.toYRot — south 0, west 90, north 180,
// east 270; up/down keep the last yaw (0 here).
func rsDirToYRot(d rsDir) float32 {
	switch d {
	case rsSouth:
		return 0
	case rsWest:
		return 90
	case rsNorth:
		return 180
	case rsEast:
		return 270
	}
	return 0
}
