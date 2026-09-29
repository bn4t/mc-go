package blackbox

// TestAllayInteractParity pins Allay.mobInteract's hand-off sounds and
// the take-back path, measured from 26.2's Allay/AllayAi (javap):
//
//   - Give: player item + empty allay hand → MAINHAND = copyWithCount(1),
//     ALLAY_ITEM_GIVEN (neutral, vol 2.0, pitch 1.0) to everyone but the
//     giver, LIKED_PLAYER latched.
//   - Take-back: empty player hand + holding allay, MAIN_HAND only →
//     ALLAY_ITEM_TAKEN, allay swings, inventory items thrown out at the
//     allay's position, LIKED_PLAYER erased, the hand item addItem'ed.
//   - Throw: GoAndGiveItemsToTarget/onItemThrown — one inventory item
//     per 20 ticks at the liked player in range, ALLAY_THROW at
//     gameTime%7==0 && random<0.9 with a pitch from THROW_SOUND_PITCHES.
//
// Wire ids: sound packet 0x75 carries VarInt(event+1) — ITEM_GIVEN is
// 5, ITEM_TAKEN 6, ALLAY_THROW 7.

import (
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// soundIDs scans 0x74 sound_entity lines for the wire sound id
// (VarInt event+1) — vanilla's entity-targeted playSound path.
func soundIDs(lines []string) (ids []int32) {
	for _, l := range lines {
		i := strings.IndexByte(l, ':')
		if i < 0 || l[:i] != "0x74" {
			continue
		}
		r := protocol.NewPacketReader([]byte(l[i+1:]))
		id, e := r.VarInt()
		if e != nil {
			continue
		}
		ids = append(ids, id)
	}
	return
}

func TestAllayInteractParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Allay", "AllayObs"),
	}, nil)
	defer targets.cleanup()

	run := func(addr string) (given, taken, thrown bool) {
		c, pr, pw := joinPlayBlackbox(t, addr, "Allay")
		defer c.Close()
		sink := newChatSink(pr, pw)
		sendClientInformationVD(t, pw, 2)
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		time.Sleep(500 * time.Millisecond)
		sendCommand(t, pw, "gamemode survival Allay")
		sendCommand(t, pw, "tp @s 8.5 -60 8.5 0 0")
		sendCommand(t, pw, `summon minecraft:allay 8.5 -60 10.5`)

		// Capture the allay's entity id (type wire id 2).
		var aid int32
		end := time.Now().Add(8 * time.Second)
		for time.Now().Before(end) && aid == 0 {
			for _, l := range sink.take() {
				i := strings.IndexByte(l, ':')
				if i < 0 || l[:i] != "0x1" {
					continue
				}
				r := protocol.NewPacketReader([]byte(l[i+1:]))
				id, e1 := r.VarInt()
				if e1 != nil {
					continue
				}
				if _, e2 := r.Read(16); e2 != nil {
					continue
				}
				typ, e3 := r.VarInt()
				if e3 == nil && typ == 2 {
					aid = id
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		if aid == 0 {
			t.Fatalf("%s: no allay spawn packet", addr)
		}

		sendCommand(t, pw, "give @s minecraft:dirt 64")
		time.Sleep(400 * time.Millisecond)
		sink.take()

		interact := func() {
			var ib protocol.Buffer
			ib.VarInt(aid)
			ib.VarInt(0)
			ib.VarInt(0)
			ib.Bool(false)
			_ = pw.WritePacket(server.IDPlayInteractSB, ib.Bytes())
		}
		// Observer: the give/take sounds exclude the causer — the
		// interacting client never sees them, so a second session
		// watches the same allay.
		oc, opr, opw := joinPlayBlackbox(t, addr, "AllayObs")
		defer oc.Close()
		osink := newChatSink(opr, opw)
		sendClientInformationVD(t, opw, 2)
		_ = opw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		time.Sleep(500 * time.Millisecond)
		sendCommand(t, opw, "tp @s 8.5 -60 6.5 0 0")
		osink.take()

		ocollect := func(d time.Duration) []string {
			var lines []string
			end := time.Now().Add(d)
			for time.Now().Before(end) {
				lines = append(lines, osink.take()...)
				time.Sleep(80 * time.Millisecond)
			}
			return lines
		}

		interact()
		olines := ocollect(1500 * time.Millisecond)
		for _, id := range soundIDs(olines) {
			if id == 5 {
				given = true
			}
		}

		// Feed the allay dirt so it has inventory to throw back —
		// summon a few stacks through the window: the allay's pickup
		// box is small and it hovers, so one drop can miss.
		for i := 0; i < 3; i++ {
			sendCommand(t, pw, `summon minecraft:item 8.5 -59.4 10.5 {Item:{id:"minecraft:dirt",count:16}}`)
			olines = append(olines, ocollect(3*time.Second)...)
		}
		for _, id := range soundIDs(olines) {
			if id == 7 {
				thrown = true
			}
		}

		// Empty the main hand to take the hand item back: vanilla
		// selects an empty hotbar slot via set_carried_item.
		var hb protocol.Buffer
		hb.Short(4) // pick an empty hotbar slot (dirt landed in the first)
		_ = pw.WritePacket(server.IDPlayHeldChangeSB, hb.Bytes())
		time.Sleep(300 * time.Millisecond)
		sink.take()
		osink.take()

		interact()
		olines = append(olines, ocollect(2*time.Second)...)
		for _, id := range soundIDs(olines) {
			if id == 6 {
				taken = true
			}
		}
		return given, taken, thrown
	}

	vg, vt, vw := run(targets.vanilla)
	mg, mt, mw := run(targets.mcgo)
	if !vg {
		t.Fatalf("vanilla: no ALLAY_ITEM_GIVEN sound to observer")
	}
	if !vt {
		t.Fatalf("vanilla: no ALLAY_ITEM_TAKEN sound to observer")
	}
	if !vw {
		t.Fatalf("vanilla: no ALLAY_THROW sound to observer")
	}
	if mg != vg || mt != vt || mw != vw {
		t.Fatalf("mcgo: given=%v taken=%v thrown=%v, want %v/%v/%v", mg, mt, mw, vg, vt, vw)
	}
}
