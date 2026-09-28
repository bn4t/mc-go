package server

import (
	"math"
	"math/rand"
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

// shearAnimal runs the shears branches on the clicked mob, split the
// way vanilla does: the equipment slot (BODY) branch is
// attemptToShearEquipment inside Entity.interact — it consumes the
// interact when it fires. The mob-wool branch (ShearsItem.
// interactLivingEntity → Shearable.shear) is the item phase, which
// Player.interactOn reaches only when the entity's whole interact —
// leash branches and mobInteract included — returned PASS. Two
// separate entry points keep both halves in vanilla order.
//
// Returns true when the equipment-shear consumed the interact.
func (s *Server) shearAnimal(sess *Session, eid int32, hand int32, sneaking bool) bool {
	if sneaking {
		return false
	}
	held, hslot := sess.stackForHand(hand)
	if held.Empty() {
		return false
	}
	if n, ok := itemNameOf(held.ID); !ok || n != "shears" {
		return false
	}
	s.entities.mu.RLock()
	e, ok := s.entities.byID[eid]
	if !ok {
		s.entities.mu.RUnlock()
		return false
	}
	t := *e
	s.entities.mu.RUnlock()
	if !t.Living || t.Dying {
		return false
	}
	if t.BodyItem.Empty() {
		// No shearable equipment — attemptToShearEquipment can't fire;
		// the item-phase wool shear is shearMobItemPhase.
		return false
	}
	// canShearEquipment: wolves answer isOwnedBy; every other mob
	// answers !isVehicle — mc-go's shearables are owned wolves, and a
	// passenger-carrying mob blocks it either way.
	if et, _ := EntityTypeName(t.Type); et == "wolf" && t.owner != sess.getName() {
		return false
	}
	if len(t.Passengers) > 0 {
		return false
	}
	// attemptToShearEquipment: the BODY slot's Equippable must be
	// shearable (wolf_armor/harness are) and free of binding_curse —
	// mc-go carries no curse component on mob equipment.
	armor := t.BodyItem
	if n, ok := itemNameOf(armor.ID); !ok || !shearableBody(n) {
		return false
	}
	if p, ok := s.entities.GetPtr(t.ID); ok {
		p.BodyItem = ItemStack{}
	}
	pos := world.Pos{X: int(math.Floor(t.X)), Y: int(math.Floor(t.Y)), Z: int(math.Floor(t.Z))}
	s.spawnItemDrop(t.Dim, pos, armor)
	// Mob.shearItem, then this.playSound(equippable.shearingSound) —
	// the mob's own playSound → a POSITIONAL packet at the mob with
	// the adult voice pitch (r1-r2)*0.2+1. wolf_armor's shearing
	// sound is item.armor.unequip_wolf (78).
	s.playSoundAt(t.Dim, t.X, t.Y, t.Z, 78, 1,
		(rand.Float32()-rand.Float32())*0.2+1)
	s.wearSlot(sess, hslot, 1)
	iname, _ := itemNameOf(armor.ID)
	etype, _ := EntityTypeName(t.Type)
	s.firePlayerShearedEquipment(sess, etype, iname)
	return true
}

// shearMobItemPhase is ShearsItem.interactLivingEntity: the item-side
// right-click that runs only after the entity's interact chain —
// sneak-transfer, leash-shear, equipment-shear, unleash/attach and
// mobInteract all returned PASS. It re-checks the hand itself.
func (s *Server) shearMobItemPhase(sess *Session, eid int32, hand int32, sneaking bool) {
	if sneaking {
		return
	}
	held, _ := sess.stackForHand(hand)
	if held.Empty() {
		return
	}
	if n, ok := itemNameOf(held.ID); !ok || n != "shears" {
		return
	}
	e, ok := s.entities.Get(eid)
	if !ok || !e.Living || e.Dying {
		return
	}
	s.shearMob(sess, e, hand)
}

// shearMob is the Shearable half of mobInteract (26.2 moved the wool
// shear inside the mob's own interact, SoundSource.PLAYERS): a shears
// right-click on a shearable mob runs its shear() — the same drop/flag
// work the dispenser path does — then wears the shears. Every
// Shearable leads shear() with level.playSound(null, this, <type's
// shear sound>, source, 1, 1) — an entity-anchored sound_entity packet.
func (s *Server) shearMob(sess *Session, t Entity, hand int32) {
	tn, _ := EntityTypeName(t.Type)
	// Every Shearable leads shear() with level.playSound(null, this,
	// <type's shear sound>, source, 1, 1) — entity-anchored 0x74.
	shearSound := map[string]int32{
		"sheep":      1441, // entity.sheep.shear
		"snow_golem": 1574, // entity.snow_golem.shear
		"bogged":     184,  // entity.bogged.shear
		"mooshroom":  989,  // entity.mooshroom.shear
	}
	emitShear := func() {
		if snd, ok := shearSound[tn]; ok {
			s.playSoundEntity(t.Dim, t.ID, snd, 7, 1, 1, nil)
		}
	}
	switch tn {
	case "sheep":
		if t.Sheared {
			return
		}
		emitShear()
		if p, ok := s.entities.GetPtr(t.ID); ok {
			p.Sheared = true
			s.broadcastEntityMetadata(t.ID, encodeLivingMetadata(p, nil))
		}
		pos := world.Pos{X: int(math.Floor(t.X)), Y: int(math.Floor(t.Y)), Z: int(math.Floor(t.Z))}
		// The shearing/sheep/<color> subtable: wool of the dyed color.
		if wid, ok := world.ItemIDs[dyeColorName[t.Color&15]+"_wool"]; ok {
			s.spawnItemDrop(t.Dim, pos, ItemStack{ID: wid, Count: int32(1 + s.rng.intn(3))})
		}
	case "snow_golem":
		if !t.Pumpkin {
			return
		}
		emitShear()
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
		emitShear()
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
		emitShear()
		// finalizeConversion's lambda: EXPLOSION puff at the mob
		// center, then the shearing/mooshroom loot — 5 of the
		// variant's mushroom (brown gets brown_mushroom).
		s.broadcastParticles(t.Dim, 30, t.X, t.Y+0.5, t.Z, 0, 0, 0, 0, 1) // explosion
		mush := "red_mushroom"
		if t.MooshroomVariant == 1 {
			mush = "brown_mushroom"
		}
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
		if pid, ok := world.ItemIDs[mush]; ok {
			for i := 0; i < 5; i++ {
				s.spawnItemDrop(t.Dim, pos, ItemStack{ID: pid, Count: 1})
			}
		}
	default:
		return
	}
	// Shearable.shear posts SHEAR at the sheared entity's position.
	if d := s.dimensionByName(t.Dim); d != nil {
		s.emitGameEvent(d, "shear", t.X, t.Y, t.Z, sess, "")
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
