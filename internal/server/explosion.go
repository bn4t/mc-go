package server

import (
	"math"
	"math/rand"
	"strings"

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
		if w != nil {
			bp := world.Pos{X: int(math.Floor(nx)), Y: int(math.Floor(ny)), Z: int(math.Floor(nz))}
			if b := w.GetBlock(bp); b != 0 && !world.NonSolid(b) {
				cx, cy, cz := windBurstPoint(e, bp, nx, ny, nz)
				s.windBurst(e, cx, cy, cz)
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

// windBurstPoint is vanilla's onHitBlock burst centre:
// hitResult.getLocation() + faceNormal*0.25 — the contacted face of
// the hit block (chosen by the dominant motion axis), pushed a quarter
// block out along the normal.
func windBurstPoint(e *Entity, bp world.Pos, nx, ny, nz float64) (float64, float64, float64) {
	ax, ay, az := math.Abs(e.MotionX), math.Abs(e.MotionY), math.Abs(e.MotionZ)
	switch {
	case ax >= ay && ax >= az:
		if e.MotionX > 0 {
			return float64(bp.X) - 0.25, ny, nz
		}
		return float64(bp.X+1) + 0.25, ny, nz
	case az >= ay:
		if e.MotionZ > 0 {
			return nx, ny, float64(bp.Z) - 0.25
		}
		return nx, ny, float64(bp.Z+1) + 0.25
	default:
		if e.MotionY > 0 {
			return nx, float64(bp.Y) - 0.25, nz
		}
		return nx, float64(bp.Y+1) + 0.25, nz
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
	positions := s.explodeBlockPositions(src.Dim, x, y, z, float32(radius))
	blockCount := len(positions)

	// hurtEntities: push every living entity in the 2r box along
	// eyePos->center. Flat open world makes getSeenPercent always 1;
	// damagesEntities=false means only knockback applies. entity.push
	// only sets the delta — the motion packet rides the next tracker
	// pass, so these are queued here and sent after the explode packet.
	doubleR := 2 * radius
	hitPlayers := map[int32][3]float64{}
	var pushedMobs []struct {
		eid int32
		kb  [3]float64
	}
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
		pushedMobs = append(pushedMobs, struct {
			eid int32
			kb  [3]float64
		}{e.ID, kb})
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

	// interactWithBlocks: the block flips run inside explode(), ahead of
	// the packets ServerLevel sends after it returns.
	s.explodeTriggerBlocks(src, positions)

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
	// The tracker pass: every pushed entity's changed delta goes out as
	// set_entity_motion — mobs to all viewers, players to everyone but
	// the victim (their knockback already rode inside the explode).
	sendMotion := func(eid int32, kb [3]float64, skipEID int32) {
		body := encodeEntityMotion(eid, kb[0], kb[1], kb[2])
		s.sessions.Range(func(_, v any) bool {
			sess := v.(*Session)
			if sess.state.Load() != StatePlay || sess.dimName() != src.Dim || sess.entityID.Load() == skipEID {
				return true
			}
			_ = sess.write(sess.ids().PlaySetEntityMotion, body)
			return true
		})
	}
	for _, pm := range pushedMobs {
		sendMotion(pm.eid, pm.kb, -1)
	}
	for eid, kb := range hitPlayers {
		sendMotion(eid, kb, eid)
	}

	s.entities.Remove(src.ID)
	s.broadcastEntityRemoveIn(src.Dim, nil, []int32{src.ID})
}

// explodeBlockPositions runs the vanilla blast sampler: 16x16x16 rays
// cast from the cube shell, each carrying power radius*(0.7+rand*0.6)
// that drains 0.225 per 0.3 step plus (resistance+0.3)*0.3 when the
// stepped block resists. Every in-bounds position stepped while power
// lasts joins the set — wind charges explode every block, air included.
// The explode packet's blockCount is the set's size; the same set feeds
// interactWithBlocks.
func (s *Server) explodeBlockPositions(dim string, cx, cy, cz float64, radius float32) []world.Pos {
	return s.explodeBlockPositionsRes(dim, cx, cy, cz, radius, func(w *world.World, pos world.Pos) (float32, bool) {
		return explosionResistance(w.GetBlock(pos))
	})
}

// explodeBlockPositionsRes is the sampler with the caller's
// ExplosionDamageCalculator resistance lookup — wind charges pass the
// barrier/bedrock-only calculator, TNT the real per-block table.
func (s *Server) explodeBlockPositionsRes(dim string, cx, cy, cz float64, radius float32, resFn func(*world.World, world.Pos) (float32, bool)) []world.Pos {
	w := s.dimWorld(dim)
	if w == nil {
		return nil
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
					if res, ok := resFn(w, pos); ok {
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
	positions := make([]world.Pos, 0, len(set))
	for pos := range set {
		positions = append(positions, pos)
	}
	return positions
}

// explodeTriggerBlocks runs vanilla's interactWithBlocks for a
// TRIGGER_BLOCK explosion (wind charges): the sampled positions are
// shuffled and each block's onExplosionHit fires — doors, trapdoors
// and fence gates toggle open when unpowered, buttons press and
// schedule their release, levers flip, lit candles extinguish. Breeze
// wind charges are gated on mobGriefing; the redstone neighbourhood
// update a manual flip produces is not simulated.
func (s *Server) explodeTriggerBlocks(src *Entity, positions []world.Pos) {
	if src.Type == mustEntityType("breeze_wind_charge") && !s.gameRuleBool("minecraft:mob_griefing") {
		return
	}
	d := s.dims[src.Dim]
	if d == nil || s.ticks == nil {
		return
	}
	// Util.shuffle: order across distinct blocks is unspecified.
	for i := len(positions) - 1; i > 0; i-- {
		j := rand.Intn(i + 1)
		positions[i], positions[j] = positions[j], positions[i]
	}
	for _, pos := range positions {
		blk := d.w.GetBlock(pos)
		name, ok := world.StateOwner(blk)
		if !ok {
			continue
		}
		s.explodeTriggerBlock(d, pos, strings.TrimPrefix(name, "minecraft:"), blk)
	}
}

// explodeTriggerBlock is one position's onExplosionHit dispatch.
func (s *Server) explodeTriggerBlock(d *dimension, pos world.Pos, name string, blk world.Block) {
	prop := func(p string) string {
		v, _ := world.StateProperty(blk, p)
		return v
	}
	switch {
	case strings.HasSuffix(name, "_door"):
		fam := windSoundFamily(name)
		// canOpenByWindCharge: every door but the iron one.
		if fam == "iron" || prop("powered") == "true" || prop("half") != "lower" {
			return
		}
		s.explodeToggleOpen(d, pos, blk, fam+"_door")
	case strings.HasSuffix(name, "_trapdoor"):
		fam := windSoundFamily(name)
		if fam == "iron" || prop("powered") == "true" {
			return
		}
		s.explodeToggleOpen(d, pos, blk, fam+"_trapdoor")
	case strings.HasSuffix(name, "_fence_gate"):
		if prop("powered") == "true" {
			return
		}
		s.explodeToggleOpen(d, pos, blk, windGateSoundFam(name))
	case strings.HasSuffix(name, "_button"):
		if prop("powered") == "true" {
			return
		}
		s.rsButtonPress(d, pos, name, blk)
	case name == "lever":
		s.rsLeverPull(d, pos, blk)
	case name == "candle" || strings.HasSuffix(name, "_candle"):
		if prop("lit") != "true" {
			return
		}
		nb, ok := world.WithProperty(blk, "lit", "false")
		if !ok {
			return
		}
		d.w.SetBlock(pos, nb)
		s.broadcastBlockState(d, pos, nb)
		s.broadcastParticles(d.name, 69, float64(pos.X)+0.5, float64(pos.Y)+0.6, float64(pos.Z)+0.5, 0, 0.1, 0, 0, 1)
		s.explodeBlockSound(d, pos, "candle.extinguish", 1, 1)
	}
}

// explodeToggleOpen flips a door/trapdoor/gate's open flag and plays
// the wood-family open/close sound at vanilla's random pitch band.
func (s *Server) explodeToggleOpen(d *dimension, pos world.Pos, blk world.Block, soundBase string) {
	open, _ := world.StateProperty(blk, "open")
	nb, ok := world.WithProperty(blk, "open", boolStr(open != "true"))
	if !ok {
		return
	}
	d.w.SetBlock(pos, nb)
	s.broadcastBlockState(d, pos, nb)
	if strings.HasSuffix(soundBase, "_door") {
		s.explodeDoorMirror(d, pos, nb)
	}
	sfx := ".open"
	if open == "true" {
		sfx = ".close"
	}
	s.explodeBlockSound(d, pos, soundBase+sfx, 1, float32(rand.Float64()*0.1+0.9))
}

// explodeDoorMirror writes a door's other half so it mirrors this
// half's new state with the half flag flipped — the sync vanilla gets
// from DoorBlock.updateShape propagating the changed half's state.
func (s *Server) explodeDoorMirror(d *dimension, pos world.Pos, nb world.Block) {
	half, _ := world.StateProperty(nb, "half")
	other := pos
	var otherHalf string
	if half == "lower" {
		other = world.Pos{X: pos.X, Y: pos.Y + 1, Z: pos.Z}
		otherHalf = "upper"
	} else {
		other = world.Pos{X: pos.X, Y: pos.Y - 1, Z: pos.Z}
		otherHalf = "lower"
	}
	ob := d.w.GetBlock(other)
	owner, ok := world.StateOwner(ob)
	if !ok || !strings.HasSuffix(strings.TrimPrefix(owner, "minecraft:"), "_door") {
		return
	}
	if oh, _ := world.StateProperty(ob, "half"); oh != otherHalf {
		return
	}
	mir, ok := world.WithProperty(nb, "half", otherHalf)
	if !ok || mir == ob {
		return
	}
	d.w.SetBlock(other, mir)
	s.broadcastBlockState(d, other, mir)
}

// explodeBlockSound broadcasts a positioned block sound (BLOCKS source)
// at the block's centre; vanilla's playSound reach is vol*16 blocks.
func (s *Server) explodeBlockSound(d *dimension, pos world.Pos, name string, vol, pitch float32) {
	soundID, ok := SoundEventID("block." + name)
	if !ok {
		return
	}
	x, y, z := float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5
	reach := float64(vol) * 16
	s.sessions.Range(func(_, v any) bool {
		sess, ok := v.(*Session)
		if !ok || sess.state.Load() != StatePlay || !sess.spawned.Load() || sess.dimName() != d.name {
			return true
		}
		px, py, pz, _, _, _ := sess.Pos()
		dx, dy, dz := px-x, py-y, pz-z
		if dx*dx+dy*dy+dz*dz > reach*reach {
			return true
		}
		var b protocol.Buffer
		b.VarInt(soundID + 1)
		b.VarInt(4) // SoundSource.BLOCKS
		b.Int(int32(x * 8))
		b.Int(int32(y * 8))
		b.Int(int32(z * 8))
		b.Float(vol)
		b.Float(pitch)
		b.Long(0)
		_ = sess.write(sess.ids().PlaySound, b.Bytes())
		return true
	})
}

func boolStr(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// windGateSoundFam is windSoundFamily for fence gates: the gate
// sounds drop the "wooden" prefix — oak's family is just
// "fence_gate", while cherry/bamboo/nether keep theirs.
func windGateSoundFam(name string) string {
	if fam := windSoundFamily(name); fam != "wooden" {
		return fam + "_fence_gate"
	}
	return "fence_gate"
}

// windSoundFamily maps a toggleable block's name to the sound-family
// prefix BlockSetType picks for it: wooden for the plain woods,
// nether_wood for crimson/warped, cherry_wood, bamboo_wood, copper for
// the copper variants (waxed included), stone for stone and
// polished_blackstone buttons, iron for the wind-charge-proof door and
// trapdoor.
func windSoundFamily(name string) string {
	switch strings.SplitN(name, "_", 2)[0] {
	case "iron":
		return "iron"
	case "copper", "exposed", "weathered", "oxidized", "waxed":
		return "copper"
	case "crimson", "warped":
		return "nether_wood"
	case "cherry":
		return "cherry_wood"
	case "bamboo":
		return "bamboo_wood"
	case "stone", "polished":
		return "stone"
	default:
		return "wooden"
	}
}

// explosionResistance feeds the blast sampler's per-step drain. Wind
// charges use SimpleExplosionDamageCalculator with the
// blocks_wind_charge_explosions immune tag ({barrier, bedrock}):
// immune blocks report the 3600000 wall, EVERY other block — obsidian
// and water included — returns the empty Optional and drains nothing.
func explosionResistance(state world.Block) (float32, bool) {
	owner, ok := world.StateOwner(state)
	if !ok {
		return 0, false
	}
	switch strings.TrimPrefix(owner, "minecraft:") {
	case "barrier", "bedrock":
		return 3600000, true
	}
	return 0, false
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

// --- powered-neighbour propagation -----------------------------------
//
// When a button or lever toggles it fires updateNeighbours: every
// neighbour of the control and every neighbour of the block it is
// attached to runs neighborChanged. Doors, trapdoors and fence gates
// answer by syncing their powered (and, when it differs, open) flag to
// the signal they sense — the mechanism vanilla wind-charge bursts lean
// on when a pressed button ends up reclosing a flipped door.

// explodeNeighborOffsets lists the six face directions.
var explodeNeighborOffsets = [6][3]int{
	{1, 0, 0}, {-1, 0, 0}, {0, 1, 0}, {0, -1, 0}, {0, 0, 1}, {0, 0, -1},
}

// explodeUpdateNeighboursAttached mirrors the shared updateNeighbours
// of FaceAttachedHorizontalDirectionalBlock (buttons, levers): every
// neighbour of pos runs neighborChanged, then every neighbour of the
// attached block — the block below for face=floor, above for ceiling,
// behind for wall — runs it too.
func (s *Server) explodeUpdateNeighboursAttached(d *dimension, pos world.Pos, blk world.Block) {
	for _, o := range explodeNeighborOffsets {
		s.rsNeighborChanged(d, world.Pos{X: pos.X + o[0], Y: pos.Y + o[1], Z: pos.Z + o[2]})
	}
	attached := world.Pos{X: pos.X, Y: pos.Y - 1, Z: pos.Z}
	switch face, _ := world.StateProperty(blk, "face"); face {
	case "ceiling":
		attached.Y = pos.Y + 1
	case "wall":
		// facing points the way the control faces; the mount sits
		// behind it.
		attached.Y = pos.Y
		switch f, _ := world.StateProperty(blk, "facing"); f {
		case "north":
			attached.Z = pos.Z + 1
		case "south":
			attached.Z = pos.Z - 1
		case "west":
			attached.X = pos.X + 1
		case "east":
			attached.X = pos.X - 1
		}
	}
	for _, o := range explodeNeighborOffsets {
		s.rsNeighborChanged(d, world.Pos{X: attached.X + o[0], Y: attached.Y + o[1], Z: attached.Z + o[2]})
	}
}

// explodeNeighborChanged is the powered/open sync vanilla's
// neighborChanged drives on doors, trapdoors and fence gates: when the
// sensed signal differs from the powered flag, powered is written (and
// open is forced to match, playing the open/close sound when it flips).
// Doors additionally sense through their other half.
func (s *Server) explodeNeighborChanged(d *dimension, pos world.Pos) {
	blk := d.w.GetBlock(pos)
	owner, ok := world.StateOwner(blk)
	if !ok {
		return
	}
	name := strings.TrimPrefix(owner, "minecraft:")
	var signal bool
	var base string
	switch {
	case strings.HasSuffix(name, "_door"):
		half, _ := world.StateProperty(blk, "half")
		other := pos
		if half == "lower" {
			other = world.Pos{X: pos.X, Y: pos.Y + 1, Z: pos.Z}
		} else if half == "upper" {
			other = world.Pos{X: pos.X, Y: pos.Y - 1, Z: pos.Z}
		}
		signal = rsHasNeighborSignal(d, pos) || rsHasNeighborSignal(d, other)
		base = windSoundFamily(name) + "_door"
	case strings.HasSuffix(name, "_trapdoor"):
		signal = rsHasNeighborSignal(d, pos)
		base = windSoundFamily(name) + "_trapdoor"
	case strings.HasSuffix(name, "_fence_gate"):
		signal = rsHasNeighborSignal(d, pos)
		base = windGateSoundFam(name)
	default:
		return
	}
	if cur, _ := world.StateProperty(blk, "powered"); cur == boolStr(signal) {
		return
	}
	nb := blk
	if cur, _ := world.StateProperty(blk, "open"); cur != boolStr(signal) {
		if nb2, ok := world.WithProperty(nb, "open", boolStr(signal)); ok {
			nb = nb2
		}
		sfx := ".close"
		if signal {
			sfx = ".open"
		}
		s.explodeBlockSound(d, pos, base+sfx, 1, float32(rand.Float64()*0.1+0.9))
	}
	if nb2, ok := world.WithProperty(nb, "powered", boolStr(signal)); ok {
		d.w.SetBlock(pos, nb2)
		s.broadcastBlockState(d, pos, nb2)
		if strings.HasSuffix(name, "_door") {
			s.explodeDoorMirror(d, pos, nb2)
		}
	}
}
