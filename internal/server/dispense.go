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
	"strconv"
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
	switch {
	case itemName == "bucket":
		// DispenseItemBehavior$4: a BucketPickup at the mouth fills
		// the bucket — water/lava source → *_bucket, powder snow →
		// powder_snow_bucket; otherwise the plain throw.
		fb := d.w.GetBlock(front)
		fk, lvl, isFl := fluidOf(fb)
		var fill string
		switch {
		case isFl && lvl == 0 && fk == fluidWater:
			fill = "water_bucket"
		case isFl && lvl == 0 && fk == fluidLava:
			fill = "lava_bucket"
		default:
			if bname, ok := blockNameAt(d, front); ok && bname == "powder_snow" {
				fill = "powder_snow_bucket"
			}
		}
		if fill != "" {
			s.setSilentBlock(d, front, world.Air)
			s.rsNeighborFanOut(d, front)
			if fid, ok := world.ItemIDs[fill]; ok {
				// consumeWithRemainder: one bucket shrinks off, the
				// filled bucket lands in the dispenser (thrown when
				// full).
				d.containers.mu.Lock()
				removeOne(&c.slots[slot])
				rem := ItemStack{ID: fid, Count: 1}
				slotsInsert(c.slots, &rem)
				d.containers.mu.Unlock()
				if rem.Count > 0 {
					s.spawnItemDrop(d.name, front, rem)
				}
			}
			s.rsContainerChanged(d, pos)
			s.broadcastLevelEvent(d.name, 1000, pos, 0)
			s.broadcastLevelEvent(d.name, 2000, pos, int32(facing))
			return
		}
	case dispenseFluidItem[itemName] != "":
		// DispenseItemBehavior$3 — emptyContents at the mouth:
		// water/lava/powder_snow place their fluid block; fish
		// buckets spawn their fish too (entity half deferred).
		placed := dispenseFluidItem[itemName]
		if fb := d.w.GetBlock(front); fb == world.Air || world.NonSolid(fb) {
			if nb, ok := world.BlockStateOf(placed, nil); ok {
				s.setSilentBlock(d, front, nb)
				s.wakeFluids(d, front)
				s.rsNeighborFanOut(d, front)
			}
		}
		bid, ok := world.ItemIDs["bucket"]
		d.containers.mu.Lock()
		removeOne(&c.slots[slot])
		if ok {
			rem := ItemStack{ID: bid, Count: 1}
			slotsInsert(c.slots, &rem)
			if rem.Count > 0 {
				s.spawnItemDrop(d.name, front, rem)
			}
		}
		d.containers.mu.Unlock()
		s.rsContainerChanged(d, pos)
		s.broadcastLevelEvent(d.name, 1000, pos, 0)
		s.broadcastLevelEvent(d.name, 2000, pos, int32(facing))
		return
	case dispenseMinecartEntity[itemName] != "":
		// MinecartDispenseItemBehavior: the mouth (or the cell below
		// it) must hold a rail — anything else falls back to the
		// default item throw.
		rail, slope := false, false
		fb := d.w.GetBlock(front)
		target := front
		if rn, ok := blockNameAt(d, front); ok && dispenseRailBlocks[rn] {
			rail = true
			slope = dispenseSlopeShape[rsPropsOf(fb)("shape")]
		} else if fb == world.Air {
			below := world.Pos{X: front.X, Y: front.Y - 1, Z: front.Z}
			if rn, ok := blockNameAt(d, below); ok && dispenseRailBlocks[rn] {
				rail = true
				target = below
				slope = dispenseSlopeShape[rsPropsOf(d.w.GetBlock(below))("shape")]
			}
		}
		if rail {
			s.spawnDispensedMinecart(d, target, slope, dispenseMinecartEntity[itemName])
			s.dispenseConsume(d, pos, c, slot)
			s.broadcastLevelEvent(d.name, 1000, pos, 0)
			s.broadcastLevelEvent(d.name, 2000, pos, int32(facing))
			return
		}
	case dispenseBoatEntity[itemName] != "":
		// BoatDispenseItemBehavior: the mouth must be water, or air
		// over water — any other front falls back to the throw.
		fk, _, isFl := fluidOf(d.w.GetBlock(front))
		target := front
		if !(isFl && fk == fluidWater) {
			fb := d.w.GetBlock(front)
			below := world.Pos{X: front.X, Y: front.Y - 1, Z: front.Z}
			fk2, _, isFl2 := fluidOf(d.w.GetBlock(below))
			if fb == world.Air && isFl2 && fk2 == fluidWater {
				target = below
				isFl = true
			} else {
				isFl = false
			}
		}
		if isFl {
			s.spawnDispensedBoat(d, target, dispenseBoatEntity[itemName])
			s.dispenseConsume(d, pos, c, slot)
			s.broadcastLevelEvent(d.name, 1000, pos, 0)
			s.broadcastLevelEvent(d.name, 2000, pos, int32(facing))
			return
		}
	case itemName == "tnt":
		// DispenseItemBehavior$6: the TNT item primes an entity at
		// the mouth (tnt_explodes=false fails it silently).
		if s.gameRuleBool("minecraft:tnt_explodes") {
			fc := frontCenter(front)
			s.primeTntAt(d, fc[0], fc[1], fc[2], tntFuseTicks)
			s.dispenseConsume(d, pos, c, slot)
			s.broadcastLevelEvent(d.name, 1000, pos, 0)
			s.broadcastLevelEvent(d.name, 2000, pos, int32(facing))
		}
		return
	case strings.HasSuffix(itemName, "_spawn_egg"):
		// SpawnEggItemBehavior: the mob spawns at the mouth cell.
		entName := strings.TrimSuffix(itemName, "_spawn_egg")
		if et, ok := EntityTypeID(entName); ok {
			e := s.entities.Spawn(&Entity{
				Type: et,
				Dim:  d.name,
				X:    float64(front.X) + 0.5,
				Y:    float64(front.Y),
				Z:    float64(front.Z) + 0.5,
			})
			s.broadcastEntitySpawn(nil, *e)
			s.dispenseConsume(d, pos, c, slot)
			s.broadcastLevelEvent(d.name, 1000, pos, 0)
			s.broadcastLevelEvent(d.name, 2000, pos, int32(facing))
			return
		}
	case itemName == "shulker_box" || strings.HasSuffix(itemName, "_shulker_box"):
		// ShulkerBoxDispenseBehavior — the box places as a block at
		// the mouth when the cell is replaceable.
		fb := d.w.GetBlock(front)
		if fb == world.Air || world.NonSolid(fb) {
			if nb, ok := world.BlockStateOf(itemName, nil); ok {
				s.setSilentBlock(d, front, nb)
				s.rsNeighborFanOut(d, front)
				s.dispenseConsume(d, pos, c, slot)
				s.broadcastLevelEvent(d.name, 1000, pos, 0)
				s.broadcastLevelEvent(d.name, 2000, pos, int32(facing))
				return
			}
		}
		return
	case itemName == "potion":
		// DispenseItemBehavior$13: a water potion converts a
		// convertable_to_mud block at the mouth to mud and leaves a
		// glass bottle.
		if st.Potion == "water" {
			if bn, ok := blockNameAt(d, front); ok && dispenseToMud[bn] {
				if nb, ok := world.BlockStateOf("mud", nil); ok {
					s.setSilentBlock(d, front, nb)
					s.rsNeighborFanOut(d, front)
				}
				if bid, ok := world.ItemIDs["glass_bottle"]; ok {
					d.containers.mu.Lock()
					removeOne(&c.slots[slot])
					rem := ItemStack{ID: bid, Count: 1}
					slotsInsert(c.slots, &rem)
					d.containers.mu.Unlock()
					if rem.Count > 0 {
						s.spawnItemDrop(d.name, front, rem)
					}
				}
				s.rsContainerChanged(d, pos)
				s.broadcastLevelEvent(d.name, 1000, pos, 0)
				s.broadcastLevelEvent(d.name, 2000, pos, int32(facing))
				return
			}
		}
	case itemName == "glass_bottle":
		// DispenseItemBehavior$9: a water source at the mouth fills
		// the bottle (beehive honey handled by honey bottles later).
		fk, lvl, isFl := fluidOf(d.w.GetBlock(front))
		if isFl && fk == fluidWater && lvl == 0 {
			s.setSilentBlock(d, front, world.Air)
			s.rsNeighborFanOut(d, front)
			if fid, ok := world.ItemIDs["potion"]; ok {
				d.containers.mu.Lock()
				removeOne(&c.slots[slot])
				rem := ItemStack{ID: fid, Count: 1, Potion: "water"}
				slotsInsert(c.slots, &rem)
				d.containers.mu.Unlock()
				if rem.Count > 0 {
					s.spawnItemDrop(d.name, front, rem)
				}
			}
			s.rsContainerChanged(d, pos)
			s.broadcastLevelEvent(d.name, 1000, pos, 0)
			s.broadcastLevelEvent(d.name, 2000, pos, int32(facing))
			return
		}
	case itemName == "glowstone":
		// DispenseItemBehavior$10: charge a respawn anchor at the
		// mouth (charge < 4) and shrink.
		fb := d.w.GetBlock(front)
		if bn, ok := blockNameAt(d, front); ok && bn == "respawn_anchor" {
			if cv, err := strconv.Atoi(rsPropsOf(fb)("charges")); err == nil && cv < 4 {
				if nb, ok := world.WithProperty(fb, "charges", strconv.Itoa(cv+1)); ok {
					s.rsWriteBlockFlag2(d, front, nb)
					s.dispenseConsume(d, pos, c, slot)
				}
				s.broadcastLevelEvent(d.name, 1000, pos, 0)
				s.broadcastLevelEvent(d.name, 2000, pos, int32(facing))
				return
			}
			return
		}
	case itemName == "honeycomb":
		// DispenseItemBehavior$12: wax a waxable copper variant at
		// the mouth (same name with the waxed_ prefix).
		if bn, ok := blockNameAt(d, front); ok && dispenseWaxable[bn] {
			if nb, ok := world.BlockStateOf("waxed_"+bn, nil); ok {
				s.rsWriteBlockFlag2(d, front, nb)
				s.dispenseConsume(d, pos, c, slot)
				s.broadcastLevelEvent(d.name, 1000, pos, 0)
				s.broadcastLevelEvent(d.name, 2000, pos, int32(facing))
				return
			}
		}
		return
	case itemName == "shears":
		// ShearsDispenseItemBehavior: shear a full beehive/nest at
		// the mouth → honeycomb drop + honey_level reset.
		fb := d.w.GetBlock(front)
		if bn, ok := blockNameAt(d, front); ok && (bn == "beehive" || bn == "bee_nest") {
			if hl, err := strconv.Atoi(rsPropsOf(fb)("honey_level")); err == nil && hl >= 5 {
				if nb, ok := world.WithProperty(fb, "honey_level", "0"); ok {
					s.rsWriteBlockFlag2(d, front, nb)
				}
				if hid, ok := world.ItemIDs["honeycomb"]; ok {
					s.spawnItemDrop(d.name, front, ItemStack{ID: hid, Count: 3})
				}
				s.broadcastLevelEvent(d.name, 1000, pos, 0)
				s.broadcastLevelEvent(d.name, 2000, pos, int32(facing))
				return
			}
			return
		}
	case itemName == "flint_and_steel":
		// FlintAndSteelDispenseItemBehavior: prime a TNT/explosive
		// entity at the mouth, else place fire where it can burn,
		// else light a campfire/candle — else nothing (optional
		// behavior fails silently, the item stays).
		fb := d.w.GetBlock(front)
		if bn, ok := blockNameAt(d, front); ok && bn == "tnt" {
			s.setNaturalBlock(d, front, world.Air)
			s.primeTntBlock(d, front, tntFuseTicks)
		} else if fb == world.Air || world.NonSolid(fb) {
			// BaseFireBlock.canBePlacedAt simplified: air mouth over
			// a supporting face takes the plain fire state.
			if nb, ok := world.BlockStateOf("fire", nil); ok {
				s.setSilentBlock(d, front, nb)
				s.rsNeighborFanOut(d, front)
			}
		} else if p := rsPropsOf(fb)("lit"); p == "false" {
			if bn, ok := blockNameAt(d, front); ok &&
				(strings.HasSuffix(bn, "campfire") || strings.HasSuffix(bn, "candle")) {
				if nb, ok := world.WithProperty(fb, "lit", "true"); ok {
					s.rsWriteBlockFlag2(d, front, nb)
				}
			}
		}
		s.broadcastLevelEvent(d.name, 1000, pos, 0)
		s.broadcastLevelEvent(d.name, 2000, pos, int32(facing))
		return
	}
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
	// Potions carry their contents into the splash/cloud fields.
	if entName == "splash_potion" || entName == "lingering_potion" {
		e.splashPotion = st.Potion
		for _, eff := range splashPotionContents[st.Potion] {
			if eid, ok := MobEffectID(eff.name); ok {
				if e.Effects == nil {
					e.Effects = map[int32]mobEffectInst{}
				}
				e.Effects[eid] = mobEffectInst{amplifier: eff.amp,
					expiry: s.gameTime() + int64(eff.dur)}
			}
		}
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

// dispenseFluidItem — DispensibleContainerItem bucket families: the
// block each places at the mouth. Fish-mob buckets also spawn the
// fish itself (deferred — the water half still lands).
var dispenseFluidItem = map[string]string{
	"water_bucket":         "water",
	"lava_bucket":          "lava",
	"powder_snow_bucket":   "powder_snow",
	"salmon_bucket":        "water",
	"cod_bucket":           "water",
	"pufferfish_bucket":    "water",
	"tropical_fish_bucket": "water",
	"axolotl_bucket":       "water",
	"tadpole_bucket":       "water",
}

var dispenseMinecartEntity = map[string]string{
	"minecart":               "minecart",
	"chest_minecart":         "chest_minecart",
	"furnace_minecart":       "furnace_minecart",
	"tnt_minecart":           "tnt_minecart",
	"hopper_minecart":        "hopper_minecart",
	"command_block_minecart": "command_block_minecart",
}

var dispenseRailBlocks = map[string]bool{
	"rail": true, "powered_rail": true,
	"detector_rail": true, "activator_rail": true,
}

var dispenseSlopeShape = map[string]bool{
	"ascending_east": true, "ascending_west": true,
	"ascending_north": true, "ascending_south": true,
}

var dispenseBoatEntity = map[string]string{
	"oak_boat": "oak_boat", "spruce_boat": "spruce_boat",
	"birch_boat": "birch_boat", "jungle_boat": "jungle_boat",
	"dark_oak_boat": "dark_oak_boat", "acacia_boat": "acacia_boat",
	"cherry_boat": "cherry_boat", "mangrove_boat": "mangrove_boat",
	"pale_oak_boat": "pale_oak_boat", "bamboo_raft": "bamboo_raft",
	"oak_chest_boat": "oak_chest_boat", "spruce_chest_boat": "spruce_chest_boat",
	"birch_chest_boat": "birch_chest_boat", "jungle_chest_boat": "jungle_chest_boat",
	"dark_oak_chest_boat": "dark_oak_chest_boat", "acacia_chest_boat": "acacia_chest_boat",
	"cherry_chest_boat": "cherry_chest_boat", "mangrove_chest_boat": "mangrove_chest_boat",
	"pale_oak_chest_boat": "pale_oak_chest_boat", "bamboo_chest_raft": "bamboo_chest_raft",
}

// spawnDispensedMinecart — createMinecart at the rail offset:
// flat rail y+0.1, slope y+0.6 (or the below-rail variants —0.9/—0.4
// folded in via the target cell).
func (s *Server) spawnDispensedMinecart(d *dimension, target world.Pos, slope bool, entName string) {
	et, ok := EntityTypeID(entName)
	if !ok {
		return
	}
	y := float64(target.Y) + 0.1
	if slope {
		y = float64(target.Y) + 0.6
	}
	e := s.entities.Spawn(&Entity{
		Type: et,
		Dim:  d.name,
		X:    float64(target.X) + 0.5,
		Y:    y,
		Z:    float64(target.Z) + 0.5,
	})
	s.broadcastEntitySpawn(nil, *e)
}

// spawnDispensedBoat — the boat lands floating on the water surface.
func (s *Server) spawnDispensedBoat(d *dimension, target world.Pos, entName string) {
	et, ok := EntityTypeID(entName)
	if !ok {
		return
	}
	e := s.entities.Spawn(&Entity{
		Type: et,
		Dim:  d.name,
		X:    float64(target.X) + 0.5,
		Y:    float64(target.Y) + 1.0,
		Z:    float64(target.Z) + 0.5,
	})
	s.broadcastEntitySpawn(nil, *e)
}

// frontCenter — the block-centre point one step out from the
// dispenser, where primed TNT spawns.
func frontCenter(pos world.Pos) [3]float64 {
	return [3]float64{float64(pos.X) + 0.5, float64(pos.Y) + 0.5, float64(pos.Z) + 0.5}
}

// dispenseWaxable — HoneycombItem.getWaxed: copper-family names that
// take a waxed_ variant (signs included).
var dispenseWaxable = func() map[string]bool {
	m := map[string]bool{}
	// The copper family: base + 3 oxidation stages per shape.
	shapes := []string{
		"copper_block", "cut_copper", "cut_copper_stairs",
		"cut_copper_slab", "chiseled_copper", "copper_grate",
		"copper_bulb", "copper_door", "copper_trapdoor",
	}
	prefixes := []string{"", "exposed_", "weathered_", "oxidized_"}
	for _, p := range prefixes {
		for _, sh := range shapes {
			name := p + sh
			if world.BlockHasName(name) && world.BlockHasName("waxed_"+name) {
				m[name] = true
			}
		}
	}
	return m
}()

var dispenseToMud = map[string]bool{
	"dirt": true, "coarse_dirt": true, "rooted_dirt": true,
}
