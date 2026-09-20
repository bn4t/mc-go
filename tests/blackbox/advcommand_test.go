package blackbox

// TestAdvancementCommandParity runs /advancement grant|revoke against
// both servers and diffs the wire: def/progress/removed sets inside
// update_advancements plus the system-chat replies (reply translatables
// and per-advancement announce chats, compared as multisets — entry
// order inside a flush is HashMap iteration order on vanilla).
//
// Commands exercised: only (whole adv, criterion, already-held
// criterion), revoke only, from, until, through, everything at the end
// (vacuously — the full grant+revoke leg already established the
// emission shapes; everything exists to check the count args).

import (
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestAdvancementCommandParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("AdvCmd"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
	})
	defer targets.cleanup()

	steps := []string{
		"advancement grant @s only minecraft:adventure/kill_a_mob",
		"advancement grant @s only minecraft:adventure/kill_a_mob", // repeat → failure
		"advancement grant @s only minecraft:adventure/kill_all_mobs minecraft:zombie",
		"advancement revoke @s only minecraft:adventure/kill_a_mob minecraft:zombie",
		"advancement grant @s from minecraft:adventure/kill_a_mob",
		"advancement revoke @s only minecraft:adventure/kill_all_mobs",
		"advancement grant @s until minecraft:adventure/kill_a_mob",
		"advancement grant @s through minecraft:adventure/kill_a_mob",
		"advancement revoke @s from minecraft:adventure/kill_a_mob",
		"advancement grant @s only minecraft:adventure/nope",
		"advancement grant @s only minecraft:adventure/kill_a_mob minecraft:nope",
		"advancement grant @s only minecraft:adventure/kill_a_mob",
		"advancement revoke @s everything",
	}

	run := func(addr string) []*advFlush {
		c, pr, pw := joinPlayBlackbox(t, addr, "AdvCmd")
		defer c.Close()
		// client() left a 10s dial-stage deadline armed; the command
		// loop runs far longer. advCollect manages read deadlines
		// itself once writes are unbounded.
		_ = c.SetDeadline(time.Time{})
		var legs []*advFlush
		drain := func(d time.Duration) {
			f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
			advCollect(t, c, pr, pw, d, f)
			legs = append(legs, f)
		}
		drain(1500 * time.Millisecond) // join burst
		for _, cmd := range steps {
			sendCommand(t, pw, cmd)
			drain(1400 * time.Millisecond)
		}
		return legs
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)

	union := func(legs []*advFlush) *advFlush {
		u := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		for _, f := range legs {
			for id, e := range f.defs {
				u.defs[id] = e
			}
			for id, crits := range f.prog {
				if u.prog[id] == nil {
					u.prog[id] = map[string]bool{}
				}
				for c, ob := range crits {
					u.prog[id][c] = u.prog[id][c] || ob
				}
			}
			for id := range f.removed {
				u.removed[id] = true
			}
			u.chats = append(u.chats, f.chats...)
		}
		return u
	}
	if diffs := diffAdvFlush(union(want), union(got)); len(diffs) > 0 {
		t.Fatalf("advancement command parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
