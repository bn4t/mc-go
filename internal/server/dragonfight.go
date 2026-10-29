package server

import (
	"math"
	"math/rand"
	"os"
	"path/filepath"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
	"mc-go/internal/worldgen"
)

// The End's dragon fight, measured from the 26.2 jar
// (net.minecraft.world.level.dimension.end.EnderDragonFight) and the
// wire:
//
//   - A level whose dimension type has an ender dragon fight keeps one
//     as saved data ender_dragon_fight
//     (dimensions/minecraft/the_end/data/minecraft/ender_dragon_fight.dat,
//     {DataVersion, data:{...}}): needs_state_scanning, dragon_killed,
//     previously_killed, respawn_stage (only while running),
//     respawn_time, dragon_uuid and exit_portal_location (only while
//     known), gateways and respawn_crystals (only when non-empty). A
//     fresh fight scans the world once and shuffles the twenty gateway
//     slots 0..19 with the level seed (Util.shuffle over a
//     SingleThreadedRandomSource).
//   - The fight's boss bar is entity.minecraft.ender_dragon, PINK,
//     PROGRESS, flags play-music|world-fog (6); it is visible while the
//     dragon lives, and every 20 ticks it is handed to the live players
//     within 192 blocks of (0, 128, 0).
//   - While the bar has players the 17x17 chunk arena loads; once it is
//     loaded the first scan runs (an exit portal already standing means
//     a dragon was killed before; else the inactive podium is placed at
//     the origin column's top), a missing dragon is created at
//     (0, 128, 0) in HOLDING_PATTERN with a random yaw (and recreated
//     after 1200 ticks unseen), and the crystals on the ten spikes are
//     recounted every 100 ticks.
//   - setDragonKilled: progress 0, bar hidden, active podium, the next
//     gateway (level event 3000, then the end_gateway feature at
//     (floor(96 cos a), 75, floor(96 sin a)), a = 2(-pi + 0.15707963 i)),
//     the dragon egg on the podium the first time.
//   - Four crystals on the exit portal's sides start the respawn: the
//     portal turns to end stone, the inactive podium returns, and the
//     stages run (START beams the crystals at (0,128,0); PREPARING
//     sends level event 3001 at ticks 0, 50-52 and 95-99; SUMMONING
//     clears, blasts and rebuilds each spike every 40 ticks with an
//     invulnerable crystal beaming at (0,128,0); SUMMONING_DRAGON
//     spawns the dragon at tick 100 after level events 3001 at 1-4 and
//     80-99, the crystals exploding without block damage).

// Respawn stages (DragonRespawnStage order).
const (
	respawnStart = iota
	respawnPreparing
	respawnSummoningPillars
	respawnSummoningDragon
	respawnEnd
)

var respawnStageNames = []string{"start", "preparing_to_summon_pillars", "summoning_pillars", "summoning_dragon", "end"}

// dragonSpawn is the fight's dragon spawn point above its origin.
var dragonSpawn = [3]float64{0, 128, 0}

type dragonFight struct {
	d *dimension

	gateways []int32

	ticksSinceDragonSeen      int
	aliveCrystals             int
	ticksSinceCrystalsScanned int
	ticksSinceLastPlayerScan  int
	emptyTime                 int

	dragonKilled       bool
	previouslyKilled   bool
	needsStateScanning bool
	dragonUUID         *[16]byte
	exitPortal         *world.Pos
	respawnStage       int // -1 when no respawn runs
	respawnTime        int
	respawnCrystals    [][16]byte

	// bar is the ServerBossEvent.
	barUUID    [16]byte
	barPlayers map[*Session]bool
	barVisible bool
	barProg    float32
	barName    any
}

// fightDataPath is the saved-data file of the dimension's fight.
func (s *Server) fightDataPath(d *dimension) string {
	return filepath.Join(world.DimensionDir(s.WorldDir, d.name), "data", "minecraft", "ender_dragon_fight.dat")
}

// ensureFight creates the End's fight the first time it ticks: the
// saved data when present (computeIfAbsent), else createDefault, then
// init.
func (s *Server) ensureFight(d *dimension) *dragonFight {
	if d.fight != nil {
		return d.fight
	}
	if d.name != dimEnd || d.terrain == nil {
		return nil
	}
	f := &dragonFight{d: d, needsStateScanning: true, respawnStage: -1, ticksSinceLastPlayerScan: 21,
		barPlayers: map[*Session]bool{}, barVisible: true, barProg: 1,
		barName: translatable("entity.minecraft.ender_dragon")}
	if s.WorldDir != "" {
		if v, err := readPlayerGzipNBT(s.fightDataPath(d)); err == nil {
			if root, ok := v.(map[string]any); ok {
				if data, ok := root["data"].(map[string]any); ok {
					f.load(data)
				}
			}
		}
	}
	var u [16]byte
	for i := range u {
		u[i] = byte(rand.Intn(256))
	}
	f.barUUID = u
	if len(f.gateways) == 0 {
		// ObjectArrayList(0..19) shuffled with the level seed.
		gw := make([]int32, 20)
		for i := range gw {
			gw[i] = int32(i)
		}
		r := worldgen.NewLegacy(s.worldSeed())
		for i := int32(len(gw)); i > 1; i-- {
			j := r.NextIntN(i)
			gw[i-1], gw[j] = gw[j], gw[i-1]
		}
		f.gateways = gw
	}
	d.fight = f
	return f
}

func (f *dragonFight) load(m map[string]any) {
	f.needsStateScanning = true
	if v, ok := m["needs_state_scanning"]; ok {
		f.needsStateScanning = nbtTruth(v)
	}
	f.dragonKilled = nbtTruth(m["dragon_killed"])
	f.previouslyKilled = nbtTruth(m["previously_killed"])
	f.respawnStage = -1
	if v, ok := m["respawn_stage"].(string); ok {
		for i, n := range respawnStageNames {
			if n == v {
				f.respawnStage = i
			}
		}
	}
	if v, ok := nbtInt32(m["respawn_time"]); ok {
		f.respawnTime = int(v)
	}
	if u, ok := m["dragon_uuid"].([]int32); ok && len(u) == 4 {
		id := uuidFromInts(u)
		f.dragonUUID = &id
	}
	if p, ok := nbtIntVec3(m["exit_portal_location"]); ok {
		f.exitPortal = &p
	}
	if l, ok := m["gateways"].(nbt.List); ok {
		for _, it := range l.Items {
			if v, ok := nbtInt32(it); ok {
				f.gateways = append(f.gateways, v)
			}
		}
	}
	if l, ok := m["respawn_crystals"].(nbt.List); ok {
		for _, it := range l.Items {
			if u, ok := it.([]int32); ok && len(u) == 4 {
				f.respawnCrystals = append(f.respawnCrystals, uuidFromInts(u))
			}
		}
	}
}

func uuidFromInts(u []int32) [16]byte {
	var id [16]byte
	for i, v := range u {
		id[i*4] = byte(v >> 24)
		id[i*4+1] = byte(v >> 16)
		id[i*4+2] = byte(v >> 8)
		id[i*4+3] = byte(v)
	}
	return id
}

// saveNBT is the fight's CODEC output inside the saved-data envelope.
func (f *dragonFight) saveNBT() map[string]any {
	data := map[string]any{
		"needs_state_scanning": nbtBool(f.needsStateScanning),
		"dragon_killed":        nbtBool(f.dragonKilled),
		"previously_killed":    nbtBool(f.previouslyKilled),
		"respawn_time":         int32(f.respawnTime),
	}
	if f.respawnStage >= 0 {
		data["respawn_stage"] = respawnStageNames[f.respawnStage]
	}
	if f.dragonUUID != nil {
		data["dragon_uuid"] = uuidInts(*f.dragonUUID)
	}
	if p := f.exitPortal; p != nil {
		data["exit_portal_location"] = []int32{int32(p.X), int32(p.Y), int32(p.Z)}
	}
	gw := nbt.List{Elem: nbt.TagInt}
	for _, g := range f.gateways {
		gw.Items = append(gw.Items, g)
	}
	if len(gw.Items) > 0 {
		data["gateways"] = gw
	}
	if len(f.respawnCrystals) > 0 {
		rc := nbt.List{Elem: nbt.TagIntArray}
		for _, u := range f.respawnCrystals {
			rc.Items = append(rc.Items, uuidInts(u))
		}
		data["respawn_crystals"] = rc
	}
	return map[string]any{"DataVersion": int32(4903), "data": data}
}

// writeFightData writes the saved data file.
func writeFightData(path string, v map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writePlayerGzipNBT(path, v)
}

// dragonFightTickStep is ServerLevel.tick's dragonFight.tick for the
// End, run before the level's entities tick. The level keeps ticking
// its fight for 300 ticks after the last player leaves (emptyTime).
func (s *Server) dragonFightTickStep() {
	d := s.dims[dimEnd]
	if d == nil || d.terrain == nil {
		return
	}
	s.drainGenEntities(d)
	occupied := s.dimHasAnySession(d.name)
	if d.fight == nil && !occupied {
		return
	}
	f := s.ensureFight(d)
	if f == nil {
		return
	}
	if occupied {
		f.emptyTime = 0
	} else if f.emptyTime++; f.emptyTime >= 300 {
		return
	}
	s.fightTick(f)
}

// dimHasAnySession reports whether a play session stands in dim.
func (s *Server) dimHasAnySession(dim string) bool {
	has := false
	s.sessions.Range(func(_, v any) bool {
		p := v.(*Session)
		if p.state.Load() == StatePlay && p.dimName() == dim {
			has = true
			return false
		}
		return true
	})
	return has
}

// drainGenEntities spawns the entities of the generated chunks handed
// out since the last tick.
func (s *Server) drainGenEntities(d *dimension) {
	t := d.terrain
	t.freshMu.Lock()
	ents := t.genEntities
	t.genEntities = nil
	t.freshMu.Unlock()
	if len(ents) > 0 {
		s.spawnGenEntities(d, ents)
	}
}

// fightTick is EnderDragonFight.tick.
func (s *Server) fightTick(f *dragonFight) {
	s.fightSetVisible(f, !f.dragonKilled)
	if f.ticksSinceLastPlayerScan++; f.ticksSinceLastPlayerScan >= 20 {
		s.fightUpdatePlayers(f)
		f.ticksSinceLastPlayerScan = 0
	}
	if len(f.barPlayers) == 0 {
		return
	}
	if !s.fightArenaLoaded(f) {
		return
	}
	if f.needsStateScanning {
		s.fightScanState(f)
		f.needsStateScanning = false
	}
	if f.respawnStage >= 0 {
		var crystals []*Entity
		for _, u := range f.respawnCrystals {
			if e := s.entityByUUID(u); e != nil && e.crystal != nil && !e.Dying {
				crystals = append(crystals, e)
			}
		}
		if len(crystals) == 0 {
			s.fightAbortRespawn(f)
			return
		}
		t := f.respawnTime
		f.respawnTime++
		s.fightRespawnStageTick(f, crystals, t)
	}
	if !f.dragonKilled {
		if f.dragonUUID == nil {
			s.fightFindOrCreateDragon(f)
			f.ticksSinceDragonSeen = 0
		} else if f.ticksSinceDragonSeen++; f.ticksSinceDragonSeen >= 1200 {
			s.fightFindOrCreateDragon(f)
			f.ticksSinceDragonSeen = 0
		}
		if f.ticksSinceCrystalsScanned++; f.ticksSinceCrystalsScanned >= 100 {
			s.fightUpdateCrystalCount(f)
			f.ticksSinceCrystalsScanned = 0
		}
	}
}

// fightArenaLoaded is isArenaLoaded under the DRAGON ticket: the 17x17
// chunks around the origin exist. Missing ones generate a few per tick,
// the way the ticket's chunk loading spreads over ticks.
func (s *Server) fightArenaLoaded(f *dragonFight) bool {
	missing := 0
	for cx := -8; cx <= 8; cx++ {
		for cz := -8; cz <= 8; cz++ {
			if f.d.w.PeekBaseChunk(cx, cz) != nil {
				continue
			}
			if missing < 24 {
				f.d.w.GetBlock(world.Pos{X: cx * 16, Y: f.d.minY, Z: cz * 16})
			}
			missing++
		}
	}
	return missing == 0
}

// entityByUUID finds a live entity by uuid.
func (s *Server) entityByUUID(u [16]byte) *Entity {
	var out *Entity
	s.entities.mu.RLock()
	for _, e := range s.entities.byID {
		if e.UUID == u {
			out = e
			break
		}
	}
	s.entities.mu.RUnlock()
	return out
}

// fightDragons is ServerLevel.getDragons: the level's live dragons.
func (s *Server) fightDragons(d *dimension) []*Entity {
	var out []*Entity
	s.entities.mu.RLock()
	for _, e := range s.entities.byID {
		if e.dragon != nil && e.Dim == d.name && !e.Dying {
			out = append(out, e)
		}
	}
	s.entities.mu.RUnlock()
	return out
}

// fightUpdatePlayers is updatePlayers: every live player of the level
// within 192 blocks of (0, 128, 0) joins the bar, the rest leave it.
func (s *Server) fightUpdatePlayers(f *dragonFight) {
	want := map[*Session]bool{}
	s.sessions.Range(func(_, v any) bool {
		p := v.(*Session)
		if p.state.Load() != StatePlay || !p.spawned.Load() || p.dimName() != f.d.name || p.dead {
			return true
		}
		x, y, z, _, _, _ := p.Pos()
		dx, dy, dz := x-dragonSpawn[0], y-dragonSpawn[1], z-dragonSpawn[2]
		if dx*dx+dy*dy+dz*dz <= 192*192 {
			want[p] = true
		}
		return true
	})
	for p := range want {
		if !f.barPlayers[p] {
			f.barPlayers[p] = true
			if f.barVisible {
				_ = p.write(p.ids().PlayBossEvent, s.fightBarPacket(f, 0))
			}
		}
	}
	for p := range f.barPlayers {
		if !want[p] {
			delete(f.barPlayers, p)
			if f.barVisible && p.state.Load() == StatePlay {
				_ = p.write(p.ids().PlayBossEvent, s.fightBarPacket(f, 1))
			}
		}
	}
}

// fightBarPacket builds one boss_event for the fight's bar.
func (s *Server) fightBarPacket(f *dragonFight, op int32) []byte {
	var b protocol.Buffer
	b.UUID(f.barUUID)
	b.VarInt(op)
	switch op {
	case 0:
		b.Raw(componentBytes(f.barName))
		b.Float(f.barProg)
		b.VarInt(0) // PINK
		b.VarInt(0) // PROGRESS
		b.Byte(0x02 | 0x04)
	case 2:
		b.Float(f.barProg)
	case 3:
		b.Raw(componentBytes(f.barName))
	}
	return b.Bytes()
}

func (s *Server) fightBarSend(f *dragonFight, op int32) {
	if !f.barVisible {
		return
	}
	body := s.fightBarPacket(f, op)
	for p := range f.barPlayers {
		if p.state.Load() == StatePlay {
			_ = p.write(p.ids().PlayBossEvent, body)
		}
	}
}

// fightSetVisible is ServerBossEvent.setVisible.
func (s *Server) fightSetVisible(f *dragonFight, v bool) {
	if v == f.barVisible {
		return
	}
	f.barVisible = v
	for p := range f.barPlayers {
		if p.state.Load() != StatePlay {
			continue
		}
		op := int32(1)
		if v {
			op = 0
		}
		_ = p.write(p.ids().PlayBossEvent, s.fightBarPacket(f, op))
	}
}

// fightSetProgress is ServerBossEvent.setProgress.
func (s *Server) fightSetProgress(f *dragonFight, p float32) {
	if p == f.barProg {
		return
	}
	f.barProg = p
	s.fightBarSend(f, 2)
}

// fightDropPlayer takes a disconnecting or travelling player off the bar
// (ServerBossEvent.removeAllPlayers on the level change).
func (s *Server) fightDropPlayer(sess *Session) {
	d := s.dims[dimEnd]
	if d == nil || d.fight == nil {
		return
	}
	if d.fight.barPlayers[sess] {
		delete(d.fight.barPlayers, sess)
		if d.fight.barVisible && sess.state.Load() == StatePlay {
			_ = sess.write(sess.ids().PlayBossEvent, s.fightBarPacket(d.fight, 1))
		}
	}
}

// fightScanState is scanState: the legacy-world scan of a fight that
// has no saved state yet.
func (s *Server) fightScanState(f *dragonFight) {
	active := s.fightHasActiveExitPortal(f)
	if active {
		f.previouslyKilled = true
	} else {
		f.previouslyKilled = false
		if _, ok := s.fightFindExitPortal(f); !ok {
			s.fightSpawnExitPortal(f, false)
		}
	}
	dragons := s.fightDragons(f.d)
	if len(dragons) == 0 {
		f.dragonKilled = true
	} else {
		dr := dragons[0]
		u := dr.UUID
		f.dragonUUID = &u
		f.dragonKilled = false
		if !active {
			s.dragonDiscard(dr)
			f.dragonUUID = nil
		}
	}
	if !f.previouslyKilled && f.dragonKilled {
		f.dragonKilled = false
	}
}

// fightHasActiveExitPortal is hasActiveExitPortal: an end portal block
// entity in the chunks -8..8. Exit portals stand on the origin column,
// so the scan reads the podium's footprint there.
func (s *Server) fightHasActiveExitPortal(f *dragonFight) bool {
	d := f.d
	portal := world.BlockStateMust("end_portal")
	buf := make([]world.Block, d.height)
	for x := -3; x <= 3; x++ {
		for z := -3; z <= 3; z++ {
			d.w.FillBox(world.Pos{X: x, Y: d.minY, Z: z}, world.Pos{X: x, Y: d.minY + d.height - 1, Z: z}, buf)
			for _, b := range buf {
				if b == portal {
					return true
				}
			}
		}
	}
	return false
}

// exitPortalAisles is the exit portal BlockPattern, top aisle first:
// '#' must be bedrock, ' ' matches anything.
var exitPortalAisles = [5][7]string{
	{"       ", "       ", "       ", "   #   ", "       ", "       ", "       "},
	{"       ", "       ", "       ", "   #   ", "       ", "       ", "       "},
	{"       ", "       ", "       ", "   #   ", "       ", "       ", "       "},
	{"  ###  ", " #   # ", "#     #", "#  #  #", "#     #", " #   # ", "  ###  "},
	{"       ", "  ###  ", " ##### ", " ##### ", " ##### ", "  ###  ", "       "},
}

// exitPortalMatches reports whether the pattern stands with its (3,3,3)
// cell - the pillar at the rim layer - at c.
func exitPortalMatches(d *dimension, c world.Pos) bool {
	bedrock := world.BlockStateMust("bedrock")
	for a, aisle := range exitPortalAisles {
		y := c.Y + 3 - a
		for r, row := range aisle {
			for col := 0; col < 7; col++ {
				if row[col] != '#' {
					continue
				}
				if d.w.GetBlock(world.Pos{X: c.X + col - 3, Y: y, Z: c.Z + r - 3}) != bedrock {
					return false
				}
			}
		}
	}
	return true
}

// fightFindExitPortal is findExitPortal: the pattern around a known
// end portal, else down the origin column from its MOTION_BLOCKING
// height. The first find records exitPortalLocation.
func (s *Server) fightFindExitPortal(f *dragonFight) (world.Pos, bool) {
	d := f.d
	if f.exitPortal != nil && exitPortalMatches(d, *f.exitPortal) {
		return *f.exitPortal, true
	}
	top := heightmapTop(d, 0, 0, worldgen.MotionBlocking)
	for y := top; y >= d.minY; y-- {
		// BlockPattern.find from (0, y, 0) searches the 7^3 cube
		// above it; the podium centre then sits three up and three in.
		for dy := 0; dy <= 3; dy++ {
			c := world.Pos{X: 0, Y: y + dy, Z: 0}
			if exitPortalMatches(d, c) {
				if f.exitPortal == nil {
					p := c
					f.exitPortal = &p
				}
				return c, true
			}
		}
	}
	return world.Pos{}, false
}

// heightmapTop is getHeightmapPos(type, (x, 0, z)).getY(): one above
// the highest matching block, minY for an empty column.
func heightmapTop(d *dimension, x, z int, pred func(world.Block) bool) int {
	buf := make([]world.Block, d.height)
	d.w.FillBox(world.Pos{X: x, Y: d.minY, Z: z}, world.Pos{X: x, Y: d.minY + d.height - 1, Z: z}, buf)
	for i := len(buf) - 1; i >= 0; i-- {
		if pred(buf[i]) {
			return d.minY + i + 1
		}
	}
	return d.minY
}

// fightSpawnExitPortal is spawnExitPortal: the location (once) is the
// origin column's MOTION_BLOCKING_NO_LEAVES top block, lowered through
// bedrock above y 63, clamped to minY+1; then EndPodiumFeature places.
func (s *Server) fightSpawnExitPortal(f *dragonFight, active bool) {
	d := f.d
	if f.exitPortal == nil {
		p := world.Pos{X: 0, Y: heightmapTop(d, 0, 0, worldgen.MotionBlockingNoLeaves) - 1, Z: 0}
		bedrock := world.BlockStateMust("bedrock")
		for d.w.GetBlock(p) == bedrock && p.Y > 63 {
			p.Y--
		}
		p.Y = max(d.minY+1, p.Y)
		f.exitPortal = &p
	}
	s.placeEndPodium(d, *f.exitPortal, active)
}

// placeEndPodium is EndPodiumFeature.place at origin: within 3.5 of the
// origin (3D block distance), below it bedrock inside 2.5 and end stone
// outside, above it air, on its layer a bedrock rim and the inner cells
// end portal (active) or air; then the four-high bedrock pillar and a
// wall torch on each side of its third block. The active podium
// replaces cells through dropPreviousAndSetBlock.
func (s *Server) placeEndPodium(d *dimension, o world.Pos, active bool) {
	bedrock := world.BlockStateMust("bedrock")
	endStone := world.BlockStateMust("end_stone")
	portal := world.BlockStateMust("end_portal")
	set := func(p world.Pos, b world.Block, drop bool) {
		cur := d.w.GetBlock(p)
		if cur == b {
			return
		}
		if drop && cur != world.Air {
			s.dropBlockForPodium(d, p, cur)
		}
		s.setNaturalBlock(d, p, b)
	}
	for z := o.Z - 4; z <= o.Z+4; z++ {
		for y := o.Y - 1; y <= o.Y+32; y++ {
			for x := o.X - 4; x <= o.X+4; x++ {
				dx, dy, dz := x-o.X, y-o.Y, z-o.Z
				d2 := float64(dx*dx + dy*dy + dz*dz)
				inner := d2 < 2.5*2.5
				if !inner && d2 >= 3.5*3.5 {
					continue
				}
				p := world.Pos{X: x, Y: y, Z: z}
				switch {
				case y < o.Y:
					if inner {
						set(p, bedrock, false)
					} else {
						set(p, endStone, active)
					}
				case y > o.Y:
					set(p, world.Air, active)
				case !inner:
					set(p, bedrock, false)
				case active:
					set(p, portal, true)
				default:
					set(p, world.Air, false)
				}
			}
		}
	}
	for i := 0; i < 4; i++ {
		set(world.Pos{X: o.X, Y: o.Y + i, Z: o.Z}, bedrock, false)
	}
	torch := world.BlockStateMust("wall_torch")
	for _, side := range []struct {
		facing string
		dx, dz int
	}{{"north", 0, -1}, {"east", 1, 0}, {"south", 0, 1}, {"west", -1, 0}} {
		st, _ := world.WithProperty(torch, "facing", side.facing)
		set(world.Pos{X: o.X + side.dx, Y: o.Y + 2, Z: o.Z + side.dz}, st, false)
	}
}

// dropBlockForPodium is the destroyBlock(pos, true) half of
// dropPreviousAndSetBlock: the break effect (level event 2001). The
// replaced block's loot is not dropped yet.
func (s *Server) dropBlockForPodium(d *dimension, p world.Pos, cur world.Block) {
	s.broadcastLevelEvent(d.name, 2001, p, int32(cur))
}

// fightFindOrCreateDragon is findOrCreateDragon.
func (s *Server) fightFindOrCreateDragon(f *dragonFight) {
	if dragons := s.fightDragons(f.d); len(dragons) > 0 {
		u := dragons[0].UUID
		f.dragonUUID = &u
		return
	}
	s.fightCreateDragon(f)
}

// fightCreateDragon is createNewDragon: a dragon at (0, 128, 0) facing
// a random yaw, in HOLDING_PATTERN, bound to this fight.
func (s *Server) fightCreateDragon(f *dragonFight) *Entity {
	e := s.newEnderDragon(f.d, dragonSpawn[0], dragonSpawn[1], dragonSpawn[2])
	e.Yaw = rand.Float32() * 360
	e.HeadYaw = e.Yaw
	e.dragon.fight = true
	s.dragonSetPhase(e, phaseHoldingPattern)
	e.dragon.phaseSent = phaseHoldingPattern
	s.spawnEnderDragon(e)
	u := e.UUID
	f.dragonUUID = &u
	return e
}

// fightUpdateDragon is updateDragon: the bar tracks the fight's dragon.
func (s *Server) fightUpdateDragon(f *dragonFight, e *Entity) {
	if f.dragonUUID == nil || e.UUID != *f.dragonUUID {
		return
	}
	max := e.MaxHealth
	if max <= 0 {
		max = 200
	}
	s.fightSetProgress(f, e.Health/max)
	f.ticksSinceDragonSeen = 0
	if e.CustomName != "" {
		name := componentFromStored(e.CustomName, e.CustomNameComp)
		if string(componentBytes(name)) != string(componentBytes(f.barName)) {
			f.barName = name
			s.fightBarSend(f, 3)
		}
	}
}

// fightUpdateCrystalCount is updateCrystalCount: the crystals standing
// in the ten spike columns.
func (s *Server) fightUpdateCrystalCount(f *dragonFight) {
	f.ticksSinceCrystalsScanned = 0
	f.aliveCrystals = len(s.spikeCrystals(f.d))
}

// spikeCrystals lists the crystals whose box meets a spike's column
// (EndSpike.getTopBoundingBox, the whole height).
func (s *Server) spikeCrystals(d *dimension) []*Entity {
	spikes := worldgen.EndSpikes(s.worldSeed())
	var out []*Entity
	s.entities.mu.RLock()
	for _, e := range s.entities.byID {
		if e.crystal == nil || e.Dying || e.Dim != d.name {
			continue
		}
		for _, sp := range spikes {
			r := float64(sp.Radius)
			if e.X-1 < float64(sp.X)+r && e.X+1 > float64(sp.X)-r && e.Z-1 < float64(sp.Z)+r && e.Z+1 > float64(sp.Z)-r {
				out = append(out, e)
				break
			}
		}
	}
	s.entities.mu.RUnlock()
	return out
}

// fightSetDragonKilled is setDragonKilled.
func (s *Server) fightSetDragonKilled(f *dragonFight, e *Entity) {
	if f.dragonUUID == nil || e.UUID != *f.dragonUUID {
		return
	}
	s.fightSetProgress(f, 0)
	s.fightSetVisible(f, false)
	s.fightSpawnExitPortal(f, true)
	s.fightSpawnNewGateway(f)
	if !f.previouslyKilled {
		top := heightmapTop(f.d, 0, 0, worldgen.MotionBlocking)
		s.setNaturalBlock(f.d, world.Pos{X: 0, Y: top, Z: 0}, world.BlockStateMust("dragon_egg"))
	}
	f.previouslyKilled = true
	f.dragonKilled = true
}

// gatewayPos is the ring position of gateway slot i.
func gatewayPos(i int32) world.Pos {
	a := 2.0 * (-math.Pi + 0.15707963267948966*float64(i))
	return world.Pos{X: int(math.Floor(96 * math.Cos(a))), Y: 75, Z: int(math.Floor(96 * math.Sin(a)))}
}

// fightSpawnNewGateway is spawnNewGateway: the last slot of the list.
func (s *Server) fightSpawnNewGateway(f *dragonFight) {
	if len(f.gateways) == 0 {
		return
	}
	i := f.gateways[len(f.gateways)-1]
	f.gateways = f.gateways[:len(f.gateways)-1]
	p := gatewayPos(i)
	s.broadcastLevelEvent(f.d.name, 3000, p, 0)
	s.placeEndGateway(f.d, p)
}

// placeEndGateway is EndGatewayFeature.place for end_gateway_delayed
// (exact false, no exit): the gateway at the centre, air on its layer,
// bedrock above and below it and on the cross arms of the other
// layers, air elsewhere in the 3x5x3 box.
func (s *Server) placeEndGateway(d *dimension, o world.Pos) {
	bedrock := world.BlockStateMust("bedrock")
	gateway := world.BlockStateMust("end_gateway")
	for z := o.Z - 1; z <= o.Z+1; z++ {
		for y := o.Y - 2; y <= o.Y+2; y++ {
			for x := o.X - 1; x <= o.X+1; x++ {
				sameX, sameY, sameZ := x == o.X, y == o.Y, z == o.Z
				dy2 := y-o.Y == 2 || o.Y-y == 2
				p := world.Pos{X: x, Y: y, Z: z}
				var b world.Block
				switch {
				case sameX && sameY && sameZ:
					b = gateway
				case sameY:
					b = world.Air
				case dy2 && sameX && sameZ:
					b = bedrock
				case (sameX || sameZ) && !dy2:
					b = bedrock
				default:
					b = world.Air
				}
				if d.w.GetBlock(p) != b {
					s.setNaturalBlock(d, p, b)
				}
			}
		}
	}
}

// fightCrystalDestroyed is onCrystalDestroyed.
func (s *Server) fightCrystalDestroyed(d *dimension, c *Entity, attacker *Session) {
	f := d.fight
	if f.respawnStage >= 0 {
		for _, u := range f.respawnCrystals {
			if u == c.UUID {
				s.fightAbortRespawn(f)
				return
			}
		}
	}
	s.fightUpdateCrystalCount(f)
	if f.dragonUUID != nil {
		if dr := s.entityByUUID(*f.dragonUUID); dr != nil && dr.dragon != nil {
			s.dragonCrystalDestroyed(dr, c, attacker)
		}
	}
}

// fightAbortRespawn is abortRespawnSequence.
func (s *Server) fightAbortRespawn(f *dragonFight) {
	f.respawnStage = -1
	f.respawnTime = 0
	s.fightResetSpikeCrystals(f)
	s.fightSpawnExitPortal(f, true)
}

// fightResetSpikeCrystals is resetSpikeCrystals.
func (s *Server) fightResetSpikeCrystals(f *dragonFight) {
	for _, c := range s.spikeCrystals(f.d) {
		c.Invulnerable = false
		crystalSetBeam(c, nil)
	}
}

// fightTryRespawn is tryRespawn: a killed dragon's exit portal with a
// crystal on each of its four sides (three out from the pillar, one up)
// starts the respawn.
func (s *Server) fightTryRespawn(d *dimension) {
	f := d.fight
	if f == nil || !f.dragonKilled || f.respawnStage >= 0 {
		return
	}
	if f.exitPortal == nil {
		if _, ok := s.fightFindExitPortal(f); !ok {
			s.fightSpawnExitPortal(f, true)
		}
	}
	loc := *f.exitPortal
	var crystals []*Entity
	for _, side := range [][2]int{{0, -1}, {1, 0}, {0, 1}, {-1, 0}} {
		bx, by, bz := float64(loc.X+3*side[0]), float64(loc.Y+1), float64(loc.Z+3*side[1])
		var found []*Entity
		s.entities.mu.RLock()
		for _, e := range s.entities.byID {
			if e.crystal == nil || e.Dying || e.Dim != d.name {
				continue
			}
			if e.X-1 < bx+1 && e.X+1 > bx && e.Y < by+1 && e.Y+2 > by && e.Z-1 < bz+1 && e.Z+1 > bz {
				found = append(found, e)
			}
		}
		s.entities.mu.RUnlock()
		if len(found) == 0 {
			return
		}
		crystals = append(crystals, found...)
	}
	s.fightRespawnDragon(f, crystals)
}

// fightRespawnDragon is respawnDragon: every bedrock and end portal
// cell of each exit portal match turns to end stone, the inactive
// podium returns and the sequence starts.
func (s *Server) fightRespawnDragon(f *dragonFight, crystals []*Entity) {
	if !f.dragonKilled || f.respawnStage >= 0 {
		return
	}
	bedrock := world.BlockStateMust("bedrock")
	portal := world.BlockStateMust("end_portal")
	endStone := world.BlockStateMust("end_stone")
	for guard := 0; guard < 8; guard++ {
		c, ok := s.fightFindExitPortal(f)
		if !ok {
			break
		}
		for a := 0; a < 5; a++ {
			for r := 0; r < 7; r++ {
				for col := 0; col < 7; col++ {
					p := world.Pos{X: c.X + col - 3, Y: c.Y + 3 - a, Z: c.Z + r - 3}
					if b := f.d.w.GetBlock(p); b == bedrock || b == portal {
						s.setNaturalBlock(f.d, p, endStone)
					}
				}
			}
		}
	}
	f.respawnStage = respawnStart
	f.respawnTime = 0
	s.fightSpawnExitPortal(f, false)
	f.respawnCrystals = nil
	for _, c := range crystals {
		f.respawnCrystals = append(f.respawnCrystals, c.UUID)
	}
}

// fightSetRespawnStage is setRespawnStage.
func (s *Server) fightSetRespawnStage(f *dragonFight, stage int) {
	f.respawnTime = 0
	if stage == respawnEnd {
		f.respawnStage = -1
		f.dragonKilled = false
		if dr := s.fightCreateDragon(f); dr != nil {
			for p := range f.barPlayers {
				s.fireSummonedEntity(p, "ender_dragon")
			}
		}
		return
	}
	f.respawnStage = stage
}

var dragonBeamCentre = world.Pos{X: 0, Y: 128, Z: 0}

// fightRespawnStageTick is DragonRespawnStage.tick.
func (s *Server) fightRespawnStageTick(f *dragonFight, crystals []*Entity, t int) {
	d := f.d
	switch f.respawnStage {
	case respawnStart:
		for _, c := range crystals {
			crystalSetBeam(c, &dragonBeamCentre)
		}
		s.fightSetRespawnStage(f, respawnPreparing)
	case respawnPreparing:
		if t < 100 {
			if t == 0 || t == 50 || t == 51 || t == 52 || t >= 95 {
				s.broadcastLevelEvent(d.name, 3001, dragonBeamCentre, 0)
			}
		} else {
			s.fightSetRespawnStage(f, respawnSummoningPillars)
		}
	case respawnSummoningPillars:
		first, last := t%40 == 0, t%40 == 39
		if !first && !last {
			return
		}
		spikes := worldgen.EndSpikes(s.worldSeed())
		i := t / 40
		if i >= len(spikes) {
			if first {
				s.fightSetRespawnStage(f, respawnSummoningDragon)
			}
			return
		}
		sp := spikes[i]
		if first {
			for _, c := range crystals {
				crystalSetBeam(c, &world.Pos{X: int(sp.X), Y: int(sp.Height) + 1, Z: int(sp.Z)})
			}
			return
		}
		for z := int(sp.Z) - 10; z <= int(sp.Z)+10; z++ {
			for y := int(sp.Height) - 10; y <= int(sp.Height)+10; y++ {
				for x := int(sp.X) - 10; x <= int(sp.X)+10; x++ {
					p := world.Pos{X: x, Y: y, Z: z}
					if d.w.GetBlock(p) != world.Air {
						s.setNaturalBlock(d, p, world.Air)
					}
				}
			}
		}
		src := &Entity{Dim: d.name, ID: -1}
		s.explodePower(src, float64(sp.X)+0.5, float64(sp.Height), float64(sp.Z)+0.5, 5, true)
		s.placeRespawnSpike(d, sp)
	case respawnSummoningDragon:
		switch {
		case t >= 100:
			s.fightSetRespawnStage(f, respawnEnd)
			s.fightResetSpikeCrystals(f)
			for _, c := range crystals {
				crystalSetBeam(c, nil)
				s.explodePower(c, c.X, c.Y, c.Z, 6, false)
				s.crystalDiscard(c)
			}
		case t >= 80:
			s.broadcastLevelEvent(d.name, 3001, dragonBeamCentre, 0)
		case t == 0:
			for _, c := range crystals {
				crystalSetBeam(c, &dragonBeamCentre)
			}
		case t < 5:
			s.broadcastLevelEvent(d.name, 3001, dragonBeamCentre, 0)
		}
	}
}

// placeRespawnSpike is the END_SPIKE feature with crystal_invulnerable
// and a (0,128,0) beam, placed for one spike into the live level.
func (s *Server) placeRespawnSpike(d *dimension, sp worldgen.EndSpike) {
	r := worldgen.NewLegacy(rand.Int63())
	at := func(p worldgen.BlockPos) world.Block {
		return d.w.GetBlock(world.Pos{X: int(p[0]), Y: int(p[1]), Z: int(p[2])})
	}
	set := func(p worldgen.BlockPos, b world.Block) {
		q := world.Pos{X: int(p[0]), Y: int(p[1]), Z: int(p[2])}
		if d.w.GetBlock(q) != b {
			s.setNaturalBlock(d, q, b)
		}
	}
	yaw := worldgen.PlaceEndSpike(sp, int32(d.minY), at, set, r)
	e := newEndCrystal(d.name, float64(sp.X)+0.5, float64(sp.Height+1), float64(sp.Z)+0.5)
	e.Yaw = yaw
	e.Invulnerable = true
	crystalSetBeam(e, &dragonBeamCentre)
	s.spawnEndCrystal(e)
}

// crystalDiscard removes a crystal without the hurt path.
func (s *Server) crystalDiscard(c *Entity) {
	if _, ok := s.entities.GetPtr(c.ID); !ok {
		return
	}
	c.Dying = true
	s.entities.Remove(c.ID)
	s.broadcastEntityRemoveIn(c.Dim, nil, []int32{c.ID})
}
