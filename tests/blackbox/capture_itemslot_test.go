package blackbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Where the /item slot table comes from.
//
// The command takes a slot name from a fixed registry - container.0,
// hotbar.3, armor.chest, weapon.mainhand - and resolves it against the
// target: a block's container, or a player. Three things about that
// resolution are not readable anywhere mc-go can look, so they are
// measured:
//
//   - which names the registry holds at all. A name it does not know is
//     refused as "slot.unknown" with the token echoed back, and a name it
//     knows but the target has no slot for is refused as
//     "commands.item.target.no_such_slot" with a number.
//   - what that number is. It is the slot's own index in a space that is
//     not any container mc-go models: a chest reports 27 for a 28th
//     container slot, and 98 for weapon.mainhand, which a chest cannot
//     hold at all.
//   - which writes a slot refuses. The armor slots refuse an item that is
//     not armor ("commands.item.target.no_changed.known_item", with the
//     item and the slot's number), and the head slot is measured
//     separately because it behaves differently from the other four.
//
//	MCGO_BLACKBOX=1 MCGO_CAPTURE=1 go test ./tests/blackbox -run TestCaptureVanillaItemSlots
//
// The recording is committed as
// internal/server/testdata/vanilla-26.2/item_slots.tsv, one
// "label<TAB>reply" line per probe, and the table mc-go ships is written
// from it by hand - the file is the evidence the code cites.
func TestCaptureVanillaItemSlots(t *testing.T) {
	if os.Getenv("MCGO_CAPTURE") != "1" {
		t.Skip("set MCGO_CAPTURE=1 to record vanilla /item slot behavior")
	}
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor(itemSlotProbeName),
	}, nil)
	defer tg.cleanup()

	c, pr, pw := joinPlayBlackbox(t, tg.vanilla, itemSlotProbeName)
	defer c.Close()
	_ = c.SetDeadline(time.Time{})
	sink := newChatSink(pr, pw)
	time.Sleep(3 * time.Second)
	sink.take()

	// The world the probes run against: one of each container shape,
	// next to spawn so the probe player's own connection is holding the
	// chunks the commands touch.
	for _, b := range []string{
		"setblock 2 -60 2 minecraft:chest",
		"setblock 3 -60 3 minecraft:furnace[facing=north]",
		"setblock 4 -60 4 minecraft:hopper[facing=down]",
		"setblock 5 -60 5 minecraft:brewing_stand",
	} {
		sendCommand(t, pw, b)
	}
	waitDrained(t, pw, sink)
	sink.take()

	cases := itemSlotProbeCases()
	cmds := make([]string, len(cases))
	for i, tc := range cases {
		cmds[i] = tc.cmd
	}
	replies := runItemProbeScript(t, pw, sink, cmds)

	dir := filepath.Join("..", "..", "internal", "server", "testdata", "vanilla-26.2")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# /item slot behavior, measured on the real server.\n")
	fmt.Fprintf(&b, "# label<TAB>reply, written by TestCaptureVanillaItemSlots.\n")
	for i, tc := range cases {
		fmt.Fprintf(&b, "%s\t%s\n", tc.label, replies[i])
	}
	path := filepath.Join(dir, "item_slots.tsv")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("recorded %d probes into %s", len(cases), path)
}

// itemSlotProbeName is the scripted operator the probe logs in as.
const itemSlotProbeName = "ItemSlotProbe"

// itemSlotProbeCase is one probe: a label naming what it asks about,
// and the command that asks it.
type itemSlotProbeCase struct {
	label string
	cmd   string
}

// itemSlotNames is the slot names the probe asks about, block target
// first and player target second: the numbered families at their edges,
// every equipment slot, and the names of the menus whose targets are not
// the ones being probed.
var itemSlotNames = []string{
	"container.0", "container.26", "container.27", "container.53", "container.54", "container.99",
	"inventory.0", "inventory.26", "inventory.27",
	"hotbar.0", "hotbar.8", "hotbar.9",
	"enderchest.0", "enderchest.26", "enderchest.27",
	"armor.head", "armor.chest", "armor.legs", "armor.feet", "armor.body",
	"weapon.mainhand", "weapon.offhand",
	"player.cursor", "player.crafting.0", "player.crafting.9",
	"villager.0", "villager.8",
	"horse.0", "horse.saddle", "horse.armor", "horse.chest.0", "horse.chest.14",
	"merchant.0", "merchant.2",
	"bogus.0", "container", "container.0.5", "container.-1", "minecraft:container.0",
}

// itemSlotProbeCases builds the probe script: every name against a chest
// and against the player, a second container of each other shape so the
// slot count that decides "no such slot" is measured rather than
// assumed, and the writes whose refusals are the other half of the
// command.
func itemSlotProbeCases() []itemSlotProbeCase {
	var out []itemSlotProbeCase
	add := func(label, cmd string) {
		out = append(out, itemSlotProbeCase{label: label, cmd: cmd})
	}

	// Every name against a chest: a block whose container the command
	// can write, so a name it accepts is told apart from one it does not
	// hold, and one the chest is too small for reports its own index.
	for _, s := range itemSlotNames {
		add("chest/"+s, "item replace block 2 -60 2 "+s+" with minecraft:stone 1")
		add("chest/clear/"+s, "item replace block 2 -60 2 "+s+" with minecraft:air")
	}
	// The other container shapes at the slot that is one past their end,
	// which is what pins the index the refusal names to the target's own
	// size rather than to the name.
	for _, q := range []struct {
		label, target string
		last, past    int
	}{
		{"furnace", "3 -60 3", 2, 3},
		{"hopper", "4 -60 4", 4, 5},
		{"brewing", "5 -60 5", 4, 5},
	} {
		for _, n := range []int{q.last, q.past} {
			add(fmt.Sprintf("%s/container.%d", q.label, n),
				fmt.Sprintf("item replace block %s container.%d with minecraft:stone 1", q.target, n))
		}
	}

	// Every name against the player, which is the target the equipment
	// slots belong to.
	add("player/clear/inventory", "item replace entity @s container.0 with minecraft:air")
	for _, s := range itemSlotNames {
		add("player/"+s, "item replace entity @s "+s+" with minecraft:stone 1")
		add("player/clear/"+s, "item replace entity @s "+s+" with minecraft:air")
	}

	// Which writes a slot refuses: the armor slots against armor that
	// fits and armor that does not, the head slot separately, and the
	// equipment slots against a block.
	for _, s := range []string{"armor.head", "armor.chest", "armor.legs", "armor.feet", "armor.body"} {
		for _, item := range []string{"minecraft:iron_helmet", "minecraft:iron_chestplate",
			"minecraft:iron_leggings", "minecraft:iron_boots", "minecraft:wolf_armor", "minecraft:diamond"} {
			add("filter/"+s+"/"+item,
				"item replace entity @s "+s+" with "+item+" 1")
			add("filter/clear/"+s, "item replace entity @s "+s+" with minecraft:air")
		}
	}
	for _, s := range []string{"weapon.mainhand", "weapon.offhand"} {
		add("filter/"+s, "item replace entity @s "+s+" with minecraft:stone 64")
		add("filter/clear/"+s, "item replace entity @s "+s+" with minecraft:air")
	}

	// Where the player's own container ends. The numbered names the
	// player accepts resolve into one inventory, and the index the
	// refusal names says which slot it is: this walks the boundary, so
	// the four armor slots and the offhand are pinned to the indices
	// they answer to and the first index past the inventory is measured
	// rather than assumed.
	for _, n := range []int{35, 36, 37, 38, 39, 40, 41, 42, 52} {
		add(fmt.Sprintf("player/container.%d", n),
			fmt.Sprintf("item replace entity @s container.%d with minecraft:stone 1", n))
		add(fmt.Sprintf("player/clear/container.%d", n),
			fmt.Sprintf("item replace entity @s container.%d with minecraft:air", n))
	}
	for _, q := range []struct {
		n     int
		item  string
		label string
	}{
		{36, "minecraft:iron_boots", "feet"},
		{37, "minecraft:iron_leggings", "legs"},
		{38, "minecraft:iron_chestplate", "chest"},
		{39, "minecraft:iron_helmet", "head"},
		{40, "minecraft:stone", "offhand"},
	} {
		add("inventory/"+q.label, fmt.Sprintf("item replace entity @s container.%d with %s 1", q.n, q.item))
		add("inventory/clear/"+q.label, fmt.Sprintf("item replace entity @s container.%d with minecraft:air", q.n))
	}

	// What "no change" means: the same stack twice, the same item at a
	// different count, a different item, and emptying a slot that is
	// already empty.
	add("same/first", "item replace entity @s hotbar.0 with minecraft:stone 64")
	add("same/again", "item replace entity @s hotbar.0 with minecraft:stone 64")
	add("same/count", "item replace entity @s hotbar.0 with minecraft:stone 32")
	add("same/item", "item replace entity @s hotbar.0 with minecraft:dirt 1")
	add("same/air", "item replace entity @s hotbar.0 with minecraft:air")
	add("same/air-again", "item replace entity @s hotbar.0 with minecraft:air")

	// Which name a numbered family means: the same slot reached by two
	// names reports the item that is already there. container.0 is the
	// first slot of the player's own container, and hotbar.0 is the
	// first hotbar slot, and whether they are the same slot is what the
	// reply says.
	add("alias/set-container", "item replace entity @s container.0 with minecraft:stone 1")
	add("alias/read-hotbar", "item replace entity @s hotbar.0 with minecraft:air")
	add("alias/set-hotbar", "item replace entity @s hotbar.0 with minecraft:stone 1")
	add("alias/read-container", "item replace entity @s container.0 with minecraft:air")
	add("alias/read-inventory", "item replace entity @s inventory.0 with minecraft:air")
	return out
}
