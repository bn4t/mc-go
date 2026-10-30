package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func instantCommandReceiptBB(t *testing.T, pw *protocol.Writer, collect func(string) []string, effect, tail, marker string, wantDuration int32) {
	t.Helper()
	command := "effect give @s " + effect
	if tail != "" {
		command += " " + tail
	}
	sendCommand(t, pw, command)
	sendCommand(t, pw, "say "+marker)
	eid, _ := server.MobEffectID(effect)
	found := false
	for _, line := range collect(marker) {
		prefix, body, ok := strings.Cut(line, ":")
		if !ok || prefix != fmt.Sprintf("%#x", server.IDPlayUpdateMobEffect) {
			continue
		}
		r := protocol.NewPacketReader([]byte(body))
		_, e0 := r.VarInt()
		id, e1 := r.VarInt()
		amp, e2 := r.VarInt()
		duration, e3 := r.VarInt()
		_, e4 := r.Byte()
		if e0 == nil && e1 == nil && e2 == nil && e3 == nil && e4 == nil && r.Remaining() == 0 && id == eid && amp == 0 && duration == wantDuration {
			found = true
		}
	}
	if !found {
		t.Errorf("%s %q duration%d update receipt absent", effect, tail, wantDuration)
	}
}

func TestInstantCommandTickDispatchParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2"}, map[string]string{"ops.json": opsJSONFor("InstantTicks", "SatTicks")}, nil)
	defer targets.cleanup()
	for _, leg := range []struct{ name, addr string }{{"native", targets.vanilla}, {"go", targets.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			conn, pr, pw := joinPlayBlackbox(t, leg.addr, "InstantTicks")
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
					t.Fatal("instant barrier absent", marker)
				}
				return lines
			}
			barrier := func(marker string) []string { sendCommand(t, pw, "say "+marker); return collect(marker) }
			setupPlayerTickBarrierBB(t, pw)
			for _, cmd := range []string{"gamerule minecraft:spawn_mobs false", "gamerule minecraft:natural_health_regeneration false", "difficulty normal", "time set midnight", "gamemode survival @s", "scoreboard objectives add InstHP dummy", "scoreboard objectives add InstStart dummy", "scoreboard objectives add InstElapsed dummy"} {
				sendCommand(t, pw, cmd)
			}
			phase := 0
			prepare := func(low bool) {
				phase++
				sendCommand(t, pw, "effect clear @s")
				sendCommand(t, pw, "attribute @s max_health base set 1000")
				sendCommand(t, pw, "effect give @s instant_health 1 10 true")
				waitPlayerTicksBB(t, pw, collect, fmt.Sprintf("INSTPREHEAL%dX", phase), 27)
				want := 1000
				if low {
					sendCommand(t, pw, "damage @s 980 minecraft:generic")
					waitPlayerTicksBB(t, pw, collect, fmt.Sprintf("INSTPREDAMAGE%dX", phase), 27)
					want = 20
				}
				good, end := fmt.Sprintf("INSTREADY%dX", phase), fmt.Sprintf("INSTSTART%dX", phase)
				sendCommand(t, pw, fmt.Sprintf("execute if data entity @s {Health:%d.0f} run say %s", want, good))
				if !structureSaidBB(barrier(end), good) {
					t.Fatal("initial health not established", phase, want)
				}
			}
			for _, c := range []struct {
				tail   string
				ticks  int32
				health int
			}{{"", 1, 24}, {"3 0 true", 3, 32}} {
				prepare(true)
				instantCommandReceiptBB(t, pw, collect, "instant_health", c.tail, fmt.Sprintf("INSTHEALGIVEN%dX", phase), c.ticks)
				waitPlayerTicksBB(t, pw, collect, fmt.Sprintf("INSTHEALWAIT%dX", phase), 10)
				good, end := fmt.Sprintf("INSTHEALPROOF%dX", phase), fmt.Sprintf("INSTHEALEND%dX", phase)
				sendCommand(t, pw, fmt.Sprintf(`execute if data entity @s {Health:%d.0f} unless data entity @s {active_effects:[{id:"minecraft:instant_health"}]} run say %s`, c.health, good))
				if !structureSaidBB(barrier(end), good) {
					t.Errorf("finite heal %q health%d mismatch", c.tail, c.health)
				}
			}
			prepare(true)
			sendCommand(t, pw, "attribute @s max_health base set 40")
			instantCommandReceiptBB(t, pw, collect, "instant_health", "infinite 0 true", "INSTINFHEALGIVEN", -1)
			waitPlayerTicksBB(t, pw, collect, "INSTINFHEALWAIT", 25)
			sendCommand(t, pw, `execute if data entity @s {Health:40.0f,active_effects:[{id:"minecraft:instant_health",duration:-1}]} run say INSTINFHEALPROOF`)
			if !structureSaidBB(barrier("INSTINFHEALEND"), "INSTINFHEALPROOF") {
				t.Error("infinite instant heal did not recur")
			}
			// Fresh no-AI mobs isolate normal and inverted active tick delivery.
			for i, c := range []struct {
				kind, effect       string
				ticks, start, want int
			}{{"villager", "instant_health", 3, 1, 13}, {"villager", "instant_damage", 21, 20, 2}, {"zombie", "instant_damage", 3, 1, 13}, {"zombie", "instant_health", 21, 20, 2}, {"villager", "saturation", 1, 20, 20}} {
				tag := fmt.Sprintf("InstantMob%d", i)
				target := "@e[tag=" + tag + ",limit=1]"
				sendCommand(t, pw, fmt.Sprintf(`summon %s ~2 ~ ~ {NoAI:1b,NoGravity:1b,Silent:1b,PersistenceRequired:1b,Health:%d.0f,Tags:["%s"]}`, c.kind, c.start, tag))
				ready, end := fmt.Sprintf("INSTMOBREADY%dX", i), fmt.Sprintf("INSTMOBSTART%dX", i)
				sendCommand(t, pw, fmt.Sprintf("execute if data entity %s {Health:%d.0f} run say %s", target, c.start, ready))
				if !structureSaidBB(barrier(end), ready) {
					t.Fatal("mob initial health absent", tag)
				}
				tail := fmt.Sprintf(" %d 0 true", c.ticks)
				if c.effect == "saturation" {
					tail = ""
				}
				sendCommand(t, pw, "effect give "+target+" "+c.effect+tail)
				waitPlayerTicksBB(t, pw, collect, fmt.Sprintf("INSTMOBWAIT%dX", i), c.ticks+5)
				good, end := fmt.Sprintf("INSTMOBPROOF%dX", i), fmt.Sprintf("INSTMOBEND%dX", i)
				sendCommand(t, pw, fmt.Sprintf(`execute if data entity %s {Health:%d.0f} unless data entity %s {active_effects:[{id:"minecraft:%s"}]} run say %s`, target, c.want, target, c.effect, good))
				if !structureSaidBB(barrier(end), good) {
					t.Errorf("%s %s ticks%d health%d mismatch", c.kind, c.effect, c.ticks, c.want)
				}
			}
			prepare(false)
			instantCommandReceiptBB(t, pw, collect, "instant_damage", "", "INSTHARMGIVEN", 1)
			waitPlayerTicksBB(t, pw, collect, "INSTHARMWAIT", 10)
			sendCommand(t, pw, `execute if data entity @s {Health:994.0f} unless data entity @s {active_effects:[{id:"minecraft:instant_damage"}]} run say INSTHARMPROOF`)
			if !structureSaidBB(barrier("INSTHARMEND"), "INSTHARMPROOF") {
				t.Error("single-tick harm mismatch")
			}
			prepare(false)
			instantCommandReceiptBB(t, pw, collect, "instant_damage", "infinite 0 true", "INSTINFHARMGIVEN", -1)
			sendCommand(t, pw, "scoreboard players operation @s InstStart = @s BBPlayerTick")
			waitPlayerTicksBB(t, pw, collect, "INSTINFHARMWAIT", 25)
			sendCommand(t, pw, "scoreboard players operation @s InstElapsed = @s BBPlayerTick")
			sendCommand(t, pw, "scoreboard players operation @s InstElapsed -= @s InstStart")
			sendCommand(t, pw, "execute store result score @s InstHP run data get entity @s Health 1000")
			sendCommand(t, pw, `execute if score @s InstElapsed matches 25..35 if score @s InstHP matches 976000..988000 if data entity @s {active_effects:[{id:"minecraft:instant_damage",duration:-1}]} run say INSTINFHARMPROOF`)
			if !structureSaidBB(barrier("INSTINFHARMEND"), "INSTINFHARMPROOF") {
				t.Error("infinite instant harm did not recur")
			}
			sendCommand(t, pw, "effect clear @s")
			barrier("INSTHPCLEAR")
			satConn, satPr, satPw := joinPlayBlackbox(t, leg.addr, "SatTicks")
			defer satConn.Close()
			awaitPlayReadyBlackbox(t, leg.addr, satConn, satPr, satPw)
			satSink := newChatSinkTP(satPr, satPw)
			satCollect := func(marker string) []string {
				var lines []string
				deadline := time.Now().Add(12 * time.Second)
				for !structureSaidBB(lines, marker) && time.Now().Before(deadline) {
					lines = append(lines, satSink.take()...)
					time.Sleep(10 * time.Millisecond)
				}
				if !structureSaidBB(lines, marker) {
					t.Fatal("saturation barrier absent", marker)
				}
				return lines
			}
			setupPlayerTickBarrierBB(t, satPw)
			sendCommand(t, satPw, "gamemode survival @s")
			sendCommand(t, satPw, `execute if data entity @s {foodLevel:20,foodSaturationLevel:5.0f,foodExhaustionLevel:0.0f} run say SATINITIAL`)
			sendCommand(t, satPw, "say SATSTART")
			if !structureSaidBB(satCollect("SATSTART"), "SATINITIAL") {
				t.Fatal("saturation initial pools absent")
			}
			for i, c := range []struct {
				tail  string
				ticks int32
				sat   int
			}{{"", 1, 7}, {"3 0 true", 3, 13}, {"infinite 0 true", -1, 20}} {
				sendCommand(t, satPw, "effect clear @s saturation")
				instantCommandReceiptBB(t, satPw, satCollect, "saturation", c.tail, fmt.Sprintf("SATGIVEN%dX", i), c.ticks)
				waitPlayerTicksBB(t, satPw, satCollect, fmt.Sprintf("SATWAIT%dX", i), 25)
				good, end := fmt.Sprintf("SATPROOF%dX", i), fmt.Sprintf("SATEND%dX", i)
				condition := fmt.Sprintf(`execute if data entity @s {foodLevel:20,foodSaturationLevel:%d.0f}`, c.sat)
				if c.ticks > 0 {
					condition += ` unless data entity @s {active_effects:[{id:"minecraft:saturation"}]}`
				} else {
					condition += ` if data entity @s {active_effects:[{id:"minecraft:saturation",duration:-1}]}`
				}
				sendCommand(t, satPw, condition+" run say "+good)
				sendCommand(t, satPw, "say "+end)
				if !structureSaidBB(satCollect(end), good) {
					t.Errorf("saturation %q pool%d mismatch", c.tail, c.sat)
				}
			}
		})
	}
}

// A frozen mob cannot hide an add-time dose behind its existing active ticks.
func TestFrozenInstantRegiveNoDoseParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2"}, map[string]string{"ops.json": opsJSONFor("InstantRegive")}, nil)
	defer targets.cleanup()
	for _, leg := range []struct{ name, addr string }{{"native", targets.vanilla}, {"go", targets.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			conn, pr, pw := joinPlayBlackbox(t, leg.addr, "InstantRegive")
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
					t.Fatal("frozen instant barrier absent", marker)
				}
				return lines
			}
			barrier := func(marker string) []string { sendCommand(t, pw, "say "+marker); return collect(marker) }
			setupPlayerTickBarrierBB(t, pw)
			for _, cmd := range []string{"tick freeze", "scoreboard objectives add RegiveBefore dummy", "scoreboard objectives add RegiveNow dummy", "scoreboard objectives add RegiveGoal dummy", "scoreboard objectives add RegiveProof dummy", "scoreboard objectives add RegiveAmp dummy"} {
				sendCommand(t, pw, cmd)
			}
			waitPlayerTicksBB(t, pw, collect, "REGIVEFROZENSET", 2)
			sendCommand(t, pw, "execute store result score @s RegiveBefore run time query gametime")
			sendCommand(t, pw, `summon villager ~2 ~ ~ {NoAI:1b,NoGravity:1b,Silent:1b,Health:1.0f,Tags:["FrozenInstant"]}`)
			target := "@e[tag=FrozenInstant,limit=1]"
			sendCommand(t, pw, "effect give "+target+" instant_health 10 1 true")
			sendCommand(t, pw, "execute store result score @s RegiveAmp run data get entity "+target+" active_effects[0].amplifier")
			sendCommand(t, pw, `execute if score @s RegiveAmp matches 1 if data entity @e[tag=FrozenInstant,limit=1] {Health:1.0f,active_effects:[{id:"minecraft:instant_health",duration:10}]} run say REGIVEADDEDNODOSE`)
			if !structureSaidBB(barrier("REGIVEADDEDEND"), "REGIVEADDEDNODOSE") {
				t.Error("accepted stored status applied before entity tick")
			}
			sendCommand(t, pw, "execute store success score @s RegiveProof run effect give "+target+" instant_health 1 0 true")
			sendCommand(t, pw, "execute store result score @s RegiveNow run time query gametime")
			sendCommand(t, pw, "execute store result score @s RegiveAmp run data get entity "+target+" active_effects[0].amplifier")
			sendCommand(t, pw, `execute if score @s RegiveProof matches 0 if score @s RegiveAmp matches 1 if score @s RegiveNow = @s RegiveBefore if data entity @e[tag=FrozenInstant,limit=1] {Health:1.0f,active_effects:[{id:"minecraft:instant_health",duration:10}]} run say REGIVEDECLINEDNODOSE`)
			if !structureSaidBB(barrier("REGIVEDECLINEDEND"), "REGIVEDECLINEDNODOSE") {
				t.Error("declined weaker status changed dose, instance, or frozen world")
			}
			sendCommand(t, pw, "scoreboard players operation @s RegiveGoal = @s RegiveBefore")
			sendCommand(t, pw, "scoreboard players add @s RegiveGoal 1")
			sendCommand(t, pw, "tick step 1")
			stepped := false
			deadline := time.Now().Add(15 * time.Second)
			for attempt := 0; !stepped && time.Now().Before(deadline); attempt++ {
				good, end := fmt.Sprintf("REGIVESTEP%dX", attempt), fmt.Sprintf("REGIVESTEPEND%dX", attempt)
				sendCommand(t, pw, "execute store result score @s RegiveNow run time query gametime")
				sendCommand(t, pw, "execute if score @s RegiveNow = @s RegiveGoal run say "+good)
				stepped = structureSaidBB(barrier(end), good)
			}
			if !stepped {
				t.Fatal("single world-step receipt absent")
			}
			sendCommand(t, pw, "execute store result score @s RegiveAmp run data get entity "+target+" active_effects[0].amplifier")
			sendCommand(t, pw, `execute if score @s RegiveAmp matches 1 if data entity @e[tag=FrozenInstant,limit=1] {Health:9.0f,active_effects:[{id:"minecraft:instant_health",duration:9}]} run say REGIVEEXISTINGTICK`)
			if !structureSaidBB(barrier("REGIVEEXISTINGEND"), "REGIVEEXISTINGTICK") {
				t.Error("existing stronger instance did not own the single entity tick")
			}
		})
	}
}
