package server

import (
	"sort"
	"strconv"
	"strings"
)

// Bucketable.saveDefaultDataToBucketTag's payload. Vanilla stores it in
// the minecraft:bucket_entity_data component as a compound: Health
// always, then each spawn flag only while set (NoAI, Silent, NoGravity,
// Glowing, Invulnerable, PersistenceRequired). Tadpole and Axolotl add
// Age/AgeLocked. mc-go keeps the component as a canonical, comparable
// "Key=value" string sorted by key; ItemStack stays usable with == and
// the encoders render the typed compound back.
//
// Glowing has no modeled entity field (it is a tag, not the glow
// effect), and the Axolotl/TropicalFish variant components travel
// separately in vanilla, so neither is carried here.

// bucketEntityDataString renders one mob's bucket payload.
func bucketEntityDataString(e *Entity, name string) string {
	parts := []string{"Health=" + strconv.FormatFloat(float64(e.Health), 'g', -1, 32)}
	addBool := func(key string, v bool) {
		if v {
			parts = append(parts, key+"=1")
		}
	}
	addBool("NoAI", e.NoAI)
	addBool("Silent", e.Silent)
	addBool("NoGravity", e.NoGravity)
	addBool("Invulnerable", e.Invulnerable)
	addBool("PersistenceRequired", e.PersistenceRequired)
	if name == "tadpole" || name == "axolotl" {
		parts = append(parts, "Age="+strconv.Itoa(int(e.Age)))
		if e.AgeLocked {
			parts = append(parts, "AgeLocked=1")
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// bucketEntityDataNBT renders the canonical string as the component's
// compound, with the native tag types (Health float, booleans byte,
// Age int).
func bucketEntityDataNBT(data string) map[string]any {
	m := map[string]any{}
	for _, part := range strings.Split(data, ",") {
		key, val, ok := strings.Cut(part, "=")
		if !ok || key == "" || val == "" {
			continue
		}
		switch key {
		case "Health":
			if f, err := strconv.ParseFloat(val, 32); err == nil {
				m[key] = float32(f)
			}
		case "Age":
			if n, err := strconv.Atoi(val); err == nil {
				m[key] = int32(n)
			}
		case "NoAI", "Silent", "NoGravity", "Invulnerable", "PersistenceRequired", "AgeLocked":
			if val == "1" {
				m[key] = int8(1)
			}
		}
	}
	return m
}

// bucketEntityDataFromNBT canonicalizes a bucket-data compound read
// from a slot patch, a save, or an item command.
func bucketEntityDataFromNBT(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	var parts []string
	for _, key := range []string{"Age", "AgeLocked", "Health", "Invulnerable", "NoAI", "NoGravity", "PersistenceRequired", "Silent"} {
		raw, ok := m[key]
		if !ok {
			continue
		}
		switch key {
		case "Health":
			if f, ok := nbtFloat(raw); ok {
				parts = append(parts, "Health="+strconv.FormatFloat(float64(f), 'g', -1, 32))
			}
		case "Age":
			if n, ok := nbtInt32(raw); ok {
				parts = append(parts, "Age="+strconv.Itoa(int(n)))
			}
		default:
			if nbtTruth(raw) {
				parts = append(parts, key+"=1")
			}
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// applyBucketEntityData loads a released mob's payload, the native
// Bucketable.loadDefaultDataFromBucketTag plus the Age pair.
func applyBucketEntityData(e *Entity, data string) {
	for _, part := range strings.Split(data, ",") {
		key, val, ok := strings.Cut(part, "=")
		if !ok || val == "" {
			continue
		}
		switch key {
		case "Health":
			if f, err := strconv.ParseFloat(val, 32); err == nil {
				h := float32(f)
				if h > e.MaxHealth {
					h = e.MaxHealth
				}
				if h > 0 {
					e.Health = h
				}
			}
		case "Age":
			if n, err := strconv.Atoi(val); err == nil {
				e.Age = int32(n)
			}
		case "NoAI", "Silent", "NoGravity", "Invulnerable", "PersistenceRequired", "AgeLocked":
			if val != "1" {
				continue
			}
			switch key {
			case "NoAI":
				e.NoAI = true
			case "Silent":
				e.Silent = true
			case "NoGravity":
				e.NoGravity = true
			case "Invulnerable":
				e.Invulnerable = true
			case "PersistenceRequired":
				e.PersistenceRequired = true
			case "AgeLocked":
				e.AgeLocked = true
			}
		}
	}
}
