package server

import (
	"math"

	"mc-go/internal/data"
	"mc-go/internal/world"
)

// mobItemPickupStep is vanilla's Mob.aiStep item scan: every living
// mob with pickup ability sweeps item entities inside its bounding
// box inflated by the pickup reach (Mob.getPickupReach = 1,0,1) and
// takes the first stack it wants, provided the item's pickup delay
// has expired and mobGriefing is on (mc-go doesn't model the rule —
// its default is on).
func (s *Server) mobItemPickupStep() {
	s.entities.mu.RLock()
	var mobs, items []*Entity
	for _, e := range s.entities.byID {
		if e.Item.ID != 0 {
			items = append(items, e)
			continue
		}
		if e.Living && s.mobCanPickUpLoot(e) {
			mobs = append(mobs, e)
		}
	}
	s.entities.mu.RUnlock()
	for _, m := range mobs {
		if m.Dying {
			continue
		}
		bb := mobBounds(m.Type)
		half := bb[0]/2 + 1 // inflate(1,0,1) reach, vanilla default
		for _, it := range items {
			if it.Item.ID == 0 || it.Dim != m.Dim || it.PickupDelay > 0 {
				continue
			}
			dx := it.X - m.X
			dz := it.Z - m.Z
			if math.Abs(dx) > half || math.Abs(dz) > half {
				continue
			}
			if it.Y < m.Y || it.Y > m.Y+bb[1] {
				continue
			}
			if !s.mobWantsItem(m, it.Item) {
				continue
			}
			s.mobPickUpItem(m, it)
		}
	}
}

// mobCanPickUpLoot mirrors the per-type canPickUpLoot overrides: a
// sulfur cube picks up while its body slot is empty; other mobs need
// the CanPickUpLoot flag (set by /summon NBT or spawn rules).
func (s *Server) mobCanPickUpLoot(e *Entity) bool {
	name, ok := EntityTypeName(e.Type)
	if !ok {
		return false
	}
	if name == "sulfur_cube" {
		return e.BodyItem.Empty()
	}
	return e.CanPickUpLoot
}

// mobWantsItem is the wantsToPickUp predicate: piglins take
// #piglin_loved stacks (adult behavior — babies and the repellent/
// holding subtleties of PiglinAi are not modeled), sulfur cubes
// swallow one #sulfur_cube_swallowable item, everything else takes
// nothing for now (generic equipToSlot pickup is unmodeled).
func (s *Server) mobWantsItem(m *Entity, it ItemStack) bool {
	name, ok := EntityTypeName(m.Type)
	if !ok {
		return false
	}
	iname, ok := world.ItemName(it.ID)
	if !ok {
		return false
	}
	switch name {
	case "piglin":
		return itemTagHas("piglin_loved", iname)
	case "sulfur_cube":
		return itemTagHas("sulfur_cube_swallowable", iname)
	}
	return false
}

// itemTagHas reports whether the item tag contains the member.
func itemTagHas(tag, name string) bool {
	for _, m := range data.ItemTags[tag] {
		if m == name {
			return true
		}
	}
	return false
}

// mobPickUpItem is Mob.pickUpItem plus LivingEntity.onItemPickup:
// the mob claims the stack, the item entity shrinks/discards, and a
// player-thrown item fires thrown_item_picked_up_by_entity for the
// thrower. Sulfur cubes take a single item into BODY (vanilla's
// split(1)); a piglin takes the whole stack into its inventory.
func (s *Server) mobPickUpItem(m *Entity, it *Entity) {
	name, _ := EntityTypeName(m.Type)
	take := it.Item.Count
	if name == "sulfur_cube" {
		take = 1
		m.BodyItem = it.Item.withCount(1)
	}
	if sess := s.sessionByEntityID(it.Thrower); sess != nil {
		s.fireThrownItemPickedUp(sess, it.Item, name)
	}
	it.Item.Count -= take
	if it.Item.Count <= 0 {
		it.Item = ItemStack{}
		s.entities.Remove(it.ID)
		s.broadcastEntityRemoveIn(it.Dim, nil, []int32{it.ID})
		return
	}
	s.broadcastEntityMetadata(it.ID, encodeItemMetadata(it.Item))
}
