package blackbox

// TestThornsUnbreakingParity pins vanilla 26.2's post_attack
// (thorns) and item_damage (unbreaking) enchantment passes, measured
// from the datapack entries:
//   - thorns: per equipped piece, chance 0.15*level; a proc deals the
//     attacker uniform[1,5) thorns damage (source = victim) and wears
//     the piece a flat 2 durability.
//   - unbreaking: every durability point is independently removed
//     with probability l/(l+1) on tools, 2l/(5l+5) on armor.
//
// Proc rolls are per-server RNG, so legs report predicate outcomes.
// Durability is read off the wire — the inventory resync packets —
// not /data.
import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestThornsUnbreakingParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("A", "C")}, nil)
	defer targets.cleanup()

	run := func(addr string) []string {
		ca, pra, pwa := joinPlayBlackbox(t, addr, "A")
		defer ca.Close()
		cc, prc, pwc := joinPlayBlackbox(t, addr, "C")
		defer cc.Close()
		_ = ca.SetDeadline(time.Now().Add(400 * time.Second))
		_ = cc.SetDeadline(time.Now().Add(400 * time.Second))
		sink := newChatSinkTP(prc, pwc)
		var posA [3]float64
		obsA := wearObs{}
		drainWear(t, ca, pra, pwa, &posA, 1200*time.Millisecond, &obsA, nil)
		sendCommand(t, pwc, "gamemode survival A") // creative takes no damage
		sendCommand(t, pwc, "tp A 4.5 -60 2.5 -90 0")
		sendCommand(t, pwc, "gamerule minecraft:natural_health_regeneration false")
		sendCommand(t, pwc, "time set night") // zombies must not burn
		drainWear(t, ca, pra, pwa, &posA, 800*time.Millisecond, &obsA, nil)

		// state folds everything pump sees: A's last health, damage on
		// the watched inventory slots (window 0), spawn eids.
		st := &probeState{slotDmg: map[int16]int32{}, spawn: map[int32]int32{}}
		pump := func(d time.Duration) {
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
				case server.IDPlaySyncPos:
					r := protocol.NewPacketReader(p.Data)
					if id, err := r.VarInt(); err == nil {
						var b protocol.Buffer
						b.VarInt(id)
						_ = pwa.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
					}
				case server.IDPlaySetHealth:
					r := protocol.NewPacketReader(p.Data)
					if f, err := r.Float(); err == nil {
						st.health = f
					}
				case server.IDPlaySpawnEntity:
					r := protocol.NewPacketReader(p.Data)
					eid, err := r.VarInt()
					if err != nil {
						continue
					}
					if _, err := r.UUID(); err != nil {
						continue
					}
					if ty, err := r.VarInt(); err == nil {
						st.spawn[ty] = eid
					}
				case server.IDPlayContainerSlot:
					r := protocol.NewPacketReader(p.Data)
					wid, e1 := r.VarInt()
					_, e2 := r.VarInt()
					slot, e3 := r.Short()
					it, dmg, has, e4 := readSlotBBFull(r)
					if e1 != nil || e2 != nil || e3 != nil || e4 != nil || wid != 0 {
						continue
					}
					if it.Empty() || !has {
						delete(st.slotDmg, slot)
					} else {
						st.slotDmg[slot] = dmg
					}
				case server.IDPlayContainerContent:
					r := protocol.NewPacketReader(p.Data)
					wid, e1 := r.VarInt()
					_, e2 := r.VarInt()
					n, e3 := r.VarInt()
					if e1 != nil || e2 != nil || e3 != nil || wid != 0 || n <= 0 || n > 100 {
						continue
					}
					for i := int32(0); i < n; i++ {
						it, dmg, has, err := readSlotBBFull(r)
						if err != nil {
							break
						}
						if it.Empty() || !has {
							delete(st.slotDmg, int16(i))
						} else {
							st.slotDmg[int16(i)] = dmg
						}
					}
				}
			}
		}
		summon := func(mob, nbt string) int32 {
			sendCommand(t, pwc, fmt.Sprintf("summon minecraft:%s 6.5 -60 4 %s", mob, nbt))
			before := st.spawn
			st.spawn = map[int32]int32{}
			pump(900 * time.Millisecond)
			ty, _ := server.EntityTypeID(mob)
			for k, v := range before {
				if _, ok := st.spawn[k]; !ok {
					st.spawn[k] = v
				}
			}
			if eid := st.spawn[ty]; eid != 0 {
				return eid
			}
			t.Fatalf("%s: %s never spawned", addr, mob)
			return 0
		}
		attack := func(eid int32) {
			var atk protocol.Buffer
			atk.VarInt(eid)
			_ = pwa.WritePacket(server.IDPlayAttackSB, atk.Bytes())
		}
		equipA := func(item string) {
			sendCommand(t, pwc, "item replace entity A weapon.mainhand with "+item)
			pump(400 * time.Millisecond)
		}
		// nbtFloat reads a numeric NBT path via /data get; -1 = absent,
		// -2 = no reply.
		nbtFloat := func(sel, path string) float64 {
			sendCommand(t, pwc, fmt.Sprintf("data get entity %s %s", sel, path))
			sendCommand(t, pwc, "say ZEND")
			deadline := time.Now().Add(8 * time.Second)
			for time.Now().Before(deadline) {
				got, endSeen := "", false
				for _, line := range sink.take() {
					if i := strings.Index(line, "data.entity.query"); i >= 0 {
						got = line[:i]
					}
					if strings.Contains(line, "ZEND") {
						endSeen = true
					}
				}
				if endSeen {
					if got == "" {
						return -1
					}
					return lastNumber(got)
				}
				time.Sleep(40 * time.Millisecond)
			}
			return -2
		}

		var out []string
		// Absorption keeps A alive through every leg; a thorns proc
		// drains the pool even though Health stays 20.
		sendCommand(t, pwc, "effect give A minecraft:absorption 600 250 true")

		// --- thorns on a mob victim: A punches a zombie whose lone
		// chestplate is thorns10 — chance 0.15*level >= 1 procs on all
		// 8 hits, so A's absorb drops and the piece wears 8*2 = 16.
		z := summon("zombie", fmt.Sprintf(`{NoAI:1b,equipment:{chest:{id:"minecraft:diamond_chestplate",count:1,%s}}}`,
			`components:{"minecraft:enchantments":{"minecraft:thorns":10}}`))
		for i := 0; i < 8; i++ {
			attack(z)
			pump(700 * time.Millisecond)
		}
		ab := nbtFloat("A", "AbsorptionAmount")
		zd := nbtFloat("@e[type=minecraft:zombie,limit=1]", `equipment.chest.components."minecraft:damage"`)
		out = append(out, fmt.Sprintf("thorns-mob proc=%v(abs=%v,zd=%v)",
			ab >= 0 && ab < 1004 && zd == 16, ab, zd))
		sendCommand(t, pwc, "kill @e[type=minecraft:zombie]")

		// --- thorns on a player victim: A wears thorns3 x4, a live
		// zombie melees; each swing eats ~91% proc odds, so the zombie
		// is under 20 or dead inside a few hits.
		for _, slot := range []string{"head", "chest", "legs", "feet"} {
			sendCommand(t, pwc, fmt.Sprintf("item replace entity A armor.%s with minecraft:diamond_%s[enchantments={thorns:10}]",
				slot, map[string]string{"head": "helmet", "chest": "chestplate", "legs": "leggings", "feet": "boots"}[slot]))
		}
		preAbs := nbtFloat("A", "AbsorptionAmount")
		sendCommand(t, pwc, "summon minecraft:zombie 5.5 -60 3 {}")
		pump(12 * time.Second) // zombie walks up and swings
		zh := nbtFloat("@e[type=minecraft:zombie,limit=1]", "Health")
		postAbs := nbtFloat("A", "AbsorptionAmount")
		// A's chestplate wears 1 per zombie hit (player doHurtEquip-
		// ment: max(1, raw/4) on ~3 dmg) plus a flat 2 per proc — ≥3
		// means at least one proc landed alongside a hit.
		dc := st.slotDmg[6]
		out = append(out, fmt.Sprintf("thorns-player ok=%v(zh=%v,abs=%v,dc=%v)",
			zh < 20 && postAbs < preAbs && dc >= 3, zh, postAbs, dc))
		sendCommand(t, pwc, "kill @e[type=minecraft:zombie]")

		// --- unbreaking on a weapon: 30 landed hits wear a plain
		// sword exactly 30; unbreaking3 keeps binomial(30,0.25) ≤ 15.
		// Each pig dies to two diamond-sword hits, one live pig at a
		// time at jittered spots — no entity cramming.
		for _, item := range []string{
			"minecraft:diamond_sword[enchantments={unbreaking:3}]",
			"minecraft:diamond_sword",
		} {
			equipA(item)
			for i := 0; i < 15; i++ {
				// The pigs pile on A's tile and shove it; re-pin A
				// before each spawn so reach never drifts out.
				sendCommand(t, pwc, "tp A 4.5 -60 2.5 -90 0")
				sendCommand(t, pwc, fmt.Sprintf("summon minecraft:pig %v -60 %v {NoAI:1b}",
					5.2+float64(i%3)*0.4, 2.4+float64(i/3)*0.3))
				before := st.spawn
				st.spawn = map[int32]int32{}
				pump(700 * time.Millisecond)
				ty, _ := server.EntityTypeID("pig")
				for k, v := range before {
					if _, ok := st.spawn[k]; !ok {
						st.spawn[k] = v
					}
				}
				p := st.spawn[ty]
				attack(p)
				pump(650 * time.Millisecond)
				attack(p)
				pump(650 * time.Millisecond)
			}
			tag, hi := "plain", int32(30)
			if strings.Contains(item, "unbreaking") {
				tag, hi = "ub3", int32(15)
			}
			d := st.slotDmg[36]
			out = append(out, fmt.Sprintf("weapon-%s ok=%v(dmg=%d)",
				tag, d > 0 && d <= hi, d))
		}

		// --- unbreaking on armor: 20x /damage 40 wears a plain piece
		// 10/hit=200; unbreaking3 keeps binomial(200,0.7) ≤ 160.
		sendCommand(t, pwc, "item replace entity A armor.chest with minecraft:diamond_chestplate[enchantments={unbreaking:3}]")
		sendCommand(t, pwc, "item replace entity A armor.legs with minecraft:diamond_leggings")
		pump(600 * time.Millisecond)
		for i := 0; i < 20; i++ {
			sendCommand(t, pwc, "damage A 40 minecraft:mob_attack")
			pump(600 * time.Millisecond)
		}
		adc := st.slotDmg[6] // chest
		dl := st.slotDmg[7]  // legs
		out = append(out, fmt.Sprintf("armor-ub3 ok=%v(dmg=%d)", adc > 0 && adc <= 160, adc))
		out = append(out, fmt.Sprintf("armor-plain ok=%v(dmg=%d)", dl == 200, dl))
		return out
	}

	vanilla := run(targets.vanilla)
	mcgo := run(targets.mcgo)
	for i := range vanilla {
		name := strings.Fields(vanilla[i])[0]
		vb := strings.Contains(vanilla[i], "=true")
		gb := strings.Contains(mcgo[i], "=true")
		t.Logf("%-14s vanilla=%s mcgo=%s", name, vanilla[i], mcgo[i])
		if vb != gb {
			t.Errorf("leg %s: vanilla %s, mc-go %s", name, vanilla[i], mcgo[i])
		}
	}
}

// probeState folds what the watch pump saw on A's connection.
type probeState struct {
	health  float32
	slotDmg map[int16]int32 // window-0 slot -> damage component
	spawn   map[int32]int32 // entity type -> last spawned eid
}

// lastNumber extracts the final number run in s — the data get reply
// renders the value just before the translate key.
func lastNumber(s string) float64 {
	last := ""
	for i := 0; i < len(s); {
		if s[i] >= '0' && s[i] <= '9' {
			j := i
			for j < len(s) && ((s[j] >= '0' && s[j] <= '9') || s[j] == '.') {
				j++
			}
			if run := strings.Trim(s[i:j], "."); run != "" {
				last = run
			}
			i = j
		} else {
			i++
		}
	}
	f, err := strconv.ParseFloat(last, 64)
	if err != nil {
		return -1
	}
	return f
}
