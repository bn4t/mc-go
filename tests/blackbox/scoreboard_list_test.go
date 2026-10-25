package blackbox

import (
	"fmt"
	"net"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestScoreboardListParity pins scoreboard's read-out surface: the
// bare/subcommand error shapes, objectives list, and players list in
// both forms — no-arg over every tracked holder (live players, fake
// names, and entities by their decorated name), and the single-entity
// form over each score. Selector targets on set/get/reset exercise
// the multi/single argument split. Replies are compared verbatim
// across vanilla and mc-go.
func TestScoreboardListParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": `[{"uuid":"14075747-e146-3809-a161-83abe512191e","name":"P","level":2,"bypassesPlayerLimit":false}]`},
		func(s *server.Server) { s.OpsRequired = true; s.SetOpLevel("P", 2) })
	defer targets.cleanup()

	setup := []string{
		`summon minecraft:pig 10 -60 10 {NoAI:1b,PersistenceRequired:1b}`,
		`summon minecraft:cow -10 -60 -10 {NoAI:1b,PersistenceRequired:1b}`,
	}
	cmds := []string{
		// Grammar errors and the empty boards.
		`scoreboard`,
		`scoreboard bogus`,
		`scoreboard objectives`,
		`scoreboard objectives bogus`,
		`scoreboard players`,
		`scoreboard players bogus`,
		`scoreboard objectives list`,
		`scoreboard players list`,
		// Objectives list, display arg, criteria and remove errors.
		`scoreboard objectives add zzsl dummy`,
		`scoreboard objectives add zzsl2 dummy {"text":"SL Two"}`,
		`scoreboard objectives list`,
		`scoreboard objectives add zzsl dummy`,
		`scoreboard objectives add zzc notacriteria`,
		`scoreboard objectives add`,
		`scoreboard objectives add zzonly`,
		`scoreboard objectives remove zzmissing`,
		`scoreboard objectives remove`,
		`scoreboard objectives remove zzsl2 extra`,
		// setdisplay slot grammar and replies.
		`scoreboard objectives setdisplay`,
		`scoreboard objectives setdisplay bogus zzsl`,
		`scoreboard objectives setdisplay sidebar nosuchobj`,
		`scoreboard objectives setdisplay sidebar zzsl`,
		`scoreboard objectives setdisplay sidebar zzsl`,
		`scoreboard objectives setdisplay sidebar`,
		`scoreboard objectives setdisplay sidebar`,
		`scoreboard objectives setdisplay below_name zzsl`,
		`scoreboard objectives setdisplay sidebar.team.red zzsl`,
		`scoreboard objectives setdisplay sidebar.team.bogus zzsl`,
		// players list with no holders, then populated.
		`scoreboard players list`,
		`scoreboard players set @s zzsl 5`,
		`scoreboard players set FakeHolder zzsl 7`,
		`scoreboard players set @s zzsl2 3`,
		`scoreboard players list`,
		// Single-entity form.
		`scoreboard players list @s`,
		`scoreboard players list P`,
		`scoreboard players list nosuchplayer`,
		`scoreboard players list zzunknown`,
		`scoreboard players list @e[type=pig]`,
		`scoreboard players list @e[type=zombie]`,
		`scoreboard players list @e[type=pig,limit=1]`,
		// Multi-target writes and the entity holder's own entry.
		`scoreboard players set @e[type=pig] zzsl 4`,
		`scoreboard players list`,
		`scoreboard players list @e[type=pig,limit=1]`,
		`scoreboard players get @e[type=pig] zzsl`,
		`scoreboard players get @a zzsl`,
		`scoreboard players get @e[type=pig,limit=1] zzsl`,
		`scoreboard players reset @e[type=pig]`,
		`scoreboard players list @e[type=pig,limit=1]`,
		`scoreboard players list`,
		// Value parse errors, unknown holders and objectives.
		`scoreboard players set @s zzsl`,
		`scoreboard players set @s zzsl abc`,
		`scoreboard players set @s zzsl 2147483648`,
		`scoreboard players set @e[type=zombie] zzsl 1`,
		`scoreboard players get nosuch zzsl`,
		`scoreboard players get FakeHolder zzsl2`,
		`scoreboard players reset nosuch zzsl`,
		`scoreboard players reset FakeHolder zzsl2`,
		`scoreboard players reset FakeHolder`,
		`scoreboard players list FakeHolder`,
		// Multi-entity set/reset.
		`scoreboard players set @e[type=!player] zzsl 8`,
		`scoreboard players list`,
		`scoreboard players reset @e[type=!player]`,
		`scoreboard players list @e[type=pig,limit=1]`,
		// add/remove/operation grammar and math.
		`scoreboard players add @s zzsl 2`,
		`scoreboard players add @e[type=!player] zzsl 1`,
		`scoreboard players remove @s zzsl 1`,
		`scoreboard players add nosuch zzsl 2`,
		`scoreboard players add @s zzmissing 2`,
		`scoreboard players add @s zzsl`,
		`scoreboard players operation @s zzsl = @s zzsl2`,
		`scoreboard players operation @s zzsl += @s zzsl2`,
		`scoreboard players operation @s zzsl bogus @s zzsl2`,
		`scoreboard players operation`,
		`scoreboard players operation @s zzsl = @s`,
		// objectives modify: displayname, displayautoupdate,
		// numberformat (blank/fixed/styled/clear) and rendertype.
		`scoreboard objectives add zztr trigger`,
		`scoreboard objectives modify`,
		`scoreboard objectives modify zzsl`,
		`scoreboard objectives modify zznosuch displayname "x"`,
		`scoreboard objectives modify zzsl bogus x`,
		`scoreboard objectives modify zzsl displayname {"text":"SL Renamed"}`,
		`scoreboard objectives modify zzsl displayname "x" extra`,
		`scoreboard objectives modify zzsl displayautoupdate true`,
		`scoreboard objectives modify zzsl displayautoupdate nope`,
		`scoreboard objectives modify zzsl rendertype hearts`,
		`scoreboard objectives modify zzsl rendertype bogus`,
		`scoreboard objectives modify zzsl numberformat`,
		`scoreboard objectives modify zzsl numberformat blank`,
		`scoreboard objectives modify zzsl numberformat fixed {"text":"pts"}`,
		`scoreboard objectives modify zzsl numberformat styled {color:"red"}`,
		`scoreboard objectives list`,
		// trigger before enable: locked and invalid objectives.
		`trigger zztr`,
		`trigger zzsl`,
		`trigger zznosuch`,
		`trigger`,
		`trigger zztr bogus`,
		`trigger zztr add`,
		`trigger zztr add abc`,
		// players enable: trigger criteria gate, locked-flag releases.
		`scoreboard players enable @s zztr`,
		`scoreboard players enable @s zztr`,
		`trigger zztr`,
		`trigger zztr add 5`,
		`trigger zztr set 9`,
		`trigger zztr set 2147483648`,
		`scoreboard players get @s zztr`,
		`scoreboard players enable @s zzsl`,
		`scoreboard players enable @s zznosuch`,
		`scoreboard players enable @e[type=zombie] zztr`,
		`scoreboard players enable @e[type=pig] zztr`,
		`scoreboard players enable @e[type=pig] zztr`,
		// players display: per-score name/numberformat overrides.
		`scoreboard players display`,
		`scoreboard players display bogus @s zzsl`,
		`scoreboard players display name @s zzsl {"text":"Score!"}`,
		`scoreboard players display name @s zzsl`,
		`scoreboard players display name @e[type=!player] zzsl {"text":"E"}`,
		`scoreboard players display name nosuch zzsl`,
		`scoreboard players display numberformat @s zzsl styled {color:"green"}`,
		`scoreboard players display numberformat @s zzsl`,
		`scoreboard players display numberformat @s zzsl bogus`,
		`scoreboard players display name @s zznosuch "x"`,
		// Trailing-arg and selector-shape errors on the entity form.
		`scoreboard players list @s extra`,
		`scoreboard players list @a`,
		`scoreboard players get @s zzmissing`,
		`scoreboard players set @s zzmissing 1`,
		`scoreboard players reset @s zzmissing`,
		// Shared single-entity toomany cursor comparison.
		`data get entity @e[type=pig]`,
		`data get entity @e[type=pig] Pos`,
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

	// normalizeListEvents reorders the wire parts whose order comes
	// from JVM hash-map iteration and so differs run to run and server
	// to server: the holder names inside players.list.success (the
	// holder keys include random entity UUIDs that hash differently on
	// each side) and the per-objective list.entity.entry lines (a
	// Reference2ObjectOpenHashMap keyed by Objective identity).
	normalizeListEvents := func(evs []string) []string {
		out := make([]string, 0, len(evs))
		var entries []string
		flush := func() {
			if len(entries) == 0 {
				return
			}
			sort.Strings(entries)
			out = append(out, entries...)
			entries = nil
		}
		for _, ev := range evs {
			if strings.Contains(ev, "players.list.entity.entry") {
				entries = append(entries, ev)
				continue
			}
			flush()
			if strings.Contains(ev, "players.list.success") {
				ev = sortJoinedNames(ev)
			}
			out = append(out, ev)
		}
		flush()
		return out
	}
	normalize := func(rows [][]string) [][]string {
		out := make([][]string, len(rows))
		for i, evs := range rows {
			out[i] = normalizeListEvents(evs)
		}
		return out
	}

	want := normalize(run(targets.vanilla))
	got := normalize(run(targets.mcgo))
	all := append(setup, cmds...)
	for i, cmd := range all {
		if fmt.Sprintf("%v", want[i]) != fmt.Sprintf("%v", got[i]) {
			t.Errorf("cmd %q:\n  vanilla %v\n  mc-go   %v", cmd, want[i], got[i])
		}
	}
}

// sortJoinedNames sorts the `{"": name}` members inside a
// players.list.success event's joined extra list, keeping the gray
// comma separators between them.
func sortJoinedNames(ev string) string {
	nameRe := regexp.MustCompile(`\{"": "([^"]*)"\}`)
	loc := strings.Index(ev, `"extra": [`)
	end := strings.Index(ev, `], "text": ""}`)
	if loc < 0 || end < 0 || end < loc {
		return ev
	}
	inner := ev[loc+len(`"extra": [`) : end]
	names := nameRe.FindAllStringSubmatch(inner, -1)
	if len(names) == 0 {
		return ev
	}
	sorted := make([]string, 0, len(names))
	for _, n := range names {
		sorted = append(sorted, n[1])
	}
	sort.Strings(sorted)
	parts := make([]string, 0, 2*len(sorted)-1)
	for i, n := range sorted {
		if i > 0 {
			parts = append(parts, `{"color": "gray", "text": ", "}`)
		}
		parts = append(parts, `{"": "`+n+`"}`)
	}
	return ev[:loc+len(`"extra": [`)] + strings.Join(parts, ", ") + ev[end:]
}
