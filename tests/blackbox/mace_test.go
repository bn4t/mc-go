package blackbox

// TestOveroverkillParity mace-smashes a NoAI pig from ~90 blocks up
// and diffs the advancement flush: adventure/overoverkill requires a
// player_hurt_entity hit whose direct entity holds a mace, carries
// the mace_smash damage tag, and deals ≥100 raw damage — only the
// piecewise fall bonus (22 + fall-8 past 8) gets a plain mace over
// that line.
//
// Fall distance is built the way a real client builds it: a stream
// of descending onGround=false move packets, ending two blocks above
// the pig (in attack reach, still airborne). The mace's ~33-tick
// cooldown means the descent doubles as the charge wait.

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestOveroverkillParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Mace"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	run := func(addr string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "Mace")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)

		var pigID int32 = -1
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
				case server.IDPlaySpawnEntity:
					r := protocol.NewPacketReader(p.Data)
					eid, err := r.VarInt()
					if err != nil {
						continue
					}
					_, _ = r.Long()
					_, _ = r.Long()
					et, err := r.VarInt()
					if err == nil && et == 100 { // pig
						pigID = eid
					}
				case server.IDPlayUpdateAdvance:
					decodeUpdateAdv(t, p.Data, f)
				case server.IDPlaySystemChatCB:
					f.chats = append(f.chats, chatKey(p.Data))
				}
			}
		}

		sendCommand(t, pw, "advancement grant @s only minecraft:adventure/minecraft_trials_edition")
		sendCommand(t, pw, "summon minecraft:pig 8 -60 5 {NoAI:1b}")
		sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:mace")
		sendCommand(t, pw, "tp @s 8 30 5 180 0")
		drain(1200 * time.Millisecond)
		if pigID < 0 {
			t.Fatalf("pig spawn packet not seen")
		}

		// 90-block fall in 2-block steps, ~20 packets/second like a
		// real client's movement cadence. Ends at feet y=-58: inside
		// the 3-block attack reach of the pig's box top (-59.1).
		y := 30.0
		for i := 0; i < 45; i++ {
			y -= 2
			var mb protocol.Buffer
			mb.Double(8.5)
			mb.Double(y)
			mb.Double(5.5)
			mb.Bool(false) // onGround: still falling
			_ = pw.WritePacket(server.IDPlayMovePosSB, mb.Bytes())
			time.Sleep(40 * time.Millisecond)
			if i%10 == 9 {
				drain(50 * time.Millisecond)
			}
		}
		drain(300 * time.Millisecond)

		var ib protocol.Buffer
		ib.VarInt(pigID)
		_ = pw.WritePacket(server.IDPlayAttackSB, ib.Bytes())
		drain(4 * time.Second)
		return f
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	if !want.prog["minecraft:adventure/overoverkill"]["overoverkill"] {
		t.Fatalf("vanilla: overoverkill not obtained: %v", want.prog["minecraft:adventure/overoverkill"])
	}
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("overoverkill parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
