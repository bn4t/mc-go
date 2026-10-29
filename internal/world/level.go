package world

// The world's metadata files: level.dat, and the per-world SavedData
// files the real 26.2 server keeps beside it.
//
// The layout and every field name here were read off a world the real
// server saved (a flat world, seed 1, one player-less boot), because
// this is a format with no tolerance in it: vanilla reports a world it
// cannot parse as "Failed to load world" and starts a new one, and a
// field it does not recognize is discarded silently. Two things stand
// out from that measurement and are easy to get wrong from memory:
//
//   - the day clock is no longer in level.dat. The real server keeps a
//     clock per dimension in data/minecraft/world_clocks.dat, and
//     `/time set 6000` is visible there as the overworld clock's
//     total_ticks. level.dat's Time is the game time, which no /time
//     command moves.
//   - the world generator is no longer in level.dat either: it lives in
//     data/minecraft/world_gen_settings.dat, and the level's own file
//     carries only the spawn, the game type, the difficulty and the
//     enabled data packs.
//
// Chunks live under dimensions/<namespace>/<path>/region/, so a world
// directory holds one subdirectory per dimension rather than the three
// bare region folders older versions used.

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"mc-go/internal/nbt"
)

// LevelData is the metadata a world directory carries.
type LevelData struct {
	// Dir is the world directory: level.dat lives directly in it and
	// the other files under data/minecraft.
	Dir string
	// LevelName is what the server list and logging call the world.
	LevelName string
	// GameType is the default game type: 0 survival, 1 creative,
	// 2 adventure, 3 spectator.
	GameType int32
	// Difficulty is vanilla's difficulty name.
	Difficulty string
	// Seed and GenerateStructures describe the flat overworld the
	// generator settings file declares.
	Seed               int64
	GenerateStructures bool
	// Generator is the overworld generator: "" or "flat" for the flat
	// preset, "normal" for the noise generator with the overworld
	// multi-noise biome source.
	Generator string
	// GameTime is the tick counter that only ever moves forward, and
	// DayTime the overworld's day clock, which /time moves.
	GameTime int64
	DayTime  int64
	// Spawn is the world spawn block position.
	Spawn [3]int
	// GameRules holds each rule's value as the server states it -
	// "true", "false" or a count - and is converted to and from the
	// types the real file carries (a byte for booleans, an int for
	// counts) on the way through.
	GameRules map[string]string
}

// levelFormatVersion is the level.dat "version" field: the level format
// version, not the game's data version. The real server writes 19133.
const levelFormatVersion = 19133

// disabledDataPacks are the data packs the real server lists as
// disabled in a default world: the bundled experiments it does not
// enable.
var disabledDataPacks = []string{
	"minecart_improvements", "redstone_experiments", "trade_rebalance",
}

// WriteLevel writes level.dat and the world's SavedData files into
// d.Dir, creating the data directory if needed. The four files are
// independent: vanilla recreates any missing one with its defaults, so
// a caller that only cares about the blocks can write the chunks and
// stop.
func WriteLevel(d LevelData) error {
	// The directory may not exist yet: saving a world for the first
	// time is the ordinary case, and the real server creates the
	// directory then too, so a missing one is not an error to report.
	if err := os.MkdirAll(d.Dir, 0o755); err != nil {
		return err
	}
	// level.dat is gzipped NBT under an unnamed root with a single
	// named child, "Data".
	root := map[string]any{"Data": levelDatCompounds(d)}
	if err := writeGzipNBT(filepath.Join(d.Dir, "level.dat"), "", root); err != nil {
		return err
	}
	dataDir := filepath.Join(d.Dir, "data", "minecraft")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	if err := writeGzipNBT(filepath.Join(dataDir, "world_gen_settings.dat"), "",
		savedData(worldGenSettings(d))); err != nil {
		return err
	}
	if err := writeGzipNBT(filepath.Join(dataDir, "world_clocks.dat"), "",
		savedData(worldClocks(d))); err != nil {
		return err
	}
	if len(d.GameRules) > 0 {
		if err := writeGzipNBT(filepath.Join(dataDir, "game_rules.dat"), "",
			savedData(gameRuleValues(d.GameRules))); err != nil {
			return err
		}
	}
	return nil
}

// gameRuleValues renders game rules the way the real file holds them:
// a byte for the boolean rules and an int for the counted ones, so
// "true" is 1 and "max_block_modifications" is 32768. A rule the real
// server does not have a type for would be a byte, which is the
// default vanilla parses to.
func gameRuleValues(rules map[string]string) map[string]any {
	out := make(map[string]any, len(rules))
	for name, s := range rules {
		switch s {
		case "true":
			out[name] = int8(1)
		case "false":
			out[name] = int8(0)
		default:
			if n, err := strconv.ParseInt(s, 10, 32); err == nil {
				out[name] = int32(n)
			} else {
				out[name] = s
			}
		}
	}
	return out
}

// gameRuleStrings is gameRuleValues' inverse: the NBT values back as
// the strings the server and the client's Game Rule Values packet use.
func gameRuleStrings(payload map[string]any) map[string]string {
	out := make(map[string]string, len(payload))
	for name, v := range payload {
		switch t := v.(type) {
		case int8:
			if t != 0 {
				out[name] = "true"
			} else {
				out[name] = "false"
			}
		case int32:
			out[name] = strconv.FormatInt(int64(t), 10)
		case int64:
			out[name] = strconv.FormatInt(t, 10)
		case string:
			out[name] = t
		}
	}
	return out
}

// savedData wraps a SavedData file's payload: every one of them is an
// unnamed root carrying DataVersion and the payload under "data".
func savedData(payload map[string]any) map[string]any {
	return map[string]any{
		"DataVersion": int32(DataVersion26_2),
		"data":        payload,
	}
}

// levelDatCompounds builds the Data compound of level.dat.
func levelDatCompounds(d LevelData) map[string]any {
	name := d.LevelName
	if name == "" {
		name = "world"
	}
	difficulty := d.Difficulty
	if difficulty == "" {
		difficulty = "normal"
	}
	disabled := make([]any, 0, len(disabledDataPacks))
	for _, p := range disabledDataPacks {
		disabled = append(disabled, p)
	}
	return map[string]any{
		"DataVersion": int32(DataVersion26_2),
		"Version": map[string]any{
			"Id":       int32(DataVersion26_2),
			"Name":     "26.2",
			"Series":   "main",
			"Snapshot": int8(0),
		},
		"LevelName":  name,
		"LastPlayed": time.Now().UnixMilli(),
		"GameType":   d.GameType,
		// ServerBrands is what the server list shows as the source of
		// the world. mc-go says "vanilla" for the same reason the real
		// server does: the world is an unmodified one.
		"ServerBrands": nbt.List{Elem: nbt.TagString, Items: []any{"vanilla"}},
		"WasModded":    int8(0),
		"DataPacks": map[string]any{
			"Enabled":  nbt.List{Elem: nbt.TagString, Items: []any{"vanilla"}},
			"Disabled": nbt.List{Elem: nbt.TagString, Items: disabled},
		},
		"allowCommands": int8(0),
		"difficulty_settings": map[string]any{
			"difficulty": difficulty,
			"hardcore":   int8(0),
			"locked":     int8(0),
		},
		"initialized": int8(1),
		"spawn": map[string]any{
			"dimension": "minecraft:overworld",
			"pitch":     float32(0),
			"pos":       []int32{int32(d.Spawn[0]), int32(d.Spawn[1]), int32(d.Spawn[2])},
			"yaw":       float32(0),
		},
		"version": int32(levelFormatVersion),
		"Time":    d.GameTime,
	}
}

// worldGenSettings declares the generators the world's dimensions use.
// The overworld is the flat preset the generator produces, so the real
// server reads the same terrain back; the other two keep the real
// server's own declarations, because their terrain is not implemented
// yet and declaring the flat preset there would be a lie about what the
// saved chunks contain.
//
// The flat preset's structure_overrides and generate_structures are
// vanilla's own defaults for a world created with level-type=flat -
// both were read off the real file, not assumed: the default preset
// does list strongholds and villages.
func worldGenSettings(d LevelData) map[string]any {
	overworld := map[string]any{
		"type": "minecraft:overworld",
		"generator": map[string]any{
			"type": "minecraft:flat",
			"settings": map[string]any{
				"biome":    "minecraft:the_void",
				"features": int8(0),
				"lakes":    int8(0),
				"layers": nbt.List{Elem: nbt.TagCompound, Items: []any{
					map[string]any{"block": "minecraft:bedrock", "height": int32(1)},
					map[string]any{"block": "minecraft:dirt", "height": int32(2)},
					map[string]any{"block": "minecraft:grass_block", "height": int32(1)},
				}},
				"structure_overrides": nbt.List{Elem: nbt.TagString, Items: []any{
					"minecraft:strongholds", "minecraft:villages",
				}},
			},
		},
	}
	if settings, ok := noiseLevelSettings[d.Generator]; ok {
		// The world presets' overworld (world_preset/normal, amplified,
		// large_biomes): the noise generator over its settings.
		overworld = map[string]any{
			"type": "minecraft:overworld",
			"generator": map[string]any{
				"type":     "minecraft:noise",
				"settings": "minecraft:" + settings,
				"biome_source": map[string]any{
					"type":   "minecraft:multi_noise",
					"preset": "minecraft:overworld",
				},
			},
		}
	}
	return map[string]any{
		"bonus_chest":         int8(0),
		"generate_structures": boolByte(d.GenerateStructures),
		"seed":                d.Seed,
		"dimensions": map[string]any{
			"minecraft:overworld": overworld,
			"minecraft:the_nether": map[string]any{
				"type": "minecraft:the_nether",
				"generator": map[string]any{
					"type":     "minecraft:noise",
					"settings": "minecraft:nether",
					"biome_source": map[string]any{
						"type":   "minecraft:multi_noise",
						"preset": "minecraft:nether",
					},
				},
			},
			"minecraft:the_end": map[string]any{
				"type": "minecraft:the_end",
				"generator": map[string]any{
					"type":         "minecraft:noise",
					"settings":     "minecraft:end",
					"biome_source": map[string]any{"type": "minecraft:the_end"},
				},
			},
		},
	}
}

// worldClocks declares the per-dimension clocks. The real server keeps
// the overworld's day clock here and the end's total tick count; the
// nether has no clock because it has no day. The overworld's clock is
// what /time reads and writes, so this is the file that makes a saved
// world resume at the same time of day.
func worldClocks(d LevelData) map[string]any {
	return map[string]any{
		"minecraft:overworld": map[string]any{"total_ticks": d.DayTime},
		"minecraft:the_end":   map[string]any{"total_ticks": d.GameTime},
	}
}

// boolByte renders a Go bool as the byte NBT holds it.
func boolByte(b bool) int8 {
	if b {
		return 1
	}
	return 0
}

// writeGzipNBT writes one gzipped NBT file, the form everything in a
// world directory except the region files uses.
func writeGzipNBT(path, name string, v any) error {
	var raw bytes.Buffer
	if err := nbt.WriteNamed(&raw, name, v); err != nil {
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

// readGzipNBT reads one gzipped NBT file, accepting an uncompressed one
// too: the real server always gzips these, but a hand-edited world may
// not.
func readGzipNBT(path string) (any, error) {
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

// ReadLevel reads the metadata back. A missing file is not an error at
// this level: vanilla fills in its defaults, and so does a caller that
// finds no metadata, which is what a world saved by an older mc-go
// looks like. Only a file that exists and cannot be parsed is.
func ReadLevel(dir string) (LevelData, error) {
	out := LevelData{Dir: dir}
	root, err := readGzipNBT(filepath.Join(dir, "level.dat"))
	if err != nil {
		return out, err
	}
	data, ok := root.(map[string]any)
	if !ok {
		return out, fmt.Errorf("%s: level.dat root is not a compound", dir)
	}
	if d, ok := data["Data"].(map[string]any); ok {
		data = d
	}
	if v, ok := data["LevelName"].(string); ok {
		out.LevelName = v
	}
	if v, ok := data["GameType"].(int32); ok {
		out.GameType = v
	}
	if v, ok := data["Time"].(int64); ok {
		out.GameTime = v
	}
	if diff, ok := data["difficulty_settings"].(map[string]any); ok {
		if v, ok := diff["difficulty"].(string); ok {
			out.Difficulty = v
		}
	}
	if spawn, ok := data["spawn"].(map[string]any); ok {
		if pos, ok := spawn["pos"].([]int32); ok && len(pos) == 3 {
			out.Spawn = [3]int{int(pos[0]), int(pos[1]), int(pos[2])}
		}
	}
	dataDir := filepath.Join(dir, "data", "minecraft")
	if root, err := readGzipNBT(filepath.Join(dataDir, "world_clocks.dat")); err == nil {
		if payload, ok := savedDataPayload(root); ok {
			if ow, ok := payload["minecraft:overworld"].(map[string]any); ok {
				if ticks, ok := ow["total_ticks"].(int64); ok {
					out.DayTime = ticks
				}
			}
		}
	}
	if root, err := readGzipNBT(filepath.Join(dataDir, "world_gen_settings.dat")); err == nil {
		if payload, ok := savedDataPayload(root); ok {
			if seed, ok := payload["seed"].(int64); ok {
				out.Seed = seed
			}
			if gen, ok := payload["generate_structures"].(int8); ok {
				out.GenerateStructures = gen != 0
			}
			if dims, ok := payload["dimensions"].(map[string]any); ok {
				if ow, ok := dims["minecraft:overworld"].(map[string]any); ok {
					if g, ok := ow["generator"].(map[string]any); ok {
						switch g["type"] {
						case "minecraft:noise":
							out.Generator = "normal"
							for gen, set := range noiseLevelSettings {
								if g["settings"] == "minecraft:"+set {
									out.Generator = gen
								}
							}
						case "minecraft:flat":
							out.Generator = "flat"
						}
					}
				}
			}
		}
	}
	if root, err := readGzipNBT(filepath.Join(dataDir, "game_rules.dat")); err == nil {
		if payload, ok := savedDataPayload(root); ok {
			out.GameRules = gameRuleStrings(payload)
		}
	}
	return out, nil
}

// savedDataPayload unwraps a SavedData file's "data" compound.
func savedDataPayload(root any) (map[string]any, bool) {
	m, ok := root.(map[string]any)
	if !ok {
		return nil, false
	}
	payload, ok := m["data"].(map[string]any)
	return payload, ok
}

// noiseLevelSettings maps the noise level types to their overworld
// noise settings.
var noiseLevelSettings = map[string]string{
	"normal": "overworld", "amplified": "amplified", "large_biomes": "large_biomes",
}
