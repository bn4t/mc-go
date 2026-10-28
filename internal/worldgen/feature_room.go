package worldgen

// Monster rooms: a cobblestone box (mossy on most of its floor) carved
// out of solid ground with one to five openings at floor level, up to
// two loot chests against its walls and a spawner in the middle.

func init() {
	featureKinds["minecraft:monster_room"] = func(any) func(*featureCtx) bool { return placeMonsterRoom }
}

var (
	stCobblestone      = InternState("minecraft:cobblestone")
	stMossyCobblestone = InternState("minecraft:mossy_cobblestone")
	monsterRoomMobs    = []string{"minecraft:skeleton", "minecraft:zombie", "minecraft:zombie", "minecraft:spider"}
)

// replaceableByFeatures is Feature.isReplaceable(#features_cannot_replace).
func replaceableByFeatures(s StateID) bool { return !s.inTag("features_cannot_replace") }

// safeSet is Feature.safeSetBlock: the write happens only over a block
// the predicate allows.
func (r *decoRegion) safeSet(p Point, s StateID, ok func(StateID) bool) bool {
	if !ok(r.get(p)) {
		return false
	}
	return r.set(p, s)
}

func placeMonsterRoom(fc *featureCtx) bool {
	r, o, rand := fc.r, fc.origin, fc.rand
	hx := rand.NextIntn(2) + 2
	x0, x1 := -hx-1, hx+1
	hz := rand.NextIntn(2) + 2
	z0, z1 := -hz-1, hz+1
	openings := 0
	for dx := x0; dx <= x1; dx++ {
		for dy := int32(-1); dy <= 4; dy++ {
			for dz := z0; dz <= z1; dz++ {
				p := o.add(Point{dx, dy, dz})
				solid := r.get(p).isSolid()
				if (dy == -1 || dy == 4) && !solid {
					return false
				}
				if (dx == x0 || dx == x1 || dz == z0 || dz == z1) && dy == 0 &&
					r.get(p).isAir() && r.get(p.offset(dirUp)).isAir() {
					openings++
				}
			}
		}
	}
	if openings < 1 || openings > 5 {
		return false
	}
	for dx := x0; dx <= x1; dx++ {
		for dy := int32(3); dy >= -1; dy-- {
			for dz := z0; dz <= z1; dz++ {
				p := o.add(Point{dx, dy, dz})
				s := r.get(p)
				if dx != x0 && dy != -1 && dz != z0 && dx != x1 && dy != 4 && dz != z1 {
					if !s.is("chest") && !s.is("spawner") {
						r.safeSet(p, stCaveAir, replaceableByFeatures)
					}
					continue
				}
				if p.Y >= r.minY && !r.get(p.offset(dirDown)).isSolid() {
					r.set(p, stCaveAir)
				} else if s.isSolid() && !s.is("chest") {
					if dy == -1 && rand.NextIntn(4) != 0 {
						r.safeSet(p, stMossyCobblestone, replaceableByFeatures)
					} else {
						r.safeSet(p, stCobblestone, replaceableByFeatures)
					}
				}
			}
		}
	}
	for i := 0; i < 2; i++ {
		for j := 0; j < 3; j++ {
			p := Point{o.X + rand.NextIntn(hx*2+1) - hx, o.Y, o.Z + rand.NextIntn(hz*2+1) - hz}
			if !r.get(p).isAir() {
				continue
			}
			walls := 0
			for _, d := range horizontalDirs {
				if r.get(p.offset(d)).isSolid() {
					walls++
				}
			}
			if walls != 1 {
				continue
			}
			if r.safeSet(p, reorientChest(r, p, defaultState("chest")), replaceableByFeatures) {
				r.setBlockEntity(p, &GenBlockEntity{ID: "minecraft:chest", LootTable: "minecraft:chests/simple_dungeon", LootSeed: rand.NextLong()})
			}
			break
		}
	}
	if r.safeSet(o, defaultState("spawner"), replaceableByFeatures) {
		mob := monsterRoomMobs[rand.NextIntn(int32(len(monsterRoomMobs)))]
		r.setBlockEntity(o, &GenBlockEntity{ID: "minecraft:mob_spawner", SpawnEntity: mob})
	}
	return true
}

var clockwise = map[int]int{dirNorth: dirEast, dirEast: dirSouth, dirSouth: dirWest, dirWest: dirNorth}

var facingNames = map[int]string{dirNorth: "north", dirSouth: "south", dirWest: "west", dirEast: "east"}

// reorientChest is StructurePiece.reorient: a chest beside another stays
// as it is; one with a single solid-render neighbour faces away from it;
// otherwise its facing turns away from solid blocks in front.
func reorientChest(r *decoRegion, p Point, s StateID) StateID {
	wall := -1
	for _, d := range horizontalDirs {
		n := r.get(p.offset(d))
		if n.is("chest") {
			return s
		}
		if n.isSolidRender() {
			if wall >= 0 {
				wall = -1
				break
			}
			wall = d
		}
	}
	if wall >= 0 {
		return s.with("facing", facingNames[wall^1])
	}
	f := directionNames[s.prop("facing")]
	if r.get(p.offset(f)).isSolidRender() {
		f ^= 1
	}
	if r.get(p.offset(f)).isSolidRender() {
		f = clockwise[f]
	}
	if r.get(p.offset(f)).isSolidRender() {
		f ^= 1
	}
	return s.with("facing", facingNames[f])
}
