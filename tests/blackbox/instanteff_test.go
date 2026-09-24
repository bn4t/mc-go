//go:build blackbox

package blackbox

// TestInstantEffectParity pins instant_health / instant_damage on
// /effect give: vanilla's HealOrHarmMobEffect applies once when the
// instance is added — instant_health heals 4<<amplifier (clamped to
// max health), instant_damage hurts 6<<amplifier as magic.

import (
	"math"
	"net"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestInstantEffectParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("A", "C")}, nil)
	defer targets.cleanup()

	// legs are sequential; want is the absolute health after the
	// command (instant_health clamps at max_health 20).
	legs := []struct {
		cmd  string
		want float64
	}{
		{"damage A 12 minecraft:generic", 8},
		{"effect give A minecraft:instant_health", 12},
		{"effect give A minecraft:instant_health 1 0", 16},
		{"effect give A minecraft:instant_health 1 2", 20}, // 4<<2 = 16, clamped
		{"effect give A minecraft:instant_damage", 14},
		{"effect give A minecraft:instant_damage 1 1", 2}, // 6<<1 = 12
	}
	for _, s := range []struct{ label, addr string }{
		{"vanilla", targets.vanilla}, {"mcgo", targets.mcgo},
	} {
		t.Run(s.label, func(t *testing.T) {
			ca, pra, pwA, _ := joinPlayEid(t, s.addr, "A")
			defer ca.Close()
			cc, prc, pwc := joinPlayBlackbox(t, s.addr, "C")
			defer cc.Close()
			sinkC := newChatSinkTP(prc, pwc)
			sendCommand(t, pwc, "gamemode survival A")
			sendCommand(t, pwc, "gamerule minecraft:natural_health_regeneration false")
			sendCommand(t, pwc, "tp A 0.5 -60 0.5")
			waitSurvival(t, sinkC, pwc, pwA)
			time.Sleep(3600 * time.Millisecond) // past join invulnerability
			for _, leg := range legs {
				sendCommand(t, pwc, leg.cmd)
				// Health updates land on the next nbt read; the 1.3s
				// sleep also clears the 20-tick hurt cooldown so each
				// instant_damage leg isn't absorbed by the last.
				time.Sleep(1300 * time.Millisecond)
				got := nbtFloatOf(t, sinkC, pwc, "A", "Health")
				if math.Abs(float64(got)-leg.want) > 0.01 {
					t.Fatalf("%s %q: health %v, want %v", s.label, leg.cmd, got, leg.want)
				}
			}

			// hiddenEffect downgrade: a stronger-but-shorter give pushes
			// the running instance into hiddenEffect; when it expires
			// the weaker one resumes (MobEffectInstance.update +
			// downgradeToHiddenEffect). Watch the amplifier carried by
			// Update Mob Effect packets on A's own conn.
			sendCommand(t, pwc, "effect give A minecraft:speed 30 0")
			time.Sleep(300 * time.Millisecond)
			sendCommand(t, pwc, "effect give A minecraft:speed 3 1")
			// active_effects is part of the entity save: the indexed
			// amplifier query hits while amp 1 is active — but only
			// because it is nonzero; amplifier omits at 0.
			if st, got := dataGetAmp(t, sinkC, pwc, "active_effects[0].amplifier"); st != "query" || got != 1 {
				t.Fatalf("%s active_effects[0].amplifier mid-leg: %q/%v, want query/1", s.label, st, got)
			}
			if got := pumpSpeedAmp(t, ca, pra, pwA, 1200*time.Millisecond); got != 1 {
				t.Fatalf("%s speed amplifier mid-leg: %v, want 1", s.label, got)
			}
			// The 3s amp-1 instance ends ~2s in; wait past it and
			// require the resumed amp-0 update packet.
			if got := pumpSpeedAmp(t, ca, pra, pwA, 3000*time.Millisecond); got != 0 {
				t.Fatalf("%s speed amplifier after downgrade: %v, want 0", s.label, got)
			}
			// Resumed instance is amplifier 0 — the field is omitted
			// from the save compound, so the indexed query comes back
			// nothing_found on both servers.
			if st := dataGetState(t, sinkC, pwc, "active_effects[0].amplifier"); st != "nothing_found" {
				t.Fatalf("%s amplifier@0 state: %q, want nothing_found", s.label, st)
			}
			if st := dataGetState(t, sinkC, pwc, "active_effects[0].hidden_effect"); st != "nothing_found" {
				t.Fatalf("%s hidden_effect state: %q, want nothing_found", s.label, st)
			}
		})
	}
}

// pumpSpeedAmp drains the conn for d, answering keepalives and
// teleports, and returns the amplifier of the last Update Mob Effect
// for speed (mob_effect registry id 0). Returns -1 when none arrived.
func pumpSpeedAmp(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, d time.Duration) int32 {
	t.Helper()
	deadline := time.Now().Add(d)
	amp := int32(-1)
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(time.Now().Add(time.Until(deadline)))
		p, err := pr.ReadPacket()
		if err != nil {
			break
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
		case server.IDPlayUpdateMobEffect:
			r := protocol.NewPacketReader(p.Data)
			if _, err := r.VarInt(); err == nil { // entity id
				if eff, err := r.VarInt(); err == nil && eff == 0 {
					if a, err := r.VarInt(); err == nil {
						amp = a
					}
				}
			}
		}
	}
	_ = c.SetReadDeadline(time.Time{})
	return amp
}

// dataGetState classifies the /data get reply for a path:
// "query" when a value came back, "nothing_found" for the path-miss
// error, "invalid" for a parse-level failure, "" for silence.
func dataGetState(t *testing.T, sink *chatSink, pw *protocol.Writer, path string) string {
	t.Helper()
	sendCommand(t, pw, "data get entity A "+path)
	sendCommand(t, pw, "say ZEND")
	deadline := time.Now().Add(6 * time.Second)
	got := ""
	for time.Now().Before(deadline) {
		end := false
		for _, line := range sink.take() {
			if strings.Contains(line, "data.entity.query") {
				got = "query"
			}
			if strings.Contains(line, "nbtpath.nothing_found") {
				got = "nothing_found"
			}
			if strings.Contains(line, "data.entity.invalid") {
				got = "invalid"
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
	return got
}

// dataGetAmp is dataGetState plus the parsed numeric value.
func dataGetAmp(t *testing.T, sink *chatSink, pw *protocol.Writer, path string) (string, float64) {
	t.Helper()
	sendCommand(t, pw, "data get entity A "+path)
	sendCommand(t, pw, "say ZEND")
	deadline := time.Now().Add(6 * time.Second)
	st, val := "", -1.0
	for time.Now().Before(deadline) {
		end := false
		for _, line := range sink.take() {
			if i := strings.Index(line, "data.entity.query"); i >= 0 {
				st, val = "query", lastNumber(line[:i])
			}
			if strings.Contains(line, "nbtpath.nothing_found") {
				st = "nothing_found"
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
	return st, val
}
