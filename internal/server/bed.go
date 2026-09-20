package server

import (
	"strings"

	"mc-go/internal/world"
)

// isBedBlock reports whether a block state is any of the 16 beds —
// vanilla's BedBlock shares one class, keyed by name suffix.
func isBedBlock(b world.Block) bool {
	name, ok := world.StateOwner(b)
	return ok && strings.HasSuffix(name, "_bed")
}

// useBed handles a right-click on a bed. Vanilla's gate is the
// overworld sleep window (daytime ∈ [12542, 23459] mod 24000 — storms
// extend it, not modeled); inside it the player enters the bed, the
// spawn point is set ("Respawn point set" action bar), and
// slept_in_bed fires. Outside it the "no sleep" action bar shows.
func (s *Server) useBed(sess *Session, clicked world.Pos) {
	day := s.dayTime() % 24000
	if day < 12542 || day > 23459 {
		s.sendActionBar(sess, component{translate: "block.minecraft.bed.no_sleep"})
		return
	}
	sess.sleeping.Store(true)
	sess.sleepTicks.Store(0)
	s.sendActionBar(sess, component{translate: "block.minecraft.set_spawn"})
	s.fireSleptInBed(sess)
	_ = clicked
}

// sleepTickStep runs once per game tick: when every play-state session
// in a dimension has been asleep for the vanilla 100-tick sleep
// duration, the world jumps to the next morning and everyone wakes to
// the "Skipping through the night" action bar.
func (s *Server) sleepTickStep() {
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.sleeping.Load() {
			sess.sleepTicks.Add(1)
		}
		return true
	})
	// Group sleeping players by dimension — the skip is per-level.
	type dimCount struct{ sleep, awake int }
	counts := map[string]*dimCount{}
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay {
			return true
		}
		d := sess.dimension()
		c := counts[d.name]
		if c == nil {
			c = &dimCount{}
			counts[d.name] = c
		}
		if sess.sleeping.Load() && sess.sleepTicks.Load() >= 100 {
			c.sleep++
		} else {
			c.awake++
		}
		return true
	})
	for dim, c := range counts {
		if c.sleep == 0 || c.awake > 0 {
			continue
		}
		s.setDayTime((s.dayTime()/24000 + 1) * 24000)
		s.sessions.Range(func(_, v any) bool {
			sess := v.(*Session)
			if sess.state.Load() != StatePlay || sess.dimension().name != dim {
				return true
			}
			sess.sleeping.Store(false)
			sess.sleepTicks.Store(0)
			s.sendActionBar(sess, component{translate: "sleep.skipping_night"})
			return true
		})
	}
}
