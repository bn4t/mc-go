package blackbox

import (
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// Helpers for measuring what a /item reply says about stack sizes. They
// live next to the capture that wrote the table rather than in the
// parity test, because the parity test compares replies against the same
// keys and arguments this reads.

// maxStackReplies reads the replies to n probe commands off the sink, in
// the order the commands were sent: the connection preserves that order,
// so reply i belongs to command i.
//
// Anything the server says that is not a command reply is skipped rather
// than counted, and the count of what was skipped is logged - a probe
// whose alignment slipped by one would otherwise classify every item as
// its neighbour's size and still look like a measurement.
func maxStackReplies(t *testing.T, sink *chatSink, n int) []string {
	t.Helper()
	out := make([]string, 0, n)
	skipped := 0
	deadline := time.Now().Add(30 * time.Second)
	for len(out) < n && time.Now().Before(deadline) {
		for _, line := range sink.take() {
			if _, _, _, ok := maxStackReply(line); !ok {
				skipped++
				continue
			}
			out = append(out, line)
		}
		if len(out) < n {
			time.Sleep(2 * time.Millisecond)
		}
	}
	if len(out) != n {
		t.Fatalf("%d of %d probe replies arrived (%s)", len(out), n, sink.health())
	}
	if skipped > 0 {
		t.Logf("%d probe replies, %d other messages skipped", n, skipped)
	}
	return out
}

// runItemProbeScript runs each command of a probe script on its own
// and returns one normalized reply per command, in order.
//
// One command does not always mean one message: a parse failure is
// followed by a second message carrying the input with the token the
// parse stopped on marked, and a command that fails after it parsed -
// an oversized stack, a slot the target does not have - answers once.
// Running one command at a time is what keeps the two together: the
// messages that arrive after a command and before the stream goes quiet
// are that command's reply, whatever their number, and the next command
// is only sent once they have all landed.
func runItemProbeScript(t *testing.T, pw *protocol.Writer, sink *chatSink, cmds []string) []string {
	t.Helper()
	out := make([]string, 0, len(cmds))
	for _, cmd := range cmds {
		sink.take()
		sendCommand(t, pw, cmd)
		msgs := componentReplies(sink, true)
		if len(msgs) == 0 {
			out = append(out, "<no reply>")
			continue
		}
		out = append(out, strings.Join(msgs, " "))
	}
	return out
}

// maxStackReply decodes one reply and reports whether it is the failure
// the real server answers an oversized stack with.
//
// A successful command reply carries its translation key at the top
// level; a failed one carries the message one level down, in extra, with
// the key's own style on the outside - which is the same shape
// componentArgs already reads, and is what tells an accepted stack from
// a refused one.
func maxStackReply(line string) (key string, args []any, refused bool, ok bool) {
	i := strings.Index(line, ":")
	if i < 0 {
		return "", nil, false, false
	}
	id, err := strconv.ParseInt(line[:i], 0, 32)
	if err != nil || int32(id) != server.IDPlaySystemChatCB {
		return "", nil, false, false
	}
	root, err := nbt.ReadUnnamed(strings.NewReader(line[i+1:]))
	if err != nil {
		return "", nil, false, false
	}
	m, ok := root.(map[string]any)
	if !ok {
		return "", nil, false, false
	}
	if k, found := m["translate"].(string); found {
		return k, nbtArgs(m), false, true
	}
	list, ok := m["extra"].(nbt.List)
	if !ok || len(list.Items) == 0 {
		return "", nil, false, false
	}
	sub, ok := list.Items[0].(map[string]any)
	if !ok {
		return "", nil, false, false
	}
	k, found := sub["translate"].(string)
	if !found {
		return "", nil, false, false
	}
	return k, nbtArgs(sub), true, true
}

// nbtArgs returns a compound's translation arguments.
func nbtArgs(m map[string]any) []any {
	if l, ok := m["with"].(nbt.List); ok {
		return l.Items
	}
	return nil
}

// maxStackCap reads the stack size out of a refusal's arguments.
//
// The refusal is one number and one item name - "minecraft:iron_pickaxe
// can only stack up to 1" - and both travel as resource-key compounds,
// {"": "minecraft:iron_pickaxe"} and {"": 1}, which is the shape
// vanilla's own arguments take wherever a string or a number is one: the
// same shape /time set reports its world as. So the number is the only
// integer anywhere in the reply, inside a compound or not, and a reply
// with no integer, or with two different ones, is not a reply this probe
// understands.
func maxStackCap(args []any) (int32, bool) {
	var found int32
	have := false
	var walk func(v any) bool
	walk = func(v any) bool {
		switch x := v.(type) {
		case int8:
			return record(int32(x), &found, &have)
		case int16:
			return record(int32(x), &found, &have)
		case int32:
			return record(x, &found, &have)
		case int64:
			return record(int32(x), &found, &have)
		case map[string]any:
			for _, k := range sortedStringKeys(x) {
				if !walk(x[k]) {
					return false
				}
			}
			return true
		case nbt.List:
			for _, it := range x.Items {
				if !walk(it) {
					return false
				}
			}
			return true
		}
		return true
	}
	for _, a := range args {
		if !walk(a) {
			return 0, false
		}
	}
	if !have || found < 1 || found > 64 {
		return 0, false
	}
	return found, true
}

// record keeps one integer, refusing a second that disagrees with it.
func record(v int32, found *int32, have *bool) bool {
	if *have && v != *found {
		return false
	}
	*found, *have = v, true
	return true
}

// sortedStringKeys returns a map's keys in order, so a walk over a
// decoded component does not depend on Go's iteration order.
func sortedStringKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// mustComponent decodes a captured System Chat payload for rendering in
// a log line.
func mustComponent(t *testing.T, line string) any {
	t.Helper()
	i := strings.Index(line, ":")
	if i < 0 {
		t.Fatalf("not a captured packet: %s", line)
	}
	root, err := nbt.ReadUnnamed(strings.NewReader(line[i+1:]))
	if err != nil {
		t.Fatalf("decode %s: %v", line, err)
	}
	return root
}

// sortedKeys returns a map's integer keys in order, so a log line or a
// generated file does not depend on Go's map iteration order.
func sortedKeys[V any](m map[int32]V) []int32 {
	out := make([]int32, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
