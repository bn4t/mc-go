package server

import (
	"math"

	"mc-go/internal/world"
	"strings"

	"mc-go/internal/protocol"
)

// Entity animation ids (Entity Animation packet).
const (
	animSwingMainArm   = 0
	animSwingOffhand   = 3
	animCriticalEffect = 4
	animMagicCritical  = 5
)

// handleSwing broadcasts the arm swing animation to other players.
func (s *Server) handleSwing(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	hand, err := r.VarInt()
	if err != nil {
		return
	}
	anim := byte(animSwingMainArm)
	if hand == 1 {
		anim = animSwingOffhand
	}
	s.broadcastEntityAnimation(sess, sess.entityID.Load(), anim)
}

// broadcastEntityAnimation sends one Entity Animation to every other
// spawned play-stage session in the subject's dimension.
func (s *Server) broadcastEntityAnimation(except *Session, eid int32, anim byte) {
	if eid == 0 {
		return
	}
	dim := dimOverworld
	if e, ok := s.entities.Get(eid); ok {
		dim = e.Dim
	}
	var b protocol.Buffer
	b.VarInt(eid)
	b.Byte(anim)
	body := b.Bytes()
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess == except || sess.state.Load() != StatePlay || !sess.spawned.Load() {
			return true
		}
		if sess.dimName() != dim {
			return true
		}
		_ = sess.write(sess.ids().PlayEntityAnimation, body)
		return true
	})
}

// Player Action statuses that are not block breaking.
const (
	actionDropStack  = 3
	actionDropItem   = 4
	actionReleaseUse = 5
	actionSwapHands  = 6
)

// handlePlayerActionExtras handles the non-digging Player Action
// statuses: drop item/stack and swap with offhand.
func (s *Server) handlePlayerActionExtras(sess *Session, p protocol.Packet) bool {
	r := protocol.NewPacketReader(p.Data)
	status, err := r.VarInt()
	if err != nil {
		return false
	}
	switch status {
	case actionDropStack, actionDropItem:
		if _, _, _, err := r.Position(); err != nil {
			return false
		}
		whole := status == actionDropStack
		s.dropHeld(sess, whole)
		return true
	case actionReleaseUse:
		sess.blocking.Store(false)
		// stopUsingItem posts ITEM_INTERACT_FINISH whenever a use was
		// in progress — a sneaking player's emission every sculk
		// sensor in earshot dampens, earning the sneak
		// avoid_vibration.
		if sess.sneaking && (sess.bowStartTick != 0 || sess.crossbowStartTick != 0 || sess.tridentStartTick != 0 || sess.eatingTicks > 0) {
			x, y, z, _, _, _ := sess.Pos()
			s.fireAvoidVibration(sess, x, y, z, sess.dimName())
		}
		s.releaseBow(sess)
		s.releaseTrident(sess)
		s.releaseCrossbow(sess)
		sess.usingItemID = 0
		return true
	case actionSwapHands:
		s.swapHands(sess)
		return true
	}
	return false
}

// dropHeld throws the selected hotbar stack — all of it, or a single
// item — spawning the item entity and counting the drop stats.
func (s *Server) dropHeld(sess *Session, whole bool) {
	slot := invSlotForHotbar(sess.held.Load())
	sess.invMu.Lock()
	it := sess.inv[slot]
	if it.Empty() {
		sess.invMu.Unlock()
		return
	}
	out := it
	if !whole {
		out = it.withCount(1)
	}
	sess.inv[slot].Count -= out.Count
	if sess.inv[slot].Count <= 0 {
		sess.inv[slot] = ItemStack{}
	}
	updated := sess.inv[slot]
	sess.invState.Add(1)
	s.dropItemLocked(sess, out)
	sess.invMu.Unlock()
	_ = s.sendContainerSlot(sess, slot, updated)
	s.fireInventoryChanged(sess)
}

// swapHands swaps the held hotbar stack with the offhand stack.
func (s *Server) swapHands(sess *Session) {
	slot := invSlotForHotbar(sess.held.Load())
	sess.invMu.Lock()
	sess.inv[slot], sess.inv[slotOffhand] = sess.inv[slotOffhand], sess.inv[slot]
	main := sess.inv[slot]
	off := sess.inv[slotOffhand]
	sess.invMu.Unlock()
	_ = s.sendContainerSlot(sess, slot, main)
	_ = s.sendContainerSlot(sess, slotOffhand, off)
}

// attackReach is the vanilla entity_interaction_range base value.
const attackReach = 3.0

// withinAttackRange is vanilla's isWithinAttackRange: the clamped
// distance from the attacker's eye position to the target's bounding
// box must stay inside entity_interaction_range (3.0 base).
func (sess *Session) withinAttackRange(cx, cy, cz float64, bb [2]float64) bool {
	x, y, z, _, _, _ := sess.Pos()
	eye := y + 1.62
	hw := bb[0] / 2
	gx := math.Max(math.Max(cx-hw-x, x-cx-hw), 0)
	gy := math.Max(math.Max(cy-eye, eye-cy-bb[1]), 0)
	gz := math.Max(math.Max(cz-hw-z, z-cz-hw), 0)
	return gx*gx+gy*gy+gz*gz <= attackReach*attackReach
}

// handleAttack applies a melee attack to another player: damage, hurt
// animation, and knockback (motion sync). Entity types beyond players
// arrive with the entity milestone.
func (s *Server) handleAttack(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	targetID, err := r.VarInt()
	if err != nil {
		return
	}
	s.attackEntity(sess, targetID)
}

// attackEntity applies a melee hit to one entity id, shared by the
// Attack packet and the Interact packet's attack action so both routes
// produce identical damage, combat, and animation packets.
func (s *Server) attackEntity(sess *Session, targetID int32) {
	if sess.mode() == GameModeSpectator || sess.dead {
		return
	}
	if targetID == 0 || targetID == sess.entityID.Load() {
		return
	}
	target := s.sessionByEntityID(targetID)
	if target == nil {
		// Not a player: attack a living mob if one has this id. The
		// reach gate is vanilla's isWithinAttackRange — the eye to the
		// target's bounding box, not center distance.
		if e, ok := s.entities.Get(targetID); ok && e.Living {
			bb := mobBounds(e.Type)
			if !sess.withinAttackRange(e.X, e.Y, e.Z, bb) {
				return
			}
			plan := s.planAttack(sess)
			s.preAttackSound(sess, plan)
			sess.attackTicker = 0
			dealt, graced, died := s.damageMob(&e, plan.damage)
			if dealt > 0 {
				// Melee lands on player_hurt_entity too: the direct
				// entity is the player and there's no projectile tag.
				// Vanilla's trigger reads the RAW damage amount, not
				// the health-capped value damageMob reports.
				mh, _ := itemNameOf(sess.heldStack().ID)
				s.firePlayerHurtEntity(sess, "player", false, plan.damage, mh, plan.smash)
				dmgType := "player_attack"
				if plan.smash {
					dmgType = "mace_smash"
				}
				if !graced {
					s.broadcastDamageEvent(e.ID, damageInfo{
						typeName: dmgType,
						cause:    sess.entityID.Load(),
						direct:   sess.entityID.Load(),
					})
				}
				sess.addCustomStat("damage_dealt", int32(math.Round(float64(dealt*10))))
				if died {
					sess.addStat(statKilled, e.Type, 1)
					sess.addCustomStat("mob_kills", 1)
					s.onPlayerKilledMob(sess, e)
				}
				if !graced {
					s.mobAttackKnockback(sess, e.ID, plan.scale)
				}
				if plan.smash {
					s.maceSmashAftermath(sess, e)
				}
				s.attackAftermath(sess, plan, e.ID, dealt, mobBounds(e.Type))
			} else {
				s.broadcastAttackSound(sess, sndAttackNodamage)
			}
			s.attackWear(sess)
		}
		return
	}
	if target.dead {
		return
	}
	tx, ty, tz, _, _, _ := target.Pos()
	if !sess.withinAttackRange(tx, ty, tz, [2]float64{0.6, 1.8}) {
		return
	}
	ax, _, az, _, _, _ := sess.Pos()
	sess.addExhaustion(0.1) // attacking costs hunger
	s.startCombat(sess)
	s.startCombat(target)
	// damageWith wears the armor and applies the armor reduction
	// inside (vanilla's getDamageAfterArmorAbsorb); the attacker's
	// damage_dealt counts the post-absorb amount. The cooldown scale
	// is read before the ticker resets, as in vanilla's attack().
	plan := s.planAttack(sess)
	s.preAttackSound(sess, plan)
	dmgType := "player_attack"
	if plan.smash {
		dmgType = "mace_smash"
	}
	dealt := s.damageWith(target, plan.damage, translatable("death.attack."+dmgType, literal(target.getName()), literal(sess.getName())), damageSource{
		typeName: dmgType,
		cause:    sess.entityID.Load(),
		direct:   sess.entityID.Load(),
	})
	if dealt > 0 {
		mh, _ := itemNameOf(sess.heldStack().ID)
		s.firePlayerHurtEntity(sess, "player", false, plan.damage, mh, plan.smash)
		if plan.smash {
			tx, ty, tz, _, _, _ := target.Pos()
			s.maceSmashAftermath(sess, Entity{
				X: tx, Y: ty, Z: tz,
				Dim: target.dimName(),
				ID:  target.entityID.Load(),
			})
		}
	}
	sess.attackTicker = 0
	sess.addCustomStat("damage_dealt", int32(math.Round(float64(dealt*10))))
	s.sendHurtAnimation(target, hurtYaw(tx, tz, ax, az))
	if dealt > 0 {
		s.playerAttackKnockback(sess, target, plan.scale)
		s.attackAftermath(sess, plan, target.entityID.Load(), dealt, [2]float64{0.6, 1.8})
	} else {
		s.broadcastAttackSound(sess, sndAttackNodamage)
	}
	// hurtEnemy + postHurtEnemy: the used stat and the weapon wear.
	s.attackWear(sess)
}

// Attack sound_event registry ids (vanilla 26.2). The wire value is
// the id + 1: SoundEvent packets carry a registry Holder, and holder
// ids are encoded one-based so zero can mean an inline event.
const (
	sndAttackCrit      int32 = 1302
	sndAttackKnockback int32 = 1303
	sndAttackNodamage  int32 = 1304
	sndAttackStrong    int32 = 1305
	sndAttackSweep     int32 = 1306
	sndAttackWeak      int32 = 1307
)

// attackPlan is the swing classification vanilla's Player.attack
// computes before damage lands: the cooldown scale, whether the hit is
// strong (>0.9), the sprint-knockback and crit flags, and the sweep
// flag a strong standing sword hit earns.
type attackPlan struct {
	scale    float32
	damage   float32
	strong   bool
	sprintKb bool
	crit     bool
	sweep    bool
	smash    bool
}

// planAttack mirrors Player.attack's prelude: scale read before the
// ticker resets, sprint knockback only on a strong hit, crit needing
// accumulated fall distance while airborne, sweep a strong non-crit
// non-sprint hit that lands while standing still with a sword.
func (s *Server) planAttack(sess *Session) attackPlan {
	scale := sess.attackScale()
	_, _, _, _, _, onGround := sess.Pos()
	strong := scale > 0.9
	sprintKb := sess.sprinting && strong
	crit := strong && sess.fallDistance > 0 && !onGround && !sess.sprinting
	// Vanilla's isSweepAttack checks the client-reported horizontal
	// movement stays under (movement speed * 2.5) squared; the base
	// move speed attribute is 0.1, so the bound is 0.0625.
	sweep := strong && !crit && !sprintKb && onGround && sess.lastHorizSq < 0.0625 && sess.heldIsSword()
	damage := sess.attackDamage()
	// MaceItem.getAttackDamageBonus adds the fall-scaled bonus to the
	// hit before the crit multiply — vanilla's order — and
	// canSmashAttack gates on fallDistance > 1.5 while airborne.
	smash := false
	if n, ok := itemNameOf(sess.heldStack().ID); ok && n == "mace" && sess.fallDistance > 1.5 && !onGround {
		smash = true
		damage += maceSmashBonus(sess.fallDistance)
	}
	if crit {
		damage *= 1.5
	}
	return attackPlan{
		scale:    scale,
		damage:   damage,
		strong:   strong,
		sprintKb: sprintKb,
		crit:     crit,
		sweep:    sweep,
		smash:    smash,
	}
}

// maceSmashBonus is vanilla MaceItem.getAttackDamageBonus's fall
// bonus (density/breach enchant modifiers not modeled): piecewise
// 4·fall under 3, 12+2·(fall-3) to 8, then 22+(fall-8).
func maceSmashBonus(fall float64) float32 {
	switch {
	case fall <= 3:
		return float32(4 * fall)
	case fall <= 8:
		return float32(12 + 2*(fall-3))
	default:
		return float32(22 + (fall - 8))
	}
}

// maceSmashAftermath is the hurtEnemy tail of a mace smash: the
// attacker's fall stops dead (velocity ~0.01 up, fall distance reset
// so the landing deals no fall damage), the smash sound + level
// event 2013 particles play at the impact, and every living entity
// inside the victim's 3.5-radius blast takes the fall-proportional
// knockback — attacker and victim excepted.
func (s *Server) maceSmashAftermath(sess *Session, victim Entity) {
	snd := int32(960) // item.mace.smash_ground
	if sess.fallDistance > 5 {
		snd = 961 // item.mace.smash_ground_heavy
	}
	sess.fallDistance = 0
	sess.fallStart = false
	s.playSoundSrc(sess.dimName(), victim.X, victim.Y, victim.Z, snd, 6, 1.0, 1.0, nil)
	s.broadcastLevelEvent(victim.Dim, 2013, world.Pos{X: int(math.Floor(victim.X)), Y: int(math.Floor(victim.Y)), Z: int(math.Floor(victim.Z))}, 750)
	s.maceSmashKnockback(sess, victim)
}

// maceSmashKnockback pushes every living entity inside the victim's
// box inflated by SMASH_ATTACK_KNOCKBACK_RADIUS (3.5): vanilla scales
// each push by (3.5 - distance) * 0.7 * (1 - knockback resistance),
// along the direction from the victim to the entity.
func (s *Server) maceSmashKnockback(sess *Session, victim Entity) {
	s.entities.mu.RLock()
	targets := make([]*Entity, 0, len(s.entities.byID))
	for _, e := range s.entities.byID {
		targets = append(targets, e)
	}
	s.entities.mu.RUnlock()
	for _, e := range targets {
		if !e.Living || e.Dying || e.ID == victim.ID || e.ID == sess.entityID.Load() || e.Dim != victim.Dim {
			continue
		}
		dx, dy, dz := e.X-victim.X, e.Y-victim.Y, e.Z-victim.Z
		dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
		if dist > 3.5 || dist < 1e-9 {
			continue
		}
		res := float64(0)
		if name, ok := EntityTypeName(e.Type); ok {
			res = mobKnockbackResistance[name]
		}
		power := (3.5 - dist) * 0.7 * (1 - res)
		e.MotionX += dx / dist * power
		e.MotionY += 0.7 * power
		e.MotionZ += dz / dist * power
		s.entities.SetMotion(e.ID, e.MotionX, e.MotionY, e.MotionZ)
		s.broadcastEntityMotion(e.Dim, e.ID, e.MotionX, e.MotionY, e.MotionZ)
	}
}

// broadcastLevelEvent sends a Level Event packet (global effects /
// particles like the mace smash burst) to every spawned session in
// the dimension.
func (s *Server) broadcastLevelEvent(dim string, event int32, pos world.Pos, data int32) {
	var b protocol.Buffer
	b.VarInt(event)
	b.Position(int32(pos.X), int32(pos.Y), int32(pos.Z))
	b.Int(data)
	b.Bool(false)
	body := b.Bytes()
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || !sess.spawned.Load() || sess.dimName() != dim {
			return true
		}
		_ = sess.write(sess.ids().PlayLevelEvent, body)
		return true
	})
}

// preAttackSound plays the sprint-knockback sound vanilla emits ahead
// of the hit when a sprint hit lands strong.
func (s *Server) preAttackSound(sess *Session, plan attackPlan) {
	if plan.sprintKb {
		s.broadcastAttackSound(sess, sndAttackKnockback)
	}
}

// attackAftermath runs the post-hit tail of Player.attack: the sweep
// (neighbors hurt, sweep sound + particle), the crit/strong/weak sound
// and crit animation, then the damage-indicator hearts.
func (s *Server) attackAftermath(sess *Session, plan attackPlan, victimEID int32, dealt float32, victimBB [2]float64) {
	if plan.sweep {
		s.doSweepAttack(sess, victimEID, plan, victimBB)
	}
	switch {
	case plan.crit:
		s.broadcastAttackSound(sess, sndAttackCrit)
		s.broadcastEntityAnimation(nil, victimEID, animCriticalEffect)
	case !plan.sweep:
		if plan.strong {
			s.broadcastAttackSound(sess, sndAttackStrong)
		} else {
			s.broadcastAttackSound(sess, sndAttackWeak)
		}
	}
	if dealt > 2 {
		e, ok := s.entities.Get(victimEID)
		vx, vy, vz := e.X, e.Y+float64(victimBB[1])*0.5, e.Z
		if !ok {
			return
		}
		s.broadcastParticles(e.Dim, particleDamageIndicator, vx, vy, vz, 0.1, 0, 0.1, 0.2, int32(dealt*0.5))
	}
}

// doSweepAttack is vanilla's sweep: the sweep sound first, then every
// living entity inside the victim's bounding box inflated by (1, 0.25,
// 1) and within 3 blocks of the attacker takes scale damage and a 0.4
// knockback along the attacker's facing; the sweep particle lands one
// block ahead of the attacker at half bounding height.
func (s *Server) doSweepAttack(sess *Session, victimEID int32, plan attackPlan, victimBB [2]float64) {
	s.broadcastAttackSound(sess, sndAttackSweep)
	ax, ay, az, yaw, _, _ := sess.Pos()
	v, ok := s.entities.Get(victimEID)
	vx, vy, vz := ax, ay, az
	if ok {
		vx, vy, vz = v.X, v.Y, v.Z
	} else if target := s.sessionByEntityID(victimEID); target != nil {
		vx, vy, vz, _, _, _ = target.Pos()
	}
	hw := float64(victimBB[0])/2 + 1
	dmg := plan.scale // sweeping_damage_ratio is 0: 1 * scale
	kx, kz := knockbackYaw(yaw)
	for _, e := range s.entities.All() {
		if !e.Living || e.ID == victimEID || e.ID == sess.entityID.Load() {
			continue
		}
		nb := mobBounds(e.Type)
		if math.Abs(e.X-vx) > hw || math.Abs(e.Z-vz) > hw ||
			e.Y+float64(nb[1]) < vy-0.25 || e.Y > vy+float64(victimBB[1])+0.25 {
			continue
		}
		if (ax-e.X)*(ax-e.X)+(ay-e.Y)*(ay-e.Y)+(az-e.Z)*(az-e.Z) >= 9 {
			continue
		}
		res := float64(0)
		if name, ok2 := EntityTypeName(e.Type); ok2 {
			res = mobKnockbackResistance[name]
		}
		if dealt, graced, died := s.damageMob(&e, dmg); dealt > 0 {
			if !graced {
				s.broadcastDamageEvent(e.ID, damageInfo{
					typeName: "player_attack",
					cause:    sess.entityID.Load(),
					direct:   sess.entityID.Load(),
				})
				// The hit applies the default 0.4 knockback away from
				// the attacker inside hurt, then the sweep's own 0.4
				// along the facing — each step halves the stored delta.
				mx, my, mz := knockDelta(e.MotionX, e.MotionY, e.MotionZ, 0.4, res, ax-e.X, az-e.Z, e.Grounded)
				mx, my, mz = knockDelta(mx, my, mz, 0.4, res, kx, kz, e.Grounded)
				s.entities.SetMotion(e.ID, mx, my, mz)
				s.broadcastEntityMotion(e.Dim, e.ID, mx, my, mz)
			}
			if died {
				s.onPlayerKilledMob(sess, e)
			}
		}
	}
	// Same box test over player victims (LivingEntity covers them).
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other == sess || other.state.Load() != StatePlay ||
			other.mode() == GameModeCreative || other.mode() == GameModeSpectator || other.dead {
			return true
		}
		if other.entityID.Load() == victimEID {
			return true
		}
		ox, oy, oz, _, _, og := other.Pos()
		if math.Abs(ox-vx) > hw || math.Abs(oz-vz) > hw || oy < vy-0.25 || oy-1.8 > vy+0.25 {
			return true
		}
		if (ax-ox)*(ax-ox)+(ay-oy)*(ay-oy)+(az-oz)*(az-oz) >= 9 {
			return true
		}
		dealt := s.damageWith(other, dmg, translatable("death.attack.player", literal(other.getName()), literal(sess.getName())), damageSource{
			typeName: "player_attack",
			cause:    sess.entityID.Load(),
			direct:   sess.entityID.Load(),
		})
		if dealt > 0 {
			mx, my, mz := knockDelta(0, 0, 0, 0.4, 0, ax-ox, az-oz, og)
			mx, my, mz = knockDelta(mx, my, mz, 0.4, 0, kx, kz, og)
			s.sendEntityMotion(other, mx, my, mz)
		}
		return true
	})
	rad := float64(yaw) * math.Pi / 180
	px := ax - math.Sin(rad)
	py := ay + 1.8*0.5 // getY(0.5): half the player's bounding height
	pz := az + math.Cos(rad)
	s.broadcastParticles(sess.dimName(), particleSweepAttack, px, py, pz, 0, 0, 0, 0, 1)
}

// hurtYaw is the yaw of the damage direction (from the victim toward
// the attacker), matching the Hurt Animation convention.
func hurtYaw(tx, tz, ax, az float64) float32 {
	dx, dz := ax-tx, az-tz
	if dx == 0 && dz == 0 {
		return 0
	}
	// Minecraft yaw: 0 faces +Z, increasing toward -X.
	return float32(math.Atan2(-dx, dz) * 180 / math.Pi)
}

func (s *Server) sendHurtAnimation(sess *Session, yaw float32) {
	var b protocol.Buffer
	b.VarInt(sess.entityID.Load())
	b.Float(yaw)
	_ = sess.write(sess.ids().PlayHurtAnimation, b.Bytes())
}

// Player Input flag bits: 0x20 sneak; vanilla ignores the sprint bit
// here (sprint is player_command-driven).
const (
	inputSneak = 0x20
)

// Living-entity metadata index for the pose, and the pose values we
// broadcast. The index is verified against the real server by
// TestSneakPoseParity.
const (
	metaPoseIndex = 6
	metaPoseType  = 20
	poseStanding  = 0
	poseSneaking  = 5
)

// handlePlayerInput tracks the client's movement input flags and
// broadcasts pose metadata when sneaking starts or stops.
func (s *Server) handlePlayerInput(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	flags, err := r.Byte()
	if err != nil {
		return
	}
	sneak := flags&inputSneak != 0
	jump := flags&0x10 != 0
	// Vanilla reads only the shift flag here; sprinting stays
	// player_command-driven.
	if jump && !sess.jumping {
		_, _, _, _, _, onGround := sess.Pos()
		if onGround && !sess.flying {
			sess.addCustomStat("jump", 1)
		}
		if s.GameMode == GameModeSurvival {
			sess.addExhaustion(0.05)
		}
	}
	sess.jumping = jump
	if sneak && sess.riding.Load() != 0 {
		// Player.tick: wantsToStopRiding (shift key down) -> stopRiding.
		s.dismount(sess)
	}
	if sneak == sess.sneaking {
		return
	}
	sess.sneaking = sneak
	pose := int32(poseStanding)
	if sneak {
		pose = poseSneaking
	}
	s.broadcastPose(sess, pose)
	s.broadcastSharedFlags(sess)
}

// broadcastPose sends the player's pose metadata to every play-stage
// session in their dimension (including the subject, like vanilla).
func (s *Server) broadcastPose(sess *Session, pose int32) {
	eid := sess.entityID.Load()
	if eid == 0 {
		return
	}
	dim := sess.dimName()
	var b protocol.Buffer
	b.VarInt(eid)
	b.Byte(metaPoseIndex)
	b.Byte(metaPoseType)
	b.VarInt(pose)
	b.Byte(0xFF)
	body := b.Bytes()
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other.state.Load() != StatePlay || !other.spawned.Load() {
			return true
		}
		if other.dimName() != dim {
			return true
		}
		_ = other.write(other.ids().PlayEntityMetadata, body)
		return true
	})
}

// handlePlayerAbilities applies the client's ability flag update
// (flight toggle) and echoes the server-authoritative flags, exactly
// like vanilla. Modes without flight cannot start flying.
func (s *Server) handlePlayerAbilities(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	flags, err := r.Byte()
	if err != nil {
		return
	}
	flying := flags&abilityFlagFlying != 0
	if flying && sess.mode() != GameModeCreative && sess.mode() != GameModeSpectator {
		flying = false
	}
	if flying == sess.flying {
		return
	}
	sess.flying = flying
	_ = s.sendAbilities(sess)
}

// handleChunkBatchReceived applies the client's chunk-batch
// acknowledgement: the chunks-per-tick rate it asks for, which the
// streaming throttle spends on ten batches from the next tick on. The
// real server floors the request to a whole number of chunks and clamps
// it to [1, 64], and ignores acknowledgements that do not parse.
//
// The first of the ten batches goes out at once rather than on a tick, as
// it does in the real server, so a client that acknowledges every batch
// keeps its window streaming without waiting for a tick boundary.
func (s *Server) handleChunkBatchReceived(sess *Session, p protocol.Packet) {
	rate, err := protocol.NewPacketReader(p.Data).Float()
	if err != nil {
		return
	}
	sess.sentMu.Lock()
	sess.rate = chunkRate(rate)
	sess.interval = chunkInterval(rate)
	sess.burst = burstBatches
	sess.cool = 0
	sess.sentMu.Unlock()
	_ = s.pumpChunks(sess)
}

// clientInfo is the subset of Client Information the server keeps.
type clientInfo struct {
	Locale       string
	ViewDistance byte
	ChatColors   bool
	SkinParts    byte
	MainHand     int32
}

// parseClientInfo decodes a Client Information body.
func parseClientInfo(data []byte) (clientInfo, bool) {
	r := protocol.NewPacketReader(data)
	locale, err := r.String()
	if err != nil || len(locale) > 64 {
		return clientInfo{}, false
	}
	view, err := r.Byte()
	if err != nil || view > 32 {
		return clientInfo{}, false
	}
	chatMode, err := r.VarInt()
	if err != nil || chatMode < 0 || chatMode > 2 {
		return clientInfo{}, false
	}
	chatColors, err := r.Bool()
	if err != nil {
		return clientInfo{}, false
	}
	skin, err := r.Byte()
	if err != nil {
		return clientInfo{}, false
	}
	main, err := r.VarInt()
	if err != nil {
		return clientInfo{}, false
	}
	// Trailing options (text filtering, server listings, particle
	// status) are parsed when a feature needs them.
	return clientInfo{
		Locale:       locale,
		ViewDistance: view,
		ChatColors:   chatColors,
		SkinParts:    skin,
		MainHand:     main,
	}, true
}

// handleClientInfo stores the client's settings packet.
//
// A view distance that changes mid-game widens or narrows the window the
// player is being sent, and the real server acts on it at once:
// measured on 26.2, a client that has announced nothing is sent the
// default window and is given the whole of a larger one the moment its
// Client Information arrives (329 columns for a view distance of 8,
// where the default window is 49), and asking for a smaller distance
// again takes the excess away.
func (s *Server) handleClientInfo(sess *Session, p protocol.Packet) {
	info, ok := parseClientInfo(p.Data)
	if !ok {
		return
	}
	before := sess.viewRadius(s.ViewDistance)
	sess.infoMu.Lock()
	sess.info = info
	sess.infoMu.Unlock()
	if sess.viewRadius(s.ViewDistance) == before || sess.state.Load() != StatePlay {
		return
	}
	x, _, z, _, _, _ := sess.Pos()
	_ = s.sendWindow(sess, int(x)>>4, int(z)>>4)
}

// viewRadius returns the effective streamed-window radius: the server's
// view distance capped by the client's requested distance.
//
// A client that never sent Client Information is read as asking for the
// smallest distance the protocol allows, which is what the real server
// falls back to: measured on 26.2, a scripted client that announces
// nothing is sent the same 49 columns as one that asks for a view
// distance of 2, and a client asking for 3 is sent 81 - so the fallback
// is 2 and not the smallest window mc-go used to send.
func (sess *Session) viewRadius(serverVD int) int {
	sess.infoMu.Lock()
	v := int(sess.info.ViewDistance)
	sess.infoMu.Unlock()
	if v < minViewDistance {
		v = minViewDistance
	}
	if serverVD > 0 && v > serverVD {
		v = serverVD
	}
	if v < 1 {
		v = 1
	}
	return v
}

// sprintKnockbackHit reports whether this swing is vanilla's sprint
// knockback hit (scale > 0.9 while sprinting); it must be evaluated
// before the cooldown ticker resets.
func sprintKnockbackHit(sess *Session, scale float32) bool {
	return sess.sprinting && scale > 0.9
}

// knockbackYaw returns the facing unit vector vanilla aims the sprint
// bonus along: (sin(yaw), -cos(yaw)) with yaw in degrees.
func knockbackYaw(yaw float32) (float64, float64) {
	r := float64(yaw) * math.Pi / 180
	return math.Sin(r), -math.Cos(r)
}

// mobAttackKnockback applies vanilla's two knockback steps to a living
// mob victim: the default 0.4 push away from the attacker from inside
// hurt, then the 0.5 sprint bonus along the attacker's facing. The
// composed delta goes to chunk trackers as set_entity_motion.
func (s *Server) mobAttackKnockback(sess *Session, eid int32, scale float32) {
	e, ok := s.entities.Get(eid)
	if !ok {
		return
	}
	res := float64(0)
	if name, ok := EntityTypeName(e.Type); ok {
		res = mobKnockbackResistance[name]
	}
	ax, _, az, yaw, _, _ := sess.Pos()
	vx, vy, vz := knockDelta(e.MotionX, e.MotionY, e.MotionZ, 0.4, res, ax-e.X, az-e.Z, e.Grounded)
	if sprintKnockbackHit(sess, scale) {
		kx, kz := knockbackYaw(yaw)
		vx, vy, vz = knockDelta(vx, vy, vz, 0.5, res, kx, kz, e.Grounded)
		s.endSprintKnockback(sess)
	}
	s.entities.SetMotion(eid, vx, vy, vz)
	s.broadcastEntityMotion(e.Dim, eid, vx, vy, vz)
}

// playerAttackKnockback sends the victim's post-hit velocity as
// set_entity_motion — vanilla restores the server-side delta after
// sending, so only the packet reflects the knockback. The default
// 0.4 push away from the attacker composes with the 0.5 sprint bonus
// along the attacker's facing when the swing was a strong sprint hit.
func (s *Server) playerAttackKnockback(sess, target *Session, scale float32) {
	ax, _, az, yaw, _, _ := sess.Pos()
	_, _, _, _, _, grounded := target.Pos()
	tx, _, tz, _, _, _ := target.Pos()
	vx, vy, vz := knockDelta(0, 0, 0, 0.4, 0, ax-tx, az-tz, grounded)
	if sprintKnockbackHit(sess, scale) {
		kx, kz := knockbackYaw(yaw)
		vx, vy, vz = knockDelta(vx, vy, vz, 0.5, 0, kx, kz, grounded)
		s.endSprintKnockback(sess)
	}
	s.sendEntityMotion(target, vx, vy, vz)
}

// endSprintKnockback performs vanilla's sprint-hit aftermath: the
// attacker's motion is damped horizontally and sprinting ends, which
// trackers see as a shared-flags entity data update.
func (s *Server) endSprintKnockback(sess *Session) {
	sess.sprinting = false
	s.broadcastSharedFlags(sess)
}

// broadcastSharedFlags sends the index-0 entity flags byte (sneak 0x02,
// sprint 0x08, etc.) to every play-state session in the dimension,
// including the subject like the other metadata broadcasts.
func (s *Server) broadcastSharedFlags(sess *Session) {
	eid := sess.entityID.Load()
	if eid == 0 {
		return
	}
	flags := byte(0)
	if sess.sneaking {
		flags |= 0x02
	}
	if sess.sprinting {
		flags |= 0x08
	}
	dim := sess.dimName()
	var b protocol.Buffer
	b.VarInt(eid)
	b.Byte(0) // index: shared flags
	b.Byte(0) // type: byte
	b.Byte(flags)
	b.Byte(0xFF)
	body := b.Bytes()
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other.state.Load() != StatePlay || !other.spawned.Load() {
			return true
		}
		if other.dimName() != dim {
			return true
		}
		_ = other.write(other.ids().PlayEntityMetadata, body)
		return true
	})
}

// broadcastEntityMotion sends set_entity_motion to every play-state
// session tracking the entity's dimension.
func (s *Server) broadcastEntityMotion(dim string, eid int32, vx, vy, vz float64) {
	body := encodeEntityMotion(eid, vx, vy, vz)
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other.state.Load() != StatePlay || !other.spawned.Load() {
			return true
		}
		if other.dimName() != dim {
			return true
		}
		_ = other.write(other.ids().PlaySetEntityMotion, body)
		return true
	})
}

// sendEntityMotion delivers set_entity_motion to one session.
func (s *Server) sendEntityMotion(sess *Session, vx, vy, vz float64) {
	_ = sess.write(sess.ids().PlaySetEntityMotion, encodeEntityMotion(sess.entityID.Load(), vx, vy, vz))
}

// encodeEntityMotion builds set_entity_motion: 26.2 ships the delta
// as an LpVec3 (VarInt id + packed low-precision vector).
func encodeEntityMotion(eid int32, vx, vy, vz float64) []byte {
	var b protocol.Buffer
	b.VarInt(eid)
	b.Raw(lpVec3(vx, vy, vz))
	return b.Bytes()
}

// heldIsSword reports whether the main-hand stack is in vanilla's
// SWORDS tag — the gate a sweep attack needs.
func (sess *Session) heldIsSword() bool {
	held := sess.heldStack()
	if held.Empty() {
		return false
	}
	name, ok := itemNameOf(held.ID)
	return ok && strings.HasSuffix(name, "_sword")
}

// particle type ids used by combat feedback (particle registry ids).
const (
	particleDamageIndicator int32 = 14
	particleSweepAttack     int32 = 74
)

// broadcastAttackSound plays one sound_event to every spawned session
// in the attacker's dimension, positioned at the attacker — vanilla's
// playServerSideSound. The holder field is the registry id + 1; the
// coordinates are fixed-point eighths; the seed seeds the client's
// event pitch pick.
func (s *Server) broadcastAttackSound(sess *Session, soundID int32) {
	x, y, z, _, _, _ := sess.Pos()
	var b protocol.Buffer
	b.VarInt(soundID + 1)
	b.VarInt(7) // SoundSource.PLAYERS
	b.Int(int32(x * 8))
	b.Int(int32(y * 8))
	b.Int(int32(z * 8))
	b.Float(1)
	b.Float(1)
	b.Long(0)
	body := b.Bytes()
	dim := sess.dimName()
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other.state.Load() != StatePlay || !other.spawned.Load() || other.dimName() != dim {
			return true
		}
		_ = other.write(other.ids().PlaySound, body)
		return true
	})
}

// broadcastParticles sends one level_particles packet (simple particle
// types: the id tail has no per-particle data).
func (s *Server) broadcastParticles(dim string, id int32, x, y, z, xd, yd, zd, speed float64, count int32) {
	var b protocol.Buffer
	b.Byte(0) // overrideLimiter
	b.Byte(0) // alwaysShow
	b.Double(x)
	b.Double(y)
	b.Double(z)
	b.Float(float32(xd))
	b.Float(float32(yd))
	b.Float(float32(zd))
	b.Float(float32(speed))
	b.Int(count)
	b.VarInt(id)
	body := b.Bytes()
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other.state.Load() != StatePlay || !other.spawned.Load() || other.dimName() != dim {
			return true
		}
		_ = other.write(other.ids().PlayLevelParticles, body)
		return true
	})
}
