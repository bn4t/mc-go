package blackbox

import (
	"fmt"
	"math"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Pair the source while the actor is a spectator, then admit pickup separately.
func TestOrbPlayerTouchDispatchParity(t *testing.T) {
	const actor = "OrbTouchRead"
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2", "command-spam-threshold-seconds": "0"}, map[string]string{"ops.json": opsJSONFor(actor)}, nil)
	defer targets.cleanup()
	run := func(addr string) {
		c, pr, pw, actorID := joinPlayEid(t, addr, actor)
		defer c.Close()
		awaitPlayReadyBlackbox(t, addr, c, pr, pw)
		sink := newChatSinkTP(pr, pw)
		serial := 0
		var source int32
		takeEvents, takeCount := 0, int32(0)
		observe := func(lines []string) []string {
			for _, line := range lines {
				prefix, data, ok := strings.Cut(line, ":")
				if !ok {
					continue
				}
				id, err := strconv.ParseInt(prefix, 0, 32)
				if err != nil {
					continue
				}
				r := protocol.NewPacketReader([]byte(data))
				switch int32(id) {
				case server.IDPlaySpawnEntity:
					eid, e := r.VarInt()
					_, e2 := r.UUID()
					typ, e3 := r.VarInt()
					if e == nil && e2 == nil && e3 == nil && typ == mustEType(t, "experience_orb") {
						source = eid
					}
				case server.IDPlayTakeItem:
					eid, e := r.VarInt()
					recipient, e2 := r.VarInt()
					count, e3 := r.VarInt()
					if e == nil && e2 == nil && e3 == nil && source != 0 && eid == source {
						if recipient != actorID {
							t.Errorf("CollectItem source%d recipient%d want%d", source, recipient, actorID)
						}
						takeEvents++
						takeCount += count
					}
				}
			}
			return lines
		}
		collect := func(marker string) []string { return observe(waitForExactSayBB(t, sink, marker, 30*time.Second)) }
		command := func(cmd string) []string {
			serial++
			receipt := fmt.Sprintf("ORB_TOUCH_RECEIPT_%d", serial)
			sendCommand(t, pw, cmd)
			sendCommand(t, pw, "say "+receipt)
			return collect(receipt)
		}
		command("gamemode spectator @s")
		command("tp @s 4.5 162.0 0.5")
		command("tick freeze")
		command("fill 2 161 -1 8 166 1 air")
		if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
			t.Fatal(err)
		}
		setupPlayerTickBarrierBB(t, pw)
		waitPlayerTicksBB(t, pw, collect, "OrbTouchInitial", 3)
		for i, tc := range []struct {
			name, mode string
			frozen     bool
			x          float64
			take       bool
		}{{"frozen_creative", "creative", true, 5.5, true}, {"frozen_survival", "survival", true, 5.5, true}, {"normal_creative", "creative", false, 5.5, true}, {"frozen_spectator", "spectator", true, 5.5, false}, {"orb_width_inside", "creative", true, 6.04, true}, {"orb_width_outside", "creative", true, 6.06, false}} {
			command("gamemode spectator @s")
			command("tick freeze")
			command("kill @e[type=experience_orb]")
			waitPlayerTicksBB(t, pw, collect, fmt.Sprintf("OrbTouchClear%d", i), 3)
			x, y, z, ok := dataGetPos(t, sink, pw, "@s")
			if !ok || x != 4.5 || y != 162 || z != .5 {
				t.Fatalf("actual actor Pos[%g %g %g] valid%t", x, y, z, ok)
			}
			state, health := dataGetSel(t, sink, pw, "@s", "Health")
			if state != "query" || math.IsNaN(health) || math.IsInf(health, 0) || health <= 0 {
				t.Fatalf("actor Health%g state%s", health, state)
			}
			if lines := command("execute if entity @a[name=OrbTouchRead,x=3,y=161,z=-1,dx=3,dy=4,dz=3] run say ORB_TOUCH_BODY"); !structureSaidBB(lines, "ORB_TOUCH_BODY") {
				t.Fatal("actor bounding-box witness missing")
			}
			beforeState, before := dataGetSel(t, sink, pw, "@s", "XpTotal")
			if beforeState != "query" || math.IsNaN(before) || math.IsInf(before, 0) {
				t.Fatal("missing initial XP total")
			}
			source, takeEvents, takeCount = 0, 0, 0
			command(fmt.Sprintf(`summon experience_orb %.17g 162.6 0.5 {Tags:["OrbTouch"],UUID:[I;1,2,3,%d],Count:2,Value:3s,NoGravity:1b,Motion:[0d,0d,0d],Age:0s}`, tc.x, 4+i))
			sel := "@e[type=experience_orb,tag=OrbTouch,limit=1]"
			deadline := time.Now().Add(5 * time.Second)
			for source == 0 && time.Now().Before(deadline) {
				command("say ORB_TOUCH_PAIR")
				time.Sleep(20 * time.Millisecond)
			}
			if source == 0 {
				t.Fatal("source orb never paired")
			}
			ageState, age := dataGetSel(t, sink, pw, sel, "Age")
			countState, count := dataGetSel(t, sink, pw, sel, "Count")
			if ageState != "query" || age != 0 || countState != "query" || count != 2 {
				t.Fatalf("source Age%s/%g Count%s/%g", ageState, age, countState, count)
			}
			if !tc.frozen {
				command("tick unfreeze")
			}
			command("gamemode " + tc.mode + " @s")
			marker := fmt.Sprintf("ORB_TOUCH_MODE_%d", i)
			if lines := command("execute if entity @s[gamemode=" + tc.mode + "] run say " + marker); !structureSaidBB(lines, marker) {
				t.Fatal("game mode witness missing")
			}
			waitPlayerTicksBB(t, pw, collect, fmt.Sprintf("OrbTouchObserve%d", i), 4)
			command("tick freeze")
			afterState, after := dataGetSel(t, sink, pw, "@s", "XpTotal")
			if afterState != "query" || math.IsNaN(after) || math.IsInf(after, 0) {
				t.Fatal("missing final XP total")
			}
			marker = fmt.Sprintf("ORB_TOUCH_EXISTS_%d", i)
			exists := structureSaidBB(command("execute if entity "+sel+" run say "+marker), marker)
			wantXP, wantEvents, wantCount := float64(0), 0, int32(0)
			if tc.take {
				wantXP, wantEvents, wantCount = 6, 2, 2
			}
			if after-before != wantXP || takeEvents != wantEvents || takeCount != wantCount || exists == tc.take {
				t.Errorf("%s %s XPdelta%g TakeEvents%d TakeCount%d exists%t want XP%g events%d count%d exists%t", addr, tc.name, after-before, takeEvents, takeCount, exists, wantXP, wantEvents, wantCount, !tc.take)
			}
			if exists {
				state, count := dataGetSel(t, sink, pw, sel, "Count")
				if state != "query" || count != 2 {
					t.Errorf("%s %s surviving Count%s/%g want2", addr, tc.name, state, count)
				}
			}
			t.Logf("checked %s %s source%d XPdelta%g TakeEvents%d exists%t", addr, tc.name, source, after-before, takeEvents, exists)
		}
	}
	run(targets.vanilla)
	run(targets.mcgo)
}
