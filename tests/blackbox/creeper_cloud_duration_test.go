//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCreeperCloudCopiedDurationParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"view-distance": "2", "simulation-distance": "2"}, map[string]string{"ops.json": opsJSONFor("CloudCopy")}, nil)
	defer tg.cleanup()
	creeper, _ := server.EntityTypeID("creeper")
	cow, _ := server.EntityTypeID("cow")
	cloud, _ := server.EntityTypeID("area_effect_cloud")

	for _, leg := range []struct{ name, addr string }{{"native", tg.vanilla}, {"go", tg.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, leg.addr, "CloudCopy")
			defer c.Close()
			awaitPlayReadyBlackbox(t, leg.addr, c, pr, pw)
			sink := newChatSinkResolvedTP(pr, pw)
			_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
			p := &scheduledProbeBB{t: t, pw: pw, sink: sink}
			p.command("tick freeze")
			p.command("gamerule minecraft:spawn_mobs false")
			p.command("gamerule minecraft:mob_griefing false")
			p.command("tp @s 0.5 100 4.5")
			p.moveReady(0.5, 100, 4.5)
			p.step(1)
			for _, tc := range []struct{ name, duration string }{{"finite", "5"}, {"infinite", "infinite"}} {
				t.Run(tc.name, func(t *testing.T) {
					p.t = t
					p.command("kill @e[type=!minecraft:player]")
					ids := map[string]int32{}
					health := map[int32]bool{}

					sourceGone, cloudGone, explosions := 0, 0, 0
					consume := func(lines []string) {
						for _, line := range lines {
							if body, ok := gravityPacketBody(line, server.IDPlaySpawnEntity); ok {
								r := protocol.NewPacketReader(body)
								id, e1 := r.VarInt()
								_, e2 := r.Read(16)
								typ, e3 := r.VarInt()
								x, e4 := r.Double()
								y, e5 := r.Double()
								z, e6 := r.Double()
								if e1 == nil && e2 == nil && e3 == nil && e4 == nil && e5 == nil && e6 == nil && y == 100 && z == 0.5 {
									switch {
									case typ == creeper && x == 0.5:
										ids["source"] = id
									case typ == cloud && x == 0.5:
										ids["cloud"] = id
									case typ == cow && x == 8.5:
										ids["early"] = id
									case typ == cow && x == 10.5:
										ids["late"] = id
									}
								}
							}
							if body, ok := gravityPacketBody(line, server.IDPlayEntityMetadata); ok {
								r := protocol.NewPacketReader(body)
								id, _ := r.VarInt()
								rest, _ := r.Read(r.Remaining())
								if id != ids["source"] && id != ids["early"] && id != ids["late"] {
									continue
								}
								if _, ok := decodeMetaEntries(t, rest)[9]; ok {
									health[id] = true
								}
							}
							if body, ok := gravityPacketBody(line, server.IDPlayRemoveEntity); ok {
								r := protocol.NewPacketReader(body)
								n, _ := r.VarInt()
								for i := int32(0); i < n; i++ {
									id, err := r.VarInt()
									if err != nil {
										t.Fatal(err)
									}
									if id == ids["source"] {
										sourceGone++
									}
									if id == ids["cloud"] {
										cloudGone++
									}
								}
							}
							if body, ok := gravityPacketBody(line, 0x24); ok {
								r := protocol.NewPacketReader(body)
								x, _ := r.Double()
								y, _ := r.Double()
								z, _ := r.Double()
								power, _ := r.Float()
								if x == 0.5 && y == 100 && z == 0.5 && power == 0 {
									explosions++
								}
							}
						}
					}
					command := func(cmd string) []string {
						for _, tag := range []string{"CloudEarly", "CloudLate", "CloudSource"} {
							cmd = strings.ReplaceAll(cmd, tag, tag+"_"+tc.name)
						}
						lines := p.command(cmd)
						consume(lines)
						return lines
					}
					age := func() int64 {
						for _, line := range command("time query gametime") {
							key, args, ok := componentArgs(line)
							if ok && key == "commands.time.query.gametime" && len(args) > 0 {
								if n, ok := args[0].(int64); ok {
									return n
								}
							}
						}
						t.Fatal("age receipt missing")
						return 0
					}
					step := func(n int) {
						before := age()
						command(fmt.Sprintf("tick step %d", n))
						deadline := time.Now().Add(20 * time.Second)
						for time.Now().Before(deadline) {
							now := age()
							if now == before+int64(n) {
								return
							}
							if now > before+int64(n) {
								t.Fatal("extra simulation ticks", now-before)
							}
							time.Sleep(10 * time.Millisecond)
						}
						t.Fatal("exact step completion absent")
					}

					number := func(tag string) (int64, bool) {
						for _, line := range command("data get entity @e[tag=" + tag + ",limit=1] active_effects[0].duration") {
							key, args, ok := componentArgs(line)
							if ok && strings.HasPrefix(key, "commands.data.entity.") && len(args) > 0 {
								values := dataNumLooseRe.FindAllStringSubmatch(componentPlain(args[len(args)-1]), -1)
								if len(values) > 0 {
									n, err := strconv.ParseInt(values[len(values)-1][1], 10, 64)
									if err == nil {
										return n, true
									}
								}
							}
						}
						return 0, false
					}
					command(`summon minecraft:cow 8.5 100 0.5 {Tags:["CloudEarly"],NoAI:1b,NoGravity:1b,PersistenceRequired:1b}`)
					command(`summon minecraft:cow 10.5 100 0.5 {Tags:["CloudLate"],NoAI:1b,NoGravity:1b,PersistenceRequired:1b}`)
					command(`summon minecraft:creeper 0.5 100 0.5 {Tags:["CloudSource"],NoAI:1b,NoGravity:1b,PersistenceRequired:1b,ignited:1b,Fuse:1s,ExplosionRadius:0b}`)
					deadline := time.Now().Add(10 * time.Second)
					for !(health[ids["source"]] && health[ids["early"]] && health[ids["late"]]) && time.Now().Before(deadline) {
						consume(sink.take())
						time.Sleep(10 * time.Millisecond)
					}
					if !(health[ids["source"]] && health[ids["early"]] && health[ids["late"]]) {
						t.Fatal("source/recipient pairing absent", ids, health)
					}
					command("effect give @e[tag=CloudSource,limit=1] minecraft:speed " + tc.duration)

					sourceDose, ok := number("CloudSource")
					wantSource := int64(100)
					if tc.name == "infinite" {
						wantSource = -1
					}
					if !ok || sourceDose != wantSource {
						t.Fatal("applied source effect duration receipt absent", sourceDose, ok)
					}
					birth := age()
					step(1)
					deadline = time.Now().Add(10 * time.Second)
					for (sourceGone != 1 || explosions != 1 || ids["cloud"] == 0) && time.Now().Before(deadline) {
						command("say CLOUD_BIRTH")
						time.Sleep(10 * time.Millisecond)
					}
					if sourceGone != 1 || explosions != 1 || ids["cloud"] == 0 {
						t.Fatal("source retirement/explosion/cloud pairing absent", ids, sourceGone, explosions)
					}
					command(`tp @e[tag=CloudEarly,limit=1] 0.5 100 0.5`)
					if !structureSaidBB(command(`execute if entity @e[tag=CloudEarly,nbt={Pos:[0.5d,100d,0.5d]},limit=1] run say EARLY_ENTERED`), "EARLY_ENTERED") {
						t.Fatal("early target entry receipt absent")
					}

					early, found := int64(0), false
					for i := 0; i < 15 && !found; i++ {
						step(1)
						early, found = number("CloudEarly")
					}
					if !found {
						t.Fatal("first actual early dose absent")
					}
					earlyAge := age()
					if n, ok := number("CloudLate"); ok {
						t.Fatal("outside late target acquired effect", n)
					}
					command(`tp @e[tag=CloudEarly,limit=1] 8.5 100 0.5`)
					if !structureSaidBB(command(`execute if entity @e[tag=CloudEarly,nbt={Pos:[8.5d,100d,0.5d]},limit=1] run say EARLY_LEFT`), "EARLY_LEFT") {
						t.Fatal("early target exit receipt absent")
					}

					step(int(birth + 151 - age()))
					if n, ok := number("CloudLate"); cloudGone != 0 || ok {
						t.Fatal("cloud survival/outside recipient control failed", cloudGone, n, ok)
					}
					if !structureSaidBB(command(`execute if entity @e[type=minecraft:area_effect_cloud,limit=1] run say LATE_CLOUD_ALIVE`), "LATE_CLOUD_ALIVE") {
						t.Fatal("same cloud absent before delayed admission")
					}
					command(`tp @e[tag=CloudLate,limit=1] 0.5 100 0.5`)
					if !structureSaidBB(command(`execute if entity @e[tag=CloudLate,nbt={Pos:[0.5d,100d,0.5d]},limit=1] run say LATE_ENTERED`), "LATE_ENTERED") {
						t.Fatal("late target entry receipt absent")
					}

					late, found := int64(0), false
					for i := 0; i < 5 && !found; i++ {
						step(1)
						late, found = number("CloudLate")
					}
					if !found {
						t.Fatal("delayed recipient effect absent")
					}
					t.Logf("sourceID%d cloudID%d birthAge%d earlyAge%d lateAge%d sourceDose%d early%d late%d", ids["source"], ids["cloud"], birth, earlyAge, age(), sourceDose, early, late)
					wantDose := int64(25)
					if tc.name == "infinite" {
						wantDose = -1
					}
					if early != wantDose || late != wantDose {
						t.Errorf("copied dose early%d late%d want%d", early, late, wantDose)
					}
					if early != late {
						t.Errorf("copied dose aged with cloud: early%d late%d", early, late)
					}

					command(`tp @e[tag=CloudLate,limit=1] 10.5 100 0.5`)
					step(int(birth + 312 - age()))
					if cloudGone != 1 || !structureSaidBB(command(`execute unless entity @e[type=minecraft:area_effect_cloud] run say CLOUD_RETIRED`), "CLOUD_RETIRED") {
						t.Fatal("copied cloud retirement receipt absent", cloudGone)
					}
					t.Logf("cloudID%d retiredByAge%d", ids["cloud"], age())

				})
			}
		})
	}
}
