package blackbox

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestTimeCommandParity replays the clock commands against the real
// server and against mc-go, and requires the replies to match component
// for component: the translation key, the number of arguments, each
// argument's NBT type, and every value that is not an absolute clock
// reading.
//
// This is the check that /time and /tick answer the way the real server
// does. A client localizes these replies, so a wrong key is a wrong
// message on screen, and a number sent as a string where the real
// server sends a long is a reply the client's decoder has to guess at.
func TestTimeCommandParity(t *testing.T) {
	const probe = "ClockParity"
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor(probe),
	}, nil)
	defer tg.cleanup()
	defer func() {
		if t.Failed() {
			vanillaLogTail(t)
		}
	}()

	want := runClockScript(t, tg.vanilla, probe)
	got := runClockScript(t, tg.mcgo, probe)
	for i := range clockScript {
		if want[i] == got[i] {
			continue
		}
		t.Errorf("%q: mc-go and vanilla disagree\n  vanilla: %s\n  mc-go:   %s",
			clockScript[i], want[i], got[i])
	}
}

// clockScript is the command list, in order. The game is frozen before
// the first clock reading and stays frozen except where a step or
// unfreeze is the point of the case, so every value the replies carry
// is fixed rather than a moving clock.
var clockScript = []string{
	"tick freeze",
	"tick step 5",
	"tick step 0",
	"tick step -1",
	"tick step abc",
	"tick step 2.7",
	"tick unfreeze",
	"tick step 5",
	"tick freeze",
	"tick rate 30",
	"tick rate 33.333",
	"tick rate 0.5",
	"tick rate 20000",
	"tick rate abc",
	"tick rate",
	"tick bogus",
	"tick",
	"time set 5000",
	"time query day",
	"time add 100",
	"time query day",
	"time set day",
	"time query day",
	"time set noon",
	"time set night",
	"time set midnight",
	"time query day",
	"time set 0",
	"time add 24000",
	"time query day",
	"time set abc",
	"time add abc",
	"time query day",
	"time query gametime",
	"time query daytime",
	"time query night",
	"time set",
	"time add",
	"time query",
	"time",
}

// volatileClockValues lists the commands whose reply carries an
// absolute clock reading, which differs between two servers that
// started at different moments. Their keys and argument types are still
// compared; only the numbers are dropped.
var volatileClockValues = map[string]bool{
	"time query gametime": true,
}

// runClockScript joins a server, runs the clock script, and returns one
// normalized reply per command.
func runClockScript(t *testing.T, addr, name string) []string {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, name)
	defer c.Close()
	_ = c.SetDeadline(time.Time{})
	sink := newChatSink(pr, pw)
	time.Sleep(3 * time.Second)
	sink.take()

	out := make([]string, 0, len(clockScript))
	for _, cmd := range clockScript {
		sink.take()
		sendCommand(t, pw, cmd)
		msgs := componentReplies(sink, !volatileClockValues[cmd])
		if len(msgs) == 0 {
			out = append(out, "<no reply>")
			continue
		}
		out = append(out, strings.Join(msgs, " "))
	}
	return out
}

// componentReplies drains System Chat until the stream goes quiet and
// returns every message, in order. A command answers with one message,
// with a second context message when its parse failed, and /tick query
// with a report; comparing only the first would hide both.
func componentReplies(sink *chatSink, withValues bool) []string {
	var out []string
	deadline := time.Now().Add(4 * time.Second)
	quiet := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		for _, line := range sink.take() {
			if tree, ok := componentTree(line, withValues); ok {
				out = append(out, tree)
				quiet = time.Now().Add(300 * time.Millisecond)
			}
		}
		if len(out) > 0 && time.Now().After(quiet) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return out
}

// normalizeComponent renders a reply as a comparable string: the
// translation key, then each argument with its NBT type. Values are
// included unless the command's reply is a moving clock reading.
func normalizeComponent(key string, args []any, withValues bool) string {
	var b strings.Builder
	b.WriteString(key)
	b.WriteString("(")
	for i, a := range args {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(normalizeArg(a, withValues))
	}
	b.WriteString(")")
	return b.String()
}

// normalizeArg renders one translation argument with its NBT type:
// s(string), i(int), l(long), f(float), d(double), c{compound}.
func normalizeArg(v any, withValues bool) string {
	switch a := v.(type) {
	case string:
		if withValues {
			return "s(" + a + ")"
		}
		return "s"
	case int32:
		return num("i", float64(a), withValues)
	case int64:
		return num("l", float64(a), withValues)
	case float32:
		return num("f", float64(a), withValues)
	case float64:
		return num("d", a, withValues)
	case map[string]any:
		keys := make([]string, 0, len(a))
		for k := range a {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString("c{")
		for i, k := range keys {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%q:%s", k, normalizeArg(a[k], withValues))
		}
		b.WriteString("}")
		return b.String()
	case nil:
		return "nil"
	}
	return fmt.Sprintf("%T", v)
}

// num renders a numeric argument.
func num(tag string, v float64, withValues bool) string {
	if !withValues {
		return tag
	}
	return fmt.Sprintf("%s(%v)", tag, v)
}
