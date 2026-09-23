package server

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"mc-go/internal/data"
	"mc-go/internal/world"
)

// Server data persistence: player state (inventory, XP, survival
// stats) and block entities (sign text, chest contents), saved as
// versioned JSON next to the block overrides. WorldPath keeps its
// meaning for blocks; DataPath defaults to WorldPath with a ".data"
// suffix and empty disables data persistence.

type playerPersist struct {
	Inv        [slotCount]ItemStack `json:"inv"`
	Carried    ItemStack            `json:"carried"`
	Ender      [27]ItemStack        `json:"ender"`
	Body       ItemStack            `json:"body"`
	XPTotal    int32                `json:"xp_total"`
	XPLevel    int32                `json:"xp_level"`
	XPProgress float32              `json:"xp_progress"`
	Health     float32              `json:"health"`
	Food       int32                `json:"food"`
	Saturation float32              `json:"saturation"`
	Dim        string               `json:"dim,omitempty"`
	X          float64              `json:"x,omitempty"`
	Y          float64              `json:"y,omitempty"`
	Z          float64              `json:"z,omitempty"`
	// The fields below back the vanilla-format player file
	// (players/data/<uuid>.dat); the JSON store carries them too.
	UUID           [16]byte `json:"uuid,omitempty"`
	Yaw, Pitch     float32  `json:",omitempty"`
	OnGround       bool     `json:"on_ground,omitempty"`
	FallFlying     bool     `json:"fall_flying,omitempty"`
	Flying         bool     `json:"flying,omitempty"`
	Absorb         float32  `json:"absorb,omitempty"`
	Exhaustion     float32  `json:"exhaustion,omitempty"`
	FoodTick       int32    `json:"food_tick,omitempty"`
	XPSeed         int32    `json:"xp_seed,omitempty"`
	Fire           int32    `json:"fire"`
	SleepTimer     int32    `json:"sleep_timer,omitempty"`
	PortalCooldown int32    `json:"portal_cooldown,omitempty"`
	FallDistance   float32  `json:"fall_distance,omitempty"`
	Held           int32    `json:"held,omitempty"`
	GameMode       int32    `json:"game_mode"`
	// GameModeSaved marks entries that genuinely recorded a mode;
	// JSON files from before game-mode persistence carry a zero here
	// and must not force survival on their players.
	GameModeSaved bool `json:"game_mode_saved,omitempty"`
	// PrevGameMode is vanilla's previousPlayerGameType: the mode held
	// before the last switch, or -1 when the player never switched.
	PrevGameMode int32 `json:"prev_game_mode"`
	// Stats carries the player's counters, keyed "category:id". The
	// StatsOrder list preserves the stat table's insertion history —
	// it is what a vanilla stats file's key order reproduces, and the
	// Award Statistics emit order depends on it.
	Stats      map[string]int32 `json:"stats,omitempty"`
	StatsOrder []string         `json:"stats_order,omitempty"`
	// Advancements persists criterion progress keyed by advancement id:
	// done flags and per-criterion obtain times, mirroring vanilla's
	// advancements/<uuid>.json store.
	Advancements map[string]savedAdv `json:"advancements,omitempty"`
	// RecipeBook mirrors vanilla's recipeBook compound: the known
	// recipe ids, the display ids the client already marked seen, and
	// the per-category open/filtering toggles.
	UnlockedRecipes []string `json:"unlocked_recipes,omitempty"`
	SeenRecipes     []int32  `json:"seen_recipes,omitempty"`
	BookOpen        [4]bool  `json:"book_open"`
	BookFilter      [4]bool  `json:"book_filter"`
}

// savedAdv is one advancement's persisted progress.
type savedAdv struct {
	Done     bool             `json:"done,omitempty"`
	Criteria map[string]int64 `json:"criteria,omitempty"`
}

// signPersist is one sign's state on disk. Its styling travels with it:
// the dye colour and glow of each side and whether the sign is waxed are
// part of the block entity, not of the block, so dropping them here would
// lose them on the next load - and change what a joining client is sent.
// An unstyled side stores nothing and reads back as black, unglowing.
type signPersist struct {
	Dim          string `json:"dim,omitempty"`
	X, Y, Z      int
	Front        [4]string `json:"front"`
	Back         [4]string `json:"back"`
	FrontColor   string    `json:"frontColor,omitempty"`
	BackColor    string    `json:"backColor,omitempty"`
	FrontGlowing bool      `json:"frontGlowing,omitempty"`
	BackGlowing  bool      `json:"backGlowing,omitempty"`
	Waxed        bool      `json:"waxed,omitempty"`
}

type chestPersist struct {
	Dim       string `json:"dim,omitempty"`
	X, Y, Z   int
	Items     [chestSlots]ItemStack `json:"items"`
	LootTable string                `json:"loot_table,omitempty"`
	LootSeed  int64                 `json:"loot_seed,omitempty"`
}

// containerPersist covers the generic container family. The block name
// is stored so a reload can rebuild the menu spec without reading the
// world (the data file may be loaded before any chunk exists).
type containerPersist struct {
	Dim      string `json:"dim,omitempty"`
	X, Y, Z  int
	Block    string      `json:"block"`
	Items    []ItemStack `json:"items"`
	BrewTime int32       `json:"brew_time,omitempty"`
	Fuel     int32       `json:"fuel,omitempty"`
	Disabled uint16      `json:"disabled,omitempty"`
}

type serverDataFile struct {
	Version    int                      `json:"version"`
	Players    map[string]playerPersist `json:"players"`
	Signs      []signPersist            `json:"signs"`
	Chests     []chestPersist           `json:"chests"`
	Containers []containerPersist       `json:"containers,omitempty"`
}

const serverDataVersion = 3

var dataSaveMu sync.Mutex

// dataPath returns the effective data file for this server.
func (s *Server) dataPath() string {
	if s.DataPath != "" {
		return s.DataPath
	}
	if s.WorldPath == "" {
		return ""
	}
	return s.WorldPath + ".data.json"
}

// snapshotPlayer captures a session's persistent state.
func snapshotPlayer(sess *Session) playerPersist {
	sess.invMu.RLock()
	p := playerPersist{Inv: sess.inv, Carried: sess.carried, Ender: sess.ender, Body: sess.bodyArmor}
	sess.invMu.RUnlock()
	p.XPTotal = sess.xpTotal
	p.XPLevel = sess.xpLevel
	p.XPProgress = sess.xpProgress
	p.XPSeed = sess.enchantSeed
	p.Health = sess.health
	p.Food = sess.food
	p.Saturation = sess.saturation
	p.Exhaustion = sess.exhaustion
	p.FoodTick = int32(sess.foodTickTimer)
	p.Fire = int32(sess.fireTicks)
	if p.Fire <= 0 {
		p.Fire = -20
	}
	p.Absorb = sess.absorb
	p.PortalCooldown = int32(sess.portalCooldownTicks)
	p.Flying = sess.flying
	p.UUID = sess.getUUID()
	p.Dim = sess.dimName()
	p.X, p.Y, p.Z, p.Yaw, p.Pitch, p.OnGround = sess.Pos()
	p.Held = sess.held.Load()
	sess.modeMu.RLock()
	p.GameMode = int32(sess.gameMode)
	p.GameModeSaved = true
	p.PrevGameMode = sess.prevGameMode
	sess.modeMu.RUnlock()
	sess.statMu.Lock()
	if st := sess.stats; st != nil && len(st.counts) > 0 {
		p.Stats = make(map[string]int32, len(st.counts))
		// Vanilla's stats file iterates its map when writing, so the
		// stored key order is the map's iteration order — the order
		// the entries reload in.
		order := fastutilIterDesc(st.insertOrder)
		for _, k := range order {
			key := statPersistKey(k)
			p.Stats[key] = st.counts[k]
			p.StatsOrder = append(p.StatsOrder, key)
		}
	}
	sess.statMu.Unlock()
	sess.recipesMu.Lock()
	for idx := range sess.unlockedRecipes {
		p.UnlockedRecipes = append(p.UnlockedRecipes, data.Recipes[idx].ID)
	}
	sess.recipesMu.Unlock()
	sess.bookMu.Lock()
	for id := range sess.seenRecipes {
		p.SeenRecipes = append(p.SeenRecipes, id)
	}
	p.BookOpen, p.BookFilter = sess.bookOpen, sess.bookFilter
	sess.bookMu.Unlock()
	sess.advMu.Lock()
	if len(sess.advCrits) > 0 || len(sess.advDone) > 0 {
		p.Advancements = map[string]savedAdv{}
		for id, crits := range sess.advCrits {
			e := p.Advancements[id]
			e.Criteria = make(map[string]int64, len(crits))
			for c, t := range crits {
				e.Criteria[c] = t
			}
			p.Advancements[id] = e
		}
		for id := range sess.advDone {
			e := p.Advancements[id]
			e.Done = true
			p.Advancements[id] = e
		}
	}
	sess.advMu.Unlock()
	return p
}

// SaveServerData writes players and block entities to the data file.
func (s *Server) SaveServerData() error {
	path := s.dataPath()
	if path == "" {
		return fmt.Errorf("server: data persistence disabled")
	}
	dataSaveMu.Lock()
	defer dataSaveMu.Unlock()

	f := serverDataFile{Version: serverDataVersion, Players: map[string]playerPersist{}}
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if name := sess.getName(); name != "" && sess.state.Load() == StatePlay {
			f.Players[name] = snapshotPlayer(sess)
		}
		return true
	})
	for name, d := range s.dims {
		d.signs.mu.Lock()
		for pos, st := range d.signs.signs {
			f.Signs = append(f.Signs, signPersist{
				Dim: name, X: pos.X, Y: pos.Y, Z: pos.Z,
				Front: st.Front, Back: st.Back,
				FrontColor: st.frontColor, BackColor: st.backColor,
				FrontGlowing: st.frontGlowing, BackGlowing: st.backGlowing,
				Waxed: st.waxed,
			})
		}
		d.signs.mu.Unlock()
		d.chests.mu.Lock()
		for pos, c := range d.chests.chests {
			f.Chests = append(f.Chests, chestPersist{Dim: name, X: pos.X, Y: pos.Y, Z: pos.Z, Items: c.slots, LootTable: c.lootTable, LootSeed: c.lootSeed})
		}
		d.chests.mu.Unlock()
		d.containers.mu.Lock()
		for pos, c := range d.containers.containers {
			f.Containers = append(f.Containers, containerPersist{
				Dim: name, X: pos.X, Y: pos.Y, Z: pos.Z, Block: c.block,
				Items: c.slots, BrewTime: c.brewTime, Fuel: c.fuel, Disabled: c.disabled,
			})
		}
		d.containers.mu.Unlock()
	}

	raw, err := json.Marshal(f)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

// LoadServerData restores players and block entities from the data
// file; a missing file is not an error (fresh server).
func (s *Server) LoadServerData() error {
	path := s.dataPath()
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var f serverDataFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return err
	}
	if f.Version != serverDataVersion {
		return fmt.Errorf("server: data version %d, want %d", f.Version, serverDataVersion)
	}
	// Pre-version-2 files carry no Dim; their entries belong to the
	// overworld, matching the flat world they were written against.
	for _, sp := range f.Signs {
		d := s.dims[sp.Dim]
		if d == nil {
			d = s.dims[dimOverworld]
		}
		d.signs.mu.Lock()
		if d.signs.signs == nil {
			d.signs.signs = make(map[world.Pos]*signText)
		}
		st := signText{
			Front: sp.Front, Back: sp.Back,
			frontColor: sp.FrontColor, backColor: sp.BackColor,
			frontGlowing: sp.FrontGlowing, backGlowing: sp.BackGlowing,
			waxed: sp.Waxed,
		}
		d.signs.signs[world.Pos{X: sp.X, Y: sp.Y, Z: sp.Z}] = &st
		d.signs.mu.Unlock()
	}
	for _, cp := range f.Chests {
		d := s.dims[cp.Dim]
		if d == nil {
			d = s.dims[dimOverworld]
		}
		d.chests.mu.Lock()
		if d.chests.chests == nil {
			d.chests.chests = make(map[world.Pos]*chestData)
		}
		d.chests.chests[world.Pos{X: cp.X, Y: cp.Y, Z: cp.Z}] = &chestData{slots: cp.Items, lootTable: cp.LootTable, lootSeed: cp.LootSeed}
		d.chests.mu.Unlock()
	}
	// Generic containers rebuild their spec from the stored block name,
	// which also restores the shulker box's color and drop item.
	for _, cp := range f.Containers {
		d := s.dims[cp.Dim]
		if d == nil {
			d = s.dims[dimOverworld]
		}
		blk, ok := world.BlockState(cp.Block)
		if !ok {
			continue
		}
		spec, ok := containerSpecFor(blk)
		if !ok {
			continue
		}
		item, _ := world.ItemIDForBlockState(blk)
		c := &containerData{
			spec: spec, slots: make([]ItemStack, spec.slots), block: cp.Block, item: item,
			brewTime: cp.BrewTime, fuel: cp.Fuel, disabled: cp.Disabled,
		}
		copy(c.slots, cp.Items)
		d.containers.mu.Lock()
		if d.containers.containers == nil {
			d.containers.containers = make(map[world.Pos]*containerData)
		}
		d.containers.containers[world.Pos{X: cp.X, Y: cp.Y, Z: cp.Z}] = c
		d.containers.mu.Unlock()
	}
	s.savedPlayersMu.Lock()
	s.savedPlayers = f.Players
	s.savedPlayersMu.Unlock()
	return nil
}

// applySavedPlayer loads a player's stored state into a joining
// session, if any. Entries keyed by player name come from the JSON
// store; entries a vanilla-format world loaded come keyed by uuid.
func (s *Server) applySavedPlayer(sess *Session) {
	s.savedPlayersMu.Lock()
	p, ok := s.savedPlayers[sess.getName()]
	if !ok {
		p, ok = s.savedPlayers[DashUUID(sess.getUUID())]
	}
	s.savedPlayersMu.Unlock()
	if !ok {
		return
	}
	sess.invMu.Lock()
	sess.inv = p.Inv
	sess.carried = p.Carried
	sess.ender = p.Ender
	sess.bodyArmor = p.Body
	sess.invMu.Unlock()
	sess.xpTotal = p.XPTotal
	sess.xpLevel = p.XPLevel
	sess.xpProgress = p.XPProgress
	sess.enchantSeed = p.XPSeed
	sess.health = p.Health
	sess.food = p.Food
	sess.saturation = p.Saturation
	sess.exhaustion = p.Exhaustion
	sess.foodTickTimer = int(p.FoodTick)
	if p.Fire > 0 {
		sess.fireTicks = p.Fire
	}
	sess.absorb = p.Absorb
	sess.portalCooldownTicks = int(p.PortalCooldown)
	sess.flying = p.Flying
	if p.Held >= 0 && p.Held <= 8 {
		sess.held.Store(p.Held)
	}
	if p.GameModeSaved && p.GameMode >= GameModeSurvival && p.GameMode <= GameModeSpectator {
		sess.modeMu.Lock()
		sess.gameMode = byte(p.GameMode)
		sess.prevGameMode = p.PrevGameMode
		sess.modeMu.Unlock()
	}
	if len(p.Advancements) > 0 {
		sess.pendingAdv = p.Advancements
	}
	if len(p.UnlockedRecipes) > 0 || len(p.SeenRecipes) > 0 ||
		p.BookOpen != [4]bool{} || p.BookFilter != [4]bool{} {
		sess.recipesMu.Lock()
		if sess.unlockedRecipes == nil {
			sess.unlockedRecipes = map[int]bool{}
		}
		for _, id := range p.UnlockedRecipes {
			if idx, ok := recipeIndex[strings.TrimPrefix(id, "minecraft:")]; ok {
				sess.unlockedRecipes[idx] = true
			}
		}
		sess.recipesMu.Unlock()
		sess.bookMu.Lock()
		if sess.seenRecipes == nil {
			sess.seenRecipes = map[int32]bool{}
		}
		for _, id := range p.SeenRecipes {
			sess.seenRecipes[id] = true
		}
		sess.bookOpen, sess.bookFilter = p.BookOpen, p.BookFilter
		sess.bookMu.Unlock()
	}
	if len(p.Stats) > 0 {
		sess.statMu.Lock()
		st := sess.ensureStats()
		for _, key := range p.StatsOrder {
			k, ok := parseStatPersistKey(key)
			if !ok {
				continue
			}
			st.counts[k] = p.Stats[key]
			st.insertOrder = append(st.insertOrder, k)
		}
		sess.statMu.Unlock()
	}
	// Vanilla resumes the player where they logged out: dimension and
	// position both restore.
	if d := s.dimensionByName(p.Dim); d != nil && p.Y != 0 {
		sess.dim.Store(d)
		sess.setPos(p.X, p.Y, p.Z, p.Yaw, p.Pitch, p.OnGround)
	}
}
