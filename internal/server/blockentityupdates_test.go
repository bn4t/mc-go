package server

import (
	"bytes"
	"testing"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

func TestBlockEntityUpdateGetterPolicy(t *testing.T) {
	if len(blockEntityPacketTypes) != 20 {
		t.Fatalf("update getter types%d want20", len(blockEntityPacketTypes))
	}
	for _, typ := range []string{"command_block", "chest", "trapped_chest", "furnace", "smoker", "blast_furnace", "hopper", "barrel", "dispenser", "dropper", "brewing_stand", "shulker_box", "lectern", "comparator", "crafter", "beehive", "bell", "end_portal", "jukebox", "piston", "sculk_sensor", "calibrated_sculk_sensor", "potent_sulfur"} {
		if blockEntityPacketTypes[typ] {
			t.Errorf("null update getter type%s admitted", typ)
		}
	}
	s := simulationTestServer(t)
	d := s.dims[dimOverworld]
	p := world.Pos{X: 2, Y: -60, Z: 2}
	s.simulationAction(func() {
		state, _ := world.BlockState("suspicious_sand")
		s.setSilentBlock(d, p, state)
		_, payload, ok := s.blockEntityUpdateAt(d, p)
		if !ok || !bytes.Equal(payload, []byte{nbt.TagCompound, nbt.TagEnd}) {
			t.Fatalf("empty update tag payload%x valid%v", payload, ok)
		}
	})
}
func TestBlockEntityFlushFreshnessAndDeduplication(t *testing.T) {
	s := simulationTestServer(t)
	d := s.dims[dimOverworld]
	viewer := entityViewer(t, s, 776, "BEFlush")
	viewer.queued = map[[2]int]bool{{0, 0}: true}
	p := world.Pos{X: 2, Y: -60, Z: 2}
	s.ticking.Store(true)
	defer s.ticking.Store(false)
	readUpdates := func() []map[string]any {
		var out []map[string]any
		for _, packet := range entityPackets(t, viewer) {
			if packet.id != viewer.ids().PlayBlockEntityData {
				continue
			}
			r := protocol.NewPacketReader(packet.data)
			_, _, _, _ = r.Position()
			_, _ = r.VarInt()
			data, e := r.Read(r.Remaining())
			if e != nil {
				t.Fatal(e)
			}
			value, e := nbt.ReadUnnamed(bytes.NewReader(data))
			if e != nil {
				t.Fatal(e)
			}
			out = append(out, value.(map[string]any))
		}
		return out
	}
	s.simulationAction(func() {
		state, _ := world.BlockStateOf("structure_block", map[string]string{"mode": "save"})
		s.setSilentBlock(d, p, state)
		g := s.structureBlockAt(d, p)
		g.fields["metadata"] = "stale"
		s.queueBlockEntityDirty(d, p)
		g.fields["metadata"] = "fresh"
		s.blockChangesFlushStep()
		updates := readUpdates()
		if len(updates) != 1 || updates[0]["metadata"] != "fresh" {
			t.Fatalf("batched metadata%+v", updates)
		}
		for _, key := range []string{"id", "x", "y", "z", "keepPacked"} {
			if updates[0][key] != nil {
				t.Errorf("update tag leaked%s", key)
			}
		}
		g.fields["metadata"] = "entity_only"
		s.queueBlockEntityDirty(d, p)
		s.blockChangesFlushStep()
		updates = readUpdates()
		if len(updates) != 1 || updates[0]["metadata"] != "entity_only" {
			t.Fatalf("entity-only updates%+v", updates)
		}
		for i, name := range []string{"command_block", "chest", "furnace"} {
			state, _ := world.BlockState(name)
			q := world.Pos{X: 4 + i, Y: -60, Z: 2}
			s.setSilentBlock(d, q, state)
			s.queueBlockEntityDirty(d, q)
		}
		s.blockChangesFlushStep()
		if updates = readUpdates(); len(updates) != 0 {
			t.Fatalf("null-getter updates%+v", updates)
		}
		spawner, _ := world.BlockState("spawner")
		q := world.Pos{X: 8, Y: -60, Z: 2}
		s.setSilentBlock(d, q, spawner)
		gsp := s.spawnerAt(d, q)
		s.spawnerSetEntity(d, q, "pig")
		s.spawnerSetEntity(d, q, "cow")
		s.blockChangesFlushStep()
		updates = readUpdates()
		if len(updates) != 1 {
			t.Fatalf("spawner placement plus two mutations emitted%d updates", len(updates))
		}
		sd, _ := updates[0]["SpawnData"].(map[string]any)
		ent, _ := sd["entity"].(map[string]any)
		if ent["id"] != "minecraft:cow" || gsp == nil {
			t.Fatalf("stale spawner update%+v", updates)
		}
	})
}
