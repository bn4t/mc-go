package server

import (
	"math"
	"strings"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

func playerCanFreeze(sess *Session) bool {
	if sess.mode() == GameModeSpectator {
		return false
	}
	immune := func(stack ItemStack) bool {
		if stack.Empty() {
			return false
		}
		name, _ := world.ItemName(stack.ID)
		return itemTagHas("freeze_immune_wearables", name)
	}
	for slot := int16(slotArmorStart); slot < slotArmorStart+4; slot++ {
		if immune(sess.stackAt(slot)) {
			return false
		}
	}
	return !immune(sess.bodyArmor)
}

// Each admitted inside pass contributes one point. The frame-end contribution
// completes the measured stationary total of two per player frame, both with
// a frozen world and with ordinary world stepping.
func (s *Server) playerPowderInside(sess *Session, d *dimension, x, y, z float64) {
	pe, ok := s.entities.GetPtr(sess.entityID.Load())
	if !ok {
		return
	}
	halfWidth, height := s.playerCollisionDims(&sess.playerRecord)
	inside := false
	// Native PowderSnowBlock contact melts before extinguishing. Players
	// bypass mob_griefing; ordinary world interaction permission still applies.
	burning := sess.fireTicks > 0
	// Native inside-block contact deflates by the float1e-5 value.
	const inset = float64(float32(1e-5))
	for bx := int(math.Floor(x - halfWidth + inset)); bx <= int(math.Floor(x+halfWidth-inset)); bx++ {
		for by := int(math.Floor(y + inset)); by <= int(math.Floor(y+height-inset)); by++ {
			for bz := int(math.Floor(z - halfWidth + inset)); bz <= int(math.Floor(z+halfWidth-inset)); bz++ {
				pos := world.Pos{X: bx, Y: by, Z: bz}
				name, _ := world.StateOwner(d.w.GetBlock(pos))
				if strings.TrimPrefix(name, "minecraft:") == "powder_snow" {
					inside = true
					if burning && s.mayInteract(sess, d, pos) {
						s.setNaturalBlock(d, pos, world.Air)
					}
				}
			}
		}
	}
	if inside && burning {
		sess.fireTicks = 0
		s.broadcastSharedFlags(sess)
	}
	pe.IsInPowderSnow = inside
	if inside && playerCanFreeze(sess) {
		pe.TicksFrozen = min(140, pe.TicksFrozen+1)
	}
}

func (s *Server) playerPowderTick(sess *Session, pe *Entity) {
	canFreeze := playerCanFreeze(sess)
	if pe.IsInPowderSnow && canFreeze {
		pe.TicksFrozen = min(140, pe.TicksFrozen+1)
	} else {
		pe.TicksFrozen = max(0, pe.TicksFrozen-2)
	}
	if pe.metaSent.frozen != pe.TicksFrozen {
		var b protocol.Buffer
		b.Byte(7)
		b.VarInt(1)
		b.VarInt(pe.TicksFrozen)
		b.Byte(0xff)
		s.broadcastEntityMetadata(pe.ID, b.Bytes())
		pe.metaSent.frozen = pe.TicksFrozen
	}
}
