package server

import (
	"math/rand"
	"mc-go/internal/world"
)

func trappedChestPower(d *dimension, pos world.Pos) int {
	d.chests.mu.Lock()
	defer d.chests.mu.Unlock()
	if chest := d.chests.chests[pos]; chest != nil {
		return min(15, max(0, chest.openers))
	}
	return 0
}

func (s *Server) chestViewOpen(sess *Session, w *openWindow) {
	if w.chest == nil || sess.mode() == GameModeSpectator {
		return
	}
	s.chestViewOpeners(sess, w, 1)
}

func (s *Server) chestViewClose(sess *Session, w *openWindow) {
	if w.chest == nil || w.chestCloseDone {
		return
	}
	w.chestCloseDone = true
	if sess.mode() != GameModeSpectator {
		s.chestViewOpeners(sess, w, -1)
	}
}

func (s *Server) chestViewOpeners(sess *Session, w *openWindow, delta int) {
	type change struct {
		pos           world.Pos
		before, after int
	}
	var changes []change
	rangeNow := chestInteractionRange(sess)
	w.dim.chests.mu.Lock()
	first := w.pos
	if w.chestOther != nil {
		first = w.chestFirstPos
	}
	apply := func(storage *chestData, pos world.Pos) {
		// Removed backing entities ignore stopOpen even when another chest
		// of the same type has already replaced them at this position.
		if w.dim.chests.chests[pos] != storage {
			return
		}
		before := storage.openers
		storage.openers += delta
		if delta > 0 {
			storage.maxOpenRange = max(storage.maxOpenRange, rangeNow)
		}
		changes = append(changes, change{pos, before, storage.openers})
	}
	apply(w.chest, first)
	if w.chestOther != nil {
		apply(w.chestOther, w.chestOtherPos)
	}
	w.dim.chests.mu.Unlock()
	for _, update := range changes {
		s.chestOpenerEffects(sess, w.dim, update.pos, update.before, update.after, false)
	}
}

// ChestBlockEntity emits one sound from the midpoint of a pair; LEFT
// halves stay silent. This also works for a view opened before pairing.
func (s *Server) chestLidSound(d *dimension, pos world.Pos, block world.Block, name string) {
	typ, _ := world.StateProperty(block, "type")
	if typ == "left" {
		return
	}
	x, y, z := float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5
	if typ == "right" {
		facing, _ := world.StateProperty(block, "facing")
		direction := rsDirOfName[chestConnDir(typ, facing)]
		step := rsOff[direction]
		x += float64(step.X) * 0.5
		z += float64(step.Z) * 0.5
	}
	if id, ok := soundEventIDs[name]; ok {
		s.playSoundSrc(d.name, x, y, z, id, srcBlocks, 0.5, 0.9+rand.Float32()*0.1, nil)
	}
}
