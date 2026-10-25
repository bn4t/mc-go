// Environmental conversion parity (26.2):
//   - Zombie.doUnderWaterConversion: eye submerged 600 ticks starts a
//     300-tick conversion (DATA_DROWNED_CONVERSION_ID, zombie meta 17)
//     that finishes even if the mob surfaces — zombie/zombie_villager
//     -> drowned, husk -> zombie first.
//   - Skeleton.tick's freeze chain: 140 ticks in powder snow, then a
//     300-tick DATA_STRAY_CONVERSION_ID countdown that CANCELS if the
//     mob steps out, converting skeleton -> stray.
// convertTo(single(keepEquipment, preserveName)) spawns the fresh
// entity before discarding the old; a non-silent conversion emits
// level events 1040 (drowned) / 1048 (stray).

package blackbox

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

type convObs struct {
	chats      []string
	lvlEvents  []int32
	spawnTypes []int32
	removes    int
}

func convPump(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, obs *convObs, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		p, err := pr.ReadPacket()
		if err != nil {
			continue
		}
		switch p.ID {
		case server.IDPlayKeepAliveCB:
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
		case server.IDPlaySyncPos:
			r := protocol.NewPacketReader(p.Data)
			id, _ := r.VarInt()
			var b protocol.Buffer
			b.VarInt(id)
			_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
		case server.IDPlaySystemChatCB:
			if txt := chatTextBlackbox(t, p.Data); txt != "" {
				obs.chats = append(obs.chats, txt)
			}
		case server.IDPlayLevelEvent:
			r := protocol.NewPacketReader(p.Data)
			ev, _ := r.Int()
			obs.lvlEvents = append(obs.lvlEvents, ev)
		case server.IDPlaySpawnEntity:
			r := protocol.NewPacketReader(p.Data)
			_, _ = r.VarInt()
			_, _ = r.Read(16)
			typ, _ := r.VarInt()
			obs.spawnTypes = append(obs.spawnTypes, typ)
		case server.IDPlayRemoveEntity:
			obs.removes++
		}
	}
}

// convHasEntity runs `execute if entity <sel>` — the bare conditional
// replies commands.execute.conditional.pass / .fail.
func convHasEntity(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, obs *convObs, sel string) bool {
	sendCommand(t, pw, "execute if entity "+sel)
	base := len(obs.chats)
	deadline := time.Now().Add(900 * time.Millisecond)
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
		p, err := pr.ReadPacket()
		if err != nil {
			continue
		}
		if p.ID == server.IDPlaySystemChatCB {
			if txt := chatTextBlackbox(t, p.Data); txt != "" {
				obs.chats = append(obs.chats, txt)
			}
		} else if p.ID == server.IDPlayKeepAliveCB {
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
		}
	}
	for _, ln := range obs.chats[base:] {
		if strings.Contains(ln, "conditional.pass") {
			return true
		}
		if strings.Contains(ln, "conditional.fail") {
			return false
		}
	}
	return false
}

func TestMobConvertParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	run := func(addr string) []string {
		var out []string
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		_ = c.SetDeadline(time.Now().Add(300 * time.Second))
		obs := &convObs{}
		sendCommand(t, pw, "gamemode creative P")
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		convPump(t, c, pr, pw, obs, 600*time.Millisecond)

		// Drown leg: a walled 3x3x3 water pool keeps both the zombie
		// and the converted drowned inside; night keeps them off the
		// sun-burn path during the 45s wait.
		sendCommand(t, pw, "time set 18000")
		sendCommand(t, pw, "fill ~-2 ~-1 ~-2 ~2 ~3 ~2 minecraft:stone hollow")
		sendCommand(t, pw, "fill ~-1 ~ ~-1 ~1 ~2 ~1 minecraft:water")
		obs.lvlEvents = nil
		obs.spawnTypes = nil
		obs.removes = 0
		sendCommand(t, pw, "summon minecraft:zombie ~ ~1 ~ {}")
		convPump(t, c, pr, pw, obs, 50*time.Second)
		drowned := convHasEntity(t, c, pr, pw, obs, "@e[type=minecraft:drowned]")
		noZombie := !convHasEntity(t, c, pr, pw, obs, "@e[type=minecraft:zombie]")
		saw1040 := false
		for _, ev := range obs.lvlEvents {
			if ev == 1040 {
				saw1040 = true
			}
		}
		out = append(out, fmt.Sprintf("drown drowned=%v gone=%v ev1040=%v", drowned, noZombie, saw1040))
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		sendCommand(t, pw, "fill ~-2 ~-1 ~-2 ~2 ~3 ~2 minecraft:air")
		convPump(t, c, pr, pw, obs, 600*time.Millisecond)

		// Freeze leg: a walled stone box keeps the skeleton inside a
		// 3x3 powder-snow patch — 22s to stray.
		sendCommand(t, pw, "fill ~-2 ~-1 ~-2 ~2 ~2 ~2 minecraft:stone hollow")
		sendCommand(t, pw, "fill ~-1 ~ ~-1 ~1 ~1 ~1 minecraft:powder_snow")
		obs.lvlEvents = nil
		sendCommand(t, pw, "summon minecraft:skeleton ~ ~ ~ {}")
		convPump(t, c, pr, pw, obs, 28*time.Second)
		stray := convHasEntity(t, c, pr, pw, obs, "@e[type=minecraft:stray]")
		noSkel := !convHasEntity(t, c, pr, pw, obs, "@e[type=minecraft:skeleton]")
		saw1048 := false
		for _, ev := range obs.lvlEvents {
			if ev == 1048 {
				saw1048 = true
			}
		}
		out = append(out, fmt.Sprintf("freeze stray=%v gone=%v ev1048=%v", stray, noSkel, saw1048))
		return out
	}

	van := run(targets.vanilla)
	got := run(targets.mcgo)
	for k := range van {
		t.Logf("leg %d vanilla: %s", k, van[k])
		t.Logf("leg %d mc-go:   %s", k, got[k])
		if van[k] != got[k] {
			t.Errorf("leg %d mismatch: %q vs %q", k, van[k], got[k])
		}
	}
}

// TestConvertCarryParity pins which fields ConversionType.convert
// carries onto the new entity — measured on vanilla 26.2: the flag
// block (NoGravity/Silent/LeftHanded/PersistenceRequired/
// CanPickUpLoot/IsBaby/CustomNameVisible/CanBreakDoors/Invulnerable),
// the custom name, entity tags, motion, active_effects, Pos/Rotation
// — while Health, AbsorptionAmount, UUID and attributes reset fresh.
func snbtFromChat(line string) string {
	var sb strings.Builder
	for i := 0; i+5 < len(line); i++ {
		if line[i:i+5] == "text\x00" {
			var n int
			l := i + 5
			for l < len(line) {
				n = n<<7 | int(line[l]&0x7f)
				if line[l]&0x80 == 0 {
					l++
					break
				}
				l++
			}
			end := l + n
			if end > len(line) {
				end = len(line)
			}
			sb.WriteString(line[l:end])
			i = end
		}
	}
	return sb.String()
}

func dataGetAll(t *testing.T, sink *chatSink, pw *protocol.Writer, sel string) string {
	t.Helper()
	sendCommand(t, pw, "data get entity "+sel)
	sendCommand(t, pw, "say ZEND")
	deadline := time.Now().Add(6 * time.Second)
	out := ""
	for time.Now().Before(deadline) {
		end := false
		for _, line := range sink.take() {
			if strings.Contains(line, "data.entity.query") {
				out = snbtFromChat(line)
			}
			if strings.Contains(line, "ZEND") {
				end = true
			}
		}
		if end {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	return out
}

func wantKeys(t *testing.T, label, what, dump string, keys []string) {
	t.Helper()
	if dump == "" {
		t.Errorf("%s %s: no entity matched", label, what)
		return
	}
	for _, k := range keys {
		if !strings.Contains(dump, k) {
			t.Errorf("%s %s: missing %q in %s", label, what, k, dump)
		}
	}
}

func TestConvertCarryParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("CV")}, nil)
	defer targets.cleanup()

	for _, s := range []struct{ label, addr string }{
		{"vanilla", targets.vanilla}, {"mcgo", targets.mcgo},
	} {
		t.Run(s.label, func(t *testing.T) {
			ca, pra, pwA, _ := joinPlayEid(t, s.addr, "CV")
			defer ca.Close()
			sink := newChatSinkTP(pra, pwA)
			sink.cap = 1 << 20 // full entity NBT replies are >4KB
			sendCommand(t, pwA, "tp CV 0.5 -60 0.5")

			// Piglin → zombified_piglin after 300 overworld ticks.
			sendCommand(t, pwA, `summon minecraft:piglin 4 -60 4 {LeftHanded:1b,Silent:1b,NoGravity:1b,PersistenceRequired:1b,CanPickUpLoot:1b,Health:9f,AbsorptionAmount:6f,IsBaby:0b,CustomNameVisible:1b,CustomName:'{"text":"CVP"}',Tags:["convtag"],active_effects:[{id:"minecraft:speed",amplifier:2,duration:1500}]}`)
			time.Sleep(500 * time.Millisecond)
			t.Logf("%s piglin pre: %s", s.label, dataGetAll(t, sink, pwA, "@e[type=minecraft:piglin,limit=1]"))
			sendCommand(t, pwA, "tick sprint 320")
			time.Sleep(1200 * time.Millisecond)
			zp := dataGetAll(t, sink, pwA, "@e[type=minecraft:zombified_piglin,limit=1]")
			t.Logf("%s zpiglin post: %s", s.label, zp)
			wantKeys(t, s.label, "zpiglin", zp, []string{
				"NoGravity1b", "IsBaby0b", "Silent1b", "LeftHanded1b",
				"PersistenceRequired1b", "CanPickUpLoot1b",
				"CustomName", `"CVP"`, "CustomNameVisible1b", "convtag",
				"active_effects", "minecraft:speed", "minecraft:nausea",
				"Health20.0f", "AbsorptionAmount0.0f", "Brain",
				"CanBreakDoors", "DrownedConversionTime",
				"anger_end_time", "minecraft:movement_speed",
				// Vanilla's inWaterTime sentinel is -1 while the
				// conversion isn't armed — the fresh zombified
				// piglin keeps it.
				"InWaterTime-1",
			})
			if strings.Contains(zp, "NoAI") {
				t.Errorf("%s zpiglin dump has NoAI — vanilla writes it only while set", s.label)
			}

			// Zombie → drowned: readAdditionalSaveData honors
			// DrownedConversionTime — it restarts the countdown via
			// startUnderWaterConversion, which then runs to the end
			// even out of water. Deterministic and fast; the live
			// 600-wet-tick accrual is covered by TestMobConvertParity.
			sendCommand(t, pwA, `summon minecraft:zombie 20 -60 20 {CanBreakDoors:1b,LeftHanded:1b,DrownedConversionTime:10}`)
			sendCommand(t, pwA, "tick sprint 40")
			time.Sleep(800 * time.Millisecond)
			dw := dataGetAll(t, sink, pwA, "@e[type=minecraft:drowned,limit=1]")
			t.Logf("%s drowned post: %s", s.label, dw)
			t.Logf("%s zombie post: %s", s.label, dataGetAll(t, sink, pwA, "@e[type=minecraft:zombie,limit=1]"))
			wantKeys(t, s.label, "drowned", dw, []string{
				"CanBreakDoors1b", "LeftHanded1b", "Health",
				"Brain", "InWaterTime-1", "DrownedConversionTime",
				// convertTo() finalizeSpawns the new mob — the
				// zombie-family attribute rolls land on it.
				"minecraft:spawn_reinforcements",
			})

			// Skeleton → stray in powder snow (140 + 300): walled
			// so it can't step out of the snow cell mid-timer.
			for _, f := range []string{
				"fill 11 -61 11 11 -57 11 minecraft:stone",
				"fill 14 -61 11 14 -57 14 minecraft:stone",
				"fill 11 -61 11 14 -57 11 minecraft:stone",
				"fill 11 -61 14 14 -57 14 minecraft:stone",
				"fill 11 -57 11 14 -57 14 minecraft:stone",
				"fill 12 -60 12 13 -60 13 minecraft:powder_snow",
			} {
				sendCommand(t, pwA, f)
			}
			// The freeze countdown cancels on the first tick the
			// skeleton isn't registered as in-snow, so arm it after
			// it settles rather than on the summon.
			sendCommand(t, pwA, `summon minecraft:skeleton 12.5 -60 12.5 {CustomName:'{"text":"CVS"}'}`)
			time.Sleep(400 * time.Millisecond)
			sendCommand(t, pwA, `data merge entity @e[type=minecraft:skeleton,limit=1] {StrayConversionTime:10}`)
			sendCommand(t, pwA, "tick sprint 40")
			time.Sleep(800 * time.Millisecond)
			st := dataGetAll(t, sink, pwA, "@e[type=minecraft:stray,limit=1]")
			t.Logf("%s stray post: %s", s.label, st)
			wantKeys(t, s.label, "stray", st, []string{
				"CustomName", `"CVS"`, "Brain", "Health20.0f",
			})
		})
	}
}
