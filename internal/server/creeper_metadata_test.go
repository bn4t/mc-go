package server

import (
	"bytes"
	"mc-go/internal/protocol"
	"testing"
)

func TestCreeperMetadataDefaultsAndIgnition(t *testing.T) {
	typ := mustEntityType("creeper")
	for _, dir := range []int32{0, -1, 1} {
		e := NewManager().Spawn(&Entity{Type: typ, Living: true, Health: 1, SwellDir: dir})
		want := dir
		if want == 0 {
			want = -1
		}
		if e.SwellDir != want {
			t.Fatalf("constructor swell %d: got%d want%d", dir, e.SwellDir, want)
		}
	}
	e := NewManager().Spawn(&Entity{Type: typ, Living: true, Health: 1})
	if got := encodeLivingMetadataDirty(e, nil); len(got) != 0 {
		t.Fatalf("native-default creeper has dirty metadata: %x", got)
	}
	p := entityProfile(t, 775)
	check := func(label string, items, want []byte) {
		t.Helper()
		if !bytes.Equal(items, want) {
			t.Fatalf("%s metadata=%x want%x", label, items, want)
		}
		var b protocol.Buffer
		b.VarInt(e.ID)
		b.Raw(items)
		if _, err := p.projectMetadata776(typ, b.Bytes()); err != nil {
			t.Fatal(err)
		}
	}
	e.Ignited = true
	check("ignition", encodeLivingMetadataDirty(e, nil), []byte{18, 8, 1, 255})
	if got := encodeLivingMetadataDirty(e, nil); len(got) != 0 {
		t.Fatalf("unchanged ignition emitted %x", got)
	}
	// Countdown progress is local server state, distinct from the two synced flags.
	e.Swell = 9
	if got := encodeLivingMetadataDirty(e, nil); len(got) != 0 {
		t.Fatalf("countdown emitted %x", got)
	}
	e.SwellDir = 1
	check("swell direction", encodeLivingMetadataDirty(e, nil), []byte{16, 1, 1, 255})
	e.Ignited = false
	check("ignition reset", encodeLivingMetadataDirty(e, nil), []byte{18, 8, 0, 255})
	e.Ignited = true
	initial := encodeLivingMetadata(e, nil)
	if !bytes.Contains(initial, []byte{18, 8, 1}) {
		t.Fatalf("non-default initial ignition absent: %x", initial)
	}
	var b protocol.Buffer
	b.VarInt(e.ID)
	b.Raw(initial)
	if _, err := p.projectMetadata776(typ, b.Bytes()); err != nil {
		t.Fatal(err)
	}
}
