package server

import (
	"math"
	"strings"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
	"mc-go/internal/worldgen"
)

// End crystals, measured from the 26.2 jar and the wire:
//
//   - EndCrystal synched data: DATA_BEAM_TARGET (8, OPTIONAL_BLOCK_POS,
//     empty by default) and DATA_SHOW_BOTTOM (9, BOOLEAN, true by
//     default). The pairing bundle carries only non-default entries; a
//     summon or placement that changed them also leaves them dirty, so
//     the same pack goes out once more on the next tracker tick.
//   - Save data: beam_target (int array, only while set) and ShowBottom;
//     the box is 2x2 and the entity is fire immune.
//   - tick: inside a level with a dragon fight an air block at the
//     crystal's block position becomes BaseFireBlock.getState.
//   - hurtServer: refused for isInvulnerableToBase (invulnerable unless
//     a creative player) and for any damage the ender dragon caused;
//     otherwise the crystal is removed (remove_entities goes out at
//     once) and, unless the damage was an explosion, explodes at its
//     position with power 6, no fire, BLOCK interaction - so a crystal
//     caught in another crystal's blast vanishes without a chain. Either
//     way the fight hears onCrystalDestroyed.
//   - EndCrystalItem.useOn: the clicked block must be obsidian or
//     bedrock and the block above empty with no entity in the 1x2x1 box
//     over it; the crystal spawns at the above block's bottom centre
//     with show_bottom false, ENTITY_PLACE fires there, the fight tries
//     a respawn, and the stack shrinks.

// endCrystal is an end_crystal entity's own state.
type endCrystal struct {
	beam       *world.Pos
	showBottom bool
	// sentBeam/sentShow are the values the clients last received.
	sentBeam *world.Pos
	sentShow bool
}

func init() {
	if id, ok := EntityTypeID("end_crystal"); ok {
		// EntityType END_CRYSTAL: sized(2.0, 2.0).
		mobBoundsTable[id] = [2]float64{2, 2}
	}
}

func samePos(a, b *world.Pos) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// newEndCrystal builds a crystal entity at its constructor defaults.
func newEndCrystal(dim string, x, y, z float64) *Entity {
	return &Entity{
		Type: mustEntityType("end_crystal"),
		Dim:  dim,
		X:    x, Y: y, Z: z,
		crystal: &endCrystal{showBottom: true, sentShow: true},
	}
}

// encodeCrystalMetaEntries writes the crystal's entries: all
// non-default ones (dirty=false) or the ones changed since the last
// send (dirty=true).
func encodeCrystalMetaEntries(c *endCrystal, dirty bool) []byte {
	var b protocol.Buffer
	n := 0
	if (dirty && !samePos(c.beam, c.sentBeam)) || (!dirty && c.beam != nil) {
		b.Byte(8)
		b.Byte(11) // OPTIONAL_BLOCK_POS
		if c.beam == nil {
			b.Bool(false)
		} else {
			b.Bool(true)
			b.Position(int32(c.beam.X), int32(c.beam.Y), int32(c.beam.Z))
		}
		n++
	}
	if (dirty && c.showBottom != c.sentShow) || (!dirty && !c.showBottom) {
		b.Byte(9)
		b.Byte(8) // BOOLEAN
		b.Bool(c.showBottom)
		n++
	}
	if n == 0 {
		return nil
	}
	b.Byte(0xFF)
	return b.Bytes()
}

// encodeCrystalMeta is the pairing pack.
func encodeCrystalMeta(e *Entity) []byte {
	if e.crystal == nil {
		return nil
	}
	return encodeCrystalMetaEntries(e.crystal, false)
}

// crystalSync is the crystal's ServerEntity.sendDirtyEntityData.
func (s *Server) crystalSync(e *Entity) {
	c := e.crystal
	if meta := encodeCrystalMetaEntries(c, true); meta != nil {
		s.broadcastEntityMetadata(e.ID, meta)
	}
	if c.beam != nil {
		p := *c.beam
		c.sentBeam = &p
	} else {
		c.sentBeam = nil
	}
	c.sentShow = c.showBottom
}

// crystalSetBeam is EndCrystal.setBeamTarget.
func crystalSetBeam(e *Entity, p *world.Pos) {
	if e.crystal == nil {
		return
	}
	if p != nil {
		q := *p
		p = &q
	}
	e.crystal.beam = p
}

// spawnEndCrystal adds a crystal to the level (addFreshEntity) and pairs
// it with the players in range. A crystal whose data left the defaults
// also goes out dirty on the next tick.
func (s *Server) spawnEndCrystal(e *Entity) *Entity {
	e = s.entities.Spawn(e)
	e.Dormant = s.shouldPark(e.Dim)
	if !e.Dormant {
		s.broadcastEntitySpawn(nil, *e)
	}
	return e
}

// crystalSyncStep is the crystals' ServerEntity.sendChanges. Vanilla's
// chunk source ticks (and its trackers send) early in the level tick,
// before the dragon fight and the entities tick, so a change made by a
// packet or command goes out before the fight's own changes that tick.
func (s *Server) crystalSyncStep() {
	typ := mustEntityType("end_crystal")
	var crystals []*Entity
	s.entities.mu.RLock()
	for _, e := range s.entities.byID {
		if e.Type == typ && e.crystal != nil && !e.Dormant {
			crystals = append(crystals, e)
		}
	}
	s.entities.mu.RUnlock()
	for _, e := range crystals {
		s.crystalSync(e)
	}
}

// crystalTickStep runs EndCrystal.tick once per game tick: the fire
// under a crystal in a level with a dragon fight.
func (s *Server) crystalTickStep() {
	typ := mustEntityType("end_crystal")
	var crystals []*Entity
	s.entities.mu.RLock()
	for _, e := range s.entities.byID {
		if e.Type == typ && e.crystal != nil && !e.Dormant {
			crystals = append(crystals, e)
		}
	}
	s.entities.mu.RUnlock()
	for _, e := range crystals {
		d := s.dims[e.Dim]
		if d == nil {
			continue
		}
		if d.fight != nil {
			pos := floorPos(e)
			if d.w.GetBlock(pos) == world.Air {
				s.setNaturalBlock(d, pos, fireGetState(d, pos))
			}
		}
	}
}

// crystalHurt is EndCrystal.hurtServer. creative marks a creative
// player behind the damage, explosion an IS_EXPLOSION source, byDragon
// damage the ender dragon caused; attacker is the player credited.
func (s *Server) crystalHurt(e *Entity, attacker *Session, creative, explosion, byDragon bool) bool {
	if e.crystal == nil || e.Dying {
		return false
	}
	if e.Invulnerable && !creative {
		return false
	}
	if byDragon {
		return false
	}
	if _, ok := s.entities.GetPtr(e.ID); !ok {
		return false
	}
	// remove(KILLED): the tracker drops it at once.
	e.Dying = true
	s.entities.Remove(e.ID)
	s.broadcastEntityRemoveIn(e.Dim, nil, []int32{e.ID})
	if !explosion {
		s.explodePower(e, e.X, e.Y, e.Z, 6, true)
	}
	if d := s.dims[e.Dim]; d != nil && d.fight != nil {
		s.fightCrystalDestroyed(d, e, attacker)
	}
	return true
}

// explodeHurtEndCrystals is the crystal share of
// ServerExplosion.hurtEntities: every crystal within 2r of the centre
// takes explosion damage (at least 1), which removes it without a
// second blast.
func (s *Server) explodeHurtEndCrystals(src *Entity, x, y, z float64, radius float32) {
	typ := mustEntityType("end_crystal")
	doubleR := 2 * float64(radius)
	var hit []*Entity
	s.entities.mu.RLock()
	for _, e := range s.entities.byID {
		if e.Type != typ || e.crystal == nil || e.Dying || e.Dim != src.Dim || e.ID == src.ID {
			continue
		}
		if math.Sqrt((e.X-x)*(e.X-x)+(e.Y-y)*(e.Y-y)+(e.Z-z)*(e.Z-z))/doubleR > 1 {
			continue
		}
		hit = append(hit, e)
	}
	s.entities.mu.RUnlock()
	for _, e := range hit {
		s.crystalHurt(e, nil, false, true, false)
	}
}

// attackEndCrystal is Player.attack against a crystal: the swing's
// pre-sounds, the hurt (remove + blast), then the strong/weak/crit
// sound - the crystal is no LivingEntity, so no damage hearts.
func (s *Server) attackEndCrystal(sess *Session, e *Entity) {
	bb := entityBBDims(e)
	if !sess.withinAttackRange(e.X, e.Y, e.Z, bb) {
		return
	}
	plan := s.planAttack(sess)
	s.preAttackSound(sess, plan)
	sess.attackTicker = 0
	if plan.damage <= 0 {
		return
	}
	if !s.crystalHurt(e, sess, sess.mode() == GameModeCreative, false, false) {
		s.broadcastAttackSound(sess, sndAttackNodamage)
		return
	}
	plan.sweep = false
	s.attackAftermath(sess, plan, e.ID, 0, bb)
	s.attackWear(sess)
}

// useEndCrystal is EndCrystalItem.useOn.
func (s *Server) useEndCrystal(sess *Session, d *dimension, clicked world.Pos) bool {
	return s.useEndCrystalInHand(sess, d, clicked, 0)
}

func (s *Server) useEndCrystalInHand(sess *Session, d *dimension, clicked world.Pos, hand int32) bool {
	held, slot := sess.stackForHand(hand)
	if held.Empty() {
		return false
	}
	if n, ok := itemNameOf(held.ID); !ok || strings.TrimPrefix(n, "minecraft:") != "end_crystal" {
		return false
	}
	if n, _ := world.StateOwner(d.w.GetBlock(clicked)); n != "obsidian" && n != "bedrock" {
		return true
	}
	above := world.Pos{X: clicked.X, Y: clicked.Y + 1, Z: clicked.Z}
	if d.w.GetBlock(above) != world.Air {
		return true
	}
	x, y, z := float64(above.X), float64(above.Y), float64(above.Z)
	if s.anyEntityInBox(d.name, x, y, z, x+1, y+2, z+1) {
		return true
	}
	e := newEndCrystal(d.name, x+0.5, y, z+0.5)
	e.crystal.showBottom = false
	s.spawnEndCrystal(e)
	s.emitGameEvent(d, "entity_place", x, y, z, sess, "")
	if d.fight != nil {
		s.fightTryRespawn(d)
	}
	sess.addItemStat(statUsed, held.ID, 1)
	if sess.mode() != GameModeCreative {
		sess.consumeSlot(slot, 1)
	}
	return true
}

// anyEntityInBox is Level.getEntities(null, box) non-empty: any entity
// (players included) whose bounding box overlaps the open box.
func (s *Server) anyEntityInBox(dim string, x0, y0, z0, x1, y1, z1 float64) bool {
	overlaps := func(ex, ey, ez float64, bb [2]float64) bool {
		hw := bb[0] / 2
		return ex-hw < x1 && ex+hw > x0 && ey < y1 && ey+bb[1] > y0 && ez-hw < z1 && ez+hw > z0
	}
	found := false
	s.entities.mu.RLock()
	for _, e := range s.entities.byID {
		if e.Dim != dim || e.Dying || e.Dormant {
			continue
		}
		if overlaps(e.X, e.Y, e.Z, entityBBDims(e)) {
			found = true
			break
		}
	}
	s.entities.mu.RUnlock()
	if found {
		return true
	}
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || sess.dimName() != dim || sess.dead {
			return true
		}
		px, py, pz, _, _, _ := sess.Pos()
		if overlaps(px, py, pz, [2]float64{0.6, 1.8}) {
			found = true
			return false
		}
		return true
	})
	return found
}

// summonEndCrystal is /summon end_crystal: the constructor defaults
// plus the summon tag's Entity.load and readAdditionalSaveData keys.
func (s *Server) summonEndCrystal(d *dimension, x, y, z float64, tag *snbtTag) *Entity {
	e := newEndCrystal(d.name, x, y, z)
	if tag != nil {
		if m, ok := snbtToValue(tag).(map[string]any); ok {
			applyCrystalSave(e, m, true)
			crystalSummonTags(e, m)
		}
	}
	return s.spawnEndCrystal(e)
}

// applyCrystalSave reads the crystal keys of a save compound - the
// shared Entity.load ones mc-go models for crystals, then
// EndCrystal.readAdditionalSaveData (beam_target, ShowBottom default
// true). summon leaves rotation/uuid to the tag when present only.
func applyCrystalSave(e *Entity, m map[string]any, summon bool) {
	if e.crystal == nil {
		e.crystal = &endCrystal{showBottom: true, sentShow: true}
	}
	if v, ok := m["Invulnerable"]; ok || !summon {
		e.Invulnerable = nbtTruth(v)
	}
	if rot, ok := nbtVec2f(m["Rotation"]); ok {
		e.Yaw, e.Pitch = float32(math.Mod(float64(rot[0]), 360)), float32(math.Mod(float64(rot[1]), 360))
		// Entity.getYHeadRot is 0 for a non-living entity.
	}
	if u, ok := m["UUID"].([]int32); ok && len(u) == 4 {
		for i, v := range u {
			e.UUID[i*4] = byte(v >> 24)
			e.UUID[i*4+1] = byte(v >> 16)
			e.UUID[i*4+2] = byte(v >> 8)
			e.UUID[i*4+3] = byte(v)
		}
	}
	if v, ok := m["Silent"]; ok {
		e.Silent = nbtTruth(v)
	}
	if v, ok := m["NoGravity"]; ok {
		e.setNoGravity(nbtTruth(v))
	}
	e.crystal.beam = nil
	if p, ok := nbtIntVec3(m["beam_target"]); ok {
		e.crystal.beam = &p
	}
	e.crystal.showBottom = true
	if v, ok := m["ShowBottom"]; ok {
		e.crystal.showBottom = nbtTruth(v)
	}
}

// crystalSaveFields adds EndCrystal.addAdditionalSaveData's keys.
func crystalSaveFields(e *Entity, m map[string]any) {
	if e.crystal == nil {
		return
	}
	if b := e.crystal.beam; b != nil {
		m["beam_target"] = []int32{int32(b.X), int32(b.Y), int32(b.Z)}
	}
	m["ShowBottom"] = nbtBool(e.crystal.showBottom)
}

// restoreEndCrystal respawns a saved crystal compound.
func (s *Server) restoreEndCrystal(d *dimension, save map[string]any, pos [3]float64) {
	e := newEndCrystal(d.name, pos[0], pos[1], pos[2])
	applyCrystalSave(e, save, false)
	e.crystal.sentBeam, e.crystal.sentShow = e.crystal.beam, e.crystal.showBottom
	if ex, ok := save["Tags"].(nbt.List); ok && len(ex.Items) > 0 {
		e.ExtraNBT = map[string]any{"Tags": ex}
	}
	s.entities.Spawn(e)
}

// spawnGenEntities adds the entities a provided generated chunk carried
// (the end spikes' crystals) to the level.
func (s *Server) spawnGenEntities(d *dimension, ents []worldgen.GenEntity) {
	for _, g := range ents {
		if g.Type != "end_crystal" {
			continue
		}
		e := newEndCrystal(d.name, g.X, g.Y, g.Z)
		e.Yaw = g.Yaw
		e.Invulnerable = g.Invulnerable
		if g.BeamTarget != nil {
			p := world.Pos{X: int(g.BeamTarget[0]), Y: int(g.BeamTarget[1]), Z: int(g.BeamTarget[2])}
			e.crystal.beam = &p
			e.crystal.sentBeam = &p
		}
		s.spawnEndCrystal(e)
	}
}

// crystalKilled is EndCrystal.kill: onDestroyedBy with a generic
// source, then the removal.
func (s *Server) crystalKilled(e *Entity) {
	if e.Dying {
		return
	}
	e.Dying = true
	s.entities.Remove(e.ID)
	s.broadcastEntityRemoveIn(e.Dim, nil, []int32{e.ID})
	if d := s.dims[e.Dim]; d != nil && d.fight != nil {
		s.fightCrystalDestroyed(d, e, nil)
	}
}
