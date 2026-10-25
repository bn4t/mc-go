package blackbox

// TestShearMobParity right-clicks shearable mobs with held shears:
// Entity.interact → ShearsItem.interactLivingEntity → Shearable.shear
// — sheep drops wool + Sheared flag, snow golem pops its pumpkin,
// bogged sheds mushrooms + sheared flag, mooshroom converts to a cow
// + 5 red mushrooms. Probes pin the NBT flips and item drops on both
// servers.
//
// Entity type ids come from entitytypes_gen (sheep 111, snow_golem
// 121, bogged 16, mooshroom 86, cow 30, item 71).

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// shearSnd is one parsed clientbound sound_entity packet (0x74):
// (sound event wire id = registry id + 1, SoundSource id, entity id).
// Ambient mob sounds also ride 0x74 — the shear emits are pinned by
// the full tuple, not the packet's mere presence.
type shearSnd struct{ snd, src, eid int32 }

func parseEntSounds(lines []string) []shearSnd {
	var out []shearSnd
	for _, l := range lines {
		i := strings.IndexByte(l, ':')
		if i < 0 || l[:i] != "0x74" {
			continue
		}
		r := protocol.NewPacketReader([]byte(l[i+1:]))
		snd, e1 := r.VarInt()
		src, e2 := r.VarInt()
		eid, e3 := r.VarInt()
		if e1 != nil || e2 != nil || e3 != nil {
			continue
		}
		out = append(out, shearSnd{snd, src, eid})
	}
	return out
}

// countItemSpawns decodes 0x01 spawn_entity lines and counts item
// entities (type 71) that spawned within r of (cx, cz). The packet
// body is: VarInt id, 16B uuid, VarInt type, double x, double y,
// double z — the tail is unparsed.
func countItemSpawns(lines []string, cx, cz, r float64) int {
	n := 0
	for _, l := range lines {
		i := strings.IndexByte(l, ':')
		if i < 0 || l[:i] != "0x1" {
			continue
		}
		pr := protocol.NewPacketReader([]byte(l[i+1:]))
		if _, e := pr.VarInt(); e != nil {
			continue
		}
		if _, e := pr.Read(16); e != nil {
			continue
		}
		typ, e := pr.VarInt()
		if e != nil || typ != 71 {
			continue
		}
		x, e1 := pr.Double()
		_, e2 := pr.Double()
		z, e3 := pr.Double()
		if e1 != nil || e2 != nil || e3 != nil {
			continue
		}
		if x > cx-r && x < cx+r && z > cz-r && z < cz+r {
			n++
		}
	}
	return n
}

func TestShearMobParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Shear"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
	})
	defer targets.cleanup()

	probes := []struct{ name, spec string }{
		{"sheep sheared nbt", `entity @e[type=minecraft:sheep,nbt={Sheared:1b},x=30,y=-60,z=30,distance=..3]`},
		{"sheep wool dropped", `entity @e[type=minecraft:item,x=30,y=-60,z=30,distance=..4]`},
		{"golem pumpkinless nbt", `entity @e[type=minecraft:snow_golem,nbt={Pumpkin:0b},x=36,y=-60,z=30,distance=..3]`},
		{"golem pumpkin dropped", `entity @e[type=minecraft:item,x=36,y=-60,z=30,distance=..4]`},
		{"bogged sheared nbt", `entity @e[type=minecraft:bogged,nbt={sheared:1b},x=42,y=-60,z=30,distance=..3]`},
		{"bogged shrooms dropped", `entity @e[type=minecraft:item,x=42,y=-60,z=30,distance=..4]`},
		{"mooshroom converted", `entity @e[type=minecraft:cow,x=48,y=-60,z=30,distance=..3]`},
		{"mooshroom gone", `entity @e[type=minecraft:mooshroom,x=48,y=-60,z=30,distance=..6]`},
		{"mooshroom shrooms", `entity @e[type=minecraft:item,x=48,y=-60,z=30,distance=..4]`},
	}

	type mobLeg struct {
		hits  []string
		snds  []shearSnd
		eids  map[int32]int32 // entity type → entity id
		items int
	}
	run := func(addr string) mobLeg {
		c, pr, pw := joinPlayBlackbox(t, addr, "Shear")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		// Vanilla only starts entity tracking after player_loaded.
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		// Flush the join-time chunk window with a fixed bound (a
		// quiet-timer drain never terminates: vanilla's ~1s
		// time_update heartbeat keeps the stream non-quiet).
		flushEnd := time.Now().Add(1500 * time.Millisecond)
		for time.Now().Before(flushEnd) {
			_ = c.SetReadDeadline(time.Now().Add(time.Until(flushEnd)))
			p, err := pr.ReadPacket()
			if err != nil {
				break
			}
			switch p.ID {
			case server.IDPlayKeepAliveCB:
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
			case server.IDPlayChunkBatchDone:
				_ = ackChunkBatch(pw)
			case server.IDPlaySyncPos:
				if id, err := protocol.NewPacketReader(p.Data).VarInt(); err == nil {
					var tb protocol.Buffer
					tb.VarInt(id)
					_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, tb.Bytes())
				}
			}
		}
		sendCommand(t, pw, "tp @s 39 -60 24 0 0")
		sendCommand(t, pw, "fill 27 -61 27 51 -59 33 minecraft:stone")
		sendCommand(t, pw, "fill 28 -60 28 50 -59 32 minecraft:air")
		time.Sleep(1500 * time.Millisecond)
		sendCommand(t, pw, `summon minecraft:sheep 30.5 -60 30.5 {NoAI:1b}`)
		sendCommand(t, pw, `summon minecraft:snow_golem 36.5 -60 30.5 {NoAI:1b}`)
		sendCommand(t, pw, `summon minecraft:bogged 42.5 -60 30.5 {NoAI:1b}`)
		sendCommand(t, pw, `summon minecraft:mooshroom 48.5 -60 30.5 {NoAI:1b}`)
		sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:shears")
		ids := map[int32]int32{}
		end := time.Now().Add(10 * time.Second)
		for time.Now().Before(end) && len(ids) < 4 {
			_ = c.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
			p, err := pr.ReadPacket()
			if err != nil {
				continue
			}
			switch p.ID {
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
			case server.IDPlaySpawnEntity:
				r := protocol.NewPacketReader(p.Data)
				id, e1 := r.VarInt()
				_, e2 := r.Read(16)
				typ, e3 := r.VarInt()
				if e1 != nil || e2 != nil || e3 != nil {
					continue
				}
				t.Logf("spawn id=%d typ=%d", id, typ)
				switch typ {
				case 111, 121, 16, 86:
					ids[typ] = id
				}
			default:
				if p.ID == 0x1d || p.ID == 0x1c {
					t.Logf("disconnect pkt %x: %q", p.ID, string(p.Data)[:min(len(p.Data), 200)])
				}
			}
		}
		// Clear the read deadline the capture loop armed — the sink's
		// reader would otherwise see only i/o timeouts.
		_ = c.SetReadDeadline(time.Time{})
		dbg := newChatSinkTP(pr, pw)
		time.Sleep(200 * time.Millisecond)
		dbg.take()
		for i, spec := range []string{
			`entity @e[type=minecraft:sheep,x=30,y=-60,z=30,distance=..3]`,
			`entity @e[type=minecraft:snow_golem,x=36,y=-60,z=30,distance=..3]`,
			`entity @e[type=minecraft:bogged,x=42,y=-60,z=30,distance=..3]`,
			`entity @e[type=minecraft:mooshroom,x=48,y=-60,z=30,distance=..3]`,
		} {
			sendCommand(t, pw, fmt.Sprintf("execute if %s run say EX-%d", spec, i))
		}
		sendCommand(t, pw, "say EX-end")
		time.Sleep(2 * time.Second)
		for _, l := range dbg.take() {
			if strings.Contains(l, "EX-") {
				t.Logf("exist: %.80q", l)
			}
		}
		if len(ids) != 4 {
			t.Fatalf("missing spawn ids: got %v", ids)
		}
		// Interact each mob with the held shears.
		// Vanilla rejects entity interactions past its ~3-block reach —
		// tp beside each mob before clicking it (a real client always
		// clicks in range).
		posByTyp := map[int32][2]float64{111: {30.5, 29.0}, 121: {36.5, 29.0}, 16: {42.5, 29.0}, 86: {48.5, 29.0}}
		for _, typ := range []int32{111, 121, 16, 86} {
			px := posByTyp[typ]
			sendCommand(t, pw, fmt.Sprintf("tp @s %.1f -60 %.1f", px[0]-1, px[1]))
			time.Sleep(350 * time.Millisecond)
			var ib protocol.Buffer
			ib.VarInt(ids[typ])
			ib.VarInt(0) // hand: main
			ib.Raw(lpVec3(0.5, 0.9, 0.5))
			ib.Bool(false)
			if err := pw.WritePacket(server.IDPlayInteractSB, ib.Bytes()); err != nil {
				t.Logf("interact typ=%d: %v", typ, err)
			}
			time.Sleep(250 * time.Millisecond)
		}
		time.Sleep(800 * time.Millisecond)
		sink := dbg
		lines := sink.take()
		snds := parseEntSounds(lines)
		items := countItemSpawns(lines, 42.5, 30.5, 4.0)
		t.Logf("sinkerr: %v ids:%v", sink.err, sink.ids)
		out := make([]string, 0, len(probes))
		leg := mobLeg{snds: snds, eids: ids, items: items}
		for i, p := range probes {
			sendCommand(t, pw, fmt.Sprintf("execute if %s run say SH-ok-%d", p.spec, i))
			sendCommand(t, pw, fmt.Sprintf("execute unless %s run say SH-miss-%d", p.spec, i))
			sendCommand(t, pw, fmt.Sprintf("say SHK%d", i))
			marker := fmt.Sprintf("SHK%d", i)
			hit := "none"
			deadline := time.Now().Add(4 * time.Second)
			for time.Now().Before(deadline) {
				for _, line := range sink.take() {
					if strings.Contains(line, marker) {
						goto done
					}
					if strings.Contains(line, fmt.Sprintf("SH-ok-%d", i)) {
						hit = "hit"
					}
					if strings.Contains(line, fmt.Sprintf("SH-miss-%d", i)) {
						hit = "miss"
					}
				}
			}
		done:
			out = append(out, hit)
		}
		leg.hits = out
		return leg
	}

	vanilla := run(targets.vanilla)
	mcgo := run(targets.mcgo)
	for i, p := range probes {
		want := "hit"
		if p.name == "mooshroom gone" {
			want = "miss"
		}
		t.Logf("%-24s vanilla=%s mcgo=%s", p.name, vanilla.hits[i], mcgo.hits[i])
		if vanilla.hits[i] != want {
			t.Errorf("probe %q: vanilla %s, want %s", p.name, vanilla.hits[i], want)
		}
		if mcgo.hits[i] != want {
			t.Errorf("probe %q: mc-go %s, want %s", p.name, mcgo.hits[i], want)
		}
	}

	// Each Shearable leads shear() with playSound(null, this, <its
	// shear sound>, PLAYERS, 1, 1) — an entity-anchored 0x74 carrying
	// source=7 (PLAYERS) and the mob's entity id. Registry id + 1 on
	// the wire: sheep.shear=1441, snow_golem.shear=1574, bogged.shear=184,
	// mooshroom.shear=989.
	wantSnd := map[int32]int32{1442: 111, 1575: 121, 185: 16, 990: 86}
	for _, lg := range []struct {
		tag string
		leg mobLeg
	}{{"vanilla", vanilla}, {"mc-go", mcgo}} {
		for wantS, wantTyp := range wantSnd {
			found := false
			for _, s := range lg.leg.snds {
				if s.snd == wantS && s.src == 7 && s.eid == lg.leg.eids[wantTyp] {
					found = true
				}
			}
			if !found {
				t.Errorf("%s: shear sound %d (src=7, eid=%d) missing: snds=%v",
					lg.tag, wantS, lg.leg.eids[wantTyp], lg.leg.snds)
			}
		}
		// shearing/bogged is rolls=2 over a 2-entry {brown,red} pool:
		// exactly 2 mushroom item spawns near the bogged (the earlier
		// roll-per-entry bug dropped 4).
		if lg.leg.items != 2 {
			t.Errorf("%s: bogged mushroom spawns = %d, want 2", lg.tag, lg.leg.items)
		}
	}
}
