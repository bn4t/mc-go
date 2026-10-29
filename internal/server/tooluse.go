package server

import (
	"math/rand"
	"mc-go/internal/world"
	"strings"
)

// useToolOnBlock handles transformations after the clicked block has passed
// its own interaction. Each action carries the selected slot through its cost.
func (s *Server) useToolOnBlock(sess *Session, d *dimension, pos world.Pos, face, hand int32) bool {
	if mode := sess.mode(); mode == GameModeAdventure || mode == GameModeSpectator {
		return false
	}
	held, slot := sess.stackForHand(hand)
	if held.Empty() {
		return false
	}
	item, _ := itemNameOf(held.ID)
	state := d.w.GetBlock(pos)
	name, _ := world.StateOwner(state)
	target, sound := "", ""
	event := int32(0)
	keep, roots, consumable := false, false, false
	switch {
	case strings.HasSuffix(item, "_axe"):
		if hand == 0 && !sess.sneaking {
			off, _ := sess.stackForHand(1)
			offName, _ := itemNameOf(off.ID)
			if !off.Empty() && offName == "shield" {
				return false
			}
		}
		keep = true
		if next := toolStrippables[name]; next != "" {
			target, sound = next, "item.axe.strip"
		} else if next := toolCopperPrevious[name]; next != "" {
			target, sound, event = next, "item.axe.scrape", 3005
		} else if next := toolWaxOff[name]; next != "" {
			target, sound, event = next, "item.axe.wax_off", 3004
		}
	case strings.HasSuffix(item, "_shovel"):
		if face == 0 {
			return false
		}
		switch name {
		case "grass_block", "dirt", "coarse_dirt", "podzol", "mycelium", "rooted_dirt":
			if isAirCell(d.w.GetBlock(posAbove(pos, 1))) {
				target, sound = "dirt_path", "item.shovel.flatten"
			}
		case "campfire", "soul_campfire":
			if lit, _ := world.StateProperty(state, "lit"); lit == "true" {
				next, ok := world.WithProperty(state, "lit", "false")
				if !ok {
					return false
				}
				s.broadcastLevelEvent(d.name, 1009, pos, 0)
				s.setToolTransformBlock(d, pos, next)
				s.emitGameEvent(d, "block_change", float64(pos.X)+.5, float64(pos.Y)+.5, float64(pos.Z)+.5, sess, name)
				s.wearSlot(sess, slot, 1)
				sess.addItemStat(statUsed, held.ID, 1)
				return true
			}
		}
	case strings.HasSuffix(item, "_hoe"):
		if name == "rooted_dirt" {
			target, sound, roots = "dirt", "item.hoe.till", true
		} else if face != 0 && isAirCell(d.w.GetBlock(posAbove(pos, 1))) {
			switch name {
			case "grass_block", "dirt_path", "dirt":
				target, sound = "farmland", "item.hoe.till"
			case "coarse_dirt":
				target, sound = "dirt", "item.hoe.till"
			}
		}
	case item == "honeycomb":
		target = toolWaxOn[name]
		keep, consumable, event = true, true, 3003
	}
	if target == "" {
		return false
	}
	props := map[string]string{}
	if keep {
		oldProps := world.BlockProperties(state)
		for _, key := range world.BlockPropNames(target) {
			if v, ok := oldProps[key]; ok {
				props[key] = v
			}
		}
	}
	next, ok := world.BlockStateOf(target, props)
	if !ok {
		return false
	}
	if sound != "" {
		if id, ok := SoundEventID(sound); ok {
			s.playSoundSrc(d.name, float64(pos.X)+.5, float64(pos.Y)+.5, float64(pos.Z)+.5, id, 4, 1, 1, sess)
		}
	}
	s.setToolTransformBlock(d, pos, next)
	s.emitGameEvent(d, "block_change", float64(pos.X)+.5, float64(pos.Y)+.5, float64(pos.Z)+.5, sess, target)
	if event != 0 {
		s.broadcastLevelEventExcept(d.name, event, pos, 0, sess)
	}
	if roots {
		s.spawnToolResourceFromFace(d, pos, face, ItemStack{ID: world.ItemIDs["hanging_roots"], Count: 1})
	}
	if consumable {
		if sess.mode() != GameModeCreative {
			sess.consumeSlot(slot, 1)
		}
	} else {
		s.wearSlot(sess, slot, 1)
	}
	sess.addItemStat(statUsed, held.ID, 1)
	return true
}

// Block.popResourceFromFace places the item beyond the selected face. Tangent
// position and velocity are random; the normal coordinate and impulse are fixed.
func (s *Server) spawnToolResourceFromFace(d *dimension, pos world.Pos, face int32, item ItemStack) {
	if !s.gameRuleBool("minecraft:block_drops") || item.Empty() {
		return
	}
	dx, dy, dz, ok := faceOffset(face)
	if !ok {
		return
	}
	normal := [3]float64{float64(dx), float64(dy), float64(dz)}
	xyz := [3]float64{float64(pos.X) + .5, float64(pos.Y) + .5, float64(pos.Z) + .5}
	velocity := [3]float64{}
	for axis, n := range normal {
		if n == 0 {
			xyz[axis] += rand.Float64()*.5 - .25
			velocity[axis] = rand.Float64()*.2 - .1
		} else {
			xyz[axis] += n * .625
			velocity[axis] = n * .1
		}
	}
	xyz[1] -= .125
	if normal[1] == 0 {
		velocity[1] = rand.Float64() * .1
	} else {
		velocity[1] += .1
	}
	kind, ok := EntityTypeID("item")
	if !ok {
		return
	}
	e := s.entities.Spawn(&Entity{Type: kind, Dim: d.name, X: xyz[0], Y: xyz[1], Z: xyz[2], MotionX: velocity[0], MotionY: velocity[1], MotionZ: velocity[2], Item: item, PickupDelay: 10})
	s.broadcastEntitySpawn(nil, *e)
	s.broadcastEntityMetadata(e.ID, encodeItemMetadata(item))
}
