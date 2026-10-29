package server

import (
	"bytes"
	"math"
	"math/rand"
	"strings"
	"sync"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// Entity-placing items' useOn (ServerPlayerGameMode.useItemOn reaches
// the held item after the clicked block's own use passed):
//
//   - SpawnEggItem.useOn — a Spawner block entity (spawner,
//     trial_spawner) takes the type instead of spawning; otherwise the
//     mob spawns at the clicked cell when its collision shape is empty,
//     else at the face neighbour, through EntityType.spawn with
//     shouldOffsetY=true and shouldOffsetYMore = (moved off the
//     clicked cell && face UP).
//   - MinecartItem.useOn — a RAILS-tag block only; the cart lands at
//     the rail's bottom-centre + 0.0625, +0.5 more on an ascending
//     shape (createMinecart at reason DISPENSER, old movement: no rail
//     snap).
//   - ArmorStandItem.useOn — never on a DOWN face; the BlockPlaceContext
//     cell (clicked when replaceable) must have no block collision and
//     no entity in the stand's 0.5x1.975 box at its bottom-centre.
//   - HangingEntityItem.useOn — painting (horizontal faces only) and
//     item frames (any face inside the build height) hang on the face
//     neighbour when survives() holds.
//
// Every success shrinks the held stack by one; ServerPlayerGameMode
// restores the count for players with infinite materials (creative).

// useEntityItem dispatches the held stack to its entity-item useOn.
// It returns false when the held item is none of these families so
// the caller's block-item path continues; a recognized item always
// reports true — FAIL/CONSUME outcomes end the use like vanilla's
// non-PASS results.
func (s *Server) useEntityItem(sess *Session, d *dimension, clicked world.Pos, face int32, facePos world.Pos) bool {
	held := sess.heldStack()
	// A spectator's use_item_on never reaches the item's useOn.
	if held.Empty() || sess.mode() == GameModeSpectator {
		return false
	}
	iname, ok := itemNameOf(held.ID)
	if !ok {
		return false
	}
	iname = strings.TrimPrefix(iname, "minecraft:")
	switch {
	case strings.HasSuffix(iname, "_spawn_egg"):
		s.useSpawnEgg(sess, d, clicked, face, facePos, held, strings.TrimSuffix(iname, "_spawn_egg"))
		return true
	case dispenseMinecartEntity[iname] != "":
		s.useMinecartItem(sess, d, clicked, held, dispenseMinecartEntity[iname])
		return true
	case iname == "armor_stand":
		s.useArmorStandItem(sess, d, clicked, face, facePos, held)
		return true
	case iname == "painting" || iname == "item_frame" || iname == "glow_item_frame":
		s.useHangingItem(sess, d, face, facePos, held, iname)
		return true
	}
	return false
}

// entityItemConsume is the success tail every useOn here shares: the
// ITEM_USED stat (ItemStack.useOn on a consuming result) and the
// one-item shrink a creative player gets back.
func (s *Server) entityItemConsume(sess *Session, held ItemStack) {
	sess.addItemStat(statUsed, held.ID, 1)
	if sess.mode() != GameModeCreative {
		sess.consumeHeld(1)
	}
}

// useSpawnEgg is SpawnEggItem.useOn.
func (s *Server) useSpawnEgg(sess *Session, d *dimension, clicked world.Pos, face int32, facePos world.Pos, held ItemStack, ent string) {
	if _, ok := EntityTypeID(ent); !ok {
		return
	}
	// EntityType.canSpawn: a notInPeaceful type fails on peaceful.
	if s.difficulty == DifficultyPeaceful && notInPeaceful[ent] {
		return
	}
	cur := d.w.GetBlock(clicked)
	if bn, _ := world.StateOwner(cur); bn == "spawner" || bn == "trial_spawner" {
		// The Spawner block entity path: spawner_blocks_work=false
		// answers advMode.notEnabled.spawner and FAILs; otherwise
		// setEntityId, BLOCK_CHANGE at the block, and shrink(1).
		if !s.gameRuleBool("minecraft:spawner_blocks_work") {
			s.sendSystemChatComponent(sess, translatable("advMode.notEnabled.spawner"))
			return
		}
		if bn == "spawner" {
			s.spawnerSetEntity(d, clicked, ent)
		}
		s.emitGameEvent(d, "block_change", float64(clicked.X)+0.5, float64(clicked.Y)+0.5, float64(clicked.Z)+0.5, sess, "")
		s.entityItemConsume(sess, held)
		return
	}
	spawnPos := clicked
	if !collisionShapeEmpty(cur) {
		spawnPos = facePos
	}
	more := spawnPos != clicked && face == 1
	s.spawnEggMob(sess, d, held, ent, spawnPos, true, more)
}

// spawnEggMob is SpawnEggItem.spawnMob at a cell: EntityType.spawn with
// or without the shouldOffsetY drop, the finalizeSpawn rolls, the
// stack's name, one ambient voice, the consume and ENTITY_PLACE.
func (s *Server) spawnEggMob(sess *Session, d *dimension, held ItemStack, ent string, spawnPos world.Pos, offsetY, more bool) {
	et, _ := EntityTypeID(ent)
	dims := mobBounds(et)
	cx, cz := float64(spawnPos.X)+0.5, float64(spawnPos.Z)+0.5
	y := float64(spawnPos.Y)
	if offsetY {
		y += spawnYOffset(d, spawnPos, more, cx, cz, dims[0])
	}
	// create's snapTo: a random wrapDegrees(nextFloat*360) yaw, pitch
	// 0; Mob copies it to yHeadRot/yBodyRot before finalizeSpawn.
	yaw := mthWrapDegrees(rand.Float32() * 360)
	e, ok := s.summonMobRaw(d, ent, cx, y, cz, false, true)
	if !ok {
		return
	}
	e.Yaw, e.Pitch = yaw, 0
	e.HeadYaw, e.LookYaw = yaw, yaw
	// Mob.finalizeSpawn(SPAWN_ITEM_USE): the same rolls the bare
	// summon's finalizeSpawn makes.
	if e.Living && ent != "armor_stand" {
		if v := rollSpawnVariant(ent, strings.TrimPrefix(d.w.Biome(), "minecraft:")); v != "" {
			e.VariantName = v
		}
		e.SoundVariant = rollSoundVariant(ent)
		s.mobDefaultEquip(e)
		if raidRaiderType(ent) {
			e.CanJoinRaid = true
		}
	}
	// appendComponentsConfig: the stack's custom_name names the mob.
	if held.Name != "" {
		e.CustomName = held.Name
		e.CustomNameComp = false
	}
	if !e.Dormant {
		s.broadcastFreshMob(e)
	}
	// EntityType.spawn ends with Mob.playAmbientSound — one voice at
	// the spawn, independent of the ambient-time counter.
	if a, ok := mobAmbient[ent]; ok && ent != "breeze" {
		s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, mobAmbientSoundID(e, ent), mobVoiceOf(e).src(), a.vol, voicePitchE(e), nil)
	}
	s.entityItemConsume(sess, held)
	s.emitGameEvent(d, "entity_place", float64(spawnPos.X)+0.5, float64(spawnPos.Y)+0.5, float64(spawnPos.Z)+0.5, sess, "")
}

// useSpawnEggAir is SpawnEggItem.use: a SOURCE_ONLY POV clip that
// must hit a block (else PASS); a type that may not spawn fails; the
// hit block must be a LiquidBlock (else PASS) the player may interact
// with; the mob spawns in that cell without the y offset.
func (s *Server) useSpawnEggAir(sess *Session, hand int32, yaw, pitch float32, ent string) {
	d := sess.dimension()
	if d == nil || sess.mode() == GameModeSpectator {
		return
	}
	hit := s.playerPOVHit(sess, d, clipFluidSourceOnly, yaw, pitch)
	if hit.miss {
		return
	}
	if _, ok := EntityTypeID(ent); !ok || (s.difficulty == DifficultyPeaceful && notInPeaceful[ent]) {
		return
	}
	if !worldIsLiquid(d.w.GetBlock(hit.pos)) {
		return
	}
	if !s.mayInteract(sess, d, hit.pos) || !bucketMayUseAt(sess) {
		return
	}
	held, _ := sess.stackForHand(hand)
	s.spawnEggMob(sess, d, held, ent, hit.pos, false, false)
	if sess.mode() != GameModeCreative {
		// useItem saw the hand stack shrink: sendAllDataToRemote.
		_ = s.sendContainerContent(sess)
	}
}

// useMinecartItem is MinecartItem.useOn.
func (s *Server) useMinecartItem(sess *Session, d *dimension, clicked world.Pos, held ItemStack, ent string) {
	cur := d.w.GetBlock(clicked)
	bn, _ := world.StateOwner(cur)
	bn = strings.TrimPrefix(bn, "minecraft:")
	if !blockTagHas("rails", bn) {
		return
	}
	et, ok := EntityTypeID(ent)
	if !ok {
		return
	}
	y := float64(clicked.Y) + 0.0625
	if shape, _ := world.StateProperty(cur, "shape"); strings.HasPrefix(shape, "ascending_") {
		y += 0.5
	}
	e := s.entities.Spawn(&Entity{
		Type: et,
		Dim:  d.name,
		X:    float64(clicked.X) + 0.5,
		Y:    y,
		Z:    float64(clicked.Z) + 0.5,
	})
	if held.Name != "" {
		e.CustomName = held.Name
		e.CustomNameComp = false
	}
	s.broadcastEntitySpawn(nil, *e)
	below := ""
	if n, ok := world.StateOwner(d.w.GetBlock(world.Pos{X: clicked.X, Y: clicked.Y - 1, Z: clicked.Z})); ok {
		below = strings.TrimPrefix(n, "minecraft:")
	}
	s.emitGameEvent(d, "entity_place", float64(clicked.X)+0.5, float64(clicked.Y)+0.5, float64(clicked.Z)+0.5, sess, below)
	s.entityItemConsume(sess, held)
}

// useArmorStandItem is ArmorStandItem.useOn.
func (s *Server) useArmorStandItem(sess *Session, d *dimension, clicked world.Pos, face int32, facePos world.Pos, held ItemStack) {
	if face == 0 {
		return
	}
	pos := facePos
	if placeReplaceable(d.w.GetBlock(clicked)) {
		pos = clicked
	}
	cx, cz := float64(pos.X)+0.5, float64(pos.Z)+0.5
	box := aabb{cx - 0.25, float64(pos.Y), cz - 0.25, cx + 0.25, float64(pos.Y) + 1.975, cz + 0.25}
	if blockCollisionIn(d, box) || s.entityInBox(d, box) {
		return
	}
	y := float64(pos.Y) + spawnYOffset(d, pos, true, cx, cz, 0.5)
	_, _, _, pyaw, _, _ := sess.Pos()
	yaw := float32(math.Floor(float64((mthWrapDegrees(pyaw-180)+22.5)/45))) * 45
	e, ok := s.summonMobRaw(d, "armor_stand", cx, y, cz, false, false)
	if !ok {
		return
	}
	e.Yaw, e.Pitch = yaw, 0
	// LivingEntity's constructor rolls yRot = nextFloat*6.2831855 and
	// copies it to yHeadRot; the stand is no Mob, so neither create's
	// snapTo nor the item's yaw snap touch the head.
	e.HeadYaw = rand.Float32() * 6.2831855
	if held.Name != "" {
		e.CustomName = held.Name
		e.CustomNameComp = false
	}
	if !e.Dormant {
		s.broadcastFreshMob(e)
	}
	if snd, ok := SoundEventID("entity.armor_stand.place"); ok {
		s.playSoundSrc(d.name, e.X, e.Y, e.Z, snd, 4, 0.75, 0.8, nil)
	}
	s.emitGameEvent(d, "entity_place", e.X, e.Y, e.Z, sess, "")
	s.entityItemConsume(sess, held)
}

// useHangingItem is HangingEntityItem.useOn (ItemFrameItem's mayPlace
// override for the frames).
func (s *Server) useHangingItem(sess *Session, d *dimension, face int32, pos world.Pos, held ItemStack, iname string) {
	// mayUseItemAt: a player without mayBuild (adventure, spectator)
	// may not hang anything.
	if sess.mode() == GameModeAdventure || sess.mode() == GameModeSpectator {
		return
	}
	if iname == "painting" {
		if face < 2 {
			return
		}
	} else if pos.Y < d.minY || pos.Y > d.minY+d.height-1 {
		return
	}
	et, ok := EntityTypeID(iname)
	if !ok {
		return
	}
	e := &Entity{Type: et, Dim: d.name, HangSet: true, HangDir: face, HangPos: pos, Data: face}
	if iname == "painting" {
		v, ok := s.paintingPick(d, pos, face)
		if !ok {
			return // Painting.create empty: CONSUME, nothing placed
		}
		e.PaintingVariant = v
	}
	hangSetRotation(e)
	box := hangingBox(e)
	e.X, e.Y, e.Z = (box[0]+box[3])/2, (box[1]+box[4])/2, (box[2]+box[5])/2
	if held.Name != "" {
		e.CustomName = held.Name
		e.CustomNameComp = false
	}
	if !s.hangingSurvives(d, e) {
		return // CONSUME
	}
	// playPlacementSound, the ENTITY_PLACE event, then addFreshEntity.
	snd := "entity.painting.place"
	if iname != "painting" {
		snd = "entity." + iname + ".place"
	}
	if id, ok := SoundEventID(snd); ok {
		s.playSoundSrc(d.name, e.X, e.Y, e.Z, id, 6, 1, 1, nil)
	}
	s.emitGameEvent(d, "entity_place", e.X, e.Y, e.Z, sess, "")
	e.hangResend = true
	ne := s.entities.Spawn(e)
	s.broadcastEntitySpawn(nil, *ne)
	s.entityItemConsume(sess, held)
}

// hangSetRotation is setDirection's rotation write: a horizontal facing
// takes yRot = 2D data value * 90, xRot 0; ItemFrame's vertical faces
// take xRot = -90 * step and yRot 0.
func hangSetRotation(e *Entity) {
	switch e.HangDir {
	case 0:
		e.Yaw, e.Pitch = 0, 90
	case 1:
		e.Yaw, e.Pitch = 0, -90
	case 2:
		e.Yaw = 180
	case 3:
		e.Yaw = 0
	case 4:
		e.Yaw = 90
	case 5:
		e.Yaw = 270
	}
}

// aabb is a world-space box: min x,y,z then max x,y,z.
type aabb [6]float64

func (a aabb) intersects(b aabb) bool {
	return a[0] < b[3] && a[3] > b[0] && a[1] < b[4] && a[4] > b[1] && a[2] < b[5] && a[5] > b[2]
}

// hangDirStep is Direction.getStepX/Y/Z for a 3D data value.
func hangDirStep(dir int32) (int, int, int) {
	switch dir {
	case 0:
		return 0, -1, 0
	case 1:
		return 0, 1, 0
	case 2:
		return 0, 0, -1
	case 3:
		return 0, 0, 1
	case 4:
		return -1, 0, 0
	case 5:
		return 1, 0, 0
	}
	return 0, 0, 0
}

// hangDirCCW is Direction.getCounterClockWise for the horizontals:
// north→west→south→east→north.
func hangDirCCW(dir int32) int32 {
	switch dir {
	case 2:
		return 4
	case 4:
		return 3
	case 3:
		return 5
	case 5:
		return 2
	}
	return dir
}

// hangingBox is calculateBoundingBox: centred on the attachment cell
// pushed 0.46875 against the wall (Vec3.relative(dir, -0.46875)). A
// painting shifts half a block along counterclockwise / up for an
// even width / height and spans width x height x 0.0625; a frame is
// the 0.75 x 0.75 x 0.0625 plate (getPopBox — no framed map here).
func hangingBox(e *Entity) aabb {
	sx, sy, sz := hangDirStep(e.HangDir)
	cx := float64(e.HangPos.X) + 0.5 - 0.46875*float64(sx)
	cy := float64(e.HangPos.Y) + 0.5 - 0.46875*float64(sy)
	cz := float64(e.HangPos.Z) + 0.5 - 0.46875*float64(sz)
	var w, h, d float64
	if e.PaintingVariant != "" {
		pw, ph := paintingSize(e.PaintingVariant)
		off := func(n int) float64 {
			if n%2 == 0 {
				return 0.5
			}
			return 0
		}
		qx, _, qz := hangDirStep(hangDirCCW(e.HangDir))
		cx += off(pw) * float64(qx)
		cz += off(pw) * float64(qz)
		cy += off(ph)
		h = float64(ph)
		if sx != 0 {
			w, d = 0.0625, float64(pw)
		} else {
			w, d = float64(pw), 0.0625
		}
	} else {
		w, h, d = 0.75, 0.75, 0.75
		switch {
		case sx != 0:
			w = 0.0625
		case sy != 0:
			h = 0.0625
		default:
			d = 0.0625
		}
	}
	return aabb{cx - w/2, cy - h/2, cz - d/2, cx + w/2, cy + h/2, cz + d/2}
}

// hangingSurvives is HangingEntity.survives (paintings) and
// ItemFrame.survives: no block collision inside the box; the wall
// behind is legacy-solid (or a diode — horizontal faces only for a
// frame); and no other hanging entity overlaps on the same face
// (canCoexist — a painting also refuses any overlapping painting).
func (s *Server) hangingSurvives(d *dimension, e *Entity) bool {
	box := hangingBox(e)
	if blockCollisionIn(d, box) {
		return false
	}
	painting := e.PaintingVariant != ""
	if painting {
		sx, sy, sz := hangDirStep(e.HangDir)
		sup := aabb{
			box[0] - 0.5*float64(sx) + 1e-7, box[1] - 0.5*float64(sy) + 1e-7, box[2] - 0.5*float64(sz) + 1e-7,
			box[3] - 0.5*float64(sx) - 1e-7, box[4] - 0.5*float64(sy) - 1e-7, box[5] - 0.5*float64(sz) - 1e-7,
		}
		for x := int(math.Floor(sup[0])); x <= int(math.Floor(sup[3])); x++ {
			for y := int(math.Floor(sup[1])); y <= int(math.Floor(sup[4])); y++ {
				for z := int(math.Floor(sup[2])); z <= int(math.Floor(sup[5])); z++ {
					b := d.w.GetBlock(world.Pos{X: x, Y: y, Z: z})
					if !legacySolid(b) && !isDiodeBlock(b) {
						return false
					}
				}
			}
		}
	} else {
		sx, sy, sz := hangDirStep(e.HangDir)
		b := d.w.GetBlock(world.Pos{X: e.HangPos.X - sx, Y: e.HangPos.Y - sy, Z: e.HangPos.Z - sz})
		if !legacySolid(b) && !(e.HangDir >= 2 && isDiodeBlock(b)) {
			return false
		}
	}
	for _, o := range s.entities.All() {
		if o.ID == e.ID || o.Dim != e.Dim || !o.HangSet {
			continue
		}
		if !(painting && o.PaintingVariant != "") && o.HangDir != e.HangDir {
			continue
		}
		if hangingBox(&o).intersects(box) {
			return false
		}
	}
	return true
}

// paintingPick is Painting.create: the placeable tag's variants that
// survive at (pos, dir), narrowed to the largest area, one picked
// uniformly (Util.getRandomSafe).
func (s *Server) paintingPick(d *dimension, pos world.Pos, dir int32) (string, bool) {
	probe := &Entity{Dim: d.name, HangSet: true, HangDir: dir, HangPos: pos, ID: -1}
	var fits []string
	best := 0
	for _, v := range paintingPlaceable() {
		probe.PaintingVariant = v
		if !s.hangingSurvives(d, probe) {
			continue
		}
		w, h := paintingSize(v)
		if w*h > best {
			best, fits = w*h, fits[:0]
		}
		if w*h == best {
			fits = append(fits, v)
		}
	}
	if len(fits) == 0 {
		return "", false
	}
	return fits[rand.Intn(len(fits))], true
}

// encodeHangingMeta is a placed hanging entity's pairing data: the
// non-default entries — DATA_DIRECTION (index 8, Direction
// serializer 12) off its SOUTH default, and a painting's
// DATA_PAINTING_VARIANT_ID (index 9, holder serializer 34, id+1) off
// the registry's first entry (VariantUtils.getAny).
func encodeHangingMeta(e *Entity) []byte {
	var b protocol.Buffer
	n := 0
	if e.HangDir != 3 {
		b.Byte(8)
		b.VarInt(12)
		b.VarInt(e.HangDir)
		n++
	}
	if e.PaintingVariant != "" {
		if id := registryEntryID("minecraft:painting_variant", "minecraft:"+e.PaintingVariant); id > 0 {
			b.Byte(9)
			b.VarInt(34)
			b.VarInt(id + 1)
			n++
		}
	}
	if n == 0 {
		return nil
	}
	b.Byte(0xFF)
	return b.Bytes()
}

// encodeHangingMetaDirty is the first sendChanges' packDirty after the
// placement: DATA_DIRECTION when setDirection moved it off SOUTH, and
// a painting's variant always — Painting.create's survives probe sets
// every placeable candidate in turn, so the entry is dirty even when
// the pick lands back on the default.
func encodeHangingMetaDirty(e *Entity) []byte {
	var b protocol.Buffer
	n := 0
	if e.HangDir != 3 {
		b.Byte(8)
		b.VarInt(12)
		b.VarInt(e.HangDir)
		n++
	}
	if e.PaintingVariant != "" {
		if id := registryEntryID("minecraft:painting_variant", "minecraft:"+e.PaintingVariant); id >= 0 {
			b.Byte(9)
			b.VarInt(34)
			b.VarInt(id + 1)
			n++
		}
	}
	if n == 0 {
		return nil
	}
	b.Byte(0xFF)
	return b.Bytes()
}

var paintingData struct {
	once      sync.Once
	size      map[string][2]int
	placeable []string
}

// loadPaintingData reads the variant sizes out of the captured
// painting_variant Registry Data body (width/height of each entry's
// NBT) and the placeable tag's member order out of the captured
// Update Tags body — both are vanilla's own wire bytes.
func loadPaintingData() {
	paintingData.once.Do(func() {
		paintingData.size = map[string][2]int{}
		data, err := javaConfigFor(776)
		if err != nil {
			return
		}
		for name, m := range capturedRegistryNBT("minecraft:painting_variant") {
			w, _ := m["width"].(int32)
			h, _ := m["height"].(int32)
			paintingData.size[name] = [2]int{int(w), int(h)}
		}
		names := data.names["minecraft:painting_variant"]
		r := protocol.NewPacketReader(data.tags)
		regs, err := r.VarInt()
		if err != nil {
			return
		}
		for i := int32(0); i < regs; i++ {
			key, err := r.String()
			if err != nil {
				return
			}
			tags, err := r.VarInt()
			if err != nil {
				return
			}
			for j := int32(0); j < tags; j++ {
				tag, err := r.String()
				if err != nil {
					return
				}
				n, err := r.VarInt()
				if err != nil {
					return
				}
				for k := int32(0); k < n; k++ {
					id, err := r.VarInt()
					if err != nil {
						return
					}
					if key == "minecraft:painting_variant" && tag == "minecraft:placeable" &&
						id >= 0 && int(id) < len(names) {
						paintingData.placeable = append(paintingData.placeable, strings.TrimPrefix(names[id], "minecraft:"))
					}
				}
			}
		}
	})
}

var capturedNBT struct {
	mu   sync.Mutex
	regs map[string]map[string]map[string]any
}

// capturedRegistryNBT decodes one captured Registry Data body into its
// entries' NBT (entry name without the minecraft: prefix → compound),
// cached per registry key.
func capturedRegistryNBT(key string) map[string]map[string]any {
	capturedNBT.mu.Lock()
	defer capturedNBT.mu.Unlock()
	if m, ok := capturedNBT.regs[key]; ok {
		return m
	}
	if capturedNBT.regs == nil {
		capturedNBT.regs = map[string]map[string]map[string]any{}
	}
	out := map[string]map[string]any{}
	capturedNBT.regs[key] = out
	data, err := javaConfigFor(776)
	if err != nil {
		return out
	}
	for _, reg := range data.registries {
		if reg.key != key {
			continue
		}
		br := bytes.NewReader(reg.body)
		_, _ = protocol.ReadString(br)
		count, _ := protocol.ReadVarInt(br)
		for i := int32(0); i < count; i++ {
			name, err := protocol.ReadString(br)
			if err != nil {
				return out
			}
			present, err := protocol.ReadBool(br)
			if err != nil {
				return out
			}
			if !present {
				continue
			}
			v, err := nbt.ReadUnnamed(br)
			if err != nil {
				return out
			}
			if m, ok := v.(map[string]any); ok {
				out[strings.TrimPrefix(name, "minecraft:")] = m
			}
		}
	}
	return out
}

// variantAmbientSound is getAmbientSound for the sound-variant mobs
// whose voice is the variant's plain ambient_sound (Pig, Cow,
// Chicken): the entry's adult_sounds — baby_sounds for a baby — out of
// the captured <type>_sound_variant registry; "classic" is the define
// default.
func variantAmbientSound(e *Entity, typ string) (int32, bool) {
	switch typ {
	case "pig", "cow", "chicken":
	default:
		return 0, false
	}
	v := e.SoundVariant
	if v == "" {
		v = "classic"
	}
	entry := capturedRegistryNBT("minecraft:" + typ + "_sound_variant")[strings.TrimPrefix(v, "minecraft:")]
	set := "adult_sounds"
	if e.Baby {
		set = "baby_sounds"
	}
	sounds, _ := entry[set].(map[string]any)
	name, _ := sounds["ambient_sound"].(string)
	if name == "" {
		return 0, false
	}
	return SoundEventID(strings.TrimPrefix(name, "minecraft:"))
}

// mobAmbientSoundID is the ambient voice a mob plays: the sound
// variant's own ambient where the type has one, else the mobAmbient
// table's adult/baby sound.
func mobAmbientSoundID(e *Entity, typ string) int32 {
	if id, ok := variantAmbientSound(e, typ); ok {
		return id
	}
	a := mobAmbient[typ]
	if e.Baby && a.babySnd != 0 {
		return a.babySnd
	}
	return a.snd
}

func paintingSize(v string) (int, int) {
	loadPaintingData()
	s := paintingData.size[v]
	if s[0] == 0 {
		return 1, 1
	}
	return s[0], s[1]
}

func paintingPlaceable() []string {
	loadPaintingData()
	return paintingData.placeable
}

// mthWrapDegrees is Mth.wrapDegrees(float): f % 360 folded into
// [-180, 180).
func mthWrapDegrees(f float32) float32 {
	r := float32(math.Mod(float64(f), 360))
	if r >= 180 {
		r -= 360
	}
	if r < -180 {
		r += 360
	}
	return r
}

// cellShape is the block's collision shape as cell-relative boxes: the
// collisionboxes table where it has the block, nothing for the
// no-collision cells, else the unit cube.
func cellShape(b world.Block) []cellBox {
	if b == world.Air || isAirCell(b) {
		return nil
	}
	if boxes, has := collisionBoxesOf(b); has {
		return boxes
	}
	// Low plates the shared table leaves out: DiodeBlock's 2/16
	// slab and the carpets' 1/16 sheet.
	if isDiodeBlock(b) {
		return []cellBox{{0, 0, 0, 1, 0.125, 1}}
	}
	if n, _ := world.StateOwner(b); strings.HasSuffix(n, "_carpet") {
		return []cellBox{{0, 0, 0, 1, 0.0625, 1}}
	}
	if world.NonSolid(b) || moveNoCollision(b) {
		return nil
	}
	return []cellBox{{0, 0, 0, 1, 1, 1}}
}

// collisionShapeEmpty is getCollisionShape(...).isEmpty().
func collisionShapeEmpty(b world.Block) bool { return len(cellShape(b)) == 0 }

// legacySolid is BlockState.isSolid's shape rule: a non-empty
// collision shape whose bounds average at least 0.7291666 per axis or
// reach a full block of height.
func legacySolid(b world.Block) bool {
	boxes := cellShape(b)
	if len(boxes) == 0 {
		return false
	}
	mn := [3]float64{1, 1, 1}
	mx := [3]float64{0, 0, 0}
	for _, c := range boxes {
		for i := 0; i < 3; i++ {
			mn[i] = math.Min(mn[i], c[i])
			mx[i] = math.Max(mx[i], c[i+3])
		}
	}
	avg := ((mx[0] - mn[0]) + (mx[1] - mn[1]) + (mx[2] - mn[2])) / 3
	return avg >= 0.7291666666666666 || mx[1]-mn[1] >= 1
}

func isDiodeBlock(b world.Block) bool {
	n, _ := world.StateOwner(b)
	n = strings.TrimPrefix(n, "minecraft:")
	return n == "repeater" || n == "comparator"
}

// placeReplaceable is BlockState.canBeReplaced for a non-block item's
// BlockPlaceContext: the replaceable tag, with SnowLayerBlock's
// override (only a single layer gives way).
func placeReplaceable(b world.Block) bool {
	n, _ := world.StateOwner(b)
	n = strings.TrimPrefix(n, "minecraft:")
	if n == "snow" {
		l, _ := world.StateProperty(b, "layers")
		return l == "1"
	}
	if b == world.Air {
		return true
	}
	return blockTagHas("replaceable", n)
}

// worldShapes lists the collision boxes of every block whose shape
// overlaps box (BlockCollisions, which scans one cell beyond the box
// for shapes that spill out of their cell).
func worldShapes(d *dimension, box aabb) []aabb {
	var out []aabb
	for x := int(math.Floor(box[0])) - 1; x <= int(math.Floor(box[3])); x++ {
		for y := int(math.Floor(box[1])) - 1; y <= int(math.Floor(box[4])); y++ {
			for z := int(math.Floor(box[2])) - 1; z <= int(math.Floor(box[5])); z++ {
				for _, c := range cellShapeAt(d.w.GetBlock(world.Pos{X: x, Y: y, Z: z}), world.Pos{X: x, Y: y, Z: z}) {
					a := aabb{float64(x) + c[0], float64(y) + c[1], float64(z) + c[2], float64(x) + c[3], float64(y) + c[4], float64(z) + c[5]}
					if a.intersects(box) {
						out = append(out, a)
					}
				}
			}
		}
	}
	return out
}

// blockCollisionIn is !noBlockCollision(box).
func blockCollisionIn(d *dimension, box aabb) bool { return len(worldShapes(d, box)) > 0 }

// spawnYOffset is EntityType.getYOffset: the collisions inside the
// spawn cell (extended one cell down when shouldOffsetYMore) stop the
// entity's box, placed one block up, as it moves down by at most 1
// (2 with the extension); the offset is 1 plus that move.
func spawnYOffset(d *dimension, pos world.Pos, more bool, cx, cz, width float64) float64 {
	q := aabb{float64(pos.X), float64(pos.Y), float64(pos.Z), float64(pos.X) + 1, float64(pos.Y) + 1, float64(pos.Z) + 1}
	maxDist := -1.0
	if more {
		q[1]--
		maxDist = -2
	}
	bottom := float64(pos.Y) + 1
	hw := width / 2
	dist := maxDist
	for _, a := range worldShapes(d, q) {
		if a[3] <= cx-hw+1e-7 || a[0] >= cx+hw-1e-7 || a[5] <= cz-hw+1e-7 || a[2] >= cz+hw-1e-7 {
			continue
		}
		if a[4] <= bottom+1e-7 && a[4]-bottom > dist {
			dist = a[4] - bottom
		}
	}
	if math.Abs(dist) < 1e-7 {
		dist = 0
	}
	return 1 + dist
}

// entityInBox is !getEntities(null, box).isEmpty(): any non-spectator
// entity or player whose bounding box overlaps.
func (s *Server) entityInBox(d *dimension, box aabb) bool {
	for _, o := range s.entities.All() {
		if o.Dim != d.name || o.Dormant {
			continue
		}
		var ob aabb
		if o.HangSet {
			ob = hangingBox(&o)
		} else {
			dims := mobBounds(o.Type)
			ob = aabb{o.X - dims[0]/2, o.Y, o.Z - dims[0]/2, o.X + dims[0]/2, o.Y + dims[1], o.Z + dims[0]/2}
		}
		if ob.intersects(box) {
			return true
		}
	}
	hit := false
	s.sessions.Range(func(_, v any) bool {
		p := v.(*Session)
		if p.state.Load() != StatePlay || !p.spawned.Load() || p.dimName() != d.name || p.mode() == GameModeSpectator {
			return true
		}
		x, y, z, _, _, _ := p.Pos()
		if (aabb{x - 0.3, y, z - 0.3, x + 0.3, y + 1.8, z + 0.3}).intersects(box) {
			hit = true
			return false
		}
		return true
	})
	return hit
}
