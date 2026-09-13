package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

// The interaction gate: a player's use and dig are dropped until the
// client reports its terrain loaded, or until the grace runs out.
//
// Measured on 26.2 by sending the same use from the moment of joining:
// nothing comes back, however often it is repeated, and the first one
// that is acted on is the one sent 3.0 seconds (60 ticks) after the
// join. A client that reports loading is answered at once, which is what
// a real client does - and what the scripted clients in these tests do,
// since joinPlayBlackbox reports it, so no other test has to know about
// the gate.
const gateName = "GateProbe"

// gateSign is the sign the reporting client works with and gateSlowSign
// the one the silent client does. They have to be different signs: a
// sign answers nothing at all while another player's editor is open on
// it, and the reporting client leaves its editor open, so one sign
// between the two halves would measure that rule instead of the gate.
var (
	gateSign     = [3]int{8, -60, 0}
	gateSlowSign = [3]int{8, -60, -24}
)

// gateOutcome is what one connection saw of the gate.
type gateOutcome struct {
	swallowed   bool // a use right after joining was dropped
	afterReport bool // a use after the terrain-loaded report was acted on
	afterGrace  bool // a use after the grace was acted on
}

// gateSteps runs the gate script against one server.
func gateSteps(t *testing.T, tag, addr, name string) gateOutcome {
	t.Helper()
	var out gateOutcome

	// A connection that reports nothing: a use inside the first moments
	// of the session is dropped, and the same use works the instant the
	// report goes out. The two halves are what make this a measurement
	// of the gate rather than of the player's position or reach.
	c, pr, pw := joinPlayQuiet(t, addr, name)
	defer c.Close()
	sink := newSignSink(pr, pw)
	_ = c.SetDeadline(time.Time{})

	// A sign to open the editor on, and the player standing in front of
	// it: /tp is a command, which the gate does not touch.
	sendCommand(t, pw, "forceload add -64 -64 128 128")
	signHold(t, pw, "")
	before := sink.syncCount()
	sendCommand(t, pw, fmt.Sprintf("tp %s %g %g %g %g %v", name,
		float64(gateSign[0])+0.5, float64(gateSign[1]), float64(gateSign[2])+3.5, 180.0, 10.0))
	if !sink.waitSync(before, 10*time.Second) {
		t.Fatalf("%s: no teleport to accept after /tp", tag)
	}
	time.Sleep(200 * time.Millisecond)

	mark := sink.mark()
	signUse(t, pw, gateSign, 3)
	time.Sleep(400 * time.Millisecond)
	out.swallowed = len(sink.since(mark)) == 0

	if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
		t.Fatal(err)
	}
	signHold(t, pw, "")
	mark = sink.mark()
	signUse(t, pw, gateSign, 3)
	time.Sleep(500 * time.Millisecond)
	out.afterReport = sawEditor(sink.since(mark))

	// A connection that never reports anything is let through once the
	// grace has run out.
	c2, pr2, pw2 := joinPlayQuiet(t, addr, name+"Slow")
	defer c2.Close()
	slow := newSignSink(pr2, pw2)
	_ = c2.SetDeadline(time.Time{})
	signHold(t, pw2, "")
	before = slow.syncCount()
	sendCommand(t, pw2, fmt.Sprintf("tp %s %g %g %g %g %v", name+"Slow",
		float64(gateSlowSign[0])+0.5, float64(gateSlowSign[1]), float64(gateSlowSign[2])+3.5, 180.0, 10.0))
	if !slow.waitSync(before, 10*time.Second) {
		t.Fatalf("%s: no teleport to accept for the slow client", tag)
	}
	time.Sleep(3500 * time.Millisecond)
	signHold(t, pw2, "")
	mark = slow.mark()
	signUse(t, pw2, gateSlowSign, 3)
	time.Sleep(500 * time.Millisecond)
	out.afterGrace = sawEditor(slow.since(mark))

	t.Logf("%s: use swallowed=%v, acted on after the report=%v, after the grace=%v",
		tag, out.swallowed, out.afterReport, out.afterGrace)
	return out
}

// sawEditor reports whether any recorded event is an editor opening.
func sawEditor(events []string) bool {
	_, ok := signEditorSide(events)
	return ok
}

// TestInteractionGateParity proves both servers swallow a player's
// interactions until the terrain-loaded report arrives, and act on them
// once it does, or once the grace has run out without it.
func TestInteractionGateParity(t *testing.T) {
	// The setup client needs the same operator level as the rest: without
	// it the real server refuses the setblock, no sign exists, and every
	// use is answered with nothing - which is exactly what a gate looks
	// like, so the test would measure the wrong thing.
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor(gateName, gateName+"Slow", gateName+"Setup")}, nil)
	defer tg.cleanup()

	// A sign to interact with, placed before anyone joins: the gate
	// script only has the first three seconds of a session to work in,
	// so no time can go on setting it up.
	setup := func(addr string) {
		c, pr, pw := joinPlayBlackbox(t, addr, gateName+"Setup")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		sink := newChatSink(pr, pw)
		sendCommand(t, pw, fmt.Sprintf("setblock %d %d %d minecraft:oak_sign[rotation=0]", gateSign[0], gateSign[1], gateSign[2]))
		sendCommand(t, pw, fmt.Sprintf("setblock %d %d %d minecraft:oak_sign[rotation=0]", gateSlowSign[0], gateSlowSign[1], gateSlowSign[2]))
		sendCommand(t, pw, "forceload add -64 -64 128 128")
		time.Sleep(500 * time.Millisecond)
		waitDrained(t, pw, sink)
		for _, line := range sink.take() {
			for _, bad := range []string{"nknown or incomplete", "ermission", "o such"} {
				if strings.Contains(line, bad) {
					t.Errorf("%s: the setup command was refused (%s): %s", addr, bad, line)
				}
			}
		}
	}
	setup(tg.vanilla)
	setup(tg.mcgo)

	v := gateSteps(t, "vanilla", tg.vanilla, gateName)
	m := gateSteps(t, "mc-go", tg.mcgo, gateName)

	for _, got := range []struct {
		tag string
		gateOutcome
	}{{"vanilla", v}, {"mc-go", m}} {
		if !got.swallowed {
			t.Errorf("%s acted on a use sent before the client reported loading", got.tag)
		}
		if !got.afterReport {
			t.Errorf("%s did not act on a use sent after the terrain-loaded report", got.tag)
		}
		if !got.afterGrace {
			t.Errorf("%s did not act on a use sent after the grace had run out", got.tag)
		}
	}
	if v != m {
		t.Errorf("vanilla %+v, mc-go %+v", v, m)
	}
}
