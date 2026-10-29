package server

import (
	"mc-go/internal/world"
	"strings"
)

// Water's candidate box and axolotl branch are separate from splash falloff.
// Measured with OpenJDK25.0.5 javap on26.2 AbstractThrownPotion/Axolotl.
func waterPotionBox(x, y, z, tx, ty, tz, width, height float64) bool {
	return tx+width/2 > x-4.125 && tx-width/2 < x+4.125 && ty+height > y-2 && ty < y+2.25 && tz+width/2 > z-4.125 && tz-width/2 < z+4.125
}
func (s *Server) waterPotionBlockImpact(e *Entity, bx, by, bz, axis int, hx, hy, hz float64) {
	if e.splashPotion != "water" || len(potionStackEffects(projectilePotionStack(e))) != 0 {
		return
	}
	d := s.dimensionByName(e.Dim)
	if d == nil {
		return
	}
	struck := world.Pos{X: bx, Y: by, Z: bz}
	outside := struck
	switch axis {
	case 0:
		outside.X += fireballHitDir(hx, bx)
	case 1:
		outside.Y += fireballHitDir(hy, by)
	case 2:
		outside.Z += fireballHitDir(hz, bz)
	}
	cells := []world.Pos{outside, struck, {X: outside.X, Y: outside.Y, Z: outside.Z - 1}, {X: outside.X + 1, Y: outside.Y, Z: outside.Z}, {X: outside.X, Y: outside.Y, Z: outside.Z + 1}, {X: outside.X - 1, Y: outside.Y, Z: outside.Z}}
	for _, pos := range cells {
		s.waterPotionDowse(e, d, pos)
	}
}
func (s *Server) waterPotionDowse(e *Entity, d *dimension, pos world.Pos) {
	state := d.w.GetBlock(pos)
	owner, ok := world.StateOwner(state)
	if !ok {
		return
	}
	name := strings.TrimPrefix(owner, "minecraft:")
	if blockTagHas("fire", name) {
		s.setNaturalBlock(d, pos, world.Air)
		s.emitGameEvent(d, "block_destroy", float64(pos.X)+.5, float64(pos.Y)+.5, float64(pos.Z)+.5, nil, name)
		return
	}
	if !propIs(state, "lit", "true") {
		return
	}
	unlit, ok := world.WithProperty(state, "lit", "false")
	if !ok {
		return
	}
	if blockTagHas("campfires", name) {
		s.broadcastLevelEvent(e.Dim, 1009, pos, 0)
		s.emitGameEvent(d, "block_change", float64(pos.X)+.5, float64(pos.Y)+.5, float64(pos.Z)+.5, s.sessionByEntityID(e.arrowShooter), "")
		s.setNaturalBlock(d, pos, unlit)
	} else if blockTagHas("candles", name) || blockTagHas("candle_cakes", name) {
		s.setNaturalBlock(d, pos, unlit)
		s.blockSoundAt(d, pos, "block.candle.extinguish", 1, 1)
		s.emitGameEvent(d, "block_change", float64(pos.X)+.5, float64(pos.Y)+.5, float64(pos.Z)+.5, nil, "")
	}
}
