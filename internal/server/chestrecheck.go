package server

import (
	"math"
	"strings"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// Interaction uses the effective attribute including the creative mode
// modifier; menu validity adds four blocks to that reach (Container).
func chestInteractionRange(sess *Session) float64 {
	base := 4.5
	mods := map[string]attrMod{}
	if entity, ok := sess.server.entities.Get(sess.entityID.Load()); ok {
		if value, ok := entity.AttrBase["block_interaction_range"]; ok {
			base = value
		}
		for name, modifier := range entity.AttrMods["block_interaction_range"] {
			mods[name] = modifier
		}
	}
	if sess.mode() == GameModeCreative {
		if _, ok := mods[attrModCreativeBlockRange]; !ok {
			mods[attrModCreativeBlockRange] = attrMod{value: 0.5}
		}
	}
	value := attrValue(base, mods)
	if math.IsNaN(value) {
		return 0
	}
	return min(64, max(0, value))
}

type chestPlayerBounds struct{ x, y, z, eyeY, half, height float64 }

func chestBoundsOf(sess *Session) chestPlayerBounds {
	x, y, z, _, _, _ := sess.Pos()
	pose := int32(0)
	scale := 1.0
	if entity, ok := sess.server.entities.Get(sess.entityID.Load()); ok {
		pose = entity.Pose
		if value, ok := entity.AttrBase["scale"]; ok {
			scale = value
		}
		scale = attrValue(scale, entity.AttrMods["scale"])
	}
	if math.IsNaN(scale) {
		scale = 0.0625
	}
	narrow := float32(min(16, max(0.0625, scale)))
	half, height := playerPoseDimsAtScale(pose, narrow)
	eyeScale := narrow
	if pose == 2 || pose == 7 {
		eyeScale = 1
	}
	return chestPlayerBounds{x, y, z, y + float64(float32(playerPoseEyeHeight(pose))*eyeScale), half, height}
}

func chestMenuInRange(sess *Session, pos world.Pos) bool {
	bounds := chestBoundsOf(sess)
	dx := bounds.x - min(max(bounds.x, float64(pos.X)), float64(pos.X+1))
	dy := bounds.eyeY - min(max(bounds.eyeY, float64(pos.Y)), float64(pos.Y+1))
	dz := bounds.z - min(max(bounds.z, float64(pos.Z)), float64(pos.Z+1))
	reach := chestInteractionRange(sess) + 4
	return dx*dx+dy*dy+dz*dz < reach*reach
}

func (s *Server) scheduleChestRecheck(d *dimension, pos world.Pos) {
	if s.ticks == nil {
		return
	}
	name, _ := world.StateOwner(d.w.GetBlock(pos))
	kind := tickChestRecheck
	if name == "trapped_chest" {
		kind = tickTrappedChestRecheck
	} else if name != "chest" {
		return
	}
	s.ticks.schedule(schedKey{dim: d.name, pos: pos, kind: kind}, s.TickCount()+5)
}

// Delta callbacks distinguish an actual first increment/last decrement.
// Recounts compare positive occupancy, including recovery from negatives.
func (s *Server) chestOpenerEffects(sess *Session, d *dimension, pos world.Pos, before, after int, recount bool) {
	block := d.w.GetBlock(pos)
	if !isChestBlock(block) {
		return
	}
	s.queueChestBlockEvent(d, pos, block, after)
	opens := before == 0 && after > 0
	closes := before > 0 && after == 0
	if recount {
		opens = before <= 0 && after > 0
		closes = before > 0 && after <= 0
	}
	event, sound := "", ""
	if opens {
		event = "container_open"
		sound = chestHingeSound(block, true)
	}
	if closes {
		event = "container_close"
		sound = chestHingeSound(block, false)
	}
	if event != "" {
		s.chestLidSound(d, pos, block, sound)
		s.emitGameEvent(d, event, float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5, sess, "")
	}
	if opens && !recount {
		s.scheduleChestRecheck(d, pos)
	}
	name, _ := world.StateOwner(block)
	if name == "trapped_chest" && before != after {
		s.rsNeighborFanOut(d, pos)
		s.rsNeighborFanOut(d, world.Pos{X: pos.X, Y: pos.Y - 1, Z: pos.Z})
	}
}

func (s *Server) chestRecheckTick(d *dimension, pos world.Pos, kind tickKind) {
	expected := "chest"
	if kind == tickTrappedChestRecheck {
		expected = "trapped_chest"
	}
	if !isBlockNamed(d.w.GetBlock(pos), expected) {
		return
	}
	d.chests.mu.Lock()
	storage := d.chests.chests[pos]
	if storage == nil {
		d.chests.mu.Unlock()
		return
	}
	before, rangeBefore := storage.openers, storage.maxOpenRange
	d.chests.mu.Unlock()
	count := 0
	maxRange := 0.0
	radius := rangeBefore + 4
	s.sessions.Range(func(_, value any) bool {
		sess := value.(*Session)
		if sess.dimension() != d || sess.mode() == GameModeSpectator {
			return true
		}
		sess.windowMu.Lock()
		w := sess.window
		owns := w != nil && w.dim == d && (w.chest == storage || w.chestOther == storage)
		sess.windowMu.Unlock()
		if !owns {
			return true
		}
		body := chestBoundsOf(sess)
		if body.x+body.half <= float64(pos.X)-radius || body.x-body.half >= float64(pos.X+1)+radius || body.y+body.height <= float64(pos.Y)-radius || body.y >= float64(pos.Y+1)+radius || body.z+body.half <= float64(pos.Z)-radius || body.z-body.half >= float64(pos.Z+1)+radius {
			return true
		}
		count++
		maxRange = max(maxRange, chestInteractionRange(sess))
		return true
	})
	d.chests.mu.Lock()
	storage.openers = count
	storage.maxOpenRange = maxRange
	d.chests.mu.Unlock()
	s.chestOpenerEffects(nil, d, pos, before, count, true)
	if count > 0 {
		s.scheduleChestRecheck(d, pos)
	}
}

// ServerPlayer validates its open menu on player ticks, also while frozen.
// Broadcast queued slot changes first, then retire invalid backing storage.
func (s *Server) validateChestMenu(sess *Session) {
	sess.windowMu.Lock()
	w := sess.window
	sess.windowMu.Unlock()
	if w == nil || w.chest == nil {
		return
	}
	valid := w.dim == sess.dimension()
	for slot := 0; valid && slot < w.containerSize(); slot += chestSlots {
		storage, pos, _ := w.chestSlot(slot)
		name, _ := world.StateOwner(w.dim.w.GetBlock(pos))
		w.dim.chests.mu.Lock()
		same := w.dim.chests.chests[pos] == storage
		w.dim.chests.mu.Unlock()
		valid = same && chestBackingNameCompatible(w.chestBlockName, name) && chestMenuInRange(sess, pos)
	}
	if valid {
		return
	}
	sess.windowMu.Lock()
	if sess.window != w {
		sess.windowMu.Unlock()
		return
	}
	sess.window = nil
	sess.windowMu.Unlock()
	sess.quickCraft = quickCraft{}
	var packet protocol.Buffer
	packet.VarInt(w.id)
	_ = sess.write(sess.ids().PlayContainerClose, packet.Bytes())
	s.menuRemoved(sess, w, false)
}

// Compatible copper tool states retain the same live chest entity. Ordinary
// replacement still fails the storage identity check above.
func chestBackingNameCompatible(initial, current string) bool {
	copper := func(name string) bool { return name == "copper_chest" || strings.HasSuffix(name, "_copper_chest") }
	return initial == current || copper(initial) && copper(current)
}
