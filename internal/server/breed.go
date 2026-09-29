package server

import (
	"mc-go/internal/world"

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
	"wolf":      wolfFoodNames(),
	"cat":       {"cod": true, "salmon": true},
	"ocelot":    {"cod": true, "salmon": true},
	"fox":       {"sweet_berries": true, "glow_berries": true},
	"panda":     {"bamboo": true},
	"turtle":    {"seagrass": true},
	"hoglin":    {"crimson_fungus": true},
	"strider":   {"warped_fungus": true},
}

// wolfFoodNames is the wolf_food item tag from the 26.2 jar:
// the meat tag plus both cods, both salmons, tropical_fish,
// pufferfish and rabbit_stew. It feeds Animal.isFood for feeding
// and BegGoal's item predicate (which adds bone on top).
func wolfFoodNames() map[string]bool {
	return map[string]bool{
		"beef": true, "chicken": true, "mutton": true,
		"porkchop": true, "rabbit": true, "rotten_flesh": true,
		"cooked_beef": true, "cooked_chicken": true,
		"cooked_mutton": true, "cooked_porkchop": true,
		"cooked_rabbit": true, "cod": true, "cooked_cod": true,
		"salmon": true, "cooked_salmon": true,
		"tropical_fish": true, "pufferfish": true,
		"rabbit_stew": true,
	}
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
	// Babies and adults still on the 6000-tick breed cooldown can't
	// love — canMate gates on age == 0.
	if e.Age != 0 {
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
	// Breeding itself is BreedGoal's: the pair walks together for
	// ~60 ticks and the baby spawns on contact — see mobBreedStep.
}

// breedPair is Animal.spawnChildFromBreeding: love clears on both
// parents and they take the 6000-tick (5-minute) cooldown, a baby of
// their species spawns at the breeder, 1-7 xp drops at the pair, and
// the last feeder earns bred_animals criteria.
func (s *Server) breedPair(e, mate *Entity, name string) {
	causer := e.loveCause
	partnerCauser := mate.loveCause
	e.loveTicks, mate.loveTicks = 0, 0
	e.loveCause, mate.loveCause = "", ""
	e.Age, mate.Age = 6000, 6000
	e.BreedTime, mate.BreedTime = 0, 0
	baby := s.entities.Spawn(&Entity{
		Type:      e.Type,
		Dim:       e.Dim,
		X:         e.X,
		Y:         e.Y,
		Z:         e.Z,
		Living:    true,
		Health:    mobHealth[name],
		MaxHealth: mobHealth[name],
		Age:       -24000,
		Baby:      true,
	})
	if name == "fox" {
		// FoxBreedGoal.breed(): the offspring trusts the love
		// causes — the animal's feeder first (slot0), then the
		// partner's when it is a different player (addTrustedEntity
		// fills slot0 then slot1, two max). EntityReference.of is
		// the feeder's uuid.
		for _, c := range []string{causer, partnerCauser} {
			if c == "" || len(baby.FoxTrusted) == 2 {
				continue
			}
			u := offlineUUID(c)
			if sess := s.sessionByName(c); sess != nil {
				u = sess.getUUID()
			}
			dup := false
			for _, ex := range baby.FoxTrusted {
				if ex == u {
					dup = true
					break
				}
			}
			if !dup {
				baby.FoxTrusted = append(baby.FoxTrusted, u)
			}
		}
	}
	s.broadcastEntitySpawn(nil, *baby)
	s.broadcastEntityMetadata(baby.ID, encodeLivingMetadata(baby, nil))
	s.broadcastEntityEventTo(nil, e.Dim, baby.ID, 18) // hearts on the newborn
	// ExperienceOrb.award: one orb worth 1-7.
	s.spawnXPOrb(e.Dim, e.X, e.Y, e.Z, int32(1+rand.Intn(7)))
	if sess := s.sessionByName(causer); sess != nil {
		s.fireBredAnimal(sess, name)
	}
}

// sessionByName finds the play-state session with that name — the
// loveCause bookkeeping stores names, not entity ids.
func (s *Server) sessionByName(name string) *Session {
	var out *Session
	s.sessions.Range(func(_, v any) bool {
		p := v.(*Session)
		if p.getName() == name {
			out = p
			return false
		}
		return true
	})
	return out
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
func (s *Server) tameAnimal(sess *Session, eid int32, hand int32) bool {
	s.entities.mu.RLock()
	e, ok := s.entities.byID[eid]
	s.entities.mu.RUnlock()
	if !ok || !e.Living || e.tamed {
		return false
	}
	name, ok := EntityTypeName(e.Type)
	if !ok {
		return false
	}
	tf, ok := tameFood[name]
	if !ok {
		return false
	}
	// Wolf's untamed arm gates on !isAngry — an angry wolf's bone
	// click falls to Animal.mobInteract (not food) and never rolls.
	if e.AngryAt != ([16]byte{}) {
		return false
	}
	held, hslot := sess.stackForHand(hand)
	if held.Empty() {
		return false
	}
	iname, ok := itemNameOf(held.ID)
	if !ok || !tf.items[iname] {
		return false
	}
	if sess.mode() == GameModeSurvival || sess.mode() == GameModeAdventure {
		sess.consumeSlot(hslot, 1)
	}
	if rand.Intn(tf.odds) != 0 {
		s.broadcastEntityEventTo(nil, e.Dim, e.ID, 6) // tame failed — smoke
		// The consume already happened — SUCCESS_SERVER: the sit
		// toggle arm never runs on this click.
		return true
	}
	e.tamed = true
	e.owner = sess.getName()
	e.ownerUUID = sess.uuid
	// Wolf.mobInteract and Cat.mobInteract's tryToTame both end in
	// setOrderedToSit(true) — a plain field in 26.2: the tame packs
	// flags 0x04 alone; the 0x01 pose bit lands a tick later via
	// SitWhenOrderedToGoal (only with AI). Parrot's roll stands too.
	if name == "wolf" || name == "cat" {
		e.Sitting = true
	}
	e.MetaDirty = true
	s.broadcastEntityEventTo(nil, e.Dim, e.ID, 7) // tamed — hearts
	s.broadcastEntityAnimation(sess, sess.entityID.Load(), 0)
	s.fireTamedAnimal(sess, name)
	return true
}

// tamableSitToggle is the post-tame mobInteract arm every TamableAnimal
// shares: an owner's click that no other handler consumed flips
// setOrderedToSit — navigation.stop, setTarget(null), and the idx18
// sit bit toggling on the wire. Vanilla's gate order matters: breed
// food still feeds first (feedAnimal ran above and consumed), collar
// dyes repaint on wolf/cat, wolf armor equips — those items don't
// reach the toggle, so the toggle skips them all.
func (s *Server) tamableSitToggle(sess *Session, eid int32, hand int32) {
	s.entities.mu.RLock()
	e, ok := s.entities.byID[eid]
	s.entities.mu.RUnlock()
	if !ok || !e.Living || !e.tamed {
		return
	}
	if e.ownerUUID != sess.uuid {
		return
	}
	name, _ := EntityTypeName(e.Type)
	held, _ := sess.stackForHand(hand)
	if !held.Empty() {
		iname, ok := itemNameOf(held.ID)
		if !ok {
			return
		}
		// isFood consumes first (the breed food map is each
		// type's food tag — bones are NOT wolf food, so a tamed
		// wolf right-clicked with a bone still toggles); collar
		// dyes and wolf armor have their own arms — none toggle.
		if bf, ok := animalFood[name]; ok && bf[iname] {
			return
		}
		if (name == "wolf" || name == "cat") && (isDyeItem(iname) || iname == "wolf_armor") {
			return
		}
		if name == "parrot" && iname == "cookie" {
			// PARROT_POISONOUS_FOOD: the poison arm, not the toggle.
			return
		}
	}
	e.Sitting = !e.Sitting
	e.MetaDirty = true
	// setOrderedToSit's tail: jumping false, navigation.stop,
	// setTarget(null) — the wolf drops whatever it stalked.
	e.AngryAt = [16]byte{}
	e.AngryUntil = -1
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
