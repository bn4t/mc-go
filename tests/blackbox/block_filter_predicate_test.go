package blackbox

import (
	"fmt"
	"mc-go/internal/server"
	"testing"
	"time"
)

func TestFillCloneBlockPredicateParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor("FilterProbe")}, func(s *server.Server) { s.GameMode = server.GameModeCreative })
	defer tg.cleanup()
	cases := []struct {
		name, source, filter string
		want                 bool
	}{
		{"tag member", "chest[facing=north]", `#guarded_by_piglins`, true},
		{"tag property", "chest[facing=north]", `#guarded_by_piglins[facing=north]`, true},
		{"tag wrong property", "chest[facing=north]", `#guarded_by_piglins[facing=east]`, false},
		{"tag vague property", "chest[facing=north]", `#guarded_by_piglins[no_such_property=yes]`, false},
		{"tag NBT", "chest[facing=north]", `#guarded_by_piglins{Items:[{id:"minecraft:stone",count:3}]}`, true},
		{"tag typed NBT mismatch", "chest[facing=north]", `#guarded_by_piglins{Items:[{count:3b}]}`, false},
		{"tag properties NBT", "chest[facing=north]", `#guarded_by_piglins[facing=north]{Items:[{Slot:0b}]}`, true},
		{"exact NBT", "chest[facing=north]", `chest{Items:[{count:3}]}`, true},
		{"exact NBT mismatch", "chest[facing=north]", `chest{Items:[]}`, false},
		{"empty NBT requires BE", "gold_block", `#guarded_by_piglins{}`, false},
		{"exact empty NBT requires BE", "stone", `stone{}`, false},
		{"case sensitive native key", "chest[facing=north]", `chest{items:[]}`, false},
	}
	for _, target := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(target.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, target.addr, "FilterProbe")
			defer c.Close()
			drainToJoinBlackbox(t, target.name, pr)
			awaitPlayReadyBlackbox(t, target.name, c, pr, pw)
			sink := newChatSinkTP(pr, pw)
			serial := 0
			command := func(cmd string) []string {
				sendCommand(t, pw, cmd)
				serial++
				marker := fmt.Sprintf("FILTER_BARRIER_%d", serial)
				sendCommand(t, pw, "say "+marker)
				return waitForExactSayBB(t, sink, marker, 10*time.Second)
			}
			command("tick freeze")
			for _, kind := range []string{"fill", "clone"} {
				for i, tc := range cases {
					command("setblock 4 -60 4 air")
					command("setblock 4 -60 4 " + tc.source)
					if tc.source == "chest[facing=north]" {
						command(`data merge block 4 -60 4 {Items:[{Slot:0b,id:"minecraft:stone",count:3}]}`)
					}
					command("setblock 12 -60 4 diamond_block")
					if kind == "fill" {
						command("fill 4 -60 4 4 -60 4 diamond_block replace " + tc.filter)
					} else {
						command("clone 4 -60 4 4 -60 4 12 -60 4 filtered " + tc.filter)
					}
					pos, block := "4 -60 4", "diamond_block"
					if kind == "clone" {
						pos = "12 -60 4"
						if tc.want {
							block = tc.source
						}
					} else if !tc.want {
						block = tc.source
					}
					marker := fmt.Sprintf("FILTER_RESULT_%s_%d", kind, i)
					lines := command("execute if block " + pos + " " + block + " run say " + marker)
					if !structureSaidBB(lines, marker) {
						t.Errorf("%s %s missing expected world-state %s (filter match%v)", kind, tc.name, block, tc.want)
					}
				}
			}
			t.Logf("checked%d fill/clone world-state filter outcomes", len(cases)*2)
		})
	}
}
