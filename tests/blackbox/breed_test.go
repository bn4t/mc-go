package blackbox

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// breedObserve summons two pigs 4 blocks apart around the player,
// feeds each a carrot, and watches the entity spawn stream for a third
// pig (the baby) and an experience orb.
func breedObserve(t *testing.T, tag, addr, name, nbt string, window time.Duration) (baby, orb bool) {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, name)
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(window + 30*time.Second))
	drainChunkWindow(t, tag, c, pr, pw, 400*time.Millisecond)
	sendCommand(t, pw, "tp @s 6 -60 8.5 0 0")
	var mv protocol.Buffer
	mv.Double(6)
	mv.Double(-60)
	mv.Double(8.5)
	mv.Byte(0x01)
	_ = pw.WritePacket(server.IDPlayMovePosSB, mv.Bytes())
	time.Sleep(300 * time.Millisecond)
	sendCommand(t, pw, "summon minecraft:pig 4 -60 8 "+nbt)
	sendCommand(t, pw, "summon minecraft:pig 8 -60 8 "+nbt)
	sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:carrot")
	var hb protocol.Buffer
	hb.Short(0)
	_ = pw.WritePacket(server.IDPlayHeldChangeSB, hb.Bytes())

	pigEIDs := map[int32]bool{}
	end := time.Now().Add(2500 * time.Millisecond)
	for time.Now().Before(end) && len(pigEIDs) < 2 {
		_ = c.SetReadDeadline(time.Now().Add(80 * time.Millisecond))
		p, err := pr.ReadPacket()
		if err != nil {
			continue
		}
		switch int32(p.ID) {
		case server.IDPlayKeepAliveCB:
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
		case server.IDPlayChunkBatchDone:
			_ = ackChunkBatch(pw)
		case 1:
			r := protocol.NewPacketReader(p.Data)
			eid, _ := r.VarInt()
			r.UUID()
			ty, _ := r.VarInt()
			if n, _ := server.EntityTypeName(ty); n == "pig" {
				pigEIDs[eid] = true
			}
		}
	}
	if len(pigEIDs) < 2 {
		t.Fatalf("%s: wanted two pigs, saw %d", tag, len(pigEIDs))
	}

	// Feed both parents — right-click interact, main hand.
	feed := func(eid int32) {
		var ib protocol.Buffer
		ib.VarInt(eid)
		ib.VarInt(0) // interact
		ib.VarInt(0) // main hand
		ib.Bool(false)
		_ = pw.WritePacket(server.IDPlayInteractSB, ib.Bytes())
	}
	// Higher eid second — order doesn't matter.
	var eids []int32
	for eid := range pigEIDs {
		eids = append(eids, eid)
	}
	feed(eids[0])
	time.Sleep(150 * time.Millisecond)
	feed(eids[1])

	start := time.Now()
	for time.Since(start) < window {
		_ = c.SetReadDeadline(time.Now().Add(80 * time.Millisecond))
		p, err := pr.ReadPacket()
		if err != nil {
			continue
		}
		switch int32(p.ID) {
		case server.IDPlayKeepAliveCB:
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
		case server.IDPlayChunkBatchDone:
			_ = ackChunkBatch(pw)
		case 1:
			r := protocol.NewPacketReader(p.Data)
			r.VarInt()
			r.UUID()
			ty, _ := r.VarInt()
			n, _ := server.EntityTypeName(ty)
			if n == "pig" {
				baby = true
			}
			if n == "experience_orb" {
				orb = true
			}
		}
	}
	sendCommand(t, pw, "kill @e[type=minecraft:pig]")
	time.Sleep(1300 * time.Millisecond)
	return baby, orb
}

// TestMobBreedParity pins BreedGoal: two fed pigs in love walk to each
// other and, after ~60 ticks of approach, a baby spawns with the 1-7
// xp award — while NoAI pigs in love never produce one.
func TestMobBreedParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BREED"),
	}, nil)
	defer targets.cleanup()

	t.Run("breed_baby", func(t *testing.T) {
		for _, tc := range []struct{ tag, addr string }{
			{"vanilla", targets.vanilla},
			{"mc-go", targets.mcgo},
		} {
			baby, orb := breedObserve(t, tc.tag, tc.addr, "BREED", "{}", 9*time.Second)
			t.Logf("%s: baby=%v orb=%v", tc.tag, baby, orb)
			if !baby {
				t.Fatalf("%s: no baby pig spawned — BreedGoal should mate the fed pair", tc.tag)
			}
			if !orb {
				t.Fatalf("%s: no xp orb spawned — spawnChildFromBreeding awards 1-7 xp", tc.tag)
			}
		}
	})

	t.Run("noai_no_breed", func(t *testing.T) {
		for _, tc := range []struct{ tag, addr string }{
			{"vanilla", targets.vanilla},
			{"mc-go", targets.mcgo},
		} {
			baby, _ := breedObserve(t, tc.tag, tc.addr, "BREED", "{NoAI:1b}", 7*time.Second)
			t.Logf("%s: baby=%v", tc.tag, baby)
			if baby {
				t.Fatalf("%s: NoAI pair produced a baby — the goal selector must be off", tc.tag)
			}
		}
	})
}
