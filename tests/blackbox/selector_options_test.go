//go:build blackbox

package blackbox

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestSelectorOptionsParity pins the deep selector semantics measured
// on vanilla 26.2: the option registry (unknown vs valueless vs
// inapplicable), per-option error keys and cursors, distance
// zero-bound dropping, the dx/dy/dz volume as a bounding-box
// intersection (positive extent includes +1), per-axis x/y/z
// re-anchoring, @s's limit/sort ban, @n, and the overridable
// p/r/n sort/limit defaults. Replies are compared verbatim across
// vanilla and mc-go.
func TestSelectorOptionsParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": `[{"uuid":"14075747-e146-3809-a161-83abe512191e","name":"P","level":2,"bypassesPlayerLimit":false}]`},
		func(s *server.Server) { s.OpsRequired = true; s.SetOpLevel("P", 2) })
	defer targets.cleanup()

	setup := []string{
		"summon minecraft:pig 10 -60 10 {NoAI:1b,PersistenceRequired:1b}",
		"summon minecraft:husk 20 -60 20 {NoAI:1b,PersistenceRequired:1b}",
		"summon minecraft:cow -10 -60 -10 {NoAI:1b,PersistenceRequired:1b}",
		"tp Q 40 -60 0",
		"tp @s 0 -60 0",
		"scoreboard objectives add zzso dummy",
		"scoreboard players set @s zzso 9",
		"team add zteam",
		"team join zteam @s",
	}
	cmds := []string{
		// Option registry: unknown name, known name without =, bare =.
		`execute if entity @e[foo=bar]`,
		`execute if entity @e[foo]`,
		`execute if entity @e[=x]`,
		`execute if entity @e[x]`,
		`execute if entity @e[limit]`,
		// Single-use repeats and the all-negated type rule.
		`execute if entity @e[type=pig,type=cow]`,
		`execute if entity @e[type=!pig,type=!cow]`,
		`execute if entity @e[type=!pig,type=cow]`,
		`execute if entity @e[type=pig,type=!cow]`,
		`execute if entity @e[limit=1,limit=2]`,
		`execute if entity @e[gamemode=creative,gamemode=survival]`,
		`execute if entity @e[name=P,name=Q]`,
		`execute if entity @e[team=zteam,team=zteam]`,
		`execute if entity @e[distance=1,distance=2]`,
		`execute if entity @e[x=0,x=1]`,
		`execute if entity @e[x_rotation=0,x_rotation=1]`,
		// Repeatable options do not trip it.
		`execute if entity @e[tag=zzno,tag=zzno2]`,
		`execute if entity @e[nbt={},nbt={}]`,
		`execute if entity @e[scores={},scores={}]`,
		`execute if entity @e[predicate=x,predicate=y]`,
		`execute if entity @e[advancements={},advancements={}]`,
		// type values.
		`execute if entity @e[type=nosuchmob]`,
		`execute if entity @e[type=Zombie]`,
		`execute if entity @e[type=]`,
		`execute if entity @e[type=!]`,
		`execute if entity @e[type=zombie!]`,
		`execute if entity @e[type=minecraft:nosuchmob]`,
		`execute if entity @e[type=#zombies]`,
		`execute if entity @e[type=!#zombies]`,
		// gamemode / sort / limit values.
		`execute if entity @e[gamemode=bad]`,
		`execute if entity @e[gamemode=!bad]`,
		`execute if entity @e[sort=bad]`,
		`execute if entity @e[limit=x]`,
		`execute if entity @e[limit=0]`,
		`execute if entity @e[limit=-1]`,
		`execute if entity @e[limit=2]`,
		// Doubles and ranges.
		`execute if entity @e[x=abc]`,
		`execute if entity @e[dx=abc]`,
		`execute if entity @e[distance=..]`,
		`execute if entity @e[distance=abc]`,
		`execute if entity @e[distance=5..3]`,
		`execute if entity @e[distance=-1]`,
		`execute if entity @e[distance=0]`,
		`execute if entity @e[distance=..0]`,
		`execute if entity @e[distance=..15]`,
		`execute if entity @e[distance=15..]`,
		`execute if entity @e[distance=14.2..]`,
		`execute if entity @e[level=5..3]`,
		`execute if entity @e[level=abc]`,
		`execute if entity @e[x_rotation=abc]`,
		`execute if entity @e[x_rotation=200..100]`,
		// scores inner errors.
		`execute if entity @e[scores={bad}]`,
		`execute if entity @e[scores={zzso=x}]`,
		`execute if entity @e[scores={zzso=}]`,
		`execute if entity @e[scores={zzso=1..0}]`,
		`execute if entity @e[scores={zzso=9}]`,
		`execute if entity @e[scores={nosuch=0..}]`,
		// advancements inner errors / match.
		`execute if entity @e[advancements={story/root=x}]`,
		`execute if entity @e[advancements={story/root=true}]`,
		`execute if entity @e[advancements={story/root=false}]`,
		// nbt / predicate.
		`execute if entity @e[nbt=notnbt]`,
		`execute if entity @e[nbt={bad]`,
		`execute if entity @e[predicate=#bad]`,
		`execute if entity @e[predicate=nosuch]`,
		// @s restrictions and acceptance.
		`execute if entity @s[limit=1]`,
		`execute if entity @s[sort=nearest]`,
		`execute if entity @s[type=player]`,
		`execute if entity @s[distance=..5]`,
		`execute if entity @s[gamemode=creative]`,
		`execute if entity @s[name=P]`,
		`execute if entity @s[team=zteam]`,
		`execute if entity @s[scores={zzso=9}]`,
		`execute if entity @s[x=0]`,
		`execute if entity @s[x_rotation=0]`,
		// @n and the overridable p/r/n defaults.
		`execute if entity @n`,
		`execute if entity @n[limit=2]`,
		`execute if entity @n[distance=..100]`,
		`execute if entity @n[sort=furthest]`,
		`execute if entity @p[limit=2]`,
		`execute if entity @r[limit=2]`,
		`execute if entity @p[sort=furthest]`,
		`execute if entity @r[sort=furthest]`,
		`execute if entity @e[sort=nearest,limit=2]`,
		// Volume: +1 extent on positive deltas, bbox intersects.
		`execute if entity @e[x=10,z=10,dx=0,dy=2,dz=0]`,
		`execute if entity @e[x=11,z=11,dx=0,dy=2,dz=0]`,
		`execute if entity @e[x=9,z=9,dx=0,dy=2,dz=0]`,
		`execute if entity @e[x=10.5,z=10.5,dx=-1,dy=2,dz=-1]`,
		`execute if entity @e[x=11.4,z=11.4,dx=-1,dy=2,dz=-1]`,
		`execute if entity @e[dx=100,dy=5,dz=100]`,
		`execute if entity @e[x=40,z=0,dx=0,dy=2,dz=0]`,
		`execute if entity @e[x=0,y=-60,z=0,dx=0.4,dy=0.9,dz=0.4]`,
		// x/y/z alone re-anchor, no box.
		`execute if entity @e[x=50]`,
		`execute if entity @e[y=0]`,
		`execute if entity @e[x=10,z=10]`,
		// name / tag / team matching.
		`execute if entity @a[name=P]`,
		`execute if entity @a[name='P']`,
		`execute if entity @a[name="P"]`,
		`execute if entity @a[name=p]`,
		`execute if entity @a[name=!P]`,
		`execute if entity @e[tag=]`,
		`execute if entity @e[tag=zzno]`,
		`execute if entity @e[team=]`,
		`execute if entity @e[team=!]`,
		`execute if entity @e[team=zteam]`,
		// gamemode/level are players-only.
		`execute if entity @e[type=husk,gamemode=!survival]`,
		`execute if entity @e[type=husk,level=..0]`,
		// Empty brackets and trailing comma.
		`execute if entity @e[]`,
		`execute if entity @e[type=pig,]`,
		// Rotations: P gets yaw=90 pitch=30 then matches.
		`tp @s 0 -60 0 90 30`,
		`execute if entity @s[y_rotation=90]`,
		`execute if entity @s[x_rotation=30]`,
		`execute if entity @s[y_rotation=0]`,
		`tp @s 0 -60 0 0 0`,
		// Single-entity args reject multi-match selectors.
		`data get entity @e[type=!player] Pos`,
		// tp's sibling-arg merge reports unknown.argument.
		`tp @e[type=zombie,type=zombie] Q`,
		// Type tags and scoreboard names.
		`kill @e[type=husk,distance=..50]`,
		`execute if entity @e[type=husk]`,
		`kill @e[type=#zombies]`,
	}

	run := func(addr string) [][]string {
		var c net.Conn
		var pw *protocol.Writer
		var sink *chatSink
		rejoin := func() {
			if c != nil {
				c.Close()
			}
			var pr *protocol.Reader
			c, pr, pw = joinPlayQuiet(t, addr, "P")
			_ = c.SetDeadline(time.Time{})
			sink = newChatSink(pr, pw)
			sink.cap = 1 << 20
			time.Sleep(2 * time.Second)
		}
		rejoin()
		defer c.Close()
		cQ, _, _ := joinPlayQuiet(t, addr, "Q")
		defer cQ.Close()
		time.Sleep(1500 * time.Millisecond)

		send := func(cmd string) error {
			var b protocol.Buffer
			b.String(cmd)
			return pw.WritePacket(server.IDPlayChatCommandSB, b.Bytes())
		}
		collect := func(marker string) []string {
			var evs []string
			end := time.Now().Add(5 * time.Second)
			for time.Now().Before(end) {
				for _, line := range sink.take() {
					if strings.Contains(line, marker) {
						return evs
					}
					var ev string
					if tree, ok := componentTree(line, true); ok {
						ev = tree
					} else if strings.HasPrefix(line, "0x79:") {
						i2 := strings.Index(line, ":")
						if root, err := nbt.ReadUnnamed(strings.NewReader(line[i2+1:])); err == nil {
							ev = renderNBT(root, true)
						}
					}
					if ev != "" {
						evs = append(evs, maskUUID(ev))
					}
				}
				time.Sleep(30 * time.Millisecond)
			}
			return append(evs, "<TIMEOUT>")
		}

		var out [][]string
		for i, cmd := range append(setup, cmds...) {
			var evs []string
			for attempt := 0; attempt < 4; attempt++ {
				sink.take()
				if err := send(cmd); err != nil {
					rejoin()
					continue
				}
				time.Sleep(160 * time.Millisecond)
				if err := send(fmt.Sprintf("me ZZ%d", i)); err != nil {
					rejoin()
					continue
				}
				evs = collect(fmt.Sprintf("ZZ%d", i))
				if len(evs) > 0 && evs[len(evs)-1] == "<TIMEOUT>" {
					rejoin()
					continue
				}
				break
			}
			out = append(out, evs)
		}
		return out
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	all := append(setup, cmds...)
	for i, cmd := range all {
		if fmt.Sprintf("%v", want[i]) != fmt.Sprintf("%v", got[i]) {
			t.Errorf("cmd %q:\n  vanilla %v\n  mc-go   %v", cmd, want[i], got[i])
		}
	}
}
