package server

import (
	"math"
)

// animalFood is the breeding feed each species accepts (vanilla's
// Animal.isFood — the item right-clicked on it). Love lasts 600 ticks;
// two animals of the same species in love within ~8 blocks produce a
// baby, granting bred_animals to the love causes.
var animalFood = map[string]map[string]bool{
	"cow":       {"wheat": true},
	"sheep":     {"wheat": true},
	"mooshroom": {"wheat": true},
	"chicken":   {"wheat_seeds": true, "melon_seeds": true, "pumpkin_seeds": true, "beetroot_seeds": true, "torchflower_seeds": true},
	"pig":       {"carrot": true, "potato": true, "beetroot": true},
	"rabbit":    {"carrot": true, "dandelion": true, "golden_carrot": true},
	"horse":     {"wheat": true, "apple": true, "golden_carrot": true, "golden_apple": true, "hay_block": true},
	"donkey":    {"wheat": true, "apple": true, "golden_carrot": true, "golden_apple": true, "hay_block": true},
	"llama":     {"wheat": true, "hay_block": true},
	"wolf":      {"bone": true, "beef": true, "chicken": true, "mutton": true, "porkchop": true, "rabbit": true, "rotten_flesh": true, "cod": true, "salmon": true, "pufferfish": true, "tropical_fish": true},
	"cat":       {"cod": true, "salmon": true},
	"ocelot":    {"cod": true, "salmon": true},
	"fox":       {"sweet_berries": true, "glow_berries": true},
	"panda":     {"bamboo": true},
	"turtle":    {"seagrass": true},
	"hoglin":    {"crimson_fungus": true},
	"strider":   {"warped_fungus": true},
}

// feedAnimal handles an Interact/Interact-At on a mob: when the held
// item is that species' food the animal enters love (entity status 18
// = hearts), the food is consumed in survival, and a partner already
// in love within reach produces a baby.
func (s *Server) feedAnimal(sess *Session, eid int32) {
	s.entities.mu.RLock()
	e, ok := s.entities.byID[eid]
	s.entities.mu.RUnlock()
	if !ok || !e.Living {
		return
	}
	name, ok := EntityTypeName(e.Type)
	if !ok {
		return
	}
	foods, breedable := animalFood[name]
	if !breedable {
		return
	}
	held := sess.heldStack()
	if held.Empty() {
		return
	}
	iname, ok := itemNameOf(held.ID)
	if !ok || !foods[iname] {
		return
	}
	if e.loveTicks > 0 {
		return // already in love — extra feeds are no-ops
	}
	if sess.mode() == GameModeSurvival || sess.mode() == GameModeAdventure {
		sess.consumeHeld(1)
	}
	e.loveTicks = 600
	e.loveCause = sess.getName()
	s.broadcastEntityEventTo(nil, e.Dim, e.ID, 18) // hearts
	s.broadcastEntityAnimation(sess, sess.entityID.Load(), 0)

	// A same-species mate already in love within ~8 blocks breeds.
	s.entities.mu.RLock()
	var mate *Entity
	for _, other := range s.entities.byID {
		if other.ID == e.ID || other.Dim != e.Dim || other.Type != e.Type || other.loveTicks <= 0 {
			continue
		}
		dx, dy, dz := other.X-e.X, other.Y-e.Y, other.Z-e.Z
		if math.Sqrt(dx*dx+dy*dy+dz*dz) <= 8 {
			mate = other
			break
		}
	}
	s.entities.mu.RUnlock()
	if mate == nil {
		return
	}
	e.loveTicks, mate.loveTicks = 0, 0
	e.loveCause, mate.loveCause = "", ""
	baby := s.entities.Spawn(&Entity{
		Type:      e.Type,
		Dim:       e.Dim,
		X:         (e.X + mate.X) / 2,
		Y:         e.Y,
		Z:         (e.Z + mate.Z) / 2,
		Living:    true,
		Health:    mobHealth[name],
		MaxHealth: mobHealth[name],
	})
	s.broadcastEntitySpawn(nil, *baby)
	s.broadcastEntityMetadata(baby.ID, encodeLivingMetadata(baby))
	s.fireBredAnimal(sess, name)
}
