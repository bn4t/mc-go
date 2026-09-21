package server

import (
	"math"
	"math/rand"

	"mc-go/internal/world"
)

// cureTickStep advances zombie-villager conversion. Vanilla's
// ZombieVillager.tick decrements villagerConversionTime by
// getConversionProgress each tick while isConverting; at zero the
// zombie is replaced by a villager (finishConversion) and the player
// who started the cure earns cured_zombie_villager.
func (s *Server) cureTickStep() {
	zvType := mustEntityType("zombie_villager")
	s.entities.mu.RLock()
	var zvs []*Entity
	for _, e := range s.entities.byID {
		if e.Type == zvType && e.Converting && !e.Dying {
			zvs = append(zvs, e)
		}
	}
	s.entities.mu.RUnlock()
	for _, e := range zvs {
		e.ConversionTime -= int32(cureProgress(s, e))
		if e.ConversionTime <= 0 {
			s.finishCure(e)
		}
	}
}

// mobHasEffect mirrors LivingEntity.hasEffect for entities: the entry
// counts only while live — an expired-but-unticked instance is gone.
func mobHasEffect(s *Server, e *Entity, id int32) bool {
	if e.Effects == nil {
		return false
	}
	inst, ok := e.Effects[id]
	if ok && inst.expiry >= 0 && inst.expiry <= s.clock.count {
		return false
	}
	return ok
}

func mobAddEffect(e *Entity, id int32, amplifier int32, expiry int64) {
	if e.Effects == nil {
		e.Effects = map[int32]mobEffectInst{}
	}
	e.Effects[id] = mobEffectInst{amplifier: amplifier, expiry: expiry}
}

func mobRemoveEffect(e *Entity, id int32) {
	delete(e.Effects, id)
}

// cureZombieVillager is ZombieVillager.mobInteract: a golden apple on a
// weakness-afflicted zombie villager consumes one stack and starts the
// conversion timer (3600 + rand(2401) ticks), clears the weakness,
// adds strength for the conversion's length at vanilla's
// min(difficulty-1, 0) amplifier, and broadcasts entity event 16 (the
// cure sound). An apple without weakness returns CONSUME — nothing
// changes.
func (s *Server) cureZombieVillager(sess *Session, eid int32, hand int32) {
	s.entities.mu.RLock()
	e, ok := s.entities.byID[eid]
	s.entities.mu.RUnlock()
	if !ok || e.Dying || e.Type != mustEntityType("zombie_villager") || e.Converting {
		return
	}
	held, hslot := sess.stackForHand(hand)
	if name, ok2 := itemNameOf(held.ID); !ok2 || name != "golden_apple" || held.Empty() {
		return
	}
	weakness, _ := MobEffectID("weakness")
	if !mobHasEffect(s, e, weakness) {
		return
	}
	if sess.mode() == GameModeSurvival || sess.mode() == GameModeAdventure {
		sess.consumeSlot(hslot, 1)
	}
	s.entities.mu.Lock()
	e.Converting = true
	e.ConversionTime = int32(3600 + rand.Intn(2401))
	e.ConversionStarter = sess.getUUID()
	e.HasConvStarter = true
	mobRemoveEffect(e, weakness)
	strength, _ := MobEffectID("strength")
	amplifier := int32(s.difficulty) - 1
	if amplifier > 0 {
		amplifier = 0
	}
	mobAddEffect(e, strength, amplifier, s.clock.count+int64(e.ConversionTime))
	s.entities.mu.Unlock()
	s.broadcastEntityEventTo(nil, e.Dim, eid, 16)
	s.broadcastEntityAnimation(sess, sess.entityID.Load(), 0)
}

// cureProgress mirrors getConversionProgress: 1 per tick, plus a 1%
// roll that scans the ±4-exclusive box around the zombie for iron bars
// or beds (vanilla checks state.is(Blocks.IRON_BARS) or a BedBlock —
// each found block, capped at 14, adds 1 with 30% probability). The
// box origin uses Java's (int) truncation, not floor.
func cureProgress(s *Server, e *Entity) int {
	progress := 1
	if rand.Float32() >= 0.01 {
		return progress
	}
	w := s.dimWorld(e.Dim)
	if w == nil {
		return progress
	}
	bx, by, bz := int(e.X), int(e.Y), int(e.Z)
	found := 0
	for x := bx - 4; x < bx+4 && found < 14; x++ {
		for y := by - 4; y < by+4 && found < 14; y++ {
			for z := bz - 4; z < bz+4 && found < 14; z++ {
				blk := w.GetBlock(world.Pos{X: x, Y: y, Z: z})
				name, ok := world.StateOwner(blk)
				if !ok || (name != "iron_bars" && !isBedBlock(blk)) {
					continue
				}
				if rand.Float32() < 0.3 {
					progress++
				}
				found++
			}
		}
	}
	return progress
}

// finishCure replaces the zombie villager with a villager — vanilla's
// convertTo restores the new entity from the old one's NBT save, so
// position, NoAI, villagerdata, offers, equipment and the live
// effects map all carry over (the wire shows the copied strength plus
// the fresh nausea in the villager's first dirty resend). A villager
// created from restored data always packs villagerDataFinalized —
// readAdditionalSaveData forces it when VillagerData is present. The
// pairing burst for the villager precedes the zombie's
// remove_entities, matching vanilla's end-of-tick tracker order.
func (s *Server) finishCure(e *Entity) {
	var effects map[int32]mobEffectInst
	if e.Effects != nil {
		effects = make(map[int32]mobEffectInst, len(e.Effects))
		for k, inst := range e.Effects {
			effects[k] = inst
		}
	}
	v := s.entities.Spawn(&Entity{
		Type:              mustEntityType("villager"),
		Dim:               e.Dim,
		X:                 e.X,
		Y:                 e.Y,
		Z:                 e.Z,
		Yaw:               e.Yaw,
		Pitch:             e.Pitch,
		Living:            true,
		Health:            mobHealth["villager"],
		MaxHealth:         mobHealth["villager"],
		NoAI:              e.NoAI,
		Silent:            e.Silent,
		Profession:        e.Profession,
		Offers:            e.Offers,
		Baby:              e.Baby,
		Equips:            e.Equips,
		Effects:           effects,
		VillagerTypeID:    e.VillagerTypeID,
		VillagerProfID:    e.VillagerProfID,
		VillagerLevel:     e.VillagerLevel,
		VillagerFinalized: true,
	})
	s.broadcastFreshMob(v)
	s.entities.Remove(e.ID)
	s.broadcastEntityRemoveIn(e.Dim, nil, []int32{e.ID})
	if nausea, ok := MobEffectID("nausea"); ok {
		mobAddEffect(v, nausea, 0, s.clock.count+200)
	}

	if e.HasConvStarter {
		if sess := s.sessionByUUID(e.ConversionStarter); sess != nil {
			s.fireCuredZombieVillager(sess)
		}
	}
	if !e.Silent {
		s.broadcastLevelEvent(e.Dim, 1027, world.Pos{
			X: int(math.Floor(e.X)),
			Y: int(math.Floor(e.Y)),
			Z: int(math.Floor(e.Z)),
		}, 0)
	}
}
