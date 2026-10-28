package blackbox

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestRespawnTravelBurstParity pins the level-info bursts a client gets
// when its level is replaced. Dimension travel follows ServerPlayer's
// teleport to another level — Respawn, difficulty, permission level,
// position, abilities, sendLevelInfo (border, time, default spawn,
// weather, chunk-wait event, tick state), inventory, held slot, effects —
// and a death respawn follows PlayerList.respawn — Respawn, position,
// default spawn, difficulty, experience, effects, sendLevelInfo,
// permission level, inventory — and in both cases the next tick's
// health and experience resends. Without the chunk-wait event a real
// client keeps its loading screen up after the level resets. A second
// kill right after a respawn does nothing: the respawned player cannot
// be hurt until its client reports the terrain loaded or the grace
// runs out.
//
// The comparison is the ordered list of those packet kinds (game and
// entity events by their type) from Respawn up to the first chunk, for
// a nether teleport, a respawn back in the overworld from there, and a
// same-dimension respawn.
func TestRespawnTravelBurstParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("RB")}, nil)
	defer targets.cleanup()

	run := func(label, addr string) []string {
		c, pr, pw := joinPlayBlackbox(t, addr, "RB")
		defer c.Close()
		sendClientInformation(t, pw)
		time.Sleep(400 * time.Millisecond)
		drainChunkWindow(t, label, c, pr, pw, 300*time.Millisecond)
		sendCommand(t, pw, "gamerule immediate_respawn true")
		sendCommand(t, pw, "effect give @s minecraft:speed 600 1 true")
		drainChunkWindow(t, label, c, pr, pw, 300*time.Millisecond)
		var phases []string
		sendCommand(t, pw, "execute in minecraft:the_nether run tp @s 0 100 0")
		phases = append(phases, "travel: "+burstKinds(t, c, pr, pw, false))
		sendCommand(t, pw, "kill @s")
		phases = append(phases, "respawn-across: "+burstKinds(t, c, pr, pw, true))
		sendCommand(t, pw, "kill @s")
		phases = append(phases, "respawn-same: "+burstKinds(t, c, pr, pw, true))
		return phases
	}
	want := run("vanilla", targets.vanilla)
	got := run("mcgo", targets.mcgo)
	for i := range want {
		_, kinds, _ := strings.Cut(want[i], ": ")
		// The third kill lands while the respawned client is still
		// loading, so vanilla shows nothing there.
		if (i < 2) != strings.HasPrefix(kinds, "respawn") {
			t.Fatalf("vanilla phase %d: %s", i, want[i])
		}
		if got[i] != want[i] {
			t.Errorf("phase %d:\n  vanilla %s\n  mc-go   %s", i, want[i], got[i])
		}
	}
}

// burstKinds reads until a Respawn arrives (answering a death with
// Client Status perform-respawn when respawn is set) and then records
// the burst's packet kinds up to the first chunk.
func burstKinds(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, respawn bool) string {
	t.Helper()
	names := map[int32]string{
		server.IDPlayRespawn: "respawn", server.IDPlaySyncPos: "pos",
		server.IDPlayDefaultSpawn: "spawn", server.IDPlayDifficulty: "difficulty",
		server.IDPlayAbilitiesCB: "abilities", server.IDPlayInitializeBorder: "border",
		server.IDPlayUpdateTime: "time", server.IDPlayTickingState: "tick_state",
		server.IDPlayTickingStep: "tick_step", server.IDPlayContainerContent: "inventory",
		server.IDPlaySetHeldItemCB: "held", server.IDPlayUpdateMobEffect: "effect",
		server.IDPlaySetExperience: "xp", server.IDPlaySetHealth: "health",
	}
	var out []string
	started := false
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(deadline)
		p, err := pr.ReadPacket()
		if err != nil {
			break
		}
		id := int32(p.ID)
		switch id {
		case server.IDPlayKeepAliveCB:
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
			continue
		case server.IDPlayChunkBatchDone:
			_ = ackChunkBatch(pw)
		case server.IDPlayCombatDeath:
			if respawn && !started {
				var b protocol.Buffer
				b.VarInt(0) // perform respawn
				_ = pw.WritePacket(server.IDPlayClientStatusSB, b.Bytes())
			}
		case server.IDPlaySyncPos:
			if tid, err := protocol.NewPacketReader(p.Data).VarInt(); err == nil {
				var b protocol.Buffer
				b.VarInt(tid)
				_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
			}
		}
		if id == server.IDPlayRespawn {
			started = true
		}
		if !started {
			continue
		}
		if id == server.IDPlayChunkData {
			break
		}
		switch id {
		case server.IDPlayGameEvent:
			if ev, err := protocol.NewPacketReader(p.Data).Byte(); err == nil {
				out = append(out, fmt.Sprintf("game_event:%d", ev))
			}
		case server.IDPlayEntityEvent:
			r := protocol.NewPacketReader(p.Data)
			_, _ = r.Int()
			if st, err := r.Byte(); err == nil && st >= 24 && st <= 28 {
				out = append(out, "op_level")
			}
		default:
			if n, ok := names[id]; ok {
				out = append(out, n)
			}
		}
	}
	_ = c.SetReadDeadline(time.Time{})
	// Time packets after the burst are the once-a-second time sync
	// ticking while the first chunk is prepared, not part of the burst.
	for len(out) > 0 && out[len(out)-1] == "time" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, " ")
}
