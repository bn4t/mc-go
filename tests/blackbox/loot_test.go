package blackbox

// TestLootParity pins vanilla 26.2's entity-loot context gating
// (measured from the datapack tables + javap): drops roll whenever
// shouldDropLoot (!baby && mob_drops) — no player needed — while XP
// orbs additionally require the lastHurtByPlayer window live.
// killed_by_player pools (spider_eye, zombie rares) only open inside
// that window; furnace_smelt converts to the cooked variant when the
// victim burned OR the killer's mainhand carries #smelts_loot
// (fire_aspect); enchanted_count_increase adds round(uniform[0,1]·l).
//
// The legs report predicate outcomes, not raw counts — drop rolls are
// per-server RNG so only the invariants can be byte-compared.
import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/world"
)

func TestLootParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("A", "C")}, nil)
	defer targets.cleanup()

	itemType, _ := server.EntityTypeID("item")
	orbType, _ := server.EntityTypeID("experience_orb")

	run := func(addr string) []string {
		ca, pra, pwa := joinPlayBlackbox(t, addr, "A")
		defer ca.Close()
		cc, prc, pwc := joinPlayBlackbox(t, addr, "C")
		defer cc.Close()
		_ = ca.SetDeadline(time.Now().Add(300 * time.Second))
		_ = cc.SetDeadline(time.Now().Add(300 * time.Second))
		_ = newChatSinkTP(prc, pwc)
		var posA [3]float64
		obsA := wearObs{}
		drainWear(t, ca, pra, pwa, &posA, 1200*time.Millisecond, &obsA, nil)
		sendCommand(t, pwc, "tp A 4.5 -60 2.5 -90 0")
		drainWear(t, ca, pra, pwa, &posA, 800*time.Millisecond, &obsA, nil)

		attack := func(eid int32) {
			var atk protocol.Buffer
			atk.VarInt(eid)
			_ = pwa.WritePacket(server.IDPlayAttackSB, atk.Bytes())
		}
		summon := func(mob string, nbt string) int32 {
			sendCommand(t, pwc, fmt.Sprintf("summon minecraft:%s 6.5 -60 4 %s", mob, nbt))
			spm := map[int32]int32{}
			drainWear(t, ca, pra, pwa, &posA, 900*time.Millisecond, &obsA, spm)
			ty, _ := server.EntityTypeID(mob)
			if eid := spm[ty]; eid != 0 {
				return eid
			}
			t.Fatalf("%s: %s never spawned", addr, mob)
			return 0
		}
		// collectDrops reads A's conn for d and folds item-entity
		// spawns into item-name totals plus an orb spawn count.
		collectDrops := func(d time.Duration) (items map[string]int32, orbs int) {
			items = map[string]int32{}
			pending := map[int32]bool{}
			deadline := time.Now().Add(d)
			for time.Now().Before(deadline) {
				_ = ca.SetReadDeadline(time.Now().Add(120 * time.Millisecond))
				p, err := pra.ReadPacket()
				if err != nil {
					continue
				}
				switch p.ID {
				case server.IDPlayKeepAliveCB:
					_ = pwa.WritePacket(server.IDPlayKeepAliveSB, p.Data)
				case server.IDPlaySpawnEntity:
					r := protocol.NewPacketReader(p.Data)
					eid, err := r.VarInt()
					if err != nil {
						continue
					}
					if _, err := r.UUID(); err != nil {
						continue
					}
					ty, _ := r.VarInt()
					if ty == itemType {
						pending[eid] = true
					}
					if ty == orbType {
						orbs++
					}
				case server.IDPlayEntityMetadata:
					r := protocol.NewPacketReader(p.Data)
					eid, err := r.VarInt()
					if err != nil || !pending[eid] {
						continue
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
							// slot value — index 8 is the item stack.
							st, err := readSlotBB(r)
							if err != nil {
								break
							}
							if idx == 8 && st.ID != 0 {
								if nm, ok := world.ItemName(st.ID); ok {
									items[nm] += st.Count
								}
							}
							continue
						}
						if !skipMetaVal(r, sty) {
							break
						}
					}
				}
			}
			return items, orbs
		}
		killWith := func(eid int32, hits int) {
			for i := 0; i < hits; i++ {
				attack(eid)
				time.Sleep(800 * time.Millisecond)
			}
		}
		equipA := func(item string) {
			sendCommand(t, pwc, "item replace entity A weapon.mainhand with "+item)
			drainWear(t, ca, pra, pwa, &posA, 400*time.Millisecond, &obsA, nil)
		}

		var out []string
		// /kill pig: drops roll anyway (no player gate), no XP.
		summon("pig", "{NoAI:1b}")
		sendCommand(t, pwc, "kill @e[type=minecraft:pig,limit=1]")
		di, orbs := collectDrops(1500 * time.Millisecond)
		out = append(out, fmt.Sprintf("kill-pig porkchop=%v,orbs=%v",
			di["porkchop"] >= 1, orbs == 0))
		// Player-kill pig: porkchop 1-3 + XP.
		p2 := summon("pig", "{NoAI:1b}")
		equipA("minecraft:diamond_sword")
		killWith(p2, 2)
		di, orbs = collectDrops(1500 * time.Millisecond)
		out = append(out, fmt.Sprintf("melee-pig porkchop=%v,orbs=%v",
			di["porkchop"] >= 1, orbs >= 1))
		// Baby pig player-kill: nothing at all.
		pb := summon("pig", "{NoAI:1b,Age:-24000}")
		killWith(pb, 2)
		di, orbs = collectDrops(1500 * time.Millisecond)
		out = append(out, fmt.Sprintf("baby-pig items=%v,orbs=%v",
			len(di) == 0, orbs == 0))
		// fire_aspect kill: cooked even though the pig never burned.
		pf := summon("pig", "{NoAI:1b}")
		equipA("minecraft:diamond_sword[enchantments={fire_aspect:2}]")
		killWith(pf, 2)
		di, _ = collectDrops(1800 * time.Millisecond)
		out = append(out, fmt.Sprintf("fire-pig cooked=%v,raw=%v",
			di["cooked_porkchop"] >= 1, di["porkchop"] == 0))
		// /kill zombie x6: flesh may roll, rares and XP never.
		for i := 0; i < 6; i++ {
			summon("zombie", "{NoAI:1b}")
			sendCommand(t, pwc, "kill @e[type=minecraft:zombie,limit=1]")
		}
		di, orbs = collectDrops(1500 * time.Millisecond)
		out = append(out, fmt.Sprintf("kill-zombie rares=%v,orbs=%v",
			di["iron_ingot"] == 0 && di["carrot"] == 0 && di["potato"] == 0,
			orbs == 0))
		// /kill spider x4: spider_eye is killed_by_player-gated.
		for i := 0; i < 4; i++ {
			summon("spider", "{NoAI:1b}")
			sendCommand(t, pwc, "kill @e[type=minecraft:spider,limit=1]")
		}
		di, orbs = collectDrops(1500 * time.Millisecond)
		out = append(out, fmt.Sprintf("kill-spider eyes=%v,orbs=%v",
			di["spider_eye"] == 0, orbs == 0))
		// looting3 on ten pigs: count = [1,3] + 0-or-3, so at least one
		// stack must reach ≥4 (per-kill miss chance 0.5 → ~0.001).
		equipA("minecraft:diamond_sword[enchantments={looting:3}]")
		var maxSeen int32
		for i := 0; i < 10; i++ {
			pl := summon("pig", "{NoAI:1b}")
			killWith(pl, 2)
			di, _ = collectDrops(1300 * time.Millisecond)
			if di["porkchop"] > maxSeen {
				maxSeen = di["porkchop"]
			}
		}
		out = append(out, fmt.Sprintf("looting3 max=%v", maxSeen >= 4))
		// mob_drops off: nothing drops at all.
		sendCommand(t, pwc, "gamerule minecraft:mob_drops false")
		summon("pig", "{NoAI:1b}")
		sendCommand(t, pwc, "kill @e[type=minecraft:pig,limit=1]")
		di, orbs = collectDrops(1500 * time.Millisecond)
		out = append(out, fmt.Sprintf("no-mob-drops items=%v,orbs=%v",
			len(di) == 0, orbs == 0))
		sendCommand(t, pwc, "gamerule minecraft:mob_drops true")
		return out
	}

	vanilla := run(targets.vanilla)
	mcgo := run(targets.mcgo)
	for i := range vanilla {
		name := strings.Fields(vanilla[i])[0]
		t.Logf("%-14s vanilla=%s mcgo=%s", name, vanilla[i], mcgo[i])
		if vanilla[i] != mcgo[i] {
			t.Errorf("leg %d (%s): vanilla %s, mc-go %s", i, name, vanilla[i], mcgo[i])
		}
	}
}

// skipMetaVal advances past one entity-data value for the serializer
// ids that can precede an item stack entry (0 byte, 1 VarInt, 3 float,
// 4 string, 8 bool); anything else stops the scan — unknown serializer
// on an item entity is a parse risk, not a silent miscount.
func skipMetaVal(r *protocol.PacketReader, typ byte) bool {
	var err error
	switch typ {
	case 0, 8: // byte, bool
		_, err = r.Byte()
	case 1, 2, 10, 11, 12, 14, 15, 16, 17, 19, 20, 21: // varint-ish
		_, err = r.VarInt()
	case 3: // float
		_, err = r.Read(4)
	case 4: // string
		var n int32
		n, err = r.VarInt()
		if err == nil && n >= 0 && n < 1<<20 {
			_, err = r.Read(int(n))
		}
	default:
		return false
	}
	return err == nil
}
