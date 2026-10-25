package blackbox

// TestDeflectArrowParity raises an offhand shield and lets a summoned
// arrow fly into the blocking player. Vanilla blocks the hit (the
// player faces the arrow's approach), plays item.shield.block, and
// entity_hurt_player with blocked=true lights story/deflect_arrow's
// deflected_projectile — "Not Today, Thank You" announces.

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestDeflectArrowParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Deflect"),
	}, nil)
	defer targets.cleanup()

	run := func(addr string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "Deflect")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		// Probe: last set_health (104) and the shield sound (117, id+1).
		lastHealth := float32(-1)
		sawShieldSound := false

		drain := func(d time.Duration) {
			end := time.Now().Add(d)
			for time.Now().Before(end) {
				_ = c.SetReadDeadline(time.Now().Add(time.Until(end)))
				p, err := pr.ReadPacket()
				if err != nil {
					return
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
				case server.IDPlayChunkBatchDone:
					ackChunkBatch(pw)
				case 104:
					r := protocol.NewPacketReader(p.Data)
					if h, err := r.Float(); err == nil {
						lastHealth = h
					}
				case 117:
					r := protocol.NewPacketReader(p.Data)
					if id, err := r.VarInt(); err == nil && id == 1456 {
						sawShieldSound = true
					}
				}
			}
		}
		drain(1200 * time.Millisecond)

		// Survival so damage lands; yaw 90 faces -x (west), toward the
		// arrow's approach — vanilla's block check needs the source
		// inside the view cone.
		sendCommand(t, pw, "gamemode survival")
		sendCommand(t, pw, "tp @s 30 -60 30 90 0")
		sendCommand(t, pw, "item replace entity @s weapon.offhand with minecraft:shield")
		drain(800 * time.Millisecond)

		// Raise: use_item for the offhand (hand 1). The shield holds
		// until release — the test never releases.
		var ub protocol.Buffer
		ub.VarInt(1) // off hand
		ub.VarInt(0) // sequence
		ub.Float(90) // yaw
		ub.Float(0)  // pitch
		_ = pw.WritePacket(server.IDPlayUseItemSB, ub.Bytes())
		drain(300 * time.Millisecond)

		// Arrow west of the player moving east: ~5 blocks at 0.6/tick
		// lands inside a second, falling only ~0.05*~8 below spawn y —
		// spawn high enough to stay inside the hitbox.
		sendCommand(t, pw, "summon minecraft:arrow 28 -59.4 30.5 {Motion:[0.8,0.0,0.0]}")
		drain(3 * time.Second)
		if lastHealth != 20 || !sawShieldSound {
			t.Fatalf("block signals: health=%v shieldSound=%v (want 20,true)", lastHealth, sawShieldSound)
		}
		return f
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	if !want.prog["minecraft:story/deflect_arrow"]["deflected_projectile"] {
		t.Fatalf("vanilla: deflected_projectile not obtained: %v", want.prog["minecraft:story/deflect_arrow"])
	}
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("deflect_arrow parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
