package blackbox

import (
	"testing"
	"time"
)

// /item replace is how a world is built from a command line, and it is
// what every fixture in this package is built with - the furnace and
// hopper worlds are assembled on the real server with it. mc-go had no
// /item until now, which is why those fixtures are built on the real
// server and handed over rather than built on both.
//
// The command is also where the item stack rules surface that nothing
// else in mc-go can see: an item's maximum stack size. A slot write
// refuses a count larger than the item stacks to, and the refusal names
// the cap, so the whole table is measurable from one command per item
// (see capture_maxstack_test.go). What this test pins is the command
// surface around it: which slots exist, what the replies say, and which
// writes are refused.
//
// TestItemCommandParity runs one script on both servers and requires the
// replies to match component for component - the translation key, the
// argument count, each argument's NBT type, and its value - so a reply
// that names the wrong slot, or sends a count as a string where the real
// server sends an int, is caught.
func TestItemCommandParity(t *testing.T) {
	const probe = "ItemParity"
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor(probe),
	}, nil)
	defer tg.cleanup()
	defer func() {
		if t.Failed() {
			vanillaLogTail(t)
		}
	}()

	want := runItemScript(t, tg.vanilla, probe)
	got := runItemScript(t, tg.mcgo, probe)
	for i := range itemScript {
		if want[i] == got[i] {
			continue
		}
		t.Errorf("%q: mc-go and vanilla disagree\n  vanilla: %s\n  mc-go:   %s",
			itemScript[i], want[i], got[i])
	}
}

// itemSlot is the player the script replaces slots on, which is the same
// name on both servers: the reply names the target, so a script that
// replaced a slot on a different player on each server would compare two
// different messages.
const itemSlot = "@s"

// itemScript is every case, in order, run against both servers. The
// blocks come first, so the same world is under the script on both: a
// position with no block entity, a container of each size, and a block
// whose slots are its own (a furnace's three).
var itemScript = []string{
	// The world the script writes into.
	"setblock 2 -60 2 minecraft:chest",
	"setblock 3 -60 3 minecraft:furnace[facing=north]",
	"setblock 4 -60 4 minecraft:hopper[facing=down]",
	"setblock 5 -60 5 minecraft:brewing_stand",

	// A stack larger than the item's own: refused, with the cap named.
	"item replace block 2 -60 2 container.0 with minecraft:iron_pickaxe 4",
	"item replace block 2 -60 2 container.0 with minecraft:ender_pearl 64",
	// ...and the largest stack the item does take.
	"item replace block 2 -60 2 container.0 with minecraft:stone 64",
	"item replace block 2 -60 2 container.0 with minecraft:ender_pearl 16",
	"item replace block 2 -60 2 container.0 with minecraft:iron_pickaxe 1",
	// One over the cap is still over the cap.
	"item replace block 2 -60 2 container.0 with minecraft:stone 65",
	"item replace block 2 -60 2 container.0 with minecraft:stone 99",
	// The count is optional, and defaults to one.
	"item replace block 2 -60 2 container.0 with minecraft:stone",
	// A count no slot will take.
	"item replace block 2 -60 2 container.0 with minecraft:stone 0",
	"item replace block 2 -60 2 container.0 with minecraft:stone -1",

	// The item has to exist.
	"item replace block 2 -60 2 container.0 with minecraft:not_an_item 1",
	"item replace block 2 -60 2 container.0 with stone 1",

	// The slot has to exist, and has to belong to the block: a furnace
	// has three slots and a chest twenty-seven, and the player slots a
	// block does not have are named the same way on both servers.
	"item replace block 2 -60 2 container.26 with minecraft:stone 1",
	"item replace block 2 -60 2 container.27 with minecraft:stone 1",
	"item replace block 2 -60 2 container.99 with minecraft:stone 1",
	"item replace block 3 -60 3 container.2 with minecraft:stone 1",
	"item replace block 3 -60 3 container.3 with minecraft:stone 1",
	"item replace block 4 -60 4 container.4 with minecraft:stone 1",
	"item replace block 4 -60 4 container.5 with minecraft:stone 1",
	// A slot whose contents are typed - a furnace's fuel, a brewing
	// stand's bottles - takes whatever it is handed: the slot filters
	// belong to the menu a player opens, and this command does not open
	// one.
	"item replace block 3 -60 3 container.1 with minecraft:stone 1",
	"item replace block 5 -60 5 container.0 with minecraft:stone 1",
	"item replace block 5 -60 5 container.3 with minecraft:stone 1",
	"item replace block 2 -60 2 weapon.mainhand with minecraft:stone 1",
	"item replace block 2 -60 2 hotbar.0 with minecraft:stone 1",
	"item replace block 2 -60 2 bogus.0 with minecraft:stone 1",

	// A position with no block entity at all, which is not the same
	// thing as a container with an empty slot.
	"item replace block 6 -60 6 container.0 with minecraft:stone 1",
	"item replace block 2 -60 2 container.0 from block 3 -60 3 container.0",

	// The player's own slots, which are the other half of the command.
	"item replace entity " + itemSlot + " hotbar.0 with minecraft:stone 64",
	"item replace entity " + itemSlot + " inventory.0 with minecraft:stone 64",
	"item replace entity " + itemSlot + " container.0 with minecraft:stone 1",
	"item replace entity " + itemSlot + " weapon.mainhand with minecraft:stone 1",
	"item replace entity " + itemSlot + " weapon.offhand with minecraft:stone 1",
	"item replace entity " + itemSlot + " armor.head with minecraft:stone 1",
	"item replace entity " + itemSlot + " armor.chest with minecraft:stone 1",
	"item replace entity " + itemSlot + " armor.legs with minecraft:stone 1",
	"item replace entity " + itemSlot + " armor.feet with minecraft:stone 1",
	"item replace entity " + itemSlot + " armor.body with minecraft:stone 1",
	"item replace entity " + itemSlot + " hotbar.9 with minecraft:stone 1",
	"item replace entity " + itemSlot + " inventory.27 with minecraft:stone 1",
	"item replace entity " + itemSlot + " enderchest.0 with minecraft:stone 1",
	"item replace entity " + itemSlot + " hotbar.0 with minecraft:iron_pickaxe 4",
	"item replace entity " + itemSlot + " bogus.0 with minecraft:stone 1",

	// The parts of the tree that are not "replace ... with": a parse
	// that stops at the token it could not take, which is what the
	// error context message repeats.
	"item",
	"item replace",
	"item replace block",
	"item replace block 2 -60 2",
	"item replace block 2 -60 2 container.0",
	"item replace block 2 -60 2 container.0 with",
	"item replace bogus 2 -60 2 container.0 with minecraft:stone 1",
	"item modify block 2 -60 2 container.0 minecraft:stone",
	"item bogus",
}

// runItemScript joins a server, runs the script, and returns one
// normalized reply per command.
func runItemScript(t *testing.T, addr, name string) []string {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, name)
	defer c.Close()
	_ = c.SetDeadline(time.Time{})
	sink := newChatSink(pr, pw)
	time.Sleep(3 * time.Second)
	sink.take()

	return runItemProbeScript(t, pw, sink, itemScript)
}
