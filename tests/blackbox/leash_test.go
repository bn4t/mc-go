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
	"fmt"
	"strings"
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

// TestLeashDeathDropParity pins tickLeash's canInteractWithLevel gate
// (26.2 bytecode): the tick a leashed mob OR its holder can no longer
// interact with the level (dead, dying, or removed), the leash drops —
// the lead item only under the entity_drops rule (dropLeash), else
// removeLeash. The snap-distance arm drops the item unconditionally.
//
//   A: sheep leashed to a fence knot is killed → lead item lands.
//   B: entity_drops=false → unlink without the item.
//   C: the fence under the knot breaks — the knot pops, the holder
//      vanishes, and the leashee drops its leash → lead item lands.

func TestLeashDeathDropParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("LD"),
	}, nil)
	defer targets.cleanup()

	leadProbe := `@e[type=minecraft:item,x=%d,y=-61,z=%d,dx=5,dy=3,dz=5,nbt={Item:{id:"minecraft:lead"}}]`

	run := func(addr string) map[string]string {
		c, pr, pw := joinPlayBlackbox(t, addr, "LD")
		defer c.Close()
		sink := newChatSinkTP(pr, pw)
		sendCommand(t, pw, "gamerule doMobSpawning false")
		sendCommand(t, pw, "time set 18000")
		time.Sleep(300 * time.Millisecond)

		out := map[string]string{}
		probe := func(sel, mk string) string {
			sendCommand(t, pw, fmt.Sprintf("execute if entity %s run say %sH", sel, mk))
			sendCommand(t, pw, "say "+mk+"K")
			hit := "miss"
			end := time.Now().Add(4 * time.Second)
			for time.Now().Before(end) {
				stop := false
				for _, l := range sink.take() {
					if strings.Contains(l, mk+"H") {
						hit = "hit"
					}
					if strings.Contains(l, mk+"K") {
						stop = true
					}
				}
				if stop {
					break
				}
				time.Sleep(30 * time.Millisecond)
			}
			return hit
		}

		// Leg A: fence-knot leash, kill the sheep → lead item lands.
		sendCommand(t, pw, "setblock 8 -60 9 minecraft:oak_fence")
		sendCommand(t, pw, "summon minecraft:sheep 8 -60 8 {leash:[I;8,-60,9]}")
		time.Sleep(400 * time.Millisecond)
		sendCommand(t, pw, "kill @e[type=minecraft:sheep]")
		time.Sleep(800 * time.Millisecond)
		out["A"] = probe(fmt.Sprintf(leadProbe, 6, 6), "LA")
		sendCommand(t, pw, "kill @e[type=minecraft:item]")
		time.Sleep(200 * time.Millisecond)

		// Leg B: entity_drops=false → the leash still unlinks but no
		// item lands.
		sendCommand(t, pw, "gamerule entity_drops false")
		sendCommand(t, pw, "setblock 28 -60 9 minecraft:oak_fence")
		sendCommand(t, pw, "summon minecraft:sheep 28 -60 8 {leash:[I;28,-60,9]}")
		time.Sleep(400 * time.Millisecond)
		sendCommand(t, pw, "kill @e[type=minecraft:sheep]")
		time.Sleep(800 * time.Millisecond)
		out["B"] = probe(fmt.Sprintf(leadProbe, 26, 6), "LB")
		sendCommand(t, pw, "gamerule entity_drops true")
		sendCommand(t, pw, "kill @e[type=minecraft:item]")

		// Leg C: break the fence under the knot — the knot pops on
		// the ~100t survives pass, the holder vanishes, and the
		// leashee's tickLeash drops the leash (+item) next tick.
		sendCommand(t, pw, "setblock 48 -60 9 minecraft:oak_fence")
		sendCommand(t, pw, "summon minecraft:sheep 48.5 -60 8.5 {leash:[I;48,-60,9]}")
		time.Sleep(400 * time.Millisecond)
		sendCommand(t, pw, "setblock 48 -60 9 minecraft:air")
		time.Sleep(6 * time.Second)
		out["C"] = probe(fmt.Sprintf(leadProbe, 46, 6), "LC")
		sendCommand(t, pw, "kill @e[type=minecraft:item]")
		return out
	}

	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	t.Logf("vanilla %v", van)
	t.Logf("mc-go   %v", mc)
	want := map[string]string{"A": "hit", "B": "miss", "C": "hit"}
	for k, w := range want {
		if van[k] != w {
			t.Errorf("leg %s vanilla=%s want %s", k, van[k], w)
		}
		if mc[k] != van[k] {
			t.Errorf("leg %s: vanilla=%s mc-go=%s", k, van[k], mc[k])
		}
	}
}
