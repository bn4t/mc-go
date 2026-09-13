package server

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"

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
	Dim     string `json:"dim,omitempty"`
	X, Y, Z int
	Items   [chestSlots]ItemStack `json:"items"`
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
	p := playerPersist{Inv: sess.inv, Carried: sess.carried}
	sess.invMu.RUnlock()
	p.XPTotal = sess.xpTotal
	p.XPLevel = sess.xpLevel
	p.XPProgress = sess.xpProgress
	p.Health = sess.health
	p.Food = sess.food
	p.Saturation = sess.saturation
	p.Dim = sess.dimName()
	p.X, p.Y, p.Z, _, _, _ = sess.Pos()
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
			f.Chests = append(f.Chests, chestPersist{Dim: name, X: pos.X, Y: pos.Y, Z: pos.Z, Items: c.slots})
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
		d.chests.chests[world.Pos{X: cp.X, Y: cp.Y, Z: cp.Z}] = &chestData{slots: cp.Items}
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
	s.savedPlayers = f.Players
	return nil
}

// applySavedPlayer loads a player's stored state into a joining
// session, if any.
func (s *Server) applySavedPlayer(sess *Session) {
	p, ok := s.savedPlayers[sess.getName()]
	if !ok {
		return
	}
	sess.invMu.Lock()
	sess.inv = p.Inv
	sess.carried = p.Carried
	sess.invMu.Unlock()
	sess.xpTotal = p.XPTotal
	sess.xpLevel = p.XPLevel
	sess.xpProgress = p.XPProgress
	sess.health = p.Health
	sess.food = p.Food
	sess.saturation = p.Saturation
	// Vanilla resumes the player where they logged out: dimension and
	// position both restore.
	if d := s.dimensionByName(p.Dim); d != nil && p.Y != 0 {
		sess.dim.Store(d)
		sess.setPos(p.X, p.Y, p.Z, 0, 0, false)
	}
}
