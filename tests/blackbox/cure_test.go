package blackbox

import (
	"fmt"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestCuredZombieVillagerParity: a zombie_villager summoned with
// ConversionTime + ConversionPlayer NBT (vanilla's save fields for an
// in-progress cure) counts down, is replaced by a villager, and grants
// the curing player minecraft:story/cure_zombie_villager.
func TestCuredZombieVillagerParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("CureP"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	type result struct {
		f          *advFlush
		zombieGone bool
		villSeen   bool
	}

	run := func(addr string) result {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "CureP")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)

		sendCommand(t, pw, "gamemode survival @s")
		u := server.OfflineUUID("CureP")
		sendCommand(t, pw, fmt.Sprintf(`summon minecraft:zombie_villager 8.5 -60 8.5 {NoAI:1b,ConversionTime:5,ConversionPlayer:[I;%d,%d,%d,%d]}`,
			int32(u[0])<<24|int32(u[1])<<16|int32(u[2])<<8|int32(u[3]),
			int32(u[4])<<24|int32(u[5])<<16|int32(u[6])<<8|int32(u[7]),
			int32(u[8])<<24|int32(u[9])<<16|int32(u[10])<<8|int32(u[11]),
			int32(u[12])<<24|int32(u[13])<<16|int32(u[14])<<8|int32(u[15])))

		r := result{f: f}
		var zid int32
		end := time.Now().Add(10 * time.Second)
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
			case server.IDPlayChunkBatchDone:
				_ = ackChunkBatch(pw)
			case server.IDPlaySpawnEntity:
				rp := protocol.NewPacketReader(p.Data)
				id, e1 := rp.VarInt()
				_, e2 := rp.Read(16)
				typ, e3 := rp.VarInt()
				if e1 == nil && e2 == nil && e3 == nil {
					if typ == 154 {
						zid = id
					}
					if typ == 140 && zid != 0 {
						r.villSeen = true
					}
				}
			case server.IDPlayRemoveEntity:
				rp := protocol.NewPacketReader(p.Data)
				n, _ := rp.VarInt()
				for i := int32(0); i < n; i++ {
					id, _ := rp.VarInt()
					if id == zid && zid != 0 {
						r.zombieGone = true
					}
				}
			case server.IDPlayUpdateAdvance:
				decodeUpdateAdv(t, p.Data, f)
			case server.IDPlaySystemChatCB:
				f.chats = append(f.chats, chatKey(p.Data))
			}
			if r.zombieGone && r.villSeen && f.prog["minecraft:story/cure_zombie_villager"]["cured_zombie"] {
				break
			}
		}
		return r
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)

	if !want.zombieGone || !want.villSeen {
		t.Fatalf("vanilla: cure did not swap entities (zombieGone=%v villSeen=%v)", want.zombieGone, want.villSeen)
	}
	if !want.f.prog["minecraft:story/cure_zombie_villager"]["cured_zombie"] {
		t.Fatalf("vanilla: story/cure_zombie_villager not granted")
	}
	diffAdvFlush(want.f, got.f)
	if got.zombieGone != want.zombieGone {
		t.Errorf("mc-go zombie removed = %v, vanilla %v", got.zombieGone, want.zombieGone)
	}
	if got.villSeen != want.villSeen {
		t.Errorf("mc-go villager spawned = %v, vanilla %v", got.villSeen, want.villSeen)
	}
}
