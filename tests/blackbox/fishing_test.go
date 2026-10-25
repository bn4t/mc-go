package blackbox

// TestFishingParity casts a rod into a water pool, waits for the bite,
// reels in, and diffs the advancement flush — fishy_business's cod/
// salmon/pufferfish/tropical_fish OR criteria are fishing_rod_hooked
// with an item predicate over the caught stack. Loot is random
// (85% fish, open-water treasure pool on), so the harness retries the
// cast until a fish lands; both legs run the same loop.
//
// The player stands on the bank (not in the water) so neither leg
// trips the in_water recipe unlocks. The pool must cut through the
// surface block — a buried fill leaves the bobber parked on grass,
// which never bites. An upfront `give cod` normalizes the
// inventory_changed advancement defs (husbandry/root, cooked_cod
// recipes) so the diff isolates the fishing trigger.

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// fishyFlush reports whether any fishy_business fish criterion landed.
func fishyFlush(f *advFlush) bool {
	for _, crit := range []string{"cod", "salmon", "pufferfish", "tropical_fish"} {
		if f.prog["minecraft:husbandry/fishy_business"][crit] {
			return true
		}
	}
	return false
}

func TestFishingParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Fish"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	run := func(addr string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "Fish")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)

		// 7x2x7 pool at 5..11/-62..-61/5..11 — deep enough that the
		// surface block is replaced and the water is open to sky;
		// player dry on the shore at z=14 facing north (-z).
		// Every fish/junk/treasure item the table can produce goes into
		// the inventory up front, so a caught stack can't unlock a
		// recipe advancement on only one leg when the rolls diverge.
		sendCommand(t, pw, "fill 5 -62 5 11 -61 11 minecraft:water")
		for _, it := range []string{
			"cod", "salmon", "pufferfish", "tropical_fish",
			"lily_pad", "leather_boots", "leather", "bone", "string",
			"fishing_rod", "bowl", "stick", "ink_sac", "tripwire_hook",
			"rotten_flesh", "bamboo", "name_tag", "saddle", "bow",
			"enchanted_book", "nautilus_shell",
		} {
			sendCommand(t, pw, "give @s minecraft:"+it)
		}
		sendCommand(t, pw, `give @s minecraft:potion[minecraft:potion_contents="minecraft:water"]`)
		sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:fishing_rod")
		sendCommand(t, pw, "tp @s 8 -60 14 180 25")
		advCollect(t, c, pr, pw, 800*time.Millisecond, f)

		use := func() {
			var ub protocol.Buffer
			ub.VarInt(0) // main hand
			ub.VarInt(0) // sequence
			ub.Float(180)
			ub.Float(25)
			_ = pw.WritePacket(server.IDPlayUseItemSB, ub.Bytes())
		}

		drain := func(d time.Duration, watchBite bool) bool {
			end := time.Now().Add(d)
			for time.Now().Before(end) {
				_ = c.SetReadDeadline(time.Now().Add(time.Until(end)))
				p, err := pr.ReadPacket()
				if err != nil {
					return false
				}
				switch p.ID {
				case server.IDPlayKeepAliveCB:
					var kb protocol.Buffer
					kb.Raw(p.Data)
					_ = pw.WritePacket(server.IDPlayKeepAliveSB, kb.Bytes())
				case server.IDPlaySyncPos:
					r := protocol.NewPacketReader(p.Data)
					if id, err := r.VarInt(); err == nil {
						var b protocol.Buffer
						b.VarInt(id)
						_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
					}
				case server.IDPlayUpdateAdvance:
					decodeUpdateAdv(t, p.Data, f)
				case server.IDPlaySystemChatCB:
					f.chats = append(f.chats, chatKey(p.Data))
				case server.IDPlayEntityMetadata:
					if !watchBite {
						continue
					}
					// metadata idx 9 = biting flag (type 8 boolean).
					r := protocol.NewPacketReader(p.Data)
					_, _ = r.VarInt() // entity id
					for {
						idx, err := r.Byte()
						if err != nil || idx == 0xFF {
							break
						}
						typ, err := r.VarInt()
						if err != nil {
							break
						}
						if typ == 8 { // boolean
							v, err := r.Bool()
							if err != nil {
								break
							}
							if idx == 9 && v {
								return true
							}
							continue
						}
						if _, ok := readMetaValue(r, int(typ)); !ok {
							break
						}
					}
				}
			}
			return false
		}

		fishy := func() bool { return fishyFlush(f) }

		for attempt := 0; attempt < 4; attempt++ {
			use() // cast
			drain(2*time.Second, false)
			if !drain(40*time.Second, true) {
				use() // bobber lost or never bit — reel in / recast
				drain(800*time.Millisecond, false)
				continue
			}
			use() // reel in on the bite
			drain(3*time.Second, false)
			if fishy() {
				break
			}
			// caught junk/treasure or the bite window lapsed — recast
		}
		return f
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	if !fishyFlush(want) {
		t.Fatalf("vanilla: no fishy_business criterion granted after retries: %v", want.prog["minecraft:husbandry/fishy_business"])
	}
	// Which of the four fish criteria fired is pure loot-table luck —
	// what must agree is that A fish criterion was granted.
	collapseFishy := func(f *advFlush) {
		m := f.prog["minecraft:husbandry/fishy_business"]
		if m == nil {
			return
		}
		f.prog["minecraft:husbandry/fishy_business"] = map[string]bool{
			"fish": fishyFlush(f),
		}
	}
	collapseFishy(want)
	collapseFishy(got)
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("fishing parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
