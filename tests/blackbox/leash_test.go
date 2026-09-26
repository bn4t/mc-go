package blackbox

// TestLeashSummonWireParity pins vanilla's wire shape for a leashed
// summon: `summon sheep ~ ~ ~ {leash:[I;x,y,z]}` pairs the sheep in
// its own bundle, pairs the auto-created leash_knot in a second
// bundle, then emits set_entity_link loose — the leash is unresolved
// when sendPairingData built the sheep's bundle, so the link packet
// never rides inside it.
//
//   vanilla 26.2, measured:  {0, add_entity(sheep), 99, 0} ...
//                            {0, add_entity(leash_knot), 0} ...
//                            set_entity_link(sheep, knot)   — loose

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestLeashSummonWireParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Trk"),
	}, nil)
	defer targets.cleanup()

	run := func(tag, addr string) []wirePk {
		c, pr, pw := joinPlayBlackbox(t, addr, "Trk")
		defer c.Close()
		drainChunkWindow(t, tag, c, pr, pw, 300*time.Millisecond)
		done := make(chan []wirePk, 1)
		go func() { done <- entityStream(t, tag, c, pr, pw, 3*time.Second) }()
		time.Sleep(400 * time.Millisecond)
		sendCommand(t, pw, "summon minecraft:sheep 8 -60 8 {leash:[I;8,-60,9]}")
		return <-done
	}

	want := run("vanilla", targets.vanilla)
	got := run("mc-go", targets.mcgo)

	// shape collects, in wire order: the add_entity type of each bundle,
	// and the eid/holder of every loose set_entity_link (0x64).
	shape := func(seq []wirePk) (bundled []string, links [][2]int32) {
		for _, w := range seq {
			if w.id == int32(server.IDPlaySpawnEntity) && w.bund {
				bundled = append(bundled, w.typ)
			}
			if w.id == 0x64 && !w.bund {
				r := protocol.NewPacketReader(w.raw)
				lid, _ := r.Int()
				hid, _ := r.Int()
				links = append(links, [2]int32{lid, hid})
			}
		}
		return
	}

	wantB, wantL := shape(want)
	gotB, gotL := shape(got)

	// Both servers: sheep bundle then knot bundle, then one loose
	// set_entity_link. Nothing else is asserted — tick noise differs.
	if len(gotB) < 2 || len(wantB) < 2 {
		t.Fatalf("bundled entity set: vanilla=%v mc-go=%v", wantB, gotB)
	}
	if gotB[0] != wantB[0] || gotB[1] != wantB[1] {
		t.Fatalf("bundle order: vanilla=%v mc-go=%v", wantB, gotB)
	}
	if len(gotL) != len(wantL) {
		t.Fatalf("loose set_entity_link count: vanilla=%v mc-go=%v", wantL, gotL)
	}
}
