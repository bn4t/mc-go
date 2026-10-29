package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// Reloading the active command source must retain its live result/output
// destination. The feedback source itself is chosen before the command runs.
func TestCommandSourceReloadParity(t *testing.T) {
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
		watch, watchReader, watchWriter := joinPlayQuiet(t, addr, "ReloadWatch")
		defer watch.Close()
		_ = watch.SetDeadline(time.Time{})
		sendClientInformation(t, watchWriter)
		watchSink := newChatSinkTP(watchReader, watchWriter)
		time.Sleep(time.Second)
		for _, cmd := range []string{"tp @s 20 -55 30", "gamemode spectator ReloadWatch", "tp ReloadWatch 20 -55 30", "tick freeze", "gamerule command_block_output false", "scoreboard objectives add reload dummy", "scoreboard players set zero reload 0", "fill 18 -61 28 42 -57 36 minecraft:air", `summon minecraft:armor_stand 20.5 -50 34.5 {Tags:["reloadready1"]}`, `summon minecraft:armor_stand 40.5 -50 34.5 {Tags:["reloadready2"]}`} {
			sendCommand(t, pw, cmd)
		}
		var observed []string
		clock := func() int64 {
			sendCommand(t, pw, "time query gametime")
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
				batch := sink.take()
				observed = append(observed, batch...)
				for _, line := range batch {
					key, args, ok := componentArgs(line)
					if ok && key == "commands.time.query.gametime" && len(args) > 0 {
						if v, ok := args[0].(int64); ok {
							return v
						}
					}
				}
			}
			t.Fatal("reload clock missing")
			return 0
		}
		step := func(n int) {
			before := clock()
			sendCommand(t, pw, fmt.Sprintf("tick step %d", n))
			deadline := time.Now().Add(10 * time.Second)
			for clock() < before+int64(n) {
				if time.Now().After(deadline) {
					t.Fatal("reload step incomplete")
				}
			}
		}
		ready := false
		var warmWatch []string
		trackedWarm := map[int32]bool{}
		for attempt := 0; attempt < 40 && !ready; attempt++ {
			step(1)
			sink.take()
			sendCommand(t, pw, `execute if entity @e[tag=reloadready1] if entity @e[tag=reloadready2] unless entity @e[tag=reloadready1,nbt={Pos:[20.5d,-50d,34.5d]}] unless entity @e[tag=reloadready2,nbt={Pos:[40.5d,-50d,34.5d]}] run say RELOAD_READY`)
			sendCommand(t, pw, "say RELOAD_READY_END")
			moved := sawMarker(waitForMarker(t, sink, "RELOAD_READY_END", 10*time.Second), "RELOAD_READY")
			warmWatch = append(warmWatch, watchSink.take()...)
			prefix := fmt.Sprintf("%#x:", server.IDPlaySpawnEntity)
			armorType, _ := server.EntityTypeID("armor_stand")
			for _, line := range append(observed, warmWatch...) {
				if !strings.HasPrefix(line, prefix) {
					continue
				}
				r := protocol.NewPacketReader([]byte(strings.TrimPrefix(line, prefix)))
				id, e1 := r.VarInt()
				_, e2 := r.Read(16)
				typ, e3 := r.VarInt()
				if e1 == nil && e2 == nil && e3 == nil && typ == armorType {
					trackedWarm[id] = true
				}
			}
			ready = moved && len(trackedWarm) >= 2
		}
		if !ready {
			t.Fatalf("%s: reload arenas did not tick", addr)
		}
		sendCommand(t, pw, "kill @e[tag=reloadready1]")
		sendCommand(t, pw, "kill @e[tag=reloadready2]")
		sendCommand(t, pw, `setblock 34 -55 34 minecraft:command_block{Command:"",CustomName:"ForkA",SuccessCount:999}`)
		sendCommand(t, pw, `setblock 36 -55 34 minecraft:command_block{Command:"",CustomName:"ForkB",SuccessCount:999}`)
		sendCommand(t, pw, `summon minecraft:armor_stand 34.5 -54 34.5 {NoGravity:1b,Tags:["reloadfork"]}`)
		sendCommand(t, pw, `summon minecraft:armor_stand 36.5 -54 34.5 {NoGravity:1b,Tags:["reloadfork"]}`)

		blockCommands := map[int]string{
			20: `setblock ~ ~ ~ minecraft:command_block{Command:"say next",TrackOutput:1b}`,
			24: `setblock ~ ~ ~ minecraft:command_block{Command:"say next",TrackOutput:0b,UpdateLastExecution:0b,LastExecution:123L,LastOutput:"incoming"}`,
			28: `setblock ~ ~ ~ minecraft:command_block{Command:"say next",TrackOutput:1b,LastOutput:"seed"}`,
			32: `data merge block ~ ~ ~ {CustomName:"Renamed",SuccessCount:999,TrackOutput:0b}`,
			36: `data merge block ~ ~ ~ {CustomName:"Renamed",SuccessCount:999,TrackOutput:1b}`,
			40: `execute as @e[type=minecraft:armor_stand,tag=reloadfork,sort=nearest] at @s run data modify block 40 -60 30 {} merge from block ~ ~-1 ~`,
		}
		for _, x := range []int{20, 24, 28, 32, 36, 40} {
			track := 1
			if x == 28 || x == 36 {
				track = 0
			}
			sendCommand(t, pw, fmt.Sprintf(`setblock %d -60 30 minecraft:command_block[facing=east]{Command:%q,TrackOutput:%db,CustomName:"Original"}`, x, blockCommands[x], track))
			sendCommand(t, pw, fmt.Sprintf("setblock %d -60 30 minecraft:redstone_block", x-1))
		}
		cartCommands := map[int]string{20: `data merge entity @s {CustomName:"Renamed",SuccessCount:999,TrackOutput:0b}`, 24: `data merge entity @s {CustomName:"Renamed",SuccessCount:999,TrackOutput:1b}`, 28: `say original`}
		for _, x := range []int{20, 24, 28} {
			track := 1
			if x == 24 {
				track = 0
			}
			sendCommand(t, pw, fmt.Sprintf("setblock %d -61 34 minecraft:redstone_block", x))
			sendCommand(t, pw, fmt.Sprintf("setblock %d -60 34 minecraft:activator_rail[shape=north_south,powered=true]", x))
			sendCommand(t, pw, fmt.Sprintf(`summon minecraft:command_block_minecart %d.5 -60 34.5 {Tags:["reloadcart%d"],Command:%q,TrackOutput:%db,CustomName:"Original"}`, x, x, cartCommands[x], track))
		}
		baseline := clock()
		index := 0
		check := func(condition string) {
			sink.take()
			marker, end := fmt.Sprintf("CSR_PASS%d", index), fmt.Sprintf("CSR_DONE%d", index)
			index++
			sendCommand(t, pw, "execute "+condition+" run say "+marker)
			sendCommand(t, pw, "say "+end)
			if !sawMarker(waitForMarker(t, sink, end, 10*time.Second), marker) {
				t.Errorf("%s: %s", addr, condition)
			}
		}
		blockEdit := func(x int32, command string) {
			var b protocol.Buffer
			b.Position(x, -60, 30)
			b.String(command)
			b.VarInt(2)
			b.Byte(1)
			if err := pw.WritePacket(server.IDPlaySetCommandBlockSB, b.Bytes()); err != nil {
				t.Fatal(err)
			}
			waitDrained(t, pw, sink)
		}
		cartIDs := map[int]int32{}
		captureCartIDs := func() {
			prefix := fmt.Sprintf("%#x:", server.IDPlaySpawnEntity)
			cartType, _ := server.EntityTypeID("command_block_minecart")
			for _, line := range append(observed, watchSink.take()...) {
				if !strings.HasPrefix(line, prefix) {
					continue
				}
				r := protocol.NewPacketReader([]byte(strings.TrimPrefix(line, prefix)))
				id, e1 := r.VarInt()
				_, e2 := r.Read(16)
				typ, e3 := r.VarInt()
				x, e4 := r.Double()
				if e1 == nil && e2 == nil && e3 == nil && e4 == nil && typ == cartType {
					cartIDs[int(x)] = id
				}
			}
		}
		check(`if data block 34 -55 34 {CustomName:"ForkA",SuccessCount:999}`)
		check(`if data block 36 -55 34 {CustomName:"ForkB",SuccessCount:999}`)
		step(1)
		for attempt := 0; attempt < 40; attempt++ {
			captureCartIDs()
			if len(cartIDs) == 3 {
				break
			}
			clock()
			time.Sleep(30 * time.Millisecond)
		}
		if len(cartIDs) != 3 {
			sendCommand(t, pw, `data get entity @e[tag=reloadcart20,limit=1]`)
			sendCommand(t, pw, "say RELOAD_DIAG_END")
			t.Logf("reload entity diagnostic: %v", waitForMarker(t, sink, "RELOAD_DIAG_END", 10*time.Second))
			sink.mu.Lock()
			t.Logf("primary packet counts: %v", sink.ids)
			sink.mu.Unlock()
			t.Fatalf("%s: reload cart IDs not tracked: %v", addr, cartIDs)
		}
		for _, condition := range []string{`if data block 20 -60 30 {Command:"say next",SuccessCount:1,TrackOutput:1b}`, `if data block 20 -60 30 LastOutput.extra[0]`, `if data block 24 -60 30 {Command:"say next",SuccessCount:1,TrackOutput:0b,UpdateLastExecution:0b}`, `unless data block 24 -60 30 LastExecution`, `unless data block 24 -60 30 LastOutput`, `if data block 28 -60 30 {Command:"say next",SuccessCount:1,TrackOutput:1b,LastOutput:"seed"}`, `if data block 32 -60 30 {CustomName:"Renamed",SuccessCount:1000,TrackOutput:0b}`, `unless data block 32 -60 30 LastOutput`, `if data block 36 -60 30 {CustomName:"Renamed",SuccessCount:1000,TrackOutput:1b}`, `unless data block 36 -60 30 LastOutput`} {
			check(condition)
		}
		for _, x := range []int{20, 28, 32, 36, 40} {
			check(fmt.Sprintf("if data block %d -60 30 {LastExecution:%dL}", x, baseline+1))
		}
		check(`if data block 40 -60 30 {CustomName:"ForkA",SuccessCount:1000}`)
		check("if data block 40 -60 30 LastOutput.extra[0]")
		check(`if data block 34 -55 34 {CustomName:"ForkA",SuccessCount:999}`)
		check(`if data block 36 -55 34 {CustomName:"ForkB",SuccessCount:999}`)
		check(`if data block 40 -60 30 {id:"minecraft:command_block",x:40,y:-60,z:30}`)
		blockEdit(24, "say next")
		check("if data block 24 -60 30 LastOutput.extra[0]")
		check("unless data block 24 -60 30 LastExecution")
		blockEdit(32, blockCommands[32])
		check("if data block 32 -60 30 LastOutput.extra[0]")
		// A disabled packet edit leaves metadata13 stale. Feedback from the
		// new command updates it (resetting the count) before leaf completion.
		sendCommand(t, pw, "gamerule command_blocks_work false")
		waitDrained(t, pw, sink)
		var stale protocol.Buffer
		stale.VarInt(cartIDs[28])
		stale.String("scoreboard players get zero reload")
		stale.Bool(true)
		if err := pw.WritePacket(server.IDPlaySetCommandMinecartSB, stale.Bytes()); err != nil {
			t.Fatal(err)
		}
		waitDrained(t, pw, sink)
		check(`if data entity @e[tag=reloadcart28,limit=1] {Command:"scoreboard players get zero reload"}`)
		sendCommand(t, pw, "gamerule command_blocks_work true")
		waitDrained(t, pw, sink)
		step(3)
		check(`if data entity @e[tag=reloadcart28,limit=1] {SuccessCount:1}`)
		check(fmt.Sprintf("if data entity @e[tag=reloadcart28,limit=1] {LastExecution:%dL}", baseline+4))
		check("if data entity @e[tag=reloadcart28,limit=1] LastOutput.extra[0]")
		for _, x := range []int{20, 24} {
			selector := fmt.Sprintf("@e[type=minecraft:command_block_minecart,tag=reloadcart%d,limit=1]", x)
			track := 0
			if x == 24 {
				track = 1
			}
			check(fmt.Sprintf(`if data entity %s {CustomName:"Renamed",SuccessCount:1000,TrackOutput:%db}`, selector, track))
			check(fmt.Sprintf("if data entity %s {LastExecution:%dL}", selector, baseline+4))
			check("unless data entity " + selector + " LastOutput")
		}
		// Reveal the first cart's hidden output using a real edit packet, which
		// changes tracking without reloading/discarding the base's runtime value.
		id := cartIDs[20]

		var b protocol.Buffer
		b.VarInt(id)
		b.String(cartCommands[20])
		b.Bool(true)
		if err := pw.WritePacket(server.IDPlaySetCommandMinecartSB, b.Bytes()); err != nil {
			t.Fatal(err)
		}
		waitDrained(t, pw, sink)
		check(`if data entity @e[tag=reloadcart20,limit=1] LastOutput.extra[0]`)
		t.Logf("%s: %d strict source reload predicates", addr, index)
	}
	run(targets.vanilla)
	run(targets.mcgo)
}
