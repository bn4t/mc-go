package blackbox

// Survival click restrictions: middle-clicks clone nothing in survival,
// on the menu or under it, and a click on window 0 while any menu is
// open is ignored outright. Measured on vanilla 26.2. Both servers run
// in survival mode from the start (no /gamemode switch); the creative
// contrast lives in the click script. One script runs on both servers;
// logical slot state is compared after every step.

import (
	"testing"

	"mc-go/internal/server"
)

func TestSurvivalClickRestrictions(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "survival"}, map[string]string{
		"ops.json": opsJSONFor("SurvClickV", "SurvClickM"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer tg.cleanup()

	steps := []struct {
		tag        string
		slot       int16
		button     byte
		mode       int32
		w0         bool
		badState   bool
		closeFirst bool
		reopen     bool
	}{
		{tag: "setup"},
		// Heal window-0 state: setup traffic is dropped, so this
		// stale click forces a full inventory resync on both servers.
		{tag: "surv-sync", slot: 10, button: 0, mode: 0, w0: true, badState: true},
		// Survival: middle-clicks clone nothing, on the menu or
		// under it.
		{tag: "surv-w0-mid", slot: 36, button: 2, mode: 3, w0: true},
		{tag: "surv-reopen", reopen: true},
		{tag: "surv-menu-mid", slot: 54, button: 2, mode: 3},
		// A click on window 0 while the menu is open is ignored.
		{tag: "surv-w0-under-menu", slot: 36, button: 0, mode: 0, w0: true},
		{tag: "close2", closeFirst: true},
	}

	run := func(addr, name string) []clickSnap {
		d := newClickDriverOn(t, addr, name, "minecraft:chest", 2, [][2]string{
			{"hotbar.0", "minecraft:dirt 64"},
		}, nil)
		snaps := make([]clickSnap, 0, len(steps))
		seq := int32(12)
		for _, s := range steps {
			switch {
			case s.tag == "setup":
			case s.closeFirst:
				d.close()
			case s.reopen:
				d.reopen(8, -61, 0, seq, "reopen-surv")
				seq++
			default:
				d.click(s.tag, s.slot, s.button, s.mode, s.w0, s.badState)
			}
			cp := clickSnap{carried: d.snap.carried, menu: map[int32]string{}, p0: map[int32]string{}}
			for k, v := range d.snap.menu {
				cp.menu[k] = v
			}
			for k, v := range d.snap.p0 {
				cp.p0[k] = v
			}
			snaps = append(snaps, cp)
		}
		return snaps
	}

	want := run(tg.vanilla, "SurvClickV")
	got := run(tg.mcgo, "SurvClickM")
	for i, s := range steps {
		if diffs := diffSnaps(want[i], got[i]); len(diffs) > 0 {
			for _, df := range diffs {
				t.Errorf("step %s: %s", s.tag, df)
			}
		}
	}
}
