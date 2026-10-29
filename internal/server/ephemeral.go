package server

import (
	"math"
	"strings"

	"mc-go/internal/world"
)

// Summon coverage for the remaining non-living entity types, measured
// off the 26.2 jar (SummonCommand → EntityType.create accepts every
// registry type; each constructor carries its own defaults and its
// own per-tick end condition):
//
//   - ThrowableProjectile family (snowball, egg, ender_pearl,
//     experience_bottle, splash_potion, lingering_potion, llama_spit):
//     spawned with the summon SNBT's Motion list; the arrowTickStep
//     arm already integrates the flying ones.
//   - fishing_bobber: FishingHook.tick discards instantly when
//     getPlayerOwner() is null — every summon.
//   - evoker_fangs: warmupDelayTicks 0, lifeTicks 22 → the fang
//     bites (entity event 4) on its first tick, deals its hit at
//     warmup -8 and discards when lifeTicks runs out (~23 ticks) —
//     fangTick in evoker.go.
//   - eye_of_ender: EyeOfEnder.tick (endereye.go) — a summoned eye has
//     no target and surviveAfterDeath false, so it drifts on its Motion
//     and shatters (level event 2003) past 80 ticks.
//   - ominous_item_spawner: spawnItemAfterTicks rolls
//     nextIntBetweenInclusive(60,120); tickCount reaching it fires
//     spawnItem() + kill(). A bare summon carries ItemStack.EMPTY,
//     so nothing drops.
//   - hanging entities (painting, item_frame, glow_item_frame):
//     BlockAttachedEntity.tick's checkInterval-100 survives() pass —
//     no support → discard + dropItem (the frame/painting item).
//     leash_knot rides the existing knotTickStep (fence check);
//     its pop drops the lead.
//   - falling_block: applyGravity 0.04/tick; on ground the entity's
//     blockPosition becomes the block (setBlock flag 3) and the
//     entity discards — or drops its item when the cell can't be
//     placed.
//   - item: ItemEntity — the existing itemTickStep ages it to the
//     6000-tick despawn like any drop.
//   - marker-lite types (block_display, item_display, text_display,
//     interaction, end_crystal): summoned, live, and inert.

// summonThrownNames are ThrowableProjectile-family types the /summon
// arm accepts with just a Motion list.
var summonThrownNames = map[string]bool{
	"snowball": true, "egg": true, "ender_pearl": true,
	"experience_bottle": true, "splash_potion": true,
	"lingering_potion": true, "llama_spit": true,
}

// summonPlainNames are the remaining registry types a bare summon
// creates with only constructor defaults.
var summonPlainNames = map[string]bool{
	"block_display": true, "item_display": true, "text_display": true,
	"interaction": true, "painting": true,
	"item_frame": true, "glow_item_frame": true,
	"leash_knot": true, "eye_of_ender": true,
	"evoker_fangs": true, "ominous_item_spawner": true,
	"falling_block": true,
}

// ephemeralTickStep runs the per-tick end conditions of the summoned
// non-living entities — see the file header for each type's fate.
func (s *Server) ephemeralTickStep() {
	for _, snap := range s.entities.All() {
		name, ok := EntityTypeName(snap.Type)
		if !ok {
			continue
		}
		switch name {
		case "item":
			// ItemEntity.tick discards the entity when its stack is
			// empty — a bare /summon item carries air.
			if snap.Item.Empty() {
				if e, ok := s.entities.GetPtr(snap.ID); ok {
					s.ephemeralDiscard(e)
				}
			}
		case "fishing_bobber":
			if snap.fishing == nil {
				if e, ok := s.entities.GetPtr(snap.ID); ok {
					// getPlayerOwner()==null → discard on the first tick.
					s.ephemeralDiscard(e)
				}
			}
		case "evoker_fangs":
			// EvokerFangs.tick — evoker.go.
			if e, ok := s.entities.GetPtr(snap.ID); ok {
				s.fangTick(e)
			}
		case "eye_of_ender":
			e, ok := s.entities.GetPtr(snap.ID)
			if !ok {
				continue
			}
			s.eyeTick(e)
		case "ominous_item_spawner":
			e, ok := s.entities.GetPtr(snap.ID)
			if !ok {
				continue
			}
			e.EphemeralAge++
			if int64(e.EphemeralAge) >= e.OminousSpawnAfter {
				s.ephemeralDiscard(e)
			}
		case "painting", "item_frame", "glow_item_frame":
			e, ok := s.entities.GetPtr(snap.ID)
			if !ok {
				continue
			}
			if e.hangResend {
				// ServerEntity.sendChanges' first pass after the
				// spawn: the entries the placement set are still
				// dirty and go out once more.
				e.hangResend = false
				if meta := encodeHangingMetaDirty(e); meta != nil {
					s.broadcastEntityMetadata(e.ID, meta)
				}
			}
			e.EphemeralAge++
			if e.EphemeralAge < 100 {
				continue
			}
			e.EphemeralAge = 0
			if s.hangSurvives(e) {
				continue
			}
			if id, ok := world.ItemID(name); ok {
				s.spawnItemDrop(e.Dim, floorPos(e), ItemStack{ID: id, Count: 1})
			}
			s.ephemeralDiscard(e)
		case "falling_block":
			e, ok := s.entities.GetPtr(snap.ID)
			if !ok {
				continue
			}
			s.fallingBlockTick(e)
		}
	}
}

// ephemeralDiscard removes the entity and announces it.
func (s *Server) ephemeralDiscard(e *Entity) {
	dim := e.Dim
	s.entities.Remove(e.ID)
	s.broadcastEntityRemoveIn(dim, nil, []int32{e.ID})
}

// floorPos is entity.blockPosition: the cell containing the entity.
func floorPos(e *Entity) world.Pos {
	return world.Pos{X: int(math.Floor(e.X)), Y: int(math.Floor(e.Y)), Z: int(math.Floor(e.Z))}
}

// hangSurvives approximates HangingEntity.survives for a frame or
// painting: a bare summon faces south, so the sturdy support face is
// the -Z neighbour — a summon dropped into open air pops at the
// checkInterval pass, and a block below doesn't count as support.
func (s *Server) hangSurvives(e *Entity) bool {
	d := s.dimensionByName(e.Dim)
	if d == nil {
		return false
	}
	if e.HangSet {
		// An item-hung entity knows its facing and attachment block.
		return s.hangingSurvives(d, e)
	}
	pos := floorPos(e)
	pos.Z--
	b := d.w.GetBlock(pos)
	if _, _, isFluid := fluidOf(b); isFluid {
		return false
	}
	return !world.NonSolid(b) && !isAirCell(b)
}

// fallingBlockTick is FallingBlockEntity.tick: 0.04 of gravity per
// tick; once the cell below stops being replaceable the entity's own
// blockPosition becomes the block (flag 3) and it discards — falling
// out of the world drops the item instead.
func (s *Server) fallingBlockTick(e *Entity) {
	e.MotionY -= 0.04
	if e.MotionY < -3.92 {
		e.MotionY = -3.92
	}
	e.Y += e.MotionY
	d := s.dimensionByName(e.Dim)
	if d == nil {
		return
	}
	pos := floorPos(e)
	if pos.Y < d.minY {
		if id, ok := world.ItemID(e.FallingBlockName); ok {
			s.spawnItemDrop(e.Dim, pos, ItemStack{ID: id, Count: 1})
		}
		s.ephemeralDiscard(e)
		return
	}
	if !fallReplaceable(d.w.GetBlock(posBelow(pos, 1))) &&
		fallReplaceable(d.w.GetBlock(pos)) {
		if nb, ok := world.BlockStateOf(e.FallingBlockName, nil); ok {
			s.setNaturalBlock(d, pos, nb)
		}
		s.ephemeralDiscard(e)
	}
}

// fallReplaceable is canBeReplaced(DirectionalPlaceContext) for the
// falling block's landing cell: air, a fluid, or a snow layer.
func fallReplaceable(b world.Block) bool {
	if isAirCell(b) {
		return true
	}
	if _, _, ok := fluidOf(b); ok {
		return true
	}
	name, _ := world.StateOwner(b)
	return strings.TrimPrefix(name, "minecraft:") == "snow"
}
