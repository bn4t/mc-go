package blackbox

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// Witch.aiStep drinks: underwater → water_breathing (0.15/tick),
// burning → fire_resistance (0.15), hurt → healing (0.05, +4 health),
// far target → swiftness (0.5). Each drink is a 32-tick
// use-duration potion held in the mainhand; the finish clears the
// slot and applies the contents.
func TestWitchDrinkParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	type result struct {
		fireRes, healed, speed bool
	}
	run := func(addr string) result {
		var r result
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(120 * time.Second))
		obs := &fleeObs{}

		effects := func() string {
			sendCommand(t, pw, "execute as @e[type=minecraft:witch] run data get entity @s active_effects")
			base := len(obs.chats)
			fleePump(t, c, pr, pw, obs, 1500*time.Millisecond)
			var sb strings.Builder
			for _, ln := range obs.chats[base:] {
				if strings.Contains(ln, "witch") {
					sb.WriteString(ln)
				}
			}
			return sb.String()
		}
		health := func() float64 {
			sendCommand(t, pw, "data get entity @e[type=minecraft:witch,limit=1] Health")
			base := len(obs.chats)
			fleePump(t, c, pr, pw, obs, 1500*time.Millisecond)
			for _, ln := range obs.chats[base:] {
				if i := strings.Index(ln, "witch"); i >= 0 {
					rest := ln[i+5:]
					// Trim the reply prefix word boundary: the
					// number starts at the first digit/-.
					j := 0
					for j < len(rest) && !(rest[j] >= '0' && rest[j] <= '9' || rest[j] == '-') {
						j++
					}
					end := j
					for end < len(rest) && strings.IndexByte("0123456789.eE+-", rest[end]) >= 0 {
						end++
					}
					if v, err := strconv.ParseFloat(rest[j:end], 64); err == nil {
						return v
					}
				}
			}
			return -1
		}

		sendCommand(t, pw, "gamemode creative P")
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		sendCommand(t, pw, "tp P 100.5 -60 0.5")
		fleePump(t, c, pr, pw, obs, 800*time.Millisecond)

		// Burning witch → fire_resistance drink.
		sendCommand(t, pw, "summon minecraft:witch 105.5 -59 0.5 {Fire:100s}")
		fleePump(t, c, pr, pw, obs, 4500*time.Millisecond)
		r.fireRes = strings.Contains(effects(), "fire_resistance")
		sendCommand(t, pw, "kill @e[type=minecraft:witch]")
		fleePump(t, c, pr, pw, obs, 500*time.Millisecond)

		// Hurt witch → healing drink (+4 health back).
		sendCommand(t, pw, "summon minecraft:witch 105.5 -59 0.5")
		fleePump(t, c, pr, pw, obs, 600*time.Millisecond)
		sendCommand(t, pw, "damage @e[type=minecraft:witch,limit=1] 10 minecraft:generic")
		fleePump(t, c, pr, pw, obs, 7000*time.Millisecond)
		r.healed = health() > 17
		sendCommand(t, pw, "kill @e[type=minecraft:witch]")
		fleePump(t, c, pr, pw, obs, 500*time.Millisecond)

		// Survival player 15 blocks out → swiftness drink. The pick
		// needs dist²>121, so the drink can only start while the
		// witch is still approaching — the wider window means ~20
		// ticks of the 0.5 roll before it settles at range 10.
		sendCommand(t, pw, "effect give P minecraft:resistance 60 4")
		sendCommand(t, pw, "gamemode survival P")
		sendCommand(t, pw, "tp P 115.5 -59 0.5")
		sendCommand(t, pw, "summon minecraft:witch 130.5 -59 0.5")
		fleePump(t, c, pr, pw, obs, 9000*time.Millisecond)
		r.speed = strings.Contains(effects(), "speed")
		sendCommand(t, pw, "gamemode creative P")
		return r
	}

	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	t.Logf("van %+v", van)
	t.Logf("mc  %+v", mc)
	for name, r := range map[string]result{"vanilla": van, "mc-go": mc} {
		if !r.fireRes {
			t.Errorf("%s burning witch never gained fire_resistance", name)
		}
		if !r.healed {
			t.Errorf("%s hurt witch never healed above 17", name)
		}
		if !r.speed {
			t.Errorf("%s far-target witch never gained speed", name)
		}
	}
}

// witchUsingByte scans a set_entity_data payload for the witch's
// DATA_USING_ITEM entry — index 17, boolean serializer 8 — anywhere
// the pair appears (the flag is the only idx-17 writer on a witch).
func witchUsingByte(data []byte) (byte, bool) {
	for i := 0; i+2 < len(data); i++ {
		if data[i] == 0x11 && data[i+1] == 0x08 && data[i+2] <= 1 {
			return data[i+2], true
		}
	}
	return 0, false
}

// TestWitchHandUseParity pins Witch.setUsingItem's DATA_USING_ITEM
// flag — metadata index 17, boolean serializer (type 8): a witch
// mid-drink broadcasts 1 and clears to 0 when the potion lands.
func TestWitchHandUseParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	witchID, _ := server.EntityTypeID("witch")
	run := func(addr string) [2]bool {
		var sawUp, sawDown bool
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(120 * time.Second))
		obs := &fleeObs{}
		sendCommand(t, pw, "gamemode creative P")
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		sendCommand(t, pw, "tp P 100.5 -60 0.5")
		fleePump(t, c, pr, pw, obs, 600*time.Millisecond)
		sendCommand(t, pw, "summon minecraft:witch 106.5 -59 0.5 {Fire:100s}")

		var witchEID int32 = -1
		upAt, downAt := -1, -1
		deadline := time.Now().Add(9 * time.Second)
		for time.Now().Before(deadline) && !(sawUp && sawDown) {
			_ = c.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
			p, err := pr.ReadPacket()
			if err != nil {
				continue
			}
			switch p.ID {
			case server.IDPlayKeepAliveCB:
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
			case server.IDPlaySyncPos:
				r := protocol.NewPacketReader(p.Data)
				if id, err := r.VarInt(); err == nil {
					var b protocol.Buffer
					b.VarInt(id)
					_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
				}
			case server.IDPlaySpawnEntity:
				r := protocol.NewPacketReader(p.Data)
				id, _ := r.VarInt()
				r.UUID()
				typ, _ := r.VarInt()
				if typ == witchID {
					witchEID = id
				}
			case server.IDPlayEntityMetadata:
				r := protocol.NewPacketReader(p.Data)
				eid, err := r.VarInt()
				if err != nil || eid != witchEID {
					continue
				}
				v, ok := witchUsingByte(p.Data)
				if !ok {
					continue
				}
				if v == 1 && upAt < 0 {
					upAt = len(obs.chats)
					sawUp = true
				}
				if v == 0 && sawUp && downAt < 0 {
					downAt = len(obs.chats)
					sawDown = true
				}
			case server.IDPlaySystemChatCB:
				obs.chats = append(obs.chats, chatTextBlackbox(t, p.Data))
			}
		}
		return [2]bool{sawUp, sawDown}
	}

	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	t.Logf("van %v mc %v", van, mc)
	for name, r := range map[string][2]bool{"vanilla": van, "mc-go": mc} {
		if !r[0] {
			t.Errorf("%s witch never broadcast hand-active during drink", name)
		}
		if !r[1] {
			t.Errorf("%s witch hand-active byte never cleared", name)
		}
	}
}
