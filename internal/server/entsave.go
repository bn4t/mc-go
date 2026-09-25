package server

import (
	"math"
	"strings"

	"mc-go/internal/world"
)

// Entity persistence, in the real server's own format: alongside the
// chunk regions a dimension dir carries entities/r.<rx>.<rz>.mca —
// the same Anvil container holding per-chunk compounds whose Entities
// list is the mobs and item entities that stood in the chunk.
//
// entitySaveMap already renders the vanilla compound (id, Pos,
// Motion, Rotation, UUID, the living and per-type fields, and the
// ExtraNBT carry-through that round-trips fields mc-go does not
// model); applyEntitySave is its reader. What this file adds is only
// the grouping and the spawn-back: a mob comes back through
// summonMobRaw so no broadcast leaks before a client attaches.

// saveEntityRegions writes dir/entities for one dimension.
func (s *Server) saveEntityRegions(d *dimension) error {
	now := s.gameTime()
	groups := map[[2]int32][]map[string]any{}
	for _, e := range s.entities.All() {
		e := e
		if e.Dim != d.name || e.Dying {
			continue
		}
		name, ok := EntityTypeName(e.Type)
		if !ok || (!e.Living && name != "item") {
			continue
		}
		m := entitySaveMap(&e, now, s.entities).m
		// entitySaveMap's caller puts id on the /data reply itself;
		// on disk it is part of the compound, and a record without
		// one is dropped on load.
		m["id"] = "minecraft:" + name
		if name == "item" {
			// ItemEntity's Health is its destroy counter — 5
			// default, unlike the 0 a fresh mc-go item carries.
			if e.Health <= 0 {
				m["Health"] = float32(5)
			}
		}
		c := [2]int32{int32(math.Floor(e.X)) >> 4, int32(math.Floor(e.Z)) >> 4}
		groups[c] = append(groups[c], m)
	}
	return world.SaveEntityRegions(world.DimensionDir(s.WorldDir, d.name), groups)
}

// restoreEntities respawns one dimension's saved entity compounds.
// It runs once at boot, after the dimension's chunks load.
func (s *Server) restoreEntities(d *dimension) {
	ents, err := world.LoadEntityRegions(world.DimensionDir(s.WorldDir, d.name))
	if err != nil {
		return
	}
	for _, save := range ents {
		idStr, _ := save["id"].(string)
		name := strings.TrimPrefix(idStr, "minecraft:")
		pos, ok := nbtVec3(save["Pos"])
		if !ok {
			continue
		}
		var uuid [16]byte
		if u, ok := save["UUID"].([]int32); ok && len(u) == 4 {
			for i, v := range u {
				uuid[i*4] = byte(v >> 24)
				uuid[i*4+1] = byte(v >> 16)
				uuid[i*4+2] = byte(v >> 8)
				uuid[i*4+3] = byte(v)
			}
		}
		if name == "item" {
			stack := ItemStack{}
			if it, ok := save["Item"].(map[string]any); ok {
				stack = itemFromNBT(it)
			}
			if stack.Empty() {
				continue
			}
			e := s.entities.Spawn(&Entity{
				Type: mustEntityType("item"),
				Dim:  d.name,
				X:    pos[0], Y: pos[1], Z: pos[2],
				Item: stack,
			})
			if v, ok := nbtFloat(save["PickupDelay"]); ok {
				e.PickupDelay = int(v)
			}
			if v, ok := nbtFloat(save["Age"]); ok {
				e.ItemAge = int32(v)
			}
			if mot, ok := nbtVec3(save["Motion"]); ok {
				e.MotionX, e.MotionY, e.MotionZ = mot[0], mot[1], mot[2]
			}
			e.UUID = uuid
			continue
		}
		e, ok := s.summonMobRaw(d, name, pos[0], pos[1], pos[2], false, false)
		if !ok {
			continue
		}
		if uuid != [16]byte{} {
			e.UUID = uuid
		}
		s.applyEntitySave(e, save)
		// The merge left a dirty pack queued as if a /data edit had
		// landed; a loaded entity has no prior client state, so the
		// tracker-attach burst alone describes it.
		e.dataSync = nil
		e.MetaDirty = false
		e.PosDirty = false
	}
}
