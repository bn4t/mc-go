package blackbox

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"mc-go/internal/world"
)

// Where every item's maximum stack size comes from.
//
// The number is a property of the item and nothing mc-go can read: it is
// not in the bundled data pack (the config capture records the 29
// registries that arrive with element data, and the item registry is not
// one of them), and the jar's own code is off limits. So it is measured,
// the same way everything else here is: ask the real server to put 64 of
// the item in a slot and read what it says.
//
// A 64-stack item is accepted and the command reports what it replaced.
// An item that stacks to 16 is refused with the cap in the message, and
// so is one that does not stack at all, which is how the three sizes
// tell themselves apart in one command per item: an accepted reply means
// 64, and a refusal carries the cap as its argument.
//
//	MCGO_BLACKBOX=1 MCGO_CAPTURE=1 go test ./tests/blackbox -run TestCaptureVanillaMaxStack
//
// The recording is committed as internal/server/testdata/vanilla-26.2/max_stack.tsv
// and turned into the table mc-go ships by tools/genmaxstack, so the
// only thing that ever runs at server runtime is Go code.
func TestCaptureVanillaMaxStack(t *testing.T) {
	if os.Getenv("MCGO_CAPTURE") != "1" {
		t.Skip("set MCGO_CAPTURE=1 to record vanilla max stack sizes")
	}
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor(maxStackProbeName),
	}, nil)
	defer tg.cleanup()

	c, pr, pw := joinPlayBlackbox(t, tg.vanilla, maxStackProbeName)
	defer c.Close()
	// The join burst streams chunks; the probe is a long stream of
	// commands after that, so no per-read deadline.
	_ = c.SetDeadline(time.Time{})
	sink := newChatSink(pr, pw)
	time.Sleep(3 * time.Second)
	sink.take()

	// A chest to probe into: any container would do, and a chest holds
	// every item. It sits next to spawn, which is the chunk the probe
	// player's own connection loads - 26.2 keeps no spawn chunks, and a
	// command aimed at a chunk nobody holds is refused as unloaded.
	sendCommand(t, pw, "setblock 2 -60 2 minecraft:chest")
	waitDrained(t, pw, sink)
	sink.take()

	names := probeItemNames()
	probed := map[string]int32{}
	accepted := []string{}
	keys := map[string]int{}
	samples := map[string]string{}
	for i := 0; i < len(names); i += maxStackBatch {
		end := min(i+maxStackBatch, len(names))
		for _, name := range names[i:end] {
			sendCommand(t, pw, fmt.Sprintf(
				"item replace block 2 -60 2 container.0 with minecraft:%s 64", name))
		}
		for j, line := range maxStackReplies(t, sink, end-i) {
			name := names[i+j]
			key, args, refused, ok := maxStackReply(line)
			if !ok {
				t.Errorf("the reply for %s is not a command reply: %s", name, line)
				continue
			}
			keys[key]++
			if _, seen := samples[key]; !seen {
				samples[key] = renderNBT(mustComponent(t, line), false)
			}
			if !refused {
				probed[name] = 64
				accepted = append(accepted, name)
				continue
			}
			cap, ok := maxStackCap(args)
			if !ok {
				t.Errorf("the refusal for %s does not name a stack size: key %q args %v",
					name, key, args)
				continue
			}
			probed[name] = cap
		}
	}
	for key, n := range keys {
		t.Logf("%d replies: %s = %s", n, key, samples[key])
	}
	if len(probed) != len(names) {
		t.Fatalf("measured %d of %d items", len(probed), len(names))
	}
	if len(accepted) == len(names) {
		t.Fatalf("every one of the %d items was accepted at 64, which cannot be right", len(names))
	}
	// A cross-check on the alignment rather than on the sizes: these are
	// items whose stack size is not in question, and a reply stream that
	// slipped by one item would report its neighbour's answer against
	// them. Without it a slipped measurement is a plausible table.
	for _, want := range []struct {
		name string
		size int32
	}{
		{"stone", 64},
		{"diamond_block", 64},
		{"iron_pickaxe", 1},
		{"diamond_sword", 1},
		{"ender_pearl", 16},
		{"bucket", 16},
		{"egg", 16},
	} {
		if got := probed[want.name]; got != want.size {
			t.Errorf("%s measured %d, want %d", want.name, got, want.size)
		}
	}

	sizes := map[int32]int{}
	for _, size := range probed {
		sizes[size]++
	}
	for _, size := range sortedKeys(sizes) {
		t.Logf("%d items stack to %d", sizes[size], size)
	}

	dir := filepath.Join("..", "..", "internal", "server", "testdata", "vanilla-26.2")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, name := range names {
		fmt.Fprintf(&b, "%s\t%d\n", name, probed[name])
	}
	if err := os.WriteFile(filepath.Join(dir, "max_stack.tsv"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("measured %d items into %s", len(names), dir)
}

// maxStackProbeName is the scripted operator the probe logs in as.
const maxStackProbeName = "MaxStackProbe"

// maxStackBatch is how many probe commands are in flight before the
// replies are read. It exists to keep the probe under the real server's
// chat spam limit: a client that sends commands without pausing is
// disconnected for spam, which would truncate the measurement rather
// than fail it.
const maxStackBatch = 20

// probeItemNames is every item the real server has, sorted, with air
// left out: the registry lists it first and it is not an item anybody
// holds. The names are the registry's own, which is the list mc-go's
// item table was generated from - a probe that asked for a name the real
// server does not have would be measuring nothing.
func probeItemNames() []string {
	names := make([]string, 0, len(world.ItemNames))
	for id, name := range world.ItemNames {
		if id == 0 {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
