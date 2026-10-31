package blackbox

import (
	"fmt"
	"math"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"strings"
	"testing"
	"time"
)

// Keep all packet evidence through exact game-time receipts: querying the
// clock must not drain away the removal or explosion being measured.
func TestCreeperFuseTicksParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("FuseTicks")}, nil)
	defer tg.cleanup()
	typ, _ := server.EntityTypeID("creeper")
	for _, leg := range []struct{ name, addr string }{{"native", tg.vanilla}, {"go", tg.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, leg.addr, "FuseTicks")
			defer c.Close()
			awaitPlayReadyBlackbox(t, leg.addr, c, pr, pw)
			sink := newChatSinkResolvedTP(pr, pw)
			_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
			p := &scheduledProbeBB{t: t, pw: pw, sink: sink}
			p.command("tick freeze")
			p.command("gamerule minecraft:spawn_mobs false")
			p.command("gamerule minecraft:mob_griefing false")
			for _, tc := range []struct {
				name, fuse       string
				noAI, lit, click bool
				ticks            int
			}{
				{"default30_noai", "", true, true, false, 30},
				{"invalid_string", `,Fuse:"bad"`, true, true, false, 30},
				{"invalid_list", `,Fuse:[]`, true, true, false, 30},
				{"int_wrap_zero", `,Fuse:65536`, true, true, false, 1},
				{"one_noai", ",Fuse:1s", true, true, false, 1},
				{"zero_noai", ",Fuse:0s", true, true, false, 1},
				{"negative_noai", ",Fuse:-1s", true, true, false, 1},
				{"cold_zero", ",Fuse:0s", true, false, false, 1},
				{"cold_negative", ",Fuse:-1s", true, false, false, 1},
				{"three_ai", ",Fuse:3s", false, true, false, 3},
				{"three_noai", ",Fuse:3s", true, true, false, 3},
				{"click_three", ",Fuse:3s", true, false, true, 3},
				{"repeat_click_three", ",Fuse:3s", true, false, true, 3},
				{"shrink_live_fuse", ",Fuse:5s", true, true, false, 3},
				{"cold_three", ",Fuse:3s", true, false, false, 5},
			} {
				t.Run(tc.name, func(t *testing.T) {
					p.t = t
					p.command("kill @e[tag=fuseTicks]")
					id := int32(-1)
					paired := false
					removed, exploded := 0, 0
					var pos [3]float64
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
								if e1 == nil && e2 == nil && e3 == nil && e4 == nil && e5 == nil && e6 == nil && et == typ {
									id = eid
									pos = [3]float64{x, y, z}
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
								for i := int32(0); i < n; i++ {
									eid, err := r.VarInt()
									if err != nil {
										t.Fatal(err)
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
								if e1 == nil && e2 == nil && e3 == nil && math.Abs(x-pos[0]) < 1e-6 && math.Abs(y-pos[1]) < 1e-6 && math.Abs(z-pos[2]) < 1e-6 {
									exploded++
								}
							}
						}
					}
					command := func(cmd string) []string { lines := p.command(cmd); consume(lines); return lines }
					clock := func() int64 {
						lines := command("time query gametime")
						for _, line := range lines {
							key, args, ok := componentArgs(line)
							if ok && key == "commands.time.query.gametime" && len(args) > 0 {
								if n, ok := args[0].(int64); ok {
									return n
								}
							}
						}
						t.Fatal("game time receipt absent")
						return 0
					}
					step := func(n int) {
						before := clock()
						command(fmt.Sprintf("tick step %d", n))
						deadline := time.Now().Add(15 * time.Second)
						for time.Now().Before(deadline) {
							now := clock()
							if now == before+int64(n) {
								return
							}
							if now > before+int64(n) {
								t.Fatalf("requested%d ticks got%d", n, now-before)
							}
							time.Sleep(10 * time.Millisecond)
						}
						t.Fatal("exact tick completion absent")
					}
					boolNBT := func(v bool) string {
						if v {
							return "1b"
						}
						return "0b"
					}
					command(fmt.Sprintf(`summon minecraft:creeper ~ ~ ~2 {Tags:["fuseTicks"],NoGravity:1b,PersistenceRequired:1b,NoAI:%s,ignited:%s%s}`, boolNBT(tc.noAI), boolNBT(tc.lit), tc.fuse))
					deadline := time.Now().Add(10 * time.Second)
					for !paired && time.Now().Before(deadline) {
						consume(sink.take())
						time.Sleep(10 * time.Millisecond)
					}
					if !paired {
						t.Fatal("same-body spawn/health tracking receipt absent", id)
					}
					fuseValue := strings.TrimPrefix(tc.fuse, ",Fuse:")
					if tc.name == "invalid_string" || tc.name == "invalid_list" {
						fuseValue = "30s"
					}
					if tc.name == "int_wrap_zero" {
						fuseValue = "0s"
					}
					if fuseValue == "" {
						fuseValue = "30s"
					}
					if !structureSaidBB(command(`execute if entity @e[tag=fuseTicks,nbt={Fuse:`+fuseValue+`},limit=1] run say FUSE_VALUE`), "FUSE_VALUE") {
						t.Fatal("saved fuse value receipt absent", fuseValue)
					}

					if tc.click {
						command("item replace entity @s weapon.mainhand with minecraft:flint_and_steel")
						if !structureSaidBB(command(`execute if items entity @s weapon.mainhand minecraft:flint_and_steel run say FUSE_HELD`), "FUSE_HELD") {
							t.Fatal("held igniter receipt absent")
						}
						var b protocol.Buffer
						b.VarInt(id)
						b.VarInt(0)
						b.Raw(lpVec3(0, 0, 0))
						b.Bool(false)
						_ = pw.WritePacket(server.IDPlayInteractSB, b.Bytes())
						applied := false
						deadline = time.Now().Add(10 * time.Second)
						for !applied && time.Now().Before(deadline) {
							applied = structureSaidBB(command(`execute if entity @e[tag=fuseTicks,nbt={ignited:1b},limit=1] run say FUSE_APPLIED`), "FUSE_APPLIED")
						}
						if !applied {
							t.Fatal("applied ignition receipt absent")
						}
					}
					if removed != 0 || exploded != 0 {
						t.Fatal("frozen setup advanced fuse", removed, exploded)
					}
					alive := func() bool {
						return structureSaidBB(command(`execute if entity @e[tag=fuseTicks,limit=1] run say FUSE_ALIVE`), "FUSE_ALIVE")
					}
					if !alive() {
						t.Fatal("body absent before first tick")
					}
					if tc.ticks > 1 {
						step(tc.ticks - 1)
						if !alive() || removed != 0 || exploded != 0 {
							t.Fatalf("early explosion at tick%d body%d alive/removal/explosion=%v/%d/%d", tc.ticks-1, id, alive(), removed, exploded)
						}
					}
					if tc.name == "shrink_live_fuse" {
						command(`data merge entity @e[tag=fuseTicks,limit=1] {Fuse:3s}`)
						if !structureSaidBB(command(`execute if entity @e[tag=fuseTicks,nbt={Fuse:3s},limit=1] run say FUSE_RESIZED`), "FUSE_RESIZED") {
							t.Fatal("live Fuse resize receipt absent")
						}
					}
					if tc.name == "repeat_click_three" {
						command("gamemode survival @s")
						command("item replace entity @s weapon.mainhand with minecraft:fire_charge 2")
						if !structureSaidBB(command(`execute if entity @s[nbt={SelectedItem:{id:"minecraft:fire_charge",count:2}}] run say FUSE_REHELD`), "FUSE_REHELD") {
							t.Fatal("repeat igniter held receipt absent")
						}
						var b protocol.Buffer
						b.VarInt(id)
						b.VarInt(0)
						b.Raw(lpVec3(0, 0, 0))
						b.Bool(false)
						_ = pw.WritePacket(server.IDPlayInteractSB, b.Bytes())
						applied := false
						deadline = time.Now().Add(10 * time.Second)
						for !applied && time.Now().Before(deadline) {
							applied = structureSaidBB(command(`execute if entity @s[nbt={SelectedItem:{id:"minecraft:fire_charge",count:1}}] run say FUSE_REAPPLIED`), "FUSE_REAPPLIED")
						}
						if !applied {
							t.Fatal("repeat ignition cost receipt absent")
						}
						command("gamemode creative @s")
					}

					step(1)
					if tc.name == "cold_three" {
						if !alive() || removed != 0 || exploded != 0 {
							t.Fatal("unignited control exploded")
						}
						return
					}
					deadline = time.Now().Add(10 * time.Second)
					for (removed != 1 || exploded != 1) && time.Now().Before(deadline) {
						command("say FUSE_SETTLED")
						time.Sleep(10 * time.Millisecond)
					}
					if alive() || removed != 1 || exploded != 1 {
						t.Fatalf("tick%d body%d alive/removal/explosion=%v/%d/%d", tc.ticks, id, alive(), removed, exploded)
					}
					t.Logf("body%d exploded/removed exactly at simulation tick%d", id, tc.ticks)
				})
			}
		})
	}
}
