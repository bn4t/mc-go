package server

import (
	"mc-go/internal/nbt"
	"mc-go/internal/world"
	"strings"
)

// Only roots actually restored by an existing serializer grant replacement
// authority. All other compounds remain private opaque data at their source.
type nativeEntityRegionState struct {
	dir        string
	generation uint64
	observed   bool
	loadErr    error
	owned      map[[2]int32]bool
	unsettled  map[[2]int32]bool
	opaque     map[[2]int32][]map[string]any
}

// These detached primitive receipts belong to a particular successful load
// incarnation. Selected positions also cover failed/partial write repair; they
// do not become the persisted ownership baseline until a job succeeds.
type nativeEntityOwnership struct {
	generation uint64
	owned      map[[2]int32]bool
	selected   map[[2]int32]bool
}

type nativeEntitySelection struct {
	rows      map[[2]int32][]map[string]any
	ownership nativeEntityOwnership
	err       error
}

func (s *Server) newNativeEntityRegionStateOwned(dim string) *nativeEntityRegionState {
	if s.nativeEntityRegions == nil {
		s.nativeEntityRegions = map[string]*nativeEntityRegionState{}
	}
	s.nativeEntityGeneration++
	state := &nativeEntityRegionState{dir: s.WorldDir, generation: s.nativeEntityGeneration, owned: map[[2]int32]bool{}, unsettled: map[[2]int32]bool{}, opaque: map[[2]int32][]map[string]any{}}
	s.nativeEntityRegions[dim] = state
	return state
}

func (s *Server) nativeEntityRegionStateOwned(dim string) *nativeEntityRegionState {
	state := s.nativeEntityRegions[dim]
	if state == nil || state.dir != s.WorldDir {
		state = s.newNativeEntityRegionStateOwned(dim)
	}
	return state
}

func nativeEntityRootSaveable(e *Entity) bool {
	if e == nil {
		return false
	}
	name, registered := EntityTypeName(e.Type)
	return registered && (e.Living || name == "item" || name == "end_crystal" || name == "command_block_minecart" || ordinaryVehicle(name))
}

func (s *Server) selectNativeEntityRecordsOwned(dim string, live map[[2]int32][]map[string]any) nativeEntitySelection {
	ownership := nativeEntityOwnership{owned: map[[2]int32]bool{}, selected: map[[2]int32]bool{}}
	for chunk := range live {
		ownership.owned[chunk] = true
		ownership.selected[chunk] = true
	}
	if s.WorldDir == "" {
		return nativeEntitySelection{rows: live, ownership: ownership}
	}
	state := s.nativeEntityRegionStateOwned(dim)
	if !state.observed {
		// A caller may export to an existing directory without loading it first.
		// Unobserved disk roots grant no ownership and must remain opaque.
		chunks, err := world.LoadEntityRegionChunks(world.DimensionDir(s.WorldDir, dim))
		state.observed, state.loadErr = true, err
		if err == nil {
			for chunk, rows := range chunks {
				for _, row := range rows {
					state.opaque[chunk] = append(state.opaque[chunk], cloneNativeNBT(row).(map[string]any))
				}
			}
		}
	}
	ownership.generation = state.generation
	if state.loadErr != nil {
		return nativeEntitySelection{err: state.loadErr}
	}
	for chunk := range state.owned {
		ownership.selected[chunk] = true
	}
	for chunk := range state.unsettled {
		ownership.selected[chunk] = true
	}
	for chunk := range ownership.selected {
		rows := live[chunk]
		for _, opaque := range state.opaque[chunk] {
			rows = append(rows, cloneNativeNBT(opaque).(map[string]any))
		}
		// Presence with an empty list is an explicit old-position tombstone.
		live[chunk] = rows
	}
	return nativeEntitySelection{rows: live, ownership: ownership}
}

func (s *Server) beginNativeEntityWritesOwned(receipts map[string]nativeEntityOwnership) {
	for dim, receipt := range receipts {
		state := s.nativeEntityRegions[dim]
		if state == nil || state.generation != receipt.generation {
			continue
		}
		for chunk := range receipt.selected {
			state.unsettled[chunk] = true
		}
	}
}

func (s *Server) completeNativeEntityWritesOwned(receipts map[string]nativeEntityOwnership) {
	for dim, receipt := range receipts {
		state := s.nativeEntityRegions[dim]
		if state == nil || state.generation != receipt.generation {
			continue
		}
		state.owned = map[[2]int32]bool{}
		for chunk := range receipt.owned {
			state.owned[chunk] = true
		}
		for chunk := range receipt.selected {
			delete(state.unsettled, chunk)
		}
	}
}

// A partially understood passenger tree is one opaque persistence unit. Do not
// hydrate its known root: ownership of that root would otherwise erase unknown
// descendants when the live passenger list is serialized or the root is removed.
func nativeEntityCompositeRestorable(root map[string]any) bool {
	pending := []map[string]any{root}
	for len(pending) > 0 {
		node := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		raw, exists := node["Passengers"]
		if !exists {
			continue
		}
		list, ok := raw.(nbt.List)
		if !ok {
			return false
		}
		if len(list.Items) == 0 {
			continue
		}
		name, _ := node["id"].(string)
		if !nativePassengerTreeName(strings.TrimPrefix(name, "minecraft:")) {
			return false
		}
		for _, value := range list.Items {
			child, ok := value.(map[string]any)
			if !ok {
				return false
			}
			name, _ := child["id"].(string)
			name = strings.TrimPrefix(name, "minecraft:")
			// Native recursive loading skips an unregistered entity node;
			// it does not prevent valid siblings from being admitted. Registered
			// types with unsupported serializers still make this tree opaque.
			if _, registered := EntityTypeID(name); !registered {
				continue
			}
			if !nativePassengerTreeName(name) {
				return false
			}
			pending = append(pending, child)
		}
	}
	return true
}

// Capability is checked before hydration: factory support alone does not imply
// a serializer owns this root (notably clouds, XP orbs and markers).
func nativeEntityRootNameRestorable(save map[string]any) bool {
	id, _ := save["id"].(string)
	name := strings.TrimPrefix(id, "minecraft:")
	return name == "item" || name == "end_crystal" || name == "ender_dragon" || name == "command_block_minecart" || nativePassengerTreeName(name)
}
