//go:build blackbox

package blackbox

import (
	"bytes"
	"fmt"
	"mc-go/internal/server"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestChestRecheckAndValidityParity(t *testing.T) {
	names := []string{"ValidActorV", "ValidActorM", "ValidWatchV", "ValidWatchM"}
	for _, i := range []int{0, 1} {
		for c := 'a'; c <= 'p'; c++ {
			candidate := names[i] + string(c)
			off := 0
			for _, b := range []byte(candidate) {
				off = (31*off + int(b)) & 15
			}
			if off == 0 {
				names[i] = candidate
				break
			}
		}
	}
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor(names...)}, nil)
	defer tg.cleanup()
	run := func(addr, name, watchName string) []string {
		d := newClickDriverOn(t, addr, name, "minecraft:trapped_chest", 2, [][2]string{{"hotbar.0", "minecraft:diamond 3"}}, nil)
		d.close()
		command := func(view *clickDriver, text string) {
			sendCommand(t, view.pw, text)
			settleUse(t, view.sink, func(ps []usePkt) { view.foldPkts(ps) }, "chest-recheck-setup")
		}
		command(d, "setblock 9 -61 0 minecraft:trapped_chest[facing=north,type=right]")
		command(d, "setblock 7 -61 0 minecraft:redstone_wire")
		command(d, "setblock 10 -61 0 minecraft:redstone_wire")
		command(d, "tick freeze")
		other := newMenuLifecycleObserver(t, addr, watchName)
		command(other, "tp @s 6 -60 2 0 0")
		clock := func() int64 {
			sendCommand(t, other.pw, "time query gametime")
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				ps := other.sink.take()
				other.foldPkts(ps)
				for _, p := range ps {
					key, args, ok := componentArgs(fmt.Sprintf("%#x:%s", p.id, string(p.data)))
					if ok && key == "commands.time.query.gametime" && len(args) > 0 {
						if value, ok := args[0].(int64); ok {
							return value
						}
					}
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatal("recheck game time query missing")
			return 0
		}
		advance := func(n int) {
			before := clock()
			sendCommand(t, other.pw, fmt.Sprintf("tick step %d", n))
			deadline := time.Now().Add(30 * time.Second)
			for time.Now().Before(deadline) {
				after := clock()
				if after >= before+int64(n) {
					if after != before+int64(n) {
						t.Fatalf("step advanced%d wanted%d", after-before, n)
					}
					t.Logf("%s advanced%d gametime%d->%d", name, n, before, after)
					return
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatal("chest step never completed")
		}

		var out []string
		query := 0
		sample := func(tag string, power int, closed bool) {
			var answers []bool
			for _, x := range []int{7, 10} {
				yes := fmt.Sprintf("RECHECK_Y_%d", query)
				no := fmt.Sprintf("RECHECK_N_%d", query)
				query++
				condition := fmt.Sprintf("block %d -61 0 minecraft:redstone_wire[power=%d]", x, power)
				sendCommand(t, other.pw, "execute if "+condition+" run say "+yes)
				sendCommand(t, other.pw, "execute unless "+condition+" run say "+no)
				got := false
				settleUse(t, other.sink, func(ps []usePkt) {
					other.foldPkts(ps)
					for _, p := range ps {
						if bytes.Contains(p.data, []byte(yes)) {
							got = true
						}
					}
				}, "chest-recheck-power")
				answers = append(answers, got)
				if !got {
					t.Errorf("%s %s wire%d wanted%d", name, tag, x, power)
				}
			}
			settleUse(t, d.sink, func(ps []usePkt) { d.foldPkts(ps) }, "chest-recheck-actor")
			if d.sawClose != closed {
				t.Errorf("%s %s closed=%v want%v", name, tag, d.sawClose, closed)
			}
			out = append(out, fmt.Sprintf("%s wires=%v closed=%v", tag, answers, d.sawClose))
		}
		d.reopen(8, -61, 0, 80, "recheck-open")
		sample("opened", 1, false)
		command(other, "gamemode spectator "+name)
		sample("frozen-conversion", 1, false)
		for step := 1; step <= 5; step++ {
			advance(1)
			power := 1
			if step >= 5 {
				power = 0
			}
			sample(fmt.Sprintf("step-%d", step), power, false)
		}
		d.close()
		sample("spectator-close", 0, false)
		command(other, "gamemode creative "+name)
		d.reopen(9, -61, 0, 81, "recheck-reopen")
		sample("reopened", 1, false)
		d.click("validity-cursor", 81, 0, 0, false, false)
		command(other, "tp "+name+" 100 -60 2 0 0")
		sample("frozen-far-close", 0, true)
		d.win = -1
		d.click("validity-player-resync", 9, 0, 6, true, true)
		out = append(out, fmt.Sprintf("returned=%s cursor=%s", d.snap.p0[36], d.snap.carried))
		if d.snap.p0[36] != "926#3" || d.snap.carried != "-" {
			t.Errorf("%s invalidation lost cursor: %s/%s", name, d.snap.p0[36], d.snap.carried)
		}
		d.close()
		command(other, "tick unfreeze")
		return out
	}
	v, g := run(tg.vanilla, names[0], names[2]), run(tg.mcgo, names[1], names[3])
	t.Logf("vanilla=%v mcgo=%v", v, g)
	if !reflect.DeepEqual(v, g) {
		t.Error("chest opener recheck or validity differs")
	}
}

func TestChestBackingReplacementParity(t *testing.T) {
	names := []string{"BackSelfV", "BackSelfM", "BackPartV", "BackPartM"}
	for i := range names {
		for c := 'a'; c <= 'p'; c++ {
			candidate := names[i] + string(c)
			off := 0
			for _, b := range []byte(candidate) {
				off = (31*off + int(b)) & 15
			}
			if off == 0 {
				names[i] = candidate
				break
			}
		}
	}
	packMeta := `{"pack":{"description":"atomic chest replacement","min_format":107,"max_format":107}}`
	fn := "setblock 8 -61 0 minecraft:air\nsetblock 8 -61 0 minecraft:chest[facing=north,type=left]\n"
	directory := t.TempDir()
	files := map[string]string{"ops.json": opsJSONFor(names...), "world/datapacks/menupin/pack.mcmeta": packMeta, "world/datapacks/menupin/data/menupin/function/replace.mcfunction": fn}
	for name, body := range map[string]string{"datapacks/menupin/pack.mcmeta": packMeta, "datapacks/menupin/data/menupin/function/replace.mcfunction": fn} {
		path := filepath.Join(directory, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	tg := requireBlackboxFiles(t, nil, files, func(s *server.Server) { s.WorldDir = directory })
	defer tg.cleanup()
	for n, tc := range []struct{ name, replace string }{{"partner-removed", "setblock 9 -61 0 minecraft:stone"}, {"same-type-new-entity", "function menupin:replace"}} {
		t.Run(tc.name, func(t *testing.T) {
			run := func(addr, name string) string {
				d := newClickDriverOn(t, addr, name, "minecraft:chest", 2, [][2]string{{"hotbar.0", "minecraft:diamond 3"}}, nil)
				d.close()
				command := func(text string) {
					sendCommand(t, d.pw, text)
					settleUse(t, d.sink, func(ps []usePkt) { d.foldPkts(ps) }, "chest-backing")
				}
				command("setblock 9 -61 0 minecraft:chest[facing=north,type=right]")
				command("item replace block 8 -61 0 container.0 with minecraft:stone 4")
				command("item replace block 9 -61 0 container.0 with minecraft:iron_ingot 5")
				command("tick freeze")
				d.reopen(8, -61, 0, 90, "backing-open")
				d.click("backing-cursor", 81, 0, 0, false, false)
				command(tc.replace)
				d.win = -1
				d.click("backing-player", 9, 0, 6, true, true)
				if !d.sawClose || d.snap.carried != "-" || d.snap.p0[36] != "926#3" {
					t.Errorf("%s backing invalidation close=%v cursor=%s returned=%s", name, d.sawClose, d.snap.carried, d.snap.p0[36])
				}
				result := fmt.Sprintf("closed=%v cursor=%s returned=%s", d.sawClose, d.snap.carried, d.snap.p0[36])
				command("tick unfreeze")
				return result
			}
			v, g := run(tg.vanilla, names[n*2]), run(tg.mcgo, names[n*2+1])
			t.Logf("vanilla=%s mcgo=%s", v, g)
			if v != g {
				t.Error("backing replacement invalidation differs")
			}
		})
	}
}
