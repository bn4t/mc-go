package server

import (
	"strings"

	"mc-go/internal/world"
)

// PistonBaseBlock + PistonStructureResolver parity. Vanilla defers the
// move itself to a blockEvent drained at the tail of the level tick
// (ServerLevel.tickBlockEvents), and every moved cell sits as a
// moving_piston for a 2-tick interlude before landing. mc-go
// collapses the interlude: final states land when the event fires.
// The settle state — all a tick-granularity probe can see — is
// identical; the transient moving_piston cells, entity pushing, and
// the block_event animation packet are deferred.

const (
	rsEvExtend   int32 = 0 // TRIGGER_EXTEND
	rsEvContract int32 = 1 // TRIGGER_CONTRACT
	rsEvDrop     int32 = 2 // TRIGGER_DROP — head dropped mid-flight
)

// rsBlockEvent is one queued Level.blockEvent; drained end-of-tick.
type rsBlockEvent struct {
	dim string
	pos world.Pos
	id  int32
}

func rsIsPiston(blk world.Block) bool {
	name, ok := world.StateOwner(blk)
	if !ok {
		return false
	}
	short := strings.TrimPrefix(name, "minecraft:")
	return short == "piston" || short == "sticky_piston"
}

func rsPistonSticky(blk world.Block) bool {
	name, _ := world.StateOwner(blk)
	return strings.TrimPrefix(name, "minecraft:") == "sticky_piston"
}

// rsPistonSignal is PistonBaseBlock.getNeighborSignal — the
// quasi-connectivity read: every face of the piston's own cell except
// the output face, strong power pushed into the piston cell itself,
// then every face of the cell above except DOWN.
func rsPistonSignal(d *dimension, pos world.Pos, facing rsDir) bool {
	for i := range rsOff {
		dir := rsDir(i)
		if dir == facing {
			continue
		}
		if rsSignalAt(d, rsShiftPos(pos, dir), dir) > 0 {
			return true
		}
	}
	if rsSignalAt(d, pos, rsDown) > 0 {
		return true
	}
	above := rsShiftPos(pos, rsUp)
	for i := range rsOff {
		dir := rsDir(i)
		if dir == rsDown {
			continue
		}
		if rsSignalAt(d, rsShiftPos(above, dir), dir) > 0 {
			return true
		}
	}
	return false
}

// rsPistonCheck is checkIfExtend: queue the extend/contract block
// event after the resolver agrees (extends only queue when resolve
// passes — the trigger re-checks power at fire time).
func (s *Server) rsPistonCheck(d *dimension, pos world.Pos) {
	blk := d.w.GetBlock(pos)
	if !rsIsPiston(blk) {
		return
	}
	facing, ok := rsDiodeFacing(blk)
	if !ok {
		return
	}
	sig := rsPistonSignal(d, pos, facing)
	if sig && rsPropsOf(blk)("extended") != "true" {
		if _, _, ok := rsPistonResolve(d, pos, facing, true); ok {
			s.blockEvents = append(s.blockEvents, rsBlockEvent{d.name, pos, rsEvExtend})
		}
		return
	}
	if !sig && rsPropsOf(blk)("extended") == "true" {
		// The TRIGGER_DROP upgrade (a still-extending moving piston
		// two cells out) has no moving pistons to find — a retract is
		// always a full contract here.
		s.blockEvents = append(s.blockEvents, rsBlockEvent{d.name, pos, rsEvContract})
	}
}

// blockEventStep drains the block-event queue: vanilla's
// ServerLevel.tickBlockEvents runs after the scheduled block ticks.
// Events queued mid-drain still fire this tick (poll loop).
func (s *Server) blockEventStep() {
	for len(s.blockEvents) > 0 {
		ev := s.blockEvents[0]
		s.blockEvents = s.blockEvents[1:]
		d := s.dims[ev.dim]
		if d == nil {
			continue
		}
		s.rsPistonTrigger(d, ev.pos, ev.id)
	}
	s.blockEvents = s.blockEvents[:0]
}

// rsPistonTrigger is PistonBaseBlock.triggerEvent: the power state is
// re-read at fire time, so a stale event under changed power is a
// no-op (or a plain re-latch).
func (s *Server) rsPistonTrigger(d *dimension, pos world.Pos, id int32) {
	blk := d.w.GetBlock(pos)
	if !rsIsPiston(blk) {
		return
	}
	facing, ok := rsDiodeFacing(blk)
	if !ok {
		return
	}
	powered := rsPistonSignal(d, pos, facing)
	if powered && id != rsEvExtend {
		if nb, ok := world.WithProperty(blk, "extended", "true"); ok && nb != blk {
			s.rsWriteBlockFlag2(d, pos, nb)
		}
		return
	}
	if !powered && id == rsEvExtend {
		return
	}
	if id == rsEvExtend {
		if s.rsPistonExtend(d, pos, blk, facing) {
			s.explodeBlockSound(d, pos, "piston.extend", 0.5, 0.7)
		}
		return
	}
	s.rsPistonRetract(d, pos, blk, facing)
	s.explodeBlockSound(d, pos, "piston.contract", 0.5, 0.7)
}

// piston push reactions — PushReaction constants relevant to the
// pushability ladder.
const (
	rsReactNormal = iota
	rsReactDestroy
	rsReactBlock
	rsReactPushOnly
)

// rsPistonReaction is BlockBehaviour.pistonPushReaction by name. The
// vanilla default is NORMAL; the sets below cover the named and
// suffixed families.
func rsPistonReaction(name string) int {
	if rsReactDestroySet[name] {
		return rsReactDestroy
	}
	for _, suf := range rsReactDestroySuffix {
		if strings.HasSuffix(name, suf) {
			return rsReactDestroy
		}
	}
	if rsCoralPlant(name) {
		return rsReactDestroy
	}
	if rsReactBlockSet[name] {
		return rsReactBlock
	}
	if strings.HasSuffix(name, "_glazed_terracotta") {
		return rsReactPushOnly
	}
	return rsReactNormal
}

// rsPistonResolve is PistonStructureResolver.resolve: it fills toPush
// (chain order: from nearest the piston outward) and toDestroy, and
// answers whether the move may proceed.
func rsPistonResolve(d *dimension, pos world.Pos, facing rsDir, extending bool) (toPush, toDestroy []world.Pos, ok bool) {
	pushDir := facing
	var start world.Pos
	if extending {
		start = rsShiftPos(pos, facing)
	} else {
		pushDir = rsOpposite(facing)
		start = rsShiftPos(rsShiftPos(pos, facing), facing)
	}
	st := d.w.GetBlock(start)
	if !rsPistonPushableAt(d, st, start, pushDir, false, facing) {
		short, _ := blockShort(st)
		if extending && rsPistonReaction(short) == rsReactDestroy {
			return nil, []world.Pos{start}, true
		}
		return nil, nil, false
	}
	if !rsPistonAddLine(d, pos, start, pushDir, pushDir, &toPush, &toDestroy) {
		return nil, nil, false
	}
	for i := 0; i < len(toPush); i++ {
		if rsSticky(d.w.GetBlock(toPush[i])) {
			if !rsPistonBranch(d, pos, toPush[i], pushDir, &toPush, &toDestroy) {
				return nil, nil, false
			}
		}
	}
	return toPush, toDestroy, true
}

// rsPistonAddLine is addBlockLine: the chain always moves along
// pushDir — dir2 is only the applied-along gate for PUSH_ONLY cells
// (a side branch's entry dir). Back-scan sticky blocks riding behind
// pos, then forward-scan push targets; collisions reorder so the
// visited segment stays coherent.
func rsPistonAddLine(d *dimension, pistonPos, pos world.Pos, pushDir, dir2 rsDir, toPush, toDestroy *[]world.Pos) bool {
	st := d.w.GetBlock(pos)
	if st == world.Air {
		return true
	}
	if !rsPistonPushableAt(d, st, pos, pushDir, false, dir2) {
		return true
	}
	if pos == pistonPos {
		return true
	}
	if containsPos(*toPush, pos) {
		return true
	}
	i := 1
	// Back-scan: sticky blocks already behind pos along -pushDir ride
	// along, as long as each successive cell is itself sticky and can
	// stick to its predecessor.
	for rsSticky(st) {
		next := world.Pos{X: pos.X - rsOff[pushDir].X*i, Y: pos.Y - rsOff[pushDir].Y*i, Z: pos.Z - rsOff[pushDir].Z*i}
		prev := st
		st = d.w.GetBlock(next)
		if st == world.Air || !rsCanStick(prev, st) ||
			!rsPistonPushableAt(d, st, next, pushDir, false, rsOpposite(pushDir)) ||
			next == pistonPos {
			break
		}
		i++
		if i+len(*toPush) > 12 {
			return false
		}
	}
	// The back-scanned cells join farthest-first so toPush stays
	// ordered from the chain's far end toward the piston.
	for n := i - 1; n >= 0; n-- {
		*toPush = append(*toPush, world.Pos{X: pos.X - rsOff[pushDir].X*n, Y: pos.Y - rsOff[pushDir].Y*n, Z: pos.Z - rsOff[pushDir].Z*n})
	}
	step := 1
	for {
		next := rsShiftPosN(pos, pushDir, step)
		idx := indexPos(*toPush, next)
		if idx >= 0 {
			// Collision with an already-collected cell: split the
			// list at the collision and re-scan sticky branches.
			reorderPosList(toPush, i, idx)
			for j := 0; j <= idx+i; j++ {
				if rsSticky(d.w.GetBlock((*toPush)[j])) && !rsPistonBranch(d, pistonPos, (*toPush)[j], pushDir, toPush, toDestroy) {
					return false
				}
			}
			return true
		}
		st = d.w.GetBlock(next)
		if st == world.Air {
			return true
		}
		if !rsPistonPushableAt(d, st, next, pushDir, true, pushDir) || next == pistonPos {
			return false
		}
		short, _ := blockShort(st)
		if rsPistonReaction(short) == rsReactDestroy {
			*toDestroy = append(*toDestroy, next)
			return true
		}
		if len(*toPush) >= 12 {
			return false
		}
		*toPush = append(*toPush, next)
		step++
	}
}

// rsPistonBranch is addBranchingBlocks: sticky blocks pull the cells
// beside them (any axis but the push axis) into the chain.
func rsPistonBranch(d *dimension, pistonPos, pos world.Pos, pushDir rsDir, toPush, toDestroy *[]world.Pos) bool {
	st := d.w.GetBlock(pos)
	pushAxis := rsAxisOf(pushDir)
	for i := range rsOff {
		dir := rsDir(i)
		if rsAxisOf(dir) == pushAxis {
			continue
		}
		side := rsShiftPos(pos, dir)
		sideSt := d.w.GetBlock(side)
		if !rsCanStick(sideSt, st) {
			continue
		}
		if !rsPistonAddLine(d, pistonPos, side, pushDir, dir, toPush, toDestroy) {
			return false
		}
	}
	return true
}

// reorderPosList is reorderListAtCollision: rotates the tail of the
// collected chain so the segment between n1 and idx moves to the end.
func reorderPosList(list *[]world.Pos, n1, idx int) {
	l := *list
	head := append([]world.Pos(nil), l[:idx]...)
	mid := append([]world.Pos(nil), l[len(l)-n1:]...)
	tail := append([]world.Pos(nil), l[idx:len(l)-n1]...)
	*list = append(append(head, mid...), tail...)
}

func containsPos(l []world.Pos, p world.Pos) bool { return indexPos(l, p) >= 0 }

func indexPos(l []world.Pos, p world.Pos) int {
	for i, q := range l {
		if q == p {
			return i
		}
	}
	return -1
}

func rsShiftPosN(pos world.Pos, dir rsDir, n int) world.Pos {
	o := rsOff[dir]
	return world.Pos{X: pos.X + o.X*n, Y: pos.Y + o.Y*n, Z: pos.Z + o.Z*n}
}

func rsAxisOf(dir rsDir) int {
	switch dir {
	case rsDown, rsUp:
		return 1
	case rsNorth, rsSouth:
		return 2
	}
	return 0
}

func rsSticky(blk world.Block) bool {
	name, _ := world.StateOwner(blk)
	short := strings.TrimPrefix(name, "minecraft:")
	return short == "slime_block" || short == "honey_block"
}

// rsCanStick is canStickToEachOther: slime and honey grab anything
// but each other; everything else only sticks to a sticky block.
func rsCanStick(a, b world.Block) bool {
	an, _ := world.StateOwner(a)
	bn, _ := world.StateOwner(b)
	as := strings.TrimPrefix(an, "minecraft:")
	bs := strings.TrimPrefix(bn, "minecraft:")
	if as == "honey_block" && bs == "slime_block" {
		return false
	}
	if as == "slime_block" && bs == "honey_block" {
		return false
	}
	return rsStickyName(as) || rsStickyName(bs)
}

func rsStickyName(name string) bool {
	return name == "slime_block" || name == "honey_block"
}

// rsPistonPushableAt is isPushable on a pre-read state (the resolver
// already holds the state at the cell).
func rsPistonPushableAt(d *dimension, st world.Block, pos world.Pos, dir rsDir, ignoreDestroy bool, dir2 rsDir) bool {
	if pos.Y < d.minY || pos.Y > d.minY+d.height-1 {
		return false
	}
	if st == world.Air {
		return true
	}
	name, ok := world.StateOwner(st)
	if !ok {
		return false
	}
	short := strings.TrimPrefix(name, "minecraft:")
	if rsPistonImmovable[short] {
		return false
	}
	if dir == rsDown && pos.Y == d.minY {
		return false
	}
	if dir == rsUp && pos.Y == d.minY+d.height-1 {
		return false
	}
	if (short == "piston" || short == "sticky_piston") && rsPropsOf(st)("extended") == "true" {
		return false
	}
	if rsPistonUnbreakable[short] {
		return false
	}
	switch rsPistonReaction(short) {
	case rsReactBlock:
		return false
	case rsReactDestroy:
		return ignoreDestroy
	case rsReactPushOnly:
		return dir == dir2
	}
	return !rsBlockHasBE(short)
}

func blockShort(blk world.Block) (string, bool) {
	name, ok := world.StateOwner(blk)
	if !ok {
		return "", false
	}
	return strings.TrimPrefix(name, "minecraft:"), true
}

// rsPistonExtend is moveBlocks(extend=true): snapshot states, pop the
// DESTROY cells, land each pushed block one step out, write the head
// and latch EXTENDED — then run the neighbour passes over every cell
// the write touched.
func (s *Server) rsPistonExtend(d *dimension, pos world.Pos, blk world.Block, facing rsDir) bool {
	toPush, toDestroy, ok := rsPistonResolve(d, pos, facing, true)
	if !ok {
		return false
	}
	front := rsShiftPos(pos, facing)
	states := map[world.Pos]world.Block{}
	cells := map[world.Pos]world.Block{} // pos -> pre-move state (for shape/update passes)
	for _, p := range toPush {
		st := d.w.GetBlock(p)
		states[p] = st
		cells[p] = st
	}
	for _, p := range toDestroy {
		cells[p] = d.w.GetBlock(p)
	}
	cells[front] = d.w.GetBlock(front)
	for i := len(toDestroy) - 1; i >= 0; i-- {
		p := toDestroy[i]
		s.broadcastLevelEvent(d.name, 2001, p, int32(cells[p]))
		d.w.SetBlock(p, world.Air)
	}
	dst := map[world.Pos]world.Block{}
	for _, p := range toPush {
		dst[rsShiftPos(p, facing)] = states[p]
	}
	for np, st := range dst {
		d.w.SetBlock(np, st)
	}
	for _, p := range toPush {
		if _, covered := dst[p]; !covered {
			d.w.SetBlock(p, world.Air)
		}
	}
	typ := "normal"
	if rsPistonSticky(blk) {
		typ = "sticky"
	}
	if head, ok := world.BlockStateOf("piston_head", map[string]string{
		"facing": rsDirName(facing), "type": typ, "short": "false",
	}); ok {
		d.w.SetBlock(front, head)
	}
	if nb, ok := world.WithProperty(blk, "extended", "true"); ok {
		d.w.SetBlock(pos, nb)
	}
	// updateNeighborsAt over the same cells vanilla touches:
	// destroyed cells, moved origins, the head cell, the piston.
	for i := len(toDestroy) - 1; i >= 0; i-- {
		s.rsNeighborFanOut(d, toDestroy[i])
	}
	for i := len(toPush) - 1; i >= 0; i-- {
		p := toPush[i]
		s.rsNeighborFanOut(d, p)
	}
	for np, st := range dst {
		s.broadcastBlockState(d, np, st)
		s.selfFoldShapes(d, np)
		s.neighborSurvival(d, np, st)
		s.rsNeighborFanOut(d, np)
	}
	for _, p := range toDestroy {
		s.broadcastBlockState(d, p, world.Air)
		s.neighborSurvival(d, p, cells[p])
	}
	for _, p := range toPush {
		s.broadcastBlockState(d, p, d.w.GetBlock(p))
	}
	s.broadcastBlockState(d, front, d.w.GetBlock(front))
	s.broadcastBlockState(d, pos, d.w.GetBlock(pos))
	s.rsNeighborFanOut(d, front)
	s.rsNeighborFanOut(d, pos)
	return true
}

// rsPistonRetract is moveBlocks(extend=false) plus the piston-cell
// cleanup: EXTENDED clears; a sticky piston whose resolver pulls a
// chain lands it toward the head cell, otherwise the head cell goes
// air (vanilla's removeBlock of the front cell).
func (s *Server) rsPistonRetract(d *dimension, pos world.Pos, blk world.Block, facing rsDir) {
	front := rsShiftPos(pos, facing)
	pulled := false
	var touched []world.Pos
	// moveBlocks(extend=false) clears the stale head cell before the
	// resolver runs — the pull would otherwise hit it as BLOCK.
	if short, _ := blockShort(d.w.GetBlock(front)); short == "piston_head" {
		d.w.SetBlock(front, world.Air)
		touched = append(touched, front)
	}
	if rsPistonSticky(blk) {
		cell2 := rsShiftPos(front, facing)
		st2 := d.w.GetBlock(cell2)
		if st2 != world.Air && rsPistonPushableAt(d, st2, cell2, rsOpposite(facing), false, facing) {
			short, _ := blockShort(st2)
			if rsPistonReaction(short) == rsReactNormal || short == "piston" || short == "sticky_piston" {
				toPush, _, ok := rsPistonResolve(d, pos, facing, false)
				if ok && len(toPush) > 0 {
					pulled = true
					states := map[world.Pos]world.Block{}
					for _, p := range toPush {
						states[p] = d.w.GetBlock(p)
					}
					dst := map[world.Pos]world.Block{}
					// Push dir is -facing: cells land one step toward
					// the piston.
					for _, p := range toPush {
						dst[rsShiftPos(p, rsOpposite(facing))] = states[p]
					}
					for np, st := range dst {
						d.w.SetBlock(np, st)
						touched = append(touched, np)
					}
					for _, p := range toPush {
						if _, covered := dst[p]; !covered {
							d.w.SetBlock(p, world.Air)
						}
						touched = append(touched, p)
					}
				}
			}
		}
	}
	if !pulled && d.w.GetBlock(front) != world.Air {
		// removeBlock(front) when no pull landed — clears whatever is
		// there (normally already air after the head clear).
		old := d.w.GetBlock(front)
		d.w.SetBlock(front, world.Air)
		if _, ok := world.StateOwner(old); ok {
			s.broadcastLevelEvent(d.name, 2001, front, int32(old))
		}
		touched = append(touched, front)
	}
	if nb, ok := world.WithProperty(blk, "extended", "false"); ok {
		d.w.SetBlock(pos, nb)
	}
	touched = append(touched, pos)
	for _, p := range touched {
		s.broadcastBlockState(d, p, d.w.GetBlock(p))
		s.neighborSurvival(d, p, d.w.GetBlock(p))
		s.rsNeighborFanOut(d, p)
	}
}

// rsPistonImmovable is the isPushable hard-reject set — obsidian
// family plus reinforced deepslate and respawn anchors.
var rsPistonImmovable = map[string]bool{
	"obsidian":             true,
	"crying_obsidian":      true,
	"respawn_anchor":       true,
	"reinforced_deepslate": true,
}

// rsPistonUnbreakable covers getDestroySpeed == -1 — unbreakable
// blocks fail isPushable even before their push reaction reads.
var rsPistonUnbreakable = map[string]bool{
	"bedrock":                 true,
	"barrier":                 true,
	"command_block":           true,
	"chain_command_block":     true,
	"repeating_command_block": true,
	"structure_block":         true,
	"jigsaw_block":            true,
	"light":                   true,
	"end_portal_frame":        true,
	"end_portal":              true,
	"end_gateway":             true,
	"moving_piston":           true,
	"test_block":              true,
	"test_instance_block":     true,
}

// rsReactBlockSet is PushReaction.BLOCK — movable-in-name but the
// reaction vetoes any move at all.
var rsReactBlockSet = map[string]bool{
	"piston_head":          true,
	"moving_piston":        true,
	"nether_portal":        true,
	"end_portal":           true,
	"end_gateway":          true,
	"structure_void":       true,
	"spawner":              true,
	"trial_spawner":        true,
	"vault":                true,
	"creaking_heart":       true,
	"reinforced_deepslate": true,
}

// rsReactDestroySet is PushReaction.DESTROY by name — the piston pops
// these in place instead of pushing them.
var rsReactDestroySet = map[string]bool{
	// Redstone components and attachments.
	"redstone_wire": true, "repeater": true, "comparator": true,
	"tripwire": true, "tripwire_hook": true, "lever": true,
	"rail": true, "powered_rail": true, "detector_rail": true,
	"activator_rail": true,
	"torch":          true, "soul_torch": true, "redstone_torch": true,
	// Plants and small decorations.
	"short_grass": true, "tall_grass": true, "fern": true,
	"large_fern": true, "dead_bush": true, "seagrass": true,
	"tall_seagrass": true, "nether_sprouts": true,
	"hanging_roots": true, "spore_blossom": true,
	"small_dripleaf": true, "big_dripleaf": true, "big_dripleaf_stem": true,
	"azalea": true, "flowering_azalea": true,
	"brown_mushroom": true, "red_mushroom": true,
	"crimson_fungus": true, "warped_fungus": true,
	"crimson_roots": true, "warped_roots": true,
	"dandelion": true, "poppy": true, "blue_orchid": true,
	"allium": true, "azure_bluet": true, "oxeye_daisy": true,
	"cornflower": true, "lily_of_the_valley": true,
	"wither_rose": true, "torchflower": true, "sunflower": true,
	"lilac": true, "peony": true, "rose_bush": true,
	"pitcher_plant": true, "pink_petals": true, "wildflowers": true,
	"leaf_litter": true, "open_eyeblossom": true, "closed_eyeblossom": true,
	"short_dry_grass": true, "tall_dry_grass": true,
	"firefly_bush": true, "bush": true,
	// Crops and climbing plants.
	"wheat": true, "carrots": true, "potatoes": true, "beetroots": true,
	"melon_stem": true, "attached_melon_stem": true,
	"pumpkin_stem": true, "attached_pumpkin_stem": true,
	"nether_wart": true, "torchflower_crop": true, "pitcher_crop": true,
	"sweet_berry_bush": true, "cocoa": true,
	"sugar_cane": true, "bamboo": true, "bamboo_sapling": true,
	"cactus": true, "chorus_plant": true, "chorus_flower": true,
	"kelp": true, "kelp_plant": true,
	"sea_pickle": true, "lily_pad": true, "glow_lichen": true,
	"sculk_vein": true, "vine": true,
	// Fragile placed blocks.
	"snow": true, "powder_snow": true,
	"cake": true, "lantern": true, "soul_lantern": true,
	"ladder": true, "pointed_dripstone": true,
	"fire": true, "soul_fire": true,
	"cobweb": true, "turtle_egg": true, "sniffer_egg": true,
	"dragon_egg": true, "frogspawn": true,
	"water": true, "lava": true, "bubble_column": true,
	"amethyst_cluster": true,
}

// rsReactDestroySuffix covers the families expressible by name
// suffix. "_coral"/"_coral_fan" handle living corals; coral blocks
// ("_coral_block") are solid and stay pushable.
var rsReactDestroySuffix = []string{
	"_torch", "_button", "_pressure_plate", "_sapling",
	"_propagule", "_carpet", "_candle", "_tulip", "_eyeblossom",
	"_amethyst_bud", "_vines", "_vines_plant",
	"_coral_fan", "_wall_fan", "_dry_grass",
}

// rsCoralPlant catches the living coral plants without sweeping up
// "_coral_block" (which is a solid NORMAL block).
func rsCoralPlant(name string) bool {
	return strings.HasSuffix(name, "_coral") && !strings.HasSuffix(name, "_coral_block")
}

// rsBlockHasBE is BlockState.hasBlockEntity by name — blocks carrying
// block entities are unpushable. Signs/banners/heads/beds/shulkers and
// the campfire family come in via suffix.
func rsBlockHasBE(name string) bool {
	if rsBEBlocks[name] {
		return true
	}
	for _, suf := range rsBESuffix {
		if strings.HasSuffix(name, suf) {
			return true
		}
	}
	return false
}

var rsBESuffix = []string{
	"_shulker_box", "_sign", "_hanging_sign", "_wall_hanging_sign",
	"_banner", "_skull", "_head", "_wall_head", "_bed", "_campfire",
	"_command_block",
}

var rsBEBlocks = map[string]bool{
	"chest": true, "trapped_chest": true, "ender_chest": true,
	"barrel": true, "furnace": true, "blast_furnace": true,
	"smoker": true, "dispenser": true, "dropper": true,
	"hopper": true, "crafter": true, "brewing_stand": true,
	"lectern": true, "beacon": true, "enchanting_table": true,
	"jukebox": true, "decorated_pot": true, "chiseled_bookshelf": true,
	"conduit": true, "spawner": true, "trial_spawner": true,
	"vault": true, "daylight_detector": true,
	"end_portal": true, "end_gateway": true,
	"creaking_heart": true, "bee_nest": true, "beehive": true,
	"bell": true, "sculk_sensor": true, "calibrated_sculk_sensor": true,
	"sculk_shrieker": true, "sculk_catalyst": true,
	"moving_piston": true, "comparator": true,
	"suspicious_sand": true, "suspicious_gravel": true,
	"shelf": true, "copper_golem_statue": true,
	"copper_chest": true, "exposed_copper_chest": true,
	"weathered_copper_chest": true, "oxidized_copper_chest": true,
	"waxed_copper_chest": true, "waxed_exposed_copper_chest": true,
	"waxed_weathered_copper_chest": true, "waxed_oxidized_copper_chest": true,
}

// rsHeadSupported is PistonHeadBlock.canSurvive minus the
// moving_piston arm (mc-go never writes moving cells): the cell at
// FACING.opposite must be an extended piston of the matching type
// facing the same way.
func rsHeadSupported(d *dimension, pos world.Pos, head world.Block) bool {
	facing, ok := rsDiodeFacing(head)
	if !ok {
		return false
	}
	base := d.w.GetBlock(rsShiftPos(pos, rsOpposite(facing)))
	if !rsIsPiston(base) {
		return false
	}
	bprop := rsPropsOf(base)
	if bprop("extended") != "true" || bprop("facing") != rsDirName(facing) {
		return false
	}
	return rsPistonSticky(base) == (rsPropsOf(head)("type") == "sticky")
}
