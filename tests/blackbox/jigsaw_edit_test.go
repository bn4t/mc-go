package blackbox

import (
	"bytes"
	"fmt"
	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func writeJigsawEditBB(t *testing.T, w *protocol.Writer, pos [3]int32, name, target, pool, finalState, joint string, selection, placement int32) {
	t.Helper()
	var b protocol.Buffer
	b.Position(pos[0], pos[1], pos[2])
	b.String(name)
	b.String(target)
	b.String(pool)
	b.String(finalState)
	b.String(joint)
	b.VarInt(selection)
	b.VarInt(placement)
	if err := w.WritePacket(server.IDPlaySetJigsawBlockSB, b.Bytes()); err != nil {
		t.Fatal(err)
	}
}

func TestJigsawEditorParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor("JigsawEditor")}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
		s.OpsRequired = true
		s.SetOpLevel("JigsawEditor", 4)
	})
	defer tg.cleanup()
	type result struct {
		Checks                    []bool
		FirstUpdate               []string
		BlockUpdate, RepeatUpdate bool
	}
	run := func(addr string) result {
		c, pr, pw := joinPlayBlackbox(t, addr, "JigsawEditor")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSinkTP(pr, pw)
		pos := [3]int32{4, 70, 4}
		serial := 0
		sayMarker := func(lines []string, marker string) bool {
			for _, line := range lines {
				if strings.HasPrefix(line, "0x21:") && strings.Contains(line, marker) {
					return true
				}
			}
			return false
		}
		var response map[string]any
		var blockUpdate bool
		pump := func() []string {
			lines := sink.take()
			for _, line := range lines {
				prefix, data, ok := strings.Cut(line, ":")
				if !ok {
					continue
				}
				id, e := strconv.ParseInt(prefix, 0, 32)
				if e != nil {
					continue
				}
				if int32(id) != server.IDPlayBlockEntityData && int32(id) != server.IDPlayBlockUpdateCB {
					continue
				}
				r := protocol.NewPacketReader([]byte(data))
				x, y, z, e := r.Position()
				if e != nil || [3]int32{x, y, z} != pos {
					continue
				}
				if int32(id) == server.IDPlayBlockUpdateCB {
					blockUpdate = true
					continue
				}
				_, _ = r.VarInt()
				raw, e := r.Read(r.Remaining())
				if e != nil {
					continue
				}
				v, e := nbt.ReadUnnamed(bytes.NewReader(raw))
				if e == nil {
					response, _ = v.(map[string]any)
				}
			}
			return lines
		}
		barrier := func() []string {
			serial++
			marker := fmt.Sprintf("JIGSAW_DONE_%d", serial)
			sendCommand(t, pw, "say "+marker)
			deadline := time.Now().Add(15 * time.Second)
			var out []string
			for time.Now().Before(deadline) {
				lines := pump()
				out = append(out, lines...)
				if sayMarker(lines, marker) {
					return out
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatal("jigsaw command barrier timeout")
			return nil
		}
		command := func(cmd string) { sendCommand(t, pw, cmd); barrier() }
		var out result
		check := func(condition string) bool {
			serial++
			marker := fmt.Sprintf("JIGSAW_CHECK_%d", serial)
			sendCommand(t, pw, "execute "+condition+" run say "+marker)
			lines := barrier()
			ok := sayMarker(lines, marker)
			deadline := time.Now().Add(150 * time.Millisecond)
			for !ok && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
				ok = sayMarker(pump(), marker)
			}
			if !ok {
				sendCommand(t, pw, "data get block 4 70 4")
				for _, line := range barrier() {
					if strings.Contains(line, "data.block.query") {
						t.Logf("condition %s actual %q", condition, line)
					}
				}
			}
			out.Checks = append(out.Checks, ok)
			return ok
		}
		settle := func() { time.Sleep(120 * time.Millisecond); pump() }
		waitResponse := func() {
			deadline := time.Now().Add(time.Second)
			for response == nil && time.Now().Before(deadline) {
				pump()
				time.Sleep(10 * time.Millisecond)
			}
			pump()
		}
		command("tick freeze")
		command("setblock 0 69 0 stone")
		command("tp @s 0.5 70 0.5")
		command("setblock 4 70 4 minecraft:jigsaw[orientation=north_up]")
		check(`if data block 4 70 4 {name:"minecraft:empty",target:"minecraft:empty",pool:"minecraft:empty",final_state:"minecraft:air",joint:"rollable",selection_priority:0,placement_priority:0}`)
		settle()
		response = nil
		blockUpdate = false
		edit := func(name, target, pool, finalState, joint string, selection, placement int32) {
			response = nil
			writeJigsawEditBB(t, pw, pos, name, target, pool, finalState, joint, selection, placement)
			barrier()
			waitResponse()
			if joint == "invalid_joint" || joint == "bad_joint" {
				t.Logf("packet joint%s response%#v", joint, response)
			}
		}
		edit("example:entry", "example:door", "minecraft:empty", "minecraft:oak_stairs[facing=south,half=top]", "rollable", -17, 2147483647)
		waitResponse()
		out.BlockUpdate = blockUpdate
		for _, key := range []string{"name", "target", "pool", "final_state", "joint", "selection_priority", "placement_priority"} {
			out.FirstUpdate = append(out.FirstUpdate, fmt.Sprintf("%T:%v", response[key], response[key]))
		}
		check(`if data block 4 70 4 {name:"example:entry",target:"example:door",pool:"minecraft:empty",final_state:"minecraft:oak_stairs[facing=south,half=top]",joint:"rollable",selection_priority:-17,placement_priority:2147483647}`)
		settle()
		response = nil
		edit("example:entry", "example:door", "minecraft:empty", "minecraft:oak_stairs[facing=south,half=top]", "rollable", -17, 2147483647)
		waitResponse()
		out.RepeatUpdate = response != nil
		edit("plain", "goal", "empty", " literal state ", "invalid_joint", -2147483648, -5)
		check(`if data block 4 70 4 {name:"minecraft:plain",target:"minecraft:goal",pool:"minecraft:empty",final_state:" literal state ",joint:"aligned",selection_priority:-2147483648,placement_priority:-5}`)
		command("gamemode survival")
		edit("example:denied", "example:no", "minecraft:empty", "minecraft:stone", "rollable", 1, 2)
		check(`if data block 4 70 4 {name:"minecraft:plain",final_state:" literal state ",joint:"aligned",selection_priority:-2147483648,placement_priority:-5}`)
		command("gamemode creative")
		command("setblock 5 70 4 stone")
		writeJigsawEditBB(t, pw, [3]int32{5, 70, 4}, "example:wrong", "minecraft:empty", "minecraft:empty", "minecraft:air", "rollable", 1, 2)
		barrier()
		check("if block 5 70 4 stone")
		command("setblock 6 70 4 air")
		writeJigsawEditBB(t, pw, [3]int32{6, 70, 4}, "example:missing", "minecraft:empty", "minecraft:empty", "minecraft:air", "rollable", 1, 2)
		barrier()
		check("if block 6 70 4 air")
		command(`data merge block 4 70 4 {name:"native",target:"other",pool:"empty",joint:"bad",final_state:"",selection_priority:3.75f,placement_priority:-2.25d}`)
		check(`if data block 4 70 4 {name:"minecraft:native",target:"minecraft:other",pool:"minecraft:empty",joint:"aligned",final_state:"",selection_priority:3,placement_priority:-3}`)
		command(`setblock 4 70 4 minecraft:jigsaw[orientation=up_north]{name:"native",target:"goal",pool:"empty",selection_priority:11,placement_priority:-10}`)
		check(`if data block 4 70 4 {name:"minecraft:native",target:"minecraft:goal",pool:"minecraft:empty",joint:"rollable",final_state:"minecraft:air",selection_priority:11,placement_priority:-10}`)
		command(`setblock 4 70 4 minecraft:jigsaw[orientation=north_up]{name:"native",target:"goal",pool:"empty"}`)
		check(`if data block 4 70 4 {joint:"aligned",selection_priority:0,placement_priority:0}`)
		command(`data merge block 4 70 4 {name:"Bad Name",target:42,pool:"UPPER:bad",joint:"bad",final_state:42,selection_priority:"bad",placement_priority:"bad",unknown:1}`)
		check(`if data block 4 70 4 {name:"minecraft:empty",target:"minecraft:empty",pool:"minecraft:empty",joint:"aligned",final_state:"minecraft:air",selection_priority:0,placement_priority:0}`)
		check(`unless data block 4 70 4 unknown`)
		command(`data merge block 4 70 4 {name:"..:target",target:".:target",pool:"..:pool"}`)
		check(`if data block 4 70 4 {name:"minecraft:empty",target:".:target",pool:"minecraft:empty"}`)
		command(`data merge block 4 70 4 {name:"",target:"",pool:""}`)
		emptyPath := check(`if data block 4 70 4 {name:"minecraft:",target:"minecraft:",pool:"minecraft:"}`)
		emptyFallback := check(`unless data block 4 70 4 {name:"minecraft:empty",target:"minecraft:empty",pool:"minecraft:empty"}`)
		if !emptyPath || !emptyFallback {
			t.Errorf("unexpected empty identifier result %v %v", emptyPath, emptyFallback)
		}
		command(`setblock 4 70 4 minecraft:jigsaw[orientation=up_north]{}`)
		edit("example:after", "example:goal", "minecraft:empty", "minecraft:air", "bad_joint", 0, 0)
		check(`if data block 4 70 4 {name:"example:after",joint:"aligned"}`)
		gc, gpr, gpw := joinPlayBlackbox(t, addr, "JigsawGuest")
		defer gc.Close()
		_ = gc.SetDeadline(time.Time{})
		_ = gpw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		guest := newChatSinkTP(gpr, gpw)
		writeJigsawEditBB(t, gpw, pos, "example:guest", "example:goal", "minecraft:empty", "minecraft:diamond_block", "rollable", 4, 5)
		marker := "JG982461"
		sendCommand(t, gpw, marker)
		deadline := time.Now().Add(10 * time.Second)
		acked := false
		for time.Now().Before(deadline) && !acked {
			for _, line := range guest.take() {
				if strings.Contains(line, marker) {
					acked = true
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !acked {
			t.Fatal("guest packet barrier missing")
		}
		check(`if data block 4 70 4 {name:"example:after",joint:"aligned",final_state:"minecraft:air"}`)
		return out
	}
	want := run(tg.vanilla)
	got := run(tg.mcgo)
	t.Logf("vanilla %+v; mcgo %+v", want, got)
	expectedUpdate := []string{"string:example:entry", "string:example:door", "string:minecraft:empty", "string:minecraft:oak_stairs[facing=south,half=top]", "string:rollable", "int32:-17", "int32:2147483647"}
	if !reflect.DeepEqual(want.FirstUpdate, expectedUpdate) || !want.BlockUpdate || !want.RepeatUpdate {
		t.Errorf("vanilla editor update fixture changed: %+v", want)
	}
	for i, ok := range want.Checks {
		if !ok {
			t.Errorf("vanilla jigsaw fixture check%d failed", i)
		}
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatal("jigsaw editor state/update mismatch")
	}
}
