package blackbox

// TestMobKillsPlayerParity drops a hostile zombie next to a survival
// player and lets it do the killing. The differential surface is the
// advancement flush (adventure/root's killed_by_something criterion —
// the entity_killed_player trigger — plus every def/progress/removed
// the tree lights) and the chat multiset (announce + death message).
// The mob does the work, so per-leg timing differs between servers;
// the compare is on the cumulative union after the death lands.

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestMobKillsPlayerParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("MKill"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
	})
	defer targets.cleanup()

	run := func(addr string) (f *advFlush, died bool) {
		f = &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "MKill")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)
		sendCommand(t, pw, "gamemode survival @s")
		sendCommand(t, pw, "tp @s 4 -60 2 -90 0")
		sendCommand(t, pw, "summon minecraft:zombie 6.5 -60 2.5")
		// The zombie closes ~2.5 blocks and swings every 20 ticks; a
		// full-health player falls on the seventh hit (~8s). Watch for
		// the combat-death packet the whole way.
		end := time.Now().Add(20 * time.Second)
		for time.Now().Before(end) && !died {
			_ = c.SetReadDeadline(time.Now().Add(400 * time.Millisecond))
			p, err := pr.ReadPacket()
			if err != nil {
				continue
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
			case server.IDPlayCombatDeath:
				died = true
			}
		}
		// The entity_killed_player grant lands on the kill tick but the
		// advancement flush follows a tick or two later — keep draining
		// briefly after death or the criterion read races it.
		if died {
			advCollect(t, c, pr, pw, 1500*time.Millisecond, f)
		}
		return f, died
	}

	want, wantDied := run(targets.vanilla)
	got, gotDied := run(targets.mcgo)
	if !wantDied {
		t.Fatalf("vanilla: zombie never killed the player")
	}
	if !gotDied {
		t.Fatalf("mc-go: zombie never killed the player")
	}
	// vanilla grants the killed_by_something criterion; without it the
	// union compare could pass on empty sets.
	if !want.prog["minecraft:adventure/root"]["killed_by_something"] {
		t.Fatalf("vanilla: adventure/root killed_by_something not obtained: %v", want.prog["minecraft:adventure/root"])
	}
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("mob-kill parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
