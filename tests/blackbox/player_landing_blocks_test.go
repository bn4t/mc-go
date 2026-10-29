package blackbox

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// Landing blocks alter damage, sounds and the fall statistic for the same
// 9.5-block drop; the observer also distinguishes locally played sounds.
func TestPlayerLandingBlocksParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("Faller", "Watcher")}, nil)
	defer targets.cleanup()
	type result struct {
		health             float32
		damageType         int32
		hit                bool
		honeyEvent         bool
		smallFall, bigFall bool
		selfFall           bool
		fallen             int32
	}
	run := func(addr string, block string, landing float64, sneak bool, commands []string) result {
		out := result{health: 20}
		c, pr, pw := joinPlayBlackbox(t, addr, "Faller")
		defer c.Close()
		if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
			t.Fatal(err)
		}
		observer, opr, opw := joinPlayBlackbox(t, addr, "Watcher")
		defer observer.Close()
		_ = opw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		obs := &fleeObs{}
		sendCommand(t, pw, "fill -3 -60 -3 3 -48 3 air")
		sendCommand(t, pw, "fill -3 -61 -3 3 -61 3 stone")
		sendCommand(t, pw, "tp @s 0.5 -60 0.5")
		sendCommand(t, pw, "tp Watcher 3 -60 0.5")
		sendCommand(t, pw, "gamemode adventure @s")
		sendCommand(t, pw, "gamerule minecraft:natural_health_regeneration false")
		fleePump(t, c, pr, pw, obs, 3500*time.Millisecond)
		fleePump(t, observer, opr, opw, &fleeObs{}, 500*time.Millisecond)
		var position [3]float64
		before := int32(0)
		for _, row := range drainStats(t, c, pr, pw, &position, 3*time.Second) {
			if row.cat == 8 && row.id == 10 {
				before = row.val
			}
		}

		sendCommand(t, pw, "attribute @s minecraft:safe_fall_distance base set 3")
		sendCommand(t, pw, "attribute @s minecraft:fall_damage_multiplier base set 1")
		sendCommand(t, pw, "attribute @s minecraft:safe_fall_distance modifier remove minecraft:fall_test")
		sendCommand(t, pw, "effect clear @s")
		sendCommand(t, pw, "effect give @s minecraft:instant_health 1 10 true")
		sendCommand(t, pw, "item replace entity @s armor.feet with air")
		sendCommand(t, pw, "setblock 0 -61 0 "+block)
		for _, command := range commands {
			sendCommand(t, pw, command)
		}
		{
			sendCommand(t, pw, fmt.Sprintf("tp @s 0.5 %v 0.5", landing+9.5))
			fleePump(t, c, pr, pw, obs, 500*time.Millisecond)
			var input protocol.Buffer
			if sneak {
				input.Byte(0x20)
			} else {
				input.Byte(0)
			}
			_ = pw.WritePacket(server.IDPlayPlayerInputSB, input.Bytes())
			for i := 1; i <= 19; i++ {
				y := landing + 9.5 - float64(i)*0.5
				var b protocol.Buffer
				b.Double(0.5)
				b.Double(y)
				b.Double(0.5)
				b.Bool(i == 19)
				_ = pw.WritePacket(server.IDPlayMovePosSB, b.Bytes())
				time.Sleep(55 * time.Millisecond)
			}
		}
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			_ = c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
			p, err := pr.ReadPacket()
			if err != nil {
				continue
			}
			if p.ID == server.IDPlayKeepAliveCB {
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
			}
			if p.ID == server.IDPlayEntityEvent {
				rd := protocol.NewPacketReader(p.Data)
				_, _ = rd.Int()
				status, _ := rd.Byte()
				out.honeyEvent = out.honeyEvent || status == 54
			}
			if p.ID == 0x75 {
				id, _ := protocol.NewPacketReader(p.Data).VarInt()
				out.selfFall = out.selfFall || id-1 == 1318 || id-1 == 1308
			}
			if p.ID == server.IDPlayDamageEvent {
				rd := protocol.NewPacketReader(p.Data)
				_, _ = rd.VarInt()
				out.damageType, _ = rd.VarInt()
				out.hit = true
			}
			if p.ID == server.IDPlaySetHealth {
				out.health, _ = protocol.NewPacketReader(p.Data).Float()
			}

		}

		after := before
		for _, row := range drainStats(t, c, pr, pw, &position, 3*time.Second) {
			if row.cat == 8 && row.id == 10 {
				after = row.val
			}
		}
		out.fallen = after - before
		until := time.Now().Add(400 * time.Millisecond)
		for time.Now().Before(until) {
			_ = observer.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			packet, err := opr.ReadPacket()
			if err != nil {
				continue
			}
			if packet.ID == server.IDPlayKeepAliveCB {
				_ = opw.WritePacket(server.IDPlayKeepAliveSB, packet.Data)
			}
			if packet.ID == 0x75 {
				id, _ := protocol.NewPacketReader(packet.Data).VarInt()
				out.smallFall = out.smallFall || id-1 == 1318
				out.bigFall = out.bigFall || id-1 == 1308
			}
		}
		return out
	}
	for _, tc := range []struct {
		name, block string
		landing     float64
		sneak       bool
		commands    []string
		health      float32
		damage      bool
	}{
		{"hay", "hay_block", -60, false, nil, 19, true},
		{"honey", "honey_block", -60.0625, false, nil, 19, true},
		{"honey_creative", "honey_block", -60.0625, false, []string{"gamemode creative @s"}, 20, false},
		{"slime", "slime_block", -60, false, nil, 20, false},
		{"slime_sneak", "slime_block", -60, true, nil, 20, false},
		{"bed", "red_bed[part=foot,facing=north]", -60.4375, false, []string{"setblock 0 -61 -1 red_bed[part=head,facing=north]"}, 19, true},
		{"bed_sneak", "red_bed[part=foot,facing=north]", -60.4375, true, []string{"setblock 0 -61 -1 red_bed[part=head,facing=north]"}, 19, true},
		{"dripstone", "pointed_dripstone[vertical_direction=up,thickness=tip]", -60.3125, false, nil, 2, true},
		{"powder", "powder_snow", -60, false, nil, 20, false},
		{"powder_creative", "powder_snow", -60, false, []string{"gamemode creative @s"}, 20, false},
		{"feather", "stone", -60, false, []string{"item replace entity @s armor.feet with leather_boots[enchantments={feather_falling:4}]"}, 16.88, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, m := run(targets.vanilla, tc.block, tc.landing, tc.sneak, tc.commands), run(targets.mcgo, tc.block, tc.landing, tc.sneak, tc.commands)
			t.Logf("vanilla=%+v mcgo=%+v", v, m)
			expectedFall := int32(950)
			switch tc.name {
			case "slime_sneak", "powder", "powder_creative", "honey_creative":
				expectedFall = 0
			case "bed", "bed_sneak":
				expectedFall = 475
			case "dripstone":
				expectedFall = 1200
			}
			if v.fallen != expectedFall {
				t.Fatalf("vanilla fall stats %v want%v", v.fallen, expectedFall)
			}
			if v.selfFall {
				t.Fatal("subject received locally played fall sound")
			}

			if math.Abs(float64(v.health-tc.health)) > 0.001 || v.hit != tc.damage || v != m {
				t.Fatalf("landing vanilla=%+v mcgo=%+v want health%v hit%v", v, m, tc.health, tc.damage)
			}
			if strings.HasPrefix(tc.name, "honey") && !v.honeyEvent {
				t.Fatal("missing honey event54")
			}
			if strings.HasPrefix(tc.name, "powder") && !v.bigFall {
				t.Fatal("missing powder big-fall sound")
			}
		})
	}
}
