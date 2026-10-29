package blackbox

import (
	"fmt"
	"math"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"testing"
	"time"
)

func TestEntityNoGravityMetadataParity(t *testing.T) {
	const actor, late = "GravFlagReader", "GravityFlagLate"
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2", "command-spam-threshold-seconds": "0"}, map[string]string{"ops.json": opsJSONFor(actor, late)}, nil)
	defer targets.cleanup()
	type target struct {
		kind    string
		x       int
		gravity bool
		tag     string
	}
	var cases []target
	for i, kind := range []string{"cow", "armor_stand", "item", "oak_boat"} {
		xs := [][2]int{{1, 2}, {4, 5}, {7, 8}, {10, 12}}
		cases = append(cases, target{kind, xs[i][0], true, fmt.Sprintf("GravTrue%d", i)}, target{kind, xs[i][1], false, fmt.Sprintf("GravDefault%d", i)})
	}
	cases = append(cases, target{"vex", 14, true, "GravVex"})
	run := func(addr string) []string {
		ids := map[int]int32{}
		flags := map[int32][]bool{}
		process := func(lines []string) {
			for _, line := range lines {
				if body, ok := gravityPacketBody(line, server.IDPlaySpawnEntity); ok {
					r := protocol.NewPacketReader(body)
					eid, e1 := r.VarInt()
					_, e2 := r.UUID()
					typ, e3 := r.VarInt()
					x, e4 := r.Double()
					y, e5 := r.Double()
					z, e6 := r.Double()
					valid := e1 == nil && e2 == nil && e3 == nil && e4 == nil && e5 == nil && e6 == nil
					sp := fwSpawn{eid: eid, typ: typ, x: x, y: y, z: z}
					name, _ := server.EntityTypeName(typ)
					t.Logf("%s spawn-prefix eid%d type%d %s pos%v,%v,%v valid%t raw%x", addr, eid, typ, name, x, y, z, valid, body[:min(len(body), 48)])
					if valid {
						name, _ := server.EntityTypeName(sp.typ)
						for _, tc := range cases {
							if name == tc.kind && math.Abs(sp.x-float64(tc.x)) < .1 {
								ids[tc.x] = sp.eid
							}
						}
					}
				}
				if body, ok := gravityPacketBody(line, server.IDPlayEntityMetadata); ok {
					r := protocol.NewPacketReader(body)
					eid, err := r.VarInt()
					if err != nil {
						t.Fatal(err)
					}
					known := false
					for _, id := range ids {
						if id == eid {
							known = true
							break
						}
					}
					if !known {
						continue
					}
					for {
						idx, err := r.Byte()
						if err != nil {
							t.Fatal("truncated metadata")
						}
						if idx == 0xff {
							break
						}
						typ, err := r.VarInt()
						if err != nil {
							t.Fatal(err)
						}
						if idx == 5 {
							if typ != 8 {
								t.Fatalf("NoGravity serializer%d", typ)
							}
							v, err := r.Byte()
							if err != nil || v > 1 {
								t.Fatalf("invalid NoGravity bool%d err%v", v, err)
							}
							flags[eid] = append(flags[eid], v != 0)
							continue
						}
						switch typ {
						case 0, 8:
							_, err = r.Byte()
						case 1, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34:
							_, err = r.VarInt()
						case 2:
							_, err = r.VarLong()
						case 3:
							_, err = r.Float()
						case 7:
							_, err = readSlotBB(r)
						case 9:
							_, err = r.Read(12)
						case 17:
							var n int32
							n, err = r.VarInt()
							if n != 0 {
								t.Fatal("unexpected particle payload in gravity fixture")
							}
						default:
							t.Fatalf("unsupported metadata serializer%d at%d (cannot prove flag absence)", typ, idx)
						}
						if err != nil {
							t.Fatal(err)
						}
					}
				}
			}
		}
		c, pr, pw := joinPlayBlackbox(t, addr, actor)
		defer c.Close()
		awaitPlayReadyBlackbox(t, addr, c, pr, pw)
		sink := newChatSinkTP(pr, pw)
		serial := 0
		barrier := func() {
			serial++
			mark := fmt.Sprintf("GRAVITY_BARRIER_%d", serial)
			sendCommand(t, pw, "say "+mark)
			process(waitForExactSayBB(t, sink, mark, 60*time.Second))
		}
		barrier()
		sendCommand(t, pw, "gamemode creative @s")
		sendCommand(t, pw, "tp @s 8 170 6")
		sendCommand(t, pw, "tick freeze")
		barrier()
		clock := func() int64 {
			serial++
			mark := fmt.Sprintf("GRAVITY_CLOCK_%d", serial)
			sendCommand(t, pw, "time query gametime")
			sendCommand(t, pw, "say "+mark)
			lines := waitForExactSayBB(t, sink, mark, 30*time.Second)
			process(lines)
			for _, line := range lines {
				key, args, ok := componentArgs(line)
				if ok && key == "commands.time.query.gametime" && len(args) > 0 {
					if n, ok := args[0].(int64); ok {
						return n
					}
				}
			}
			t.Fatal("missing gravity clock")
			return 0
		}
		step := func(n int64) {
			before := clock()
			deadline := time.Now().Add(30 * time.Second)
			for {
				time.Sleep(150 * time.Millisecond)
				now := clock()
				if now == before {
					break
				}
				before = now
				if time.Now().After(deadline) {
					t.Fatal("gravity clock did not freeze")
				}
			}
			want := before + n
			sendCommand(t, pw, fmt.Sprintf("tick step %d", n))
			for time.Now().Before(deadline) {
				now := clock()
				if now > want {
					t.Fatalf("gravity step overshoot%d want%d", now, want)
				}
				if now == want {
					time.Sleep(150 * time.Millisecond)
					if clock() != want {
						t.Fatal("gravity pending tick")
					}
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatal("gravity step timed out")
		}
		for _, tc := range cases {
			extra := ""
			if tc.gravity {
				extra += ",NoGravity:1b"
			}
			if tc.kind == "cow" || tc.kind == "vex" {
				extra += ",NoAI:1b"
			}
			if tc.kind == "item" {
				extra += `,Item:{id:"minecraft:stone",count:1},PickupDelay:32767s`
			}
			sendCommand(t, pw, fmt.Sprintf(`summon %s %.1f 170.0 0.0 {Tags:["%s"],Motion:[0d,0d,0d]%s}`, tc.kind, float64(tc.x), tc.tag, extra))
		}
		step(2)
		for deadline := time.Now().Add(10 * time.Second); len(ids) < len(cases) && time.Now().Before(deadline); {
			time.Sleep(50 * time.Millisecond)
			barrier()
		}
		var out []string
		require := func(phase string, tc target, values []bool, want *bool) {
			if want == nil {
				if len(values) != 0 {
					t.Errorf("%s %s %s unexpected flag%v", addr, phase, tc.kind, values)
				}
				observed := "none"
				if len(values) > 0 {
					observed = fmt.Sprint(values[len(values)-1])
				}
				out = append(out, phase+":"+tc.kind+"="+observed)
				return
			}
			count := 1
			if phase == "spawn" {
				count = 2
			}
			if len(values) != count {
				t.Errorf("%s %s %s missing flag want%t", addr, phase, tc.kind, *want)
			}
			for _, v := range values {
				if v != *want {
					t.Errorf("%s %s %s flag%v want%t", addr, phase, tc.kind, values, *want)
				}
			}
			observed := "missing"
			if len(values) > 0 {
				observed = fmt.Sprint(values[len(values)-1])
			}
			out = append(out, phase+":"+tc.kind+"="+observed)
		}
		yes := true
		for _, tc := range cases {
			id, ok := ids[tc.x]
			if !ok {
				t.Fatalf("%s missing %s spawn at%d; decoded ids%v sink%s", addr, tc.kind, tc.x, ids, sink.health())
			}
			t.Logf("%s spawn %s x%d flags%v", addr, tc.kind, tc.x, flags[id])
			if tc.gravity {
				require("spawn", tc, flags[id], &yes)
			} else {
				require("default", tc, flags[id], nil)
			}
		}
		for _, phase := range []struct {
			name   string
			on     bool
			repeat bool
		}{{"clear", false, false}, {"repeat_clear", false, true}, {"restore", true, false}, {"repeat_restore", true, true}} {
			starts := map[int32]int{}
			for _, tc := range cases {
				if tc.gravity && tc.kind != "vex" {
					id := ids[tc.x]
					starts[id] = len(flags[id])
					value := 0
					if phase.on {
						value = 1
					}
					sendCommand(t, pw, fmt.Sprintf("data merge entity @e[tag=%s,limit=1] {NoGravity:%db}", tc.tag, value))
				}
			}
			step(2)
			for _, tc := range cases {
				if tc.gravity && tc.kind != "vex" {
					id := ids[tc.x]
					values := flags[id][starts[id]:]
					t.Logf("%s %s %s flags%v", addr, phase.name, tc.kind, values)
					if phase.repeat {
						require(phase.name, tc, values, nil)
					} else {
						require(phase.name, tc, values, &phase.on)
					}
				}
			}
		}
		// Start capture before readiness/chunk drains so pairing packets survive.
		lc, lpr, lpw := joinPlayBlackbox(t, addr, late)
		defer lc.Close()
		lsink := newChatSinkTP(lpr, lpw)
		savedFlags := flags
		savedIDs := ids
		ids = map[int]int32{}
		flags = map[int32][]bool{}
		sendCommand(t, lpw, "say GRAVITY_LATE_READY")
		process(waitForExactSayBB(t, lsink, "GRAVITY_LATE_READY", 60*time.Second))
		for deadline := time.Now().Add(15 * time.Second); len(ids) < len(cases) && time.Now().Before(deadline); {
			time.Sleep(100 * time.Millisecond)
			sendCommand(t, lpw, "say GRAVITY_LATE_POLL")
			process(waitForExactSayBB(t, lsink, "GRAVITY_LATE_POLL", 30*time.Second))
		}
		time.Sleep(200 * time.Millisecond)
		sendCommand(t, lpw, "say GRAVITY_LATE_SETTLED")
		process(waitForExactSayBB(t, lsink, "GRAVITY_LATE_SETTLED", 30*time.Second))
		for _, tc := range cases {
			if tc.gravity {
				id, ok := ids[tc.x]
				if !ok {
					t.Fatal("late missing spawn")
				}
				t.Logf("%s late %s flags%v", addr, tc.kind, flags[id])
				require("late", tc, flags[id], &yes)
			}
		}
		flags = savedFlags
		ids = savedIDs
		sendCommand(t, pw, "tick unfreeze")
		return out
	}
	v, g := run(targets.vanilla), run(targets.mcgo)
	if fmt.Sprint(v) != fmt.Sprint(g) {
		t.Errorf("NoGravity observations differ vanilla%v mcgo%v", v, g)
	}
}

func gravityPacketBody(line string, id int32) ([]byte, bool) {
	prefix := fmt.Sprintf("%#x:", id)
	if len(line) >= len(prefix) && line[:len(prefix)] == prefix {
		return []byte(line[len(prefix):]), true
	}
	return nil, false
}
