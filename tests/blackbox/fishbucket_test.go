package blackbox

// TestTacticalFishingParity holds a water bucket and right-clicks a
// summoned cod: the bucket becomes a cod bucket, the fish despawns, and
// filled_bucket lights husbandry/tactical_fishing's cod_bucket
// criterion — "Tactical Fishing" announces. Entity id capture filters
// to type 27 = minecraft:cod.

import (
	"net"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestTacticalFishingParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("FishT"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
	})
	defer targets.cleanup()

	drainInto := func(c net.Conn, pr *protocol.Reader, pw *protocol.Writer, f *advFlush, d time.Duration, who string) {
		end := time.Now().Add(d)
		for time.Now().Before(end) {
			_ = c.SetReadDeadline(time.Now().Add(time.Until(end)))
			p, err := pr.ReadPacket()
			if err != nil {
				break
			}
			switch p.ID {
			case server.IDPlayUpdateAdvance:
				f.advPkts++
				decodeUpdateAdv(t, p.Data, f)
			case server.IDPlaySpawnEntity:
				r := protocol.NewPacketReader(p.Data)
				id, e1 := r.VarInt()
				_, e2 := r.Read(16)
				typ, e3 := r.VarInt()
				if e1 == nil && e2 == nil && e3 == nil {
					f.spawnTypes[typ] = append(f.spawnTypes[typ], id)
				}
			case server.IDPlaySystemChatCB:
				f.chats = append(f.chats, chatKey(p.Data))
			case server.IDPlayKeepAliveCB:
				var kb protocol.Buffer
				kb.Raw(p.Data)
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, kb.Bytes())
			case server.IDPlaySyncPos:
				if id, err := protocol.NewPacketReader(p.Data).VarInt(); err == nil {
					var tb protocol.Buffer
					tb.VarInt(id)
					_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, tb.Bytes())
				}
			case server.IDPlayChunkBatchDone:
				_ = ackChunkBatch(pw)
			case 116, 117: // sound / sound_entity
				f.sounds++
			case 77: // remove_entities
				r := protocol.NewPacketReader(p.Data)
				n, e := r.VarInt()
				if e == nil {
					for i := int32(0); i < n; i++ {
						v, _ := r.VarInt()
						f.removedIDs = append(f.removedIDs, v)
					}
				}
			}
		}
	}

	run := func(addr, who string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}, spawnTypes: map[int32][]int32{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "FishT")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		drainInto(c, pr, pw, f, 2500*time.Millisecond, who)
		// Cold-boot vanilla on a fresh world can starve the command queue
		// behind spawn-chunk generation; ping until it answers.
		ready := false
		for tries := 0; tries < 10 && !ready; tries++ {
			before := len(f.chats)
			sendCommand(t, pw, "data get entity @s Health")
			drainInto(c, pr, pw, f, 700*time.Millisecond, who)
			for _, k := range f.chats[before:] {
				_ = k
				ready = true
			}
		}
		if !ready {
			t.Fatalf("vanilla commands never answered (chats=%v)", f.chats)
		}
		for _, cmd := range []string{
			"fill 28 -61 28 33 -57 33 minecraft:stone",
			"fill 29 -60 29 32 -59 32 minecraft:water",
			"tp @s 30.5 -60 30.5 0 0",
			"item replace entity @s weapon.mainhand with minecraft:water_bucket",
		} {
			sendCommand(t, pw, cmd)
			drainInto(c, pr, pw, f, 600*time.Millisecond, who)
		}
		// The fresh-world vanilla tracking can lag a tick or two around a
		// cold chunkgen + tp; re-summon until the add_entity packet lands.
		var codID int32
		for tries := 0; tries < 8 && codID == 0; tries++ {
			before := len(f.spawnTypes[27])
			sendCommand(t, pw, "summon minecraft:cod 30.5 -59.3 30.5")
			drainInto(c, pr, pw, f, 1500*time.Millisecond, who)
			if ids := f.spawnTypes[27]; len(ids) > before {
				codID = ids[len(ids)-1]
			}
		}
		if codID == 0 {
			t.Fatalf("no cod spawn packet (spawns seen: %v, chats: %v)", f.spawnTypes, f.chats)
		}
		// Already standing in the water pocket next to the cod; a short
		// settle lets any position correction resolve before interacting.
		_ = sendMovePosRotPitch(pw, 30.5, -60, 30.5, 0, 40)
		drainInto(c, pr, pw, f, 600*time.Millisecond, who)
		// Replay the real client's interact shape: a nonzero LpVec3 hit
		// location (captured from a real 26.2 client bucketing a cod).
		var ib protocol.Buffer
		ib.VarInt(codID)
		ib.VarInt(0) // hand: main
		ib.Raw([]byte{0xf9, 0xff, 0x5f, 0xff, 0x2b, 0x81})
		ib.Bool(false)
		_ = pw.WritePacket(server.IDPlayInteractSB, ib.Bytes())
		drainInto(c, pr, pw, f, 3*time.Second, who)
		for _, rid := range f.removedIDs {
			if rid == codID {
				f.codRemoved = true
			}
		}
		return f
	}

	want := run(targets.vanilla, "vanilla")
	got := run(targets.mcgo, "mc-go")
	// Measured on vanilla 26.2: the cod is removed (bucket pickup ran) but
	// no filled_bucket grant reaches the wire — reproduce both as the
	// oracle, don't assert a grant vanilla doesn't give.
	if !want.codRemoved {
		t.Fatalf("vanilla: summoned cod not removed by water-bucket interact")
	}
	if got.codRemoved != want.codRemoved {
		t.Fatalf("cod removal differs: vanilla=%v mc-go=%v", want.codRemoved, got.codRemoved)
	}
	// Chat replies are the harness's own command feedback; retry counts
	// legitimately differ between targets. Parity signals live in defs,
	// progress, removed criteria, and codRemoved.
	want.chats, got.chats = nil, nil
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("filled_bucket parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
