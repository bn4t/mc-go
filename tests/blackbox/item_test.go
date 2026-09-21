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

	// The reply's brackets component takes the item's effective rarity
	// color: epic light_purple, rare aqua, uncommon yellow, and an
	// enchanted common item bumped to rare. A custom_name is the
	// italic literal hover name.
	"item replace entity @s weapon.mainhand with minecraft:enchanted_golden_apple",
	"item replace entity @s weapon.mainhand with minecraft:beacon",
	"item replace entity @s weapon.mainhand with minecraft:angler_pottery_sherd",
	"item replace entity @s weapon.mainhand with minecraft:bow[minecraft:enchantments={\"minecraft:power\":1}]",
	"item replace entity @s weapon.mainhand with minecraft:dirt[minecraft:custom_name='Cool Dirt']",
	"item replace entity @s weapon.mainhand with minecraft:iron_pickaxe[minecraft:damage=42]",
	"item replace entity @s weapon.mainhand with minecraft:air",
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

	// "replace ... from", and the writes a target refuses: a slot the
	// player does not have fails naming the item it would have held.
	"item replace entity " + itemSlot + " container.40 with minecraft:iron_pickaxe 4",
	"item replace entity " + itemSlot + " container.40 from block 2 -60 2 container.0",
	"item replace entity " + itemSlot + " armor.chest from block 2 -60 2 container.0",
	"item replace entity " + itemSlot + " container.1 from block 2 -60 2 container.0 [] extra",
	"item replace block 2 -60 2 container.0 from block 2 -60 2 container.0",
	"item replace entity " + itemSlot + " container.0 from block 2 -60 2 container.0 extra",
	"item replace entity " + itemSlot + " container.0 from block 2 -60 2 container.0 {}",
	"item replace entity " + itemSlot + " container.0 from block 2 -60 2 container.0 []",
	"item replace block 2 -60 2 container.0 from",
	"item replace entity " + itemSlot + " enderchest.0 with minecraft:iron_pickaxe 4",

	// Counts and ids: air stacks to one, a sign is fine, a leading
	// zero is not a number, and unknown ids echo what was read.
	"item replace block 2 -60 2 container.0 with minecraft:air 1",
	"item replace block 2 -60 2 container.0 with minecraft:air 5",
	"item replace block 2 -60 2 container.0 with minecraft:air 0",
	"item replace block 2 -60 2 container.0 with minecraft:stone +5",
	"item replace block 2 -60 2 container.0 with minecraft:iron_pickaxe 0",
	"item replace block 2 -60 2 container.0 with minecraft:iron_pickaxe 100",
	"item replace block 2 -60 2 container.0 with foO:bar 1",
	"item replace block 2 -60 2 container.0 with foo:BAR 1",
	"item replace block 2 -60 2 container.0 with STONE 1",
	"item replace block 2 -60 2 container.0 with minecraft:stone 01",
	"item replace block 2 -60 2 Container.0 with minecraft:stone 1",
	"item replace entity " + itemSlot + " HOTBAR.0 with minecraft:stone 1",

	// "item modify": the modifier is an id the item_modifier registry
	// does not hold, or NBT for a loot function. A slot the target
	// does not have fails naming only the slot.
	"item replace entity " + itemSlot + " hotbar.0 with minecraft:stone 30",
	"item modify entity " + itemSlot + " hotbar.0 EXTRA",
	"item modify entity " + itemSlot + " STONE",
	"item modify entity " + itemSlot + " foo:BAR",
	"item modify entity " + itemSlot + " minecraft:",
	"item modify entity " + itemSlot + " {}",
	"item modify entity " + itemSlot + " []",
	"item modify entity " + itemSlot + " [{}]",
	"item modify entity " + itemSlot + " [1,2]",
	"item modify entity " + itemSlot + " [1,2,3]",
	"item modify entity " + itemSlot + " [{},1]",
	"item modify entity " + itemSlot + " [1,{}]",
	"item modify entity " + itemSlot + " [[1]]",
	"item modify entity " + itemSlot + " [X]",
	"item modify entity " + itemSlot + " {foo}",
	"item modify entity " + itemSlot + " {foo:bar}",
	"item modify entity " + itemSlot + " {foo:}",
	"item modify entity " + itemSlot + " {a:}",
	"item modify entity " + itemSlot + " {a:1",
	"item modify entity " + itemSlot + " {a:1]",
	"item modify entity " + itemSlot + " {a:1,",
	"item modify entity " + itemSlot + " {a:1,}",
	"item modify entity " + itemSlot + " {a:b",
	"item modify entity " + itemSlot + " {a:b!}",
	"item modify entity " + itemSlot + ` {a:"b"`,
	"item modify entity " + itemSlot + ` {a:"b"!}`,
	"item modify entity " + itemSlot + " {a:{}!}",
	"item modify entity " + itemSlot + " {a:[]!}",
	"item modify entity " + itemSlot + ` {a:"b}`,
	"item modify entity " + itemSlot + " {z:1,a:2}",
	"item modify entity " + itemSlot + " {A:1,a:2}",
	"item modify entity " + itemSlot + " {a:{}}",
	"item modify entity " + itemSlot + " {a:[]}",
	"item modify entity " + itemSlot + " {a:+1}",
	"item modify entity " + itemSlot + ` {a:"x\ny"}`,
	"item modify entity " + itemSlot + " {function:minecraft:x}",
	"item modify entity " + itemSlot + " [{function:minecraft:x}]",
	"item modify entity " + itemSlot + " [{function:minecraft:x},1]",
	"item modify entity " + itemSlot + " {function:{}}",
	"item modify entity " + itemSlot + " [{function:2}]",
	"item modify entity " + itemSlot + ` [{function:"minecraft:set_count"}]`,
	"item modify entity " + itemSlot + ` {a:{function:"minecraft:x"}}`,
	"item modify entity " + itemSlot + " [1,",
	"item modify entity " + itemSlot + " [1,]",
	"item modify entity " + itemSlot + " [1,,2]",
	"item modify entity " + itemSlot + " [,]",
	"item modify entity " + itemSlot + " [1X]",
	"item modify entity " + itemSlot + " [1e]",
	"item modify entity " + itemSlot + " [1e+]",
	"item modify entity " + itemSlot + " [1eX]",
	"item modify entity " + itemSlot + " [1e999]",
	"item modify entity " + itemSlot + " [007]",
	"item modify entity " + itemSlot + " [00.5]",
	"item modify entity " + itemSlot + " [007.5]",
	"item modify entity " + itemSlot + " [0]",
	"item modify entity " + itemSlot + " [1e3]",
	"item modify entity " + itemSlot + " [1E3F]",
	"item modify entity " + itemSlot + " [1.5]",
	"item modify entity " + itemSlot + " [1.5f]",
	"item modify entity " + itemSlot + " [1.5D]",
	"item modify entity " + itemSlot + " [1F]",
	"item modify entity " + itemSlot + " [1B]",
	"item modify entity " + itemSlot + " [1S]",
	"item modify entity " + itemSlot + " [1d]",
	"item modify entity " + itemSlot + " [2.]",
	"item modify entity " + itemSlot + " [.5]",
	"item modify entity " + itemSlot + " [true]",
	"item modify entity " + itemSlot + " [false]",
	"item modify entity " + itemSlot + " [true!]",
	"item modify entity " + itemSlot + " [trueX]",
	"item modify entity " + itemSlot + " [1.2.3]",
	"item modify entity " + itemSlot + " [1bX]",
	"item modify entity " + itemSlot + " [1sX]",
	"item modify entity " + itemSlot + " [1dX]",
	"item modify entity " + itemSlot + " [1e3X]",
	"item modify entity " + itemSlot + " [1.5s]",
	"item modify entity " + itemSlot + " [1e3s]",
	"item modify entity " + itemSlot + " [-abc]",
	"item modify entity " + itemSlot + " [-]",
	"item modify entity " + itemSlot + " [+]",
	"item modify entity " + itemSlot + " [.abc]",
	"item modify entity " + itemSlot + " [+.5]",
	"item modify entity " + itemSlot + " [B;1,2]",
	"item modify entity " + itemSlot + " [I;1,2]",
	"item modify entity " + itemSlot + " [L;1,2]",
	"item modify entity " + itemSlot + " [B;1.5]",
	"item modify entity " + itemSlot + " [b;1]",
	"item modify entity " + itemSlot + " [B;x]",
	"item modify entity " + itemSlot + " [B;+]",
	"item modify entity " + itemSlot + " [B;+1]",
	"item modify entity " + itemSlot + " [B;-1]",
	"item modify entity " + itemSlot + " [B;1s]",
	"item modify entity " + itemSlot + " [B;1f]",
	"item modify entity " + itemSlot + " [B;1b]",
	"item modify entity " + itemSlot + " [B;1B]",
	"item modify entity " + itemSlot + " [B;1i]",
	"item modify entity " + itemSlot + " [B;1,]",
	"item modify entity " + itemSlot + " [B;1bX]",
	"item modify entity " + itemSlot + " [I;1s]",
	"item modify entity " + itemSlot + " [I;1b]",
	"item modify entity " + itemSlot + " [I;1i]",
	"item modify entity " + itemSlot + " [I;1L]",
	"item modify entity " + itemSlot + " [I;1f]",
	"item modify entity " + itemSlot + " [L;1s]",
	"item modify entity " + itemSlot + " [L;1b]",
	"item modify entity " + itemSlot + " [L;1i]",
	"item modify entity " + itemSlot + " [L;1l]",
	"item modify entity " + itemSlot + " [L;1lX]",
	"item modify entity " + itemSlot + " [I;1s,2]",
	"item modify entity " + itemSlot + " [B;1b,2b]",
	"item modify entity " + itemSlot + " [I;1B]",
	"item modify entity " + itemSlot + " [I;1S]",
	"item modify entity " + itemSlot + " [B;1S]",
	"item modify entity " + itemSlot + " [L;1B]",
	"item modify entity " + itemSlot + " [L;1I]",
	"item modify entity " + itemSlot + " [B;1I]",
	"item modify entity " + itemSlot + " [I;1x]",
	"item modify entity " + itemSlot + " [I;1d]",
	"item modify entity " + itemSlot + " [L;1f]",
	"item modify entity " + itemSlot + " [B;1d]",
	"item modify entity " + itemSlot + " [B;1e3]",
	"item modify entity " + itemSlot + " [L;1.5]",
	"item modify entity " + itemSlot + " [I;1.5]",
	"item modify entity " + itemSlot + " [I;1e3]",
	"item modify entity " + itemSlot + " [B;1b",
	"item modify entity " + itemSlot + " [1s",
	"item modify entity " + itemSlot + " [1b",
	"item modify entity " + itemSlot + " [1S",
	"item modify entity " + itemSlot + " [1f",
	"item modify entity " + itemSlot + " [1L",
	"item modify entity " + itemSlot + " [1.5",
	"item modify entity " + itemSlot + " [1e3",
	"item modify entity " + itemSlot + " [1.5e3",
	"item modify entity " + itemSlot + " [1e3f]",
	"item modify entity " + itemSlot + " [1e3d]",
	"item modify entity " + itemSlot + " [1.5E3D]",
	"item modify entity " + itemSlot + " [1.5L]",
	"item modify entity " + itemSlot + " [.]",
	"item modify entity " + itemSlot + " [-5abc]",
	"item modify entity " + itemSlot + " [-abc!]",
	"item modify entity " + itemSlot + " [1e,1]",
	"item modify entity " + itemSlot + " [-abc,1]",
	"item modify entity " + itemSlot + " [X",
	"item modify entity " + itemSlot + ` ["b"`,
	"item modify entity " + itemSlot + " [{}]",
	"item modify entity " + itemSlot + " {a:[",
	"item modify entity " + itemSlot + " [%]",
	"item modify entity " + itemSlot + " {,}",
	"item modify entity " + itemSlot + " {",
	"item modify entity " + itemSlot + " {a:,}",
	"item modify entity " + itemSlot + " {a::}",
	"item modify entity " + itemSlot + " {a:1e3!}",
	"item modify entity " + itemSlot + " {a:1.5!}",
	"item modify entity " + itemSlot + " []x",
	"item modify entity " + itemSlot + " {}y",
	"item modify entity " + itemSlot + " {a:1}x",
	"item modify entity " + itemSlot + " [] extra",
	"item modify entity " + itemSlot + " container.40 []",
	"item modify block 2 -60 2 container.0 []",
	"item modify block 3 -60 3 container.9 []",
	"item modify block 7 -60 7 container.0 []",
	"item modify entity Nobody hotbar.0 []",
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
