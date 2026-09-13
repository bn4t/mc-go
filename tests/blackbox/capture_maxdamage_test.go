package blackbox

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"mc-go/internal/nbt"
)

// Where every item's maximum damage (durability) comes from.
//
// The number is a property of the item and nothing mc-go can read: like
// the stack sizes it is measured, the same way everything else here is:
// put the item in a slot, wear it to nothing with set_damage 0.0 (the
// fraction is remaining durability, so 0.0 leaves damage at the max),
// and read the damage component back with /data get.
//
//	MCGO_BLACKBOX=1 MCGO_CAPTURE=1 go test ./tests/blackbox -run TestCaptureVanillaMaxDamage
//
// MCGO_DAMAGE_SMOKE=1 limits the run to twelve items and dumps every
// reply without writing the file, for validating the reply shapes.
//
// The recording is committed as internal/server/testdata/vanilla-26.2/max_damage.tsv
// and turned into the table mc-go ships by tools/gendamage, so the only
// thing that ever runs at server runtime is Go code.
func TestCaptureVanillaMaxDamage(t *testing.T) {
	if os.Getenv("MCGO_CAPTURE") != "1" {
		t.Skip("set MCGO_CAPTURE=1 to record vanilla max damage values")
	}
	smoke := os.Getenv("MCGO_DAMAGE_SMOKE") == "1"
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor(maxDamageProbeName),
	}, nil)
	defer tg.cleanup()

	c, pr, pw := joinPlayBlackbox(t, tg.vanilla, maxDamageProbeName)
	defer c.Close()
	_ = c.SetDeadline(time.Time{})
	sink := newChatSink(pr, pw)
	time.Sleep(3 * time.Second)
	sink.take()

	sendCommand(t, pw, "setblock 2 -60 2 minecraft:chest")
	waitDrained(t, pw, sink)
	sink.take()

	names := probeItemNames()
	if smoke {
		names = []string{
			"iron_pickaxe", "diamond_sword", "bow", "fishing_rod",
			"shears", "elytra", "iron_helmet", "diamond_hoe",
			"stone", "dirt", "apple", "stick",
		}
	}
	probed := map[string]int32{}
	keys := map[string]int{}
	samples := map[string]string{}
	const batchItems = 6
	for i := 0; i < len(names); i += batchItems {
		end := min(i+batchItems, len(names))
		for _, name := range names[i:end] {
			sendCommand(t, pw, fmt.Sprintf(
				"item replace block 2 -60 2 container.0 with minecraft:%s 1", name))
			sendCommand(t, pw, `item modify block 2 -60 2 container.0 {"function":"set_damage","damage":0.0}`)
			sendCommand(t, pw, `data get block 2 -60 2 Items[0].components."minecraft:damage"`)
		}
		replies := maxStackReplies(t, sink, 3*(end-i))
		for j := range names[i:end] {
			rpl, mod, get := replies[3*j], replies[3*j+1], replies[3*j+2]
			name := names[i+j]
			for _, line := range []string{rpl, mod, get} {
				key, _, _, ok := maxStackReply(line)
				if !ok {
					t.Errorf("the reply for %s is not a command reply: %s", name, line)
					continue
				}
				keys[key]++
				if _, seen := samples[key]; !seen {
					samples[key] = renderNBT(mustComponent(t, line), false)
				}
			}
			dmg, ok := maxDamageValue(get)
			if !ok {
				if smoke {
					t.Logf("%s: get unparsed: %s", name, renderNBT(mustComponent(t, get), false))
				}
				probed[name] = -1
				continue
			}
			probed[name] = dmg
		}
	}
	keyList := make([]string, 0, len(keys))
	for key := range keys {
		keyList = append(keyList, key)
	}
	sort.Strings(keyList)
	for _, key := range keyList {
		t.Logf("%d replies: %s = %s", keys[key], key, samples[key])
	}
	if smoke {
		for _, name := range names {
			t.Logf("smoke %s -> %d", name, probed[name])
		}
		return
	}
	if len(probed) != len(probeItemNames()) {
		t.Fatalf("measured %d of %d items", len(probed), len(probeItemNames()))
	}
	for _, want := range []struct {
		name string
		dmg  int32
	}{
		{"iron_pickaxe", 250},
		{"diamond_sword", 1561},
		{"bow", 384},
		{"stone", -1},
		{"dirt", -1},
		{"apple", -1},
	} {
		if got := probed[want.name]; got != want.dmg {
			t.Errorf("%s measured %d, want %d", want.name, got, want.dmg)
		}
	}
	n := 0
	for _, dmg := range probed {
		if dmg > 0 {
			n++
		}
	}
	t.Logf("%d of %d items take damage", n, len(probed))

	dir := filepath.Join("..", "..", "internal", "server", "testdata", "vanilla-26.2")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, name := range probeItemNames() {
		fmt.Fprintf(&b, "%s\t%d\n", name, probed[name])
	}
	if err := os.WriteFile(filepath.Join(dir, "max_damage.tsv"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("measured %d items into %s", len(probed), dir)
}

// maxDamageProbeName is the scripted operator the probe logs in as.
const maxDamageProbeName = "MaxDamageProbe"

// maxDamageValue reads the damage out of a /data get reply. The query
// succeeds with the found value as its last argument when the slot has
// a damage component and fails without one, so ok=false means the item
// does not take damage. Only the query reply is read; the replace and
// modify replies are consumed for alignment upstream.
func maxDamageValue(line string) (int32, bool) {
	key, args, refused, ok := maxStackReply(line)
	if !ok || refused {
		return -1, false
	}
	if key != "commands.data.block.query" || len(args) == 0 {
		return -1, false
	}
	// The found value rides as gold text in the last argument's extra,
	// e.g. {"extra": [{"color": "gold", "text": "250"}], "text": ""}:
	// the query renders NBT as a string, never as a bare number, and
	// the block coordinates in the earlier arguments must not be read
	// as the answer.
	last, ok := args[len(args)-1].(map[string]any)
	if !ok {
		return -1, false
	}
	extra, ok := last["extra"].(nbt.List)
	if !ok || len(extra.Items) == 0 {
		return -1, false
	}
	first, ok := extra.Items[0].(map[string]any)
	if !ok {
		return -1, false
	}
	text, ok := first["text"].(string)
	if !ok {
		return -1, false
	}
	var dmg int
	if _, err := fmt.Sscanf(text, "%d", &dmg); err != nil || dmg <= 0 {
		return -1, false
	}
	return int32(dmg), true
}
