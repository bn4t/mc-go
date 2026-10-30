package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestFiniteEffectHighAmplifierCadenceParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2"}, map[string]string{"ops.json": opsJSONFor("EffectPulse")}, nil)
	defer targets.cleanup()
	for _, leg := range []struct{ name, addr string }{{"native", targets.vanilla}, {"go", targets.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			conn, pr, pw := joinPlayBlackbox(t, leg.addr, "EffectPulse")
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
					t.Fatalf("missing exact barrier%s", marker)
				}
				return lines
			}
			setupPlayerTickBarrierBB(t, pw)
			for _, cmd := range []string{"difficulty peaceful", "gamerule minecraft:natural_health_regeneration false", "gamemode survival @s", "attribute @s max_health base set 20"} {
				sendCommand(t, pw, cmd)
			}
			phase := 0
			for _, effect := range []string{"regeneration", "poison", "wither"} {
				eid, ok := server.MobEffectID(effect)
				if !ok {
					t.Fatal(effect)
				}
				for _, amp := range []int{0, 5, 6, 32, 37, 255} {
					phase++
					sendCommand(t, pw, "effect clear @s")
					sendCommand(t, pw, "effect give @s instant_health 1 10 true")
					waitPlayerTicksBB(t, pw, collect, fmt.Sprintf("PULSEHEAL%dX", phase), 2)
					start := 20
					if effect == "regeneration" {
						sendCommand(t, pw, "damage @s 10 minecraft:generic")
						start = 10
					}
					ready, end := fmt.Sprintf("PULSEREADY%dX", phase), fmt.Sprintf("PULSESTART%dX", phase)
					sendCommand(t, pw, fmt.Sprintf("execute if data entity @s {Health:%d.0f} run say %s", start, ready))
					sendCommand(t, pw, "say "+end)
					if !structureSaidBB(collect(end), ready) {
						t.Fatalf("%s amp%d initial health not established", effect, amp)
					}
					sendCommand(t, pw, fmt.Sprintf("effect give @s %s 1 %d true", effect, amp))
					given := fmt.Sprintf("PULSEGIVEN%dX", phase)
					sendCommand(t, pw, "say "+given)
					received := false
					for _, line := range collect(given) {
						prefix, body, ok := strings.Cut(line, ":")
						if !ok || prefix != fmt.Sprintf("%#x", server.IDPlayUpdateMobEffect) {
							continue
						}
						r := protocol.NewPacketReader([]byte(body))
						_, e0 := r.VarInt()
						id, e1 := r.VarInt()
						a, e2 := r.VarInt()
						duration, e3 := r.VarInt()
						_, e4 := r.Byte()
						if e0 == nil && e1 == nil && e2 == nil && e3 == nil && e4 == nil && r.Remaining() == 0 && id == eid && a == int32(amp) && duration == 20 {
							received = true
						}
					}
					if !received {
						t.Fatalf("%s amp%d update receipt absent", effect, amp)
					}
					waitPlayerTicksBB(t, pw, collect, fmt.Sprintf("PULSEWAIT%dX", phase), 25)
					want := start
					if amp != 0 && amp != 32 {
						if effect == "regeneration" {
							want = 20
						} else {
							want = 18
						}
					}
					good, end := fmt.Sprintf("PULSEDONE%dX", phase), fmt.Sprintf("PULSEEND%dX", phase)
					sendCommand(t, pw, fmt.Sprintf(`execute if data entity @s {Health:%d.0f} unless data entity @s {active_effects:[{id:"minecraft:%s"}]} run say %s`, want, effect, good))
					sendCommand(t, pw, "say "+end)
					pass := structureSaidBB(collect(end), good)
					t.Logf("%s amp%d health%d expired=%v", effect, amp, want, pass)
					if !pass {
						t.Errorf("%s amp%d finite pulse/expiry mismatch", effect, amp)
					}
				}
			}
			sendCommand(t, pw, "scoreboard objectives add PulseProof dummy")
			for _, effect := range []string{"regeneration", "poison", "wither"} {
				for _, amp := range []int{6, 32} {
					phase++
					tag := fmt.Sprintf("PulseMob%d", phase)
					target := "@e[tag=" + tag + ",limit=1]"
					start := 20
					if effect == "regeneration" {
						start = 10
					}
					sendCommand(t, pw, fmt.Sprintf(`summon villager ~2 ~ ~ {NoAI:1b,NoGravity:1b,Silent:1b,Health:%d.0f,Tags:["%s"]}`, start, tag))
					ready, end := fmt.Sprintf("MOBPULSEREADY%dX", phase), fmt.Sprintf("MOBPULSESTART%dX", phase)
					sendCommand(t, pw, fmt.Sprintf("execute if data entity %s {Health:%d.0f} run say %s", target, start, ready))
					sendCommand(t, pw, "say "+end)
					if !structureSaidBB(collect(end), ready) {
						t.Fatalf("%s mob amp%d initial body absent", effect, amp)
					}
					sendCommand(t, pw, fmt.Sprintf("effect give %s %s 1 %d true", target, effect, amp))
					sendCommand(t, pw, fmt.Sprintf("execute store result score @s PulseProof run data get entity %s active_effects[0].amplifier", target))
					given, end := fmt.Sprintf("MOBPULSEGIVEN%dX", phase), fmt.Sprintf("MOBPULSEADDED%dX", phase)
					sendCommand(t, pw, fmt.Sprintf(`execute if score @s PulseProof matches %d if data entity %s {active_effects:[{id:"minecraft:%s"}]} run say %s`, amp, target, effect, given))
					sendCommand(t, pw, "say "+end)
					if !structureSaidBB(collect(end), given) {
						t.Fatalf("%s mob amp%d active instance receipt absent", effect, amp)
					}
					waitPlayerTicksBB(t, pw, collect, fmt.Sprintf("MOBPULSEWAIT%dX", phase), 25)
					want := start
					if amp == 6 {
						if effect == "regeneration" {
							want = 20
						} else {
							want = 18
						}
					}
					good, end := fmt.Sprintf("MOBPULSEDONE%dX", phase), fmt.Sprintf("MOBPULSEEND%dX", phase)
					sendCommand(t, pw, fmt.Sprintf(`execute if data entity %s {Health:%d.0f} unless data entity %s {active_effects:[{id:"minecraft:%s"}]} run say %s`, target, want, target, effect, good))
					sendCommand(t, pw, "say "+end)
					pass := structureSaidBB(collect(end), good)
					t.Logf("mob %s amp%d health%d expired=%v", effect, amp, want, pass)
					if !pass {
						t.Errorf("mob %s amp%d finite pulse/expiry mismatch", effect, amp)
					}
					sendCommand(t, pw, "kill "+target)
				}
			}
		})
	}
}
