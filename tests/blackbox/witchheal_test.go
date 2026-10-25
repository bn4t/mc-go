package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/data"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// Witch's NearestHealableRaiderTargetGoal + performRangedAttack's
// raider branch: an in-raid witch coin-flips a heal scan each tick
// (200-tick cooldown from goal start; player targeting barred while
// it runs), locks the nearest non-witch Raider inside follow_range 16,
// and throws healing when it's at ≤4 health, regeneration otherwise —
// setTarget(null) after the throw. Observed through the thrown
// splash_potion's potion_contents on the wire (potion registry id)
// plus the raider's Health/active_effects the splash lands.

// healMetaPotion scans a set_entity_data payload for the index-8 slot
// and returns the potion registry id ("h24" healing, "h31"
// regeneration). Entries of unknown type end the parse — the thrown
// splash only syncs its item slot.
func healMetaPotion(data []byte) string {
	r := protocol.NewPacketReader(data)
	if _, err := r.VarInt(); err != nil {
		return ""
	}
	for {
		idx, err := r.Byte()
		if err != nil || idx == 0xFF {
			return ""
		}
		typ, err := r.VarInt()
		if err != nil {
			return ""
		}
		switch typ {
		case 7: // item slot
			it, _, _, _, err := readSlotBBComps(r)
			if err != nil {
				return ""
			}
			if it.Potion != "" {
				return it.Potion
			}
		case 0, 8:
			if _, err := r.Byte(); err != nil {
				return ""
			}
		case 1, 2, 12, 14, 19, 20, 21, 22, 23, 24, 26, 27, 28:
			if _, err := r.VarInt(); err != nil {
				return ""
			}
		case 3:
			if _, err := r.Read(4); err != nil {
				return ""
			}
		case 9, 29:
			if _, err := r.Read(12); err != nil {
				return ""
			}
		case 30:
			if _, err := r.Read(16); err != nil {
				return ""
			}
		case 4:
			if n, err := r.VarInt(); err == nil {
				if _, err := r.Read(int(n)); err != nil {
					return ""
				}
			} else {
				return ""
			}
		case 10:
			if _, err := r.Read(8); err != nil {
				return ""
			}
		case 11:
			if f, err := r.Byte(); err != nil {
				return ""
			} else if f != 0 {
				if _, err := r.Read(8); err != nil {
					return ""
				}
			}
		case 13:
			if f, err := r.Byte(); err != nil {
				return ""
			} else if f != 0 {
				if _, err := r.Read(16); err != nil {
					return ""
				}
			}
		default:
			return ""
		}
	}
}

func TestWitchHealParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	splashID, _ := server.EntityTypeID("splash_potion")
	healing := fmt.Sprintf("h%d", data.PotionIDs["healing"])
	regen := fmt.Sprintf("h%d", data.PotionIDs["regeneration"])

	run := func(addr string) []string {
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(240 * time.Second))
		// Vanilla treats a missing client-information packet as a
		// player that never finished joining — send it like the raid
		// test does.
		sendClientInformation(t, pw)

		splashEids := map[int32]bool{}
		var potions []string
		var chats []string
		raidUp := false
		omenSeen := false
		fold := func(p protocol.Packet) {
			switch p.ID {
			case server.IDPlayKeepAliveCB:
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
			case server.IDPlaySyncPos:
				rd := protocol.NewPacketReader(p.Data)
				if id, err := rd.VarInt(); err == nil {
					var b protocol.Buffer
					b.VarInt(id)
					_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
					// Echo the position back like a real client —
					// without it vanilla never moves the entity and
					// the village chunk is never watched.
					if x, err := rd.Double(); err == nil {
						var mb protocol.Buffer
						mb.Double(x)
						if y, err := rd.Double(); err == nil {
							mb.Double(y)
						}
						if z, err := rd.Double(); err == nil {
							mb.Double(z)
						}
						mb.Byte(0)
						_ = pw.WritePacket(server.IDPlayMovePosSB, mb.Bytes())
					}
				}
			case server.IDPlayChunkBatchDone:
				// Vanilla counts a chunk watched only after the
				// client acks its batch — without this the villager
				// chunk never entity-ticks and no claim lands.
				_ = ackChunkBatch(pw)
			case 0x09: // boss_event add — raid bar names event.minecraft.raid
				if strings.Contains(string(p.Data), "event.minecraft.raid") {
					raidUp = true
				}

			case server.IDPlaySpawnEntity:
				rd := protocol.NewPacketReader(p.Data)
				id, _ := rd.VarInt()
				rd.UUID()
				ty, _ := rd.VarInt()
				if ty == splashID {
					splashEids[id] = true
				}
			case server.IDPlayEntityMetadata:
				rd := protocol.NewPacketReader(p.Data)
				eid, err := rd.VarInt()
				if err == nil && splashEids[eid] {
					if pot := healMetaPotion(p.Data); pot != "" {
						potions = append(potions, pot)
					}
				}
			case server.IDPlayUpdateMobEffect:
				rd := protocol.NewPacketReader(p.Data)
				rd.VarInt()                           // entity id
				if eff, _ := rd.VarInt(); eff == 34 { // raid_omen
					omenSeen = true
				}
			case server.IDPlaySystemChatCB:
				chats = append(chats, chatTextBlackbox(t, p.Data))
			}
		}
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
		// Village: bed + villager claim, then Bad Omen → Raid Omen →
		// the raid bar add (same orchestration as TestRaidParity).
		pump(600 * time.Millisecond)
		sendCommand(t, pw, "tp P 2000.5 -60 2000.5 0 0")
		// setblock fails with argument.pos.unloaded until the chunk
		// stream catches up with the teleport — retry until both
		// bed halves report success.
		for i := 0; i < 40; i++ {
			ok := 0
			for _, l := range chats {
				if strings.Contains(l, "setblock.success") {
					ok++
				}
			}
			if ok >= 2 {
				break
			}
			sendCommand(t, pw, "setblock 2002 -60 2000 white_bed[part=foot]")
			sendCommand(t, pw, "setblock 2003 -60 2000 white_bed[part=head]")
			pump(1200 * time.Millisecond)
		}
		sendCommand(t, pw, "time set 6100")
		// Three villagers, not one: wave pillagers hunt them and each
		// death brings the raid closer to defeat — redundancy keeps
		// the raid alive across both heal-throw windows.
		sendCommand(t, pw, "summon minecraft:villager 2001.5 -60 2000.5 {VillagerData:{type:plains,profession:none,level:1}}")
		sendCommand(t, pw, "summon minecraft:villager 2000.5 -60 2001.5 {VillagerData:{type:plains,profession:none,level:1}}")
		sendCommand(t, pw, "summon minecraft:villager 2001.5 -60 2002.5 {VillagerData:{type:plains,profession:none,level:1}}")
		sendCommand(t, pw, "difficulty normal")
		pump(2000 * time.Millisecond)
		sendCommand(t, pw, "effect give P minecraft:bad_omen 120 0")
		pump(1500 * time.Millisecond)

		// Wait for Bad Omen → Raid Omen (the eff=34 update on self is
		// the witness, same as TestRaidParity); the villager's POI
		// claim can lag tens of seconds on a cold chunk.
		deadline := time.Now().Add(75 * time.Second)
		for time.Now().Before(deadline) && !omenSeen {
			sendCommand(t, pw, "data get entity @s active_effects")
			pump(1500 * time.Millisecond)
		}
		deadline = time.Now().Add(75 * time.Second)
		for time.Now().Before(deadline) && !raidUp {
			pump(800 * time.Millisecond)
		}
		if !raidUp {
			t.Logf("%s: raid never started (omen=%v); chats: %q",
				addr, omenSeen, chats)
			return potions
		}

		// Bare-summon the witch so finalizeSpawn flags CanJoinRaid —
		// a tagged summon keeps the field false and a raid-never-
		// joining witch never runs its heal goal at all. The pillager
		// carries NBT (skips finalizeSpawn, CanJoinRaid stays false)
		// and the heal selector doesn't need its target in the raid.
		sendCommand(t, pw, "summon minecraft:witch 2004.5 -60 2000.5")
		sendCommand(t, pw, "summon minecraft:pillager 2007.5 -60 2000.5 {NoAI:1b,NoGravity:1b}")
		deadline = time.Now().Add(70 * time.Second)
		sawRegen := false
		for time.Now().Before(deadline) && !sawRegen {
			pump(800 * time.Millisecond)
			for _, pot := range potions {
				if pot == regen {
					sawRegen = true
				}
			}
		}

		// Below five hearts the pick flips to instant-health splashes.
		sendCommand(t, pw, "damage @e[type=minecraft:pillager,limit=1] 21 minecraft:generic")
		pump(300 * time.Millisecond)
		deadline = time.Now().Add(70 * time.Second)
		sawHeal := false
		for time.Now().Before(deadline) && !sawHeal {
			pump(800 * time.Millisecond)
			for _, pot := range potions {
				if pot == healing {
					sawHeal = true
				}
			}
		}
		return potions
	}

	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	t.Logf("van potions %v", van)
	t.Logf("mc  potions %v", mc)
	want := []string{
		fmt.Sprintf("h%d", data.PotionIDs["regeneration"]),
		fmt.Sprintf("h%d", data.PotionIDs["healing"]),
	}
	for name, pots := range map[string][]string{"vanilla": van, "mc-go": mc} {
		for _, w := range want {
			seen := false
			for _, pot := range pots {
				if pot == w {
					seen = true
				}
			}
			if !seen {
				t.Errorf("%s: no splash_potion with potion_contents %s", name, w)
			}
		}
	}
}
