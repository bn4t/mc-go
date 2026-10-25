package blackbox

import (
	"fmt"
	"testing"
	"time"

	"mc-go/internal/server"
)

// TestSelectorArgsParity pins the full @[...] argument set — gamemode,
// level, scores, team, advancements, predicate, nbt partial match,
// rotations (bounds wrapped with wrapDegrees), volume (dx/dy/dz box
// normalized both directions), entity-type tags, name quoting and
// negation, repeatable/single option rules, and the implicit orderings
// (@e arbitrary, @p nearest) — on the wire, replies compared verbatim
// against both servers. Every key name in an error reply is the real
// server's (e.g. argument.entity.options.mode.invalid,
// argument.entity.options.inapplicable, argument.range.empty).
func TestSelectorArgsParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	tag := fmt.Sprintf("szp%d", time.Now().UnixNano()%1_000_000)
	cmds := []string{
		"summon minecraft:pig 9.5 -59 10.5 {NoAI:1b,Tags:[\"" + tag + "\"]}",
		"scoreboard objectives add zzso dummy",
		"scoreboard players set @s zzso 9",
		"team add zteam",
		"team join zteam @s",
		// pass/fail semantics per arg (pig vs player)
		"execute if entity @e[type=minecraft:pig,tag=" + tag + ",gamemode=!survival]",
		"execute if entity @e[type=minecraft:pig,tag=" + tag + ",gamemode=survival]",
		"execute if entity @e[gamemode=survival]",
		"execute if entity @e[gamemode=creative]",
		"execute if entity @e[type=minecraft:pig,tag=" + tag + ",level=..0]",
		"execute if entity @e[type=minecraft:pig,tag=" + tag + ",level=1..]",
		"execute if entity @e[type=minecraft:pig,tag=" + tag + ",scores={zzso=..0}]",
		"execute if entity @e[type=minecraft:pig,tag=" + tag + ",scores={zzso=1..}]",
		"execute if entity @e[scores={zzso=9}]",
		"execute if entity @e[type=minecraft:pig,tag=" + tag + ",team=]",
		"execute if entity @e[type=minecraft:pig,tag=" + tag + ",team=!]",
		"execute if entity @e[team=zteam]",
		"execute if entity @e[type=minecraft:pig,tag=" + tag + ",team=!zteam]",
		"execute if entity @e[type=minecraft:pig,tag=" + tag + ",advancements={minecraft:story/root=false}]",
		"execute if entity @e[type=minecraft:pig,tag=" + tag + ",advancements={minecraft:story/root=true}]",
		"execute if entity @e[advancements={nosuch:x=false}]",
		"execute if entity @e[type=minecraft:pig,tag=" + tag + ",predicate=nosuch:x]",
		// nbt partial-match semantics
		"execute if entity @e[type=minecraft:pig,tag=" + tag + ",nbt={NoAI:1b}]",
		"execute if entity @e[type=minecraft:pig,tag=" + tag + ",nbt={NoAI:0b}]",
		"execute if entity @e[type=minecraft:pig,tag=" + tag + ",nbt=!{NoAI:1b}]",
		"execute if entity @e[type=minecraft:pig,tag=" + tag + ",nbt={Health:10}]",
		"execute if entity @e[type=minecraft:pig,tag=" + tag + ",nbt={Health:10.0f}]",
		"execute if entity @e[type=minecraft:pig,tag=" + tag + ",nbt={Tags:[\"" + tag + "\"]}]",
		"execute if entity @e[type=minecraft:pig,tag=" + tag + ",nbt={Tags:[\"zznosuch\"]}]",
		"execute if entity @e[type=minecraft:pig,tag=" + tag + ",nbt={Health:10.0f,NoAI:1b}]",
		// rotations
		"execute if entity @e[x_rotation=..90]",
		"execute if entity @e[x_rotation=0]",
		"execute if entity @e[y_rotation=-180..180]",
		// volume
		"execute if entity @e[type=minecraft:pig,tag=" + tag + ",x=9.5,y=-59,z=10.5,dx=0.4,dy=1,dz=0.4]",
		"execute if entity @e[type=minecraft:pig,tag=" + tag + ",x=9.5,y=-59,z=10.5,dx=-1,dy=1,dz=-1]",
		"execute if entity @e[type=minecraft:pig,tag=" + tag + ",dx=100,dy=100,dz=100]",
		// type entries: include set + negation + tags
		"execute if entity @e[tag=" + tag + ",type=minecraft:pig,type=minecraft:zombie]",
		"execute if entity @e[type=minecraft:zombie,type=!minecraft:zombie]",
		"execute if entity @e[type=#minecraft:skeletons]",
		"execute if entity @e[type=#nosuch:tag]",
		// name quoting / negation on players
		"execute if entity @a[name=P]",
		"execute if entity @a[name=\"P\"]",
		"execute if entity @a[name=!P]",
		"execute if entity @a[name=nosuchname]",
		// default @e ordering vs nearest (two pigs, different distances)
		"summon minecraft:pig 30 -59 30 {NoAI:1b,Tags:[\"" + tag + "\"],CustomName:'{\"text\":\"FAR\"}'}",
		"data get entity @e[tag=" + tag + ",limit=1] CustomName",
		"data get entity @e[tag=" + tag + ",sort=furthest,limit=1] CustomName",
		"data get entity @e[tag=" + tag + ",sort=nearest,limit=1] CustomName",
		"data get entity @e[tag=" + tag + ",limit=1] Pos[0]",
		"execute if entity @e[y_rotation=0]",
		"execute if entity @e[y_rotation=90..]",
		"execute if entity @e[x_rotation=..-1]",
		"execute if entity @e[tag=" + tag + ",dx=0,dy=0,dz=0]",
		"execute if entity @a[sort=arbitrary]",
		"kill @e[type=minecraft:pig]",
	}

	run := func(addr string) [][]string {
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSink(pr, pw)
		sink.cap = 1 << 20
		time.Sleep(400 * time.Millisecond)
		var out [][]string
		for _, cmd := range cmds {
			sink.take()
			sendCommand(t, pw, cmd)
			time.Sleep(450 * time.Millisecond)
			var evs []string
			for _, line := range sink.take() {
				if tree, ok := componentTree(line, true); ok {
					evs = append(evs, "chat "+maskUUID(tree))
				}
			}
			out = append(out, evs)
		}
		return out
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	for i, cmd := range cmds {
		if fmt.Sprintf("%v", want[i]) != fmt.Sprintf("%v", got[i]) {
			t.Errorf("cmd %q:\n  vanilla %v\n  mc-go   %v", cmd, want[i], got[i])
		}
	}
}
