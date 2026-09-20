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
// placed beacon — the vanilla block entity's `levels` field.
type beaconEnt struct {
	levels int
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
