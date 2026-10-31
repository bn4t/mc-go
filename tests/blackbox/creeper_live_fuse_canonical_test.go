package blackbox

import (
	"fmt"
	"math"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"testing"
	"time"
)

// Keep all packet evidence through exact game-time receipts: querying the
// clock must not drain away the removal or explosion being measured.
func TestCreeperLiveFuseCanonicalParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("FuseCanonical")}, nil)
	defer tg.cleanup()
	typ, _ := server.EntityTypeID("creeper")
	for _, leg := range []struct{ name, addr string }{{"native", tg.vanilla}, {"go", tg.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, leg.addr, "FuseCanonical")
			defer c.Close()
			awaitPlayReadyBlackbox(t, leg.addr, c, pr, pw)
			sink := newChatSinkResolvedTP(pr, pw)
			_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
			p := &scheduledProbeBB{t: t, pw: pw, sink: sink}
			p.command("tick freeze")
			p.command("gamerule minecraft:spawn_mobs false")
			p.command("gamerule minecraft:mob_griefing false")
			for _, tc := range []struct {
				name  string
				ticks int
			}{
				{"live_string", 30}, {"live_list", 30}, {"live_wrap", 3}, {"live_signed_wrap", 3}, {"live_remove", 30}, {"live_short", 3},
			} {
				t.Run(tc.name, func(t *testing.T) {
					p.t = t
					p.command("kill @e[tag=fuseCanonical]")
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
					command(`summon minecraft:creeper ~ ~ ~2 {Tags:["fuseCanonical"],NoGravity:1b,PersistenceRequired:1b,NoAI:1b,ignited:1b,Fuse:5s}`)
					deadline := time.Now().Add(10 * time.Second)
					for !paired && time.Now().Before(deadline) {
						consume(sink.take())
						time.Sleep(10 * time.Millisecond)
					}
					if !paired {
						t.Fatal("same-body spawn/health tracking receipt absent", id)
					}
					if !structureSaidBB(command(`execute if entity @e[tag=fuseCanonical,nbt={Fuse:5s},limit=1] run say FUSE_VALUE`), "FUSE_VALUE") {
						t.Fatal("initial short5 receipt absent")
					}

					if removed != 0 || exploded != 0 {
						t.Fatal("frozen setup advanced fuse", removed, exploded)
					}
					alive := func() bool {
						return structureSaidBB(command(`execute if entity @e[tag=fuseCanonical,limit=1] run say FUSE_ALIVE`), "FUSE_ALIVE")
					}
					if !alive() {
						t.Fatal("body absent before first tick")
					}
					step(2)
					if !alive() || removed != 0 || exploded != 0 {
						t.Fatal("body expired before live edit")
					}
					edit, want := `data merge entity @e[tag=fuseCanonical,limit=1] {Fuse:3s}`, "3s"
					switch tc.name {
					case "live_string":
						edit, want = `data merge entity @e[tag=fuseCanonical,limit=1] {Fuse:"bad"}`, "30s"
					case "live_list":
						edit, want = `data merge entity @e[tag=fuseCanonical,limit=1] {Fuse:[]}`, "30s"
					case "live_wrap":
						edit = `data merge entity @e[tag=fuseCanonical,limit=1] {Fuse:65539}`
					case "live_signed_wrap":
						edit, want = `data merge entity @e[tag=fuseCanonical,limit=1] {Fuse:65535}`, "-1s"
					case "live_remove":
						edit, want = `data remove entity @e[tag=fuseCanonical,limit=1] Fuse`, "30s"
					}
					editAge := clock()
					command(edit)
					if !structureSaidBB(command(`execute if entity @e[tag=fuseCanonical,nbt={Fuse:`+want+`},limit=1] run say LIVE_FUSE_CANONICAL`), "LIVE_FUSE_CANONICAL") {
						t.Error("live edit canonical saved Fuse receipt absent", want)
					}
					if clock() != editAge || !alive() || removed != 0 || exploded != 0 {
						t.Fatal("live edit changed frozen age or retired body before next tick")
					}
					if tc.ticks > 3 {
						step(tc.ticks - 3)
						if !alive() || removed != 0 || exploded != 0 {
							t.Fatal("live edit expired before total deadline")
						}
					}

					step(1)
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
