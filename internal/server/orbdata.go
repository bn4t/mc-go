package server

import "math"

// The orb's persistent counters are distinct from living health. Age, Health
// and Value serialize as short tags; Count uses a positive integer codec.
func orbSavedFields(e *Entity, save map[string]any) {
	save["Health"] = int16(e.OrbHP)
	save["Age"] = int16(e.ItemAge)
	save["Value"] = int16(e.OrbValue)
	save["Count"] = e.OrbCount
}

func loadOrbSavedFields(e *Entity, save map[string]any) {
	e.orbHealthInitialized = true
	e.OrbHP = int32(itemSavedShort(save["Health"], 5))
	e.ItemAge = int32(itemSavedShort(save["Age"], 0))
	e.OrbValue = int32(itemSavedShort(save["Value"], 0))
	e.OrbCount = orbSavedCount(save["Count"])
}

// Floating NBT numbers narrow like Java Number.intValue before the positive
// codec rejects zero/negative values; positive overflow saturates at MaxInt32.
func orbSavedCount(value any) int32 {
	var floating float64
	switch n := value.(type) {
	case float32:
		floating = float64(n)
	case float64:
		floating = n
	default:
		if count, ok := nbtInt32(value); ok && count > 0 {
			return count
		}
		return 1
	}
	if math.IsNaN(floating) || floating < 1 {
		return 1
	}
	if floating >= math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(floating)
}
