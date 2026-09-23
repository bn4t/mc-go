//go:build blackbox

package blackbox

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

// snbtValue is a minimal reader for the SNBT payload inside a
// `data get` chat line — compounds, lists, quoted/bare strings, and
// numbers with the f/d/b/s/l suffixes.
type snbtReader struct {
	s string
	i int
}

func (r *snbtReader) ws() {
	for r.i < len(r.s) && (r.s[r.i] == ' ' || r.s[r.i] == '\t') {
		r.i++
	}
}

func (r *snbtReader) peek() byte {
	r.ws()
	if r.i >= len(r.s) {
		return 0
	}
	return r.s[r.i]
}

func (r *snbtReader) value() any {
	switch c := r.peek(); {
	case c == '{':
		return r.compound()
	case c == '[':
		return r.list()
	case c == '"' || c == '\'':
		return r.quoted()
	default:
		return r.bare()
	}
}

func (r *snbtReader) compound() map[string]any {
	m := map[string]any{}
	r.i++ // {
	for r.i < len(r.s) && len(m) < 4096 {
		if r.peek() == '}' {
			r.i++
			return m
		}
		before := r.i
		var key string
		if c := r.peek(); c == '"' || c == '\'' {
			key = r.quoted()
		} else {
			start := r.i
			for r.i < len(r.s) && r.s[r.i] != ':' && r.s[r.i] != ' ' && r.s[r.i] != '\t' {
				r.i++
			}
			key = r.s[start:r.i]
		}
		r.ws()
		if r.i < len(r.s) && r.s[r.i] == ':' {
			r.i++
		}
		m[key] = r.value()
		if r.peek() == ',' {
			r.i++
		}
		if r.i <= before {
			return m
		}
	}
	return m
}

func (r *snbtReader) list() []any {
	var out []any
	r.i++ // [
	for r.i < len(r.s) && len(out) < 4096 {
		if r.peek() == ']' {
			r.i++
			return out
		}
		before := r.i
		out = append(out, r.value())
		if r.peek() == ',' {
			r.i++
		}
		if r.i <= before {
			return out
		}
	}
	return out
}

func (r *snbtReader) quoted() string {
	q := r.s[r.i]
	r.i++
	var b strings.Builder
	for r.i < len(r.s) && r.s[r.i] != q {
		b.WriteByte(r.s[r.i])
		r.i++
	}
	r.i++ // closing quote
	return b.String()
}

func (r *snbtReader) bare() any {
	start := r.i
	for r.i < len(r.s) && !strings.ContainsRune(",}] \t", rune(r.s[r.i])) {
		r.i++
	}
	tok := r.s[start:r.i]
	if tok == "true" {
		return true
	}
	if tok == "false" {
		return false
	}
	if v, err := strconv.ParseFloat(strings.TrimRight(tok, "bdfslBDFSL"), 64); err == nil {
		if _, e2 := strconv.Atoi(tok); e2 != nil {
			// keep ints as ints only when unprefixed digits — every
			// number that carried a suffix lands here as float.
			_ = v
		}
		return v
	}
	return tok
}

// parseSNBT finds the first '[' or '{' in the line and parses the
// balanced value from there.
// chatTextPieces rebuilds the rendered text of a `data get` reply:
// the raw component serializes every styled SNBT token as its own
// NBT `text` string field (tag 0x08, name "text", u16 length, bytes),
// so concatenating those fields in wire order reconstructs the SNBT.
func chatTextPieces(line string) string {
	var b strings.Builder
	// Scan every TAG_String field (0x08, u16 nameLen, name, u16
	// valueLen, value); keep the ones the SNBT renderer uses — name
	// "text" for keywords/values and empty name for punctuation.
	i := 0
	for i+3 <= len(line) {
		if line[i] != 0x08 {
			i++
			continue
		}
		nl := int(line[i+1])<<8 | int(line[i+2])
		if i+3+nl+2 > len(line) {
			break
		}
		name := line[i+3 : i+3+nl]
		vi := i + 3 + nl
		vl := int(line[vi])<<8 | int(line[vi+1])
		vi += 2
		if vi+vl > len(line) {
			break
		}
		if nl == 0 || name == "text" {
			b.WriteString(line[vi : vi+vl])
		}
		i = vi + vl
	}
	return b.String()
}

// parseSNBT parses the SNBT value inside a `data get` chat line —
// the reconstructed reply text's first bracketed value.
func parseSNBT(line string) any {
	text := chatTextPieces(line)
	i := strings.IndexAny(text, "[{")
	if i < 0 {
		return nil
	}
	r := &snbtReader{s: text, i: i}
	return r.value()
}

// normAttrPack turns a `data get ... attributes` payload into a
// canonical, comparable form: entries sorted by id, modifiers sorted
// by id, and the randomized values masked — the triangle
// random_spawn_bonus amounts and the spawn_reinforcements base roll.
func normAttrPack(v any) []string {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, it := range list {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		id, _ := m["id"].(string)
		base := "?"
		switch {
		case strings.HasSuffix(id, ":spawn_reinforcements"):
			base = "RAND"
		default:
			base = fmt.Sprintf("%v", m["base"])
		}
		var mods []string
		if ml, ok := m["modifiers"].([]any); ok {
			for _, mv := range ml {
				mm, ok := mv.(map[string]any)
				if !ok {
					continue
				}
				mid, _ := mm["id"].(string)
				op, _ := mm["operation"].(string)
				amt := fmt.Sprintf("%v", mm["amount"])
				if strings.HasSuffix(mid, ":random_spawn_bonus") {
					amt = "RAND"
				}
				mods = append(mods, mid+"="+amt+"/"+op)
			}
			sort.Strings(mods)
		}
		ent := id + "|" + base
		if len(mods) > 0 {
			ent += "|mods{" + strings.Join(mods, ",") + "}"
		}
		out = append(out, ent)
	}
	sort.Strings(out)
	return out
}

// TestAttrSavePackParity: `data get entity X attributes` packs only
// the touched AttributeInstances — vanilla's lazy AttributeMap holds
// movement_speed for every summoned mob, max_health on the tameable
// wolf, and the non-NoAI finalizeSpawn set (follow_range +
// random_spawn_bonus, zombie-family reinforcement rolls). /attribute
// writes and `data merge` attribute lists join the pack too.
func TestAttrSavePackParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P", "Q"),
	}, nil)
	defer targets.cleanup()

	run := func(addr string) []string {
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSink(pr, pw)
		sink.cap = 1 << 20
		time.Sleep(500 * time.Millisecond)
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		sendCommand(t, pw, "gamerule do_mob_spawning false")
		time.Sleep(1200 * time.Millisecond)
		sink.take()
		var out []string
		emit := func(tag string, v any) {
			out = append(out, tag+"="+strings.Join(normAttrPack(v), ";"))
		}
		get := func(tp, tag string) {
			// A stray line occasionally buries the reply before the
			// say-ZEND flush arrives — retry the query once rather
			// than compare an empty leg.
			var v any
			for i := 0; i < 3; i++ {
				sink.take()
				v = parseSNBT(dataGetLine(t, sink, pw,
					`@e[type=minecraft:`+tp+`,limit=1,sort=nearest]`, "attributes"))
				if v != nil {
					break
				}
			}
			emit(tag, v)
		}
		leg := func(cmds ...string) {
			for _, cmd := range cmds {
				func() { defer func() { recover() }(); sendCommand(t, pw, cmd) }()
			}
			time.Sleep(700 * time.Millisecond)
		}

		// NBT summons (any non-empty tag) skip finalizeSpawn: the pack
		// is just movement_speed plus tamable max_health.
		leg("summon minecraft:wolf 0.5 -60 0.5 {NoAI:1b}",
			"summon minecraft:pig 4.5 -60 4.5 {NoAI:1b}",
			"summon minecraft:zombie 8.5 -60 8.5 {NoAI:1b}",
			"summon minecraft:husk 12.5 -60 12.5 {Tags:[a]}")
		get("wolf", "wolf-noai")
		get("pig", "pig-noai")
		get("zombie", "zombie-noai")
		get("husk", "husk-tags")

		// Bare summons (no NBT) run Mob.finalizeSpawn: follow_range +
		// random_spawn_bonus where the supplier has it, plus the
		// zombie family's reinforcement/knockback rolls.
		leg("kill @e[type=!minecraft:player]")
		// Passive mobs parked far from the hostiles — a loose wolf
		// hunts skeletons, zombies hunt the player, and a get after a
		// death is a flake.
		leg("summon minecraft:wolf 130.5 -60 30.5",
			"summon minecraft:pig 134.5 -60 34.5",
			"summon minecraft:zombie 38.5 -60 38.5",
			"summon minecraft:husk 42.5 -60 42.5",
			"summon minecraft:zombie_villager 46.5 -60 46.5",
			"summon minecraft:skeleton 50.5 -60 50.5")
		get("wolf", "wolf-ai")
		get("pig", "pig-ai")
		get("zombie", "zombie-ai")
		get("husk", "husk-ai")
		get("zombie_villager", "zv-ai")
		get("skeleton", "skeleton-ai")

		// /attribute writes add the instance to the pack.
		leg("attribute @e[type=minecraft:pig,limit=1] minecraft:armor base set 7",
			"attribute @e[type=minecraft:pig,limit=1] minecraft:movement_speed modifier add minecraft:test_boost 0.4 add_multiplied_base")
		get("pig", "pig-attrcmd")

		// data merge of an attributes list applies and joins it.
		leg(`data merge entity @e[type=minecraft:pig,limit=1] {attributes:[{id:"minecraft:jump_strength",base:0.9d},{id:"minecraft:armor",base:2.0d,modifiers:[{id:"minecraft:m1",amount:3.0d,operation:"add_value"}]}]}`)
		get("pig", "pig-merge")

		// Modifier remove keeps the instance touched.
		leg("attribute @e[type=minecraft:pig,limit=1] minecraft:movement_speed modifier remove minecraft:test_boost")
		get("pig", "pig-modrm")
		return out
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("leg %d:\n vanilla: %s\n mc-go  : %s", i, want[i], got[i])
		}
	}
}
