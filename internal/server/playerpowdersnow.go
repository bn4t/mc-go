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

// Each admitted inside pass contributes one FREEZE point. Frame travel can
// cross a new powder row and add another contact step; the living freeze arm
// itself only thaws when contact is absent or the wearable guard rejects it.
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
		pe.TicksFrozen = min(140, pe.TicksFrozen+s.playerPowderTravelContacts(sess))
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

// playerPowderTravelContacts counts new vertical powder rows for the measured
// fixed-position travel envelope. Reuse cached body bounds and collision
// clipping; merely moving or lacking ground support does not imply a new
// FREEZE step. Client movement ingress and its transient step budget remain
// separate from this frame-end calculation.
func (s *Server) playerPowderTravelContacts(sess *Session) int32 {
	d := sess.dimension()
	if d == nil {
		return 0
	}
	g := sess.sessAttr("gravity", nil)
	if sessMobHasEff(sess, "slow_falling") && g > .01 {
		g = .01
	}
	velocity := -g
	if sessMobHasEff(sess, "levitation") {
		velocity = .05 * (float64(sess.effects[effectLevitation].amplifier) + 1) * .2
	}
	// Native travel stores float0.98 drag; powder contact supplies vertical1.5.
	requested := velocity * float64(float32(.98)) * 1.5
	if requested == 0 || math.IsNaN(requested) || math.IsInf(requested, 0) {
		return 0
	}
	x, y, z, _, _, _ := sess.Pos()
	half, height := s.playerCollisionDims(&sess.playerRecord)
	moved := s.clampBoxAxis(d.w, [2]float64{y, y + height}, requested, 1, x-half, x+half, z-half, z+half)
	// Entity movement does not relocate a tiny remainder of a substantial
	// collision. This is the existing movement-resolution tolerance, not a
	// support epsilon chosen for powder.
	if moved*moved <= 1e-7 && requested*requested-moved*moved >= 1e-7 {
		return 0
	}
	const inset = float64(float32(1e-5))
	low, high := int(math.Floor(y+inset)), int(math.Floor(y+height-inset))
	endLow, endHigh := int(math.Floor(y+moved+inset)), int(math.Floor(y+moved+height-inset))
	powderRow := func(by int) bool {
		for bx := int(math.Floor(x - half + inset)); bx <= int(math.Floor(x+half-inset)); bx++ {
			for bz := int(math.Floor(z - half + inset)); bz <= int(math.Floor(z+half-inset)); bz++ {
				name, _ := world.StateOwner(d.w.GetBlock(world.Pos{X: bx, Y: by, Z: bz}))
				if strings.TrimPrefix(name, "minecraft:") == "powder_snow" {
					return true
				}
			}
		}
		return false
	}
	var contacts int32
	for by := endLow; by < low; by++ {
		if powderRow(by) {
			contacts++
		}
	}
	for by := high + 1; by <= endHigh; by++ {
		if powderRow(by) {
			contacts++
		}
	}
	return contacts
}
