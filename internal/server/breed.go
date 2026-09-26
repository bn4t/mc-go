package server

import (
	"mc-go/internal/world"

	"math"
	"math/rand"
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
	"wolf":      {"beef": true, "chicken": true, "mutton": true, "porkchop": true, "rabbit": true, "rotten_flesh": true, "cod": true, "salmon": true, "pufferfish": true, "tropical_fish": true},
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
func (s *Server) feedAnimal(sess *Session, eid int32, hand int32) {
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
	held, hslot := sess.stackForHand(hand)
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
		sess.consumeSlot(hslot, 1)
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
	s.broadcastEntityMetadata(baby.ID, encodeLivingMetadata(baby, nil))
	s.fireBredAnimal(sess, name)
}

// tameFood maps tameable species to the item that tames them and the
// 1-in-N success odds vanilla rolls per use: wolves take bones (NOT
// their breed food — tamed wolves breed on meat), cats and ocelots raw
// fish, parrots any seed.
var tameFood = map[string]struct {
	items map[string]bool
	odds  int
}{
	"wolf":   {map[string]bool{"bone": true}, 3},
	"cat":    {map[string]bool{"cod": true, "salmon": true}, 3},
	"ocelot": {map[string]bool{"cod": true, "salmon": true}, 3},
	"parrot": {map[string]bool{
		"wheat_seeds": true, "melon_seeds": true, "pumpkin_seeds": true,
		"beetroot_seeds": true, "torchflower_seeds": true,
	}, 10},
}

// tameAnimal handles Interact/Interact-At on a tameable mob holding its
// taming item: one item is consumed (in survival), the vanilla odds
// roll decides, and the outcome broadcasts entity status 7 (hearts) or
// 6 (smoke). A tame fires tame_animal.
func (s *Server) tameAnimal(sess *Session, eid int32, hand int32) {
	s.entities.mu.RLock()
	e, ok := s.entities.byID[eid]
	s.entities.mu.RUnlock()
	if !ok || !e.Living || e.tamed {
		return
	}
	name, ok := EntityTypeName(e.Type)
	if !ok {
		return
	}
	tf, ok := tameFood[name]
	if !ok {
		return
	}
	held, hslot := sess.stackForHand(hand)
	if held.Empty() {
		return
	}
	iname, ok := itemNameOf(held.ID)
	if !ok || !tf.items[iname] {
		return
	}
	if sess.mode() == GameModeSurvival || sess.mode() == GameModeAdventure {
		sess.consumeSlot(hslot, 1)
	}
	if rand.Intn(tf.odds) != 0 {
		s.broadcastEntityEventTo(nil, e.Dim, e.ID, 6) // tame failed — smoke
		return
	}
	e.tamed = true
	e.owner = sess.getName()
	s.broadcastEntityEventTo(nil, e.Dim, e.ID, 7) // tamed — hearts
	s.broadcastEntityAnimation(sess, sess.entityID.Load(), 0)
	s.fireTamedAnimal(sess, name)
}

// interactEntityAdv resolves the clicked entity's type name and the
// held item for the player_interacted_with_entity trigger.
func (s *Server) interactEntityAdv(sess *Session, eid int32, hand int32) {
	s.entities.mu.RLock()
	e := s.entities.byID[eid]
	etype, ok := "", false
	if e != nil {
		etype, ok = EntityTypeName(e.Type)
	}
	s.entities.mu.RUnlock()
	if !ok {
		return
	}
	held, _ := sess.stackForHand(hand)
	heldName := ""
	if n, ok := itemNameOf(held.ID); ok && !held.Empty() {
		heldName = n
	}
	s.firePlayerInteractedEntity(sess, etype, heldName)
}

// fishBuckets maps the bucketable fish entity names to the filled
// bucket item — vanilla's water-bucket-on-fish pickup.
var fishBuckets = map[string]string{
	"cod":           "cod_bucket",
	"salmon":        "salmon_bucket",
	"pufferfish":    "pufferfish_bucket",
	"tropical_fish": "tropical_fish_bucket",
	"axolotl":       "axolotl_bucket",
	"tadpole":       "tadpole_bucket",
}

// bucketFish implements the water-bucket-on-live-fish pickup: the entity
// despawns, the fill sound plays, and the matching fish bucket lands in
// the inventory. In creative the water bucket stays in hand (vanilla's
// createFilledResult returns the original stack and adds the filled
// bucket separately); survival replaces it.
//
// Vanilla 26.2's Bucketable.bucketMobPickup bytecode does fire
// FILLED_BUCKET.trigger(ServerPlayer, bucketStack) before discard(),
// but the criterion never lands: measured on a live jar, bucketed cod
// leaves tactical_fishing/cod_bucket=false even after the advancement
// was made visible by granting its parent — apparently a vanilla-side
// trigger bug in 26.2. Wire parity is therefore to emit nothing, so the
// fireFilledBucket call below stays commented out (keep the helper; it
// is also the correct path for any future fixed version).
func (s *Server) bucketFish(sess *Session, eid int32, hand int32) {
	s.entities.mu.RLock()
	e := s.entities.byID[eid]
	if e == nil || e.Dying {
		s.entities.mu.RUnlock()
		return
	}
	name, ok := EntityTypeName(e.Type)
	dim := e.Dim
	s.entities.mu.RUnlock()
	if !ok {
		return
	}
	bucket, ok := fishBuckets[name]
	if !ok {
		return
	}
	held, hslot := sess.stackForHand(hand)
	if heldName, ok := itemNameOf(held.ID); !ok || heldName != "water_bucket" {
		return
	}
	outID, ok := world.ItemID(bucket)
	if !ok {
		return
	}
	if sess.mode() == GameModeCreative {
		slot := sess.firstEmptySlot()
		if slot < 0 {
			return
		}
		sess.setStack(slot, ItemStack{ID: outID, Count: 1})
		_ = s.sendContainerSlot(sess, slot, sess.stackAt(slot))
	} else {
		sess.setStack(hslot, ItemStack{ID: outID, Count: 1})
		_ = s.sendContainerSlot(sess, hslot, sess.stackAt(hslot))
	}
	// See the note above: vanilla 26.2 never awards filled_bucket here.
	// s.fireFilledBucket(sess, bucket)
	s.entities.Remove(eid)
	s.broadcastEntityRemoveIn(dim, nil, []int32{eid})
	s.broadcastAttackSound(sess, 232) // minecraft:item.bucket.fill_fish
}
