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
// roll that scans the 9x9x9 box around the zombie for beds (each found
// bed, capped at 14, adds 1 with 30% probability).
func cureProgress(s *Server, e *Entity) int {
	progress := 1
	if rand.Float32() >= 0.01 {
		return progress
	}
	w := s.dimWorld(e.Dim)
	if w == nil {
		return progress
	}
	bx, by, bz := int(math.Floor(e.X)), int(math.Floor(e.Y)), int(math.Floor(e.Z))
	found := 0
	for x := bx - 4; x <= bx+4 && found < 14; x++ {
		for y := by - 4; y <= by+4 && found < 14; y++ {
			for z := bz - 4; z <= bz+4 && found < 14; z++ {
				if isBedBlock(w.GetBlock(world.Pos{X: x, Y: y, Z: z})) {
					found++
					if rand.Float32() < 0.3 {
						progress++
					}
				}
			}
		}
	}
	return progress
}

// finishCure replaces the zombie villager with a villager (new entity
// id, same position/villager data/offers), fires the advancement
// trigger for the curing player, and plays the cure level event.
func (s *Server) finishCure(e *Entity) {
	s.entities.Remove(e.ID)
	s.broadcastEntityRemoveIn(e.Dim, nil, []int32{e.ID})

	v := s.entities.Spawn(&Entity{
		Type:       mustEntityType("villager"),
		Dim:        e.Dim,
		X:          e.X,
		Y:          e.Y,
		Z:          e.Z,
		Yaw:        e.Yaw,
		Pitch:      e.Pitch,
		Living:     true,
		NoAI:       e.NoAI,
		Profession: e.Profession,
		Offers:     e.Offers,
		Baby:       e.Baby,
	})
	s.broadcastEntitySpawn(nil, *v)

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
