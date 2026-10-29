package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestCommandMinecartActivationParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"gamemode": "creative", "command-spam-threshold-seconds": "0"}, map[string]string{"ops.json": opsJSONFor("P")}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
		s.OpsRequired = true
		s.SetOpLevel("P", 4)
	})
	defer targets.cleanup()
	run := func(addr string) {
		c, pr, pw := joinPlayQuiet(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		sendClientInformation(t, pw)
		sink := newChatSinkTP(pr, pw)
		time.Sleep(time.Second)
		for _, cmd := range []string{"tp @s 20 -55 20", "tick freeze", "gamerule command_block_output false", "fill 18 -61 18 46 -59 26 minecraft:air", "scoreboard objectives add mc dummy", "scoreboard players set runs mc 0", "scoreboard players set gap mc 0", "scoreboard players set zero mc 0", `summon minecraft:armor_stand 20.5 -50 20.5 {Tags:["cartready1"]}`, `summon minecraft:armor_stand 40.5 -50 20.5 {Tags:["cartready2"]}`} {
			sendCommand(t, pw, cmd)
		}
		waitDrained(t, pw, sink)
		step := func(n int) []string {
			before := gameTimeOf(t, pw, sink)
			sink.take()
			sendCommand(t, pw, fmt.Sprintf("tick step %d", n))
			deadline := time.Now().Add(10 * time.Second)
			var lines []string
			for time.Now().Before(deadline) {
				sendCommand(t, pw, "time query gametime")
				time.Sleep(30 * time.Millisecond)
				batch := sink.take()
				lines = append(lines, batch...)
				for _, line := range batch {
					key, args, ok := componentArgs(line)
					if ok && key == "commands.time.query.gametime" && len(args) > 0 {
						if v, ok := args[0].(int64); ok && v >= before+int64(n) {
							return lines
						}
					}
				}
			}
			t.Fatal("command cart step incomplete")
			return nil
		}
		ready := false
		for attempt := 0; attempt < 40 && !ready; attempt++ {
			step(1)
			sink.take()
			sendCommand(t, pw, `execute if entity @e[tag=cartready1] if entity @e[tag=cartready2] unless entity @e[tag=cartready1,nbt={Pos:[20.5d,-50d,20.5d]}] unless entity @e[tag=cartready2,nbt={Pos:[40.5d,-50d,20.5d]}] run say CART_ENTITIES_READY`)
			sendCommand(t, pw, "say CART_READY_END")
			ready = sawMarker(waitForMarker(t, sink, "CART_READY_END", 10*time.Second), "CART_ENTITIES_READY")
		}
		if !ready {
			t.Fatalf("%s: entity ticking arena never activated", addr)
		}
		sendCommand(t, pw, "kill @e[tag=cartready1]")
		sendCommand(t, pw, "kill @e[tag=cartready2]")
		waitDrained(t, pw, sink)
		commands := map[int]string{20: `execute if entity @s[type=minecraft:command_block_minecart,tag=carttick] run scoreboard players add runs mc 1`, 24: `execute positioned ~0.5 ~0.6 ~0.5 run setblock ~ ~ ~ minecraft:diamond_block`, 28: `tick query`, 32: `/say routed`, 36: `scoreboard players get zero mc`, 40: `SeArGe`, 44: ""}
		for _, x := range []int{20, 24, 28, 32, 36, 40, 44} {
			sendCommand(t, pw, fmt.Sprintf("setblock %d -61 20 minecraft:redstone_block", x))
			sendCommand(t, pw, fmt.Sprintf("setblock %d -60 20 minecraft:activator_rail[shape=north_south]", x))
			extra := ""
			track := 1
			if x == 32 {
				extra = `,CustomName:"CartRouter"`
				track = 0
			}
			if x == 40 {
				track = 0
			}
			sendCommand(t, pw, fmt.Sprintf(`summon minecraft:command_block_minecart %d.5 -60 20.5 {Tags:["cart%d"%s],Command:%q,TrackOutput:%db%s}`, x, x, func() string {
				if x == 20 {
					return `,"carttick"`
				}
				return ""
			}(), commands[x], track, extra))
		}
		sendCommand(t, pw, `summon minecraft:command_block_minecart 28.5 -55 24.5 {Tags:["cartfloat"],NoGravity:1b}`)
		for _, cmd := range []string{"setblock 20 -61 24 minecraft:stone", "setblock 20 -60 24 minecraft:activator_rail[shape=north_south]", `summon minecraft:command_block_minecart 20.5 -60 24.5 {Tags:["cartgap"],Command:"scoreboard players add gap mc 1",TrackOutput:0b}`} {
			sendCommand(t, pw, cmd)
		}
		for _, cmd := range []string{"setblock 20 -61 28 minecraft:redstone_block", "setblock 20 -60 28 minecraft:activator_rail[shape=north_south]", `summon minecraft:command_block_minecart 20.5 -60 28.5 {Tags:["cartkill"],Command:"kill @s",TrackOutput:0b}`} {
			sendCommand(t, pw, cmd)
		}
		for _, cmd := range []string{"setblock 48 -61 20 minecraft:redstone_block", "setblock 48 -60 20 minecraft:powered_rail[shape=north_south]", "setblock 48 -61 24 minecraft:stone", "setblock 48 -60 24 minecraft:powered_rail[shape=north_south,powered=true]"} {
			sendCommand(t, pw, cmd)
		}
		waitDrained(t, pw, sink)
		baseline := gameTimeOf(t, pw, sink)
		index := 0
		check := func(condition string) {
			sink.take()
			marker, end := fmt.Sprintf("CMA_PASS%d", index), fmt.Sprintf("CMA_DONE%d", index)
			index++
			sendCommand(t, pw, "execute "+condition+" run say "+marker)
			sendCommand(t, pw, "say "+end)
			if !sawMarker(waitForMarker(t, sink, end, 10*time.Second), marker) {
				t.Errorf("%s: %s", addr, condition)
			}
		}
		entity := func(x int) string {
			return fmt.Sprintf("@e[type=minecraft:command_block_minecart,tag=cart%d,limit=1]", x)
		}
		check("if block 20 -60 20 minecraft:activator_rail[powered=true]")
		check("if block 48 -60 20 minecraft:powered_rail[powered=true]")
		check("if block 48 -60 24 minecraft:powered_rail[powered=false]")
		step(3)
		check("if score runs mc matches 0")
		check("if entity @e[tag=cartkill]")
		check("if score gap mc matches 0")
		check("unless data entity " + entity(20) + " LastExecution")
		check(`if data entity @e[tag=cartfloat,limit=1] {Pos:[28.5d,-55d,24.5d]}`)
		first := step(1)
		check("unless entity @e[tag=cartkill]")
		routed := false
		for _, line := range first {
			routed = routed || strings.HasPrefix(line, "0x21:") && strings.Contains(line, "CartRouter") && strings.Contains(line, "routed")
		}
		if !routed {
			t.Errorf("%s: command cart source name was not routed", addr)
		}
		for _, condition := range []string{"if score runs mc matches 1", "if score gap mc matches 0", "if block 25 -60 21 minecraft:diamond_block", "if block 25 -59 21 minecraft:air", "if data entity " + entity(20) + " {SuccessCount:1}", "if data entity " + entity(24) + " {SuccessCount:1}", "if data entity " + entity(28) + " {SuccessCount:0}", "if data entity " + entity(28) + " LastOutput", "if data entity " + entity(32) + " {SuccessCount:1}", "unless data entity " + entity(32) + " LastOutput", "if data entity " + entity(36) + " {SuccessCount:1}", "if data entity " + entity(36) + " LastOutput", "if data entity " + entity(40) + " {SuccessCount:1}", "unless data entity " + entity(40) + " LastExecution", "unless data entity " + entity(40) + " LastOutput"} {
			check(condition)
		}
		check(fmt.Sprintf("if data entity %s {LastExecution:%dL}", entity(20), baseline+4))
		check("if data entity " + entity(44) + " {SuccessCount:0}")
		check(fmt.Sprintf("if data entity %s {LastExecution:%dL}", entity(44), baseline+4))
		step(3)
		check("if score runs mc matches 1")
		step(1)
		check("if score runs mc matches 2")
		check("if data entity " + entity(24) + " {SuccessCount:0}")
		sendCommand(t, pw, "setblock 20 -61 24 minecraft:redstone_block")
		waitDrained(t, pw, sink)
		step(1)
		check("if score gap mc matches 1")
		step(3)
		check("if score gap mc matches 1")
		check("if score runs mc matches 3")
		step(1)
		check("if score gap mc matches 2")
		sendCommand(t, pw, "setblock 20 -61 20 minecraft:stone")
		waitDrained(t, pw, sink)
		step(3)
		check("if score runs mc matches 3")
		sendCommand(t, pw, "setblock 20 -61 20 minecraft:redstone_block")
		waitDrained(t, pw, sink)
		step(1)
		check("if score runs mc matches 4")
		step(3)
		check("if score runs mc matches 4")
		step(1)
		check("if score runs mc matches 5")
		sendCommand(t, pw, "gamerule command_blocks_work false")
		waitDrained(t, pw, sink)
		step(4)
		check("if score runs mc matches 5")
		check("if data entity " + entity(20) + " {SuccessCount:0}")
		check(fmt.Sprintf("if data entity %s {LastExecution:%dL}", entity(20), baseline+25))
		check("if data entity " + entity(40) + " {SuccessCount:1}")
		check("unless data entity " + entity(40) + " LastExecution")
		t.Logf("%s: %d strict minecart activation predicates", addr, index)
	}
	run(targets.vanilla)
	run(targets.mcgo)
}
