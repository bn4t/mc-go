//go:build blackbox

package blackbox

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
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

// TestMobBodyArmorParity pins the wolf-armor absorb model and the
// body-slot wear rules measured on vanilla: while a wolf's body slot
// holds wolf_armor and the damage type isn't in bypasses_wolf_armor,
// Wolf.actuallyHurt folds the whole hit into the armor —
// hurtAndBreak(ceil(amount)), wolf health untouched. A bypassing
// type (magic) instead runs the normal armor formula, where the
// armor attribute still absorbs and hurtArmor wears the body piece
// max(1, raw/4). Horse armor's equippable.damage_on_hurt is false —
// a horse's armor never wears. Non-wolf mobs holding wolf_armor
// absorb via the armor attribute but never wear either.
func TestMobBodyArmorParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	wolf := `@e[type=minecraft:wolf,limit=1]`
	husk := `@e[type=minecraft:husk,limit=1]`
	horse := `@e[type=minecraft:horse,limit=1]`

	dmgPath := `equipment.body.components."minecraft:damage"`

	run := func(addr string) []string {
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSink(pr, pw)
		sink.cap = 1 << 20
		time.Sleep(400 * time.Millisecond)
		for _, cmd := range []string{
			"summon minecraft:wolf 4.5 -60 4.5 {NoAI:1b}",
			"summon minecraft:husk 6.5 -60 6.5 {NoAI:1b}",
			"summon minecraft:horse 8.5 -60 8.5 {NoAI:1b}",
			"item replace entity " + wolf + " armor.body with minecraft:wolf_armor",
			"item replace entity " + husk + " armor.body with minecraft:wolf_armor",
			"item replace entity " + horse + " armor.body with minecraft:diamond_horse_armor",
		} {
			func() { defer func() { recover() }(); sendCommand(t, pw, cmd) }()
			time.Sleep(400 * time.Millisecond)
		}
		// mob_attack absorbs (8+12 wear on the wolf's armor, zero
		// health lost); thorns bypasses wolf armor but not the
		// armor tag — normal formula, /4 wear. Each spaced past
		// the 20-tick grace.
		sendCommand(t, pw, "damage "+wolf+" 8 minecraft:mob_attack")
		time.Sleep(1200 * time.Millisecond)
		sendCommand(t, pw, "damage "+husk+" 8 minecraft:mob_attack")
		time.Sleep(1200 * time.Millisecond)
		sendCommand(t, pw, "damage "+horse+" 8 minecraft:mob_attack")
		time.Sleep(1200 * time.Millisecond)
		sendCommand(t, pw, "damage "+wolf+" 12 minecraft:mob_attack")
		time.Sleep(1200 * time.Millisecond)
		sendCommand(t, pw, "damage "+wolf+" 8 minecraft:thorns")
		time.Sleep(1200 * time.Millisecond)
		var out []string
		for _, sel := range []string{wolf, husk, horse} {
			out = append(out, dataGetLine(t, sink, pw, sel, dmgPath))
			out = append(out, dataGetLine(t, sink, pw, sel, "equipment.body"))
		}
		// Horse Health is a rolled 15-30 attribute at spawn — only
		// wolf/husk get a verbatim Health leg.
		out = append(out, dataGetLine(t, sink, pw, wolf, "Health"))
		out = append(out, dataGetLine(t, sink, pw, husk, "Health"))
		return out
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	for i := range want {
		if want[i] == got[i] {
			continue
		}
		wt, okW := componentTree(want[i], true)
		gt, okG := componentTree(got[i], true)
		if !okW || !okG || maskUUID(wt) != maskUUID(gt) {
			t.Errorf("reply %d:\n  vanilla %s\n  mc-go   %s", i, want[i], got[i])
		}
	}
	// Anchor: wolf armor wear = 8+12+max(1,8/4) = 22.
	if lastNumber(want[0]) != 22 {
		t.Errorf("vanilla wolf armor wear not 22: %q", want[0])
	}
}

// TestWolfArmorFeedbackParity pins the wire feedback of wolf-armor
// hits, measured on vanilla and decompiled in Wolf.actuallyHurt +
// LivingEntity.playHurtSound: the absorb path plays
// item.wolf_armor.damage as the hurt sound, and a crackiness-level
// transition (remaining fraction crossing 0.95/0.69/0.32) adds
// item.wolf_armor.crack plus an armadillo_scute ITEM particle burst
// (count 20, spread 0.2/0.1/0.2, speed 0.1 at y+1). A bypass hit
// (thorns) is entity.wolf.hurt, and the break itself fires entity
// event 65 with a final crack. Ambient wolf sounds are excluded —
// they fire on a random timer unrelated to damage.
func TestWolfArmorFeedbackParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	wolf := `@e[type=minecraft:wolf,limit=1]`
	// ids the pins cover: crack, armor-damage, wolf-hurt.
	interest := map[int32]bool{1792: true, 1793: true, 1806: true}

	damageLegs := []string{
		"damage " + wolf + " 4 minecraft:mob_attack",  // absorb: NONE->LOW crack
		"damage " + wolf + " 4 minecraft:mob_attack",  // absorb: LOW->LOW no crack
		"damage " + wolf + " 8 minecraft:thorns",      // bypass: wolf.hurt
		"damage " + wolf + " 60 minecraft:mob_attack", // absorb: break
		"damage " + wolf + " 1 minecraft:mob_attack",  // armor gone: wolf.hurt
	}

	run := func(addr string) [][]string {
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSink(pr, pw)
		sink.cap = 1 << 20
		time.Sleep(400 * time.Millisecond)
		for _, cmd := range []string{
			// Sounds only ship within 16*vol of the position — pin the
			// listener next to the wolf so a far spawn can't flake it.
			"tp P 4.5 -60 4.5",
			"summon minecraft:wolf 4.5 -60 4.5 {NoAI:1b}",
			"item replace entity " + wolf + " armor.body with minecraft:wolf_armor",
		} {
			func() { defer func() { recover() }(); sendCommand(t, pw, cmd) }()
			time.Sleep(400 * time.Millisecond)
		}
		var legs [][]string
		for _, cmd := range damageLegs {
			sink.take()
			func() { defer func() { recover() }(); sendCommand(t, pw, cmd) }()
			time.Sleep(1200 * time.Millisecond)
			var evs []string
			for _, line := range sink.take() {
				var idHex, payload string
				if i := strings.IndexByte(line, ':'); i >= 0 {
					idHex, payload = line[:i], line[i+1:]
				} else {
					continue
				}
				switch idHex {
				case "0x74", "0x75": // sound_entity, sound
					r := protocol.NewPacketReader([]byte(payload))
					if id, err := r.VarInt(); err == nil && interest[id-1] {
						evs = append(evs, "snd "+strconv.Itoa(int(id-1)))
					}
				case "0x2f": // level_particles
					r := protocol.NewPacketReader([]byte(payload))
					if _, err := r.Byte(); err != nil {
						continue
					}
					if _, err := r.Byte(); err != nil {
						continue
					}
					ok := true
					for i := 0; i < 3 && ok; i++ {
						_, err := r.Double()
						ok = err == nil
					}
					for i := 0; i < 3 && ok; i++ {
						_, err := r.Float()
						ok = err == nil
					}
					if _, err := r.Float(); err != nil {
						ok = false
					}
					var count int64
					if n, err := r.Int(); err == nil {
						count = int64(n)
						_ = count
					} else {
						ok = false
					}
					if pid, err := r.VarInt(); ok && err == nil && pid == 54 {
						// ITEM particle carries an ItemStackTemplate:
						// VarInt item id + VarInt count + patch.
						if iid, err := r.VarInt(); err == nil {
							evs = append(evs, "ptcl item x"+strconv.Itoa(int(count))+" item="+strconv.Itoa(int(iid)))
						}
					}
				case "0x22": // entity_event
					r := protocol.NewPacketReader([]byte(payload))
					if _, err := r.Int(); err != nil {
						continue
					}
					if st, err := r.Byte(); err == nil && st == 65 {
						evs = append(evs, "evt 65")
					}
				case "0x19": // damage_event
					evs = append(evs, "dmg")
				}
			}
			legs = append(legs, evs)
		}
		return legs
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	for i, cmd := range damageLegs {
		if len(want[i]) == 0 {
			t.Fatalf("leg %q: vanilla produced no pinned packets — measurement changed", cmd)
		}
		if len(want[i]) != len(got[i]) {
			t.Errorf("leg %q:\n  vanilla %v\n  mc-go   %v", cmd, want[i], got[i])
			continue
		}
		for j := range want[i] {
			if want[i][j] != got[i][j] {
				t.Errorf("leg %q event %d:\n  vanilla %s\n  mc-go   %s", cmd, j, want[i][j], got[i][j])
			}
		}
	}
}

// TestHelmetWearParity pins the damages_helmet path on the player:
// for falling_anvil/falling_block/falling_stalactite hits with a
// helmet on, vanilla's hurtServer runs hurtHelmet (doHurtEquipment on
// HEAD — max(1, raw/4) wear) and bleeds a flat 25% through BEFORE the
// armor formula — and then hurtArmor still walks all four slots, so
// the helmet wears twice. A non-helmet type wears it once through
// hurtArmor with no 25% absorb.
func TestHelmetWearParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	legs := []string{
		"damage @s 8 minecraft:falling_anvil", // hurtHelmet 2 + hurtArmor 1, x0.75
		"damage @s 4 minecraft:falling_block", // hurtHelmet 1 + hurtArmor 1
		"damage @s 3 minecraft:mob_attack",    // hurtArmor 1, no absorb
		`item modify entity @s armor.head {"function":"minecraft:set_damage","damage":0.98}`,
		"damage @s 8 minecraft:falling_anvil", // break: event 49, then x0.75 at armor 0
	}
	dmgPath := `equipment.head.components."minecraft:damage"`

	run := func(addr string) []string {
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSink(pr, pw)
		sink.cap = 1 << 20
		time.Sleep(400 * time.Millisecond)
		sendCommand(t, pw, "gamemode survival @s")
		sendCommand(t, pw, "item replace entity @s armor.head with minecraft:leather_helmet")
		time.Sleep(400 * time.Millisecond)
		var out []string
		for _, cmd := range legs {
			func() { defer func() { recover() }(); sendCommand(t, pw, cmd) }()
			time.Sleep(1200 * time.Millisecond)
			out = append(out, dataGetLine(t, sink, pw, "@s", dmgPath))
			out = append(out, dataGetLine(t, sink, pw, "@s", "equipment.head"))
			out = append(out, dataGetLine(t, sink, pw, "@s", "Health"))
		}
		return out
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	for i := range want {
		if want[i] == got[i] {
			continue
		}
		wt, okW := componentTree(want[i], true)
		gt, okG := componentTree(got[i], true)
		if !okW || !okG || maskUUID(wt) != maskUUID(gt) {
			t.Errorf("leg %d:\n  vanilla %s\n  mc-go   %s", i, want[i], got[i])
		}
	}
	// Anchors: leather helmet wore exactly 3+2+1 then survived; the
	// break leg's nothing_found must show on both sides.
	if lastNumber(want[0]) != 3 {
		t.Fatalf("first anvil hit did not wear the helmet twice on vanilla: %q", want[0])
	}
}

// TestBreachParity pins CombatRules.getDamageAfterAbsorb's
// armor_effectiveness pass: the damage source's weapon item (the
// DIRECT entity's mainhand) applies its breach level as -0.15/level
// to the armor protection fraction, clamped to [0,1]. The player
// victim path (damageWith) and the mob victim path (Manager.Damage)
// both resolve it through the direct entity — a husk holding a
// breach-4 mace vs a plain mace moves a 5-hit from 1.5 to 4.5 on
// armor 20, and a breach hit on an iron-chest zombie ignores its
// armor entirely.
func TestBreachParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	husk := `@e[type=minecraft:husk,limit=1]`
	creeper := `@e[type=minecraft:creeper,limit=1]`

	// natural_health_regeneration off keeps health reads exact; the
	// mob victim is a creeper because zombies spawn reinforcement
	// allies on hurt and skeletons burn in daylight.
	legs := []string{
		"gamemode survival @s",
		"gamerule natural_health_regeneration false",
		"attribute @s minecraft:armor base set 20",
		"summon minecraft:husk 5.5 -60 5.5 {NoAI:1b,PersistenceRequired:1b}",
		"item replace entity " + husk + " weapon.mainhand with minecraft:mace",
		"damage @s 5 minecraft:mob_attack by " + husk, // plain: 1.5 -> 18.5
		"item replace entity " + husk + ` weapon.mainhand with minecraft:mace[minecraft:enchantments={"minecraft:breach":4}]`,
		"damage @s 5 minecraft:mob_attack by " + husk, // breach 4: 4.5 -> 14
		"summon minecraft:creeper 7.5 -60 7.5 {NoAI:1b,PersistenceRequired:1b}",
		"item replace entity " + creeper + " armor.chest with minecraft:iron_chestplate",
		"damage " + creeper + " 5 minecraft:mob_attack by " + husk, // breach: 5 -> 15
		"item replace entity " + husk + " weapon.mainhand with minecraft:mace",
		"damage " + creeper + " 5 minecraft:mob_attack by " + husk, // plain: 4.3 -> 10.7
	}

	run := func(addr string) []string {
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSink(pr, pw)
		sink.cap = 1 << 20
		time.Sleep(400 * time.Millisecond)
		sendCommand(t, pw, "gamemode creative @s")
		time.Sleep(300 * time.Millisecond)
		var out []string
		for _, cmd := range legs {
			func() { defer func() { recover() }(); sendCommand(t, pw, cmd) }()
			time.Sleep(1200 * time.Millisecond)
			out = append(out, dataGetLine(t, sink, pw, "@s", "Health"))
			out = append(out, dataGetLine(t, sink, pw, creeper, "Health"))
		}
		return out
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	for i := range want {
		if want[i] == got[i] {
			continue
		}
		wt, okW := componentTree(want[i], true)
		gt, okG := componentTree(got[i], true)
		if !okW || !okG || maskUUID(wt) != maskUUID(gt) {
			t.Errorf("leg %d %q:\n  vanilla %s\n  mc-go   %s", i, legs[i/2], want[i], got[i])
		}
	}
	// Anchors: the measured hits must match the vanilla numbers, else
	// the diff above is vacuous — 18.5 plain, 14 breach on the player,
	// 15 breach / 10.7 plain on the chestplated creeper.
	if lastNumber(want[10]) != 18.5 || lastNumber(want[14]) != 14 {
		t.Fatalf("vanilla player-hit numbers changed: %q %q", want[10], want[14])
	}
	if lastNumber(want[21]) != 15 || lastNumber(want[25]) != 10.7 {
		t.Fatalf("vanilla mob-hit numbers changed: %q %q", want[21], want[25])
	}
}
