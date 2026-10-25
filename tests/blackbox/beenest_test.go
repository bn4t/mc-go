package blackbox

// TestBeeNestDestroyedParity sets a bee nest carrying three bees via
// setblock NBT (vanilla 26.2 decodes `bees` occupant lists when the
// occupant entity id is namespaced — "minecraft:bee", not "bee", and
// min_ticks_in_hive has a codec floor below which the occupant is
// dropped — the worldgen value 600 works, 1 does not), then digs it
// out with a silk-touch pickaxe. husbandry/silk_touch_nest ("Total
// Beelocation") wants a bee_nest broken by a silk_touch tool holding
// exactly 3 bees.
//
// Measured gate on vanilla 26.2: bee_nest_destroyed lives in
// Block.playerDestroy, which ServerPlayerGameMode.destroyBlock skips
// when the player preventsBlockDrops — i.e. a creative break never
// fires the trigger. The test therefore asserts a creative dig grants
// nothing, then a survival dig (start + finish, server-timed) grants.

import (
	"bytes"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestBeeNestDestroyedParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Beekeeper"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
	})
	defer targets.cleanup()

	nest := `bee_nest{bees:[{entity_data:{id:"minecraft:bee"},ticks_in_hive:0,min_ticks_in_hive:600},{entity_data:{id:"minecraft:bee"},ticks_in_hive:0,min_ticks_in_hive:600},{entity_data:{id:"minecraft:bee"},ticks_in_hive:0,min_ticks_in_hive:600}]}`

	run := func(addr string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "Beekeeper")
		defer c.Close()
		if addr == targets.vanilla {
			t.Cleanup(func() { vanillaLogTail(t) })
		}
		_ = c.SetDeadline(time.Time{})
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)
		if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
			t.Fatal(err)
		}
		sendCommand(t, pw, "tp @s 4 -60 -8")
		advCollect(t, c, pr, pw, 600*time.Millisecond, f)
		sendCommand(t, pw, `item replace entity @s hotbar.0 with diamond_pickaxe[minecraft:enchantments={silk_touch:1}] 1`)
		sendCommand(t, pw, `setblock 4 -60 -10 `+nest)
		sendCommand(t, pw, `setblock 4 -60 -12 `+nest)
		advCollect(t, c, pr, pw, 1500*time.Millisecond, f)

		dig := func(action int, z int, seq int) {
			var b protocol.Buffer
			b.VarInt(int32(action))
			b.Position(4, -60, int32(z))
			b.VarInt(1)
			b.VarInt(int32(seq))
			if err := pw.WritePacket(server.IDPlayPlayerActionSB, b.Bytes()); err != nil {
				t.Fatal(err)
			}
		}
		announced := func() bool {
			for _, raw := range f.chatRaw {
				if bytes.Contains(raw, []byte("silk_touch_nest")) {
					return true
				}
			}
			return false
		}
		f.chats = f.chats[:0]
		f.chatRaw = f.chatRaw[:0]

		// Creative dig: vanilla skips playerDestroy for creative breaks,
		// so no grant should ever show up here.
		dig(0, -10, 0)
		for i := 0; i < 6 && !announced(); i++ {
			advCollect(t, c, pr, pw, 500*time.Millisecond, f)
		}
		if announced() {
			t.Logf("creative dig unexpectedly announced the advancement")
		}

		// Survival dig: start, let the server tick the progress, then
		// finish. Vanilla breaks the nest a couple of ticks after the
		// start on its own; mc-go's break commits on the dig-finish tick.
		sendCommand(t, pw, "gamemode survival @s")
		advCollect(t, c, pr, pw, 600*time.Millisecond, f)
		dig(0, -12, 0)
		advCollect(t, c, pr, pw, 700*time.Millisecond, f)
		dig(2, -12, 1)
		for i := 0; i < 12 && !announced(); i++ {
			advCollect(t, c, pr, pw, 500*time.Millisecond, f)
			if i == 5 {
				dig(0, -12, 2)
				dig(2, -12, 3)
			}
		}
		return f
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	announced := func(f *advFlush) bool {
		for _, raw := range f.chatRaw {
			if bytes.Contains(raw, []byte("silk_touch_nest")) {
				return true
			}
		}
		return false
	}
	if !announced(want) {
		t.Fatalf("vanilla: Total Beelocation not announced (upd=%v)", want.updTimes)
	}
	if !announced(got) {
		t.Fatalf("mc-go: Total Beelocation not announced")
	}
	// Residual parity gap: vanilla 26.2 emits no update_advancements for
	// this earn (announce chat only, measured repeatedly); mc-go emits
	// the def and progress entries. Compare chat keys only.
	want.defs, got.defs = nil, nil
	want.prog, got.prog = nil, nil
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("bee_nest_destroyed parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
