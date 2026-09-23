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
	// truncated sticks once any container exits its loop without
	// reaching the closer — a partial reconstruction the caller
	// should retry rather than compare.
	truncated bool
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
			r.truncated = true
			return m
		}
	}
	r.truncated = true
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
			r.truncated = true
			return out
		}
	}
	r.truncated = true
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
		// A 0x08 byte inside a non-String field reads as a bogus header —
		// skip it (resync) instead of truncating the tail of the line.
		if i+3+nl+2 > len(line) {
			i++
			continue
		}
		name := line[i+3 : i+3+nl]
		vi := i + 3 + nl
		vl := int(line[vi])<<8 | int(line[vi+1])
		vi += 2
		if vi+vl > len(line) {
			i++
			continue
		}
		if nl == 0 || name == "text" {
			b.WriteString(line[vi : vi+vl])
		}
		i = vi + vl
	}
	return b.String()
}

// parseSNBT parses the SNBT value inside a `data get` chat line —
// the reconstructed reply text's first bracketed value. ok is false
// when the value never reached its closer (truncated text).
func parseSNBT(line string) (any, bool) {
	text := chatTextPieces(line)
	i := strings.IndexAny(text, "[{")
	if i < 0 {
		return nil, false
	}
	r := &snbtReader{s: text, i: i}
	v := r.value()
	return v, !r.truncated
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
			// movement_speed joins the pack on the mob's first real
			// travel — for a fresh summon that means its first move,
			// which idle animals reach only on RandomStroll's dice. A
			// tap of /damage panics the mob and lands the touch
			// deterministically on both servers.
			sendCommand(t, pw, fmt.Sprintf(
				"damage @e[type=minecraft:%s,limit=1,sort=nearest] 1", tp))
			time.Sleep(600 * time.Millisecond)
			var v any
			var ok bool
			for i := 0; i < 5; i++ {
				sink.take()
				v, ok = parseSNBT(dataGetLine(t, sink, pw,
					`@e[type=minecraft:`+tp+`,limit=1,sort=nearest]`, "attributes"))
				if v != nil && ok {
					break
				}
			}
			if !ok {
				v = nil
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
		// Passives on the z=30 row, hostiles far north on the z=60 row —
		// every mob still sits inside simulation distance but nobody
		// hunts anybody (wolf-vs-skeleton, zombie-vs-player).
		leg("summon minecraft:wolf 30.5 -60 30.5",
			"summon minecraft:pig 34.5 -60 34.5",
			"summon minecraft:zombie 38.5 -60 60.5",
			"summon minecraft:husk 42.5 -60 64.5",
			"summon minecraft:zombie_villager 46.5 -60 68.5",
			"summon minecraft:skeleton 50.5 -60 72.5")
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

		// Supplier sweep: /attribute base get materializes the
		// instance inside vanilla's lazy AttributeMap too, so the
		// save pack then exposes each type's supplier bases
		// verbatim — pinning the generated entityAttrBases table on
		// both servers.
		leg("kill @e[type=!minecraft:player]")
		sweep := []struct {
			tp    string
			attrs []string
		}{
			{"cow", []string{"movement_speed", "max_health", "tempt_range"}},
			{"horse", []string{"max_health", "jump_strength", "step_height", "safe_fall_distance"}},
			{"bee", []string{"flying_speed", "attack_damage"}},
			{"warden", []string{"attack_damage", "knockback_resistance", "follow_range"}},
			{"elder_guardian", []string{"attack_damage"}},
			{"bogged", []string{"max_health", "movement_speed"}},
			{"breeze", []string{"movement_speed", "follow_range"}},
			{"panda", []string{"attack_damage", "movement_speed"}},
			{"dolphin", []string{"movement_speed", "attack_damage"}},
			{"armadillo", []string{"movement_speed", "tempt_range"}},
			{"villager", []string{"movement_speed", "follow_range"}},
			{"witch", []string{"max_health", "attack_damage"}},
		}
		sx := 24.5
		for _, sw := range sweep {
			sx += 8
			cmds := []string{fmt.Sprintf(
				"summon minecraft:%s %.1f -60 -30.5 {NoAI:1b}", sw.tp, sx)}
			for _, a := range sw.attrs {
				cmds = append(cmds, fmt.Sprintf(
					"attribute @e[type=minecraft:%s,limit=1,sort=nearest] minecraft:%s base get",
					sw.tp, a))
			}
			leg(cmds...)
			get(sw.tp, "sweep-"+sw.tp)
		}
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
