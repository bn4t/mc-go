package blackbox

import (
	"bytes"
	"fmt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"strings"
	"testing"
	"time"
)

// Retain pairing and dirty packets for the same body throughout the probe.
func TestCreeperIgnitionMetadataParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("CreepMeta")}, nil)
	defer targets.cleanup()
	typ, _ := server.EntityTypeID("creeper")
	for _, leg := range []struct{ name, addr string }{{"native", targets.vanilla}, {"go", targets.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			conn, pr, pw := joinPlayBlackbox(t, leg.addr, "CreepMeta")
			defer conn.Close()
			awaitPlayReadyBlackbox(t, leg.addr, conn, pr, pw)
			sink := newChatSinkResolvedTP(pr, pw)
			if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
				t.Fatal(err)
			}
			p := &scheduledProbeBB{t: t, pw: pw, sink: sink}
			watchConn, watchPR, watchPW := joinPlayBlackbox(t, leg.addr, "CreepWatch")
			defer watchConn.Close()
			awaitPlayReadyBlackbox(t, leg.addr, watchConn, watchPR, watchPW)
			watchSink := newChatSinkResolvedTP(watchPR, watchPW)
			if err := watchPW.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
				t.Fatal(err)
			}

			id := int32(-1)
			paired := false
			ignited := false
			swelling := false
			initialPackets := 0
			consume := func(lines []string, initial bool) {
				for _, line := range lines {
					prefix := fmt.Sprintf("%#x:", server.IDPlaySpawnEntity)
					if strings.HasPrefix(line, prefix) {
						r := protocol.NewPacketReader([]byte(line[len(prefix):]))
						eid, e1 := r.VarInt()
						_, e2 := r.Read(16)
						et, e3 := r.VarInt()
						if e1 == nil && e2 == nil && e3 == nil && et == typ {
							id = eid
						}
					}
					if id < 0 || !strings.HasPrefix(line, "0x63:") {
						continue
					}
					r := protocol.NewPacketReader([]byte(line[5:]))
					eid, err := r.VarInt()
					if err != nil || eid != id {
						continue
					}
					body, _ := r.Read(r.Remaining())
					entries := decodeMetaEntries(t, body)
					t.Logf("body=%d initial=%v metadata=%x", id, initial, body)
					if initial {
						initialPackets++
						if _, ok := entries[15]; ok {
							paired = true
						}
						for _, idx := range []int{16, 17, 18} {
							if _, ok := entries[idx]; ok {
								t.Errorf("default accessor %d redundantly emitted", idx)
							}
						}
					}
					if e, ok := entries[18]; ok {
						if e.typ != 8 || !bytes.Equal(e.val, []byte{1}) {
							t.Errorf("ignited serializer/value: %+v", e)
						}
						ignited = true
					}
					if e, ok := entries[16]; ok {
						if e.typ != 1 || !bytes.Equal(e.val, []byte{1}) {
							t.Errorf("swell serializer/value: %+v", e)
						}
						swelling = true
					}
				}
			}
			consume(p.command(`gamerule minecraft:spawn_mobs false`), true)
			consume(p.command(`summon minecraft:creeper ~ ~ ~2 {Tags:["creepMeta"],NoAI:1b,NoGravity:1b,PersistenceRequired:1b,Fuse:30000s}`), true)
			deadline := time.Now().Add(12 * time.Second)
			for !paired && time.Now().Before(deadline) {
				consume(sink.take(), true)
				time.Sleep(20 * time.Millisecond)
			}
			if !paired {
				t.Fatal("missing positive same-body spawn/NoAI metadata receipt", id, sink.health())
			}
			// Several server passes distinguish first dirty replay from the attach burst.
			for i := 0; i < 4; i++ {
				consume(p.command("say CREEPER_DEFAULT_PASS"), true)
				time.Sleep(75 * time.Millisecond)
			}
			t.Logf("default body=%d packets=%d", id, initialPackets)
			consume(p.command(`item replace entity @s weapon.mainhand with minecraft:flint_and_steel`), true)
			lines := p.command(`execute if entity @s[nbt={SelectedItem:{id:"minecraft:flint_and_steel"}}] run say CREEPER_HELD`)
			consume(lines, true)
			if !structureSaidBB(lines, "CREEPER_HELD") {
				t.Fatal("held flint-and-steel receipt absent")
			}
			var ib protocol.Buffer
			ib.VarInt(id)
			ib.VarInt(0)
			ib.Raw(lpVec3(0, 0, 0))
			ib.Bool(false)
			if err := pw.WritePacket(server.IDPlayInteractSB, ib.Bytes()); err != nil {
				t.Fatal(err)
			}
			applied := false
			deadline = time.Now().Add(12 * time.Second)
			for !(applied && ignited && swelling) && time.Now().Before(deadline) {
				lines = p.command(`execute if entity @e[type=minecraft:creeper,tag=creepMeta,limit=1,nbt={ignited:1b}] run say CREEPER_APPLIED`)
				consume(lines, false)
				applied = applied || structureSaidBB(lines, "CREEPER_APPLIED")
				time.Sleep(20 * time.Millisecond)
			}
			if !applied || !ignited || !swelling {
				t.Fatalf("body=%d applied=%v ignited=%v swelling=%v", id, applied, ignited, swelling)
			}
			sounds := 0
			for _, sound := range aniParseSounds(watchSink.take()) {
				if sound.snd == 642 {
					t.Logf("igniter observer sound: %+v", sound)
					if sound.ent || sound.src != 5 || sound.vol != 1 || sound.pitch < 0.8 || sound.pitch > 1.2 {
						t.Errorf("igniter observer sound shape: %+v", sound)
					}
					sounds++
				}
			}
			if sounds != 1 {
				t.Fatalf("observer flint sound count=%d want1", sounds)
			}

			creeperIgniterCosts(t, p, sink, pw)
			counts := map[int32]int{}
			for _, sound := range aniParseSounds(watchSink.take()) {
				if sound.snd == 642 || sound.snd == 626 {
					if sound.ent || sound.src != 5 || sound.vol != 1 || sound.pitch < 0.8 || sound.pitch > 1.2 {
						t.Errorf("cost observer sound shape: %+v", sound)
					}
					counts[sound.snd]++
				}
			}
			if counts[642] != 2 || counts[626] != 2 {
				t.Errorf("cost observer igniter sound counts=%v", counts)
			}
		})
	}
}

// The interaction consumes the selected hand only. Costs are separate from
// metadata: an authoritative NBT receipt proves that each click was applied.
func creeperIgniterCosts(t *testing.T, p *scheduledProbeBB, sink *chatSink, pw *protocol.Writer) {
	p.command("gamerule minecraft:spawn_mobs false")
	p.command("scoreboard objectives add creepClock minecraft.custom:minecraft.play_time")
	for _, tc := range []struct {
		name, mode, item, slot string
		hand                   int32
		count, want, damage    int
		ignite                 bool
		distance               int
		entity                 string
	}{
		{"survival_flint_main", "survival", "flint_and_steel", "weapon.mainhand", 0, 1, 1, 1, true, 2, "creeper"},
		{"creative_flint_off", "creative", "flint_and_steel", "weapon.offhand", 1, 1, 1, 0, true, 2, "creeper"},
		{"survival_charge_off", "survival", "fire_charge", "weapon.offhand", 1, 3, 2, 0, true, 2, "creeper"},
		{"creative_charge_main", "creative", "fire_charge", "weapon.mainhand", 0, 3, 3, 0, true, 2, "creeper"},
		{"stone_main", "survival", "stone", "weapon.mainhand", 0, 3, 3, 0, false, 2, "creeper"},
		{"far_flint", "survival", "flint_and_steel", "weapon.mainhand", 0, 1, 1, 0, false, 20, "creeper"},
		{"pig_flint", "survival", "flint_and_steel", "weapon.mainhand", 0, 1, 1, 0, false, 2, "pig"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p.t = t
			p.command("kill @e[tag=creepCost]")
			p.command("gamemode " + tc.mode + " @s")
			p.command("item replace entity @s weapon.mainhand with minecraft:stone 7")
			p.command("item replace entity @s weapon.offhand with minecraft:stone 9")
			p.command(fmt.Sprintf("item replace entity @s %s with minecraft:%s %d", tc.slot, tc.item, tc.count))
			marker := "CREEPER_COST_HELD"
			cond := fmt.Sprintf(`execute if items entity @s %s minecraft:%s run say %s`, tc.slot, tc.item, marker)
			if !structureSaidBB(p.command(cond), marker) {
				t.Fatal("selected hand receipt absent")
			}
			typ, _ := server.EntityTypeID(tc.entity)
			id := int32(-1)
			lines := p.command(fmt.Sprintf(`summon minecraft:%s ~ ~ ~%d {Tags:["creepCost"],NoAI:1b,NoGravity:1b,PersistenceRequired:1b,Fuse:30000s}`, tc.entity, tc.distance))
			deadline := time.Now().Add(10 * time.Second)
			for id < 0 && time.Now().Before(deadline) {
				for _, line := range lines {
					prefix := fmt.Sprintf("%#x:", server.IDPlaySpawnEntity)
					if !strings.HasPrefix(line, prefix) {
						continue
					}
					r := protocol.NewPacketReader([]byte(line[len(prefix):]))
					eid, e1 := r.VarInt()
					_, e2 := r.Read(16)
					et, e3 := r.VarInt()
					if e1 == nil && e2 == nil && e3 == nil && et == typ {
						id = eid
					}
				}
				if id < 0 {
					time.Sleep(20 * time.Millisecond)
					lines = sink.take()
				}
			}
			if id < 0 {
				t.Fatal("target spawn receipt absent")
			}
			p.expect("if entity @e[tag=creepCost,type=minecraft:"+tc.entity+"]", "LIVE_TARGET")
			if !tc.ignite {
				p.command("scoreboard players operation #before creepClock = @s creepClock")
			}
			var ib protocol.Buffer
			ib.VarInt(id)
			ib.VarInt(tc.hand)
			ib.Raw(lpVec3(0, 0, 0))
			ib.Bool(false)
			if err := pw.WritePacket(server.IDPlayInteractSB, ib.Bytes()); err != nil {
				t.Fatal(err)
			}
			if tc.ignite {
				p.await(`if entity @e[type=minecraft:creeper,tag=creepCost,limit=1,nbt={ignited:1b}]`, "IGNITED")
			} else {
				_ = pw.WritePacket(server.IDPlayClientTickEndSB, nil)
				p.await("if score @s creepClock > #before creepClock", "NEGATIVE_FRAME")
				p.expect(`unless entity @e[tag=creepCost,limit=1,nbt={ignited:1b}]`, "NOT_IGNITED")
			}
			expected := fmt.Sprintf(`SelectedItem:{id:"minecraft:%s",count:%d}`, tc.item, tc.want)
			if tc.hand == 1 {
				expected = fmt.Sprintf(`equipment:{offhand:{id:"minecraft:%s",count:%d}}`, tc.item, tc.want)
			}
			p.expect("if entity @s[nbt={"+expected+"}]", "COST_COUNT")
			if tc.item == "flint_and_steel" {
				p.expect(fmt.Sprintf(`if items entity @s %s minecraft:flint_and_steel[minecraft:damage~{damage:%d}]`, tc.slot, tc.damage), "COST_DAMAGE")
			}
			other := "weapon.offhand"
			sentinel := 9
			if tc.hand == 1 {
				other = "weapon.mainhand"
				sentinel = 7
			}
			otherNBT := fmt.Sprintf(`equipment:{offhand:{id:"minecraft:stone",count:%d}}`, sentinel)
			if tc.hand == 1 {
				otherNBT = fmt.Sprintf(`SelectedItem:{id:"minecraft:stone",count:%d}`, sentinel)
			}
			p.expect("if entity @s[nbt={"+otherNBT+"}]", "OTHER_HAND_"+strings.ReplaceAll(other, ".", "_"))
		})
	}
}
