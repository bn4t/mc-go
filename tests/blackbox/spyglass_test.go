package blackbox

// TestSpyglassAdvParity scopes a ghast and a parrot on both servers:
// Use Item on a spyglass puts the item in continuous use; each tick
// using_item runs and the player predicate's looking_at raycast must
// land on the named entity type for adventure/spyglass_at_*.

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestSpyglassAdvParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Spy"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
	})
	defer targets.cleanup()

	run := func(addr string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "Spy")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)
		sendCommand(t, pw, "gamerule doMobSpawning false")
		sendCommand(t, pw, "kill @e[type=!player]")
		// Facing +z at eye height: the parrot sits low, the ghast floats
		// at eye line — pitch steers the ray between them.
		sendCommand(t, pw, "tp @s 4 -60 2 0 -8")
		sendCommand(t, pw, "summon minecraft:ghast 4 -60 15 {NoAI:1b}")
		sendCommand(t, pw, "summon minecraft:parrot 4 -60 -28 {NoAI:1b}")
		sendCommand(t, pw, "summon minecraft:ender_dragon 60 -60 2 {NoAI:1b}")
		sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:spyglass")
		advCollect(t, c, pr, pw, 700*time.Millisecond, f)

		// The Use Item packet carries the client's yaw/pitch and vanilla
		// applies them over the last move — send the real aim, not 0 0.
		use := func(yaw, pitch float32) {
			var ub protocol.Buffer
			ub.VarInt(0)
			ub.VarInt(0)
			ub.Float(yaw)
			ub.Float(pitch)
			_ = pw.WritePacket(server.IDPlayUseItemSB, ub.Bytes())
		}
		release := func() {
			var ab protocol.Buffer
			ab.VarInt(5)
			ab.Position(0, 0, 0)
			ab.Byte(0)
			ab.VarInt(0)
			_ = pw.WritePacket(server.IDPlayPlayerActionSB, ab.Bytes())
		}
		// Scope the parrot first — spyglass_at_ghast's def only becomes
		// visible once its parrot parent grants, so the ghast must come
		// second or vanilla never emits it.
		sendCommand(t, pw, "tp @s 4 -60 2 180 2")
		advCollect(t, c, pr, pw, 500*time.Millisecond, f)
		use(180, 2)
		advCollect(t, c, pr, pw, 1500*time.Millisecond, f)
		release()
		advCollect(t, c, pr, pw, 300*time.Millisecond, f)
		sendCommand(t, pw, "tp @s 4 -60 2 0 0")
		sendCommand(t, pw, "tp @e[type=ghast] 4 -60 15 0 0")
		advCollect(t, c, pr, pw, 500*time.Millisecond, f)
		use(0, 0)
		advCollect(t, c, pr, pw, 1500*time.Millisecond, f)
		release()
		advCollect(t, c, pr, pw, 300*time.Millisecond, f)
		// The dragon sits east (+x → yaw -90); its 8-block tall box
		// swallows the level ray even a few blocks off eye height.
		sendCommand(t, pw, "tp @s 4 -60 2 -90 0")
		sendCommand(t, pw, "tp @e[type=ender_dragon] 60 -60 2 -90 0")
		advCollect(t, c, pr, pw, 500*time.Millisecond, f)
		use(-90, 0)
		advCollect(t, c, pr, pw, 1500*time.Millisecond, f)
		release()
		advCollect(t, c, pr, pw, 600*time.Millisecond, f)
		return f
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	for _, tc := range [][2]string{
		{"minecraft:adventure/spyglass_at_parrot", "spyglass_at_parrot"},
		{"minecraft:adventure/spyglass_at_ghast", "spyglass_at_ghast"},
		{"minecraft:adventure/spyglass_at_dragon", "spyglass_at_dragon"},
	} {
		if !want.prog[tc[0]][tc[1]] {
			t.Fatalf("vanilla: %s[%s] not obtained: %v", tc[0], tc[1], want.prog[tc[0]])
		}
	}
	want.chats, got.chats = nil, nil
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("spyglass parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
