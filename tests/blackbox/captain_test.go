package blackbox

// TestCaptainParity exercises the raid-captain slice end to end on both
// servers. A captain is summoned deterministically — the summon NBT
// carries the ominous banner patch in equipment.head plus the 2.0 head
// drop chance (Raid.setLeader's equip on vanilla, PatrollingMonster's
// banner for patrol leaders): Raider.isCaptain reads the head match,
// so the pillager is a captain without relying on the 6% finalizeSpawn
// roll.
//
// Killing it must drop the banner (equipment dropChance 2.0 — outside
// the mob_drops gate) plus one ominous_bottle whose
// ominous_bottle_amplifier is uniform 0-4, and grant voluntary_exile —
// the player_killed_entity criterion whose entity predicate is
// equipment.head matching that same patch. A non-captain pillager leg
// (plain summon; retry if the 6% roll lands a leader) drops neither and
// grants nothing extra.
//
// Compared per leg: the captain head's set_equipment component list,
// the dropped stacks' component lists and the amplifier range, the
// union of advancement progress, and the system_chat translate keys.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/world"
)

// captainNBT is the vanilla captain shape in summon SNBT: the ominous
// banner in equipment.head (the getBannerComponentPatch contents), the
// 2.0 drop chance that makes the banner's drop unconditional, the
// PatrolLeader flag, and the other slots at 0 so the auto-equip
// crossbow's 0.085 drop roll can't flake the differential. equipment
// must come FIRST: with drop_chances leading, the packrat SNBT command
// arg fails to parse the full component map (measured on 26.2).
const captainNBT = `{equipment:{head:{id:"minecraft:white_banner",count:1,components:{` +
	`"minecraft:banner_patterns":[` +
	`{pattern:"minecraft:rhombus",color:"cyan"},` +
	`{pattern:"minecraft:stripe_bottom",color:"light_gray"},` +
	`{pattern:"minecraft:stripe_center",color:"gray"},` +
	`{pattern:"minecraft:border",color:"light_gray"},` +
	`{pattern:"minecraft:stripe_middle",color:"black"},` +
	`{pattern:"minecraft:half_horizontal",color:"light_gray"},` +
	`{pattern:"minecraft:circle",color:"light_gray"},` +
	`{pattern:"minecraft:border",color:"black"}],` +
	`"minecraft:tooltip_display":{hidden_components:["minecraft:banner_patterns"]},` +
	`"minecraft:item_name":{translate:"block.minecraft.ominous_banner"},` +
	`"minecraft:rarity":"uncommon"}}},` +
	`NoAI:1b,PatrolLeader:1b,` +
	`drop_chances:{head:2.0f,chest:0.0f,legs:0.0f,feet:0.0f,mainhand:0.0f,offhand:0.0f,body:0.0f,saddle:0.0f}}`

// capObs folds one leg's traffic: per-entity head-slot component lists
// (set_equipment), dropped item stacks keyed by name, and the
// advancement/chat flush.
type capObs struct {
	headComps  map[int32][]int32
	capHead    []int32   // headComps for the captain leg's eid
	drops      []capDrop // every item-entity stack seen, in spawn order
	capDrops   []capDrop // drops in the captain-kill window
	plainDrops []capDrop
	adv        *advFlush
}

type capDrop struct {
	name  string
	count int32
	comps []int32
	amp   int32 // -1 when no ominous_bottle_amplifier
}

func TestCaptainParity(t *testing.T) {
	// The captain summon is ~800 chars — over the 256-byte command
	// packet cap — so both legs run it as the same datapack function:
	// vanilla gets it through the harness's extra-files map, mc-go's
	// WorldDir carries the same pack.
	capFn := "summon minecraft:pillager 6.5 -60 2.5 " + captainNBT + "\n"
	packMeta := `{"pack":{"description":"zz","min_format":107,"max_format":107}}`
	mcDir := t.TempDir()
	writePack := func(root string) {
		for name, body := range map[string]string{
			"datapacks/zz/pack.mcmeta":                     packMeta,
			"datapacks/zz/data/zz/function/cap.mcfunction": capFn,
		} {
			p := filepath.Join(root, name)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	writePack(mcDir)
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json":                       opsJSONFor("Capt"),
		"world/datapacks/zz/pack.mcmeta": packMeta,
		"world/datapacks/zz/data/zz/function/cap.mcfunction": capFn,
	}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
		s.WorldDir = mcDir
	})
	defer targets.cleanup()

	run := func(addr string) *capObs {
		c, pr, pw := joinPlayBlackbox(t, addr, "Capt")
		defer c.Close()
		obs := &capObs{
			headComps: map[int32][]int32{},
			adv:       &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}},
		}
		pendingItem := map[int32]bool{} // item-entity eids awaiting metadata
		seenItem := map[int32]bool{}    // item eids already recorded (metadata re-broadcasts)
		died := map[int32]bool{}        // eids whose entity-event 3 arrived
		type spawnRec struct {
			eid, ty int32
			x       int
		}
		var spawns []spawnRec
		var sysTexts []string // diagnostics for summon failures
		movePos := func() {
			var b protocol.Buffer
			b.Double(4)
			b.Double(-60)
			b.Double(2)
			b.Byte(0x01)
			_ = pw.WritePacket(0x1E, b.Bytes())
		}

		// fold merges one packet into obs. Every wait loop runs it, so a
		// set_equipment or drop landing in another step's window is never
		// lost.
		fold := func(p protocol.Packet) {
			switch p.ID {
			case server.IDPlayKeepAliveCB:
				var kb protocol.Buffer
				kb.Raw(p.Data)
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, kb.Bytes())
			case server.IDPlaySyncPos:
				r := protocol.NewPacketReader(p.Data)
				if id, err := r.VarInt(); err == nil {
					var b protocol.Buffer
					b.VarInt(id)
					_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
				}
			case server.IDPlayUpdateAdvance:
				decodeUpdateAdv(t, p.Data, obs.adv)
			case server.IDPlaySystemChatCB:
				obs.adv.chats = append(obs.adv.chats, chatKey(p.Data))
				sysTexts = append(sysTexts, chatTextBlackbox(t, p.Data))
			case server.IDPlaySpawnEntity:
				r := protocol.NewPacketReader(p.Data)
				eid, _ := r.VarInt()
				r.UUID()
				ty, _ := r.VarInt()
				ex, _ := r.Double()
				if ty == 71 { // item entity
					pendingItem[eid] = true
				}
				spawns = append(spawns, spawnRec{eid, ty, int(ex)})
			case server.IDPlayEntityMetadata:
				r := protocol.NewPacketReader(p.Data)
				eid, err := r.VarInt()
				if err != nil || !pendingItem[eid] {
					return
				}
				for {
					idx, err := r.Byte()
					if err != nil || idx == 0xFF {
						break
					}
					sty, err := r.Byte()
					if err != nil {
						break
					}
					if sty == 7 {
						st, _, _, comps, err := readSlotBBComps(r)
						if err != nil {
							break
						}
						if idx == 8 && st.ID != 0 && !seenItem[eid] {
							seenItem[eid] = true
							nm, _ := world.ItemName(st.ID)
							amp := int32(-1)
							if st.BottleAmpSet {
								amp = st.BottleAmp
							}
							obs.drops = append(obs.drops, capDrop{nm, st.Count, comps, amp})
						}
						continue
					}
					if !skipMetaVal(r, sty) {
						break
					}
				}
			case server.IDPlaySetEquipment:
				r := protocol.NewPacketReader(p.Data)
				ee, _ := r.VarInt()
				for {
					slot, err := r.Byte()
					if err != nil {
						break
					}
					last := slot&0x80 == 0
					slot &= 0x7F
					st, _, _, comps, err := readSlotBBComps(r)
					if err != nil {
						break
					}
					if slot == 5 && !st.Empty() { // EquipmentSlot.HEAD wire id
						obs.headComps[ee] = comps
					}
					if last {
						break
					}
				}
			case server.IDPlayEntityEvent:
				r := protocol.NewPacketReader(p.Data)
				if e2, _ := r.Int(); int32(e2) > 0 {
					if st, _ := r.Byte(); st == 3 {
						died[int32(e2)] = true
					}
				}
			}
		}

		// pump folds packets for d, keeping the connection alive.
		pump := func(d time.Duration) {
			end := time.Now().Add(d)
			for time.Now().Before(end) {
				_ = c.SetReadDeadline(time.Now().Add(time.Until(end)))
				p, err := pr.ReadPacket()
				if err != nil {
					return
				}
				fold(p)
			}
		}

		// summon sends cmd verbatim and returns the spawned pillager's id.
		summon := func(cmd string) int32 {
			before := len(spawns)
			sendCommand(t, pw, cmd)
			pump(1200 * time.Millisecond)
			for _, s := range spawns[before:] {
				if s.ty != 71 && s.x == 6 {
					return s.eid
				}
			}
			t.Fatalf("pillager never spawned (replies %v)", sysTexts)
			return 0
		}

		// kill swings a sword at eid until entity-event 3.
		kill := func(eid int32) {
			for swing := 0; swing < 12; swing++ {
				movePos()
				var ab protocol.Buffer
				ab.VarInt(eid)
				_ = pw.WritePacket(0x01, ab.Bytes())
				pump(700 * time.Millisecond)
				if died[eid] {
					return
				}
			}
			t.Fatalf("entity %d never died", eid)
		}

		pump(1200 * time.Millisecond)
		sendCommand(t, pw, "gamemode survival @s")
		sendCommand(t, pw, "tp @s 4 -60 2 -90 0")
		sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:stone_sword")
		pump(900 * time.Millisecond)

		// Captain leg.
		capEID := summon("function zz:cap")
		obs.capHead = obs.headComps[capEID]
		movePos()
		capMark := len(obs.drops)
		kill(capEID)
		pump(800 * time.Millisecond)
		obs.capDrops = obs.drops[capMark:]

		// Clear the ground: a later summoned pillager picks up a dropped
		// ominous banner into its head slot and becomes a captain
		// (Raider.pickUpItem / the banner-pickup goal).
		sendCommand(t, pw, "kill @e[type=item]")
		pump(400 * time.Millisecond)

		// Non-captain leg: plain summons until one shows neither an
		// equipped head nor any kill drops — the 6% finalizeSpawn roll
		// can still make one a captain, and the set_equipment for its
		// banner can land after the spawn window, so drops are the
		// authoritative non-captain check.
		plainEID := int32(-1)
		for i := 0; i < 20; i++ {
			// A captain rolled inside this loop drops its banner here —
			// clear it before the next summon or the next pillager
			// picks it up and becomes a captain too.
			sendCommand(t, pw, "kill @e[type=item]")
			pump(300 * time.Millisecond)
			e := summon(`summon minecraft:pillager 6.5 -60 2.5 {NoAI:1b,drop_chances:{head:0.0f,chest:0.0f,legs:0.0f,feet:0.0f,mainhand:0.0f,offhand:0.0f,body:0.0f,saddle:0.0f}}`)
			mark := len(obs.drops)
			kill(e)
			pump(400 * time.Millisecond)
			if len(obs.headComps[e]) == 0 && len(obs.drops) == mark {
				plainEID = e
				obs.plainDrops = obs.drops[mark:]
				break
			}
		}
		if plainEID < 0 {
			t.Fatalf("20 summons all rolled captains")
		}
		pump(400 * time.Millisecond)
		return obs
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)

	// The captain head equip writes exactly the four patch components.
	sorted := func(v []int32) []int32 {
		c := append([]int32{}, v...)
		sort.Slice(c, func(i, j int) bool { return c[i] < c[j] })
		return c
	}
	for tag, hc := range map[string][]int32{"vanilla": want.capHead, "mcgo": got.capHead} {
		if s := sorted(hc); fmt.Sprint(s) != "[9 12 18 72]" {
			t.Fatalf("%s captain head components %v, want the ominous patch set", tag, hc)
		}
	}

	// Drop sets: the captain drops one banner carrying the patch and one
	// bottle carrying the amplifier, nothing else — amplifier uniform
	// 0-4, range-checked not diffed. The plain kill drops nothing. (A
	// retry summon that rolled a captain drops its own gear inside the
	// retry loop's window — outside both marks.)
	for tag, o := range map[string]*capObs{"vanilla": want, "mcgo": got} {
		var banners, bottles, stray []capDrop
		for _, d := range o.capDrops {
			switch d.name {
			case "white_banner":
				banners = append(banners, d)
			case "ominous_bottle":
				bottles = append(bottles, d)
			default:
				stray = append(stray, d)
			}
		}
		stray = append(stray, o.plainDrops...)
		if len(banners) != 1 || banners[0].count != 1 ||
			fmt.Sprint(sorted(banners[0].comps)) != "[9 12 18 72]" {
			t.Fatalf("%s banner drops %+v, want one ominous banner", tag, banners)
		}
		if len(bottles) != 1 || bottles[0].count != 1 ||
			fmt.Sprint(sorted(bottles[0].comps)) != "[63]" ||
			bottles[0].amp < 0 || bottles[0].amp > 4 {
			t.Fatalf("%s bottle drops %+v, want one amplified bottle", tag, bottles)
		}
		if len(stray) != 0 {
			t.Fatalf("%s unexpected drops %+v", tag, stray)
		}
	}

	// Advancement traffic: voluntary_exile granted on both sides.
	if diffs := diffAdvFlush(want.adv, got.adv); len(diffs) > 0 {
		t.Fatalf("advancement parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
	if !want.adv.prog["minecraft:adventure/voluntary_exile"]["voluntary_exile"] {
		t.Fatalf("vanilla never granted voluntary_exile — measurement changed")
	}
}
