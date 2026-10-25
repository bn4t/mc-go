package server

import (
	"bytes"
	"testing"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

func chunkEntitiesReader(t *testing.T, body []byte) *protocol.PacketReader {
	t.Helper()
	r := protocol.NewPacketReader(body)
	_, _ = r.Read(8)
	maps, err := r.VarInt()
	if err != nil {
		t.Fatal(err)
	}
	for range maps {
		_, _ = r.VarInt()
		n, err := r.VarInt()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.Read(int(n) * 8); err != nil {
			t.Fatal(err)
		}
	}
	n, err := r.VarInt()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Read(int(n)); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestJavaBedChunkProfilesDetachedAndStrict(t *testing.T) {
	w := world.New(nil, 2)
	t.Cleanup(w.Stop)
	d := testOverworld(w)
	d.skylight = false
	for _, part := range []struct {
		z    int
		part string
	}{{7, "foot"}, {8, "head"}} {
		w.SetBlock(world.Pos{X: 8, Y: -60, Z: part.z}, mustBlock(t, "red_bed", map[string]string{"facing": "south", "part": part.part}))
	}
	for _, proto := range []int32{775, 776} {
		p, _ := javaProfileFor(proto)
		be := []clientBlockEntity{{pos: world.Pos{X: 1, Y: -60, Z: 2}, spec: blockEntitySpec{id: 26}, nbt: []byte{0}}}
		snapshot := captureChunkSnapshotForProfile(d, 0, 0, be, p)
		body, err := encodeChunkSnapshot(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		r := chunkEntitiesReader(t, body)
		count, err := r.VarInt()
		if err != nil {
			t.Fatal(err)
		}
		wantCount, wantBarrel := int32(1), int32(26)
		if proto == 775 {
			wantCount, wantBarrel = 3, 27
		}
		if count != wantCount {
			t.Fatalf("%d block entities=%d", proto, count)
		}
		for i := int32(0); i < count; i++ {
			xz, _ := r.Byte()
			y, _ := r.Short()
			typ, _ := r.VarInt()
			root, _ := r.Byte()
			if i == 0 {
				if xz != 0x12 || y != -60 || typ != wantBarrel || root != 0 {
					t.Fatalf("%d barrel %x %d %d %d", proto, xz, y, typ, root)
				}
			} else if (xz != 0x87 && xz != 0x88) || y != -60 || typ != 25 || root != 0 {
				t.Fatalf("775 bed %x %d %d %d", xz, y, typ, root)
			}
		}
		// Registry projection belongs to capture, and subsequent owner edits
		// cannot change this worker's target IDs, biome or block contents.
		d.biome = "minecraft:not_a_biome"
		after, err := encodeChunkSnapshot(snapshot)
		if err != nil || !bytes.Equal(body, after) {
			t.Fatal("captured chunk changed")
		}
		bad := captureChunkSnapshotForProfile(d, 0, 0, nil, p)
		if body, err := encodeChunkSnapshot(bad); err == nil || body != nil {
			t.Fatal("missing biome emitted bytes")
		}
		d.biome = "minecraft:the_void"
		if proto == 775 {
			current, _ := javaProfileFor(776)
			sulfur, _ := current.builtinRegistryID("minecraft:block_entity_type", "minecraft:potent_sulfur")
			be[0].spec.id = sulfur
			if body, err := encodeChunkSnapshot(captureChunkSnapshotForProfile(d, 0, 0, be, p)); err == nil || body != nil {
				t.Fatal("unrepresentable block entity emitted bytes")
			}
		}
	}
}

func TestJavaBedLiveRendererRecordMatchesObservedCompound(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	d := s.dims[dimOverworld]
	old, _ := enforceSession(t, s, "OldBed")
	current, _ := enforceSession(t, s, "NewBed")
	p, _ := javaProfileFor(775)
	old.profile = p
	old.ver = p.version
	for _, sess := range []*Session{old, current} {
		sess.dim.Store(d)
		sess.queued = map[[2]int]bool{{0, 0}: true}
	}
	pos := world.Pos{X: 8, Y: -60, Z: 8}
	bed := mustBlock(t, "red_bed", map[string]string{"facing": "south", "part": "head"})
	s.broadcastLegacyBedData(d, pos, bed)
	if len(current.sendCh) != 0 || len(old.sendCh) != 1 {
		t.Fatal("bed renderer update profile selection")
	}
	pkt := <-old.sendCh
	want := []byte{0, 0, 2, 0, 0, 0, 0x8f, 0xc4, 0x19, 0x0a, 0}
	if pkt.id != old.ids().PlayBlockEntityData || !bytes.Equal(pkt.data, want) {
		t.Fatalf("live bed=%x, want %x", pkt.data, want)
	}
}

func TestJavaLevelEventDataDomains(t *testing.T) {
	old, _ := javaProfileFor(775)
	current, _ := javaProfileFor(776)
	for _, name := range []string{"minecraft:13", "minecraft:cat"} {
		canonical := current.registryEntryID("minecraft:jukebox_song", name)
		body, err := old.encodeLevelEvent(1010, world.Pos{}, canonical)
		if err != nil {
			t.Fatal(err)
		}
		r := protocol.NewPacketReader(body)
		_, _ = r.Read(12)
		got, _ := r.Int()
		if got != old.registryEntryID("minecraft:jukebox_song", name) {
			t.Fatalf("song %s=%d", name, got)
		}
	}
	for _, event := range []int32{1000, 1011, 2000, 2002, 3000} {
		body, err := old.encodeLevelEvent(event, world.Pos{}, 0x123456)
		if err != nil {
			t.Fatal(err)
		}
		r := protocol.NewPacketReader(body)
		_, _ = r.Read(12)
		got, _ := r.Int()
		if got != 0x123456 {
			t.Fatalf("non-registry data changed for event %d", event)
		}
	}
}
