package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestBlockPredicateNBTParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor("BlockPredicate")}, func(s *server.Server) { s.GameMode = server.GameModeCreative })
	defer tg.cleanup()
	cases := []struct {
		name, pos, spec string
		want            bool
	}{
		{"bare", "4 -60 4", "structure_block", true},
		{"state", "4 -60 4", "structure_block[mode=save]", true},
		{"wrong state", "4 -60 4", "structure_block[mode=load]", false},
		{"integer", "4 -60 4", `structure_block{sizeX:2}`, true},
		{"wrong integer", "4 -60 4", `structure_block{sizeX:7}`, false},
		{"typed integer", "4 -60 4", `structure_block{sizeX:2b}`, false},
		{"byte", "4 -60 4", `structure_block{powered:1b}`, true},
		{"boolean", "4 -60 4", `structure_block{powered:true}`, true},
		{"wrong numeric type", "4 -60 4", `structure_block{powered:1}`, false},
		{"quoted case", "4 -60 4", `structure_block{metadata:"MiXeD"}`, true},
		{"unquoted case", "4 -60 4", `structure_block{metadata:MiXeD}`, true},
		{"wrong case", "4 -60 4", `structure_block{metadata:"mixed"}`, false},
		{"single quotes", "4 -60 4", `structure_block{'metadata':'MiXeD'}`, true},
		{"compound whitespace", "4 -60 4", `structure_block{sizeX: 2, metadata: "MiXeD"}`, true},
		{"unicode whitespace", "4 -60 4", "structure_block{\u2003sizeX:\u20032}", true},
		{"array whitespace", "4 -60 4", `structure_block{absent:[ I; 1 , 2 ]}`, false},
		{"list whitespace", "10 -60 4", `chest{Items:[ {Slot:0b}, {Slot:2b} ]}`, true},
		{"properties and NBT", "4 -60 4", `structure_block[mode=save]{sizeX:2}`, true},
		{"wrong property and NBT", "4 -60 4", `structure_block[mode=load]{sizeX:2}`, false},
		{"empty BE compound", "4 -60 4", `structure_block{}`, true},
		{"missing field", "4 -60 4", `structure_block{absent:0}`, false},
		{"full metadata", "4 -60 4", `structure_block{x:4,y:-60,z:4,id:"minecraft:structure_block"}`, true},
		{"wrong coordinate", "4 -60 4", `structure_block{x:5}`, false},
		{"wrong id", "4 -60 4", `structure_block{id:"minecraft:chest"}`, false},
		{"components", "4 -60 4", `structure_block{components:{}}`, true},
		{"chunk-only key", "4 -60 4", `structure_block{keepPacked:0b}`, false},
		{"plain non-BE", "16 -60 4", `stone`, true},
		{"non-BE empty compound", "16 -60 4", `stone{}`, false},
		{"list element subset", "10 -60 4", `chest{Items:[{Slot:0b}]}`, true},
		{"list reordered", "10 -60 4", `chest{Items:[{Slot:2b},{Slot:0b}]}`, true},
		{"list reused match", "10 -60 4", `chest{Items:[{Slot:0b},{Slot:0b}]}`, true},
		{"list size bound", "10 -60 4", `chest{Items:[{Slot:0b},{Slot:0b},{Slot:0b}]}`, false},
		{"missing list element", "10 -60 4", `chest{Items:[{Slot:1b}]}`, false},
		{"nested item subset", "10 -60 4", `chest{Items:[{Slot:0b,id:"minecraft:stone",count:3}]}`, true},
		{"nested typed count", "10 -60 4", `chest{Items:[{Slot:0b,count:3b}]}`, false},
		{"empty list vs nonempty", "10 -60 4", `chest{Items:[]}`, false},
		{"empty list", "12 -60 4", `chest{Items:[]}`, true},
	}
	for _, tc := range []struct{ tag, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(tc.tag, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, "BlockPredicate")
			defer c.Close()
			drainToJoinBlackbox(t, tc.tag, pr)
			awaitPlayReadyBlackbox(t, tc.tag, c, pr, pw)
			sink := newChatSinkTP(pr, pw)
			serial := 0
			barrier := func() []string {
				serial++
				marker := fmt.Sprintf("BLOCK_PREDICATE_END_%d", serial)
				sendCommand(t, pw, "say "+marker)
				var lines []string
				deadline := time.Now().Add(10 * time.Second)
				for time.Now().Before(deadline) {
					lines = append(lines, sink.take()...)
					if structureSaidBB(lines, marker) {
						return lines
					}
					time.Sleep(10 * time.Millisecond)
				}
				t.Fatal("typed command barrier missing")
				return nil
			}
			for _, cmd := range []string{"tick freeze", "setblock 4 -60 4 structure_block[mode=save]", "setblock 10 -60 4 chest[facing=north]", "setblock 12 -60 4 chest[facing=north]", "setblock 16 -60 4 stone", `data merge block 10 -60 4 {Items:[{Slot:0b,id:"minecraft:stone",count:3},{Slot:2b,id:"minecraft:diamond",count:1}]}`, `data merge block 4 -60 4 {name:"test:predicate",metadata:"MiXeD",sizeX:2,powered:1b}`} {
				sendCommand(t, pw, cmd)
				barrier()
			}
			for i, check := range cases {
				for _, kind := range []string{"if", "unless"} {
					marker := fmt.Sprintf("BLOCK_PREDICATE_RESULT_%d_%s", i, kind)
					sendCommand(t, pw, "execute "+kind+" block "+check.pos+" "+check.spec+" run say "+marker)
					lines := barrier()
					got := structureSaidBB(lines, marker)
					want := check.want
					if kind == "unless" {
						want = !want
					}
					if got != want {
						t.Errorf("%s %s: marker%v want%v", kind, check.name, got, want)
					}
					for _, line := range lines {
						if strings.Contains(line, "command.context.here") {
							t.Errorf("%s %s produced syntax context for valid predicate", kind, check.name)
							break
						}
					}
				}
			}
			t.Logf("checked%d positive/negative typed block-NBT conditions", len(cases)*2)
		})
	}
}
