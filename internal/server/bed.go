package server

import (
	"strings"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// isBedBlock reports whether a block state is any of the 16 beds —
// vanilla's BedBlock shares one class, keyed by name suffix.
func isBedBlock(b world.Block) bool {
	name, ok := world.StateOwner(b)
	return ok && strings.HasSuffix(name, "_bed")
}

// bedFacingRel steps pos one block in a bed's facing direction —
// the direction from the foot half toward the head half.
func bedFacingRel(pos world.Pos, facing string) world.Pos {
	switch facing {
	case "north":
		pos.Z--
	case "south":
		pos.Z++
	case "west":
		pos.X--
	case "east":
		pos.X++
	}
	return pos
}

// useBed handles a right-click on a bed. Vanilla's chain is
// BedBlock.useWithoutItem: a foot click redirects to the head and a
// missing head consumes silently; outside the overworld sleep window
// (daytime mod 24000 in [12542, 23459]) the "no sleep" action bar
// shows; an occupied head shows "bed.occupied"; otherwise the player
// enters the bed — spawn point set (system chat, not overlay), the
// arm-swing animation broadcast, entity metadata (pose SLEEPING +
// sleeping_pos head position), the bed halves' occupied flag flipped
// (riding the section-blocks-update flush), and slept_in_bed fires.
func (s *Server) useBed(sess *Session, clicked world.Pos) {
	d := sess.dimension()
	cur := d.w.GetBlock(clicked)
	facing, _ := world.StateProperty(cur, "facing")
	part, _ := world.StateProperty(cur, "part")
	head, foot := clicked, clicked
	if part == "foot" {
		head = bedFacingRel(clicked, facing)
	} else {
		foot = bedFacingRel(clicked, bedFacingOpposite(facing))
	}
	headB := d.w.GetBlock(head)
	footB := d.w.GetBlock(foot)
	if !isBedBlock(headB) || !isBedBlock(footB) {
		return
	}
	swing := func() {
		// Vanilla emits the arm-swing animation twice for a consumed
		// useWithoutItem — once from the interaction and once from the
		// result echo; both ride EntityAnimation to trackers+self.
		s.broadcastEntityAnimation(nil, sess.entityID.Load(), animSwingMainArm)
		s.broadcastEntityAnimation(nil, sess.entityID.Load(), animSwingMainArm)
	}
	day := s.dayTime() % 24000
	if day < 12542 || day > 23459 {
		s.sendActionBar(sess, component{translate: "block.minecraft.bed.no_sleep"})
		swing()
		return
	}
	if occ, _ := world.StateProperty(headB, "occupied"); occ == "true" {
		s.sendActionBar(sess, component{translate: "block.minecraft.bed.occupied"})
		swing()
		return
	}
	sess.sleeping.Store(true)
	sess.sleepTicks.Store(0)
	sess.sleepingPos.Store(head)
	// "Respawn point set" arrives as system chat with overlay=false in
	// 26.2 — not an action bar — while the skip notice is an action bar.
	s.sendSystemChatComponent(sess, component{translate: "block.minecraft.set_spawn"})
	swing()
	s.broadcastSleepMeta(sess, head, true)
	s.setBedOccupied(d, foot, head, true)
	s.fireSleptInBed(sess)
}

// bedFacingOpposite flips a bed facing across the axis.
func bedFacingOpposite(facing string) string {
	switch facing {
	case "north":
		return "south"
	case "south":
		return "north"
	case "west":
		return "east"
	case "east":
		return "west"
	}
	return facing
}

// setBedOccupied flips the occupied property on both bed halves and
// records them as changed — the tick-end flush then emits the
// section_blocks_update vanilla sends when a sleeper enters or leaves.
func (s *Server) setBedOccupied(d *dimension, foot, head world.Pos, occupied bool) {
	val := "false"
	if occupied {
		val = "true"
	}
	for _, p := range []world.Pos{foot, head} {
		b := d.w.GetBlock(p)
		nb, ok := world.WithProperty(b, "occupied", val)
		if !ok || nb == b {
			continue
		}
		d.w.SetBlock(p, nb)
		s.broadcastBlockState(d, p, nb)
	}
}

// stopSleeping wakes one session: pose back to standing, sleeping_pos
// cleared, and the bed halves freed — vanilla's stopSleepInBed without
// the position restore (a standing position pick is a GOAL gap).
func (s *Server) stopSleeping(sess *Session) {
	if !sess.sleeping.Load() {
		return
	}
	sess.sleeping.Store(false)
	sess.sleepTicks.Store(0)
	h, _ := sess.sleepingPos.Load().(world.Pos)
	sess.sleepingPos.Store(world.Pos{})
	s.broadcastSleepMeta(sess, world.Pos{}, false)
	d := sess.dimension()
	headB := d.w.GetBlock(h)
	if !isBedBlock(headB) {
		return
	}
	facing, _ := world.StateProperty(headB, "facing")
	s.setBedOccupied(d, bedFacingRel(h, bedFacingOpposite(facing)), h, false)
}

// broadcastSleepMeta writes the two-field entity metadata vanilla
// emits on bed enter/leave: idx 6 pose (SLEEPING/STANDING) and idx 14
// sleeping_pos as an optional block position.
func (s *Server) broadcastSleepMeta(sess *Session, head world.Pos, asleep bool) {
	eid := sess.entityID.Load()
	if eid == 0 {
		return
	}
	dim := sess.dimName()
	var b protocol.Buffer
	b.VarInt(eid)
	b.Byte(metaPoseIndex)
	b.Byte(metaPoseType)
	if asleep {
		b.VarInt(poseSleeping)
	} else {
		b.VarInt(poseStanding)
	}
	b.Byte(metaSleepingPosIndex)
	b.Byte(metaSleepingPosType)
	b.Bool(asleep)
	if asleep {
		b.Long(int64(protocol.EncodePosition(int32(head.X), int32(head.Y), int32(head.Z))))
	}
	b.Byte(0xFF)
	body := b.Bytes()
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other.state.Load() != StatePlay || !other.spawned.Load() || other.dimName() != dim {
			return true
		}
		_ = other.write(other.ids().PlayEntityMetadata, body)
		return true
	})
}

// sleepTickStep runs once per game tick. When every play-state session
// in a dimension is asleep the skip starts: each player sees the
// "skipping night" action bar (announced once at the edge), and after
// the vanilla 100-tick sleep duration the world jumps to the next
// morning and everyone wakes.
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
		if sess.sleeping.Load() {
			if sess.sleepTicks.Load() >= 100 {
				c.sleep++
			}
		} else {
			c.awake++
		}
		return true
	})
	for dim, c := range counts {
		allAsleep := c.awake == 0 && s.anySleepingIn(dim)
		if !allAsleep {
			delete(s.sleepSkipNotified, dim)
			continue
		}
		if !s.sleepSkipNotified[dim] {
			s.sleepSkipNotified[dim] = true
			s.sessions.Range(func(_, v any) bool {
				sess := v.(*Session)
				if sess.state.Load() != StatePlay || sess.dimension().name != dim {
					return true
				}
				s.sendActionBar(sess, component{translate: "sleep.skipping_night"})
				return true
			})
		}
		if c.sleep == 0 {
			continue
		}
		s.setDayTime((s.dayTime()/24000 + 1) * 24000)
		s.sessions.Range(func(_, v any) bool {
			sess := v.(*Session)
			if sess.state.Load() != StatePlay || sess.dimension().name != dim {
				return true
			}
			s.stopSleeping(sess)
			return true
		})
		delete(s.sleepSkipNotified, dim)
	}
}

// anySleepingIn reports whether at least one play-stage session in the
// dimension currently sleeps — the skip needs the whole dimension in
// bed, never an empty one.
func (s *Server) anySleepingIn(dim string) bool {
	found := false
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() == StatePlay && sess.dimension().name == dim && sess.sleeping.Load() {
			found = true
			return false
		}
		return true
	})
	return found
}
