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
	"strconv"
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
	case name == "sculk_sensor" || name == "calibrated_sculk_sensor":
		// SculkSensorBlock.getSignal: the analog level is the power
		// prop the vibration set — 0 on an idle/cooldown sensor.
		return rsPropInt(prop("power"))
	case name == "redstone_torch" || name == "redstone_wall_torch":
		if prop("lit") == "true" {
			return 15
		}
	case prop("powered") == "true":
		// lever, *_button, observer, detector_rail, tripwire_hook,
		// lectern, lightning_rod — every "powered" bool source
		// emits 15.
		switch name {
		case "lever", "observer", "lectern", "lightning_rod",
			"tripwire_hook":
			return 15
		case "repeater":
			return 15 // DiodeBlock.getOutputSignal — constant
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
	owner, _ := world.StateOwner(blk)
	name := strings.TrimPrefix(owner, "minecraft:")
	sig := rsOwnSignal(blk)
	if name == "comparator" {
		// ComparatorBlock's ownSignal is powered ? the block
		// entity's stored output : 0 — the level lives on the BE,
		// not in state.
		sig = rsCompOwn(d, pos, blk)
	}
	if sig == 0 {
		return 0
	}
	// getBlockSignal silences wires while it samples neighbour
	// signal so dust can't feed itself through getSignal.
	if name == "redstone_wire" && d.rsSilenceWire {
		return 0
	}
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
	case "repeater", "comparator":
		// DiodeBlock.getSignal: emits only toward the output side —
		// a reader there asks with dir == FACING (FACING names the
		// input side; output is FACING.opposite, which is where the
		// consumer sits when its consumer-to-emitter dir is FACING).
		f, ok := rsDirOfName[prop("facing")]
		if !ok || dir != f {
			return 0
		}
	case "observer":
		// ObserverBlock.getSignal: the pulse exits the back cell —
		// a reader behind the sensor asks with dir == FACING.
		f, ok := rsDirOfName[prop("facing")]
		if !ok || dir != f {
			return 0
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
	case name == "sculk_sensor" || name == "calibrated_sculk_sensor":
		// SculkSensorBlock.getDirectSignal: strong power only toward
		// the block above (dir == UP).
		if dir == rsUp {
			return rsGetSignal(d, pos, dir)
		}
	case name == "redstone_torch" || name == "redstone_wall_torch":
		if dir == rsDown {
			return rsGetSignal(d, pos, dir)
		}
	case name == "redstone_wire":
		return rsGetSignal(d, pos, dir)
	case name == "repeater" || name == "comparator" || name == "observer":
		// DiodeBlock.getDirectSignal delegates to getSignal — the
		// diode's output is strong power for the block it faces.
		// ObserverBlock's does too: the pulse drives a conductor.
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
	"piston_head": true, "moving_piston": true,
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
	case name == "powered_rail" || name == "activator_rail":
		// PoweredRailBlock.updateState: powered when a direct signal
		// lands OR a lit same-block rail sits within 8 links of the
		// connected track (findPoweredRailSignal, both directions).
		pow := sig || rsRailChainPowered(d, pos, blk)
		if rsPropsOf(blk)("powered") == boolStr(pow) {
			return
		}
		if nb, ok := world.WithProperty(blk, "powered", boolStr(pow)); ok {
			// setBlock flag 3 + updateNeighborsAt(below) +, for a
			// slope, updateNeighborsAt(above).
			d.w.SetBlock(pos, nb)
			s.broadcastBlockState(d, pos, nb)
			s.rsNeighborFanOut(d, pos)
			s.rsNeighborFanOut(d, world.Pos{X: pos.X, Y: pos.Y - 1, Z: pos.Z})
			switch rsPropsOf(nb)("shape") {
			case "ascending_east", "ascending_west",
				"ascending_north", "ascending_south":
				s.rsNeighborFanOut(d, world.Pos{X: pos.X, Y: pos.Y + 1, Z: pos.Z})
			}
		}
	case name == "detector_rail":
		// DetectorRailBlock.neighborChanged is checkPressed — a
		// minecart scan only. A neighbouring signal never lights it.
	case name == "redstone_torch" || name == "redstone_wall_torch":
		// RedstoneTorchBlock.neighborChanged: when lit agrees with
		// the mount's signal (lit while powered, dark while clear), a
		// 2-tick toggle is scheduled.
		if lit := rsPropsOf(blk)("lit") == "true"; lit == rsTorchSeesSignal(d, pos, blk) {
			s.ticks.schedule(schedKey{dim: d.name, pos: pos, kind: tickBlock}, s.TickCount()+2)
		}
	case name == "dispenser" || name == "dropper":
		s.rsDispenserNeighbor(d, pos, blk)
	case name == "crafter":
		s.crafterNeighborChanged(d, pos, blk)
	case name == "tnt":
		if sig {
			s.tntNeighborChanged(d, pos)
		}
	case name == "repeater" || name == "comparator":
		// DiodeBlock.neighborChanged: the live state already read at
		// top is the is(this) re-check; an unsupported diode drops
		// and rings its own neighbours, else the flip goes through
		// the tick queue (checkTickOnNeighbor).
		below := world.Pos{X: pos.X, Y: pos.Y - 1, Z: pos.Z}
		if !rsFaceSturdyUp(d.w.GetBlock(below)) {
			s.broadcastLevelEvent(d.name, 2001, pos, int32(blk))
			s.setNaturalBlock(d, pos, world.Air)
			// Level.removeBlock's own ring: updateNeighborsAt runs
			// around each neighbour of the popped diode.
			for _, o := range rsOff {
				s.rsNeighborFanOut(d, world.Pos{X: pos.X + o.X, Y: pos.Y + o.Y, Z: pos.Z + o.Z})
			}
			return
		}
		if name == "comparator" {
			s.rsCompCheckTick(d, pos, blk)
		} else {
			s.rsDiodeCheckTick(d, pos, name, blk)
		}
	case name == "piston" || name == "sticky_piston":
		// PistonBaseBlock.neighborChanged → checkIfExtend: the
		// quasi-powered read decides, and the move itself goes through
		// the tick-tail blockEvent queue.
		s.rsPistonCheck(d, pos)
	case name == "redstone_wire":
		// RedStoneWireBlock.neighborChanged: canSurvive gates the
		// power recompute; a wire over air drops.
		below := world.Pos{X: pos.X, Y: pos.Y - 1, Z: pos.Z}
		if !rsFaceSturdyUp(d.w.GetBlock(below)) {
			s.broadcastLevelEvent(d.name, 2001, pos, int32(blk))
			s.setNaturalBlock(d, pos, world.Air)
			return
		}
		s.rsWireUpdate(d, pos, blk)
	}
}

// rsUpdate is one queued neighbour notification. single targets just
// pos (Level.neighborChanged); otherwise it is an updateNeighborsAt
// ring over pos's six neighbours, optionally skipping one direction
// (updateNeighborsAtExceptFromFacing — skip == -1 for none).
type rsUpdate struct {
	pos    world.Pos
	skip   rsDir
	single bool
}

// rsUpdateOrder is NeighborUpdater.UPDATE_ORDER — the direction order
// updateNeighborsAt fans out in.
var rsUpdateOrder = [6]rsDir{rsWest, rsEast, rsDown, rsUp, rsNorth, rsSouth}

// rsNeighborFanOut mirrors updateNeighborsAt: every neighbour of a
// written block runs its neighborChanged. While a block write is in
// flight the call only enqueues — vanilla's CollectingNeighborUpdater
// drains chained updates after the write's own neighbour pass and the
// updateNeighbourShapes fold, so e.g. a wire that powers up mid-write
// notifies the rail beside it only once that write's updateShape has
// folded the wire's arms. The outermost write drains the queue FIFO
// (setBlockAndBroadcastNotify); calls outside a write run at once.
func (s *Server) rsNeighborFanOut(d *dimension, pos world.Pos) {
	s.rsNeighborPush(d, rsUpdate{pos: pos, skip: -1})
}

// rsNeighborFanOutExcept is updateNeighborsAtExceptFromFacing — the
// ring minus the direction back to the source block.
func (s *Server) rsNeighborFanOutExcept(d *dimension, pos world.Pos, skip rsDir) {
	s.rsNeighborPush(d, rsUpdate{pos: pos, skip: skip})
}

// rsNeighborNotify is the queued form of a single-position
// Level.neighborChanged — used by block callbacks that notify one
// specific neighbour (DiodeBlock.updateNeighborsInFront's output cell).
func (s *Server) rsNeighborNotify(d *dimension, pos world.Pos) {
	s.rsNeighborPush(d, rsUpdate{pos: pos, single: true, skip: -1})
}

func (s *Server) rsNeighborPush(d *dimension, u rsUpdate) {
	if d.rsWriteDepth > 0 {
		d.rsPending = append(d.rsPending, u)
		return
	}
	s.rsRunUpdate(d, u)
}

func (s *Server) rsRunUpdate(d *dimension, u rsUpdate) {
	if u.single {
		s.rsNeighborChanged(d, u.pos)
		return
	}
	s.rsNeighborFanOutNow(d, u.pos, u.skip)
}

func (s *Server) rsNeighborFanOutNow(d *dimension, pos world.Pos, skip rsDir) {
	for _, dir := range rsUpdateOrder {
		if dir == skip {
			continue
		}
		o := rsOff[dir]
		s.rsNeighborChanged(d, world.Pos{X: pos.X + o.X, Y: pos.Y + o.Y, Z: pos.Z + o.Z})
	}
}

// rsDrainPending flushes the queued neighbour updates FIFO; updates
// chained mid-drain append and run in order.
func (s *Server) rsDrainPending(d *dimension) {
	for len(d.rsPending) > 0 {
		next := d.rsPending[0]
		d.rsPending = d.rsPending[1:]
		s.rsRunUpdate(d, next)
	}
	d.rsPending = d.rsPending[:0]
}

// rsWriteBlock is markAndNotifyBlock for writes that bypass
// setBlockAndBroadcastNotify (hand flips, scheduled toggles): the
// silent write + onPlace hooks, then updateNeighborsAt and
// updateNeighbourShapes in order, then extra() for per-block extras
// (a lever's updateNeighbours mount ring), with the queued chained
// updates draining last.
func (s *Server) rsWriteBlock(d *dimension, pos world.Pos, nb, notify world.Block, extra func()) {
	d.rsWriteDepth++
	s.setSilentBlock(d, pos, nb)
	s.rsNeighborFanOutNow(d, pos, -1)
	if rsHasAnalog(nb) {
		s.rsOutputSignalWake(d, pos)
	}
	s.neighborSurvival(d, pos, nb)
	if extra != nil {
		extra()
	}
	if d.rsWriteDepth == 1 {
		s.rsDrainPending(d)
	}
	d.rsWriteDepth--
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
	s.rsWriteBlock(d, pos, nb, nb, func() {
		s.explodeUpdateNeighboursAttached(d, pos, nb)
	})
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
	s.rsWriteBlock(d, pos, nb, nb, func() {
		s.explodeUpdateNeighboursAttached(d, pos, nb)
	})
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
	s.rsWriteBlock(d, pos, nb, nb, nil)
	snd := fam + ".open"
	if open == "true" {
		snd = fam + ".close"
	}
	s.explodeBlockSound(d, pos, snd, 1, rand.Float32()*0.1+0.9)
	return true
}

// ---- RedStoneWireBlock: connections and the DefaultRedstoneWireEvaluator ----

var rsHoriz = [4]rsDir{rsNorth, rsSouth, rsWest, rsEast}

// rsIsSignalSource is BlockState.isSignalSource: every block that can
// emit a signal counts — dust connects to it.
func rsIsSignalSource(blk world.Block) bool {
	owner, ok := world.StateOwner(blk)
	if !ok {
		return false
	}
	name := strings.TrimPrefix(owner, "minecraft:")
	if rsSignalSourceNames[name] {
		return true
	}
	for _, suf := range rsSignalSourceSuffix {
		if strings.HasSuffix(name, suf) {
			return true
		}
	}
	return false
}

// rsSignalSourceNames is the vanilla isSignalSource=true set (26.2):
// every source block dust connects to. DiodeBlock covers repeater +
// comparator, PoweredBlock the redstone block; the rest override
// isSignalSource directly. Powered rails/lamps/pistons are NOT
// sources — dust does not fold an arm toward them.
var rsSignalSourceNames = map[string]bool{
	"redstone_wire": true, "redstone_torch": true, "redstone_wall_torch": true,
	"redstone_block": true, "lever": true, "daylight_detector": true,
	"comparator": true, "repeater": true, "observer": true,
	"target": true, "tripwire_hook": true, "lectern": true,
	"lightning_rod": true, "sculk_sensor": true, "calibrated_sculk_sensor": true,
	"detector_rail": true, "trapped_chest": true,
}

var rsSignalSourceSuffix = []string{"_button", "_pressure_plate"}

// rsShouldConnectTo is RedStoneWireBlock.shouldConnectTo(state, dir):
// wire connects to other wire, to a repeater looking at/away from it,
// to an observer facing it, and to any signal source.
func rsShouldConnectTo(d *dimension, pos world.Pos, dir rsDir) bool {
	blk := d.w.GetBlock(pos)
	owner, ok := world.StateOwner(blk)
	if !ok {
		return false
	}
	name := strings.TrimPrefix(owner, "minecraft:")
	switch name {
	case "redstone_wire":
		return true
	case "repeater":
		f, _ := world.StateProperty(blk, "facing")
		fd, ok := rsDirOfName[f]
		return ok && (fd == dir || rsOpposite(fd) == dir)
	case "observer":
		f, _ := world.StateProperty(blk, "facing")
		fd, ok := rsDirOfName[f]
		return ok && fd == dir
	}
	return rsIsSignalSource(blk)
}

// rsFaceSturdyUp approximates isFaceSturdy(_, UP): the block's top is a
// full square — conductors plus the transparent full cubes and the
// non-full blocks whose top face is still complete.
func rsFaceSturdyUp(blk world.Block) bool {
	if rsConductor(blk) {
		return true
	}
	owner, ok := world.StateOwner(blk)
	if !ok {
		return false
	}
	name := strings.TrimPrefix(owner, "minecraft:")
	switch name {
	case "glass", "tinted_glass", "ice", "packed_ice", "blue_ice",
		"frosted_ice", "glowstone", "sea_lantern", "barrier",
		"slime_block", "honey_block", "farmland", "dirt_path",
		"soul_sand", "mud", "hopper", "mangrove_roots",
		"muddy_mangrove_roots", "trial_spawner", "vault",
		"creaking_heart", "copper_bulb":
		return true
	}
	if strings.HasSuffix(name, "_stained_glass") {
		return true
	}
	// Top slabs and upside-down stairs still present a full top.
	if strings.HasSuffix(name, "_slab") {
		if t, _ := world.StateProperty(blk, "type"); t == "top" {
			return true
		}
	}
	if strings.HasSuffix(name, "_stairs") {
		if h, _ := world.StateProperty(blk, "half"); h == "top" {
			return true
		}
	}
	return false
}

// rsConnectingSide is getConnectingSide: climb to the wire on top of
// the neighbour when allowed, connect sideways into a connectable or
// down onto the wire under a non-conductor.
func (s *Server) rsConnectingSide(d *dimension, pos world.Pos, dir rsDir) string {
	o := rsOff[dir]
	n := world.Pos{X: pos.X + o.X, Y: pos.Y + o.Y, Z: pos.Z + o.Z}
	nb := d.w.GetBlock(n)
	canGoUp := !rsConductor(d.w.GetBlock(world.Pos{X: pos.X, Y: pos.Y + 1, Z: pos.Z}))
	if canGoUp && rsFaceSturdyUp(nb) {
		up := world.Pos{X: n.X, Y: n.Y + 1, Z: n.Z}
		if rsShouldConnectTo(d, up, dir) {
			// Wire climbs the sturdy face: UP when the neighbour's
			// side toward the wire is sturdy, else SIDE.
			return "up"
		}
	}
	if rsShouldConnectTo(d, n, dir) ||
		(!rsConductor(nb) && rsShouldConnectTo(d, world.Pos{X: n.X, Y: n.Y - 1, Z: n.Z}, dir)) {
		return "side"
	}
	return "none"
}

// rsWireSidesConnected reports which horizontal side props are
// connected (side or up).
func rsWireSidesConnected(blk world.Block) (conn [4]bool) {
	for i, h := range rsHoriz {
		v, _ := world.StateProperty(blk, rsDirName(h))
		conn[i] = v == "side" || v == "up"
	}
	return
}

// rsWireFold is getConnectionState: reset the four sides, recompute
// each missing connection, and — unless the wire was and stays a dot —
// add the survivor arms (a wire connected on no E/W arm gains both
// when it has no N/S connections, and vice versa).
func (s *Server) rsWireFold(d *dimension, pos world.Pos, blk world.Block) world.Block {
	conn := rsWireSidesConnected(blk)
	wasDot := !conn[0] && !conn[1] && !conn[2] && !conn[3]
	for _, h := range rsHoriz {
		i := int(h) - 2 // rsHoriz indexes 2..5 → slot 0..3
		if !conn[i] {
			nb, ok := world.WithProperty(blk, rsDirName(h), s.rsConnectingSide(d, pos, h))
			if ok {
				blk = nb
			}
		}
	}
	conn = rsWireSidesConnected(blk)
	isDot := !conn[0] && !conn[1] && !conn[2] && !conn[3]
	if wasDot && isDot {
		return blk
	}
	noNS := !conn[0] && !conn[1]
	noEW := !conn[2] && !conn[3]
	set := func(h rsDir, want bool) {
		if !want {
			return
		}
		if nb, ok := world.WithProperty(blk, rsDirName(h), "side"); ok {
			blk = nb
		}
	}
	set(rsWest, !conn[2] && noNS)
	set(rsEast, !conn[3] && noNS)
	set(rsNorth, !conn[0] && noEW)
	set(rsSouth, !conn[1] && noEW)
	return blk
}

// rsWirePowerOf reads the wire's power prop (0 for non-wire).
func rsWirePowerOf(blk world.Block) int {
	owner, ok := world.StateOwner(blk)
	if !ok || strings.TrimPrefix(owner, "minecraft:") != "redstone_wire" {
		return 0
	}
	p, _ := world.StateProperty(blk, "power")
	return rsPropInt(p)
}

// rsIncomingWireSignal is RedstoneWireEvaluator.getIncomingWireSignal:
// the best neighbour wire power — same-level wires, the wire on top of
// a conducting neighbour, or the wire below a non-conducting one —
// decayed by one.
func rsIncomingWireSignal(d *dimension, pos world.Pos) int {
	best := 0
	upBlk := d.w.GetBlock(world.Pos{X: pos.X, Y: pos.Y + 1, Z: pos.Z})
	upCond := rsConductor(upBlk)
	for _, h := range rsHoriz {
		o := rsOff[h]
		n := world.Pos{X: pos.X + o.X, Y: pos.Y + o.Y, Z: pos.Z + o.Z}
		nb := d.w.GetBlock(n)
		if v := rsWirePowerOf(nb); v > best {
			best = v
		}
		if rsConductor(nb) {
			if !upCond {
				if v := rsWirePowerOf(d.w.GetBlock(world.Pos{X: n.X, Y: n.Y + 1, Z: n.Z})); v > best {
					best = v
				}
			}
		} else if v := rsWirePowerOf(d.w.GetBlock(world.Pos{X: n.X, Y: n.Y - 1, Z: n.Z})); v > best {
			best = v
		}
	}
	if best > 0 {
		return best - 1
	}
	return 0
}

// rsWireTarget is DefaultRedstoneWireEvaluator.calculateTargetStrength:
// the best non-wire signal (wires are silenced so dust doesn't feed
// itself through getSignal), short-circuiting at 15, or the incoming
// wire strength when higher.
func (s *Server) rsWireTarget(d *dimension, pos world.Pos) int {
	d.rsSilenceWire = true
	best := rsBestNeighborSignal(d, pos)
	d.rsSilenceWire = false
	if best == 15 {
		return 15
	}
	if w := rsIncomingWireSignal(d, pos); w > best {
		return w
	}
	return best
}

// rsWireUpdate is updatePowerStrength: when the wire's power prop
// differs from the target, the wire rewrites (flag 2 — no neighbour
// fan-out from the write itself) and then pos plus all six neighbours
// run updateNeighborsAt.
func (s *Server) rsWireUpdate(d *dimension, pos world.Pos, blk world.Block) {
	target := s.rsWireTarget(d, pos)
	power := rsWirePowerOf(blk)
	if power == target {
		return
	}
	// updatePowerStrength writes only while the cell still holds the
	// wire it was asked about (a neighbourChanged re-entry may have
	// replaced it).
	if d.w.GetBlock(pos) == blk {
		if nb, ok := world.WithProperty(blk, "power", strconv.Itoa(target)); ok {
			d.w.SetBlock(pos, nb)
			s.broadcastBlockState(d, pos, nb)
		}
	}
	s.rsNeighborFanOut(d, pos)
	for i := range rsOff {
		s.rsNeighborFanOut(d, world.Pos{X: pos.X + rsOff[i].X, Y: pos.Y + rsOff[i].Y, Z: pos.Z + rsOff[i].Z})
	}
}

// rsCheckCornerChange is checkCornerChangeAt: a wire at p re-fans its
// neighbours plus the neighbours' neighbours — the ring-2 reach a
// power-carrying dust actually feeds.
func (s *Server) rsCheckCornerChange(d *dimension, p world.Pos) {
	blk := d.w.GetBlock(p)
	owner, ok := world.StateOwner(blk)
	if !ok || strings.TrimPrefix(owner, "minecraft:") != "redstone_wire" {
		return
	}
	s.rsNeighborFanOut(d, p)
	for i := range rsOff {
		s.rsNeighborFanOut(d, world.Pos{X: p.X + rsOff[i].X, Y: p.Y + rsOff[i].Y, Z: p.Z + rsOff[i].Z})
	}
}

// rsUpdateNeighborsOfNeighboringWires is updateNeighborsOfNeighboringWires:
// horizontal neighbour wires plus the wire around their corner.
func (s *Server) rsUpdateNeighborsOfNeighboringWires(d *dimension, pos world.Pos) {
	for _, h := range rsHoriz {
		o := rsOff[h]
		s.rsCheckCornerChange(d, world.Pos{X: pos.X + o.X, Y: pos.Y + o.Y, Z: pos.Z + o.Z})
	}
	for _, h := range rsHoriz {
		o := rsOff[h]
		n := world.Pos{X: pos.X + o.X, Y: pos.Y + o.Y, Z: pos.Z + o.Z}
		if rsConductor(d.w.GetBlock(n)) {
			s.rsCheckCornerChange(d, world.Pos{X: n.X, Y: n.Y + 1, Z: n.Z})
		} else {
			s.rsCheckCornerChange(d, world.Pos{X: n.X, Y: n.Y - 1, Z: n.Z})
		}
	}
}

// rsWireOnPlace is RedStoneWireBlock.onPlace: recompute own power,
// updateNeighborsAt the vertical neighbours, then the
// neighbouring-wires pass.
func (s *Server) rsWireOnPlace(d *dimension, pos world.Pos, blk world.Block) {
	s.rsWireUpdate(d, pos, blk)
	s.rsNeighborFanOut(d, world.Pos{X: pos.X, Y: pos.Y - 1, Z: pos.Z})
	s.rsNeighborFanOut(d, world.Pos{X: pos.X, Y: pos.Y + 1, Z: pos.Z})
	s.rsUpdateNeighborsOfNeighboringWires(d, pos)
}

// rsWireAfterRemoval is affectNeighborsAfterRemoval: all six
// neighbours update, the vacated pos recomputes (writes nothing since
// the wire is gone), then the neighbouring-wires pass.
func (s *Server) rsWireAfterRemoval(d *dimension, pos world.Pos) {
	for i := range rsOff {
		s.rsNeighborFanOut(d, world.Pos{X: pos.X + rsOff[i].X, Y: pos.Y + rsOff[i].Y, Z: pos.Z + rsOff[i].Z})
	}
	s.rsUpdateNeighborsOfNeighboringWires(d, pos)
}

// rsWireIsCross is isCross: all four sides connected and none up.
func rsWireIsCross(blk world.Block) bool {
	conn := rsWireSidesConnected(blk)
	if !conn[0] || !conn[1] || !conn[2] || !conn[3] {
		return false
	}
	for _, h := range rsHoriz {
		if v, _ := world.StateProperty(blk, rsDirName(h)); v == "up" {
			return false
		}
	}
	return true
}

// rsWireUpdateShape is RedStoneWireBlock.updateShape: DOWN pops when
// the support is gone, UP re-runs the whole fold, and a horizontal
// change updates just that side — a conn-ness flip or a cross wire
// triggers the full fold instead.
func (s *Server) rsWireUpdateShape(d *dimension, pos, changed world.Pos) {
	blk := d.w.GetBlock(pos)
	dx, dy, dz := changed.X-pos.X, changed.Y-pos.Y, changed.Z-pos.Z
	if dy == -1 {
		if !rsFaceSturdyUp(d.w.GetBlock(world.Pos{X: pos.X, Y: pos.Y - 1, Z: pos.Z})) {
			s.setSilentBlock(d, pos, world.Air)
		}
		return
	}
	if dy == 1 {
		if nb := s.rsWireFold(d, pos, blk); nb != blk {
			s.setSilentBlock(d, pos, nb)
		}
		return
	}
	dir := rsUp
	for _, h := range rsHoriz {
		o := rsOff[h]
		if dx == o.X && dz == o.Z {
			dir = h
		}
	}
	s.rsWireUpdateArm(d, pos, dir, blk)
}

// rsWireUpdateArm recomputes one horizontal side: when the
// connection-ness is unchanged and the wire isn't a cross only that
// arm rewrites; otherwise the whole connection state refolds.
func (s *Server) rsWireUpdateArm(d *dimension, pos world.Pos, dir rsDir, blk world.Block) {
	newSide := s.rsConnectingSide(d, pos, dir)
	oldSide, _ := world.StateProperty(blk, rsDirName(dir))
	oldConn := oldSide == "side" || oldSide == "up"
	newConn := newSide != "none"
	if oldConn == newConn && !rsWireIsCross(blk) {
		if newSide != oldSide {
			if nb, ok := world.WithProperty(blk, rsDirName(dir), newSide); ok {
				s.setSilentBlock(d, pos, nb)
			}
		}
		return
	}
	if nb := s.rsWireFold(d, pos, blk); nb != blk {
		s.setSilentBlock(d, pos, nb)
	}
}

// rsWireIndirectShapes is RedStoneWireBlock.updateIndirectNeighbourShapes:
// for each horizontal neighbour that is NOT a wire itself, the wires
// one block below and above it (the diagonal corner cells) get an
// updateShape toward the written block's side.
func (s *Server) rsWireIndirectShapes(d *dimension, pos world.Pos) {
	for _, h := range rsHoriz {
		o := rsOff[h]
		n := world.Pos{X: pos.X + o.X, Y: pos.Y + o.Y, Z: pos.Z + o.Z}
		nb := d.w.GetBlock(n)
		owner, _ := world.StateOwner(nb)
		if strings.TrimPrefix(owner, "minecraft:") == "redstone_wire" {
			continue
		}
		for _, dy := range []int{-1, 1} {
			c := world.Pos{X: n.X, Y: n.Y + dy, Z: n.Z}
			cb := d.w.GetBlock(c)
			cOwner, _ := world.StateOwner(cb)
			if strings.TrimPrefix(cOwner, "minecraft:") == "redstone_wire" {
				s.rsWireUpdateArm(d, c, rsOpposite(h), cb)
			}
		}
	}
}

// rsWireCross is crossState: every horizontal arm at SIDE.
func rsWireCross(blk world.Block) world.Block {
	for _, h := range rsHoriz {
		if nb, ok := world.WithProperty(blk, rsDirName(h), "side"); ok {
			blk = nb
		}
	}
	return blk
}

// ---- DiodeBlock (the repeater family; the comparator's analog half
// is a separate slice — it shares this shell) ----

// rsIsDiode is DiodeBlock.isDiode.
func rsIsDiode(blk world.Block) bool {
	owner, ok := world.StateOwner(blk)
	if !ok {
		return false
	}
	name := strings.TrimPrefix(owner, "minecraft:")
	return name == "repeater" || name == "comparator"
}

// rsDiodeFacing reads FACING — the direction from the diode toward its
// INPUT side. Its output sits at FACING.opposite.
func rsDiodeFacing(blk world.Block) (rsDir, bool) {
	f, ok := world.StateProperty(blk, "facing")
	if !ok {
		return 0, false
	}
	dir, ok := rsDirOfName[f]
	return dir, ok
}

func rsShiftPos(pos world.Pos, dir rsDir) world.Pos {
	o := rsOff[dir]
	return world.Pos{X: pos.X + o.X, Y: pos.Y + o.Y, Z: pos.Z + o.Z}
}

// rsDirCW/rsDirCCW are Direction.getClockWise/getCounterClockWise on
// the horizontal ring: N→E→S→W clockwise seen from above.
func rsDirCW(dir rsDir) rsDir {
	switch dir {
	case rsNorth:
		return rsEast
	case rsEast:
		return rsSouth
	case rsSouth:
		return rsWest
	case rsWest:
		return rsNorth
	}
	return dir
}

func rsDirCCW(dir rsDir) rsDir {
	switch dir {
	case rsNorth:
		return rsWest
	case rsWest:
		return rsSouth
	case rsSouth:
		return rsEast
	case rsEast:
		return rsNorth
	}
	return dir
}

// rsControlSignal is SignalGetter.getControlInputSignal: the signal a
// perpendicular side block feeds a diode. diodesOnly reads only other
// diodes' direct output — RepeaterBlock locking.
func rsControlSignal(d *dimension, pos world.Pos, dir rsDir, diodesOnly bool) int {
	blk := d.w.GetBlock(pos)
	owner, ok := world.StateOwner(blk)
	if !ok {
		return 0
	}
	name := strings.TrimPrefix(owner, "minecraft:")
	if diodesOnly {
		if !rsIsDiode(blk) {
			return 0
		}
		return rsGetDirectSignal(d, pos, dir)
	}
	switch {
	case name == "redstone_block":
		return 15
	case name == "redstone_wire":
		return rsPropInt(rsPropsOf(blk)("power"))
	case rsIsSignalSource(blk):
		return rsGetDirectSignal(d, pos, dir)
	}
	return 0
}

// rsDiodeAlt is DiodeBlock.getAlternateSignal: the strongest of the two
// perpendicular (clockwise/counter-clockwise of FACING) control inputs.
func rsDiodeAlt(d *dimension, pos world.Pos, facing rsDir, diodesOnly bool) int {
	cw, ccw := rsDirCW(facing), rsDirCCW(facing)
	a := rsControlSignal(d, rsShiftPos(pos, cw), cw, diodesOnly)
	b := rsControlSignal(d, rsShiftPos(pos, ccw), ccw, diodesOnly)
	if a > b {
		return a
	}
	return b
}

// rsDiodeLocked is the isLocked override: RepeaterBlock locks while a
// side diode pushes in; DiodeBlock's base (comparator) never locks.
// The LOCKED prop is display-only — this is the live recompute.
func rsDiodeLocked(d *dimension, pos world.Pos, name string, blk world.Block) bool {
	if name != "repeater" {
		return false
	}
	facing, ok := rsDiodeFacing(blk)
	if !ok {
		return false
	}
	return rsDiodeAlt(d, pos, facing, true) > 0
}

// rsDiodeDelay is RepeaterBlock.getDelay = DELAY*2 ticks.
// ComparatorBlock never consults getDelay — its checkTickOnNeighbor
// schedules a constant 2.
func rsDiodeDelay(name string, blk world.Block) int64 {
	if name == "repeater" {
		return int64(rsPropInt(rsPropsOf(blk)("delay"))) * 2
	}
	return 2
}

// rsDiodeInput is DiodeBlock.getInputSignal: the input cell's getSignal
// toward the diode, with the wire-power fallback (a wire reads its own
// POWER prop even when its emission to us is 0).
func rsDiodeInput(d *dimension, pos world.Pos, facing rsDir) int {
	in := rsShiftPos(pos, facing)
	sig := rsSignalAt(d, in, facing)
	if sig >= 15 {
		return sig
	}
	ib := d.w.GetBlock(in)
	if on, _ := world.StateOwner(ib); on == "minecraft:redstone_wire" {
		if w := rsPropInt(rsPropsOf(ib)("power")); w > sig {
			return w
		}
	}
	return sig
}

// rsDiodeShouldOn is DiodeBlock.shouldTurnOn.
func rsDiodeShouldOn(d *dimension, pos world.Pos, blk world.Block) bool {
	facing, ok := rsDiodeFacing(blk)
	if !ok {
		return false
	}
	return rsDiodeInput(d, pos, facing) > 0
}

// rsDiodePrioritizes is DiodeBlock.shouldPrioritize: the front block
// (output side) is a diode pointing anywhere but back along our output
// direction.
func rsDiodePrioritizes(d *dimension, pos world.Pos, blk world.Block) bool {
	facing, ok := rsDiodeFacing(blk)
	if !ok {
		return false
	}
	out := rsOpposite(facing)
	front := d.w.GetBlock(rsShiftPos(pos, out))
	if !rsIsDiode(front) {
		return false
	}
	ff, ok := rsDiodeFacing(front)
	return ok && ff != out
}

// rsDiodeCheckTick is DiodeBlock.checkTickOnNeighbor: unlocked and
// powered != shouldTurnOn, and not already due this tick → schedule
// the flip at getDelay with EXTREMELY_HIGH (back-to-back diode),
// VERY_HIGH (turning off), or HIGH (turning on) priority.
func (s *Server) rsDiodeCheckTick(d *dimension, pos world.Pos, name string, blk world.Block) {
	if rsDiodeLocked(d, pos, name, blk) {
		return
	}
	powered := rsPropsOf(blk)("powered") == "true"
	if powered == rsDiodeShouldOn(d, pos, blk) {
		return
	}
	key := schedKey{dim: d.name, pos: pos, kind: tickBlock}
	if at, ok := s.ticks.queuedAt(key); ok && at <= s.TickCount() {
		return // willTickThisTick
	}
	prio := tickPrioHigh
	if rsDiodePrioritizes(d, pos, blk) {
		prio = tickPrioExtremelyHigh
	} else if powered {
		prio = tickPrioVeryHigh
	}
	s.ticks.schedulePrio(key, s.TickCount()+rsDiodeDelay(name, blk), prio)
}

// rsDiodeTick is DiodeBlock.tick: locked diodes sit still; a powered
// diode that lost its input writes powered=false; an unpowered one
// writes powered=true and — if the input died in the delay window —
// re-arms itself to flip back at VERY_HIGH.
func (s *Server) rsDiodeTick(d *dimension, pos world.Pos, blk world.Block, name string) {
	if rsDiodeLocked(d, pos, name, blk) {
		return
	}
	powered := rsPropsOf(blk)("powered") == "true"
	shouldOn := rsDiodeShouldOn(d, pos, blk)
	switch {
	case powered && !shouldOn:
		if nb, ok := world.WithProperty(blk, "powered", "false"); ok {
			s.rsWriteBlockFlag2(d, pos, nb)
		}
	case !powered:
		if nb, ok := world.WithProperty(blk, "powered", "true"); ok {
			s.rsWriteBlockFlag2(d, pos, nb)
		}
		if !shouldOn {
			s.ticks.schedulePrio(schedKey{dim: d.name, pos: pos, kind: tickBlock},
				s.TickCount()+rsDiodeDelay(name, blk), tickPrioVeryHigh)
		}
	}
}

// rsWriteBlockFlag2 is the flag-2 write path (broadcast + onPlace +
// updateNeighbourShapes, no updateNeighborsAt ring): DiodeBlock.tick's
// powered flips announce only their output side — via onPlace's
// updateNeighborsInFront — not the whole ring.
func (s *Server) rsWriteBlockFlag2(d *dimension, pos world.Pos, nb world.Block) {
	d.rsWriteDepth++
	s.setSilentBlock(d, pos, nb)
	s.neighborSurvival(d, pos, nb)
	if d.rsWriteDepth == 1 {
		s.rsDrainPending(d)
	}
	d.rsWriteDepth--
}

// rsDiodeUpdateInFront is DiodeBlock.updateNeighborsInFront — runs on
// every write (onPlace) and on removal (affectNeighborsAfterRemoval):
// neighborChanged on the output block, then updateNeighborsAtExcept
// FromFacing over it (its ring minus the direction back to the diode).
func (s *Server) rsDiodeUpdateInFront(d *dimension, pos world.Pos, blk world.Block) {
	facing, ok := rsDiodeFacing(blk)
	if !ok {
		return
	}
	out := rsShiftPos(pos, rsOpposite(facing))
	s.rsNeighborNotify(d, out)
	s.rsNeighborFanOutExcept(d, out, facing)
}

// rsRailChainPowered is PoweredRailBlock's two-way
// findPoweredRailSignal search: the rail lights when a lit same-block
// rail sits within 8 links along the connected track in either
// direction — the 9-rail power boost.
func rsRailChainPowered(d *dimension, pos world.Pos, blk world.Block) bool {
	return rsRailChainStep(d, pos, blk, true, 0) ||
		rsRailChainStep(d, pos, blk, false, 0)
}

// rsRailChainStep is findPoweredRailSignal for one direction: move one
// cell along the track (a slope climbs when walking uphill), then
// check the cell — plus the cell below it when the step stayed level,
// which is how a chain follows a downhill rail.
func rsRailChainStep(d *dimension, pos world.Pos, blk world.Block, fwd bool, dist int) bool {
	if dist >= 8 {
		return false
	}
	x, y, z := pos.X, pos.Y, pos.Z
	shape := rsPropsOf(blk)("shape")
	axis := shape
	level := true
	switch shape {
	case "north_south":
		if fwd {
			z++
		} else {
			z--
		}
	case "east_west":
		if fwd {
			x--
		} else {
			x++
		}
	case "ascending_east":
		if fwd {
			x--
		} else {
			x++
			y++
			level = false
		}
		axis = "east_west"
	case "ascending_west":
		if fwd {
			x--
			y++
			level = false
		} else {
			x++
		}
		axis = "east_west"
	case "ascending_north":
		if fwd {
			z++
		} else {
			z--
			y++
			level = false
		}
		axis = "north_south"
	case "ascending_south":
		if fwd {
			z++
			y++
			level = false
		} else {
			z--
		}
		axis = "north_south"
	default:
		return false
	}
	if rsRailLinkPowered(d, world.Pos{X: x, Y: y, Z: z}, blk, fwd, dist, axis) {
		return true
	}
	if level && rsRailLinkPowered(d, world.Pos{X: x, Y: y - 1, Z: z}, blk, fwd, dist, axis) {
		return true
	}
	return false
}

// rsRailLinkPowered is isSameRailWithPower: the link must be the same
// rail block on a compatible axis and already POWERED, then either
// carry a direct signal or keep walking.
func rsRailLinkPowered(d *dimension, pos world.Pos, src world.Block, fwd bool, dist int, axis string) bool {
	st := d.w.GetBlock(pos)
	owner, ok := world.StateOwner(st)
	sOwner, ok2 := world.StateOwner(src)
	if !ok || !ok2 || owner != sOwner {
		return false
	}
	link := rsPropsOf(st)("shape")
	switch axis {
	case "east_west":
		if link == "north_south" || link == "ascending_north" || link == "ascending_south" {
			return false
		}
	case "north_south":
		if link == "east_west" || link == "ascending_east" || link == "ascending_west" {
			return false
		}
	}
	if rsPropsOf(st)("powered") != "true" {
		return false
	}
	return rsHasNeighborSignal(d, pos) || rsRailChainStep(d, pos, st, fwd, dist+1)
}

// rsTorchToggle is RedstoneTorchBlock$Toggle — a lit→dark flip's
// timestamp for the burnout window.
type rsTorchToggle struct {
	pos world.Pos
	at  int64
}

const (
	rsTorchToggleWindow = int64(60) // RECENT_TOGGLE_TIMER
	rsTorchToggleMax    = 8         // MAX_RECENT_TOGGLES
)

// rsTorchPruneToggles drops toggles older than the 60-tick window —
// vanilla prunes from the head of the shared list at the top of every
// torch tick.
func (s *Server) rsTorchPruneToggles(d *dimension) {
	now := s.TickCount()
	l := d.rsTorchToggles
	for len(l) > 0 && now-l[0].at > rsTorchToggleWindow {
		l = l[1:]
	}
	d.rsTorchToggles = l
}

// rsTorchTooOften is isToggledTooFrequently: optionally record this
// pos's toggle, then count the list's entries at it — true at the
// 8-toggle cap.
func (s *Server) rsTorchTooOften(d *dimension, pos world.Pos, add bool) bool {
	if add {
		d.rsTorchToggles = append(d.rsTorchToggles, rsTorchToggle{pos: pos, at: s.TickCount()})
	}
	n := 0
	for _, t := range d.rsTorchToggles {
		if t.pos == pos {
			n++
			if n >= rsTorchToggleMax {
				return true
			}
		}
	}
	return false
}
