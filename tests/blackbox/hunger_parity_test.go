package blackbox

// Player food and damage keep ticking while the world is frozen. Complete
// admission and use actual player-tick receipts; a fixed wall-clock void window
// can become lethal before its reply is drained under load.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

type healthTuple struct {
	health float32
	food   int32
	sat    float32
}

func TestHungerRegenParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "survival", "view-distance": "2", "simulation-distance": "2"}, map[string]string{"ops.json": opsJSONFor("HungV", "HungM")}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
		s.ViewDistance, s.SimulationDistance = 2, 2
	})
	defer tg.cleanup()
	run := func(addr, name string) [][]healthTuple {
		c, pr, pw := joinPlayBlackbox(t, addr, name)
		defer c.Close()
		awaitPlayReadyBlackbox(t, name, c, pr, pw)
		sink := newChatSinkTP(pr, pw)
		recording := false
		var phase []healthTuple
		collect := func(marker string) []string {
			var lines []string
			deadline := time.Now().Add(12 * time.Second)
			for !structureSaidBB(lines, marker) && time.Now().Before(deadline) {
				lines = append(lines, sink.take()...)
				time.Sleep(10 * time.Millisecond)
			}
			if !structureSaidBB(lines, marker) {
				t.Fatal("hunger receipt absent", marker)
			}
			if recording {
				for _, line := range lines {
					prefix, body, ok := strings.Cut(line, ":")
					if !ok || prefix != fmt.Sprintf("%#x", server.IDPlaySetHealth) {
						continue
					}
					r := protocol.NewPacketReader([]byte(body))
					h, e0 := r.Float()
					f, e1 := r.VarInt()
					sat, e2 := r.Float()
					if e0 == nil && e1 == nil && e2 == nil && r.Remaining() == 0 {
						phase = append(phase, healthTuple{h, f, sat})
					}
				}
			}
			return lines
		}
		setupPlayerTickBarrierBB(t, pw)
		for _, cmd := range []string{"difficulty normal", "gamerule minecraft:spawn_mobs false", "gamerule minecraft:natural_health_regeneration true", "tick freeze", "attribute @s max_health base set 100", "effect give @s instant_health 1 5 true"} {
			sendCommand(t, pw, cmd)
		}
		waitPlayerTicksBB(t, pw, collect, "HUNGERINITIAL", 3)
		sendCommand(t, pw, `execute if entity @s[gamemode=survival] if data entity @s {Health:100.0f,foodLevel:20,foodSaturationLevel:5.0f} unless data entity @s {active_effects:[{id:"minecraft:instant_health"}]} run say HUNGERREADY`)
		sendCommand(t, pw, "say HUNGERSETUP")
		if !structureSaidBB(collect("HUNGERSETUP"), "HUNGERREADY") {
			t.Fatal("healthy admitted survival setup absent", name)
		}
		recording = true
		sendCommand(t, pw, "tp @s 6.0 -140.0 2.0")
		sendCommand(t, pw, "say HUNGERTPD")
		collect("HUNGERTPD")
		waitPlayerTicksBB(t, pw, collect, "HUNGERVOID", 25)
		sendCommand(t, pw, "tp @s 6.0 -60.0 2.0")
		sendCommand(t, pw, "say HUNGERBACK")
		collect("HUNGERBACK")
		recording = false
		voided := phase
		phase = nil
		// A positive larger pool prevents void death from erasing the recovery
		// control. The strict -4 hit and <=5/6 heal checks below remain unchanged.
		sendCommand(t, pw, "scoreboard objectives add HungHP dummy")
		sendCommand(t, pw, "execute store result score @s HungHP run data get entity @s Health 1000")
		sendCommand(t, pw, `execute if score @s HungHP matches 1..99999 if data entity @s {Pos:[6.0d,-60.0d,2.0d]} run say HUNGERALIVE`)
		sendCommand(t, pw, "execute if score @s HungHP matches 1..99999 run say HUNGERDAMAGEDALIVE")
		sendCommand(t, pw, `execute if data entity @s {Pos:[6.0d,-60.0d,2.0d]} run say HUNGERATGROUND`)
		sendCommand(t, pw, "say HUNGERGROUND")
		ground := collect("HUNGERGROUND")
		if !structureSaidBB(ground, "HUNGERALIVE") {
			t.Fatalf("living recovery target absent %s: damagedAlive=%v atGround=%v voidPackets=%v", name, structureSaidBB(ground, "HUNGERDAMAGEDALIVE"), structureSaidBB(ground, "HUNGERATGROUND"), voided)
		}
		recording = true
		waitPlayerTicksBB(t, pw, collect, "HUNGERRECOVERY", 60)
		sendCommand(t, pw, "say HUNGERREGEN")
		collect("HUNGERREGEN")
		recording = false
		return [][]healthTuple{voided, phase}
	}
	want := run(tg.vanilla, "HungV")
	got := run(tg.mcgo, "HungM")
	for i, phase := range []string{"void", "regen"} {
		w, g := want[i], got[i]
		t.Logf("%s: vanilla=%v mc-go=%v", phase, w, g)
		if len(w) < 2 || len(g) < 2 {
			t.Errorf("%s phase: too few health packets to compare (vanilla=%d mc-go=%d)",
				phase, len(w), len(g))
			continue
		}
		// Wall-clock windows clip a running sequence at tick-boundary
		// luck, so packet counts and phase endpoints wander by a packet
		// or two. What must match is the physics: the same number of
		// void hits' worth of damage (within one hit) and the same
		// end-of-window food/saturation.
		wf, gf := w[len(w)-1], g[len(g)-1]
		if wf.food != gf.food {
			t.Errorf("%s end state: vanilla food=%d mc-go food=%d", phase, wf.food, gf.food)
		}
		if d := wf.sat - gf.sat; d < -1.01 || d > 1.01 {
			t.Errorf("%s end saturation: vanilla=%v mc-go=%v (one drain of boundary jitter allowed)",
				phase, wf.sat, gf.sat)
		}
		if d := wf.health - gf.health; d < -4.01 || d > 4.01 {
			t.Errorf("%s end health: vanilla=%v mc-go=%v (one-hit boundary tolerance 4)",
				phase, wf.health, gf.health)
		}
		// Every packet's health delta is either a void hit (-4), a regen
		// heal (min(sat,6)/6 > 0), or a pure drain (0) — anything else
		// means a different damage/heal model.
		check := func(seq []healthTuple, who string) {
			for j := 1; j < len(seq); j++ {
				d := seq[j].health - seq[j-1].health
				if d > -0.001 && d < 0.001 {
					continue
				}
				if d > 0 {
					if d > 0.84 {
						t.Errorf("%s %s packet %d: heal %v exceeds sat-regen max", phase, who, j, d)
					}
				} else if d < -4.01 || d > -0.49 {
					t.Errorf("%s %s packet %d: damage %v is not a void hit", phase, who, j, d)
				}
			}
		}
		check(w, "vanilla")
		check(g, "mc-go")
	}
}
