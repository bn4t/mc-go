package blackbox

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestAvoidVibrationParity: a sneaking player's step/hit_ground events
// are suppressed at every vibration listener that hears them — and any
// sculk_sensor (radius 8, calibrated_sculk_sensor 16) in earshot fires
// adventure/avoid_vibration for the sneak instead. Vanilla's check is
// VibrationSystem$User: isSteppingCarefully && event ∈
// ignore_vibrations_sneaking && canTriggerAvoidVibration → grant.
func TestAvoidVibrationParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("VibeP"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	run := func(addr string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "VibeP")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)

		sendCommand(t, pw, "tp @s 8.5 -60 12.5 0 0")
		sendCommand(t, pw, "setblock 8 -60 9 minecraft:sculk_sensor")
		sendCommand(t, pw, "give @s minecraft:bow")
		sendCommand(t, pw, "give @s minecraft:arrow")

		// Sneak on, then draw and release the bow. ITEM_INTERACT_START
		// is not in the sensor's vibration tag — the draw alone is
		// never evaluated — but stopUsingItem posts
		// ITEM_INTERACT_FINISH and the arrow spawn posts
		// PROJECTILE_SHOOT; both are sneaking-player emissions a sensor
		// in earshot dampens, granting avoid_vibration.
		var in protocol.Buffer
		in.Byte(0x20)
		_ = pw.WritePacket(server.IDPlayPlayerInputSB, in.Bytes())

		end := time.Now().Add(8 * time.Second)
		drawn := false
		actionAt := time.Now()
		for time.Now().Before(end) {
			if time.Now().After(actionAt) {
				if !drawn {
					var ub protocol.Buffer
					ub.VarInt(0) // main hand
					ub.VarInt(0) // sequence
					ub.Float(0)  // yaw
					ub.Float(0)  // pitch
					_ = pw.WritePacket(server.IDPlayUseItemSB, ub.Bytes())
					drawn = true
					actionAt = time.Now().Add(500 * time.Millisecond)
				} else {
					var rb protocol.Buffer
					rb.VarInt(5) // RELEASE_USE_ITEM
					rb.Position(8, -60, 12)
					rb.VarInt(1) // face
					rb.VarInt(0) // sequence
					_ = pw.WritePacket(server.IDPlayPlayerActionSB, rb.Bytes())
					drawn = false
					actionAt = time.Now().Add(600 * time.Millisecond)
				}
			}
			_ = c.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			p, err := pr.ReadPacket()
			if err != nil {
				continue
			}
			switch p.ID {
			case server.IDPlayKeepAliveCB:
				var kb protocol.Buffer
				kb.Raw(p.Data)
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, kb.Bytes())
			case server.IDPlayChunkBatchDone:
				_ = ackChunkBatch(pw)
			case server.IDPlaySyncPos:
				if id, err := protocol.NewPacketReader(p.Data).VarInt(); err == nil {
					var tb protocol.Buffer
					tb.VarInt(id)
					_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, tb.Bytes())
				}
			case server.IDPlayUpdateAdvance:
				decodeUpdateAdv(t, p.Data, f)
			case server.IDPlaySystemChatCB:
				f.chats = append(f.chats, chatKey(p.Data))
			}
			if f.prog["minecraft:adventure/avoid_vibration"]["avoid_vibration"] {
				break
			}
		}
		return f
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)

	if !want.prog["minecraft:adventure/avoid_vibration"]["avoid_vibration"] {
		t.Fatalf("vanilla: adventure/avoid_vibration not granted")
	}
	diffAdvFlush(want, got)
}
