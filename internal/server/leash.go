package server

import (
	"math"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// Leash model (26.2 Leashable + Entity.interact + LeadItem +
// LeashFenceKnotEntity):
//
//   - Any Mob that isn't an Enemy can be leashed (Mob.canBeLeashed).
//     Holders are entities: a player, a leash_knot, or another mob
//     for a leash chain.
//   - Lead on a mob (not sneaking, mob unleashed or fence-leashed):
//     drops any current leash, setLeashedTo(player), lead shrinks by
//     one, LEAD_TIED.
//   - Empty/other hand on a mob leashed to YOU: dropLeash (lead item
//     drops) — creative removeLeash skips the drop — LEAD_UNTIED.
//   - Shears on any entity: shearOffAllLeashConnections — its own
//     leash plus every mob leashed TO it drops; shears wear 1,
//     SHEARS_SNIP.
//   - Sneak+interact on a leashable mob: every leashable within 16
//     blocks whose holder is you re-leashes onto THAT mob (a leash
//     chain); LEAD_TIED.
//   - Lead on a fence (LeadItem.useOn → bindPlayerMobs): every
//     leashable within 16 of the fence leashed to you re-attaches to
//     a leash_knot spawned at the fence pos.
//   - Clicking a knot: your own leashables in range re-attach to the
//     knot; with none (and not sneaking) the knot's leashees attach
//     to you instead.
//   - tickLeash: dist > leashSnapDistance (12) → LEAD_BREAK +
//     leashTooFarBehaviour → dropLeash. dist > 6 elastic pull —
//     approximated here as a capped step toward the holder.
//   - setLeashedTo broadcasts ClientboundSetEntityLinkPacket
//     (sourceId, destId, -1 on unlink) to the dimension.

const (
	leashSnapDist    = 12.0 // leashSnapDistance — beyond it the leash breaks
	leashElasticDist = 6.0  // leashElasticDistance — beyond it the spring pulls
	leashAreaHalf    = 16.0 // leashableInArea: AABB.ofSize(c, 32, 32, 32)
)

// isLeashable answers Mob.canBeLeashed: every living mob that isn't
// an Enemy — mc-go's hostile voice flag marks the Enemy set.
func (s *Server) isLeashable(t *Entity) bool {
	return t.Living && !mobVoiceOf(t).hostile
}

// leashHolderEntity resolves the holder id to its entity record —
// players live in the same entity table as mobs.
func (s *Server) leashHolderEntity(e *Entity) (*Entity, bool) {
	if e.LeashHolder == 0 {
		return nil, false
	}
	h, ok := s.entities.GetPtr(e.LeashHolder)
	if !ok {
		return nil, false
	}
	return h, true
}

// leashSetTo is Leashable.setLeashedTo(E, holder, broadcast=true):
// stores the holder and emits set_entity_link to the dimension.
func (s *Server) leashSetTo(e *Entity, holderID int32) {
	e.LeashHolder = holderID
	s.broadcastEntityLink(e.Dim, e.ID, holderID)
}

// leashDrop is Leashable.dropLeash / removeLeash: clears the holder,
// optionally drops a lead item (entity_drops on, non-creative paths),
// and emits the unlink packet.
func (s *Server) leashDrop(e *Entity, dropItem bool) {
	if e.LeashHolder == 0 {
		return
	}
	oldHolder := e.LeashHolder
	e.LeashHolder = 0
	if dropItem {
		if lid, ok := world.ItemIDs["lead"]; ok {
			s.spawnItemDrop(e.Dim, world.Pos{X: int(math.Floor(e.X)), Y: int(math.Floor(e.Y)), Z: int(math.Floor(e.Z))}, ItemStack{ID: lid, Count: 1})
		}
	}
	s.broadcastEntityLink(e.Dim, e.ID, -1)
	// Leashable notifies the previous holder on detach — a leash_knot
	// with no remaining leashees discards itself.
	s.notifyLeasheeRemoved(oldHolder)
}

// broadcastEntityLink sends set_entity_link — sourceId then destId,
// both raw int32s; destId -1 is the unlink.
func (s *Server) broadcastEntityLink(dim string, sourceID, destID int32) {
	var b protocol.Buffer
	b.Int(sourceID)
	b.Int(destID)
	body := b.Bytes()
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other.state.Load() != StatePlay || !other.spawned.Load() || other.dimName() != dim {
			return true
		}
		// The link update is a tracker broadcast — only sessions
		// paired with the leashed entity receive it.
		if !other.tracksEntity(sourceID) {
			return true
		}
		_ = other.write(other.ids().PlaySetEntityLink, body)
		return true
	})
}

// leashablesInArea is Leashable.leashableInArea(level, center, pred):
// every Leashable entity whose position sits inside the 32-cube
// centred on (x,y,z).
func (s *Server) leashablesInArea(dim string, x, y, z float64, pred func(*Entity) bool) []*Entity {
	var out []*Entity
	for _, snap := range s.entities.All() {
		if snap.Dim != dim {
			continue
		}
		if math.Abs(snap.X-x) > leashAreaHalf || math.Abs(snap.Y-y) > leashAreaHalf || math.Abs(snap.Z-z) > leashAreaHalf {
			continue
		}
		e, ok := s.entities.GetPtr(snap.ID)
		if !ok || !s.isLeashable(e) {
			continue
		}
		if pred != nil && !pred(e) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// leashCanAttachTo is Leashable.canHaveALeashAttachedTo: not self,
// within leashSnapDistance, and leashable at all.
func (s *Server) leashCanAttachTo(e, holder *Entity) bool {
	if e.ID == holder.ID || !s.isLeashable(e) {
		return false
	}
	return s.leashDist(e, holder) <= leashSnapDist
}

// leashDist is Leashable.leashDistanceTo — plain centre distance.
func (s *Server) leashDist(e, holder *Entity) float64 {
	dx, dy, dz := e.X-holder.X, e.Y-holder.Y, e.Z-holder.Z
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}

// leashShiftTransfer is Entity.interact's sneak branch: right-clicking
// a leashable mob while sneaking re-leashes every leashable in range
// whose holder is the player onto the clicked mob.
func (s *Server) leashShiftTransfer(sess *Session, eid int32, sneaking bool) bool {
	if !sneaking {
		return false
	}
	e, ok := s.entities.GetPtr(eid)
	if !ok || !s.isLeashable(e) || e.Health <= 0 {
		return false
	}
	// Babies refuse the transfer target role entirely.
	if e.Baby {
		return false
	}
	myEid := sess.entityID.Load()
	var moved bool
	for _, l := range s.leashablesInArea(e.Dim, e.X, e.Y, e.Z, func(l *Entity) bool {
		return l.LeashHolder == myEid
	}) {
		if s.leashCanAttachTo(l, e) {
			s.leashSetTo(l, eid)
			moved = true
		}
	}
	if moved {
		// Entity.interact plays LEAD_TIED once on the interacted
		// entity after the transfer loop.
		s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, soundEventIDs["item.lead.tied"], srcNeutral, 1.0, 1.0, nil)
	}
	return moved
}

// shearOffLeashConnections is Entity.interact's shears branch: the
// clicked entity's own leash plus every leash held BY it drops; the
// shears wear one point and SHEARS_SNIP plays. Consumes the interact
// when any connection dropped.
func (s *Server) shearOffLeashConnections(sess *Session, eid int32, hand int32) bool {
	held, hslot := sess.stackForHand(hand)
	if held.Empty() {
		return false
	}
	if n, ok := itemNameOf(held.ID); !ok || n != "shears" {
		return false
	}
	e, ok := s.entities.GetPtr(eid)
	if !ok {
		return false
	}
	if !s.dropAllLeashConnections(e) {
		return false
	}
	s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, soundEventIDs["item.shears.snip"], srcNeutral, 1.0, 1.0, nil)
	s.wearSlot(sess, hslot, 1)
	return true
}

// dropAllLeashConnections is Entity.dropAllLeashConnections: the
// entity's own leash, then every leashable leashed TO it.
func (s *Server) dropAllLeashConnections(e *Entity) bool {
	any := false
	if e.LeashHolder != 0 {
		s.leashDrop(e, true)
		any = true
	}
	for _, l := range s.leashablesInArea(e.Dim, e.X, e.Y, e.Z, func(l *Entity) bool {
		return l.LeashHolder == e.ID
	}) {
		s.leashDrop(l, true)
		any = true
	}
	return any
}

// leashUnleashOrAttach is Entity.interact's plain branch after the
// shears checks: an entity leashed to this player unleashes (lead
// drops, or vanishes for creative); otherwise a held lead attaches.
func (s *Server) leashUnleashOrAttach(sess *Session, eid int32, hand int32) bool {
	e, ok := s.entities.GetPtr(eid)
	if !ok || e.Health <= 0 {
		return false
	}
	myEid := sess.entityID.Load()
	if tn, _ := EntityTypeName(e.Type); tn == "leash_knot" {
		return s.knotInteract(sess, e, hand)
	}
	if !s.isLeashable(e) {
		return false
	}
	if e.LeashHolder == myEid {
		// Creative removeLeash keeps the lead; survival dropLeash
		// drops it — vanilla picks on hasInfiniteMaterials.
		infinite := sess.mode() == GameModeCreative
		s.leashDrop(e, !infinite)
		s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, soundEventIDs["item.lead.untied"], srcNeutral, 1.0, 1.0, nil)
		return true
	}
	held, hslot := sess.stackForHand(hand)
	if held.Empty() {
		return false
	}
	if n, ok := itemNameOf(held.ID); !ok || n != "lead" {
		return false
	}
	// A mob already leashed to another player stays put; a knot or
	// mob holder releases first, then the lead re-attaches.
	if s.isPlayerEntity(e.LeashHolder) {
		return false
	}
	holder, _ := s.entities.GetPtr(myEid)
	if holder == nil {
		return false
	}
	if !s.leashCanAttachTo(e, holder) {
		return false
	}
	if e.LeashHolder != 0 {
		s.leashDrop(e, true)
	}
	s.leashSetTo(e, myEid)
	s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, soundEventIDs["item.lead.tied"], srcNeutral, 1.0, 1.0, nil)
	held.Count--
	sess.setStack(hslot, held)
	sess.invState.Add(1)
	_ = s.sendContainerSlot(sess, hslot, held)
	return true
}

// isPlayerEntity reports whether the id belongs to a live session —
// player entity ids share the same table, so the check is a session
// lookup, not a type check.
func (s *Server) isPlayerEntity(eid int32) bool {
	if eid == 0 {
		return false
	}
	found := false
	s.sessions.Range(func(_, v any) bool {
		if v.(*Session).entityID.Load() == eid {
			found = true
			return false
		}
		return true
	})
	return found
}

// knotInteract is LeashFenceKnotEntity.interact: your own leashables
// in range re-attach to the knot; with none — and not sneaking — the
// knot's leashees come back to you.
func (s *Server) knotInteract(sess *Session, knot *Entity, hand int32) bool {
	d := sess.dimension()
	myEid := sess.entityID.Load()
	moved := false
	for _, l := range s.leashablesInArea(d.name, knot.X, knot.Y, knot.Z, func(l *Entity) bool {
		return l.LeashHolder == myEid
	}) {
		if s.leashCanAttachTo(l, knot) {
			s.leashSetTo(l, knot.ID)
			moved = true
		}
	}
	if !moved && !sess.sneaking {
		for _, l := range s.leashablesInArea(d.name, knot.X, knot.Y, knot.Z, func(l *Entity) bool {
			return l.LeashHolder == knot.ID
		}) {
			holder, _ := s.entities.GetPtr(myEid)
			if holder != nil && s.leashCanAttachTo(l, holder) {
				s.leashSetTo(l, myEid)
				moved = true
			}
		}
	}
	if moved {
		// LeashFenceKnotEntity.interact plays LEAD_TIED once, on
		// the knot, after the re-attach loops.
		s.playSoundSrc(d.name, knot.X, knot.Y, knot.Z, soundEventIDs["item.lead.tied"], srcNeutral, 1.0, 1.0, nil)
	}
	return moved
}

// bindPlayerMobs is LeadItem.useOn's fence branch
// (LeadItem.bindPlayerMobs): every leashable within 16 of the fence
// leashed to the player re-attaches to a knot spawned at the fence.
func (s *Server) bindPlayerMobs(sess *Session, d *dimension, pos world.Pos) {
	cx, cy, cz := float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5
	myEid := sess.entityID.Load()
	mine := s.leashablesInArea(d.name, cx, cy, cz, func(l *Entity) bool {
		return l.LeashHolder == myEid
	})
	if len(mine) == 0 {
		return
	}
	knot := s.leashKnotAt(d, pos)
	created := false
	if knot == nil {
		knot = s.leashKnotCreate(d, pos)
		if knot == nil {
			return
		}
		created = true
	}
	moved := false
	for _, l := range mine {
		if s.leashCanAttachTo(l, knot) {
			s.leashSetTo(l, knot.ID)
			moved = true
		}
	}
	if moved {
		// LeadItem.bindPlayerMobs plays LEAD_TIED once at the fence.
		s.playSoundSrc(d.name, cx, cy, cz, soundEventIDs["item.lead.tied"], srcNeutral, 1.0, 1.0, nil)
	}
	if knot != nil && created && !moved {
		// bindPlayerMobs discards a fresh knot nobody attached to.
		s.entities.Remove(knot.ID)
		s.broadcastEntityRemoveIn(d.name, nil, []int32{knot.ID})
	}
}

// leashKnotAt is LeashFenceKnotEntity.getKnot — the knot entity
// already riding the fence pos, if any.
func (s *Server) leashKnotAt(d *dimension, pos world.Pos) *Entity {
	for _, snap := range s.entities.All() {
		if snap.Dim != d.name {
			continue
		}
		if n, _ := EntityTypeName(snap.Type); n != "leash_knot" {
			continue
		}
		if int(math.Floor(snap.X)) == pos.X && int(math.Floor(snap.Y)) == pos.Y && int(math.Floor(snap.Z)) == pos.Z {
			if e, ok := s.entities.GetPtr(snap.ID); ok {
				return e
			}
		}
	}
	return nil
}

// leashKnotCreate is LeashFenceKnotEntity.createKnot: the knot sits at
// the fence's exact block pos.
func (s *Server) leashKnotCreate(d *dimension, pos world.Pos) *Entity {
	et, ok := EntityTypeID("leash_knot")
	if !ok {
		return nil
	}
	knot := s.entities.Spawn(&Entity{
		Type:   et,
		Dim:    d.name,
		X:      float64(pos.X),
		Y:      float64(pos.Y),
		Z:      float64(pos.Z),
		Health: 1,
	})
	if knot == nil {
		return nil
	}
	s.broadcastEntitySpawnIn(d.name, nil, *knot)
	return knot
}

// knotTickStep is LeashFenceKnotEntity's tick: BlockAttachedEntity
// counts checkInterval to 100 and only then verifies survives() —
// the block at the knot's pos is still a fence. A knot nobody is
// leashed to discards itself eagerly via notifyLeasheeRemoved (see
// leashDrop), not on this pass.
func (s *Server) knotTickStep() {
	for _, snap := range s.entities.All() {
		if n, _ := EntityTypeName(snap.Type); n != "leash_knot" {
			continue
		}
		e, ok := s.entities.GetPtr(snap.ID)
		if !ok {
			continue
		}
		e.KnotCheckTicks++
		if e.KnotCheckTicks < 100 {
			continue
		}
		e.KnotCheckTicks = 0
		fence := false
		if d := s.dimensionByName(e.Dim); d != nil {
			pos := world.Pos{X: int(math.Floor(e.X)), Y: int(math.Floor(e.Y)), Z: int(math.Floor(e.Z))}
			if bn, _ := world.StateOwner(d.w.GetBlock(pos)); bn != "" {
				fence = blockTagHas("fences", bn)
			}
		}
		if !fence {
			// dropItem: a popped knot drops its lead like vanilla.
			if id, ok := world.ItemID("lead"); ok {
				s.spawnItemDrop(e.Dim, world.Pos{X: int(math.Floor(e.X)), Y: int(math.Floor(e.Y)), Z: int(math.Floor(e.Z))}, ItemStack{ID: id, Count: 1})
			}
			s.entities.Remove(e.ID)
			s.broadcastEntityRemoveIn(e.Dim, nil, []int32{e.ID})
		}
	}
}

// notifyLeasheeRemoved is LeashFenceKnotEntity.notifyLeasheeRemoved:
// called when a leashee detaches from a knot — a knot with no
// remaining leashees discards itself.
func (s *Server) notifyLeasheeRemoved(knotID int32) {
	knot, ok := s.entities.GetPtr(knotID)
	if !ok {
		return
	}
	if n, _ := EntityTypeName(knot.Type); n != "leash_knot" {
		return
	}
	for _, snap := range s.entities.All() {
		if snap.LeashHolder == knotID {
			return
		}
	}
	dim := knot.Dim
	s.entities.Remove(knotID)
	s.broadcastEntityRemoveIn(dim, nil, []int32{knotID})
}

// tickLeash is Leashable.tickLeash for one mob: holder liveness first
// (a gone holder drops the leash — lead item included), then the snap
// distance, then a capped elastic pull toward the holder.
func (s *Server) tickLeash(e *Entity) {
	if e.LeashHolder == 0 {
		return
	}
	holder, ok := s.leashHolderEntity(e)
	if !ok {
		s.leashDrop(e, true)
		return
	}
	if holder.Dim != e.Dim {
		return // same-level gate — physics only
	}
	dist := s.leashDist(e, holder)
	if dist > leashSnapDist {
		// Leashable.tickLeash: LEAD_BREAK plays at the HOLDER position.
		s.playSoundSrc(e.Dim, holder.X, holder.Y, holder.Z, soundEventIDs["item.lead.break"], srcNeutral, 1.0, 1.0, nil)
		s.leashDrop(e, true)
		return
	}
	if dist > leashElasticDist {
		// Elastic pull approximation: step toward the holder, capped
		// so the mob never crosses inside the elastic radius.
		step := (dist - leashElasticDist) * 0.4
		if step > 0.9 {
			step = 0.9
		}
		e.X += (holder.X - e.X) / dist * step
		e.Y += (holder.Y - e.Y) / dist * step
		e.Z += (holder.Z - e.Z) / dist * step
	}
}
