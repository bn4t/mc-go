package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestInfinitePeriodicEffectsParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2"}, map[string]string{"ops.json": opsJSONFor("InfinitePulse")}, nil)
	defer targets.cleanup()
	for _, leg := range []struct{ name, addr string }{{"native", targets.vanilla}, {"go", targets.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			conn, pr, pw := joinPlayBlackbox(t, leg.addr, "InfinitePulse")
			defer conn.Close()
			awaitPlayReadyBlackbox(t, leg.addr, conn, pr, pw)
			sink := newChatSinkTP(pr, pw)
			phase := 0
			dead, respawned := false, false
			collect := func(marker string) []string {
				var lines []string
				deadline := time.Now().Add(12 * time.Second)
				for !structureSaidBB(lines, marker) && time.Now().Before(deadline) {
					lines = append(lines, sink.take()...)
					time.Sleep(10 * time.Millisecond)
				}
				if !structureSaidBB(lines, marker) {
					t.Fatalf("missing exact barrier%s", marker)
				}
				for _, line := range lines {
					prefix, body, ok := strings.Cut(line, ":")
					if !ok {
						continue
					}
					r := protocol.NewPacketReader([]byte(body))
					if prefix == fmt.Sprintf("%#x", server.IDPlaySetHealth) {
						if h, e := r.Float(); e == nil && h == 0 {
							dead = true
						}
					}
					if prefix == fmt.Sprintf("%#x", server.IDPlayRespawn) {
						respawned = true
					}
				}
				return lines
			}
			barrier := func(tag string) []string { sendCommand(t, pw, "say "+tag); return collect(tag) }
			setupPlayerTickBarrierBB(t, pw)
			for _, cmd := range []string{"difficulty peaceful", "gamerule minecraft:natural_health_regeneration false", "gamemode survival @s", "attribute @s max_health base set 20", "scoreboard objectives add InfHealth dummy", "scoreboard objectives add InfStart dummy", "scoreboard objectives add InfElapsed dummy", "scoreboard objectives add InfWorldA dummy", "scoreboard objectives add InfWorldB dummy"} {
				sendCommand(t, pw, cmd)
			}
			prepare := func(effect string) {
				phase++
				sendCommand(t, pw, "effect clear @s")
				sendCommand(t, pw, "effect give @s instant_health 1 10 true")
				waitPlayerTicksBB(t, pw, collect, fmt.Sprintf("INFHEAL%dX", phase), 27)
				start := 20
				if effect == "regeneration" {
					sendCommand(t, pw, "damage @s 10 minecraft:generic")
					start = 10
				}
				good, end := fmt.Sprintf("INFREADY%dX", phase), fmt.Sprintf("INFSTART%dX", phase)
				sendCommand(t, pw, fmt.Sprintf("execute if data entity @s {Health:%d.0f} run say %s", start, good))
				sendCommand(t, pw, "say "+end)
				if !structureSaidBB(collect(end), good) {
					t.Fatal("initial health not established", effect, phase)
				}
			}
			give := func(effect string, amp int) {
				sendCommand(t, pw, fmt.Sprintf("effect give @s %s infinite %d true", effect, amp))
				marker := fmt.Sprintf("INFGIVEN%dX", phase)
				lines := barrier(marker)
				eid, _ := server.MobEffectID(effect)
				received := false
				for _, line := range lines {
					prefix, body, ok := strings.Cut(line, ":")
					if !ok || prefix != fmt.Sprintf("%#x", server.IDPlayUpdateMobEffect) {
						continue
					}
					r := protocol.NewPacketReader([]byte(body))
					_, e0 := r.VarInt()
					id, e1 := r.VarInt()
					a, e2 := r.VarInt()
					d, e3 := r.VarInt()
					_, e4 := r.Byte()
					if e0 == nil && e1 == nil && e2 == nil && e3 == nil && e4 == nil && r.Remaining() == 0 && id == eid && a == int32(amp) && d == -1 {
						received = true
					}
				}
				if !received {
					t.Fatalf("%s amp%d infinite update receipt absent", effect, amp)
				}
			}
			for _, effect := range []string{"regeneration", "poison", "wither"} {
				for _, amp := range []int{6, 32} {
					prepare(effect)
					give(effect, amp)
					ticks, lo, hi := 25, 16, 18
					if amp == 32 {
						ticks = 100
						switch effect {
						case "regeneration":
							lo, hi = 12, 13
						case "poison":
							lo, hi = 15, 16
						case "wither":
							lo, hi = 17, 18
						}
					} else if effect == "regeneration" {
						lo, hi = 20, 20
					}
					sendCommand(t, pw, "scoreboard players operation @s InfStart = @s BBPlayerTick")
					waitPlayerTicksBB(t, pw, collect, fmt.Sprintf("INFWAIT%dX", phase), ticks)
					sendCommand(t, pw, "scoreboard players operation @s InfElapsed = @s BBPlayerTick")
					sendCommand(t, pw, "scoreboard players operation @s InfElapsed -= @s InfStart")
					sendCommand(t, pw, "execute store result score @s InfHealth run data get entity @s Health 1000")
					good, end := fmt.Sprintf("INFDONE%dX", phase), fmt.Sprintf("INFEND%dX", phase)
					sendCommand(t, pw, fmt.Sprintf(`execute if score @s InfElapsed matches %d..%d if score @s InfHealth matches %d..%d if data entity @s {active_effects:[{id:"minecraft:%s",duration:-1}]} run say %s`, ticks, ticks+10, lo*1000, hi*1000, effect, good))
					sendCommand(t, pw, "say "+end)
					pass := structureSaidBB(collect(end), good)
					t.Logf("%s amp%d infinite health%d..%d=%v", effect, amp, lo, hi, pass)
					if !pass {
						t.Errorf("%s amp%d infinite bounded pulse receipt absent", effect, amp)
					}
				}
			}
			// Freeze first: no mob pulse can precede the frozen-body witness.
			prepare("regeneration")
			sendCommand(t, pw, "tick freeze")
			waitPlayerTicksBB(t, pw, collect, "INFFROZENSET", 2)
			sendCommand(t, pw, "execute store result score @s InfWorldA run time query gametime")
			sendCommand(t, pw, `summon villager ~2 ~ ~ {NoAI:1b,NoGravity:1b,Silent:1b,Health:10.0f,Tags:["InfClockMob"]}`)
			sendCommand(t, pw, "effect give @e[tag=InfClockMob,limit=1] regeneration infinite 6 true")
			give("regeneration", 6)
			waitPlayerTicksBB(t, pw, collect, "INFFROZENWAIT", 25)
			sendCommand(t, pw, "execute store result score @s InfWorldB run time query gametime")
			sendCommand(t, pw, `execute if score @s InfWorldA = @s InfWorldB if data entity @s {Health:20.0f,active_effects:[{id:"minecraft:regeneration",duration:-1}]} if data entity @e[tag=InfClockMob,limit=1] {Health:10.0f,active_effects:[{id:"minecraft:regeneration",duration:-1}]} run say INFFROZENPROOF`)
			if !structureSaidBB(barrier("INFFROZENEND"), "INFFROZENPROOF") {
				t.Error("frozen infinite player/mob clock split absent")
			}
			sendCommand(t, pw, "tick unfreeze")
			waitPlayerTicksBB(t, pw, collect, "INFTHAWEDWAIT", 25)
			sendCommand(t, pw, `execute if data entity @e[tag=InfClockMob,limit=1] {Health:20.0f,active_effects:[{id:"minecraft:regeneration",duration:-1}]} run say INFTHAWEDPROOF`)
			if !structureSaidBB(barrier("INFTHAWEDEND"), "INFTHAWEDPROOF") {
				t.Error("thawed mob infinite pulses absent")
			}
			dead, respawned = false, false
			sendCommand(t, pw, "kill @s")
			deadline := time.Now().Add(15 * time.Second)
			serial := 0
			for !dead && time.Now().Before(deadline) {
				serial++
				barrier(fmt.Sprintf("INFDEAD%dX", serial))
			}
			if !dead {
				t.Fatal("actual death receipt absent")
			}
			var request protocol.Buffer
			request.VarInt(0)
			if err := pw.WritePacket(server.IDPlayClientStatusSB, request.Bytes()); err != nil {
				t.Fatal(err)
			}
			deadline = time.Now().Add(15 * time.Second)
			for !respawned && time.Now().Before(deadline) {
				serial++
				barrier(fmt.Sprintf("INFRESPAWN%dX", serial))
			}
			if !respawned {
				t.Fatal("actual respawn receipt absent")
			}
			if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
				t.Fatal(err)
			}
			waitPlayerTicksBB(t, pw, collect, "INFNEWBODY", 5)
			sendCommand(t, pw, `execute unless data entity @s {active_effects:[{id:"minecraft:regeneration"}]} run say INFNEWBODYCLEAR`)
			if !structureSaidBB(barrier("INFNEWBODYEND"), "INFNEWBODYCLEAR") {
				t.Error("old infinite effect survived new body")
			}
			prepare("regeneration")
			give("regeneration", 32)
			waitPlayerTicksBB(t, pw, collect, "INFNEWBODYLOW", 55)
			sendCommand(t, pw, "execute store result score @s InfHealth run data get entity @s Health 1000")
			sendCommand(t, pw, `execute if score @s InfHealth matches 11000..12000 if data entity @s {active_effects:[{id:"minecraft:regeneration",duration:-1}]} run say INFNEWBODYPULSE`)
			if !structureSaidBB(barrier("INFNEWBODYLOWEND"), "INFNEWBODYPULSE") {
				t.Error("new body low-amplifier infinite pulse absent")
			}
		})
	}
}

func TestInfiniteFoodEffectsParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2"}, map[string]string{"ops.json": opsJSONFor("InfiniteFood")}, nil)
	defer targets.cleanup()
	for _, leg := range []struct{ name, addr string }{{"native", targets.vanilla}, {"go", targets.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			conn, pr, pw := joinPlayBlackbox(t, leg.addr, "InfiniteFood")
			defer conn.Close()
			awaitPlayReadyBlackbox(t, leg.addr, conn, pr, pw)
			sink := newChatSinkTP(pr, pw)
			collect := func(marker string) []string {
				var lines []string
				deadline := time.Now().Add(12 * time.Second)
				for !structureSaidBB(lines, marker) && time.Now().Before(deadline) {
					lines = append(lines, sink.take()...)
					time.Sleep(10 * time.Millisecond)
				}
				if !structureSaidBB(lines, marker) {
					t.Fatal("food barrier absent", marker)
				}
				return lines
			}
			barrier := func(marker string) []string { sendCommand(t, pw, "say "+marker); return collect(marker) }
			setupPlayerTickBarrierBB(t, pw)
			for _, cmd := range []string{"gamerule minecraft:spawn_mobs false", "difficulty normal", "gamerule minecraft:natural_health_regeneration false", "gamemode survival @s", "scoreboard objectives add InfFoodProof dummy"} {
				sendCommand(t, pw, cmd)
			}
			sendCommand(t, pw, `execute if data entity @s {foodLevel:20,foodSaturationLevel:5.0f,foodExhaustionLevel:0.0f} run say INFFOODINITIAL`)
			if !structureSaidBB(barrier("INFFOODSTART"), "INFFOODINITIAL") {
				t.Fatal("initial food pools not established")
			}
			sendCommand(t, pw, "effect give @s hunger infinite 0 true")
			waitPlayerTicksBB(t, pw, collect, "INFFOODHUNGER", 25)
			sendCommand(t, pw, "execute store result score @s InfFoodProof run data get entity @s foodExhaustionLevel 1000")
			sendCommand(t, pw, `execute if score @s InfFoodProof matches 100..200 if data entity @s {foodLevel:20,active_effects:[{id:"minecraft:hunger",duration:-1}]} run say INFFOODHUNGERPROOF`)
			if !structureSaidBB(barrier("INFFOODHUNGEREND"), "INFFOODHUNGERPROOF") {
				t.Error("infinite hunger did not add exhaustion")
			}
			sendCommand(t, pw, "effect clear @s hunger")
			sendCommand(t, pw, "effect give @s hunger 1 255 true")
			waitPlayerTicksBB(t, pw, collect, "INFFOODDRAIN", 25)
			sendCommand(t, pw, `execute if data entity @s {foodLevel:19,foodSaturationLevel:0.0f} unless data entity @s {active_effects:[{id:"minecraft:hunger"}]} run say INFFOODLOW`)
			if !structureSaidBB(barrier("INFFOODDRAINEND"), "INFFOODLOW") {
				t.Fatal("finite food-drain control not established")
			}
			sendCommand(t, pw, "effect give @s saturation infinite 0 true")
			waitPlayerTicksBB(t, pw, collect, "INFFOODSAT", 25)
			sendCommand(t, pw, `execute if data entity @s {foodLevel:20,foodSaturationLevel:20.0f,active_effects:[{id:"minecraft:saturation",duration:-1}]} run say INFFOODSATPROOF`)
			if !structureSaidBB(barrier("INFFOODSATEND"), "INFFOODSATPROOF") {
				t.Error("infinite saturation did not repeatedly feed")
			}
		})
	}
}
