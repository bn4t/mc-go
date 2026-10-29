// Hurting-projectile facing parity — vanilla
// (ProjectileUtil.rotateTowardsMovement javap): AbstractHurting-
// Projectile.tick calls it with 0.2 between the delta-add and setPos,
// ShulkerBullet.tick with 0.5 after hitTargetOrDeflectSelf. The
// facing eases f of the way toward the delta heading per tick:
// yaw target atan2(z,x)·deg+90 stored RAW (setYRot never mods),
// pitch atan2(horiz,y)·deg−90 stored %360 (setXRot fmods), the
// previous rot unwraps to within ±180 of the target via ±360 loops,
// then lerp(f, rotO, rot). A zero delta is a no-op.
//
// The inertia chain keeps every hurting projectile's delta parallel
// to its initial heading (accel lands along normalize(delta)), so
// the rotation target is constant in flight and the remaining gap
// decays by exactly 0.8 per tick — the checkpoint-ratio pin is
// independent of how many live ticks the summon ran before the
// freeze landed.
//
// Also pinned: Entity.load reads the summon Rotation through
// setRot — each axis lands as value % 360 (Java frem). A zero-delta
// summon doubles as the guard pin: its rotation never moves, so its
// setRot'd summon value reads identically whenever the entity is
// queryable (under-freeze summons aren't data-gettable until they
// tick-join — the reads run after unfreeze).
package blackbox

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
)

// lastNumberSigned is lastNumber with the minus sign kept — the
// shared helper parses digits+dots only, which reads -90 as 90.
func lastNumberSigned(s string) float64 {
	last := ""
	for i := 0; i < len(s); {
		if s[i] == '-' && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9' {
			j := i + 1
			for j < len(s) && ((s[j] >= '0' && s[j] <= '9') || s[j] == '.') {
				j++
			}
			last = s[i:j]
			i = j
		} else if s[i] >= '0' && s[i] <= '9' {
			j := i
			for j < len(s) && ((s[j] >= '0' && s[j] <= '9') || s[j] == '.') {
				j++
			}
			if run := strings.Trim(s[i:j], "."); run != "" {
				last = run
			}
			i = j
		} else {
			i++
		}
	}
	v, _ := strconv.ParseFloat(last, 64)
	return v
}

// dataGetVec2 reads a two-float list path (Rotation) as a pair,
// keeping negative components' signs.
func dataGetVec2(t *testing.T, sink *chatSink, pw *protocol.Writer, sel, path string) (float64, float64) {
	t.Helper()
	get := func(i int) float64 {
		sendCommand(t, pw, "data get entity "+sel+" "+path+"["+strconv.Itoa(i)+"]")
		sendCommand(t, pw, "say ZEND")
		deadline := time.Now().Add(6 * time.Second)
		val := -1.0
		for time.Now().Before(deadline) {
			end := false
			for _, line := range sink.take() {
				if j := strings.Index(line, "data.entity.query"); j >= 0 {
					val = lastNumberSigned(line[:j])
				}
				if strings.Contains(line, "ZEND") {
					end = true
				}
			}
			if end {
				break
			}
			time.Sleep(30 * time.Millisecond)
		}
		return val
	}
	return get(0), get(1)
}

func TestHurtingRotationParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("HR")}, nil)
	defer targets.cleanup()

	// Flight lanes summon live so they join the ticklist on a normal
	// tick — a summon under freeze starts ticking ~9 ticks late on
	// vanilla, which would desync the stepped checkpoints between
	// legs. acceleration_power is trimmed so nothing crosses the
	// ticket boundary (the ownerless discard) inside the window.
	type lane struct {
		name   string
		summon string
		sel    string
		ty, tp float64 // heading targets from the bytecode formula
	}
	flight := []lane{
		{"fireball",
			`summon minecraft:fireball 4 -40 -16 {Motion:[0.3d,0.15d,-0.35d],acceleration_power:0.01d}`,
			"@e[type=minecraft:fireball,limit=1]",
			math.Atan2(-0.35, 0.3)*180/math.Pi + 90,
			math.Atan2(math.Hypot(0.3, -0.35), 0.15)*180/math.Pi - 90},
		{"wither_skull",
			`summon minecraft:wither_skull 4 -40 0 {Motion:[0.45d,-0.1d,0.15d],dangerous:1b,acceleration_power:0.01d}`,
			"@e[type=minecraft:wither_skull,limit=1]",
			math.Atan2(0.15, 0.45)*180/math.Pi + 90,
			math.Atan2(math.Hypot(0.45, 0.15), -0.1)*180/math.Pi - 90},
		{"wind_charge",
			`summon minecraft:wind_charge 4 -40 16 {Motion:[0.2d,0.1d,0.15d],acceleration_power:0.01d}`,
			"@e[type=minecraft:wind_charge,limit=1]",
			math.Atan2(0.15, 0.2)*180/math.Pi + 90,
			math.Atan2(math.Hypot(0.2, 0.15), 0.1)*180/math.Pi - 90},
	}
	type legObs struct {
		edge [2]float64       // dragon_fireball Rotation (setRot arm + guard)
		zero [2]float64       // small_fireball Rotation (guard)
		cp   [3][3][2]float64 // [lane][checkpoint][axis]: after 10 / 30 / 70 steps
	}
	obs := map[string]*legObs{}

	for _, tg := range []struct{ tag, addr string }{
		{"vanilla", targets.vanilla}, {"mcgo", targets.mcgo},
	} {
		c, pr, pw := joinPlayBlackbox(t, tg.addr, "HR")
		sink := newChatSink(pr, pw)
		sink.cap = 1 << 20
		sendClientInformation(t, pw)
		time.Sleep(300 * time.Millisecond)
		sendCommand(t, pw, "tp @s 10 -40 0 0 0")
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		// A corridor covering the whole flight window — a leftover
		// wall would explode a lane mid-window.
		sendCommand(t, pw, "fill -2 -46 -24 62 -34 4 minecraft:air")
		sendCommand(t, pw, "fill -2 -46 12 62 -34 24 minecraft:air")
		dataGetLine(t, sink, pw, "@s", "Pos")

		// Edge lane: zero delta, Rotation [370,-450] — setRot mods
		// both axes to [10,-90] and the guard keeps it there
		// forever. Zero lane: plain zero delta stays [0,0]. Both
		// summon live — their rotations can't move anyway (zero
		// delta), so reads any time after the join pin the values.
		sendCommand(t, pw,
			`summon minecraft:dragon_fireball 4 -40 0 {Rotation:[370f,-450f],Motion:[0d,0d,0d],acceleration_power:0.01d}`)
		sendCommand(t, pw,
			`summon minecraft:small_fireball 6 -40 0 {Motion:[0d,0d,0d],acceleration_power:0.01d}`)

		o := &legObs{}
		waitEntity := func(sel string) {
			for attempt := 0; attempt < 8; attempt++ {
				if d := dataGetAll(t, sink, pw, sel); d != "" {
					return
				}
				time.Sleep(300 * time.Millisecond)
			}
			for _, l := range sink.take() {
				t.Logf("%s reply: %q", tg.tag, l)
			}
			c.Close()
			t.Fatalf("%s: entity never became queryable: %s", tg.tag, sel)
		}
		waitEntity("@e[type=minecraft:dragon_fireball,limit=1]")
		waitEntity("@e[type=minecraft:small_fireball,limit=1]")
		o.edge[0], o.edge[1] = dataGetVec2(t, sink, pw,
			"@e[type=minecraft:dragon_fireball,limit=1]", "Rotation")
		o.zero[0], o.zero[1] = dataGetVec2(t, sink, pw,
			"@e[type=minecraft:small_fireball,limit=1]", "Rotation")

		// The flight lanes start apart from each other: overlapping
		// redirectable projectiles can deflect each other and reset their
		// acceleration power, invalidating this independent-flight fixture.
		// Flight lanes summon live back-to-back and freeze right
		// after — each lane banks only a handful of live ticks
		// before the freeze pins the count, keeping rem_cp1 well
		// above the ratio floor. (Summoning under freeze instead
		// would join ~9 ticks late on vanilla and desync the
		// stepped checkpoints.)
		for _, l := range flight {
			sendCommand(t, pw, l.summon)
		}
		sendCommand(t, pw, "tick freeze")
		time.Sleep(500 * time.Millisecond)

		step := func(n int) {
			sendCommand(t, pw, fmt.Sprintf("tick step %d", n))
			time.Sleep(time.Duration(n*55+700) * time.Millisecond)
		}
		readLane := func(cpIdx int) {
			for li, l := range flight {
				status, power := dataGetSel(t, sink, pw, l.sel, "acceleration_power")
				if status != "query" || math.Abs(power-0.01) > 1e-6 {
					t.Fatalf("%s %s checkpoint%d acceleration power %v (%s), want0.01; lanes must remain independent and queryable", tg.tag, l.name, cpIdx, power, status)
				}
				a, b := dataGetVec2(t, sink, pw, l.sel, "Rotation")
				o.cp[li][cpIdx] = [2]float64{a, b}
			}
		}
		step(10)
		readLane(0)
		step(20)
		readLane(1)
		step(40)
		readLane(2)
		obs[tg.tag] = o
		c.Close()
	}

	// Summon setRot arm + zero-delta guard: Rotation [370,-450] lands
	// as [10,-90] and never moves (zero delta → the lerp is a
	// no-op); the plain zero-delta small_fireball stays [0,0].
	for _, tg := range []string{"vanilla", "mcgo"} {
		e := obs[tg].edge
		if math.Abs(e[0]-10) > 0.05 || math.Abs(e[1]+90) > 0.05 {
			t.Fatalf("%s edge lane Rotation %v, want [10,-90] (setRot %%360 + guard)", tg, e)
		}
		z := obs[tg].zero
		if math.Abs(z[0]) > 0.05 || math.Abs(z[1]) > 0.05 {
			t.Fatalf("%s zero lane Rotation %v, want [0,0] (guard)", tg, z)
		}
	}
	// Rate pin: the remaining heading gap decays by 0.8 per stepped
	// tick, so rem_cp2 = rem_cp1 · 0.8^20. Assert the per-leg ratio,
	// then the converged value — independent of the live-tick jitter
	// before the freeze landed.
	for li, l := range flight {
		for ax := 0; ax < 2; ax++ {
			target := l.ty
			if ax == 1 {
				target = l.tp
			}
			for _, tg := range []string{"vanilla", "mcgo"} {
				rem1 := target - obs[tg].cp[li][0][ax]
				rem2 := target - obs[tg].cp[li][1][ax]
				if math.Abs(rem1) < 0.5 {
					continue // converged already — ratio meaningless
				}
				ratio := rem2 / rem1
				if math.Abs(ratio-0.011529) > 0.006 {
					t.Errorf("%s %s axis%d: rem ratio %.4f, want 0.8^20=%.5f (rem1 %.2f rem2 %.2f)",
						tg, l.name, ax, ratio, 0.011529, rem1, rem2)
				}
			}
			for _, tg := range []string{"vanilla", "mcgo"} {
				fin := obs[tg].cp[li][2][ax]
				if math.Abs(fin-target) > 0.4 {
					t.Errorf("%s %s axis%d: converged %.2f, want %.2f",
						tg, l.name, ax, fin, target)
				}
			}
			if dv := math.Abs(obs["vanilla"].cp[li][2][ax] - obs["mcgo"].cp[li][2][ax]); dv > 0.4 {
				t.Errorf("%s axis%d: converged legs diverge %.2f", l.name, ax, dv)
			}
		}
	}
}
