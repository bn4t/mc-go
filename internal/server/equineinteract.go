// Equine mobInteract arms — Horse/AbstractChestedHorse.mobInteract and
// AbstractHorse.mobInteract (decompiled 26.2), restricted to the
// AbstractHorse family (camel's mobInteract is its own arm — it always
// mounts and never feeds through this path).
//
// Gate order (Horse.mobInteract):
//   - a vehicle or a baby holding golden_dandelion, or a tamed adult
//     under sneak — falls to super.mobInteract (mount/inventory arms)
//   - held item is food → fedFood → handleEating's heal/grow/temper/
//     in-love arms, then eating()'s open-mouth flag + eat sound
//   - held non-food on an untamed horse → makeMad: standIfPossible's
//     20-tick standing flag + the angry sound
//   - anything else → super.mobInteract (equip, doPlayerRide)
package server

import (
	"math"
	"math/rand/v2"
)

// equineFeedEntry is one row of handleEating's per-item table:
// heal amount, baby age-up ticks, temper points.
type equineFeedEntry struct {
	heal, ageUp, temper float32
}

// equineFeedTable is handleEating's item switch. The horse family
// shares HORSE_FOOD; llamas have their own LLAMA_FOOD row set.
var equineFeedTable = map[string]map[string]equineFeedEntry{
	"horse": {
		"wheat":                  {2, 20, 3},
		"sugar":                  {1, 30, 3},
		"hay_block":              {20, 180, 0},
		"apple":                  {3, 60, 3},
		"carrot":                 {3, 60, 3},
		"golden_carrot":          {4, 60, 5},
		"golden_apple":           {10, 240, 10},
		"enchanted_golden_apple": {10, 240, 10},
	},
	"llama": {
		"wheat":     {2, 10, 3},
		"hay_block": {10, 90, 6},
	},
}

// equineLoveItems is the golden-food arm: a tamed adult not already in
// love falls in love on golden carrot/apple (llamas on hay_block).
var equineLoveItems = map[string]map[string]bool{
	"horse": {"golden_carrot": true, "golden_apple": true, "enchanted_golden_apple": true},
	"llama": {"hay_block": true},
}

// equineEatSound / equineAngrySound map the family to SoundEvents.
// Skeleton horses override neither — their eat and mad calls play
// nothing (AbstractHorse returns null).
var equineEatSound = map[string]string{
	"horse":        "entity.horse.eat",
	"donkey":       "entity.donkey.eat",
	"mule":         "entity.mule.eat",
	"llama":        "entity.llama.eat",
	"trader_llama": "entity.llama.eat",
	"zombie_horse": "entity.zombie_horse.eat",
}

var equineAngrySound = map[string]string{
	"horse":        "entity.horse.angry",
	"donkey":       "entity.donkey.angry",
	"mule":         "entity.mule.angry",
	"llama":        "entity.llama.angry",
	"trader_llama": "entity.llama.angry",
	"zombie_horse": "entity.zombie_horse.angry",
}

// equineFeedFamily resolves the feed table a type uses.
func equineFeedFamily(name string) (map[string]equineFeedEntry, map[string]bool, bool) {
	switch name {
	case "llama", "trader_llama":
		return equineFeedTable["llama"], equineLoveItems["llama"], true
	case "horse", "donkey", "mule", "zombie_horse", "skeleton_horse":
		return equineFeedTable["horse"], equineLoveItems["horse"], true
	}
	return nil, nil, false
}

// equineInteract is Horse/AbstractChestedHorse.mobInteract's food and
// makeMad arms, ordered before the generic Animal.mobInteract arms:
// vanilla routes every equine right-click through the override, so a
// held item either feeds or angers — the Animal breed path never sees
// equines. Returns true when the interact was consumed.
func (s *Server) equineInteract(sess *Session, eid int32, hand int32, sneaking bool) bool {
	s.entities.mu.RLock()
	e, ok := s.entities.byID[eid]
	s.entities.mu.RUnlock()
	if !ok || !e.Living {
		return false
	}
	name, ok := EntityTypeName(e.Type)
	if !ok {
		return false
	}
	table, loveItems, equine := equineFeedFamily(name)
	if !equine {
		return false
	}
	// Horse.mobInteract's gate: a vehicle, a baby not holding
	// golden_dandelion, or a tamed adult under sneak skips the food/
	// mad arms for super.mobInteract's mount and inventory arms.
	if len(e.Passengers) > 0 {
		return false
	}
	held, hslot := sess.stackForHand(hand)
	if e.Baby {
		iname, _ := itemNameOf(held.ID)
		if iname == "golden_dandelion" {
			return false
		}
	}
	if e.Tamed && sneaking && !e.Baby {
		return false
	}
	if held.Empty() {
		return false
	}
	iname, ok := itemNameOf(held.ID)
	if !ok {
		return false
	}
	if row, isFood := table[iname]; isFood {
		s.equineFeed(sess, e, name, iname, hslot, row, loveItems)
		return true
	}
	// Untamed + held non-food → makeMad → standIfPossible +
	// getAngrySound. Llama/trader_llama can't perform the rear
	// (canPerformRearing false); the sound still plays.
	if !e.Tamed {
		s.equineMakeMad(e, name)
		return true
	}
	return false
}

// equineFeed is AbstractHorse.handleEating (+ Llama's override): the
// heal, age-up, temper, and in-love arms run in order; any one that
// applied sets itemUsed, which drives eating()'s openMouth + eat
// sound and the item consume. A fully inapplicable feed (adult at
// full health, temper maxed, already in love) is a no-op that still
// consumed the interact (vanilla's PASS ends entity interactions).
func (s *Server) equineFeed(sess *Session, e *Entity, name, iname string, hslot int16, row equineFeedEntry, loveItems map[string]bool) {
	itemUsed := false
	if loveItems[iname] && e.Tamed && e.Age == 0 && e.loveTicks <= 0 {
		itemUsed = true
		e.loveTicks = 600
		e.loveCause = sess.getName()
		s.broadcastEntityEventTo(nil, e.Dim, e.ID, 18) // hearts
	}
	if maxH := mobHealth[name]; e.Health < maxH && row.heal > 0 {
		e.Health = float32(math.Min(float64(e.Health+row.heal), float64(maxH)))
		itemUsed = true
	}
	if e.Baby && row.ageUp > 0 && !e.AgeLocked {
		// handleEating's addParticle(HAPPY_VILLAGER) + server-side
		// ageUp — the table stores vanilla's seconds, Age walks
		// toward grown at seconds*20 ticks.
		s.broadcastParticles(e.Dim, 50, // happy_villager
			e.X+(rand.Float64()*2-1)*1.0, e.Y+0.5,
			e.Z+(rand.Float64()*2-1)*1.0, 0, 0, 0, 0, 1)
		e.Age += int32(row.ageUp) * 20
		if e.Age >= 0 {
			e.Age = 0
			e.Baby = false
			e.MetaDirty = true
		}
		itemUsed = true
	}
	maxTemper := int32(100)
	if name == "llama" || name == "trader_llama" {
		maxTemper = 30
	}
	if row.temper > 0 && (itemUsed || !e.Tamed) && e.Temper < maxTemper {
		e.Temper += int32(row.temper)
		if e.Temper > maxTemper {
			e.Temper = maxTemper
		}
		itemUsed = true
	}
	if !itemUsed {
		return
	}
	// eating(): openMouth's flag-0x40 pulse + the positional eat
	// sound (pitch 1±0.2 random — vanilla's double-random band).
	e.HorseMouth = true
	e.mouthCounter = 1
	e.MetaDirty = true
	if snd, ok := equineEatSound[name]; ok {
		if e.Baby && name == "horse" {
			snd = "entity.baby_horse.eat"
		}
		if id, ok2 := SoundEventID(snd); ok2 {
			pitch := 1.0 + (rand.Float32()-rand.Float32())*0.2
			s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, id, mobVoiceOf(e).src(), 1.0, pitch, nil)
		}
	}
	if d := s.dims[e.Dim]; d != nil {
		s.emitGameEvent(d, "eat", e.X, e.Y, e.Z, nil, "")
	}
	s.broadcastEntityAnimation(sess, sess.entityID.Load(), 0)
	if sess.mode() == GameModeSurvival || sess.mode() == GameModeAdventure {
		sess.consumeSlot(hslot, 1)
	}
}

// equineMakeMad is AbstractHorse.makeMad: a not-standing mount rears
// (setStanding 20 — flag 0x20) and plays the angry sound.
func (s *Server) equineMakeMad(e *Entity, name string) {
	if e.HorseStanding {
		return
	}
	// canPerformRearing is false only for llamas — they still get
	// the angry sound, just no standing flag.
	if name != "llama" && name != "trader_llama" {
		e.HorseStanding = true
		e.standCounter = 20
		e.MetaDirty = true
	}
	if snd, ok := equineAngrySound[name]; ok {
		if e.Baby && name == "horse" {
			snd = "entity.baby_horse.angry"
		}
		if id, ok2 := SoundEventID(snd); ok2 {
			// makeSound plays at getSoundVolume() (0.8 for the
			// equine family) and the random getVoicePitch band.
			pitch := 1.0 + (rand.Float32()-rand.Float32())*0.2
			s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, id, mobVoiceOf(e).src(), 0.8, pitch, nil)
		}
	}
}

// equineMaxTemper is getMaxTemper: 100 for the horse family, 30 for
// llamas.
func equineMaxTemper(name string) int32 {
	if name == "llama" || name == "trader_llama" {
		return 30
	}
	return 100
}

// equineRideTameStep is RunAroundLikeCrazyGoal (priority 1, MOVE flag):
// while an untamed equine carries a rider the goal flees between
// random DefaultRandomPos.getPos(5,4) targets at speedModifier 1.2 and
// rolls nextInt(reducedTickDelay(50))==0 per tick — a player rider
// rolls nextInt(maxTemper)<temper to tame, otherwise modifyTemper(5)
// and everyone is bucked with makeMad + the entity_event(6) smoke.
// Non-player first passengers skip the temper arm but still eject.
func (s *Server) equineRideTameStep(e *Entity, name string) {
	// Camel.isTamed() is literal true — a ridden camel never arms
	// RunAroundLikeCrazy, so the buck/eject path can't fire.
	if e.NoAI || e.Tamed || name == "camel" || name == "camel_husk" ||
		len(e.Passengers) == 0 {
		e.FleeRidden = false
		return
	}
	// ZombieHorse.isMobControlled is the family's one override:
	// firstPassenger instanceof Mob — a mob-ridden zombie horse is
	// controlled, so the goal's canUse fails and it never flees.
	if name == "zombie_horse" && s.sessionByEntityID(e.Passengers[0]) == nil {
		e.FleeRidden = false
		return
	}
	e.FleeRidden = true
	if s.rng.intn(25) != 0 {
		return
	}
	pid := e.Passengers[0]
	if rs := s.sessionByEntityID(pid); rs != nil {
		maxT := equineMaxTemper(name)
		if maxT > 0 && int32(s.rng.intn(int(maxT))) < e.Temper {
			s.equineTameWithName(rs, e, name)
			return
		}
		e.Temper += 5
		if e.Temper > maxT {
			e.Temper = maxT
		}
	}
	// ejectPassengers: every rider off with stopRiding's dismount
	// flow (player sessions) or removePassenger's 60-tick boarding
	// cooldown (mob riders).
	for _, pid := range append([]int32(nil), e.Passengers...) {
		if rs := s.sessionByEntityID(pid); rs != nil {
			s.dismount(rs)
			continue
		}
		if rp, ok := s.entities.GetPtr(pid); ok {
			rp.Riding = 0
			rp.BoardingCooldown = 60
			rp.PosDirty = true
		}
		keep := e.Passengers[:0]
		for _, q := range e.Passengers {
			if q != pid {
				keep = append(keep, q)
			}
		}
		e.Passengers = keep
	}
	e.FleeRidden = false // isVehicle() false → the goal stops this tick
	s.broadcastSetPassengers(e)
	s.equineMakeMad(e, name)
	s.broadcastEntityEventTo(nil, e.Dim, e.ID, 6)
}

// equineTameWithName is AbstractHorse.tameWithName: setOwner +
// setTamed, the TAME_ANIMAL criteria trigger, and entity_event(7)
// hearts. No swing — it fires from the goal tick, not an interact.
func (s *Server) equineTameWithName(rs *Session, e *Entity, name string) {
	e.owner = rs.getName()
	e.ownerUUID = rs.uuid
	e.Tamed = true
	e.MetaDirty = true // DATA_ID_FLAGS 0x02
	e.FleeRidden = false
	s.broadcastEntityEventTo(nil, e.Dim, e.ID, 7)
	s.fireTamedAnimal(rs, name)
}

// camelFood maps the camel family's isFood tag to its one item:
// CAMEL_FOOD is cactus, CAMEL_HUSK_FOOD is rabbit_foot.
var camelFood = map[string]string{
	"camel":      "cactus",
	"camel_husk": "rabbit_foot",
}

// camelInteract is Camel.mobInteract's food arm. Sneaking on an adult
// skips to openCustomInventoryScreen (the generic inventory arm
// downstream owns that). Non-food clicks fall through to doPlayerRide
// — the ride path lives downstream too. The camel family has no
// temper or makeMad arm.
func (s *Server) camelInteract(sess *Session, eid int32, hand int32, sneaking bool) bool {
	s.entities.mu.RLock()
	e, ok := s.entities.byID[eid]
	s.entities.mu.RUnlock()
	if !ok || !e.Living {
		return false
	}
	name, ok := EntityTypeName(e.Type)
	if !ok {
		return false
	}
	food, isCamel := camelFood[name]
	if !isCamel || e.Health <= 0 {
		return false
	}
	if sneaking && !e.Baby {
		return false
	}
	held, hslot := sess.stackForHand(hand)
	if held.Empty() {
		return false
	}
	iname, ok := itemNameOf(held.ID)
	if !ok || iname != food {
		return false
	}
	s.camelFeed(sess, e, name, hslot)
	return true
}

// camelFeed is Camel.handleEating: independent couldHeal (2.0),
// couldSetInLove, and couldAgeUp arms — Camel.isTamed reports true
// always, so the love arm fires on any adult at Age 0 (breeding is
// gated separately by canMate, which camel_husk fails). When no arm
// applied, fedFood returns PASS and the item isn't consumed.
func (s *Server) camelFeed(sess *Session, e *Entity, name string, hslot int16) {
	itemUsed := false
	if e.Health < e.MaxHealth {
		e.Health = float32(math.Min(float64(e.Health+2.0), float64(e.MaxHealth)))
		itemUsed = true
	}
	// couldSetInLove additionally needs canFallInLove — CamelHusk's
	// override reports false: rabbit_foot can heal it but never loves.
	if e.Age == 0 && !e.Baby && e.loveTicks <= 0 && name != "camel_husk" {
		e.loveTicks = 600
		e.loveCause = sess.getName()
		s.broadcastEntityEventTo(nil, e.Dim, e.ID, 18) // hearts
		itemUsed = true
	}
	if e.Baby && !e.AgeLocked {
		s.broadcastParticles(e.Dim, 50, // happy_villager
			e.X+(rand.Float64()*2-1)*1.0, e.Y+0.5,
			e.Z+(rand.Float64()*2-1)*1.0, 0, 0, 0, 0, 1)
		e.Age += 10 * 20 // ageUp(10) is vanilla seconds
		if e.Age >= 0 {
			e.Age = 0
			e.Baby = false
			e.MetaDirty = true
		}
		itemUsed = true
	}
	if !itemUsed {
		return
	}
	snd := "entity.camel.eat"
	if name == "camel_husk" {
		snd = "entity.camel_husk.eat"
	}
	if id, ok := SoundEventID(snd); ok {
		pitch := 1.0 + (rand.Float32()-rand.Float32())*0.2
		s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, id, mobVoiceOf(e).src(), 1.0, pitch, nil)
	}
	if d := s.dims[e.Dim]; d != nil {
		s.emitGameEvent(d, "eat", e.X, e.Y, e.Z, nil, "")
	}
	s.broadcastEntityAnimation(sess, sess.entityID.Load(), 0)
	if sess.mode() == GameModeSurvival || sess.mode() == GameModeAdventure {
		sess.consumeSlot(hslot, 1)
	}
}
