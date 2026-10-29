package server

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"mc-go/internal/nbt"
	"mc-go/internal/world"
)

// Player files in the real server's format: WorldDir mode writes each
// player's state as gzip NBT at players/data/<uuid>.dat, the file
// vanilla writes on every save-all and disconnect. The field layout is
// the one a 26.2 save produces, measured from a live vanilla dump.

// playerDatCompound renders a playerPersist the way vanilla's
// Player.save writes it: inventory slots are menu slots on mc-go's
// side but vanilla numbers the hotbar 0-8 and keeps armour plus the
// offhand under `equipment`, and the recipe book stores recipe names,
// not indices.
func playerDatCompound(p playerPersist) map[string]any {
	m := map[string]any{
		"AbsorptionAmount":              p.Absorb,
		"Air":                           int16(p.Air),
		"Brain":                         map[string]any{"memories": map[string]any{}},
		"DataVersion":                   int32(world.DataVersion26_2),
		"DeathTime":                     int16(0),
		"Dimension":                     p.Dim,
		"FallFlying":                    nbtBool(p.FallFlying),
		"Fire":                          int16(p.Fire),
		"Health":                        p.Health,
		"HurtTime":                      int16(0),
		"Invulnerable":                  int8(0),
		"OnGround":                      nbtBool(p.OnGround),
		"PortalCooldown":                p.PortalCooldown,
		"Pos":                           nbt.List{Elem: nbt.TagDouble, Items: []any{p.X, p.Y, p.Z}},
		"Rotation":                      nbt.List{Elem: nbt.TagFloat, Items: []any{p.Yaw, p.Pitch}},
		"Score":                         int32(0),
		"SelectedItemSlot":              p.Held,
		"SleepTimer":                    int16(p.SleepTimer),
		"UUID":                          uuidIntArray(p.UUID),
		"XpLevel":                       p.XPLevel,
		"XpP":                           p.XPProgress,
		"XpSeed":                        p.XPSeed,
		"XpTotal":                       p.XPTotal,
		"playerGameType":                p.GameMode,
		"previousPlayerGameType":        p.PrevGameMode,
		"seenCredits":                   boolByte(p.SeenCredits),
		"spawn_extra_particles_on_fall": int8(0),
		"fall_distance":                 float32(p.FallDistance),
		"current_impulse_context_reset_grace_time": int64(0),
		"foodLevel":           p.Food,
		"foodSaturationLevel": p.Saturation,
		"foodExhaustionLevel": p.Exhaustion,
		"foodTickTimer":       p.FoodTick,
		"warden_spawn_tracker": map[string]any{
			"cooldown_ticks":           int32(0),
			"ticks_since_last_warning": int32(0),
			"warning_level":            int32(0),
		},
		"Motion": nbt.List{Elem: nbt.TagDouble, Items: []any{0.0, 0.0, 0.0}},
	}
	creative := p.GameMode == GameModeCreative
	m["abilities"] = map[string]any{
		"flySpeed":     float32(0.05),
		"flying":       nbtBool(p.Flying),
		"instabuild":   nbtBool(creative),
		"invulnerable": int8(0),
		"mayBuild":     int8(1),
		"mayfly":       nbtBool(creative),
		"walkSpeed":    float32(0.1),
	}
	// Vanilla instantiates a fixed set of player attributes and writes
	// each with its base value; the four below are the ones a fresh
	// save shows, in file order.
	m["attributes"] = nbt.List{Elem: nbt.TagCompound, Items: []any{
		map[string]any{"base": 3.0, "id": "minecraft:entity_interaction_range"},
		map[string]any{"base": 60000000.0, "id": "minecraft:waypoint_transmit_range"},
		map[string]any{"base": 4.5, "id": "minecraft:block_interaction_range"},
		map[string]any{"base": 0.1, "id": "minecraft:movement_speed"},
	}}
	// Inventory: mc's menu slots 9-35 are vanilla's 9-35 and 36-44 is
	// the hotbar, which vanilla numbers 0-8.
	inv := nbt.List{Elem: nbt.TagCompound}
	for slot := slotMainStart; slot < slotCount-1; slot++ {
		it := p.Inv[slot]
		if it.Empty() {
			continue
		}
		vanilla := slot
		if slot >= slotHotbarStart {
			vanilla = slot - slotHotbarStart
		}
		inv.Items = append(inv.Items, itemNBTSlotted(it, int8(vanilla)))
	}
	// Vanilla writes an empty list's element tag as 0, not compound.
	if len(inv.Items) == 0 {
		inv.Elem = 0
	}
	m["Inventory"] = inv
	// Armour and the offhand live under equipment, keyed by body slot;
	// mainhand items are already in Inventory.
	equip := map[string]any{}
	for _, slot := range []struct {
		name string
		it   ItemStack
	}{
		{"head", p.Inv[5]}, {"chest", p.Inv[6]}, {"legs", p.Inv[7]},
		{"feet", p.Inv[8]}, {"offhand", p.Inv[45]}, {"body", p.Body},
	} {
		if !slot.it.Empty() {
			equip[slot.name] = itemNBT(slot.it)
		}
	}
	m["equipment"] = equip
	if !p.Carried.Empty() {
		m["cursor"] = itemNBT(p.Carried)
	}
	ender := nbt.List{Elem: nbt.TagCompound}
	for i, it := range p.Ender {
		if !it.Empty() {
			ender.Items = append(ender.Items, itemNBTSlotted(it, int8(i)))
		}
	}
	if len(ender.Items) == 0 {
		ender.Elem = 0
	}
	m["EnderItems"] = ender
	// The recipe book's two lists are recipe names: everything unlocked,
	// then the subset the book still has to show the player.
	recipes := nbt.List{Elem: nbt.TagString}
	toShow := nbt.List{Elem: nbt.TagString}
	seen := map[int32]bool{}
	for _, id := range p.SeenRecipes {
		seen[id] = true
	}
	for _, name := range sortedNames(p.UnlockedRecipes) {
		full := name
		if !strings.Contains(full, ":") {
			full = "minecraft:" + full
		}
		recipes.Items = append(recipes.Items, full)
		if idx, ok := recipeIndex[strings.TrimPrefix(name, "minecraft:")]; !ok || !seen[int32(idx)] {
			toShow.Items = append(toShow.Items, full)
		}
	}
	m["recipeBook"] = map[string]any{
		"recipes":       recipes,
		"toBeDisplayed": toShow,
	}
	return m
}

// playerFromDat reads a player file back into a playerPersist. Fields
// mc-go does not model (score, warden tracker, attributes) are dropped;
// everything it does model restores.
func playerFromDat(root map[string]any) playerPersist {
	var p playerPersist
	p.Dim, _ = root["Dimension"].(string)
	p.Health = float32(nbtNumberOr(root["Health"], 20))
	p.Absorb = float32(nbtNumberOr(root["AbsorptionAmount"], 0))
	p.Food = int32(nbtNumberOr(root["foodLevel"], 20))
	p.Saturation = float32(nbtNumberOr(root["foodSaturationLevel"], 5))
	p.Exhaustion = float32(nbtNumberOr(root["foodExhaustionLevel"], 0))
	p.FoodTick = int32(nbtNumberOr(root["foodTickTimer"], 0))
	p.Fire = int32(nbtNumberOr(root["Fire"], -20))
	p.SleepTimer = int32(nbtNumberOr(root["SleepTimer"], 0))
	p.PortalCooldown = int32(nbtNumberOr(root["PortalCooldown"], 0))
	p.FallDistance = float32(nbtNumberOr(root["fall_distance"], 0))
	p.SeenCredits = nbtNumberOr(root["seenCredits"], 0) != 0
	p.FallFlying = nbtTruth(root["FallFlying"])
	p.OnGround = nbtTruth(root["OnGround"])
	p.XPLevel = int32(nbtNumberOr(root["XpLevel"], 0))
	p.XPProgress = float32(nbtNumberOr(root["XpP"], 0))
	p.XPSeed = int32(nbtNumberOr(root["XpSeed"], 0))
	p.XPTotal = int32(nbtNumberOr(root["XpTotal"], 0))
	p.Held = int32(nbtNumberOr(root["SelectedItemSlot"], 0))
	p.GameMode = int32(nbtNumberOr(root["playerGameType"], 0))
	p.GameModeSaved = true
	p.PrevGameMode = int32(nbtNumberOr(root["previousPlayerGameType"], -1))
	if pos, ok := root["Pos"].(nbt.List); ok && len(pos.Items) == 3 {
		p.X, _ = pos.Items[0].(float64)
		p.Y, _ = pos.Items[1].(float64)
		p.Z, _ = pos.Items[2].(float64)
	}
	if rot, ok := root["Rotation"].(nbt.List); ok && len(rot.Items) == 2 {
		yaw, _ := nbtNumber(rot.Items[0])
		pitch, _ := nbtNumber(rot.Items[1])
		p.Yaw, p.Pitch = float32(yaw), float32(pitch)
	}
	if u, ok := root["UUID"].([]int32); ok && len(u) == 4 {
		binary.BigEndian.PutUint32(p.UUID[0:4], uint32(u[0]))
		binary.BigEndian.PutUint32(p.UUID[4:8], uint32(u[1]))
		binary.BigEndian.PutUint32(p.UUID[8:12], uint32(u[2]))
		binary.BigEndian.PutUint32(p.UUID[12:16], uint32(u[3]))
	}
	if list, ok := root["Inventory"].(nbt.List); ok {
		for _, item := range list.Items {
			entry, ok := item.(map[string]any)
			if !ok {
				continue
			}
			slot, ok := nbtNumber(entry["Slot"])
			if !ok {
				continue
			}
			it := itemFromNBT(entry)
			switch {
			case slot >= 0 && slot <= 8:
				p.Inv[slotHotbarStart+int(slot)] = it
			case slot >= 9 && slot <= 35:
				p.Inv[int(slot)] = it
			}
		}
	}
	if equip, ok := root["equipment"].(map[string]any); ok {
		for name, slot := range map[string]int{
			"head": 5, "chest": 6, "legs": 7, "feet": 8, "offhand": 45,
		} {
			if entry, ok := equip[name].(map[string]any); ok {
				p.Inv[slot] = itemFromNBT(entry)
			}
		}
		if entry, ok := equip["body"].(map[string]any); ok {
			p.Body = itemFromNBT(entry)
		}
	}
	if cur, ok := root["cursor"].(map[string]any); ok {
		p.Carried = itemFromNBT(cur)
	}
	if list, ok := root["EnderItems"].(nbt.List); ok {
		for _, item := range list.Items {
			entry, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if slot, ok := nbtNumber(entry["Slot"]); ok && slot >= 0 && slot < 27 {
				p.Ender[int(slot)] = itemFromNBT(entry)
			}
		}
	}
	if ab, ok := root["abilities"].(map[string]any); ok {
		p.Flying = nbtTruth(ab["flying"])
	}
	if book, ok := root["recipeBook"].(map[string]any); ok {
		// recipes minus toBeDisplayed is the set the book already
		// showed; everything else re-notifies on the next book open.
		shown := map[string]bool{}
		if list, ok := book["toBeDisplayed"].(nbt.List); ok {
			for _, item := range list.Items {
				if name, ok := item.(string); ok {
					shown[name] = true
				}
			}
		}
		if list, ok := book["recipes"].(nbt.List); ok {
			for _, item := range list.Items {
				name, ok := item.(string)
				if !ok {
					continue
				}
				p.UnlockedRecipes = append(p.UnlockedRecipes, name)
				if !shown[name] {
					if idx, ok := recipeIndex[strings.TrimPrefix(name, "minecraft:")]; ok {
						p.SeenRecipes = append(p.SeenRecipes, int32(idx))
					}
				}
			}
		}
	}
	return p
}

// sortedNames returns a copy of the list in vanilla's file order: the
// recipe book writes its two name lists sorted by recipe id.
func sortedNames(names []string) []string {
	out := append([]string(nil), names...)
	sort.Strings(out)
	return out
}

// uuidIntArray renders a UUID the way NBT int arrays hold it: four
// big-endian int32s, most-significant bits first.
func uuidIntArray(u [16]byte) []int32 {
	return []int32{
		int32(binary.BigEndian.Uint32(u[0:4])),
		int32(binary.BigEndian.Uint32(u[4:8])),
		int32(binary.BigEndian.Uint32(u[8:12])),
		int32(binary.BigEndian.Uint32(u[12:16])),
	}
}

// savePlayerFile writes one player's .dat under the world directory,
// the same file vanilla touches on disconnect and save-all.
func (s *Server) savePlayerFile(p playerPersist) error {
	s.waitNativeWorldSaveOwned()
	return writeNativePlayerFile(s.WorldDir, p)
}

func writeNativePlayerFile(worldDir string, p playerPersist) error {
	if worldDir == "" {
		return nil
	}
	u := p.UUID
	if u == ([16]byte{}) {
		return nil
	}
	dir := filepath.Join(worldDir, "players", "data")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return writePlayerGzipNBT(filepath.Join(dir, DashUUID(u)+".dat"), playerDatCompound(p))
}

// writePlayerGzipNBT writes the player file's gzipped NBT — every
// non-region file in a world directory takes this form.
func writePlayerGzipNBT(path string, v any) error {
	var raw bytes.Buffer
	if err := nbt.WriteNamed(&raw, "", v); err != nil {
		return err
	}
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	if _, err := zw.Write(raw.Bytes()); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, out.Bytes(), 0o644)
}

// readPlayerGzipNBT reads a gzipped NBT file, accepting an
// uncompressed one too.
func readPlayerGzipNBT(path string) (any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r io.Reader = bytes.NewReader(raw)
	if len(raw) > 2 && raw[0] == 0x1f && raw[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("%s: gzip: %w", path, err)
		}
		defer zr.Close()
		r = zr
	}
	_, v, err := nbt.ReadNamed(r)
	return v, err
}

// collectPlayerPersists gathers the persist for every online session
// and every remembered player — the set vanilla's PlayerList.save
// touches on save-all.
func (s *Server) collectPlayerPersists() map[[16]byte]playerPersist {
	persists := map[[16]byte]playerPersist{}
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.getName() != "" && sess.state.Load() == StatePlay {
			p := snapshotPlayer(sess)
			persists[p.UUID] = p
		}
		return true
	})
	s.savedPlayersMu.Lock()
	for name, p := range s.savedPlayers {
		if p.UUID == ([16]byte{}) {
			// Entries restored from the JSON store or written before
			// the uuid field existed key the player by name, so the
			// offline-mode uuid derives from it.
			if strings.Count(name, "-") == 4 {
				continue
			}
			p.UUID = offlineUUID(name)
		}
		persists[p.UUID] = p
	}
	s.savedPlayersMu.Unlock()
	return persists
}

// savePlayerFiles writes all three player stores — data/<uuid>.dat,
// stats/<uuid>.json, advancements/<uuid>.json — for every session and
// remembered player.
func (s *Server) savePlayerFiles() {
	persists := s.collectPlayerPersists()
	for _, p := range persists {
		if err := s.savePlayerFile(p); err != nil {
			if s.dataSaveErr == nil {
				s.dataSaveErr = err
			}
		}
	}
	s.savePlayerMetaFiles(persists)
}

// loadPlayerFiles reads every players/data/*.dat into the saved-player
// table, keyed by uuid; applySavedPlayer consults it after the
// name-keyed lookup misses.
func (s *Server) loadPlayerFiles() error {
	dir := filepath.Join(s.WorldDir, "players", "data")
	ents, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	loaded := map[string]playerPersist{}
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".dat") {
			continue
		}
		key := strings.TrimSuffix(name, ".dat")
		val, err := readPlayerGzipNBT(filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("server: player file %s: %w", name, err)
		}
		root, ok := val.(map[string]any)
		if !ok {
			continue
		}
		p := playerFromDat(root)
		if p.UUID == ([16]byte{}) {
			continue
		}
		loaded[key] = p
	}
	if len(loaded) == 0 {
		return nil
	}
	s.savedPlayersMu.Lock()
	s.rememberPlayersLocked(loaded)
	s.savedPlayersMu.Unlock()
	return nil
}
