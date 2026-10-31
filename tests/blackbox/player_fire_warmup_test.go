package blackbox

import (
	"fmt"
	"mc-go/internal/server"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestPlayerFireWarmupParity(t *testing.T) {
	names := []string{"Warmfire", "Warmsoul"}
	dir := t.TempDir()
	files := map[string]string{"ops.json": opsJSONFor(names...)}
	bodies := map[string]string{
		"pack.mcmeta":                            `{"pack":{"description":"atomic fire/frame observations","min_format":107,"max_format":107}}`,
		"data/warm/function/snapshot.mcfunction": "execute store result score Fire WarmState run data get entity @s Fire\nscoreboard players operation Tick WarmState = @s BBPlayerTick\nexecute store result score Health WarmState run data get entity @s Health 1000\nscoreboard players set Frozen WarmState 0\nexecute store result score Frozen WarmState run data get entity @s TicksFrozen\nsay WARMATOM\n",
	}
	for i, kind := range []string{"fire", "soul_fire"} {
		x := 8
		if i == 1 {
			x = 12
		}
		bodies["data/warm/function/begin_"+kind+".mcfunction"] = fmt.Sprintf("setblock %d -50 2 %s\nfunction warm:snapshot\nsay WARMSTART\n", x, kind)
	}
	for name, body := range bodies {
		rel := "datapacks/warm/" + name
		files["world/"+rel] = body
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	tg := requireBlackboxFiles(t, map[string]string{"view-distance": "2", "simulation-distance": "2"}, files, func(s *server.Server) {
		s.WorldDir = dir
		s.OpsRequired = true
		for _, n := range names {
			s.SetOpLevel(n, 4)
		}
	})
	defer tg.cleanup()
	for _, leg := range []struct{ name, addr string }{{"native", tg.vanilla}, {"go", tg.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			for i, kind := range []string{"fire", "soul_fire"} {
				t.Run(kind, func(t *testing.T) {
					name := names[i]
					c, pr, pw := joinPlayBlackbox(t, leg.addr, name)
					defer c.Close()
					awaitPlayReadyBlackbox(t, leg.addr, c, pr, pw)
					sink := newChatSinkTP(pr, pw)
					collect := func(marker string) []string {
						var lines []string
						until := time.Now().Add(15 * time.Second)
						for !structureSaidBB(lines, marker) && time.Now().Before(until) {
							lines = append(lines, sink.take()...)
							time.Sleep(10 * time.Millisecond)
						}
						if !structureSaidBB(lines, marker) {
							t.Fatal("warmup receipt missing", marker)
						}
						return lines
					}
					seq := 0
					expect := func(condition string) {
						seq++
						m := fmt.Sprintf("WARMEXPECT%d", seq)
						sendCommand(t, pw, condition+" run say "+m)
						sendCommand(t, pw, "say "+m+"END")
						if !structureSaidBB(collect(m+"END"), m) {
							t.Fatal("warmup apparatus/state", condition)
						}
					}
					advance := func(n int) { seq++; waitPlayerTicksBB(t, pw, collect, fmt.Sprintf("WARMFRAME%d", seq), n) }
					tp := func(x float64) {
						sendCommand(t, pw, fmt.Sprintf("tp @s %.3f -50.000 2.500", x))
						expect(fmt.Sprintf("execute if data entity @s {Pos:[%gd,-50d,2.5d]}", x))
					}
					type state struct {
						fire   int
						tick   int64
						hp     float64
						frozen int64
					}
					readSnapshot := func(label, command string) state {
						sendCommand(t, pw, command)
						for _, field := range []string{"Fire", "Tick", "Health", "Frozen"} {
							sendCommand(t, pw, "scoreboard players get "+field+" WarmState")
						}
						seq++
						m := fmt.Sprintf("WARMSNAP%d", seq)
						sendCommand(t, pw, "say "+m)
						lines := collect(m)
						if command != "function warm:snapshot" && !structureSaidBB(lines, "WARMSTART") {
							t.Fatal("atomic source insertion did not run")
						}
						if !structureSaidBB(lines, "WARMATOM") {
							t.Fatal("atomic snapshot function did not run")
						}
						var got state
						ns := 0
						for _, line := range lines {
							key, args, ok := componentArgs(line)
							if !ok || key != "commands.scoreboard.players.get.success" || len(args) < 2 {
								continue
							}
							value := args[1]
							if wrapped, ok := value.(map[string]any); ok {
								value = wrapped[""]
							}
							n, err := strconv.ParseInt(fmt.Sprint(value), 10, 64)
							if err != nil {
								t.Fatal(value)
							}
							switch ns {
							case 0:
								got.fire = int(n)
							case 1:
								got.tick = n
							case 2:
								got.hp = float64(n) / 1000
							case 3:
								got.frozen = n
							}
							ns++
						}
						if ns != 4 || got.tick < 0 || got.hp <= 0 || got.hp > 100 {
							t.Fatalf("incomplete atomic stored snapshot scores%d %+v", ns, got)
						}
						t.Logf("%s %s %s: Fire=%d Tick=%d Health=%g Frozen=%d", leg.name, kind, label, got.fire, got.tick, got.hp, got.frozen)
						return got
					}
					snapshot := func(label string) state { return readSnapshot(label, "function warm:snapshot") }
					begin := func(label string) state { return readSnapshot(label, "function warm:begin_"+kind) }
					setupPlayerTickBarrierBB(t, pw)
					for _, cmd := range []string{"tick freeze", "difficulty normal", "gamerule minecraft:spawn_mobs false", "gamerule minecraft:natural_health_regeneration false", "gamemode survival @s", "forceload add 0 0", "scoreboard objectives add WarmState dummy", "scoreboard objectives add WarmWorldA dummy", "scoreboard objectives add WarmWorldB dummy"} {
						sendCommand(t, pw, cmd)
					}
					until := time.Now().Add(20 * time.Second)
					for {
						sendCommand(t, pw, "execute if loaded 12 -50 2 run say WARMLOADED")
						sendCommand(t, pw, "say WARMLOADEND")
						if structureSaidBB(collect("WARMLOADEND"), "WARMLOADED") {
							break
						}
						if time.Now().After(until) {
							t.Fatal("warm arena missing")
						}
					}
					sendCommand(t, pw, "fill 0 -51 0 14 -51 4 stone")
					sendCommand(t, pw, "fill 4 -50 2 4 -48 2 lava")
					sendCommand(t, pw, "fill 6 -50 2 6 -48 2 water")
					sendCommand(t, pw, "setblock 12 -51 2 soul_soil")
					expect("execute if block 4 -50 2 lava if block 6 -50 2 water if block 12 -51 2 soul_soil")
					x := 8.5
					if kind == "soul_fire" {
						x = 12.5
					}
					cell := fmt.Sprintf("%d -50 2", int(x))
					set := func(block string) {
						sendCommand(t, pw, "setblock "+cell+" "+block)
						expect("execute if block " + cell + " " + block)
					}
					reset := func() {
						set("air")
						tp(6.5)
						advance(6)
						tp(x)
						advance(6)
						sendCommand(t, pw, "attribute @s max_health base set 100")
						sendCommand(t, pw, "effect give @s instant_health 1 10 true")
						advance(12)
						expect(`execute if data entity @s {Health:100.0f,Fire:-20s,playerGameType:0} unless data entity @s {active_effects:[{id:"minecraft:instant_health"}]}`)
					}
					reset()
					sendCommand(t, pw, "execute store result score @s WarmWorldA run time query gametime")
					// Set the source under a stationary body after all teleport frames. The
					// function samples fire and play_time atomically; no client movement occurs.
					set("powder_snow")
					advance(10)
					cold := snapshot("powder-cold")
					if cold.frozen <= 0 || cold.frozen >= 140 || cold.fire != -20 {
						t.Fatal("cold contact precondition", cold)
					}
					start := begin("brief-start")
					if start.fire != -20 {
						t.Fatal("atomic cold start", start)
					}
					advance(1)
					first := snapshot("brief-first")
					advance(3)
					second := snapshot("brief-second")
					if first.fire <= -20 || first.fire >= 0 || second.fire <= first.fire || second.fire >= 0 {
						t.Errorf("negative warmup absent: %+v -> %+v", first, second)
					}
					for _, sample := range []state{first, second} {
						if sample.fire < 0 && int64(sample.fire) != -20+sample.tick-start.tick {
							t.Error("total negative warmup/frame relation", start, sample)
						}
					}
					if first.fire < 0 && second.fire < 0 && int64(second.fire-first.fire) != second.tick-first.tick {
						t.Errorf("negative progress not one per admitted frame: %+v -> %+v", first, second)
					}
					if first.frozen != 0 || second.frozen != 0 {
						t.Error("fire contact did not clear freezing", first, second)
					}
					if first.hp >= cold.hp {
						t.Error("negative-counter direct contact did not hurt", cold, first)
					}
					set("air")
					advance(3)
					dry := snapshot("brief-dry")
					advance(25)
					dryLater := snapshot("brief-dry-later")
					if dry.fire != -20 || dryLater.fire != -20 || dryLater.hp != dry.hp {
						t.Error("brief contact invented continuing burn", dry, dryLater)
					}
					// Continued real contact must eventually ignite from a fresh baseline.
					reset()
					prev := begin("eventual-start")
					if prev.fire != -20 {
						t.Fatal("eventual cold start", prev)
					}
					eventualStart := prev
					ignited := prev.fire > 0
					for n := 0; n < 30 && !ignited; n++ {
						advance(1)
						now := snapshot("eventual-frame")
						if prev.fire < 0 && now.fire < 0 && int64(now.fire-prev.fire) != now.tick-prev.tick {
							t.Error("warmup/frame relation", prev, now)
						}
						if now.fire < 0 && int64(now.fire) != -20+now.tick-eventualStart.tick {
							t.Error("total eventual warmup/frame relation", eventualStart, now)
						}
						if now.fire > 0 && (now.tick-eventualStart.tick < 20 || prev.tick-eventualStart.tick >= 20) {
							t.Error("ignition did not cross twenty-frame threshold", eventualStart, prev, now)
						}
						prev = now
						ignited = now.fire > 0
					}
					if !ignited {
						t.Error("fire contact never ignited")
					}
					// Start well above the minimum burn, then insert fire under a settled body.
					reset()
					tp(4.5)
					advance(6)
					tp(x)
					advance(6)
					hot := begin("positive-start")
					advance(6)
					hotLater := snapshot("positive-later")
					if hot.fire <= 160 || hotLater.tick <= hot.tick {
						t.Fatal("positive burn precondition", hot, hotLater)
					}
					delta, frames := hotLater.fire-hot.fire, hotLater.tick-hot.tick
					if delta < 0 || int64(delta) > frames {
						t.Errorf("positive contact net increment%d over%d frames; want[0,frames]", delta, frames)
					}
					tp(6.5)
					advance(6)
					settled := snapshot("water-settled")
					if settled.fire != -20 {
						t.Error("water settlement regressed", settled)
					}
					sendCommand(t, pw, "execute store result score @s WarmWorldB run time query gametime")
					expect("execute if score @s WarmWorldA = @s WarmWorldB")
				})
			}
		})
	}
}
