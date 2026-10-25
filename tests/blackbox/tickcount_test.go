package blackbox

// How far /tick step and /tick sprint actually move the world.
//
// Neither reply says. A step answers at once and then runs its ticks at
// twenty a second however fast the machine is - a step of a hundred is
// still in progress five seconds later, which is why a test that needs a
// deterministic run out of the real server sprints instead - and a
// sprint answers when its ticks are done but does not say how many it
// ran. The count is read off the game clock instead: freeze, read the
// clock, give one command, read it again.
//
// The clock is what makes the reading exact rather than a guess: it
// stands still while the game is frozen and moves one tick per tick
// simulated, and the same two readings were cross-checked against the
// cook and burn counters of a furnace that had never been lit, which can
// only move on a tick.
//
// The sprint is the one that is not obvious. A sprint of n moves the
// clock by n+1, because the command is handled inside a tick and the
// sprint applies to the rest of that tick as well: measured on 26.2,
// three sprints of ten in a row moved the clock by eleven each and a
// sprint of one by two. A step moves by exactly the number it is given.
// mc-go simulates the same counts, and this is the test that says so.
import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tickCountCases is every command and how far the real server's clock
// moved when it was given, in the order they are run. The settle time is
// long enough for the command to have finished: a step runs at twenty
// ticks a second, so a step of ten is still going half a second after it
// has answered.
var tickCountCases = []struct {
	cmd    string
	move   int64
	settle time.Duration
}{
	{"tick step 1", 1, time.Second},
	{"tick step 10", 10, 3 * time.Second},
	{"tick sprint 1", 2, time.Second},
	{"tick sprint 10", 11, time.Second},
	{"tick sprint 100", 101, time.Second},
}

// TestTickCountParity runs each command against both servers, from the
// same frozen state, and requires the same number of simulated ticks.
func TestTickCountParity(t *testing.T) {
	const name = "TickCount"
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor(name),
	}, nil)
	defer tg.cleanup()

	// The real server is driven through its console, which is the
	// command surface an operator has and the one the counts above were
	// measured on.
	dir := t.TempDir()
	port, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}
	_, say, _ := vanillaConsole(t, blackboxJar(t), port, map[string]string{"level-name": "world"}, nil, dir)
	say("tick freeze")

	c, pr, pw := joinPlayBlackbox(t, tg.mcgo, name)
	defer c.Close()
	_ = c.SetDeadline(time.Time{})
	sink := newChatSink(pr, pw)
	time.Sleep(3 * time.Second)
	sink.take()
	// Frozen before the first count, so the tick the command is handled
	// in is the only thing either server simulates that the other does
	// not know about.
	sendCommand(t, pw, "tick freeze")
	waitDrained(t, pw, sink)

	for _, tc := range tickCountCases {
		vMove := vanillaClockMove(t, say, dir, tc.cmd, tc.settle)
		if vMove != tc.move {
			t.Errorf("the real server moved %d ticks for %q, want %d",
				vMove, tc.cmd, tc.move)
		}
		before := gameTimeOf(t, pw, sink)
		sendCommand(t, pw, tc.cmd)
		time.Sleep(tc.settle)
		mMove := gameTimeOf(t, pw, sink) - before
		if mMove != vMove {
			t.Errorf("%q: mc-go moved %d ticks, vanilla %d", tc.cmd, mMove, vMove)
			continue
		}
		t.Logf("%q: both moved %d ticks", tc.cmd, mMove)
	}
}

// vanillaClockMove gives the real server one command and reports how far
// its clock moved.
func vanillaClockMove(t *testing.T, say func(string), dir, cmd string, settle time.Duration) int64 {
	t.Helper()
	before := vanillaGameTime(t, say, dir)
	say(cmd)
	time.Sleep(settle)
	return vanillaGameTime(t, say, dir) - before
}

// vanillaGameTime asks the console for the game clock and waits for an
// answer newer than the last one it read, so the value is the reply to
// this query rather than an earlier one still sitting in the log. The
// wait is what makes it usable at all: the reading is written to the log
// by the server thread, and a test that just read the newest line would
// sometimes get the previous query's answer and measure nothing.
func vanillaGameTime(t *testing.T, say func(string), dir string) int64 {
	t.Helper()
	seen := strings.Count(vanillaLogText(dir), "The game time is ")
	say("time query gametime")
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		log := vanillaLogText(dir)
		if strings.Count(log, "The game time is ") <= seen {
			continue
		}
		if v, ok := lastGameTime(log); ok {
			return v
		}
	}
	t.Fatalf("the real server never answered a game time query")
	return 0
}

// vanillaLogText is the whole log of a directory the test chose, which
// is what a count of its answers needs; the tail a failure prints is not
// enough.
func vanillaLogText(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "vanilla.log"))
	if err != nil {
		return ""
	}
	return string(b)
}
