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
	selection := s.captureNativeEntitySelectionOwned(d)
	if selection.err != nil {
		return selection.err
	}
	receipts := map[string]nativeEntityOwnership{d.name: selection.ownership}
	s.beginNativeEntityWritesOwned(receipts)
	if err := world.MergeEntityRegions(world.DimensionDir(s.WorldDir, d.name), selection.rows); err != nil {
		return err
	}
	s.completeNativeEntityWritesOwned(receipts)
	return nil
}

// captureEntityRegionsOwned renders private NBT values while entity state is owned.
func (s *Server) captureEntityRegionsOwned(d *dimension) map[[2]int32][]map[string]any {
	return s.captureNativeEntitySelectionOwned(d).rows
}

func (s *Server) captureNativeEntitySelectionOwned(d *dimension) nativeEntitySelection {
	// Active-effect deadlines use the local simulation counter; world-age
	// base must not be subtracted from their saved remaining durations.
	now := s.clock.count
	groups := map[[2]int32][]map[string]any{}
	for _, e := range s.entities.All() {
		e := e
		if e.Dim != d.name || e.Dying {
			continue
		}
		name, ok := EntityTypeName(e.Type)
		if !ok || !nativeEntityRootSaveable(&e) {
			continue
		}
		if e.Riding != 0 && nativePassengerTreeEntity(&e) {
			if parent, ok := s.entities.GetPtr(e.Riding); ok && parent.Dim == d.name && !parent.Dying && nativePassengerTreeEntity(parent) && nativePassengerListed(parent, e.ID) {
				continue
			}
		}
		m := entitySaveMap(&e, now, s.entities).m
		// entitySaveMap's caller puts id on the /data reply itself;
		// on disk it is part of the compound, and a record without
		// one is dropped on load.
		m["id"] = "minecraft:" + name
		c := [2]int32{int32(math.Floor(e.X)) >> 4, int32(math.Floor(e.Z)) >> 4}
		groups[c] = append(groups[c], cloneNativeNBT(m).(map[string]any))
	}
	return s.selectNativeEntityRecordsOwned(d.name, groups)
}

// restoreEntities respawns one dimension's saved entity compounds.
// It runs once at boot, after the dimension's chunks load.
func (s *Server) restoreEntities(d *dimension) error {
	chunks, err := world.LoadEntityRegionChunks(world.DimensionDir(s.WorldDir, d.name))
	state := s.newNativeEntityRegionStateOwned(d.name)
	state.observed, state.loadErr = true, err
	if err != nil {
		return err
	}
	for chunk, rows := range chunks {
		for _, save := range rows {
			original := cloneNativeNBT(save).(map[string]any)
			var entity *Entity
			if nativeEntityRootNameRestorable(save) && nativeEntityCompositeRestorable(save) {
				entity = s.restoreEntityRecordOwned(d, save)
			}
			if nativeEntityRootSaveable(entity) {
				state.owned[chunk] = true
			} else {
				state.opaque[chunk] = append(state.opaque[chunk], original)
			}
		}
	}
	return nil
}

func (s *Server) restoreEntityRecordOwned(d *dimension, save map[string]any) *Entity {
	idStr, _ := save["id"].(string)
	name := strings.TrimPrefix(idStr, "minecraft:")
	pos, ok := nbtVec3(save["Pos"])
	if !ok {
		return nil
	}
	var uuid [16]byte
	if u, ok := save["UUID"].([]int32); ok && len(u) == 4 {
		uuid = uuidFromInts(u)
	}
	if name == "end_crystal" {
		return s.restoreEndCrystal(d, save, pos)
	}
	if name == "ender_dragon" {
		return s.restoreEnderDragon(d, save, pos)
	}
	if name == "command_block_minecart" {
		return s.restoreCommandMinecart(d, save, pos, uuid)
	}
	if ordinaryVehicle(name) {
		return s.restoreVehicleNative(d, name, save, pos, uuid)
	}
	if name == "item" {
		stack := ItemStack{}
		if item, ok := save["Item"].(map[string]any); ok {
			stack = itemFromNBT(item)
		}
		if stack.Empty() {
			return nil
		}
		entity := s.entities.Spawn(&Entity{Type: mustEntityType("item"), Dim: d.name, X: pos[0], Y: pos[1], Z: pos[2], Item: stack})
		entity.ItemHP = int32(itemSavedShort(save["Health"], 5))
		applyItemTouchSave(entity, save)
		if motion, ok := nbtVec3(save["Motion"]); ok {
			entity.MotionX, entity.MotionY, entity.MotionZ = motion[0], motion[1], motion[2]
		}
		entity.UUID = uuid
		return entity
	}
	entity, ok := s.summonMobRaw(d, name, pos[0], pos[1], pos[2], false, false)
	if !ok {
		return nil
	}
	if uuid != [16]byte{} {
		entity.UUID = uuid
	}
	s.applyEntitySave(entity, save)
	s.restoreNativePassengers(d, entity, save)
	// Native pairing alone describes a newly loaded entity; the stored motion
	// keeps its prior needsSync behavior without a duplicate metadata update.
	entity.dataSync = nil
	entity.MetaDirty = false
	return entity
}
