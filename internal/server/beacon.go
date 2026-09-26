package server

// Beacon activation, measured on vanilla 26.2: the beacon block entity
// rescans its pyramid base every 80 game ticks once the beam can see
// the sky, counts complete (2i+1)-square layers of beacon_base_blocks
// under it (1..4), and on a 0→active transition plays
// block.beacon.activate and fires construct_beacon(level) for every
// player inside the grant box — the block pos boxed x/z ±10,
// y-9…y+5. Losing the base drops levels to 0 and plays
// block.beacon.deactivate; while active block.beacon.ambient replays on
// the same 80-tick cadence.

import (
	"math"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// beaconBaseBlocks mirrors data/minecraft/tags/block/beacon_base_blocks.json.
var beaconBaseBlocks = map[string]bool{
	"iron_block":      true,
	"gold_block":      true,
	"diamond_block":   true,
	"emerald_block":   true,
	"netherite_block": true,
}

// Beacon sound_event registry ids (vanilla 26.2).
const (
	sndBeaconActivate   int32 = 152
	sndBeaconAmbient    int32 = 153
	sndBeaconDeactivate int32 = 154
)

// beaconEnt is the per-position activation state mc-go tracks for a
// placed beacon — the vanilla block entity's `levels` field plus the
// chosen powers (primaryPower/secondaryPower, "" when unset).
type beaconEnt struct {
	levels    int
	primary   string
	secondary string
}

// beaconEffectTiers is vanilla BEACON_EFFECTS: the pyramid tier
// (1..4) that unlocks each power.
var beaconEffectTiers = [][]string{
	{"speed", "haste"},
	{"resistance", "jump_boost"},
	{"strength"},
	{"regeneration"},
}

// beaconRequiredLevels is BeaconBlockEntity.getRequiredLevelsFor: the
// 1-based tier an effect needs, 0 for an empty selection, and an
// unreachable level for an effect the beacon cannot supply.
func beaconRequiredLevels(name string) int {
	if name == "" {
		return 0
	}
	for i, tier := range beaconEffectTiers {
		for _, e := range tier {
			if e == name {
				return i + 1
			}
		}
	}
	return math.MaxInt32
}

// validateBeaconEffects mirrors BeaconBlockEntity.validateEffects, the
// gate BeaconMenu.updateEffects runs before committing a selection:
// any secondary needs a maxed pyramid, each effect's tier must fit the
// level, the primary can never be a tier-4 power, and a tier-1..3
// secondary must boost the primary by matching it.
func validateBeaconEffects(primary, secondary string, levels int) bool {
	if secondary != "" && levels < 4 {
		return false
	}
	reqP, reqS := beaconRequiredLevels(primary), beaconRequiredLevels(secondary)
	if reqP > levels || reqS > levels {
		return false
	}
	if reqP >= 4 {
		return false
	}
	if reqS != 0 && reqS < 4 && primary != secondary {
		return false
	}
	return true
}

// syncBeacon keeps the dimension's beacon set in step with a block
// write, sharing the syncBlockEntities call path.
func (s *Server) syncBeacon(d *dimension, pos world.Pos, blk world.Block) {
	name, _ := world.StateOwner(blk)
	if name == "beacon" {
		if d.beacons == nil {
			d.beacons = map[world.Pos]*beaconEnt{}
		}
		if _, ok := d.beacons[pos]; !ok {
			d.beacons[pos] = &beaconEnt{}
		}
		return
	}
	delete(d.beacons, pos)
}

// beaconTickStep is the per-tick beacon pass. Vanilla gates the base
// rescan on gameTime%80 == 0; we keep the same global cadence.
func (s *Server) beaconTickStep() {
	if s.clock.count%80 != 0 {
		return
	}
	for _, d := range s.dims {
		for pos, be := range d.beacons {
			s.beaconRescan(d, pos, be)
		}
	}
}

// beaconRescan recomputes one beacon's level and applies the transition
// effects: activate sound + grants on 0→n, deactivate sound on n→0, the
// ambient loop while active.
func (s *Server) beaconRescan(d *dimension, pos world.Pos, be *beaconEnt) {
	levels := 0
	if s.beaconSeesSky(d, pos) {
		levels = s.beaconBaseDepth(d, pos)
	}
	wasActive := be.levels > 0
	be.levels = levels
	active := levels > 0
	switch {
	case !wasActive && active:
		s.playSoundIn(d.name, pos, sndBeaconActivate)
		s.fireConstructBeaconNear(d, pos, levels)
	case wasActive && !active:
		s.playSoundIn(d.name, pos, sndBeaconDeactivate)
	}
	if active {
		s.playSoundIn(d.name, pos, sndBeaconAmbient)
		s.beaconApplyEffects(d, pos, be)
	}
	s.beaconPropsChanged(d, pos)
}

// beaconApplyEffects is BeaconBlockEntity.applyEffects on the 80-tick
// rescan cadence: every player inside the power box gets the chosen
// primary re-applied, and at level 4 a distinct secondary alongside it.
// The box is the block pos inflated by levels*10+10 then stretched to
// the top of the world, so a player anywhere above the beacon but not
// deeper than the base is in range. Duration is (9+levels*2)*20 ticks —
// long enough to bridge the 80-tick gap — and the amplifier climbs to 1
// only when the secondary slot boosts the same effect on a maxed
// pyramid.
func (s *Server) beaconApplyEffects(d *dimension, pos world.Pos, be *beaconEnt) {
	if be.primary == "" {
		return
	}
	pid, ok := MobEffectID(be.primary)
	if !ok {
		return
	}
	duration := int32((9 + be.levels*2) * 20)
	dist := float64(be.levels*10 + 10)
	amp := int32(0)
	if be.levels >= 4 && be.secondary == be.primary {
		amp = 1
	}
	sid, hasSec := int32(0), false
	if be.levels >= 4 && be.secondary != "" && be.secondary != be.primary {
		if id, ok := MobEffectID(be.secondary); ok {
			sid, hasSec = id, true
		}
	}
	xMin, xMax := float64(pos.X)-dist, float64(pos.X)+1+dist
	zMin, zMax := float64(pos.Z)-dist, float64(pos.Z)+1+dist
	yMin, yMax := float64(pos.Y)-dist, float64(pos.Y)+1+dist+float64(d.height)
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || !sess.spawned.Load() || sess.dimName() != d.name {
			return true
		}
		x, y, z, _, _, _ := sess.Pos()
		if x < xMin || x > xMax || z < zMin || z > zMax || y < yMin || y > yMax {
			return true
		}
		// The ambient+visible instance the vanilla beacon hands out
		// shows as flags 0x07: ambient(1) | visible(2) | icon(4).
		s.applyMobEffectWire(sess, pid, amp, duration, false, 0x07)
		if hasSec {
			s.applyMobEffectWire(sess, sid, 0, duration, false, 0x07)
		}
		return true
	})
}

// beaconPropsChanged pushes the beacon menu's three data slots to every
// viewer of that beacon's menu: vanilla's setChanged lands these on
// the same rescan the levels moved.
func (s *Server) beaconPropsChanged(d *dimension, pos world.Pos) {
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay {
			return true
		}
		sess.windowMu.Lock()
		w := sess.window
		sess.windowMu.Unlock()
		if w == nil || w.container == nil || w.container.spec.menu != menuBeacon || w.dim != d || w.pos != pos {
			return true
		}
		s.sendBeaconProperties(sess, w)
		return true
	})
}

// sendBeaconProperties pushes the beacon menu's data slots —
// levels, primary, secondary — each effect the mob-effect registry
// id + 1, 0 marking "no effect" (BeaconMenu.encodeEffect).
func (s *Server) sendBeaconProperties(sess *Session, w *openWindow) {
	be := w.dim.beacons[w.pos]
	props := [3]int32{}
	if be != nil {
		props[0] = int32(be.levels)
		if id, ok := MobEffectID(be.primary); ok {
			props[1] = id + 1
		}
		if id, ok := MobEffectID(be.secondary); ok {
			props[2] = id + 1
		}
	}
	for i, v := range props {
		s.sendContainerProperty(sess, w.id, int16(i), v)
	}
}

// closeBeaconMenu is BeaconMenu.removed: the carried stack goes back
// first — dropped when the menu died with the session, placed back in
// the inventory on a client close — and the payment slot's whole
// stack is dropped at the player either way.
func (s *Server) closeBeaconMenu(sess *Session, w *openWindow, c *containerData, dropped bool) {
	if dropped {
		s.dropCarried(sess)
	} else {
		s.returnCarried(sess)
	}
	w.dim.containers.mu.Lock()
	st := c.slots[beaconPayment]
	c.slots[beaconPayment] = ItemStack{}
	w.dim.containers.mu.Unlock()
	if !st.Empty() {
		s.dropItemLocked(sess, st)
	}
}

// beaconSeesSky checks that the column above the beacon is air up to
// the dimension ceiling — the vanilla beam-section scan's outcome.
func (s *Server) beaconSeesSky(d *dimension, pos world.Pos) bool {
	for y := pos.Y + 1; y < d.minY+d.height; y++ {
		if d.w.GetBlock(world.Pos{X: pos.X, Y: y, Z: pos.Z}) != world.Air {
			return false
		}
	}
	return true
}

// beaconBaseDepth counts the contiguous complete pyramid layers under
// the beacon: layer i is the (2i+1)×(2i+1) square of base blocks at
// pos.Y-i, i=1..4.
func (s *Server) beaconBaseDepth(d *dimension, pos world.Pos) int {
	depth := 0
	for i := 1; i <= 4; i++ {
		y := pos.Y - i
		ok := true
		for dx := -i; dx <= i && ok; dx++ {
			for dz := -i; dz <= i && ok; dz++ {
				b := d.w.GetBlock(world.Pos{X: pos.X + dx, Y: y, Z: pos.Z + dz})
				name, _ := world.StateOwner(b)
				if !beaconBaseBlocks[name] {
					ok = false
				}
			}
		}
		if !ok {
			break
		}
		depth = i
	}
	return depth
}

// fireConstructBeaconNear grants construct_beacon(level) to every
// player inside vanilla's activation box around the beacon: x/z ±10,
// y from pos.Y-9 to pos.Y+5 (the AABB corner at pos.Y-4 inflated by 5).
func (s *Server) fireConstructBeaconNear(d *dimension, pos world.Pos, levels int) {
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || !sess.spawned.Load() || sess.dimName() != d.name {
			return true
		}
		x, y, z, _, _, _ := sess.Pos()
		if x >= float64(pos.X-10) && x <= float64(pos.X+10) &&
			z >= float64(pos.Z-10) && z <= float64(pos.Z+10) &&
			y >= float64(pos.Y-9) && y <= float64(pos.Y+5) {
			s.fireConstructBeacon(sess, levels)
		}
		return true
	})
}

// playSoundIn broadcasts a positioned block sound (SoundSource.BLOCKS)
// to every play session in the dimension. The wire sound_event value
// is the registry protocol id + 1 — the holder encoding reserves 0 for
// an inline event — same convention as broadcastAttackSound.
func (s *Server) playSoundIn(dim string, pos world.Pos, soundID int32) {
	var b protocol.Buffer
	b.VarInt(soundID + 1)
	b.VarInt(4) // SoundSource.BLOCKS
	b.Int(int32(pos.X * 8))
	b.Int(int32(pos.Y * 8))
	b.Int(int32(pos.Z * 8))
	b.Float(1)
	b.Float(1)
	b.Long(0)
	body := b.Bytes()
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other.state.Load() != StatePlay || !other.spawned.Load() || other.dimName() != dim {
			return true
		}
		_ = other.write(other.ids().PlaySound, body)
		return true
	})
}
