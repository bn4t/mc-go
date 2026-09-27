package server

// fireworkrocket.go — FireworkRocketItem + FireworkRocketEntity parity
// (measured on the vanilla 26.2 jar):
//   - use (right-click on air) fires only while the user is
//     fall-flying: leashes the player holds drop with LEAD_BREAK, an
//     attached rocket spawns at the player's position, the stack
//     consumes one (creative keeps it) and ITEM_USED awards.
//   - useOn (right-click on a block) is a PASS while gliding;
//     otherwise a free-flight rocket spawns at the click point pushed
//     0.15 toward the face and the stack shrinks one in EVERY gamemode
//     (vanilla's ItemStack.shrink, not the creative-safe consume).
//   - Rocket tick: life==0 fires the FIREWORK_ROCKET_LAUNCH ambient
//     sound once; an attached rocket copies its owner's position every
//     tick (the client's own rocket applies the acceleration); a free
//     rocket's horizontal delta scales ×1.15/tick with a +0.04 y-draft
//     (shot-at-angle rockets fly fixed); life > lifetime explodes.
//   - explode: entity-event 17, EXPLODE game event, damage = 5+2*n per
//     the explosions list, attached entity takes the full amount,
//     living entities within 5 blocks take
//     dmg·sqrt((5-dist)/5), then discard.

import (
	"math"
	"math/rand"
	"strconv"
	"strings"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// srcAmbient is SoundSource.AMBIENT — firework launch sounds ride it.
const srcAmbient int32 = 8

// rocketFlight reads the FIREWORKS component's flight_duration —
// "flight;exp;exp;..." — 0 when absent (vanilla adds it onto a base
// of 1: lifetime = 10·(1+flight) + rand6 + rand7).
func rocketFlight(st ItemStack) int {
	if st.Fireworks == "" {
		return 0
	}
	n, _ := strconv.Atoi(strings.SplitN(st.Fireworks, ";", 2)[0])
	return n
}

// rocketExplosionCount counts the entries in the FIREWORKS
// component's explosions list (the ";" separators).
func rocketExplosionCount(st ItemStack) int {
	return strings.Count(st.Fireworks, ";")
}

// rocketLifetime is FireworkRocketEntity's lifetime roll:
// 10·(1+flightDuration) + nextInt(6) + nextInt(7).
func rocketLifetime(st ItemStack) int32 {
	return int32(10*(1+rocketFlight(st)) + rand.Intn(6) + rand.Intn(7))
}

// encodeRocketMetadata builds the Set Entity Data body for a
// firework_rocket: index 8 slot = the fireworks item stack, index 9
// optional_unsigned_int = the attached entity id (VarInt eid+1, 0
// absent — elided while default like vanilla's non-default packing),
// index 10 boolean = shot-at-angle (likewise elided while false).
// Serializer ids follow the 26.2 registration order: 7 = ITEM_STACK,
// 19 = OPTIONAL_UNSIGNED_INT, 8 = BOOLEAN.
func encodeRocketMetadata(e *Entity) []byte {
	var b protocol.Buffer
	b.Byte(8)
	b.Byte(7) // ITEM_STACK
	st := e.Item
	if st.ID == 0 {
		// DATA_ID_FIREWORKS_ITEM defaults to the item's default stack.
		if id, ok := world.ItemID("firework_rocket"); ok {
			st = ItemStack{ID: id, Count: 1}
		}
	}
	writeSlot(&b, st)
	if e.AttachedTo != 0 {
		b.Byte(9)
		b.Byte(19) // OPTIONAL_UNSIGNED_INT
		b.VarInt(e.AttachedTo + 1)
	}
	if e.ShotAtAngle {
		b.Byte(10)
		b.Byte(8) // BOOLEAN
		b.Byte(1)
	}
	b.Byte(0xFF)
	return b.Bytes()
}

// spawnFireworkRocket mints a rocket entity: the vanilla ctor's tiny
// triangle-jitter initial delta, lifetime from the stack's flight
// duration, the held stack copied into entity data. attachedTo is
// the entity id a boost rocket rides (0 for free-flight spawns).
func (s *Server) spawnFireworkRocket(d *dimension, st ItemStack, x, y, z float64, attachedTo, owner int32) *Entity {
	const j = 0.002297
	e := s.entities.Spawn(&Entity{
		Type:         mustEntityType("firework_rocket"),
		Dim:          d.name,
		X:            x,
		Y:            y,
		Z:            z,
		MotionX:      (rand.Float64() - rand.Float64()) * j,
		MotionY:      0.05 + (rand.Float64()-rand.Float64())*j,
		MotionZ:      (rand.Float64() - rand.Float64()) * j,
		Item:         st,
		AttachedTo:   attachedTo,
		Fuse:         rocketLifetime(st),
		RocketLife:   0,
		arrowShooter: owner,
	})
	s.broadcastEntitySpawn(nil, *e)
	s.broadcastEntityMetadata(e.ID, encodeRocketMetadata(e))
	return e
}

// useFireworkBoost is FireworkRocketItem.use — reached only while the
// sender is fall-flying (the handler gates it): leashes drop with
// LEAD_BREAK, an attached rocket spawns at the player's position, the
// stack consumes one (creative keeps it), ITEM_USED awards.
func (s *Server) useFireworkBoost(sess *Session, st ItemStack, hand int32) {
	eid := sess.entityID.Load()
	x, y, z, _, _, _ := sess.Pos()
	d := sess.dimension()
	// Entity.dropAllLeashConnections(player): every leashable the
	// player holds drops — LEAD_BREAK plays only when one was held.
	if s.dropPlayerLeashes(sess) {
		s.playSoundSrc(d.name, x, y, z, soundEventIDs["item.lead.break"], srcNeutral, 1.0, 1.0, nil)
	}
	s.spawnFireworkRocket(d, st, x, y, z, eid, eid)
	// ItemStack.consume(1, player): hasInfiniteMaterials keeps the
	// stack — only non-creative modes shrink.
	if sess.mode() != GameModeCreative {
		_, slot := sess.stackForHand(hand)
		s.consumeHandSlot(sess, slot, 1)
	}
	sess.addItemStat(statUsed, st.ID, 1)
}

// dropPlayerLeashes drops every leashable held by the player
// (LeashHolder == the player's entity id) — the player's side of
// Entity.dropAllLeashConnections.
func (s *Server) dropPlayerLeashes(sess *Session) bool {
	eid := sess.entityID.Load()
	var held []*Entity
	s.entities.mu.RLock()
	for _, l := range s.entities.byID {
		if l.LeashHolder == eid {
			held = append(held, l)
		}
	}
	s.entities.mu.RUnlock()
	for _, l := range held {
		s.leashDrop(l, true)
	}
	return len(held) > 0
}

// consumeHandSlot shrinks a hand slot by n — vanilla's bare
// ItemStack.shrink (creative pays too); the creative-safe paths gate
// on the caller's mode instead.
func (s *Server) consumeHandSlot(sess *Session, slot int16, n int32) {
	sess.invMu.Lock()
	it := sess.inv[slot]
	it.Count -= n
	if it.Count <= 0 {
		it = ItemStack{}
	}
	sess.inv[slot] = it
	sess.invState.Add(1)
	sess.invMu.Unlock()
	_ = s.sendContainerSlot(sess, slot, it)
}

// useFireworkPlace is FireworkRocketItem.useOn: a gliding user gets
// vanilla's PASS (silent); otherwise a free rocket spawns at the
// click location pushed 0.15 toward the face and the stack shrinks
// one regardless of gamemode.
func (s *Server) useFireworkPlace(sess *Session, hand int32, x, y, z int32, dx, dy, dz int32, cursor [3]float64) bool {
	st, slot := sess.stackForHand(hand)
	if st.Empty() {
		return false
	}
	if un, ok := itemNameOf(st.ID); !ok || un != "firework_rocket" {
		return false
	}
	if sess.fallFlying {
		// useOn returns PASS while gliding, which lets the caller's
		// fallback run ItemStack.use — the boost fires all the same.
		s.useFireworkBoost(sess, st, hand)
		return true
	}
	px := float64(x) + cursor[0] + float64(dx)*0.15
	py := float64(y) + cursor[1] + float64(dy)*0.15
	pz := float64(z) + cursor[2] + float64(dz)*0.15
	s.spawnFireworkRocket(sess.dimension(), st, px, py, pz, 0, sess.entityID.Load())
	s.consumeHandSlot(sess, slot, 1)
	return true
}

// fireworkTick is FireworkRocketEntity.tick, replacing the generic
// projectile move: launch sound at life 0, attached rockets ride
// their owner's position, free rockets accelerate with the ×1.15
// horizontal / +0.04 vertical draft, and life > lifetime explodes.
func (s *Server) fireworkTick(e *Entity) {
	if e.RocketLife == 0 && !e.Silent {
		s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, soundEventIDs["entity.firework_rocket.launch"], srcAmbient, 3.0, 1.0, nil)
	}
	e.RocketLife++
	if e.AttachedTo != 0 {
		if attached := s.sessionByEntityID(e.AttachedTo); attached != nil {
			x, y, z, _, _, _ := attached.Pos()
			// getHandHoldingItemAngle's small hand offset is unmodeled
			// (GOAL gap); the rocket rides at the owner's position.
			e.X, e.Y, e.Z = x, y, z
			s.broadcastEntityTeleport(nil, e.ID, e.X, e.Y, e.Z, e.Yaw, e.Pitch, false)
		}
	} else {
		// Free flight: horizontal delta scales ×1.15 each tick until a
		// horizontal collision latches the factor at 1.0 — mc-go
		// projectiles have no block collisions, so the accel always
		// applies; +0.04 y-draft. Shot-at-angle skips both.
		if !e.ShotAtAngle {
			e.MotionX *= 1.15
			e.MotionZ *= 1.15
			e.MotionY += 0.04
		}
		e.X += e.MotionX
		e.Y += e.MotionY
		e.Z += e.MotionZ
		s.broadcastEntityTeleport(nil, e.ID, e.X, e.Y, e.Z, e.Yaw, e.Pitch, false)
	}
	if e.RocketLife > e.Fuse {
		s.rocketExplode(e)
	}
}

// rocketExplode is FireworkRocketEntity.explode + dealExplosionDamage:
// entity-event 17 and discard always; with explosions the attached
// entity takes 5+2·n and living entities within 5 blocks take the
// sqrt falloff (LOS always clear on the flat harness world — a GOAL
// gap vs vanilla's two-point block clip).
func (s *Server) rocketExplode(e *Entity) {
	s.broadcastEntityEventTo(nil, e.Dim, e.ID, 17)
	n := rocketExplosionCount(e.Item)
	if n > 0 {
		dmg := float32(5 + n*2)
		src := damageSource{typeName: "fireworks", cause: e.arrowShooter, direct: e.ID}
		s.sessions.Range(func(_, v any) bool {
			sess := v.(*Session)
			if sess.state.Load() != StatePlay || sess.dimName() != e.Dim {
				return true
			}
			seid := sess.entityID.Load()
			px, py, pz, _, _, _ := sess.Pos()
			if seid == e.AttachedTo {
				// The attached entity takes the flat full damage.
				s.damageWith(sess, dmg, s.fireworksDeathMsg(sess, e), src)
				return true
			}
			dx, dy, dz := px-e.X, py-e.Y, pz-e.Z
			if dx*dx+dy*dy+dz*dz > 25 {
				return true
			}
			f := dmg * float32(math.Sqrt((5.0-math.Sqrt(dx*dx+dy*dy+dz*dz))/5.0))
			s.damageWith(sess, f, s.fireworksDeathMsg(sess, e), src)
			return true
		})
		s.entities.mu.RLock()
		mobs := make([]*Entity, 0, len(s.entities.byID))
		for _, m := range s.entities.byID {
			mobs = append(mobs, m)
		}
		s.entities.mu.RUnlock()
		for _, m := range mobs {
			if !m.Living || m.Dim != e.Dim || m.ID == e.AttachedTo {
				continue
			}
			dx, dy, dz := m.X-e.X, m.Y-e.Y, m.Z-e.Z
			d2 := dx*dx + dy*dy + dz*dz
			if d2 > 25 {
				continue
			}
			f := dmg * float32(math.Sqrt((5.0-math.Sqrt(d2))/5.0))
			var killer *Session
			if e.arrowShooter != 0 {
				killer = s.sessionByEntityID(e.arrowShooter)
			}
			s.damageMob(m, f, "fireworks", killer, &damageInfo{
				typeName: "fireworks", cause: e.arrowShooter, direct: e.ID,
			})
		}
	}
	s.entities.Remove(e.ID)
	s.broadcastEntityRemove([]int32{e.ID})
}

// fireworksDeathMsg picks the fireworks death message: "went off
// with a bang" alone, or "while fighting" when the rocket's owner
// is another player (the named-item .item variant is a GOAL gap).
func (s *Server) fireworksDeathMsg(victim *Session, e *Entity) component {
	if owner := s.sessionByEntityID(e.arrowShooter); owner != nil && owner != victim {
		return translatable("death.attack.fireworks.player",
			playerChatComponent(victim), playerChatComponent(owner))
	}
	return translatable("death.attack.fireworks", playerChatComponent(victim))
}

// applyRocketSummonNBT reads the rocket's /summon keys — Life,
// LifeTime, ShotAtAngle, FireworksItem ({id,count,components}). Only
// the fireworks component parses inside the item compound.
func applyRocketSummonNBT(e *Entity, tag *snbtTag) {
	if v, ok := tag.vals["Life"]; ok && v.kind == snbtNumber {
		e.RocketLife = int32(snbtNum(v))
	}
	if v, ok := tag.vals["LifeTime"]; ok && v.kind == snbtNumber {
		e.Fuse = int32(snbtNum(v))
	}
	if v, ok := tag.vals["ShotAtAngle"]; ok && (v.num == "1b" || v.num == "1" || v.str == "true") {
		e.ShotAtAngle = true
	}
	if v, ok := tag.vals["FireworksItem"]; ok && v.kind == snbtCompound {
		e.Item = snbtItemCost(v)
		if e.Item.ID == 0 {
			if id, ok := world.ItemID("firework_rocket"); ok {
				e.Item.ID = id
			}
		}
		if comps, ok := v.vals["components"]; ok && comps.kind == snbtCompound {
			for key, cv := range comps.vals {
				short := strings.TrimPrefix(key, "minecraft:")
				if short != "fireworks" || cv.kind != snbtCompound {
					continue
				}
				fv, ok := cv.vals["flight_duration"]
				if !ok || fv.kind != snbtNumber {
					continue
				}
				var b strings.Builder
				b.WriteString(strconv.Itoa(int(snbtNum(fv))))
				if ev, ok := cv.vals["explosions"]; ok && ev.kind == snbtList {
					for _, it := range ev.items {
						exp, ok2 := parseFireworkExplosionSNBT(it)
						if !ok2 {
							continue
						}
						b.WriteString(";" + exp)
					}
				}
				e.Item.Fireworks = b.String()
			}
		}
	}
}
