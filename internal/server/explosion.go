package server

import (
	"math"
	"math/rand"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// windChargeTickStep moves wind_charge entities each tick: no gravity
// (AbstractHurtingProjectile), explode on block or player contact.
func (s *Server) windChargeTickStep() {
	wc := mustEntityType("wind_charge")
	bwc := mustEntityType("breeze_wind_charge")
	s.entities.mu.RLock()
	var charges []*Entity
	for _, e := range s.entities.byID {
		if (e.Type == wc || e.Type == bwc) && !e.Dying {
			charges = append(charges, e)
		}
	}
	s.entities.mu.RUnlock()
	for _, e := range charges {
		nx, ny, nz := e.X+e.MotionX, e.Y+e.MotionY, e.Z+e.MotionZ
		w := s.dimWorld(e.Dim)
		if w != nil && e.MotionY < 0 {
			if b := w.GetBlock(world.Pos{X: int(math.Floor(nx)), Y: int(math.Floor(ny)), Z: int(math.Floor(nz))}); b != 0 && !world.NonSolid(b) {
				s.windBurst(e, nx, ny, nz)
				continue
			}
		}
		if s.windChargeHitsPlayer(e, nx, ny, nz) {
			s.windBurst(e, nx, ny, nz)
			continue
		}
		s.entities.Update(e.ID, nx, ny, nz, e.Yaw, e.Pitch, false)
	}
}

// windChargeHitsPlayer reports whether the charge's next position
// intersects a player body (vanilla's entity-target ray hits the 0.6
// x 1.8 box).
func (s *Server) windChargeHitsPlayer(e *Entity, x, y, z float64) bool {
	hit := false
	s.sessions.Range(func(_, v any) bool {
		sess, ok := v.(*Session)
		if !ok || sess.state.Load() != StatePlay || sess.dimName() != e.Dim {
			return true
		}
		px, py, pz, _, _, _ := sess.Pos()
		if math.Abs(x-px) < 0.9 && math.Abs(z-pz) < 0.9 && y > py-0.1 && y < py+1.9 {
			hit = true
			return false
		}
		return true
	})
	return hit
}

// explodeProfile bundles the per-charge-type explode constants: blast
// radius, the SimpleExplosionDamageCalculator knockback multiplier
// (1.22 for the player's wind_charge, empty -> 1.0 for the breeze's),
// and the burst sound id.
func explodeProfile(src *Entity) (radius, kbMult float64, soundID int32) {
	if src.Type == mustEntityType("breeze_wind_charge") {
		return 3.0, 1.0, 210 // entity.breeze.wind_burst
	}
	return 1.2, 1.22, 1772 // entity.wind_charge.wind_burst
}

// windBurst explodes a wind charge at the contact point, mirroring
// ServerLevel.explode: the block sampler counts every position the
// 16x16x16 shell rays step through while power lasts (air included —
// no resistance drain — solids shorten their ray), entities inside the
// 2*radius box get pushed along eyePos->center at
// (1-dist)*seen*mult*(1-kbResist), and the explode packet goes to every
// player within 64 blocks carrying the victim's knockback inline
// (spectators and flying creatives get no field). The charge's removal
// follows on the wire, matching discard-after-explode.
func (s *Server) windBurst(src *Entity, x, y, z float64) {
	radius, kbMult, soundID := explodeProfile(src)
	// isSmall: radius < 2.0 picks the small emitter particle; the
	// breeze's 3.0 blast gets gust_emitter_large.
	particleID := int32(34) // gust_emitter_small
	if radius >= 2.0 {
		particleID = 33 // gust_emitter_large
	}
	blockCount := s.explodeBlockCount(src.Dim, x, y, z, float32(radius))

	// hurtEntities: push every living entity in the 2r box along
	// eyePos->center. Flat open world makes getSeenPercent always 1;
	// damagesEntities=false means only knockback applies.
	doubleR := 2 * radius
	hitPlayers := map[int32][3]float64{}
	s.entities.mu.RLock()
	var mobs []*Entity
	for _, e := range s.entities.byID {
		if e.Living && !e.Dying && e.Dim == src.Dim && e.ID != src.ID {
			mobs = append(mobs, e)
		}
	}
	s.entities.mu.RUnlock()
	push := func(eid int32, ex, ey, ez, eyeY float64, kbResist float64) ([3]float64, bool) {
		dist := math.Sqrt((ex-x)*(ex-x)+(ey-y)*(ey-y)+(ez-z)*(ez-z)) / doubleR
		if dist > 1 {
			return [3]float64{}, false
		}
		dx, dy, dz := ex-x, eyeY-y, ez-z
		n := math.Sqrt(dx*dx + dy*dy + dz*dz)
		if n == 0 {
			dy = 1
			n = 1
		}
		power := (1 - dist) * kbMult * (1 - kbResist)
		vx, vy, vz := dx/n*power, dy/n*power, dz/n*power
		s.entities.SetMotion(eid, vx, vy, vz)
		return [3]float64{vx, vy, vz}, true
	}
	for _, e := range mobs {
		// Mob eye height is the vanilla LivingEntity default: 0.85 of
		// the bounding-box height.
		eyeY := e.Y + mobBounds(e.Type)[1]*0.85
		kb, ok := push(e.ID, e.X, e.Y, e.Z, eyeY, mobExplosionKBResist(e.Type))
		if !ok {
			continue
		}
		body := encodeEntityMotion(e.ID, kb[0], kb[1], kb[2])
		s.sessions.Range(func(_, v any) bool {
			sess := v.(*Session)
			if sess.state.Load() != StatePlay || sess.dimName() != e.Dim {
				return true
			}
			_ = sess.write(sess.ids().PlaySetEntityMotion, body)
			return true
		})
	}
	s.sessions.Range(func(_, v any) bool {
		sess, ok := v.(*Session)
		if !ok || sess.state.Load() != StatePlay || sess.dimName() != src.Dim {
			return true
		}
		px, py, pz, _, _, _ := sess.Pos()
		kb, ok := push(sess.entityID.Load(), px, py, pz, py+1.62, 0)
		if !ok {
			return true
		}
		// onExplosionHit latches impact+cause for fall_after_explosion;
		// hitPlayers (the explode packet's knockback field) skips
		// spectators and creative players in flight.
		sess.explodeImpact = &[3]float64{px, py, pz}
		sess.explodeCause = src.Type
		if sess.mode() != GameModeSpectator && !(sess.mode() == GameModeCreative && sess.flying) {
			hitPlayers[sess.entityID.Load()] = kb
		}
		return true
	})

	// One explode packet per player within 64 blocks; the victim's own
	// packet carries the knockback vector, everyone else's omits it.
	s.sessions.Range(func(_, v any) bool {
		sess, ok := v.(*Session)
		if !ok || sess.state.Load() != StatePlay || sess.dimName() != src.Dim {
			return true
		}
		px, py, pz, _, _, _ := sess.Pos()
		if (px-x)*(px-x)+(py-y)*(py-y)+(pz-z)*(pz-z) >= 4096 {
			return true
		}
		var b protocol.Buffer
		b.Double(x)
		b.Double(y)
		b.Double(z)
		b.Float(float32(radius))
		b.Int(int32(blockCount))
		if kb, ok := hitPlayers[sess.entityID.Load()]; ok {
			b.Bool(true)
			b.Double(kb[0])
			b.Double(kb[1])
			b.Double(kb[2])
		} else {
			b.Bool(false)
		}
		b.VarInt(particleID)
		b.VarInt(soundID + 1) // sound holder ids are one-based
		b.VarInt(0)           // empty block-particles weighted list
		_ = sess.write(sess.ids().PlayExplode, b.Bytes())
		return true
	})
	// A pushed player's changed delta goes out to observers as
	// set_entity_motion on the tracker pass — after the explode packet.
	for eid, kb := range hitPlayers {
		body := encodeEntityMotion(eid, kb[0], kb[1], kb[2])
		s.sessions.Range(func(_, v any) bool {
			sess := v.(*Session)
			if sess.state.Load() != StatePlay || sess.dimName() != src.Dim || sess.entityID.Load() == eid {
				return true
			}
			_ = sess.write(sess.ids().PlaySetEntityMotion, body)
			return true
		})
	}

	s.entities.Remove(src.ID)
	s.broadcastEntityRemoveIn(src.Dim, nil, []int32{src.ID})
}

// explodeBlockCount runs the vanilla blast sampler: 16x16x16 rays cast
// from the cube shell, each carrying power radius*(0.7+rand*0.6) that
// drains 0.225 per 0.3 step plus (resistance+0.3)*0.3 when the stepped
// block resists. Every in-bounds position stepped while power lasts
// joins the set — wind charges explode every block, air included.
func (s *Server) explodeBlockCount(dim string, cx, cy, cz float64, radius float32) int {
	w := s.dimWorld(dim)
	if w == nil {
		return 0
	}
	set := map[world.Pos]struct{}{}
	for xx := 0; xx < 16; xx++ {
		for yy := 0; yy < 16; yy++ {
		ray:
			for zz := 0; zz < 16; zz++ {
				if xx != 0 && xx != 15 && yy != 0 && yy != 15 && zz != 0 && zz != 15 {
					continue
				}
				xd := float64(xx)/15*2 - 1
				yd := float64(yy)/15*2 - 1
				zd := float64(zz)/15*2 - 1
				d := math.Sqrt(xd*xd + yd*yd + zd*zd)
				xd, yd, zd = xd/d, yd/d, zd/d
				xp, yp, zp := cx, cy, cz
				for power := float64(radius) * (0.7 + rand.Float64()*0.6); power > 0; power -= 0.22500001 {
					pos := world.Pos{X: int(math.Floor(xp)), Y: int(math.Floor(yp)), Z: int(math.Floor(zp))}
					if pos.Y < -64 || pos.Y > 319 {
						continue ray
					}
					if res, ok := explosionResistance(w.GetBlock(pos)); ok {
						power -= float64((res + 0.3) * 0.3)
					}
					if power > 0 {
						set[pos] = struct{}{}
					}
					xp += xd * 0.3
					yp += yd * 0.3
					zp += zd * 0.3
				}
			}
		}
	}
	return len(set)
}

// explosionResistance returns the block explosion resistance fed to
// the blast sampler, keyed by state owner. Air and unknown blocks
// report no resistance (empty Optional in vanilla), immune-tag blocks
// (blocks_wind_charge_explosions) report the 3600000 wall.
func explosionResistance(state world.Block) (float32, bool) {
	owner, ok := world.StateOwner(state)
	if !ok {
		return 0, false
	}
	res, ok := explodeResists[owner]
	return res, ok
}

var explodeResists = map[string]float32{
	"barrier": 3600000, "bedrock": 3600000, "obsidian": 1200,
	"water": 100, "lava": 100,
	"stone": 6, "deepslate": 6, "cobblestone": 6, "iron_block": 6,
	"iron_bars": 6, "obsidian_side": 6,
	"oak_planks": 3, "oak_log": 2, "oak_leaves": 0.2,
	"grass_block": 0.6, "gravel": 0.6, "clay": 0.6, "sandstone": 0.8,
	"dirt": 0.5, "coarse_dirt": 0.5, "rooted_dirt": 0.5, "dirt_path": 0.5,
	"farmland": 0.6, "podzol": 0.5, "mycelium": 0.5, "mud": 0.5,
	"sand": 0.5, "red_sand": 0.5, "ice": 0.5,
	"glass": 0.3, "snow_block": 0.2, "snow": 0.1,
}

// mobExplosionKBResist is the explosion_knockback_resistance attribute
// base per mob type; every vanilla mob defaults to 0 except the iron
// golem at 1.0.
func mobExplosionKBResist(typ int32) float64 {
	if name, ok := EntityTypeName(typ); ok && name == "iron_golem" {
		return 1.0
	}
	return 0
}
