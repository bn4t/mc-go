package world

import (
	"math"
	"strings"

	"mc-go/internal/nbt"
)

// SavedChunkTicks returns detached records from the last region load. Repeated
// loads replace this handoff just as they replace loaded block entities.
func (w *World) SavedChunkTicks() map[[2]int32]SavedChunkTicks {
	out := make(map[[2]int32]SavedChunkTicks, len(w.savedChunkTicks))
	for key, chunk := range w.savedChunkTicks {
		out[key] = SavedChunkTicks{Blocks: append([]SavedTick(nil), chunk.Blocks...), Fluids: append([]SavedTick(nil), chunk.Fluids...)}
	}
	return out
}

// DecodeSavedChunkTicks reads the native chunk codec's usable records. A bad
// entry does not discard its valid siblings, and duplicate records retain their
// order. Chunk ownership checks X/Z only; vertical bounds are not a codec rule.
func DecodeSavedChunkTicks(compound map[string]any, cx, cz int) SavedChunkTicks {
	return SavedChunkTicks{
		Blocks: decodeSavedTickList(compound["block_ticks"], cx, cz, false),
		Fluids: decodeSavedTickList(compound["fluid_ticks"], cx, cz, true),
	}
}

func decodeSavedTickList(value any, cx, cz int, fluid bool) []SavedTick {
	list, ok := value.(nbt.List)
	if !ok {
		return nil
	}
	var out []SavedTick
	for _, value := range list.Items {
		row, ok := value.(map[string]any)
		if !ok {
			continue
		}
		name, ok := row["i"].(string)
		if !ok {
			continue
		}
		if !strings.ContainsRune(name, ':') {
			name = "minecraft:" + name
		} else if strings.HasPrefix(name, ":") {
			name = "minecraft" + name
		}
		local, builtin := strings.CutPrefix(name, "minecraft:")
		if !builtin {
			continue
		}
		if fluid {
			switch local {
			case "empty", "water", "flowing_water", "lava", "flowing_lava":
			default:
				continue
			}
		} else if !BlockHasName(local) {
			continue
		}
		var numbers [5]int32
		valid := true
		for i, key := range [...]string{"x", "y", "z", "t", "p"} {
			var found bool
			numbers[i], found = savedTickInt(row[key])
			valid = valid && found
		}
		if !valid || int(numbers[0])>>4 != cx || int(numbers[2])>>4 != cz {
			continue
		}
		priority := numbers[4]
		if priority < -3 {
			priority = -3
		} else if priority > 3 {
			priority = 3
		}
		out = append(out, SavedTick{Type: name, Pos: Pos{int(numbers[0]), int(numbers[1]), int(numbers[2])}, Delay: numbers[3], Priority: priority})
	}
	return out
}

// NBT codec integers accept Number.intValue: integral tags narrow, while
// floating tags truncate toward zero and saturate (NaN becomes zero).
func savedTickInt(value any) (int32, bool) {
	switch value := value.(type) {
	case int8:
		return int32(value), true
	case int16:
		return int32(value), true
	case int32:
		return value, true
	case int64:
		return int32(value), true
	case float32:
		return savedTickFloatInt(float64(value)), true
	case float64:
		return savedTickFloatInt(value), true
	default:
		return 0, false
	}
}

func savedTickFloatInt(value float64) int32 {
	if math.IsNaN(value) {
		return 0
	}
	if value >= math.MaxInt32 {
		return math.MaxInt32
	}
	if value <= math.MinInt32 {
		return math.MinInt32
	}
	return int32(value)
}
