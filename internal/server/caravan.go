package server

import (
	"math"

	"mc-go/internal/world"
)

// LlamaFollowCaravanGoal (goal priority 2 on llama + trader_llama —
// it outranks the family's PanicGoal(3), BreedGoal(4), TemptGoal(5)
// and the WaterAvoidingRandomStrollGoal(7)). Measured off the 26.2
// decompile (goal/LlamaFollowCaravanGoal.java):
//
//	canUse: an unleashed llama outside any caravan scans a 9×4×9
//	box for other llamas. It prefers the closest EXISTING caravan
//	member that isn't already tailed (that llama is the chain end);
//	failing that, the closest leashed llama with no tail. Joins are
//	refused when the candidate is closer than 4.0 (dist²), or when
//	the candidate's head-chain never reaches a leashed llama within
//	8 hops. A join writes caravanHead on the follower and
//	caravanTail on the head — runtime links, never saved.
//
//	canContinueToUse: inCaravan && head alive && some head within 8
//	hops of me is leashed. Past 26 blocks of head distance the
//	speedModifier (starts 2.1) ramps ×1.2 per check to a 3.0 cap,
//	and a 40-tick counter at the cap drops the caravan entirely.
//
//	tick: the follower paths to a point trailing its head by two
//	blocks — its own position plus (head-self).normalize()·max(
//	distance-2, 0) — unless a fence knot holds its own leash (a
//	fence-pinned follower stops moving; a player-held one still
//	follows, dragging the holder).
const (
	caravanJoinMinSq = 4.0
	caravanFarSq     = 676.0 // 26²
	caravanSpeedBase = 2.1
	caravanSpeedCap  = 3.0
	caravanBreakCnt  = 40
	caravanChainMax  = 8
	caravanTrail     = 2.0
)

// llamaCaravanStep runs the caravan goal for llama/trader_llama:
// join when unleashed and loose, follow while the chain holds.
func (s *Server) llamaCaravanStep(e *Entity, w *world.World) bool {
	name, _ := EntityTypeName(e.Type)
	if name != "llama" && name != "trader_llama" {
		return false
	}
	if e.CaravanHead == 0 {
		return s.llamaCaravanJoin(e)
	}
	return s.llamaCaravanFollow(e, w)
}

// llamaCaravanJoin is canUse: pick the closest viable head inside
// the 9×4×9 scan box — an untailed caravan member first, else an
// untailed leashed llama — then link behind it.
func (s *Server) llamaCaravanJoin(e *Entity) bool {
	if e.LeashHolder != 0 {
		return false
	}
	pick := func(inCaravan bool) (*Entity, float64) {
		best, bd := (*Entity)(nil), math.MaxFloat64
		for _, o := range s.entities.All() {
			if o.ID == e.ID || !o.Living || o.Dim != e.Dim || o.Dying || o.Dormant {
				continue
			}
			on, _ := EntityTypeName(o.Type)
			if on != "llama" && on != "trader_llama" {
				continue
			}
			if math.Abs(o.X-e.X) > 9 || math.Abs(o.Y-e.Y) > 4 || math.Abs(o.Z-e.Z) > 9 {
				continue
			}
			if (o.CaravanHead != 0) != inCaravan || o.CaravanTail != 0 {
				continue
			}
			if !inCaravan && o.LeashHolder == 0 {
				continue
			}
			d2 := (o.X-e.X)*(o.X-e.X) + (o.Y-e.Y)*(o.Y-e.Y) + (o.Z-e.Z)*(o.Z-e.Z)
			if d2 < bd {
				best, _ = s.entities.GetPtr(o.ID)
				bd = d2
			}
		}
		return best, bd
	}
	head, d2 := pick(true)
	if head == nil {
		head, d2 = pick(false)
	}
	if head == nil || d2 < caravanJoinMinSq {
		return false
	}
	// !closest.isLeashed() && !firstIsLeashed(closest, 1): a leashed
	// candidate joins unconditionally; an unleashed one (a caravan
	// member) only if ITS head-chain reaches a leash within 8 hops.
	if head.LeashHolder == 0 && !s.llamaChainLeashed(head, 1) {
		return false
	}
	e.CaravanHead = head.ID
	head.CaravanTail = e.ID
	e.CaravanSpeed = caravanSpeedBase
	e.CaravanCnt = 0
	return true
}

// llamaCaravanFollow is canContinueToUse + tick: keep the link while
// the chain is alive and leashed, ramp the speed past 26 blocks, and
// drive toward the two-block trail point.
func (s *Server) llamaCaravanFollow(e *Entity, w *world.World) bool {
	head, ok := s.entities.GetPtr(e.CaravanHead)
	if !ok || head.Dying || !s.llamaChainLeashed(e, 0) {
		s.llamaLeaveCaravan(e)
		return false
	}
	dx, dy, dz := head.X-e.X, head.Y-e.Y, head.Z-e.Z
	if dx*dx+dy*dy+dz*dz > caravanFarSq {
		if e.CaravanSpeed <= caravanSpeedCap {
			e.CaravanSpeed *= 1.2
			e.CaravanCnt = caravanBreakCnt
		} else if e.CaravanCnt == 0 {
			s.llamaLeaveCaravan(e)
			return false
		}
	}
	if e.CaravanCnt > 0 {
		e.CaravanCnt--
	}
	// A fence-held follower skips the moveTo; a player-held one
	// still follows and drags the holder behind it.
	if hn, ok2 := s.entities.GetPtr(e.LeashHolder); ok2 {
		if hn2, _ := EntityTypeName(hn.Type); hn2 == "leash_knot" {
			return true
		}
	}
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if dist <= caravanTrail {
		return true
	}
	// tick's moveTo lands on self + dir·max(dist-2, 0) — the point
	// two blocks short of the head along the join line.
	scale := (dist - caravanTrail) / dist
	tx, tz := e.X+dx*scale, e.Z+dz*scale
	en, _ := EntityTypeName(e.Type)
	speed, ok := entityAttrBases[en]["movement_speed"]
	if !ok || speed <= 0 {
		speed = mobWalkSpeed
	}
	mobTurnToward(e, tx-e.X, tz-e.Z, 90)
	e.LookYaw = e.Yaw
	s.mobTravelStep(e, w, speed*e.CaravanSpeed)
	return true
}

// llamaChainLeashed is firstIsLeashed: walk the caravanHead chain —
// a head that is itself leashed satisfies it, otherwise keep
// climbing while each head is still in a caravan, at most 8 hops.
func (s *Server) llamaChainLeashed(e *Entity, counter int) bool {
	if counter > caravanChainMax {
		return false
	}
	head, ok := s.entities.GetPtr(e.CaravanHead)
	if !ok {
		return false
	}
	if head.LeashHolder != 0 {
		return true
	}
	if head.CaravanHead != 0 {
		return s.llamaChainLeashed(head, counter+1)
	}
	return false
}

// llamaLeaveCaravan is the goal's stop(): unlink both sides of this
// node and reset the speed for the next run.
func (s *Server) llamaLeaveCaravan(e *Entity) {
	if head, ok := s.entities.GetPtr(e.CaravanHead); ok && head.CaravanTail == e.ID {
		head.CaravanTail = 0
	}
	e.CaravanHead = 0
	e.CaravanSpeed = caravanSpeedBase
	e.CaravanCnt = 0
}

// TraderLlama.maybeDespawn + WanderingTrader.maybeDespawn — the
// trader-family despawn countdowns. TraderLlama's gates are
// !tamed && !leashed-to-non-trader && !one-player-passenger &&
// !age-locked && !persistence-required; while leashed to a
// wandering_trader it copies the trader's own delay-1 instead of
// counting itself. At <=0 the leash drops (removeLeash — no item)
// and the mob discards. WanderingTrader's is the simpler arm:
// `despawnDelay > 0 && !isTrading() && --despawnDelay == 0`.
func (s *Server) traderDespawn(e *Entity) bool {
	name, _ := EntityTypeName(e.Type)
	switch name {
	case "wandering_trader":
		// isTrading — a trade session open on the trader — pauses
		// the countdown; mc-go has no trade sessions yet, so the
		// arm is unconditional past the delay sign.
		if e.DespawnDelay > 0 {
			e.DespawnDelay--
			if e.DespawnDelay == 0 {
				s.mobRemovedPassengerCleanup(e)
				s.entities.Remove(e.ID)
				s.broadcastEntityRemoveIn(e.Dim, nil, []int32{e.ID})
				return true
			}
		}
		return false
	case "trader_llama":
		if !s.traderLlamaCanDespawn(e) {
			return false
		}
		traderLinked := false
		if h, ok := s.entities.GetPtr(e.LeashHolder); ok {
			if hn, _ := EntityTypeName(h.Type); hn == "wandering_trader" {
				e.DespawnDelay = h.DespawnDelay - 1
				traderLinked = true
			}
		}
		if !traderLinked {
			e.DespawnDelay--
		}
		if e.DespawnDelay > 0 {
			return false
		}
		// removeLeash(): vanilla's leash drop on despawn keeps the
		// lead item (removeLeash vs dropLeash — no lead drop).
		if e.LeashHolder != 0 {
			s.leashDrop(e, false)
		}
		s.mobRemovedPassengerCleanup(e)
		s.entities.Remove(e.ID)
		s.broadcastEntityRemoveIn(e.Dim, nil, []int32{e.ID})
		return true
	}
	return false
}

// traderLlamaCanDespawn is TraderLlama.canDespawn: an untamed trader
// llama that isn't player-ridden, age-locked, persistence-required,
// or leashed to anything besides its wandering trader.
func (s *Server) traderLlamaCanDespawn(e *Entity) bool {
	if e.Tamed || e.AgeLocked || e.PersistenceRequired {
		return false
	}
	if h, ok := s.entities.GetPtr(e.LeashHolder); ok {
		if hn, _ := EntityTypeName(h.Type); hn != "wandering_trader" {
			return false
		}
	}
	if len(e.Passengers) == 1 && s.isPlayerEntity(e.Passengers[0]) {
		return false
	}
	return true
}

// traderLlamaHuntTypes is the trader_llama targetSelector(2) table:
// NearestAttackableTargetGoal(Zombie, !ZOMBIFIED_PIGLIN) —
// the whole zombie hierarchy minus the piglin — and
// NearestAttackableTargetGoal(AbstractIllager): evoker, illusioner,
// pillager, vindicator (ravager/witch ride the Raider branch, not
// AbstractIllager).
var traderLlamaHuntTypes = map[string]bool{
	"zombie": true, "zombie_villager": true, "drowned": true, "husk": true,
	"evoker": true, "illusioner": true, "pillager": true, "vindicator": true,
}

// llamaHunterStep is the llama family's mob-hunt targetSelector +
// RangedAttackGoal arm: every ~10 ticks it repicks the nearest valid
// target — an untamed wolf inside follow_range×0.25 (4.0 blocks) for
// both types, and for trader_llama also a zombie-family or illager
// inside the full 16 — and holds it while alive. While held, the
// RangedAttackGoal runs the shared 40-tick attackTime countdown
// (mob.go's LlamaSpitTicks block resolves LlamaSpitMob at fire), and
// drives toward the target past the 20-block attack radius.
func (s *Server) llamaHunterStep(e *Entity, w *world.World) bool {
	name, _ := EntityTypeName(e.Type)
	e.HuntScan--
	if e.HuntScan <= 0 {
		e.HuntScan = 10
		e.LlamaSpitMob = s.llamaHuntTarget(e, name, w)
	}
	m, ok := s.entities.GetPtr(e.LlamaSpitMob)
	if !ok || m.Dying || m.Dim != e.Dim {
		e.LlamaSpitMob = 0
		return false
	}
	// The target goal (priority 1-2) keeps the mark; RangedAttackGoal's
	// tick re-arms attackTime to the 40-tick interval every cycle, so
	// a held target takes a spit every ~2 seconds.
	if e.LlamaSpitTicks == 0 {
		e.LlamaSpitTicks = 40
	}
	dx, dz := m.X-e.X, m.Z-e.Z
	if dx*dx+dz*dz > 400 {
		// Past attackRadiusSqr 20² the goal paths in at its 1.25
		// speedModifier — the same nav drive the caravan uses.
		speed, ok2 := entityAttrBases[name]["movement_speed"]
		if !ok2 || speed <= 0 {
			speed = mobWalkSpeed
		}
		mobTurnToward(e, dx, dz, 90)
		e.LookYaw = e.Yaw
		s.mobTravelStep(e, w, speed*1.25)
	}
	return true
}

// llamaHuntTarget picks the nearest valid mark: wolves must be
// untamed (LlamaAttackWolfGoal's `!isTame` predicate), trader_llama
// adds the zombie/illager sets at full follow_range. Both legs run
// NearestAttackableTargetGoal's mustSee — clipSolidSegment between
// eye heights.
func (s *Server) llamaHuntTarget(e *Entity, name string, w *world.World) int32 {
	d := s.dims[e.Dim]
	if d == nil {
		return 0
	}
	ed := entityBBDims(e)
	trader := name == "trader_llama"
	best, bd := int32(0), math.MaxFloat64
	for _, o := range s.entities.All() {
		if o.ID == e.ID || !o.Living || o.Dim != e.Dim || o.Dying || o.Dormant {
			continue
		}
		on, _ := EntityTypeName(o.Type)
		var limit float64
		switch {
		case on == "wolf" && !o.Tamed:
			limit = 4.0 // follow_range × 0.25
		case trader && traderLlamaHuntTypes[on]:
			limit = 16.0
		default:
			continue
		}
		d2 := (o.X-e.X)*(o.X-e.X) + (o.Y-e.Y)*(o.Y-e.Y) + (o.Z-e.Z)*(o.Z-e.Z)
		if d2 > limit*limit || d2 >= bd {
			continue
		}
		od := entityBBDims(&o)
		if _, _, _, _, _, _, _, hit := s.clipSolidSegment(d,
			e.X, e.Y+ed[1]*0.85, e.Z,
			o.X, o.Y+od[1]*0.85, o.Z); hit {
			continue
		}
		best, bd = o.ID, d2
	}
	return best
}
