//go:build blackbox

package blackbox

import (
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

// TestItemMobParity covers /item's entity form against world entities:
// weapon/armor/body/saddle slots accept any stack with no item filter
// (wolf_armor on a zombie, a saddle on one too), multi-entity selectors
// aggregate into the ...multiple reply, `from` copies mob-to-mob and
// mob-to-player, non-living entities and out-of-table slots fail
// no_changed, and the writes read back through `data get equipment.*`.
func TestItemMobParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	z := "@e[type=minecraft:husk,limit=1,x=4,y=-60,z=4,distance=..3]"
	zAll := "@e[type=minecraft:husk]"
	w := "@e[type=minecraft:wolf,limit=1]"
	p := "@e[type=minecraft:pig,limit=1]"
	dropped := "@e[type=minecraft:item,limit=1]"

	cmds := []string{
		`summon minecraft:husk 4.5 -60 4.5 {NoAI:1b}`,
		`summon minecraft:husk 6.5 -60 6.5 {NoAI:1b}`,
		`summon minecraft:wolf 8.5 -60 8.5 {NoAI:1b}`,
		`summon minecraft:pig 10.5 -60 10.5 {NoAI:1b}`,
		`summon minecraft:item 12.5 -60 12.5 {Item:{id:"minecraft:dirt",count:1b}}`,
		"item replace entity " + z + " weapon.mainhand with minecraft:iron_sword",
		"item replace entity " + z + " armor.chest with minecraft:diamond_chestplate",
		"item replace entity " + z + " armor.body with minecraft:wolf_armor",
		"item replace entity " + z + " saddle with minecraft:saddle",
		"item replace entity " + w + " armor.body with minecraft:wolf_armor",
		"item replace entity " + w + " armor.head with minecraft:iron_helmet",
		"item replace entity " + p + " saddle with minecraft:saddle",
		"item replace entity " + p + " armor.head with minecraft:iron_helmet",
		"item replace entity " + zAll + " weapon.offhand with minecraft:shield",
		"item modify entity " + z + ` weapon.mainhand {"function":"minecraft:set_count","count":2}`,
		"item replace entity " + z + " armor.head from entity " + z + " weapon.mainhand",
		"item replace entity " + z + " container.0 with minecraft:dirt",
		"item replace entity " + z + " bogus.slot with minecraft:dirt",
		"item replace entity " + z + " weapon.mainhand",
		"item replace entity " + zAll + " weapon.mainhand with minecraft:iron_sword 2",
		"item replace entity " + dropped + " weapon.mainhand with minecraft:dirt",
		"item replace entity " + z + " weapon.mainhand with minecraft:air",
		"item modify entity " + p + " armor.head {\"function\":\"minecraft:set_damage\",\"damage\":0.5}",
		"data get entity " + z + " equipment",
		"data get entity " + w + " equipment.body",
		"data get entity " + p + " equipment.saddle",
		"data get entity " + p + " equipment.head",
	}

	run := func(addr string) [][]string {
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSink(pr, pw)
		sink.cap = 1 << 20
		time.Sleep(400 * time.Millisecond)
		var out [][]string
		for i, cmd := range cmds {
			sink.take()
			t.Logf("send %d %q", i, cmd)
			func() { defer func() { recover() }(); sendCommand(t, pw, cmd) }()
			time.Sleep(500 * time.Millisecond)
			var evs []string
			for _, line := range sink.take() {
				if tree, ok := componentTree(line, true); ok {
					evs = append(evs, "chat "+maskUUID(tree))
				}
			}
			out = append(out, evs)
		}
		return out
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	for i, cmd := range cmds {
		if len(want[i]) != len(got[i]) {
			t.Errorf("cmd %q: vanilla %v\n  mc-go %v", cmd, want[i], got[i])
			continue
		}
		for j := range want[i] {
			if want[i][j] != got[i][j] {
				t.Errorf("cmd %q event %d:\n  vanilla %s\n  mc-go   %s", cmd, j, want[i][j], got[i][j])
			}
		}
	}
}

// TestMobWeaponWearParity pins the mob-melee durability model: a
// husk swinging an iron sword at an armored survival player lands
// hits (the target's health drops) yet the weapon stays pristine —
// weapon wear lives in ItemStack.postHurtEnemy, which only the
// player attack path calls; Mob.doHurtTarget's hurtEnemy is an
// empty hook and LivingEntity.hurtArmor is a no-op off the player
// class, so mob equipment is never worn down by combat.
func TestMobWeaponWearParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	husk := `@e[type=minecraft:husk,limit=1]`

	run := func(addr string) (equip, health string) {
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSink(pr, pw)
		sink.cap = 1 << 20
		time.Sleep(400 * time.Millisecond)
		// Armor 30 shaves husk hits to ~0.5 so the player stays alive
		// and in reach; cramming off so the slime swarm can't kill
		// the husk mid-measurement.
		sendCommand(t, pw, "gamemode survival @s")
		sendCommand(t, pw, "attribute @s minecraft:armor base set 30")
		sendCommand(t, pw, "gamerule max_entity_cramming 0")
		sendCommand(t, pw, "summon minecraft:husk 5.5 -60 5.5 {PersistenceRequired:1b}")
		sendCommand(t, pw, "item replace entity "+husk+" weapon.mainhand with minecraft:iron_sword")
		sendCommand(t, pw, "tp @s 6.5 -60 5.5")
		time.Sleep(5 * time.Second)
		equip = dataGetLine(t, sink, pw, husk, "equipment.mainhand")
		health = dataGetLine(t, sink, pw, "@s", "Health")
		return equip, health
	}

	wantEquip, wantHealth := run(targets.vanilla)
	gotEquip, gotHealth := run(targets.mcgo)

	if lastNumber(wantHealth) >= 20 {
		t.Fatalf("no husk hits measured on vanilla (Health %v): %q", wantHealth, wantHealth)
	}
	if strings.Contains(wantEquip, "damage") {
		t.Fatalf("vanilla wore the mob weapon — measurement changed: %q", wantEquip)
	}
	wantTree, okW := componentTree(wantEquip, true)
	gotTree, okG := componentTree(gotEquip, true)
	if !okW || !okG || maskUUID(wantTree) != maskUUID(gotTree) {
		t.Errorf("mainhand equipment after melee:\n  vanilla %s\n  mc-go   %s", wantTree, gotTree)
	}
	if lastNumber(gotHealth) >= 20 {
		t.Errorf("mc-go husk never hit the player: Health %v", gotHealth)
	}
}
