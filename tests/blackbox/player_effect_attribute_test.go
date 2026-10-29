package blackbox

import (
	"fmt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"testing"
	"time"
)

func TestPlayerEffectAttributeLifecycleParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2"}, map[string]string{"ops.json": opsJSONFor("EffectAttrs")}, nil)
	defer targets.cleanup()
	controls := []struct {
		effect, attribute, id string
		amount                int
	}{
		{"absorption", "max_absorption", "absorption", 8000},
		{"haste", "attack_speed", "haste", 200},
		{"health_boost", "max_health", "health_boost", 8000},
		{"invisibility", "waypoint_transmit_range", "waypoint_transmit_range_hide", -2000},
		{"jump_boost", "safe_fall_distance", "jump_boost", 2000},
		{"luck", "luck", "luck", 2000},
		{"mining_fatigue", "attack_speed", "mining_fatigue", -200},
		{"slowness", "movement_speed", "slowness", -300},
		{"speed", "movement_speed", "speed", 400},
		{"strength", "attack_damage", "strength", 6000},
		{"unluck", "luck", "unluck", -2000},
		{"weakness", "attack_damage", "weakness", -8000},
	}
	for _, addr := range []string{targets.vanilla, targets.mcgo} {
		conn, pr, pw := joinPlayBlackbox(t, addr, "EffectAttrs")
		awaitPlayReadyBlackbox(t, addr, conn, pr, pw)
		sink := newChatSinkTP(pr, pw)
		collect := func(marker string) []string {
			var lines []string
			deadline := time.Now().Add(12 * time.Second)
			for time.Now().Before(deadline) && !structureSaidBB(lines, marker) {
				lines = append(lines, sink.take()...)
				time.Sleep(10 * time.Millisecond)
			}
			if !structureSaidBB(lines, marker) {
				t.Fatalf("%s missing%s", addr, marker)
			}
			return lines
		}
		sendCommand(t, pw, "scoreboard objectives add EffectAttrProof dummy")
		for i, c := range controls {
			sendCommand(t, pw, "effect give @s minecraft:"+c.effect+" 30 1 true")
			sendCommand(t, pw, "execute store result score @s EffectAttrProof run attribute @s minecraft:"+c.attribute+" modifier value get minecraft:effect."+c.id+" 1000")
			added, end := fmt.Sprintf("EFFECTADDED%dX", i), fmt.Sprintf("EFFECTADDEND%dX", i)
			sendCommand(t, pw, fmt.Sprintf("execute if score @s EffectAttrProof matches %d run say %s", c.amount, added))
			sendCommand(t, pw, "say "+end)
			if !structureSaidBB(collect(end), added) {
				t.Errorf("%s %s modifier add/amplification mismatch", addr, c.effect)
			}
			sendCommand(t, pw, "effect clear @s minecraft:"+c.effect)
			sendCommand(t, pw, "execute store success score @s EffectAttrProof run attribute @s minecraft:"+c.attribute+" modifier value get minecraft:effect."+c.id)
			removed, end := fmt.Sprintf("EFFECTREMOVED%dX", i), fmt.Sprintf("EFFECTREMOVEEND%dX", i)
			sendCommand(t, pw, "execute if score @s EffectAttrProof matches 0 run say "+removed)
			sendCommand(t, pw, "say "+end)
			if !structureSaidBB(collect(end), removed) {
				t.Errorf("%s %s modifier survived clear", addr, c.effect)
			}
		}
		sendCommand(t, pw, "attribute @s minecraft:movement_speed modifier add minecraft:effect.speed 0.75 add_multiplied_total")
		sendCommand(t, pw, "effect clear @s minecraft:speed")
		sendCommand(t, pw, "execute store result score @s EffectAttrProof run attribute @s minecraft:movement_speed modifier value get minecraft:effect.speed 1000")
		sendCommand(t, pw, "execute if score @s EffectAttrProof matches 750 run say EFFECTABSENTIDPRESERVED")
		sendCommand(t, pw, "say EFFECTABSENTEND")
		if !structureSaidBB(collect("EFFECTABSENTEND"), "EFFECTABSENTIDPRESERVED") {
			t.Errorf("%s absent-effect removal erased command ID", addr)
		}
		conn.Close()
	}
}

// Explicit Jump Boost modifier edits must affect damage even while the status
// effect remains active. Query receipts establish the actual raw modifier first.
func TestPlayerJumpModifierFallParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2"}, map[string]string{"ops.json": opsJSONFor("JumpEdit")}, nil)
	defer targets.cleanup()
	for _, addr := range []string{targets.vanilla, targets.mcgo} {
		conn, pr, pw := joinPlayBlackbox(t, addr, "JumpEdit")
		awaitPlayReadyBlackbox(t, addr, conn, pr, pw)
		sink := newChatSinkTP(pr, pw)
		collect := func(marker string) []string {
			var lines []string
			deadline := time.Now().Add(12 * time.Second)
			for time.Now().Before(deadline) && !structureSaidBB(lines, marker) {
				lines = append(lines, sink.take()...)
				time.Sleep(10 * time.Millisecond)
			}
			if !structureSaidBB(lines, marker) {
				t.Fatalf("%s missing%s", addr, marker)
			}
			return lines
		}
		sendCommand(t, pw, "forceload add -16 -16 0 0")
		loaded := false
		loadDeadline := time.Now().Add(15 * time.Second)
		for n := 0; time.Now().Before(loadDeadline) && !loaded; n++ {
			yes, end := fmt.Sprintf("JUMPLOADED%dX", n), fmt.Sprintf("JUMPLOADEND%dX", n)
			sendCommand(t, pw, "execute if loaded -16 150 -16 if loaded -16 150 0 if loaded 0 150 -16 if loaded 0 150 0 run say "+yes)
			sendCommand(t, pw, "say "+end)
			loaded = structureSaidBB(collect(end), yes)
		}
		if !loaded {
			t.Fatal("jump pad chunks not loaded")
		}
		setupPlayerTickBarrierBB(t, pw)
		sendCommand(t, pw, "scoreboard objectives add JumpModProof dummy")
		sendCommand(t, pw, "gamerule minecraft:natural_health_regeneration false")
		for i, override := range []bool{false, true} {
			for _, command := range []string{"gamemode creative @s", "tp @s 8 151 8", "fill -2 150 -2 2 162 2 air", "fill -2 150 -2 2 150 2 stone", "effect clear @s", "attribute @s minecraft:safe_fall_distance base set 3", "attribute @s minecraft:fall_damage_multiplier base set 1", "effect give @s instant_health 1 10 true", "effect give @s jump_boost 60 1 true", "attribute @s minecraft:safe_fall_distance modifier remove minecraft:effect.jump_boost"} {
				sendCommand(t, pw, command)
			}
			want, store, health := 0, "success", 14
			if override {
				sendCommand(t, pw, "attribute @s minecraft:safe_fall_distance modifier add minecraft:effect.jump_boost 6 add_value")
				want, store, health = 6000, "result", 20
			}
			sendCommand(t, pw, "execute store "+store+" score @s JumpModProof run attribute @s minecraft:safe_fall_distance modifier value get minecraft:effect.jump_boost 1000")
			raw, end := fmt.Sprintf("JUMPRAW%dX", i), fmt.Sprintf("JUMPRAWEND%dX", i)
			sendCommand(t, pw, fmt.Sprintf("execute if score @s JumpModProof matches %d run say %s", want, raw))
			sendCommand(t, pw, "say "+end)
			if !structureSaidBB(collect(end), raw) {
				t.Fatalf("%s raw jump edit not established", addr)
			}
			sendCommand(t, pw, "gamemode adventure @s")
			sendCommand(t, pw, "tp @s 0.5 160.5 0.5")
			ready, end := fmt.Sprintf("JUMPPOS%dX", i), fmt.Sprintf("JUMPPOSEND%dX", i)
			sendCommand(t, pw, `execute if data entity @s {Pos:[0.5d,160.5d,0.5d],Health:20.0f,active_effects:[{id:"minecraft:jump_boost"}]} if block 0 150 0 stone run say `+ready)
			sendCommand(t, pw, "say "+end)
			if !structureSaidBB(collect(end), ready) {
				t.Fatalf("%s jump position/health/effect/pad setup invalid", addr)
			}
			// Drain actual hurt-time ticks so a smaller second hit cannot be
			// hidden by the preceding control's damage immunity window.
			waitPlayerTicksBB(t, pw, collect, fmt.Sprintf("JUMPHURT%d", i), 25)
			for y := 160.0; y >= 151; y -= .5 {
				var body protocol.Buffer
				body.Double(.5)
				body.Double(y)
				body.Double(.5)
				body.Bool(y == 151)
				if err := pw.WritePacket(server.IDPlayMovePosSB, body.Bytes()); err != nil {
					t.Fatal(err)
				}
				time.Sleep(55 * time.Millisecond)
			}
			landed := false
			deadline := time.Now().Add(6 * time.Second)
			for attempt := 0; !landed && time.Now().Before(deadline); attempt++ {
				yes, end := fmt.Sprintf("JUMPLAND%d_%dX", i, attempt), fmt.Sprintf("JUMPLANDEND%d_%dX", i, attempt)
				sendCommand(t, pw, fmt.Sprintf(`execute if data entity @s {Pos:[0.5d,151.0d,0.5d],Health:%d.0f} run say %s`, health, yes))
				sendCommand(t, pw, "say "+end)
				landed = structureSaidBB(collect(end), yes)
			}
			if !landed {
				sendCommand(t, pw, "data get entity @s Health")
				sendCommand(t, pw, "say JUMPDIAGNOSTIC")
				t.Errorf("%s override%v fall damage mismatch: %q", addr, override, collect("JUMPDIAGNOSTIC"))
			}
		}
		conn.Close()
	}
}
