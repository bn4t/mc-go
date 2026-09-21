package blackbox

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestFallAfterExplosionParity: a wind_charge bursting on the player
// latches the impact position (onExplosionHit); when the client then
// reports rising past the apex and falling, the first falling tick
// fires fall_after_explosion — distance.y.min 7 on
// adventure/who_needs_rockets measures impact->apex rise.
func TestFallAfterExplosionParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BoomP"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	type result struct {
		f      *advFlush
		burst  bool // charge despawned/exploded
		motion bool // victim got a velocity packet (explode or set_entity_motion)
	}

	run := func(addr string) result {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "BoomP")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)

		sendCommand(t, pw, "gamemode survival @s")
		sendCommand(t, pw, "tp @s 8.5 -60 8.5 0 0")
		advCollect(t, c, pr, pw, 600*time.Millisecond, f)
		// Charge hovering at the player's feet, nudged down — it meets
		// the player body and bursts there.
		sendCommand(t, pw, "summon minecraft:wind_charge 8.5 -60.2 8.5 {Motion:[0d,-0.2d,0d]}")

		r := result{f: f}
		burstAt := time.Now().Add(8 * time.Second)
		for time.Now().Before(burstAt) && !r.burst {
			_ = c.SetReadDeadline(time.Now().Add(time.Until(burstAt)))
			p, err := pr.ReadPacket()
			if err != nil {
				break
			}
			switch p.ID {
			case server.IDPlayKeepAliveCB:
				var kb protocol.Buffer
				kb.Raw(p.Data)
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, kb.Bytes())
			case server.IDPlayChunkBatchDone:
				_ = ackChunkBatch(pw)
			case server.IDPlayUpdateAdvance:
				decodeUpdateAdv(t, p.Data, f)
			case 0x24: // explode
				r.burst = true
				r.motion = true
			case 0x65: // set_entity_motion
				r.motion = true
			case server.IDPlayRemoveEntity:
				r.burst = true
			case server.IDPlaySystemChatCB:
				f.chats = append(f.chats, chatKey(p.Data))
			}
		}
		if !r.burst {
			return r
		}

		// Emulate what the real client does with the impulse: climb
		// ~9 blocks from the impact (-60) then fall back. Rising
		// moves keep fallDistance at 0; the first descending packet
		// latches the fall start on both servers.
		y := -60.0
		for i := 0; i < 9; i++ {
			y += 1.0
			var b protocol.Buffer
			b.Double(8.5)
			b.Double(y)
			b.Double(8.5)
			b.Byte(0) // airborne
			_ = pw.WritePacket(server.IDPlayMovePosSB, b.Bytes())
			time.Sleep(50 * time.Millisecond)
		}
		for i := 0; i < 9; i++ {
			y -= 1.0
			var b protocol.Buffer
			b.Double(8.5)
			b.Double(y)
			b.Double(8.5)
			b.Byte(0)
			_ = pw.WritePacket(server.IDPlayMovePosSB, b.Bytes())
			time.Sleep(50 * time.Millisecond)
		}
		var b protocol.Buffer
		b.Double(8.5)
		b.Double(-60.0)
		b.Double(8.5)
		b.Byte(1) // on ground
		_ = pw.WritePacket(server.IDPlayMovePosSB, b.Bytes())

		end := time.Now().Add(4 * time.Second)
		for time.Now().Before(end) {
			_ = c.SetReadDeadline(time.Now().Add(time.Until(end)))
			p, err := pr.ReadPacket()
			if err != nil {
				break
			}
			switch p.ID {
			case server.IDPlayKeepAliveCB:
				var kb protocol.Buffer
				kb.Raw(p.Data)
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, kb.Bytes())
			case server.IDPlayUpdateAdvance:
				decodeUpdateAdv(t, p.Data, f)
			case server.IDPlaySystemChatCB:
				f.chats = append(f.chats, chatKey(p.Data))
			case 0x24, 0x65: // explode / set_entity_motion
				r.motion = true
			}
			if f.prog["minecraft:adventure/who_needs_rockets"]["who_needs_rockets"] {
				return r
			}
		}
		return r
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)

	if !want.burst {
		t.Fatalf("vanilla: wind charge never burst")
	}
	if !want.f.prog["minecraft:adventure/who_needs_rockets"]["who_needs_rockets"] {
		t.Fatalf("vanilla: adventure/who_needs_rockets not granted after burst + fall")
	}
	diffAdvFlush(want.f, got.f)
	if got.burst != want.burst {
		t.Errorf("mc-go burst = %v, vanilla %v", got.burst, want.burst)
	}
	if !got.motion {
		t.Errorf("mc-go never sent the victim a velocity packet")
	}
}
