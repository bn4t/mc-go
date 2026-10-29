package server

import (
	"strings"
	"sync"

	"mc-go/internal/world"
)

// placeItem is a block item's measured placement facts
// (tools/genplacement).
type placeItem struct {
	class  string // item class chain, most specific first
	block  string
	wall   string // StandingAndWallBlockItem's wall block
	attach string // and its attachment direction
}

func (p placeItem) is(class string) bool {
	for _, c := range strings.Split(p.class, ",") {
		if c == class {
			return true
		}
	}
	return false
}

// placeSound is a block's SoundType place sound.
type placeSound struct {
	event         string
	volume, pitch float32
}

// noteInstrument is a block's NoteBlockInstrument.
type noteInstrument struct {
	name  string
	above bool // worksAboveNoteBlock
}

var (
	placeItemIndexOnce sync.Once
	placeItemByBlock   map[string]string
)

func buildPlaceItemIndex() {
	placeItemByBlock = map[string]string{}
	for item, pi := range placeItems {
		if cur, ok := placeItemByBlock[pi.block]; !ok || item == pi.block || (cur != pi.block && item < cur) {
			placeItemByBlock[pi.block] = item
		}
		if pi.wall != "" {
			placeItemByBlock[pi.wall] = item
		}
	}
}

// placementState is BlockItem.getPlacementState with the item class
// overrides; false fails the place.
func (s *Server) placementState(c *placeCtx, pi placeItem) (world.Block, bool) {
	if pi.is("GameMasterBlockItem") && !(c.sess.mode() == GameModeCreative && c.sess.opLevel() >= 2) {
		// Player.canUseGameMasterBlocks: creative with permission 2.
		return 0, false
	}
	if pi.is("StandingAndWallBlockItem") {
		return s.standingOrWallState(c, pi)
	}
	st, ok := c.stateForPlacement(pi.block)
	if !ok {
		return 0, false
	}
	// BlockItem.canPlace: mustSurvive (all but scaffolding) and an
	// unobstructed collision shape.
	if !pi.is("ScaffoldingBlockItem") && !c.canSurvive(st, c.pos) {
		return 0, false
	}
	if !s.placeUnobstructed(c.d, st, c.pos) {
		return 0, false
	}
	return st, true
}

// standingOrWallState is StandingAndWallBlockItem.getPlacementState:
// over the looking directions (skipping the attachment's opposite),
// the standing block for the attachment direction, else the wall
// block, first one that the item can place (canSurvive; hanging signs
// also WallHangingSignBlock.canPlace), unobstructed for an empty
// collision context.
func (s *Server) standingOrWallState(c *placeCtx, pi placeItem) (world.Block, bool) {
	attach, _ := pdirFromName(pi.attach)
	wall, wallOK := c.stateForPlacement(pi.wall)
	var chosen world.Block
	found := false
	for _, dir := range c.nearestLookingDirs() {
		if dir == attach.opp() {
			continue
		}
		st, ok := wall, wallOK
		if dir == attach {
			st, ok = c.stateForPlacement(pi.block)
		}
		if !ok || !c.canSurvive(st, c.pos) {
			continue
		}
		if pi.is("HangingSignItem") && hasClassName(bstate(st), "WallHangingSignBlock") && !wallHangingCanPlace(c, st) {
			continue
		}
		chosen, found = st, true
		break
	}
	if !found || !s.placeUnobstructed(c.d, chosen, c.pos) {
		return 0, false
	}
	return chosen, true
}

// placeUnobstructed is Level.isUnobstructed(state, pos, context): no
// building-blocking entity (players and living entities, boats,
// minecarts, end crystals, falling blocks, primed TNT) intersects the
// state's collision shape at pos.
func (s *Server) placeUnobstructed(d *dimension, st world.Block, pos world.Pos) bool {
	boxes := s.playerWorldContextShapeAt(d, st, pos, playerShapeContext{placement: true})
	if len(boxes) == 0 {
		return true
	}
	hits := func(minX, minY, minZ, maxX, maxY, maxZ float64) bool {
		for _, b := range boxes {
			if maxX > float64(pos.X)+b[0] && minX < float64(pos.X)+b[3] &&
				maxY > float64(pos.Y)+b[1] && minY < float64(pos.Y)+b[4] &&
				maxZ > float64(pos.Z)+b[2] && minZ < float64(pos.Z)+b[5] {
				return true
			}
		}
		return false
	}
	for _, p := range s.activePlayersOwned() {
		if p.physicalDimName() != d.name || p.mode() == GameModeSpectator {
			continue
		}
		if sess := p.java; sess != nil && (sess.state.Load() != StatePlay || !sess.spawned.Load()) {
			continue
		}
		position := p.position()
		x, y, z := position.X, position.Y, position.Z
		half, height := s.playerCollisionDims(p)
		if hits(x-half, y, z-half, x+half, y+height, z+half) {
			return false
		}
	}
	playerType := mustEntityType("player")
	for _, e := range s.entities.All() {
		if e.Dim != d.name || e.Type == playerType {
			continue
		}
		name, _ := EntityTypeName(e.Type)
		name = strings.TrimPrefix(name, "minecraft:")
		// ArmorStand marker mode clears blocksBuilding independently of its box.
		if name == "armor_stand" && e.ArmorStandMarker {
			continue
		}
		if _, living := entityAttrBases[name]; !living && !blocksBuildingType(name) {
			continue
		}
		dims := entityBBDims(&e)
		hw := dims[0] / 2
		if hits(e.X-hw, e.Y, e.Z-hw, e.X+hw, e.Y+dims[1], e.Z+hw) {
			return false
		}
	}
	return true
}

// blocksBuildingType names the non-living entity types that set
// blocksBuilding.
func blocksBuildingType(name string) bool {
	switch {
	case strings.HasSuffix(name, "_boat"), strings.HasSuffix(name, "_raft"), strings.HasSuffix(name, "minecart"):
		return true
	}
	switch name {
	case "end_crystal", "falling_block", "tnt":
		return true
	}
	return false
}

// scaffoldPlaceContext is ScaffoldingBlockItem.updatePlacementContext:
// clicking scaffolding extends it - sneaking toward the clicked face
// (its opposite when inside), else up, or along the facing when the
// top was clicked - over at most seven horizontal scaffolding cells to
// the first replaceable one; nil fails the place. Outside scaffolding
// a position whose distance is 7 fails.
func (s *Server) scaffoldPlaceContext(c *placeCtx) *placeCtx {
	if bstate(c.at(c.pos)) != "scaffolding" {
		if s.scaffoldDistance(c.d, c.pos) == 7 {
			return nil
		}
		return c
	}
	var dir pdir
	if c.sneak {
		dir = c.face
		if c.inside {
			dir = c.face.opp()
		}
	} else if c.face == pUp {
		dir = c.horizontal()
	} else {
		dir = pUp
	}
	p := dir.rel(c.pos)
	for i := 0; i < 7; {
		if p.Y < c.d.minY || p.Y > c.d.minY+c.d.height-1 {
			if p.Y > c.d.minY+c.d.height-1 {
				s.sendBuildTooHigh(c.sess, c.d)
			}
			return nil
		}
		st := c.at(p)
		if bstate(st) != "scaffolding" {
			if !c.canBeReplaced(st) {
				return nil
			}
			// BlockPlaceContext.at(p, dir): the hit moves to p, face dir,
			// replacing p in place.
			n := *c
			n.hit, n.face, n.pos, n.replaceClicked, n.inside = p, dir, p, true, false
			sx, sy, sz := dir.step()
			n.loc = [3]float64{float64(p.X) + 0.5 + float64(sx)*0.5, float64(p.Y) + 0.5 + float64(sy)*0.5, float64(p.Z) + 0.5 + float64(sz)*0.5}
			n.replaceClicked = n.canBeReplaced(n.at(p))
			if !n.replaceClicked {
				n.pos = dir.rel(p)
			}
			return &n
		}
		p = dir.rel(p)
		if dir.horizontal() {
			i++
		}
	}
	return nil
}

// placedByOtherHalf runs the setPlacedBy overrides that write a second
// cell: DoorBlock (the upper half, flags 3), DoublePlantBlock (its
// default upper half, waterlogged from the cell), SmallDripleafBlock
// (keeping the facing), BedBlock (the head toward its facing).
// PitcherCropBlock places nothing.
func (s *Server) placedByOtherHalf(sess *Session, d *dimension, pos world.Pos, st world.Block) {
	name := bstate(st)
	cls := blockClassOf(name)
	if len(cls) == 0 {
		return
	}
	above := posAbove(pos, 1)
	switch {
	case cls[0] == "StructureBlock":
		if g := s.structureBlockAt(d, pos); g != nil {
			g.fields["author"] = sess.getName()
		}
	case cls[0] == "PitcherCropBlock":
	case cls[0] == "SmallDripleafBlock":
		up, _ := world.BlockState(name)
		up = with(up, "half", "upper")
		f, _ := world.StateProperty(st, "facing")
		up = copyWaterlogged(d, above, with(up, "facing", f))
		s.setBlockAndBroadcast(sess.getName(), above, up)
	case hasClassName(name, "DoorBlock"):
		s.setBlockAndBroadcast(sess.getName(), above, with(st, "half", "upper"))
	case hasClassName(name, "DoublePlantBlock"):
		up, _ := world.BlockState(name)
		up = copyWaterlogged(d, above, with(up, "half", "upper"))
		s.setBlockAndBroadcast(sess.getName(), above, up)
	case hasClassName(name, "BedBlock"):
		f, _ := world.StateProperty(st, "facing")
		fd, _ := pdirFromName(f)
		s.setBlockAndBroadcast(sess.getName(), fd.rel(pos), with(st, "part", "head"))
	}
}
