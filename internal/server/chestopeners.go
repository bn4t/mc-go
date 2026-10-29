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
	w.chestOpenCounted = true
	s.chestViewOpeners(sess, w, 1)
}

func (s *Server) chestViewClose(sess *Session, w *openWindow) {
	if !w.chestOpenCounted {
		return
	}
	w.chestOpenCounted = false
	s.chestViewOpeners(sess, w, -1)
}

func (s *Server) chestViewOpeners(sess *Session, w *openWindow, delta int) {
	type change struct {
		pos           world.Pos
		before, after int
	}
	var changes []change
	w.dim.chests.mu.Lock()
	first := w.pos
	if w.chestOther != nil {
		first = w.chestFirstPos
	}
	changes = append(changes, change{first, w.chest.openers, max(0, w.chest.openers+delta)})
	w.chest.openers = changes[0].after
	if w.chestOther != nil {
		next := max(0, w.chestOther.openers+delta)
		changes = append(changes, change{w.chestOtherPos, w.chestOther.openers, next})
		w.chestOther.openers = next
	}
	w.dim.chests.mu.Unlock()
	for _, update := range changes {
		block := w.dim.w.GetBlock(update.pos)
		if !isChestBlock(block) {
			continue
		}
		event, sound := "", ""
		if update.before == 0 && update.after > 0 {
			event = "container_open"
			sound = "block.chest.open"
		}
		if update.before > 0 && update.after == 0 {
			event = "container_close"
			sound = "block.chest.close"
		}
		if event != "" {
			s.chestLidSound(w.dim, update.pos, block, sound)
			s.emitGameEvent(w.dim, event, float64(update.pos.X)+0.5, float64(update.pos.Y)+0.5, float64(update.pos.Z)+0.5, sess, "")
		}
		name, _ := world.StateOwner(block)
		if name != "trapped_chest" {
			continue
		}
		s.rsNeighborFanOut(w.dim, update.pos)
		s.rsNeighborFanOut(w.dim, world.Pos{X: update.pos.X, Y: update.pos.Y - 1, Z: update.pos.Z})
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
