package blackbox

// TestPlayerShearedEquipmentParity shears wolf_armor off a tamed wolf
// and diffs the advancement flush: husbandry/remove_wolf_armor's
// remove_wolf_armor criterion is player_sheared_equipment with
// entity=wolf and item=wolf_armor — the item predicate matches the
// REMOVED stack, not the shears.
//
// Vanilla's Entity.interact shears branch: Items.SHEARS in hand,
// Mob.canShearEquipment (Wolf overrides it to isOwnedBy — the wolf is
// summoned with Owner=<the player> so it counts tame), and the player
// must NOT be sneaking. Mob.shearItem clears the BODY slot, drops the
// armor, wears the shears one point, and fires the trigger. The parent
// husbandry/tame_an_animal is granted first so the child is visible.

import (
	"fmt"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestPlayerShearedEquipmentParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("ShearP"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	run := func(addr string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "ShearP")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)

		sendCommand(t, pw, "gamemode survival @s")
		sendCommand(t, pw, "advancement grant @s only minecraft:husbandry/tame_an_animal")
		sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:shears")
		sendCommand(t, pw, "tp @s 8.5 -60 10.5 180 0")
		advCollect(t, c, pr, pw, 900*time.Millisecond, f)

		// Wolf owned by the player so canShearEquipment passes, with
		// wolf_armor already in the BODY slot.
		u := server.OfflineUUID("ShearP")
		sendCommand(t, pw, fmt.Sprintf(`summon minecraft:wolf 8.5 -60 8.5 {NoAI:1b,Owner:[I;%d,%d,%d,%d],equipment:{body:{id:"minecraft:wolf_armor",count:1}}}`,
			int32(u[0])<<24|int32(u[1])<<16|int32(u[2])<<8|int32(u[3]),
			int32(u[4])<<24|int32(u[5])<<16|int32(u[6])<<8|int32(u[7]),
			int32(u[8])<<24|int32(u[9])<<16|int32(u[10])<<8|int32(u[11]),
			int32(u[12])<<24|int32(u[13])<<16|int32(u[14])<<8|int32(u[15])))

		var wolfID int32
		end := time.Now().Add(10 * time.Second)
		for time.Now().Before(end) && wolfID == 0 {
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
			case server.IDPlaySpawnEntity:
				r := protocol.NewPacketReader(p.Data)
				id, e1 := r.VarInt()
				_, e2 := r.Read(16)
				typ, e3 := r.VarInt()
				if e1 == nil && e2 == nil && e3 == nil && typ == 149 {
					wolfID = id
				}
			case server.IDPlayUpdateAdvance:
				decodeUpdateAdv(t, p.Data, f)
			case server.IDPlaySystemChatCB:
				f.chats = append(f.chats, chatKey(p.Data))
			}
		}
		if wolfID == 0 {
			t.Fatalf("no wolf spawn seen")
		}
		var ib protocol.Buffer
		ib.VarInt(wolfID)
		ib.VarInt(0) // main hand
		ib.Byte(0)   // LpVec3 zero
		ib.Bool(false)
		_ = pw.WritePacket(server.IDPlayInteractSB, ib.Bytes())
		advCollect(t, c, pr, pw, 3*time.Second, f)
		return f
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	if !want.prog["minecraft:husbandry/remove_wolf_armor"]["remove_wolf_armor"] {
		t.Fatalf("vanilla: remove_wolf_armor not obtained: %v", want.prog["minecraft:husbandry/remove_wolf_armor"])
	}
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("player_sheared_equipment parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
