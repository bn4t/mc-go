package blackbox

// TestThrownItemPickedUpByPlayerParity drops a thrown item onto the
// player and diffs the advancement flush: husbandry/
// allay_deliver_item_to_player grants when the picker collects an
// item entity whose Thrower (EntityReference) resolves to an allay.
//
// Vanilla fires THROWN_ITEM_PICKED_UP_BY_PLAYER from
// ServerPlayer.onItemPickup — the criterion's "entity" predicate is
// evaluated on the item's OWNER (getOwner = Thrower, not the Owner/
// target delivery field). The natural allay delivery path is AI —
// this test instead mints a known UUID on a summoned allay and spawns
// the item with Thrower:[I;...] pointing at it, which is equivalent
// for the trigger (owner type is all the predicate sees).
//
// The allay sits 5 blocks south of the pickup spot: an allay whose
// pickup cooldown is clear would steal the item on vanilla, so the
// item drops straight onto the player instead.

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestThrownItemPickedUpByPlayerParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Pickup"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	run := func(addr string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "Pickup")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)

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
				}
			}
		}

		sendCommand(t, pw, "fill 8 -60 4 8 -60 11 minecraft:stone")
		sendCommand(t, pw, "summon minecraft:allay 8.5 -59 5.0 {NoAI:1b,UUID:[I;1,2,3,4]}")
		sendCommand(t, pw, "tp @s 8.5 -59 10 0 0")
		drain(300 * time.Millisecond)
		sendCommand(t, pw, "summon minecraft:item 8.5 -58.5 10 {Item:{id:\"minecraft:cookie\",count:1},Thrower:[I;1,2,3,4],PickupDelay:0s}")
		drain(3 * time.Second)
		return f
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	if !want.prog["minecraft:husbandry/allay_deliver_item_to_player"]["allay_deliver_item_to_player"] {
		t.Fatalf("vanilla: allay_deliver_item_to_player not obtained: %v", want.prog["minecraft:husbandry/allay_deliver_item_to_player"])
	}
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("thrown_item_picked_up_by_player parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
