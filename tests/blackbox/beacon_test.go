package blackbox

// TestConstructBeaconParity builds a four-layer beacon pyramid out of
// iron blocks and expects the beacon to activate: vanilla fires
// construct_beacon for every player inside its activation box
// (x/z ±10, y −9…+5), granting nether/create_beacon ("beacon", min
// level 1) and nether/create_full_beacon ("beacon", level 4), and plays
// block.beacon.activate. The base rescan cadence is every 80 game
// ticks, so the wait budget is generous.

import (
	"net"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestConstructBeaconParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BeacT"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
	})
	defer targets.cleanup()

	drainInto := func(c net.Conn, pr *protocol.Reader, pw *protocol.Writer, f *advFlush, d time.Duration, who string) {
		end := time.Now().Add(d)
		for time.Now().Before(end) {
			_ = c.SetReadDeadline(time.Now().Add(time.Until(end)))
			p, err := pr.ReadPacket()
			if err != nil {
				break
			}
			switch p.ID {
			case server.IDPlayUpdateAdvance:
				f.advPkts++
				decodeUpdateAdv(t, p.Data, f)
			case server.IDPlaySystemChatCB:
				f.chats = append(f.chats, chatKey(p.Data))
			case server.IDPlayKeepAliveCB:
				var kb protocol.Buffer
				kb.Raw(p.Data)
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, kb.Bytes())
			case server.IDPlaySyncPos:
				if id, err := protocol.NewPacketReader(p.Data).VarInt(); err == nil {
					var tb protocol.Buffer
					tb.VarInt(id)
					_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, tb.Bytes())
				}
			case server.IDPlayChunkBatchDone:
				_ = ackChunkBatch(pw)
			case 117: // sound
				r := protocol.NewPacketReader(p.Data)
				if id, err := r.VarInt(); err == nil {
					f.soundIDs[id] = true
				}
			}
		}
	}

	run := func(addr, who string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}, soundIDs: map[int32]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "BeacT")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		drainInto(c, pr, pw, f, 2500*time.Millisecond, who)
		// Cold-boot vanilla on a fresh world can starve the command queue;
		// ping until it answers.
		ready := false
		for tries := 0; tries < 10 && !ready; tries++ {
			before := len(f.chats)
			sendCommand(t, pw, "data get entity @s Health")
			drainInto(c, pr, pw, f, 700*time.Millisecond, who)
			for range f.chats[before:] {
				ready = true
			}
		}
		if !ready {
			t.Fatalf("%s: commands never answered (chats=%v)", who, f.chats)
		}
		// Full four-layer pyramid, beacon on top, player parked inside the
		// activation box before the beacon lands.
		for _, cmd := range []string{
			"fill 26 -60 26 34 -60 34 minecraft:iron_block", // 9x9
			"fill 27 -59 27 33 -59 33 minecraft:iron_block", // 7x7
			"fill 28 -58 28 32 -58 32 minecraft:iron_block", // 5x5
			"fill 29 -57 29 31 -57 31 minecraft:iron_block", // 3x3
			"tp @s 24.5 -60 24.5 0 0",
			"setblock 30 -56 30 minecraft:beacon",
		} {
			sendCommand(t, pw, cmd)
			drainInto(c, pr, pw, f, 600*time.Millisecond, who)
		}
		// Vanilla rescans the base every 80 ticks; budget three windows.
		drainInto(c, pr, pw, f, 15*time.Second, who)
		return f
	}

	want := run(targets.vanilla, "vanilla")
	got := run(targets.mcgo, "mc-go")
	for _, id := range []string{"minecraft:nether/create_beacon", "minecraft:nether/create_full_beacon"} {
		if !want.prog[id]["beacon"] {
			t.Fatalf("vanilla: %s beacon criterion never obtained (prog=%v)", id, want.prog)
		}
		if got.prog[id]["beacon"] != want.prog[id]["beacon"] {
			t.Fatalf("%s beacon criterion differs: vanilla=%v mc-go=%v", id, want.prog[id]["beacon"], got.prog[id]["beacon"])
		}
	}
	// block.beacon.activate on activation — wire id 153 = registry 152 + 1
	// (sound_event holder encoding is one-based).
	if !want.soundIDs[153] {
		t.Fatalf("vanilla: no beacon activate sound (sounds=%v)", want.soundIDs)
	}
	if got.soundIDs[153] != want.soundIDs[153] {
		t.Fatalf("beacon activate sound differs: vanilla=%v mc-go=%v", want.soundIDs[153], got.soundIDs[153])
	}
	// Command feedback is harness input, not outcome.
	want.chats, got.chats = nil, nil
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("construct_beacon parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
