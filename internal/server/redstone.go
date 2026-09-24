package server

// The 26.2 SignalGetter model — measured on vanilla bytecode:
//
//	hasNeighborSignal(P) = ∃d: signalFrom(P+d, d) > 0
//	signalFrom(N, d)       = max(blockSignal(N, d),
//	                         conductor(N) ? directSignalTo(N) : 0)
//	directSignalTo(N)      = max_d directSignal(N+d, d)
//
// `d` is the offset direction from the consumer toward the emitter:
// a consumer at P asks each neighbour N = P+d for the signal it emits
// in direction d (i.e. back toward P). Blocks: ownSignal is the base
// emit strength, getSignal adds direction carve-outs (a floor torch
// emits nothing downward toward its mount), and getDirectSignal is
// the strong-power half a solid conductor block relays onward.
//
// Redstone wire's own power propagation (the 26.2 evaluator) is a
// separate slice — the wire's emit side (power prop out to connected
// sides) is modelled so pre-placed or command-set dust still powers
// neighbours.
//
// http://minecraft.wiki/Redstone_mechanics
import (
	"math/rand"
	"strings"

	"mc-go/internal/world"
)

// rsDir indexes Direction's six faces; rsOff gives the offset the
// consumer-to-emitter direction implies.
type rsDir int

const (
	rsDown rsDir = iota
	rsUp
	rsNorth
	rsSouth
	rsWest
	rsEast
)

var rsOff = [6]world.Pos{
	{X: 0, Y: -1, Z: 0}, {X: 0, Y: 1, Z: 0},
	{X: 0, Y: 0, Z: -1}, {X: 0, Y: 0, Z: 1},
	{X: -1, Y: 0, Z: 0}, {X: 1, Y: 0, Z: 0},
}

var rsDirOfName = map[string]rsDir{
	"down": rsDown, "up": rsUp,
	"north": rsNorth, "south": rsSouth,
	"west": rsWest, "east": rsEast,
}

func rsOpposite(d rsDir) rsDir { return d ^ 1 }

// rsPropsOf is the small state-prop helper the emitters lean on.
func rsPropsOf(blk world.Block) func(string) string {
	return func(k string) string {
		v, _ := world.StateProperty(blk, k)
		return v
	}
}

// rsAttachDir is FaceAttachedHorizontalDirectionalBlock
// .getConnectedDirection: the direction the control's mount sits in —
// down for floor, up for ceiling, and behind facing for wall.
func rsAttachDir(blk world.Block) (rsDir, bool) {
	prop := rsPropsOf(blk)
	switch prop("face") {
	case "floor":
		return rsDown, true
	case "ceiling":
		return rsUp, true
	case "wall":
		if d, ok := rsDirOfName[prop("facing")]; ok {
			return rsOpposite(d), true
		}
	}
	return 0, false
}

// rsOwnSignal is each signal source's ownSignal: the strength the
// block emits before direction carving. The powered/lit boolean
// convention covers levers, buttons, plates, torches, rails-as
// -sources (detector), hooks and observers; numeric sources read
// their `power` prop (wire, daylight detector, weighted plates).
func rsOwnSignal(blk world.Block) int {
	owner, ok := world.StateOwner(blk)
	if !ok {
		return 0
	}
	name := strings.TrimPrefix(owner, "minecraft:")
	prop := rsPropsOf(blk)
	switch {
	case name == "redstone_block":
		return 15 // PoweredBlock.ownSignal — constant
	case name == "redstone_wire":
		return rsPropInt(prop("power"))
	case name == "daylight_detector":
		return rsPropInt(prop("power"))
	case strings.HasSuffix(name, "_pressure_plate"):
		// Weighted plates carry a numeric power prop; the rest emit
		// 15 while their powered flag is up.
		if pw := prop("power"); pw != "" {
			return rsPropInt(pw)
		}
		if prop("powered") == "true" {
			return 15
		}
	case name == "redstone_torch" || name == "redstone_wall_torch":
		if prop("lit") == "true" {
			return 15
		}
	case prop("powered") == "true":
		// lever, *_button, observer, detector_rail, tripwire_hook,
		// lectern, lightning_rod, sculk_sensor — every "powered" bool
		// source emits 15.
		switch name {
		case "lever", "observer", "lectern", "lightning_rod",
			"tripwire_hook", "sculk_sensor", "calibrated_sculk_sensor":
			return 15
		default:
			if strings.HasSuffix(name, "_button") ||
				name == "detector_rail" {
				return 15
			}
		}
	}
	return 0
}

func rsPropInt(v string) int {
	n := 0
	for _, c := range v {
		if c < '0' || c > '9' {
			return n
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// rsGetSignal is Block.getSignal: ownSignal minus the direction
// carve-outs each source applies.
func rsGetSignal(d *dimension, pos world.Pos, dir rsDir) int {
	blk := d.w.GetBlock(pos)
	sig := rsOwnSignal(blk)
	if sig == 0 {
		return 0
	}
	owner, _ := world.StateOwner(blk)
	name := strings.TrimPrefix(owner, "minecraft:")
	prop := rsPropsOf(blk)
	switch name {
	case "redstone_torch":
		// The mount is below — a torch never powers down into it.
		if dir == rsUp {
			return 0
		}
	case "redstone_wall_torch":
		// Nor does a wall torch feed the block in front of its face.
		if f, ok := rsDirOfName[prop("facing")]; ok && dir == f {
			return 0
		}
	case "redstone_wire":
		// Wire emits only through connected sides — toward the block
		// below always, never up, and horizontally only where the
		// facing side prop is attached.
		switch {
		case dir == rsDown:
			return 0
		case dir == rsUp:
		default:
			side := prop(rsDirName(rsOpposite(dir)))
			if side != "side" && side != "up" {
				return 0
			}
		}
	}
	return sig
}

// rsDirName maps back to the prop/direction name used in state data.
func rsDirName(d rsDir) string {
	return [...]string{"down", "up", "north", "south", "west", "east"}[d]
}

// rsGetDirectSignal is getDirectSignal — the strong power that feeds
// conductor blocks. Lever/button push their mount, a plate pushes
// the block beneath it, a torch pushes the block above it, and wire
// drives its connected sides directly.
func rsGetDirectSignal(d *dimension, pos world.Pos, dir rsDir) int {
	blk := d.w.GetBlock(pos)
	owner, ok := world.StateOwner(blk)
	if !ok {
		return 0
	}
	name := strings.TrimPrefix(owner, "minecraft:")
	prop := rsPropsOf(blk)
	switch {
	case name == "lever" || strings.HasSuffix(name, "_button"):
		if prop("powered") != "true" {
			return 0
		}
		// getConnectedDirection is the control's pointing direction —
		// opposite the mount — so only the mount's query direction
		// matches and the mount block ends up strongly powered.
		if att, ok := rsAttachDir(blk); ok && dir == rsOpposite(att) {
			return 15
		}
	case strings.HasSuffix(name, "_pressure_plate"):
		if dir == rsUp {
			return rsOwnSignal(blk)
		}
	case name == "redstone_torch" || name == "redstone_wall_torch":
		if dir == rsDown {
			return rsGetSignal(d, pos, dir)
		}
	case name == "redstone_wire":
		return rsGetSignal(d, pos, dir)
	}
	return 0
}

// rsDirectSignalTo is SignalGetter.getDirectSignalTo: the strongest
// direct signal pushed into pos by any neighbour.
func rsDirectSignalTo(d *dimension, pos world.Pos) int {
	best := 0
	for i, o := range rsOff {
		n := world.Pos{X: pos.X + o.X, Y: pos.Y + o.Y, Z: pos.Z + o.Z}
		if v := rsGetDirectSignal(d, n, rsDir(i)); v > best {
			best = v
			if best >= 15 {
				return 15
			}
		}
	}
	return best
}

// rsConductor approximates BlockState.isRedstoneConductor (vanilla's
// isSolidRender): full opaque cubes relay strong power; transparent
// or non-full blocks never do. The set lists the transparent and
// non-full families the name-based test can't tell from solids.
func rsConductor(blk world.Block) bool {
	if world.NonSolid(blk) {
		return false
	}
	owner, ok := world.StateOwner(blk)
	if !ok {
		return false
	}
	name := strings.TrimPrefix(owner, "minecraft:")
	if rsNonConductor[name] {
		return false
	}
	for _, suf := range rsNonConductorSuffix {
		if strings.HasSuffix(name, suf) {
			return false
		}
	}
	return true
}

var rsNonConductorSuffix = []string{
	"_slab", "_stairs", "_door", "_trapdoor", "_fence", "_fence_gate",
	"_wall", "_pane", "_sign", "_banner", "_bed", "_carpet", "_candle",
	"_rail", "_button", "_pressure_plate", "_lever", "_torch",
	"_skull", "_head", "_lantern", "_chain", "_bell", "_leaves",
	"_glass", "_coral_fan", "_shelf", "_bars",
}

// Vanilla isRedstoneConductor defaults to canOcclude: transparent
// solids (glass, ice, slime) and non-full shapes (anvils, hoppers,
// chests) never relay direct power; opaque full cubes do.
var rsNonConductor = map[string]bool{
	"glass": true, "tinted_glass": true, "ice": true, "packed_ice": true,
	"blue_ice": true, "frosted_ice": true, "glowstone": true,
	"sea_lantern": true, "barrier": true, "slime_block": true,
	"honey_block": true, "beacon": true, "soul_sand": true,
	"farmland": true, "dirt_path": true,
	"daylight_detector": true, "comparator": true, "repeater": true,
	"redstone_wire": true, "hopper": true, "composter": true,
	"cauldron": true, "water_cauldron": true, "lava_cauldron": true,
	"powder_snow_cauldron": true, "anvil": true, "chipped_anvil": true,
	"damaged_anvil": true, "grindstone": true, "stonecutter": true,
	"lectern": true, "conduit": true, "lightning_rod": true,
	"pointed_dripstone": true, "amethyst_cluster": true,
	"big_dripleaf": true, "small_dripleaf": true, "scaffolding": true,
	"chest": true, "trapped_chest": true, "ender_chest": true,
	"enchanting_table": true, "brewing_stand": true, "snow": true,
}

// rsSignalAt is SignalGetter.getSignal(N, dir): the block at N's own
// emission in direction dir, or — when N is a redstone conductor —
// the strong power pushed into N.
func rsSignalAt(d *dimension, n world.Pos, dir rsDir) int {
	sig := rsGetSignal(d, n, dir)
	if rsConductor(d.w.GetBlock(n)) {
		if v := rsDirectSignalTo(d, n); v > sig {
			sig = v
		}
	}
	return sig
}

// rsSignalFrom is the signal arriving at the consumer pos from
// direction dir — rsSignalAt evaluated at the neighbour pos+dir.
func rsSignalFrom(d *dimension, pos world.Pos, dir rsDir) int {
	n := world.Pos{X: pos.X + rsOff[dir].X, Y: pos.Y + rsOff[dir].Y, Z: pos.Z + rsOff[dir].Z}
	return rsSignalAt(d, n, dir)
}

// rsBestNeighborSignal is getBestNeighborSignal: the strongest
// neighbour signal reaching pos.
func rsBestNeighborSignal(d *dimension, pos world.Pos) int {
	best := 0
	for i := range rsOff {
		if v := rsSignalFrom(d, pos, rsDir(i)); v > best {
			best = v
			if best >= 15 {
				return 15
			}
		}
	}
	return best
}

// rsHasNeighborSignal is Level.hasNeighborSignal.
func rsHasNeighborSignal(d *dimension, pos world.Pos) bool {
	return rsBestNeighborSignal(d, pos) > 0
}

// rsTorchMountDir returns the mount pos and the torch→mount direction
// (down for a floor torch, opposite(facing) for a wall torch).
func rsTorchMountDir(pos world.Pos, blk world.Block) (world.Pos, rsDir) {
	if f, _ := world.StateProperty(blk, "facing"); f != "" {
		if dir, ok := rsDirOfName[f]; ok {
			md := rsOpposite(dir)
			o := rsOff[md]
			return world.Pos{X: pos.X + o.X, Y: pos.Y + o.Y, Z: pos.Z + o.Z}, md
		}
	}
	return world.Pos{X: pos.X, Y: pos.Y - 1, Z: pos.Z}, rsDown
}

// rsTorchSeesSignal is RedstoneTorchBlock.hasNeighborSignal —
// hasSignal(mount, mountDir): the mount's own emission toward
// mountDir, or its strong power when the mount conducts. True means
// "the mount carries signal" — the torch must be dark then.
func rsTorchSeesSignal(d *dimension, pos world.Pos, blk world.Block) bool {
	mount, mdir := rsTorchMountDir(pos, blk)
	return rsSignalAt(d, mount, mdir) > 0
}

// rsNeighborChanged is the consumer half of vanilla's
// neighborChanged fan-out: the blocks that re-evaluate their signal
// and write a state when it moved. Doors/trapdoors/gates already do
// this through explodeNeighborChanged; lamp, rails and the torches
// join them here, and TNT primes on a fresh signal.
func (s *Server) rsNeighborChanged(d *dimension, pos world.Pos) {
	blk := d.w.GetBlock(pos)
	owner, ok := world.StateOwner(blk)
	if !ok {
		return
	}
	name := strings.TrimPrefix(owner, "minecraft:")
	sig := rsHasNeighborSignal(d, pos)
	switch {
	case strings.HasSuffix(name, "_door") ||
		strings.HasSuffix(name, "_trapdoor") ||
		strings.HasSuffix(name, "_fence_gate"):
		s.explodeNeighborChanged(d, pos)
	case name == "redstone_lamp":
		// RedstoneLampBlock.neighborChanged: a fresh signal latches
		// lit instantly; losing it schedules a 4-tick unlatch.
		lit := rsPropsOf(blk)("lit") == "true"
		if lit == sig {
			return
		}
		if lit {
			s.ticks.schedule(schedKey{dim: d.name, pos: pos, kind: tickBlock}, s.TickCount()+4)
			return
		}
		if nb, ok := world.WithProperty(blk, "lit", "true"); ok {
			d.w.SetBlock(pos, nb)
			s.broadcastBlockState(d, pos, nb)
		}
	case name == "powered_rail" || name == "activator_rail" ||
		name == "detector_rail":
		// BaseRailBlock.neighborChanged: powered follows the sensed
		// signal (the 9-rail chain is a later slice).
		if rsPropsOf(blk)("powered") == boolStr(sig) {
			return
		}
		if nb, ok := world.WithProperty(blk, "powered", boolStr(sig)); ok {
			d.w.SetBlock(pos, nb)
			s.broadcastBlockState(d, pos, nb)
		}
	case name == "redstone_torch" || name == "redstone_wall_torch":
		// RedstoneTorchBlock.neighborChanged: when lit agrees with
		// the mount's signal (lit while powered, dark while clear), a
		// 2-tick toggle is scheduled.
		if lit := rsPropsOf(blk)("lit") == "true"; lit == rsTorchSeesSignal(d, pos, blk) {
			s.ticks.schedule(schedKey{dim: d.name, pos: pos, kind: tickBlock}, s.TickCount()+2)
		}
	case name == "tnt":
		if sig {
			s.tntNeighborChanged(d, pos)
		}
	}
}

// rsNeighborFanOut mirrors updateNeighborsAt: every neighbour of a
// written block runs its neighborChanged. Called from the shared
// write path (setBlockAndBroadcastNotify) and the face-control
// toggles via explodeUpdateNeighboursAttached.
func (s *Server) rsNeighborFanOut(d *dimension, pos world.Pos) {
	for _, o := range rsOff {
		s.rsNeighborChanged(d, world.Pos{X: pos.X + o.X, Y: pos.Y + o.Y, Z: pos.Z + o.Z})
	}
}

// rsTorchNotify is RedstoneTorchBlock.notifyNeighbors: a torch's
// place/remove update reaches every neighbour of every neighbour —
// the blocks a torch powers through its mount sit one step out.
func (s *Server) rsTorchNotify(d *dimension, pos world.Pos) {
	for _, o := range rsOff {
		s.rsNeighborFanOut(d, world.Pos{X: pos.X + o.X, Y: pos.Y + o.Y, Z: pos.Z + o.Z})
	}
}

// rsButtonPress is ButtonBlock.press: powered -> true, the attached
// fan-out runs, the release tick is scheduled (30t wood / 20t stone)
// and click_on plays. Shared by wind-charge hits and hand use.
func (s *Server) rsButtonPress(d *dimension, pos world.Pos, name string, blk world.Block) {
	nb, ok := world.WithProperty(blk, "powered", "true")
	if !ok {
		return
	}
	d.w.SetBlock(pos, nb)
	s.broadcastBlockState(d, pos, nb)
	s.explodeUpdateNeighboursAttached(d, pos, nb)
	fam := windSoundFamily(name)
	stay := int64(30)
	if fam == "stone" {
		stay = 20
	}
	s.ticks.schedule(schedKey{dim: d.name, pos: pos, kind: tickBlock}, s.TickCount()+stay)
	s.explodeBlockSound(d, pos, fam+"_button.click_on", 1, 1)
}

// rsLeverPull is LeverBlock.pull: powered flips, the attached fan-out
// runs and the click plays (0.6 rising / 0.5 falling, vol 0.3).
func (s *Server) rsLeverPull(d *dimension, pos world.Pos, blk world.Block) {
	powered, _ := world.StateProperty(blk, "powered")
	nb, ok := world.WithProperty(blk, "powered", boolStr(powered != "true"))
	if !ok {
		return
	}
	d.w.SetBlock(pos, nb)
	s.broadcastBlockState(d, pos, nb)
	s.explodeUpdateNeighboursAttached(d, pos, nb)
	pitch := float32(0.5)
	if powered != "true" {
		pitch = 0.6
	}
	s.explodeBlockSound(d, pos, "lever.click", 0.3, pitch)
}

// rsToggleOpenable is DoorBlock/TrapDoorBlock/FenceGateBlock
// useWithoutItem: open flips and the family's open/close sound plays
// at pitch 0.9-1.0. Iron variants don't answer to hands. Returns
// false when the clicked block isn't a hand-openable.
func (s *Server) rsToggleOpenable(d *dimension, pos world.Pos, name string) bool {
	blk := d.w.GetBlock(pos)
	var fam string
	switch {
	case strings.HasSuffix(name, "_door"):
		fam = windSoundFamily(name)
		if fam == "iron" {
			return false
		}
		fam += "_door"
	case strings.HasSuffix(name, "_trapdoor"):
		fam = windSoundFamily(name)
		if fam == "iron" {
			return false
		}
		fam += "_trapdoor"
	case strings.HasSuffix(name, "_fence_gate"):
		fam = windGateSoundFam(name)
	default:
		return false
	}
	open, _ := world.StateProperty(blk, "open")
	nb, ok := world.WithProperty(blk, "open", boolStr(open != "true"))
	if !ok {
		return false
	}
	d.w.SetBlock(pos, nb)
	s.broadcastBlockState(d, pos, nb)
	snd := fam + ".open"
	if open == "true" {
		snd = fam + ".close"
	}
	s.explodeBlockSound(d, pos, snd, 1, rand.Float32()*0.1+0.9)
	return true
}
