package blackbox

// TestDataEntityParity pins vanilla's /data entity scope on the wire:
// the entity display-name component (raw CustomName string vs
// entity.<type> translation, UUID insertion, show_entity hover), the
// pretty-printed save compound in HashMap key order, the notfound /
// toomany / nothing_found failures with their cursor marks, and the
// post-mutation tracker resync (dirty set_entity_data entries plus the
// movement_speed update_attributes vanilla emits after every entity
// load). Paths, merges, removes, and modify ops are all byte-compared
// between vanilla and mc-go.
//
// Entity ids and UUIDs differ between runs and implementations, so raw
// packet bodies are compared with the VarInt entity id stripped and
// uuid strings masked.

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

// maskEntityVars hides the per-run UUID strings a name component or
// save map carries — both the dashed string form and the [I;w,x,y,z]
// int-array form a rendered save compound shows.
var uuidRe = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
var uuidArrRe = regexp.MustCompile(`(\{"color": "red", "text": "I"\}, \{"": ";"\}, \{"": " "\}, )(\{"color": "gold", "text": "-?\d+"\}(?:, \{"": ","\}, \{"": " "\}, )?)+`)
var uuidSNBTRe = regexp.MustCompile(`\[I;-?\d+(,-?\d+){3}\]`)

func maskUUID(s string) string {
	s = uuidRe.ReplaceAllString(s, "<uuid>")
	s = uuidArrRe.ReplaceAllString(s, "${1}<uuid-ints>")
	return uuidSNBTRe.ReplaceAllString(s, "[I;<uuid>]")
}

// stripVarInt drops a leading VarInt (entity id) from a raw packet body.
func stripVarInt(s string) string {
	i := 0
	for i < len(s) && s[i]&0x80 != 0 {
		i++
	}
	return s[i+1:]
}

func TestDataEntityParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	tag := fmt.Sprintf("probe%d", time.Now().UnixNano()%1_000_000)
	tag2 := tag + "b"
	sel := fmt.Sprintf("@e[type=minecraft:pig,tag=%s,limit=1]", tag)
	sel2 := fmt.Sprintf("@e[type=minecraft:pig,tag=%s,limit=1]", tag2)
	selAny := fmt.Sprintf("@e[tag=%s]", tag)
	cmds := []string{
		"summon minecraft:pig 9.5 -59 10.5 {NoAI:1b,CustomName:'{\"text\":\"Babe\"}',Tags:[\"" + tag + "\"]}",
		"summon minecraft:pig 12.5 -59 10.5 {NoAI:1b,Tags:[\"" + tag2 + "\"],Health:3f}",
		"data get entity " + sel,
		"data get entity " + sel + " Health",
		"data get entity " + sel + " Pos[1]",
		"data get entity " + sel + " NoAI",
		"data get entity " + sel + " CustomName",
		"data get entity " + sel + " Brain.memories",
		"data get entity " + sel + " NoSuch",
		"data get entity @e[type=minecraft:creeper,tag=" + tag + "]",
		"data merge entity " + sel + " {Health:5f}",
		"data get entity " + sel + " Health",
		"data remove entity " + sel + " CustomName",
		"data get entity " + sel + " CustomName",
		"data modify entity " + sel + " Health set value 3f",
		"data get entity " + sel + " Health",
		"data remove entity " + sel + " Health",
		"data get entity " + sel + " Health",
		"data modify entity " + sel + " Motion set value [0.5d,-0.2d,0.5d]",
		"data get entity " + sel + " Motion",
		"data modify entity " + sel + " Health set from entity " + sel2 + " Health",
		"data get entity " + sel + " Health",
		"data modify entity " + sel + " CustomName set from entity " + sel2 + " Health",
		"data get entity " + sel + " CustomName",
		"data modify entity " + sel + " Brain.memories.zzk set value 7",
		"data get entity " + sel + " Brain.memories",
		"data modify entity " + sel + " Motion prepend value 0.25d",
		"data get entity " + sel + " Motion",
		"data modify entity " + sel + " Motion insert 1 value 9d",
		"data get entity " + sel + " Motion",
		"data modify entity " + sel + " Health set string entity " + sel2 + " UUID",
		"data merge block 1 -60 1 {KeepPacked:99b}",
		"teleport @s 9.5 -59.25 10.5",
		"data get entity @s Pos",
		"data get entity @p Health",
		"data get entity @p foodLevel",
		"data merge entity @s {Health:12f}",
		"data get entity @s Health",
		"data merge entity @e[limit=2] {Health:5f}",
		"data get entity " + selAny,
		"data get entity @e[limit=2]",
		// The storage scope: a missing id reads as an empty compound,
		// writes answer commands.data.storage.modified with the bare
		// id string, and from/string sources reach across scopes.
		"data get storage zzprobe:s",
		"data merge storage zzprobe:s {a:1b,b:\"two\",c:[1,2,3]}",
		"data get storage zzprobe:s",
		"data get storage zzprobe:s c[1]",
		"data modify storage zzprobe:s a set value 9b",
		"data get storage zzprobe:s a",
		"data remove storage zzprobe:s b",
		"data get storage zzprobe:s",
		"data modify storage zzprobe:s g set from storage zzprobe:s c",
		"data get storage zzprobe:s g",
		"data modify storage zzprobe:s hp set from entity " + sel + " Health",
		"data get storage zzprobe:s hp",
		"data merge storage zzprobe:s {d:{e:[{f:5}]}}",
		"data modify storage zzprobe:s d.e[0].f set string storage zzprobe:s c[0]",
		"data get storage zzprobe:s",
		"data merge storage zzprobe:s {arr:[I;1,2]}",
		"data get storage zzprobe:s arr",
		"data modify storage zzprobe:s h set string entity @s UUID",
		"data get storage zznone:ns",
		"data remove storage zzprobe:s nosuch",
		// Kills come last so the pigs' deaths can't disturb the data
		// reads above: notfound replies, a single kill's name-comp
		// reply + pose/health death pack, and a multi kill. Tag
		// selectors — not type — pin the multi case so untagged
		// entities elsewhere in the world can't inflate the count.
		"summon minecraft:pig 14.5 -59 10.5 {NoAI:1b,Tags:[\"" + tag2 + "\"]}",
		"kill @e[type=minecraft:creeper,tag=" + tag + "]",
		"kill nosuchplayer",
		"kill " + sel,
		"kill @e[tag=" + tag2 + "]",
		"kill @e[tag=" + tag + "none]",
	}

	run := func(addr string) [][]string {
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSink(pr, pw)
		sink.cap = 1 << 20
		time.Sleep(400 * time.Millisecond)
		// Chunk-generated animals wander into the flat vanilla world
		// and emit entity packets on their own schedule. Wipe every
		// non-player entity — twice, so the first pass's drops/orbs go
		// too — and let the death animations drain before comparing.
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		time.Sleep(1400 * time.Millisecond)
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		time.Sleep(800 * time.Millisecond)
		sink.take()
		var out [][]string
		for _, cmd := range cmds {
			sink.take()
			sendCommand(t, pw, cmd)
			time.Sleep(450 * time.Millisecond)
			var evs []string
			for _, line := range sink.take() {
				// Entity-data / update_attributes bodies compare minus
				// the leading VarInt entity id.
				if strings.HasPrefix(line, "0x63:") || strings.HasPrefix(line, "0x83:") {
					body := stripVarInt(line[5:])
					// Mob kills spawn loot items and XP orbs; their
					// index-8 metadata (item stack / orb value) is rolled
					// per kill, so it can't pin anything.
					if strings.HasPrefix(body, "\x08\x01") || strings.HasPrefix(body, "\x08\x07") {
						continue
					}
					evs = append(evs, "pkt "+line[:4]+" "+maskUUID(fmt.Sprintf("%q", body)))
					continue
				}
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

// TestDataMergeHealthParity pins the Health write path of entity
// /data merge: vanilla's load clamps the merged value to
// [0, max_health] and pushes a zeroed mob through die() on the next
// tick. An untamed wolf reads Health against the loaded maximum before
// its class resets max_health to8, so max40/Health100 initially gives
// Health40 with effective max8. This unfrozen, sleep-paced fixture sees
// the later living-frame cap to8; TestWolfFrozenLoadHealthParity pins
// the distinct immediate ordering.
// Player targets stay read-only: commands.data.entity.invalid.
func TestDataMergeHealthParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	tag := fmt.Sprintf("dmh%d", time.Now().UnixNano()%1_000_000)
	wolfSel := fmt.Sprintf("@e[type=minecraft:wolf,tag=%s,limit=1]", tag)
	huskSel := fmt.Sprintf("@e[type=minecraft:husk,tag=%s,limit=1]", tag)
	cmds := []string{
		"summon minecraft:wolf 9.5 -59 10.5 {NoAI:1b,Tags:[\"" + tag + "\"]}",
		"data get entity " + wolfSel + " Health",
		"data merge entity " + wolfSel + " {Health:100f}",
		"data get entity " + wolfSel + " Health",
		"attribute " + wolfSel + " minecraft:max_health base set 40",
		"attribute " + wolfSel + " minecraft:max_health base get",
		"data merge entity " + wolfSel + " {Health:100f}",
		"data get entity " + wolfSel + " Health",
		"attribute " + wolfSel + " minecraft:max_health base get",
		"data merge entity " + wolfSel + " {Health:-5f}",
		"data get entity " + wolfSel + " Health",
		"summon minecraft:husk 9.5 -59 10.5 {NoAI:1b,Tags:[\"" + tag + "\"]}",
		"data merge entity " + huskSel + " {Health:100f}",
		"data get entity " + huskSel + " Health",
		"data merge entity " + huskSel + " {Health:0f}",
		"data get entity " + huskSel + " Health",
		"data merge entity @s {Health:5f}",
		"data get entity @s Health",
	}

	run := func(addr string) [][]string {
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSink(pr, pw)
		sink.cap = 1 << 20
		time.Sleep(400 * time.Millisecond)
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		time.Sleep(1400 * time.Millisecond)
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		time.Sleep(800 * time.Millisecond)
		sink.take()
		var out [][]string
		for i, cmd := range cmds {
			sink.take()
			sendCommand(t, pw, cmd)
			// The zero-health legs die and linger twenty ticks; give
			// them longer so the notfound read sees the corpse gone.
			wait := 450
			if i == 9 || i == 14 {
				wait = 1500
			}
			time.Sleep(time.Duration(wait) * time.Millisecond)
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
