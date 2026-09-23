package blackbox

// TestCreeperSwellParity pins SwellGoal's vanilla state machine:
// the fuse arms only inside 3 blocks, the MOVE flag freezes the
// creeper while it hisses (a swelling creeper does NOT keep walking
// into its target), escaping beyond 7 blocks defuses it, and the
// blast deals ((impact²+impact)/2 · 7 · 2r + 1) — the 2r term doubles
// what the formula's surface reading suggests.
import (
	"fmt"
	"math"
	"net"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
)

// healthNum finds the float in a `data get entity P Health` reply.
var healthNum = regexp.MustCompile(`-?[0-9]+\.?[0-9]*f`)

func swellHealth(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, obs *fleeObs) float64 {
	t.Helper()
	sendCommand(t, pw, "data get entity P Health")
	base := len(obs.chats)
	fleePump(t, c, pr, pw, obs, 900*time.Millisecond)
	for i := len(obs.chats) - 1; i >= base; i-- {
		if m := healthNum.FindString(obs.chats[i]); m != "" {
			v, err := strconv.ParseFloat(strings.TrimSuffix(m, "f"), 64)
			if err == nil {
				return v
			}
		}
	}
	t.Fatalf("no Health reply; chats=%v", obs.chats)
	return -1
}

func swellHasEntity(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, obs *fleeObs, sel string) bool {
	t.Helper()
	sendCommand(t, pw, "execute if entity "+sel)
	base := len(obs.chats)
	fleePump(t, c, pr, pw, obs, 900*time.Millisecond)
	for _, ln := range obs.chats[base:] {
		if strings.Contains(ln, "conditional.pass") {
			return true
		}
		if strings.Contains(ln, "conditional.fail") {
			return false
		}
	}
	return false
}

// swellDist reads the nearest creeper's Pos and returns its XZ
// distance to pp; ok=false means no creeper (detonated).
func swellDist(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, obs *fleeObs, pp [3]float64) (float64, bool) {
	t.Helper()
	sendCommand(t, pw, "data get entity @e[type=minecraft:creeper,limit=1] Pos")
	base := len(obs.chats)
	fleePump(t, c, pr, pw, obs, 800*time.Millisecond)
	for i := len(obs.chats) - 1; i >= base; i-- {
		nums := posNum.FindAllString(obs.chats[i], -1)
		if len(nums) >= 3 {
			var cp [3]float64
			for k := 0; k < 3; k++ {
				v, _ := strconv.ParseFloat(strings.TrimSuffix(nums[k], "d"), 64)
				cp[k] = v
			}
			return math.Sqrt((cp[0]-pp[0])*(cp[0]-pp[0]) + (cp[2]-pp[2])*(cp[2]-pp[2])), true
		}
	}
	return 0, false
}

func TestCreeperSwellParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	run := func(addr string) []string {
		var out []string
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		_ = c.SetDeadline(time.Now().Add(240 * time.Second))
		obs := &fleeObs{}
		sendCommand(t, pw, "gamemode survival P")
		sendCommand(t, pw, "gamerule minecraft:natural_health_regeneration false")
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		sendCommand(t, pw, "time set 6000")
		fleePump(t, c, pr, pw, obs, 800*time.Millisecond)
		pp := fleePos(t, c, pr, pw, obs, "P")

		// Leg A: creeper summoned 6 out chases in, freezes at the
		// ~3-block arm point while the fuse burns, then detonates.
		// The minimum sampled distance before the boom IS the freeze
		// point (~2.7-2.9); post-blast the player is badly hurt but
		// alive (measured 1.1-1.9 hearts on both).
		sendCommand(t, pw, fmt.Sprintf("summon minecraft:creeper %.1f %.1f %.1f", pp[0]+6, pp[1], pp[2]))
		minDist := math.MaxFloat64
		boom := false
		for i := 0; i < 10; i++ {
			fleePump(t, c, pr, pw, obs, 700*time.Millisecond)
			d, ok := swellDist(t, c, pr, pw, obs, pp)
			if !ok {
				boom = true
				break
			}
			if d < minDist {
				minDist = d
			}
		}
		hp := swellHealth(t, c, pr, pw, obs)
		out = append(out, fmt.Sprintf("A boom=%v min=%.2f hp=%.2f", boom, minDist, hp))
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		sendCommand(t, pw, "effect give P minecraft:instant_health 1 200")
		fleePump(t, c, pr, pw, obs, 700*time.Millisecond)

		// Leg B: a swelling creeper defuses when the target escapes
		// beyond 7 — teleport out mid-fuse, the creeper survives and
		// resumes the chase without ever detonating.
		pp = fleePos(t, c, pr, pw, obs, "P")
		sendCommand(t, pw, fmt.Sprintf("summon minecraft:creeper %.1f %.1f %.1f", pp[0]+2.5, pp[1], pp[2]))
		fleePump(t, c, pr, pw, obs, 900*time.Millisecond)
		sendCommand(t, pw, fmt.Sprintf("tp P %.1f %.1f %.1f", pp[0]+18, pp[1], pp[2]))
		fleePump(t, c, pr, pw, obs, 1700*time.Millisecond)
		alive := swellHasEntity(t, c, pr, pw, obs, "@e[type=minecraft:creeper]")
		hp = swellHealth(t, c, pr, pw, obs)
		out = append(out, fmt.Sprintf("B alive=%v hp=%.2f", alive, hp))
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		fleePump(t, c, pr, pw, obs, 600*time.Millisecond)
		c.Close()
		return out
	}

	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	for i := range van {
		var m string
		if i < len(mc) {
			m = mc[i]
		}
		t.Logf("leg %d vanilla: %s", i, van[i])
		t.Logf("leg %d mc-go:   %s", i, m)
	}
	if len(van) != len(mc) {
		t.Fatalf("leg count %d vs %d", len(van), len(mc))
	}
	// Both sides must land inside the measured bands, and match.
	legA := regexp.MustCompile(`^A boom=(\w+) min=([0-9.]+) hp=([0-9.]+)$`)
	check := func(tag, s string) {
		if m := legA.FindStringSubmatch(s); m != nil {
			if m[1] != "true" {
				t.Errorf("%s leg A: creeper never detonated: %q", tag, s)
				return
			}
			mn, _ := strconv.ParseFloat(m[2], 64)
			hp, _ := strconv.ParseFloat(m[3], 64)
			if mn < 2.0 || mn > 3.4 {
				t.Errorf("%s leg A freeze dist %.2f outside [2.0,3.4]: %q", tag, mn, s)
			}
			if hp <= 0 || hp >= 4 {
				t.Errorf("%s leg A post-blast health %.2f outside (0,4): %q", tag, hp, s)
			}
			return
		}
		if m := regexp.MustCompile(`^B alive=(\w+) hp=([0-9.]+)$`).FindStringSubmatch(s); m != nil {
			if m[1] != "true" {
				t.Errorf("%s leg B: creeper detonated: %q", tag, s)
			}
			if hp, _ := strconv.ParseFloat(m[2], 64); hp != 20 {
				t.Errorf("%s leg B: player health %.2f != 20: %q", tag, hp, s)
			}
		}
	}
	for i := range van {
		check("vanilla", van[i])
		check("mc-go", mc[i])
		// Leg A's real-valued distances/health sit inside measured
		// bands — only the boolean legs compare verbatim.
		if !strings.HasPrefix(van[i], "A ") && van[i] != mc[i] {
			t.Errorf("leg %d mismatch: vanilla %q mc-go %q", i, van[i], mc[i])
		}
	}
}
