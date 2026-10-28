package worldgen

import (
	"encoding/base64"
	"encoding/binary"
	"sort"
	"strings"
	"sync"
)

// Block-state facts measured from the game (blockprops_gen.go), joined
// to this package's interned states.

type propLayout struct {
	name   string
	values []string
	stride int
}

type blockLayout struct {
	name         string
	first, count int
	def          int
	props        []propLayout
	// class is the block's class; survive, placeOn and shape the
	// classes that declare its canSurvive, mayPlaceOn and updateShape
	// ("-" for none); tag the support tag it keeps ("-" for none).
	class, survive, placeOn, tag, shape string
}

// State flag bits.
const (
	bsfAir uint64 = 1 << iota
	bsfBlocksMotion
	bsfSolid
	bsfReplaceable
	bsfFluid
	bsfWater
	bsfSource
	bsfLeaves
	bsfSturdyDown
	bsfSturdyUp
	bsfSturdyNorth
	bsfSturdySouth
	bsfSturdyWest
	bsfSturdyEast
	bsfFullBlock
	bsfSolidRender
	bsfCenterDown
	bsfCenterUp
	bsfCenterNorth
	bsfCenterSouth
	bsfCenterWest
	bsfCenterEast
	bsfRigidDown
	bsfRigidUp
	bsfRigidNorth
	bsfRigidSouth
	bsfRigidWest
	bsfRigidEast
	bsfFlammable
	bsfCollisionTopFull
	bsfLiquid
	bsfOccludeDown
	bsfOccludeUp
	bsfOccludeNorth
	bsfOccludeSouth
	bsfOccludeWest
	bsfOccludeEast
	bsfCanOcclude
	bsfCollideDown
	bsfCollideUp
	bsfCollideNorth
	bsfCollideSouth
	bsfCollideWest
	bsfCollideEast
)

// Directions in the game's order.
const (
	dirDown = iota
	dirUp
	dirNorth
	dirSouth
	dirWest
	dirEast
)

var dirOffsets = [6][3]int32{{0, -1, 0}, {0, 1, 0}, {0, 0, -1}, {0, 0, 1}, {-1, 0, 0}, {1, 0, 0}}

var (
	blockPropsOnce sync.Once
	stateFlagWords []uint64
	blockByName    map[string]int
	blockOfID      []int32 // global state id -> layout index

	stateInfoMu sync.RWMutex
	stateInfos  []stateInfo // by StateID
)

// stateInfo is what is known of an interned state: its registry id
// (-1 when the string names no state) and flags.
type stateInfo struct {
	id    int32
	block int32
	flags uint64
	known bool
}

func loadBlockProps() {
	blockPropsOnce.Do(func() {
		raw, err := base64.StdEncoding.DecodeString(stateFlagData)
		if err != nil {
			panic(err)
		}
		stateFlagWords = make([]uint64, stateCount)
		for i := range stateFlagWords {
			stateFlagWords[i] = binary.LittleEndian.Uint64(raw[8*i:])
		}
		blockByName = make(map[string]int, len(blockLayouts))
		blockOfID = make([]int32, stateCount)
		for i, l := range blockLayouts {
			blockByName[l.name] = i
			for j := 0; j < l.count; j++ {
				blockOfID[l.first+j] = int32(i)
			}
		}
	})
}

// info returns a state's measured facts, resolving it on first use.
func (s StateID) info() stateInfo {
	stateInfoMu.RLock()
	if int(s) < len(stateInfos) && stateInfos[s].known {
		in := stateInfos[s]
		stateInfoMu.RUnlock()
		return in
	}
	stateInfoMu.RUnlock()
	in := resolveState(StateName(s))
	stateInfoMu.Lock()
	for len(stateInfos) <= int(s) {
		stateInfos = append(stateInfos, stateInfo{})
	}
	stateInfos[s] = in
	stateInfoMu.Unlock()
	return in
}

func resolveState(canonical string) stateInfo {
	loadBlockProps()
	name, rest, _ := strings.Cut(strings.TrimPrefix(canonical, "minecraft:"), "[")
	bi, ok := blockByName[name]
	if !ok {
		return stateInfo{id: -1, block: -1, known: true}
	}
	l := &blockLayouts[bi]
	id := l.first
	props := map[string]string{}
	if rest != "" {
		for _, kv := range strings.Split(strings.TrimSuffix(rest, "]"), ",") {
			k, v, _ := strings.Cut(kv, "=")
			props[k] = v
		}
	}
	for _, p := range l.props {
		v, ok := props[p.name]
		idx := -1
		if ok {
			for i, pv := range p.values {
				if pv == v {
					idx = i
					break
				}
			}
		}
		if idx < 0 {
			return stateInfo{id: -1, block: int32(bi), known: true}
		}
		id += idx * p.stride
	}
	return stateInfo{id: int32(id), block: int32(bi), flags: stateFlagWords[id], known: true}
}

func (s StateID) has(f uint64) bool { return s.info().flags&f != 0 }

// isAir, blocksMotion and the like are the game's state predicates.
func (s StateID) isAir() bool        { return s.has(bsfAir) }
func (s StateID) blocksMotion() bool { return s.has(bsfBlocksMotion) }
func (s StateID) isSolid() bool      { return s.has(bsfSolid) }
func (s StateID) canBeReplaced() bool {
	return s.has(bsfReplaceable)
}
func (s StateID) hasFluid() bool { return s.has(bsfFluid) }
func (s StateID) isWaterFluid() bool {
	return s.info().flags&(bsfFluid|bsfWater) == bsfFluid|bsfWater
}
func (s StateID) isLavaFluid() bool {
	return s.info().flags&(bsfFluid|bsfWater) == bsfFluid
}
func (s StateID) isSourceFluid() bool { return s.has(bsfSource) }
func (s StateID) isLeaves() bool      { return s.has(bsfLeaves) }
func (s StateID) sturdy(dir int) bool { return s.has(bsfSturdyDown << dir) }

// sturdyCenter and sturdyRigid are the center and rigid face supports.
func (s StateID) sturdyCenter(dir int) bool { return s.has(bsfCenterDown << dir) }
func (s StateID) sturdyRigid(dir int) bool  { return s.has(bsfRigidDown << dir) }
func (s StateID) isSolidRender() bool       { return s.has(bsfSolidRender) }
func (s StateID) flammable() bool           { return s.has(bsfFlammable) }
func (s StateID) collisionTopFull() bool    { return s.has(bsfCollisionTopFull) }
func (s StateID) isLiquidBlock() bool       { return s.has(bsfLiquid) }

// faceOccludes reports whether the state's occlusion shape on a face is
// a full block (an empty shape is not); canOcclude is the state's
// occlusion flag.
func (s StateID) faceOccludes(dir int) bool { return s.has(bsfOccludeDown << dir) }
func (s StateID) canOcclude() bool          { return s.has(bsfCanOcclude) }

// collisionFaceFull reports whether the state's collision shape covers
// a whole face.
func (s StateID) collisionFaceFull(dir int) bool { return s.has(bsfCollideDown << dir) }

// layout is the state's block layout, nil for an unknown block.
func (s StateID) layout() *blockLayout {
	in := s.info()
	if in.block < 0 {
		return nil
	}
	return &blockLayouts[in.block]
}

// block is the state's block identifier without the namespace.
func (s StateID) block() string {
	in := s.info()
	if in.block < 0 {
		return blockOf(s)
	}
	return blockLayouts[in.block].name
}

// is reports whether the state is of the named block.
func (s StateID) is(block string) bool { return s.block() == block }

// stateByID interns the canonical form of a registry state id.
func stateByID(id int) StateID {
	loadBlockProps()
	l := &blockLayouts[blockOfID[id]]
	rel := id - l.first
	if len(l.props) == 0 {
		return InternState("minecraft:" + l.name)
	}
	parts := make([]string, len(l.props))
	for i, p := range l.props {
		parts[i] = p.name + "=" + p.values[rel/p.stride%len(p.values)]
	}
	sort.Strings(parts)
	return InternState("minecraft:" + l.name + "[" + strings.Join(parts, ",") + "]")
}

// defaultState is a block's default state.
func defaultState(block string) StateID {
	loadBlockProps()
	bi, ok := blockByName[strings.TrimPrefix(block, "minecraft:")]
	if !ok {
		return InternState(block)
	}
	return stateByID(blockLayouts[bi].def)
}

// with returns the state with one property changed (itself when the
// block has no such property or value).
func (s StateID) with(prop, value string) StateID {
	in := s.info()
	if in.id < 0 {
		return s
	}
	l := &blockLayouts[in.block]
	for _, p := range l.props {
		if p.name != prop {
			continue
		}
		rel := int(in.id) - l.first
		cur := rel / p.stride % len(p.values)
		for i, v := range p.values {
			if v == value {
				return stateByID(int(in.id) + (i-cur)*p.stride)
			}
		}
	}
	return s
}

// prop is the state's value of a property, "" when it has none.
func (s StateID) prop(name string) string {
	in := s.info()
	if in.id < 0 {
		return ""
	}
	l := &blockLayouts[in.block]
	for _, p := range l.props {
		if p.name == name {
			return p.values[(int(in.id)-l.first)/p.stride%len(p.values)]
		}
	}
	return ""
}

// hasProp reports whether the state's block has a property.
func (s StateID) hasProp(name string) bool {
	in := s.info()
	if in.block < 0 {
		return false
	}
	for _, p := range blockLayouts[in.block].props {
		if p.name == name {
			return true
		}
	}
	return false
}
