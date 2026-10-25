package server

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"mc-go/api/event"
	"mc-go/api/plugin"
	"mc-go/api/registry"
	"mc-go/api/worldview"
	"mc-go/internal/world"
)

func blockEditKey(t *testing.T, name string) registry.StateKey {
	t.Helper()
	id, ok := world.BlockStateOf(name, nil)
	if !ok {
		t.Fatal("unknown test block", name)
	}
	key, ok := world.BlockCatalog().State(id)
	if !ok {
		t.Fatal("missing test identity")
	}
	return key
}

func TestTypedBlockEditRejectsBeforeWriteSideEffects(t *testing.T) {
	for _, mode := range []string{"veto", "panic", "invalid-target", "cancel-request", "cancel-subscription", "expected-mismatch"} {
		t.Run(mode, func(t *testing.T) {
			s := simulationTestServer(t)
			t.Cleanup(s.Stop)
			d, pos := s.dims[dimOverworld], world.Pos{X: 3, Y: 10, Z: 3}
			chest := world.BlockStateMust("chest")
			s.simulationAction(func() { s.setBlockAndBroadcastInOwned(d, "", pos, chest, true) })
			storage := s.chestAt(d, pos)
			storage.slots[0] = ItemStack{ID: world.ItemIDs["diamond"], Count: 2}
			beforeEntities, beforeTicks := s.entities.Count(), s.ticks.pending()
			legacy, stopLegacy := s.bus.Subscribe(event.TopicBlockChange, 4)
			defer stopLegacy()
			typed, stopTyped := s.bus.Subscribe(event.TopicBlockEdited, 4)
			defer stopTyped()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			lifetime, stopLifetime := context.WithCancel(context.Background())
			defer stopLifetime()
			called := 0
			remove := s.hooks.OnBlockEdit(lifetime, 0, func(_ context.Context, edit *plugin.BlockEdit) bool {
				called++
				switch mode {
				case "panic":
					panic("private edit policy detail")
				case "invalid-target":
					edit.Target, _ = registry.NewStateKey("example:unknown", nil)
				case "cancel-request":
					cancel()
				case "cancel-subscription":
					stopLifetime()
				case "veto":
					return false
				}
				return true
			})
			defer remove()
			expected := blockEditKey(t, "chest")
			if mode == "expected-mismatch" {
				expected = blockEditKey(t, "stone")
			}
			result, err := pluginAvatarCore(s).EditBlock(ctx, worldview.Edit{Dimension: dimOverworld, Position: worldview.Pos{X: 3, Y: 10, Z: 3}, State: blockEditKey(t, "stone"), Expected: &expected})
			if err == nil || result.Applied || d.w.GetBlock(pos) != chest || storage.slots[0].Count != 2 || s.entities.Count() != beforeEntities || s.ticks.pending() != beforeTicks || len(legacy) != 0 || len(typed) != 0 {
				t.Fatalf("rejection changed gameplay: result=%+v err=%v entities=%d ticks=%d", result, err, s.entities.Count(), s.ticks.pending())
			}
			if mode == "expected-mismatch" && called != 0 {
				t.Fatal("failed initial CAS dispatched policy")
			}
		})
	}
}

func TestTypedBlockEditExplicitDimensionTargetAndLegacyObservation(t *testing.T) {
	s := simulationTestServer(t)
	t.Cleanup(s.Stop)
	core := pluginAvatarCore(s)
	core.attachment.label = "test-transport"
	position := worldview.Pos{X: 3, Y: 64, Z: 3}
	pos := world.Pos{X: 3, Y: 64, Z: 3}
	nether := s.dims[dimNether]
	before, overworldBefore := nether.w.GetBlock(pos), s.world.GetBlock(pos)
	expected, _ := s.BlockCatalog().State(before)
	legacy, stopLegacy := s.bus.Subscribe(event.TopicBlockChange, 4)
	defer stopLegacy()
	typed, stopTyped := s.bus.Subscribe(event.TopicBlockEdited, 4)
	defer stopTyped()
	remove := s.hooks.OnBlockEdit(context.Background(), 0, func(_ context.Context, edit *plugin.BlockEdit) bool {
		edit.Origin, edit.Dimension, edit.Position, edit.Before = "spoofed", dimOverworld, worldview.Pos{Y: -9999}, registry.StateKey{}
		edit.Target = blockEditKey(t, "dirt")
		return true
	})
	defer remove()
	result, err := core.EditBlock(context.Background(), worldview.Edit{Dimension: dimNether, Position: position, State: blockEditKey(t, "stone"), Expected: &expected})
	if err != nil || !result.Applied || result.Before != before || result.After != world.Dirt || nether.w.GetBlock(pos) != world.Dirt || s.world.GetBlock(pos) != overworldBefore {
		t.Fatalf("wrong dimension/target: result=%+v err=%v", result, err)
	}
	old := (<-legacy).Data.(event.BlockChange)
	if old.Player != "" || old.Block != world.Dirt || old.X != 3 || old.Y != 64 || old.Z != 3 {
		t.Fatalf("legacy observation schema changed: %+v", old)
	}
	new := (<-typed).Data.(event.BlockEdited)
	if new.Origin != "frontend:test-transport" || new.Dimension != dimNether || new.Position != position || new.Before != before || new.Target != world.Dirt || new.After != world.Dirt {
		t.Fatalf("typed edit identity changed: %+v", new)
	}
}

func TestTypedBlockEditRevalidatesCASAndSuppressesNoop(t *testing.T) {
	s := simulationTestServer(t)
	t.Cleanup(s.Stop)
	pos := world.Pos{X: 2, Y: 10, Z: 2}
	request := worldview.Edit{Dimension: dimOverworld, Position: worldview.Pos{X: 2, Y: 10, Z: 2}, State: blockEditKey(t, "air")}
	result, err := pluginAvatarCore(s).EditBlock(context.Background(), request)
	if err != nil || result.Applied || result.Before != world.Air || result.After != world.Air {
		t.Fatalf("no-op result=%+v err=%v", result, err)
	}
	expected := request.State
	request.Expected, request.State = &expected, blockEditKey(t, "stone")
	remove := s.hooks.OnBlockEdit(context.Background(), 0, func(context.Context, *plugin.BlockEdit) bool {
		// An internal host test deliberately mutates through its legacy escape.
		// The public plugin API has no writable World handle.
		s.world.SetBlock(pos, world.Dirt)
		return true
	})
	defer remove()
	result, err = pluginAvatarCore(s).EditBlock(context.Background(), request)
	if !errors.Is(err, worldview.ErrRejected) || result.Applied || s.world.GetBlock(pos) != world.Dirt {
		t.Fatalf("post-hook CAS overwrote intervening state: %+v %v", result, err)
	}
}

func TestTypedBlockEditReportsFinalSideEffectsAndPersists(t *testing.T) {
	s := simulationTestServer(t)
	t.Cleanup(s.Stop)
	pos := worldview.Pos{X: 3, Y: 10, Z: 3}
	s.world.SetBlock(world.Pos{X: 4, Y: 10, Z: 3}, world.BlockStateMust("redstone_block"))
	result, err := pluginAvatarCore(s).EditBlock(context.Background(), worldview.Edit{Dimension: dimOverworld, Position: pos, State: blockEditKey(t, "tnt")})
	if err != nil || !result.Applied || result.Before != world.Air || result.After != world.Air || s.entities.Count() != 1 {
		t.Fatalf("receipt reported requested TNT instead of primed air: %+v err=%v entities=%d", result, err, s.entities.Count())
	}
	s.WorldPath = filepath.Join(t.TempDir(), "world.json")
	result, err = pluginAvatarCore(s).EditBlock(context.Background(), worldview.Edit{Dimension: dimNether, Position: pos, State: blockEditKey(t, "dirt")})
	if err != nil || !result.Applied {
		t.Fatalf("edit before save: %+v %v", result, err)
	}
	if err := s.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	restarted := simulationTestServer(t)
	t.Cleanup(restarted.Stop)
	restarted.WorldPath = s.WorldPath
	if err := restarted.LoadWorld(); err != nil {
		t.Fatal(err)
	}
	if got := restarted.dims[dimNether].w.GetBlock(world.Pos{X: 3, Y: 10, Z: 3}); got != result.After {
		t.Fatalf("typed edit absent after canonical save/load: %d want %d", got, result.After)
	}
}
