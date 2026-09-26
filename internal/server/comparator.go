package server

// ComparatorBlock past the shared DiodeBlock shell in redstone.go: the
// analog path. Its block entity stores the last computed output, side
// inputs read real signals (sideInputDiodesOnly=false), and its
// scheduled tick recomputes rather than flips.
//
// The dimension's comparatorOut map stands in for
// ComparatorBlockEntity.output — the value the diode publishes while
// powered, kept across signal computations until a refresh rewrites
// it (vanilla persists it as the OutputSignal NBT).

import (
	"math"
	"strings"

	"mc-go/internal/world"
)

// rsHasAnalog is BlockBehaviour.hasAnalogOutputSignal for the block
// names mc-go models: any of these written with flag 1 wakes the
// comparators looking at it, even one whose analog read would be 0.
var rsAnalogNames = map[string]bool{
	"barrel": true, "chest": true, "trapped_chest": true,
	"dispenser": true, "dropper": true, "hopper": true,
	"furnace": true, "blast_furnace": true, "smoker": true,
	"brewing_stand": true, "crafter": true, "lectern": true,
	"composter": true, "cake": true, "jukebox": true,
	"respawn_anchor": true, "beehive": true, "bee_nest": true,
	"chiseled_bookshelf": true, "decorated_pot": true,
	"end_portal_frame": true,
	"command_block":    true, "chain_command_block": true,
	"repeating_command_block": true,
	"water_cauldron":          true, "lava_cauldron": true,
	"powder_snow_cauldron": true,
}

func rsHasAnalog(blk world.Block) bool {
	owner, ok := world.StateOwner(blk)
	if !ok {
		return false
	}
	name := strings.TrimPrefix(owner, "minecraft:")
	if name == "shulker_box" || strings.HasSuffix(name, "_shulker_box") {
		return true
	}
	return rsAnalogNames[name]
}

// rsAnalogSignal is BlockState.getAnalogOutputSignal: the comparator
// read of an analog-capable block. Containers use the fill formula;
// the prop-read blocks report their level/bites/charges; blocks mc-go
// does not model (jukebox disc, bookshelf last-hit, pot, command
// blocks) read 0.
func rsAnalogSignal(d *dimension, pos world.Pos, blk world.Block) int {
	owner, ok := world.StateOwner(blk)
	if !ok {
		return 0
	}
	name := strings.TrimPrefix(owner, "minecraft:")
	prop := rsPropsOf(blk)
	switch name {
	case "chest", "trapped_chest":
		d.chests.mu.Lock()
		defer d.chests.mu.Unlock()
		if c := d.chests.chests[pos]; c != nil {
			return rsContainerFill(c.slots[:])
		}
		return 0
	case "hopper":
		d.hoppers.mu.Lock()
		defer d.hoppers.mu.Unlock()
		if h := d.hoppers.hoppers[pos]; h != nil {
			return rsContainerFill(h.slots[:])
		}
		return 0
	case "furnace", "blast_furnace", "smoker":
		d.furnaces.mu.Lock()
		defer d.furnaces.mu.Unlock()
		if f := d.furnaces.furnaces[pos]; f != nil {
			return rsContainerFill(f.slots[:])
		}
		return 0
	case "crafter":
		// CrafterBlockEntity.getRedstoneSignal: the count of slots
		// that are occupied OR disabled — not the fill fraction.
		d.containers.mu.Lock()
		defer d.containers.mu.Unlock()
		if c := d.containers.containers[pos]; c != nil {
			n := 0
			for i, it := range c.slots {
				if !it.Empty() || c.disabled&(1<<i) != 0 {
					n++
				}
			}
			return n
		}
		return 0
	case "composter":
		return rsPropInt(prop("level"))
	case "cake":
		return (7 - rsPropInt(prop("bites"))) * 2
	case "respawn_anchor":
		// getScaledChargeLevel(charges, 15): floor(charges*15/4).
		return rsPropInt(prop("charges")) * 15 / 4
	case "water_cauldron", "powder_snow_cauldron":
		return rsPropInt(prop("level"))
	case "lava_cauldron":
		return 3
	case "bee_nest", "beehive":
		return rsPropInt(prop("honey_level"))
	case "end_portal_frame":
		if prop("eye") == "true" {
			return 15
		}
		return 0
	case "lectern":
		if prop("has_book") != "true" {
			return 0
		}
		// LecternBlockEntity.getRedstoneSignal: pageCount ≤ 1 reads 1;
		// else floor(page/(pageCount-1)*14)+1 — the page's position in
		// the book, scaled to the comparator's analog band.
		d.containers.mu.Lock()
		defer d.containers.mu.Unlock()
		if c := d.containers.containers[pos]; c != nil {
			pc := lecternPageCount(c.slots[lecternSlot])
			if pc <= 1 {
				return 1
			}
			return int(c.page*14/(pc-1)) + 1
		}
		return 0
	}
	// Generic container blocks (barrel, dispenser, dropper,
	// brewing_stand, shulker boxes, decorated_pot...) share the fill
	// formula. Jukebox, chiseled_bookshelf, and command blocks have
	// no modelled contents here and read 0.
	if _, ok := containerSpecFor(blk); ok {
		d.containers.mu.Lock()
		defer d.containers.mu.Unlock()
		if c := d.containers.containers[pos]; c != nil {
			return rsContainerFill(c.slots)
		}
	}
	return 0
}

// rsContainerFill is AbstractContainerMenu.getRedstoneSignalFromContainer:
// floor(avgFill*14), plus 1 when the container holds anything at all.
// Every vanilla container's own maxStackSize is 64, so each slot's
// share is count/itemMax.
func rsContainerFill(slots []ItemStack) int {
	if len(slots) == 0 {
		return 0
	}
	f := 0.0
	any := false
	for _, it := range slots {
		if it.Empty() {
			continue
		}
		any = true
		mx := maxStackOfID(it.ID)
		if mx < 1 {
			mx = 1
		}
		f += float64(it.Count) / float64(mx)
	}
	f /= float64(len(slots))
	out := int(math.Floor(f * 14))
	if any {
		out++
	}
	return out
}

// rsCompOwn is ComparatorBlock's getSignal core: powered ? the stored
// ComparatorBlockEntity.output : 0.
func rsCompOwn(d *dimension, pos world.Pos, blk world.Block) int {
	if rsPropsOf(blk)("powered") != "true" {
		return 0
	}
	return d.comparatorOut[pos]
}

// rsCompInput is ComparatorBlock.getInputSignal: the plain diode input,
// then the analog read — the input cell's own getAnalogOutputSignal,
// or one cell deeper through a conductor. (Vanilla's item-frame entity
// read is skipped: mc-go has no item frames.)
func rsCompInput(d *dimension, pos world.Pos, facing rsDir) int {
	sig := rsDiodeInput(d, pos, facing)
	in := rsShiftPos(pos, facing)
	ib := d.w.GetBlock(in)
	if rsHasAnalog(ib) {
		return rsAnalogSignal(d, in, ib)
	}
	if sig >= 15 || !rsConductor(ib) {
		return sig
	}
	deep := rsShiftPos(in, facing)
	db := d.w.GetBlock(deep)
	if rsHasAnalog(db) {
		return rsAnalogSignal(d, deep, db)
	}
	return sig
}

// rsCompCalc is ComparatorBlock.calculateOutputSignal: the input minus
// the stronger side signal in SUBTRACT mode, 0 when the side wins.
func rsCompCalc(d *dimension, pos world.Pos, blk world.Block) int {
	facing, ok := rsDiodeFacing(blk)
	if !ok {
		return 0
	}
	in := rsCompInput(d, pos, facing)
	if in == 0 {
		return 0
	}
	alt := rsDiodeAlt(d, pos, facing, false)
	if alt > in {
		return 0
	}
	if rsPropsOf(blk)("mode") == "subtract" {
		return in - alt
	}
	return in
}

// rsCompShouldOn is ComparatorBlock.shouldTurnOn: input>0, input beats
// the sides, or ties them in COMPARE mode.
func rsCompShouldOn(d *dimension, pos world.Pos, blk world.Block) bool {
	facing, ok := rsDiodeFacing(blk)
	if !ok {
		return false
	}
	in := rsCompInput(d, pos, facing)
	if in == 0 {
		return false
	}
	alt := rsDiodeAlt(d, pos, facing, false)
	if in > alt {
		return true
	}
	return in == alt && rsPropsOf(blk)("mode") == "compare"
}

// rsCompCheckTick is ComparatorBlock.checkTickOnNeighbor: nothing if
// the tick is already due, else a recompute is due when the fresh calc
// disagrees with the stored output or powered disagrees with
// shouldTurnOn. Prios are HIGH (prioritized) or NORMAL — not the
// diode's EXTREMELY/VERY_HIGH family.
func (s *Server) rsCompCheckTick(d *dimension, pos world.Pos, blk world.Block) {
	key := schedKey{dim: d.name, pos: pos, kind: tickBlock}
	if at, ok := s.ticks.queuedAt(key); ok && at <= s.TickCount() {
		return
	}
	calc := rsCompCalc(d, pos, blk)
	powered := rsPropsOf(blk)("powered") == "true"
	if calc == d.comparatorOut[pos] && powered == rsCompShouldOn(d, pos, blk) {
		return
	}
	prio := tickPrioNormal
	if rsDiodePrioritizes(d, pos, blk) {
		prio = tickPrioHigh
	}
	s.ticks.schedulePrio(key, s.TickCount()+2, prio)
}

// rsCompRefresh is ComparatorBlock.refreshOutputState — the comparator's
// scheduled tick runs this too: store the fresh calc on the block
// entity, flip POWERED flag-2 if it disagrees with shouldTurnOn, and
// announce the front. An unchanged output in COMPARE mode skips the
// whole tail (vanilla's early return).
func (s *Server) rsCompRefresh(d *dimension, pos world.Pos, blk world.Block) {
	calc := rsCompCalc(d, pos, blk)
	old := d.comparatorOut[pos]
	if d.comparatorOut == nil {
		d.comparatorOut = map[world.Pos]int{}
	}
	d.comparatorOut[pos] = calc
	if old == calc && rsPropsOf(blk)("mode") == "compare" {
		return
	}
	shouldOn := rsCompShouldOn(d, pos, blk)
	powered := rsPropsOf(blk)("powered") == "true"
	switch {
	case powered && !shouldOn:
		if nb, ok := world.WithProperty(blk, "powered", "false"); ok {
			s.rsWriteBlockFlag2(d, pos, nb)
		}
	case !powered && shouldOn:
		if nb, ok := world.WithProperty(blk, "powered", "true"); ok {
			s.rsWriteBlockFlag2(d, pos, nb)
		}
	}
	s.rsDiodeUpdateInFront(d, pos, blk)
}

// rsOutputSignalWake is Level.updateNeighbourForOutputSignal: wakes
// the comparator facing pos on each horizontal axis — directly, or
// one cell deeper through a conductor. Inside a block write the wake
// queues like the neighbour ring; outside one (container setChanged,
// hopper moves) it runs at once. The callers carry vanilla's gates:
// new-state hasAnalogOutputSignal on writes, the removal set on
// destroy, unconditional on container setChanged.
func (s *Server) rsOutputSignalWake(d *dimension, pos world.Pos) {
	for _, dir := range [4]rsDir{rsNorth, rsSouth, rsWest, rsEast} {
		near := rsShiftPos(pos, dir)
		if rsIsComparator(d.w.GetBlock(near)) {
			s.rsNeighborNotify(d, near)
			continue
		}
		if !rsConductor(d.w.GetBlock(near)) {
			continue
		}
		deep := rsShiftPos(near, dir)
		if rsIsComparator(d.w.GetBlock(deep)) {
			s.rsNeighborNotify(d, deep)
		}
	}
}

// rsContainerChanged is BlockEntity.setChanged's redstone side: a
// content write on a container block fires updateNeighbourForOutput-
// Signal with its own block.
func (s *Server) rsContainerChanged(d *dimension, pos world.Pos) {
	s.rsOutputSignalWake(d, pos)
}

// rsRemovalWakes is the block set whose affectNeighborsAfterRemoval
// runs Containers.updateNeighboursAfterDestroy — every container
// block, but not the prop-only analog blocks (composter, cake,
// cauldron, respawn_anchor...), whose removal stays quiet.
var rsRemovalWakeNames = map[string]bool{
	"barrel": true, "chest": true, "trapped_chest": true,
	"dispenser": true, "dropper": true, "hopper": true,
	"furnace": true, "blast_furnace": true, "smoker": true,
	"brewing_stand": true, "crafter": true, "jukebox": true,
	"chiseled_bookshelf": true, "decorated_pot": true,
	"beehive": true, "bee_nest": true,
	"creaking_heart": true, "shelf": true,
}

func rsRemovalWakes(name string) bool {
	if name == "shulker_box" || strings.HasSuffix(name, "_shulker_box") {
		return true
	}
	return rsRemovalWakeNames[name]
}

func rsIsComparator(blk world.Block) bool {
	owner, ok := world.StateOwner(blk)
	return ok && strings.TrimPrefix(owner, "minecraft:") == "comparator"
}
