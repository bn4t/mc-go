package server

import (
	"math"
	"strings"

	"mc-go/internal/world"
)

// Shearing equipment off a mob: vanilla Entity.interact checks for
// shears BEFORE delegating to mobInteract — Items.SHEARS in hand,
// canShearEquipment (Mob: not a vehicle; Wolf overrides to owned-by),
// and the player not secondary-using (sneaking). It then scans the
// equipment slots for a shearable Equippable stack and calls
// shearItem: shears wear one point, the slot clears, the stack drops,
// gameEvent SHEAR, and player_sheared_equipment fires with the
// REMOVED stack. mc-go models the BODY slot only (wolf_armor) — no
// other shearable equipment exists.

// shearAnimal runs the shears branch of Entity.interact on the
// clicked mob. It must precede the type-specific handlers in the
// interact chain — vanilla returns SUCCESS before mobInteract, so an
// owned wolf being sheared does not also toggle its sit pose.
func (s *Server) shearAnimal(sess *Session, eid int32, hand int32, sneaking bool) {
	if sneaking {
		return
	}
	held, hslot := sess.stackForHand(hand)
	if held.Empty() {
		return
	}
	if n, ok := itemNameOf(held.ID); !ok || n != "shears" {
		return
	}
	s.entities.mu.RLock()
	e, ok := s.entities.byID[eid]
	if !ok {
		s.entities.mu.RUnlock()
		return
	}
	t := *e
	s.entities.mu.RUnlock()
	if !t.Living || t.Dying {
		return
	}
	if t.BodyItem.Empty() {
		// No shearable equipment — the item's interactLivingEntity
		// shear branch is next in vanilla's chain.
		s.shearMob(sess, t, hand)
		return
	}
	// canShearEquipment: wolves answer isOwnedBy; every other mob
	// answers !isVehicle — mc-go's shearables are owned wolves, and a
	// passenger-carrying mob blocks it either way.
	if et, _ := EntityTypeName(t.Type); et == "wolf" && t.owner != sess.getName() {
		return
	}
	if len(t.Passengers) > 0 {
		return
	}
	// attemptToShearEquipment: the BODY slot's Equippable must be
	// shearable (wolf_armor/harness are) and free of binding_curse —
	// mc-go carries no curse component on mob equipment.
	armor := t.BodyItem
	if n, ok := itemNameOf(armor.ID); !ok || !shearableBody(n) {
		return
	}
	if p, ok := s.entities.GetPtr(eid); ok {
		p.BodyItem = ItemStack{}
	}
	pos := world.Pos{X: int(math.Floor(t.X)), Y: int(math.Floor(t.Y)), Z: int(math.Floor(t.Z))}
	s.spawnItemDrop(t.Dim, pos, armor)
	s.wearSlot(sess, hslot, 1)
	iname, _ := itemNameOf(armor.ID)
	etype, _ := EntityTypeName(t.Type)
	s.firePlayerShearedEquipment(sess, etype, iname)
}

// shearMob is the Shearable half of ShearsItem.interactLivingEntity:
// a shears right-click on a shearable mob runs its shear() — the same
// drop/flag work the dispenser path does — then wears the shears.
func (s *Server) shearMob(sess *Session, t Entity, hand int32) {
	tn, _ := EntityTypeName(t.Type)
	switch tn {
	case "sheep":
		if t.Sheared {
			return
		}
		if p, ok := s.entities.GetPtr(t.ID); ok {
			p.Sheared = true
			s.broadcastEntityMetadata(t.ID, encodeLivingMetadata(p, nil))
		}
		pos := world.Pos{X: int(math.Floor(t.X)), Y: int(math.Floor(t.Y)), Z: int(math.Floor(t.Z))}
		if wid, ok := world.ItemIDs["white_wool"]; ok {
			s.spawnItemDrop(t.Dim, pos, ItemStack{ID: wid, Count: int32(1 + s.rng.intn(3))})
		}
	case "snow_golem":
		if !t.Pumpkin {
			return
		}
		if p, ok := s.entities.GetPtr(t.ID); ok {
			p.Pumpkin = false
			s.broadcastEntityMetadata(t.ID, encodeLivingMetadata(p, nil))
		}
		pos := world.Pos{X: int(math.Floor(t.X)), Y: int(math.Floor(t.Y)), Z: int(math.Floor(t.Z))}
		if pid, ok := world.ItemIDs["carved_pumpkin"]; ok {
			s.spawnItemDrop(t.Dim, pos, ItemStack{ID: pid, Count: 1})
		}
	case "bogged":
		if t.Sheared {
			return
		}
		if p, ok := s.entities.GetPtr(t.ID); ok {
			p.Sheared = true
			s.broadcastEntityMetadata(t.ID, encodeLivingMetadata(p, nil))
		}
		pos := world.Pos{X: int(math.Floor(t.X)), Y: int(math.Floor(t.Y)), Z: int(math.Floor(t.Z))}
		for i := 0; i < 2; i++ {
			for _, iname := range []string{"brown_mushroom", "red_mushroom"} {
				if pid, ok := world.ItemIDs[iname]; ok {
					s.spawnItemDrop(t.Dim, pos, ItemStack{ID: pid, Count: 1})
				}
			}
		}
	case "mooshroom":
		pos := world.Pos{X: int(math.Floor(t.X)), Y: int(math.Floor(t.Y)), Z: int(math.Floor(t.Z))}
		s.entities.Remove(t.ID)
		s.broadcastEntityRemoveIn(t.Dim, nil, []int32{t.ID})
		if et, ok := EntityTypeID("cow"); ok {
			cow := s.entities.Spawn(&Entity{
				Type:      et,
				Dim:       t.Dim,
				X:         t.X,
				Y:         t.Y,
				Z:         t.Z,
				Living:    true,
				Health:    10,
				MaxHealth: 10,
				NoAI:      t.NoAI,
			})
			s.broadcastEntitySpawnIn(t.Dim, nil, *cow)
		}
		if pid, ok := world.ItemIDs["red_mushroom"]; ok {
			for i := 0; i < 5; i++ {
				s.spawnItemDrop(t.Dim, pos, ItemStack{ID: pid, Count: 1})
			}
		}
	default:
		return
	}
	held2, hslot := sess.stackForHand(hand)
	s.wearSlot(sess, hslot, 1)
	sess.addItemStat(statUsed, held2.ID, 1)
}

// shearables lists BODY-slot equipment vanilla's canBeSheared marks
// removable with shears — wolf_armor today.
func shearableBody(name string) bool {
	return strings.HasSuffix(name, "wolf_armor") || strings.HasSuffix(name, "harness")
}
