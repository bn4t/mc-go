package blackbox

// TestAdvRejoinParity kills a zombie, disconnects, rejoins, and diffs
// the update_advancements sync each server sends on the second join.
// Vanilla persists criterion progress in advancements/<uuid>.json and
// replays the full visible set — defs and progress — because client
// state resets every session; mc-go persists the same state in its
// .data.json player blob and must emit the identical union.

import (
	"fmt"
	"net"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// advCollect drains keepalive/syncpos/update_advancements/system_chat
// for d into f.
func advCollect(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, d time.Duration, f *advFlush) {
	t.Helper()
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
		case server.IDPlayChunkBatchDone:
			_ = ackChunkBatch(pw)
		case server.IDPlayUpdateAdvance:
			decodeUpdateAdv(t, p.Data, f)
		case server.IDPlaySystemChatCB:
			f.chats = append(f.chats, chatKey(p.Data))
			f.chatRaw = append(f.chatRaw, p.Data)
		case server.IDPlayEntityEvent:
			r := protocol.NewPacketReader(p.Data)
			if _, err := r.Int(); err == nil {
				if st, err := r.Byte(); err == nil && st == 35 {
					f.ev35++
				}
			}
		case server.IDPlayUpdateMobEffect:
			r := protocol.NewPacketReader(p.Data)
			if _, err := r.VarInt(); err == nil { // entity id
				if eff, err := r.VarInt(); err == nil {
					amp, e1 := r.VarInt()
					dur, e2 := r.VarInt()
					fl, e3 := r.Byte()
					if e1 == nil && e2 == nil && e3 == nil {
						if f.mobFx == nil {
							f.mobFx = map[int32]string{}
						}
						if _, seen := f.mobFx[eff]; !seen {
							f.mobFx[eff] = fmt.Sprintf("%d,%d,%d", amp, dur, fl)
						}
					}
				}
			}
		case server.IDPlayRemoveMobEffect:
			f.fxRemoved = append(f.fxRemoved, true)
		case server.IDPlaySpawnEntity:
			if f.spawnTypes != nil {
				r := protocol.NewPacketReader(p.Data)
				id, e1 := r.VarInt()
				_, e2 := r.Read(16)
				typ, e3 := r.VarInt()
				if e1 == nil && e2 == nil && e3 == nil {
					f.spawnTypes[typ] = append(f.spawnTypes[typ], id)
				}
			}
		}
	}
}

func TestAdvRejoinParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("AdvRej"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
	})
	defer targets.cleanup()

	run := func(addr string) (first, rejoin *advFlush) {
		first = &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "AdvRej")
		advCollect(t, c, pr, pw, 1200*time.Millisecond, first)
		sendCommand(t, pw, "gamemode survival @s")
		sendCommand(t, pw, "tp @s 4 -60 2 -90 0")
		sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:stone_sword")
		advCollect(t, c, pr, pw, 900*time.Millisecond, first)

		sendCommand(t, pw, "summon minecraft:zombie 6.5 -60 2.5 {NoAI:1b}")
		var eid int32 = -1
		end := time.Now().Add(1200 * time.Millisecond)
		for time.Now().Before(end) && eid < 0 {
			_ = c.SetReadDeadline(time.Now().Add(time.Until(end)))
			p, err := pr.ReadPacket()
			if err != nil {
				break
			}
			if p.ID != server.IDPlaySpawnEntity {
				continue
			}
			r := protocol.NewPacketReader(p.Data)
			id, _ := r.VarInt()
			r.UUID()
			r.VarInt()
			ex, _ := r.Double()
			ey, _ := r.Double()
			ez, _ := r.Double()
			if int32(ex) == 6 && int32(ey) == -60 && int32(ez) == 2 {
				eid = id
			}
		}
		if eid < 0 {
			t.Fatalf("%s: zombie never spawned", addr)
		}
		// Stone sword deals 5 damage; a zombie has 20. Swing generously
		// past the death notice — extra attacks on a corpse are silent.
		for i := 0; i < 12; i++ {
			var mb protocol.Buffer
			mb.Double(4)
			mb.Double(-60)
			mb.Double(2)
			mb.Byte(0x01)
			_ = pw.WritePacket(0x1E, mb.Bytes())
			var ab protocol.Buffer
			ab.VarInt(eid)
			_ = pw.WritePacket(0x01, ab.Bytes())
			advCollect(t, c, pr, pw, 500*time.Millisecond, first)
		}
		c.Close()
		time.Sleep(400 * time.Millisecond)

		rejoin = &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c2, pr2, pw2 := joinPlayBlackbox(t, addr, "AdvRej")
		advCollect(t, c2, pr2, pw2, 2500*time.Millisecond, rejoin)
		c2.Close()
		return first, rejoin
	}

	want1, want2 := run(targets.vanilla)
	got1, got2 := run(targets.mcgo)

	// The kill must actually have granted Monster Hunter on both sides,
	// or the rejoin compare would pass vacuously.
	for tag, f := range map[string]*advFlush{"vanilla": want1, "mc-go": got1} {
		if crits := f.prog["minecraft:adventure/kill_a_mob"]; crits["minecraft:zombie"] != true {
			t.Fatalf("%s: kill_a_mob not obtained, got %v", tag, crits)
		}
	}
	if diffs := diffAdvFlush(want2, got2); len(diffs) > 0 {
		t.Fatalf("rejoin sync: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
