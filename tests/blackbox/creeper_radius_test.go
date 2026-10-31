package blackbox

import (
	"fmt"
	"math"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"testing"
	"time"
)

func TestCreeperRadiusParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("RadiusProbe")}, nil)
	defer tg.cleanup()
	typ, _ := server.EntityTypeID("creeper")
	crystalType, _ := server.EntityTypeID("end_crystal")
	for _, leg := range []struct{ name, addr string }{{"native", tg.vanilla}, {"go", tg.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, leg.addr, "RadiusProbe")
			defer c.Close()
			awaitPlayReadyBlackbox(t, leg.addr, c, pr, pw)
			sink := newChatSinkResolvedTP(pr, pw)
			_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
			p := &scheduledProbeBB{t: t, pw: pw, sink: sink}
			p.command("tick freeze")
			p.command("gamerule minecraft:spawn_mobs false")
			p.command("gamerule minecraft:mob_griefing true")
			p.command("tp @s 0.5 100 4.5")
			p.moveReady(0.5, 100, 4.5)
			for i, tc := range []struct {
				name, nbt string
				radius    int8
				powered   bool
			}{
				{"default", "", 3, false}, {"wrong_string", `,ExplosionRadius:"bad"`, 3, false}, {"wrong_list", `,ExplosionRadius:[]`, 3, false},
				{"one", ",ExplosionRadius:1b", 1, false}, {"zero", ",ExplosionRadius:0b", 0, false}, {"negative", ",ExplosionRadius:-1b", -1, false},
				{"minimum", ",ExplosionRadius:-128b", -128, false}, {"wrap_zero", ",ExplosionRadius:256", 0, false}, {"wrap_negative", ",ExplosionRadius:255", -1, false}, {"wrap_one", ",ExplosionRadius:257", 1, false},
				{"powered_zero", ",ExplosionRadius:0b", 0, true}, {"powered_negative", ",ExplosionRadius:-1b", -1, true},
				{"merge_zero", "", 0, false}, {"merge_wrap_negative", "", -1, false}, {"merge_wrong_string", ",ExplosionRadius:1b", 3, false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					p.t = t
					p.command("kill @e[tag=radiusProbe]")
					p.command("fill -4 99 -4 4 103 4 minecraft:air")
					p.command("setblock 1 100 0 minecraft:glass")
					victim := fmt.Sprintf("radiusVictim%d", i)
					tag := fmt.Sprintf("radiusBody%d", i)
					p.command(fmt.Sprintf(`summon minecraft:cow -0.5 100 0.5 {Tags:["radiusProbe","%s"],NoAI:1b,NoGravity:1b,PersistenceRequired:1b,attributes:[{id:"minecraft:max_health",base:1000d}],Health:1000f}`, victim))
					p.expect(fmt.Sprintf(`if entity @e[tag=%s,nbt={Health:1000f},limit=1]`, victim), "VICTIM_HEALTH")
					far := fmt.Sprintf("radiusFar%d", i)
					p.command(fmt.Sprintf(`summon minecraft:cow 30.5 100 0.5 {Tags:["radiusProbe","%s"],NoAI:1b,NoGravity:1b,PersistenceRequired:1b,attributes:[{id:"minecraft:max_health",base:1000d}],Health:1000f}`, far))
					p.expect(fmt.Sprintf(`if entity @e[tag=%s,nbt={Health:1000f,Motion:[0d,0d,0d]},limit=1]`, far), "FAR_VICTIM_BASELINE")

					p.expect("if block 1 100 0 minecraft:glass if block 0 100 0 minecraft:air if block -1 100 0 minecraft:air", "GLASS_AND_CLEAR_ARENA")
					id := int32(-1)
					crystalID := int32(-1)
					crystalRemoved := 0
					crystalTag := fmt.Sprintf("radiusCrystal%d", i)
					paired := false
					removed, explosions := 0, 0
					packetRadius := float32(-9999)
					consume := func(lines []string) {
						for _, line := range lines {
							if body, ok := gravityPacketBody(line, server.IDPlaySpawnEntity); ok {
								r := protocol.NewPacketReader(body)
								eid, e1 := r.VarInt()
								_, e2 := r.Read(16)
								et, e3 := r.VarInt()
								x, e4 := r.Double()
								y, e5 := r.Double()
								z, e6 := r.Double()
								if e1 == nil && e2 == nil && e3 == nil && e4 == nil && e5 == nil && e6 == nil && et == crystalType && x == 30.5 && y == 100 && z == 2.5 {
									crystalID = eid
								}
								if e1 == nil && e2 == nil && e3 == nil && e4 == nil && e5 == nil && e6 == nil && et == typ && x == 0.5 && y == 100 && z == 0.5 {
									id = eid
								}
							}
							if id < 0 {
								continue
							}
							if body, ok := gravityPacketBody(line, server.IDPlayEntityMetadata); ok {
								r := protocol.NewPacketReader(body)
								eid, _ := r.VarInt()
								if eid == id {
									rest, _ := r.Read(r.Remaining())
									if _, ok := decodeMetaEntries(t, rest)[9]; ok {
										paired = true
									}
								}
							}
							if body, ok := gravityPacketBody(line, server.IDPlayRemoveEntity); ok {
								r := protocol.NewPacketReader(body)
								n, _ := r.VarInt()
								for j := int32(0); j < n; j++ {
									eid, err := r.VarInt()
									if err != nil {
										t.Fatal(err)
									}
									if eid == crystalID {
										crystalRemoved++
									}
									if eid == id {
										removed++
									}
								}
							}
							if body, ok := gravityPacketBody(line, 0x24); ok {
								r := protocol.NewPacketReader(body)
								x, e1 := r.Double()
								y, e2 := r.Double()
								z, e3 := r.Double()
								power, e4 := r.Float()
								if e1 == nil && e2 == nil && e3 == nil && e4 == nil && x == 0.5 && y == 100 && z == 0.5 {
									explosions++
									packetRadius = power
								}
							}
						}
					}
					command := func(cmd string) []string { lines := p.command(cmd); consume(lines); return lines }
					clock := func() int64 {
						for _, line := range command("time query gametime") {
							key, args, ok := componentArgs(line)
							if ok && key == "commands.time.query.gametime" && len(args) > 0 {
								if n, ok := args[0].(int64); ok {
									return n
								}
							}
						}
						t.Fatal("game-time receipt absent")
						return 0
					}
					if tc.radius <= 0 {
						command(fmt.Sprintf(`summon minecraft:end_crystal 30.5 100 2.5 {Tags:["radiusProbe","%s"]}`, crystalTag))
						if crystalID < 0 {
							deadline := time.Now().Add(10 * time.Second)
							for crystalID < 0 && time.Now().Before(deadline) {
								consume(sink.take())
								time.Sleep(10 * time.Millisecond)
							}
						}
						if crystalID < 0 {
							t.Fatal("typed far crystal spawn receipt absent")
						}
					}
					powered := "0b"
					if tc.powered {
						powered = "1b"
					}
					command(fmt.Sprintf(`summon minecraft:creeper 0.5 100 0.5 {Tags:["radiusProbe","%s"],NoAI:1b,NoGravity:1b,PersistenceRequired:1b,ignited:1b,Fuse:1s,powered:%s%s}`, tag, powered, tc.nbt))
					deadline := time.Now().Add(10 * time.Second)
					for !paired && time.Now().Before(deadline) {
						consume(sink.take())
						time.Sleep(10 * time.Millisecond)
					}
					if !paired {
						t.Fatal("same-body spawn/health tracking absent", id)
					}

					if tc.name == "merge_zero" {
						command(fmt.Sprintf(`data merge entity @e[tag=%s,limit=1] {ExplosionRadius:0b}`, tag))
					}
					if tc.name == "merge_wrap_negative" {
						command(fmt.Sprintf(`data merge entity @e[tag=%s,limit=1] {ExplosionRadius:255}`, tag))
					}
					if tc.name == "merge_wrong_string" {
						command(fmt.Sprintf(`data merge entity @e[tag=%s,limit=1] {ExplosionRadius:"bad"}`, tag))
					}
					if !structureSaidBB(command(fmt.Sprintf(`execute if entity @e[tag=%s,nbt={ExplosionRadius:%db},limit=1] run say RADIUS_VALUE`, tag, tc.radius)), "RADIUS_VALUE") {
						t.Fatal("typed saved radius receipt absent", tc.radius)
					}
					before := clock()
					command("tick step 1")
					deadline = time.Now().Add(15 * time.Second)
					for time.Now().Before(deadline) {
						now := clock()
						if now == before+1 {
							break
						}
						if now > before+1 {
							t.Fatal("extra simulation ticks", now-before)
						}
						time.Sleep(10 * time.Millisecond)
					}
					if clock() != before+1 {
						t.Fatal("exact one-tick completion absent")
					}
					deadline = time.Now().Add(10 * time.Second)
					for (removed != 1 || explosions != 1) && time.Now().Before(deadline) {
						command("say RADIUS_SETTLED")
						time.Sleep(10 * time.Millisecond)
					}
					if removed != 1 || explosions != 1 {
						t.Fatalf("body%d removal/explosion=%d/%d", id, removed, explosions)
					}
					if structureSaidBB(command(fmt.Sprintf(`execute if entity @e[tag=%s,limit=1] run say RADIUS_ALIVE`, tag)), "RADIUS_ALIVE") {
						t.Fatal("exploded body still alive")
					}
					// All physical checks address a separately witnessed victim and a real
					// adjacent block, rather than trusting only the packet's radius field.
					p.expect(fmt.Sprintf(`if entity @e[tag=%s,limit=1]`, victim), "VICTIM_SURVIVES")
					if tc.radius > 0 {
						p.expect("if block 1 100 0 minecraft:air", "POSITIVE_BREAKS_GLASS")
						p.expect(fmt.Sprintf(`unless entity @e[tag=%s,nbt={Health:1000f},limit=1]`, victim), "POSITIVE_HURTS_VICTIM")
					} else {
						p.expect("if block 1 100 0 minecraft:glass", "NONPOSITIVE_KEEPS_GLASS")
						p.expect(fmt.Sprintf(`if entity @e[tag=%s,nbt={Health:1000f},limit=1]`, victim), "NONPOSITIVE_KEEPS_HEALTH")
					}

					if tc.radius <= 0 {
						nearStill := structureSaidBB(command(fmt.Sprintf(`execute if entity @e[tag=%s,nbt={Motion:[0d,0d,0d]},limit=1] run say RADIUS_NEAR_STILL`, victim)), "RADIUS_NEAR_STILL")
						farStill := structureSaidBB(command(fmt.Sprintf(`execute if entity @e[tag=%s,nbt={Motion:[0d,0d,0d]},limit=1] run say RADIUS_FAR_STILL`, far)), "RADIUS_FAR_STILL")
						t.Logf("nonpositive nearStill=%v farStill=%v", nearStill, farStill)
						if !nearStill || !farStill {
							t.Error("nonpositive radius changed victim motion")
						}
						p.expect(fmt.Sprintf(`if entity @e[tag=%s,nbt={Health:1000f},limit=1]`, far), "FAR_HEALTH_PRESERVED")
						p.expect(fmt.Sprintf(`if entity @e[tag=%s,type=minecraft:end_crystal,limit=1]`, crystalTag), "CRYSTAL_PRESERVED")
						if crystalRemoved != 0 {
							t.Error("nonpositive radius removed far crystal", crystalID, crystalRemoved)
						}

					}
					wantPower := float32(tc.radius)
					if tc.powered {
						wantPower *= 2
					}
					if packetRadius != wantPower {
						t.Errorf("wire radius%v want%v", packetRadius, wantPower)
					}
					if math.IsNaN(float64(packetRadius)) {
						t.Fatal("NaN explosion radius")
					}
					t.Logf("body%d savedRadius%d powered%v packetRadius%v", id, tc.radius, tc.powered, packetRadius)
				})
			}
		})
	}
}
