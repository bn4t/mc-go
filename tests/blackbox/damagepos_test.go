package blackbox

import (
	"math"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// DamageCommand's `at` clause lands on DamageSource.sourcePosition:
// damage_event carries the explicit coords with hasPosition=true.
// The `by`/`from` entity forms send no position at all — the client's
// models look the direction up from the source entity itself.
func TestDamageAtPosParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	type result struct {
		hasPos      bool
		x, y, z     float64
		cause, drct int32
	}
	run := func(addr, cmd string) result {
		var r result
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(60 * time.Second))
		obs := &fleeObs{}

		sendCommand(t, pw, "gamemode creative P")
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		sendCommand(t, pw, "tp P 0.5 -60 0.5")
		fleePump(t, c, pr, pw, obs, 600*time.Millisecond)
		sendCommand(t, pw, "summon minecraft:pig 5.5 -60 0.5 {NoAI:1b}")
		fleePump(t, c, pr, pw, obs, 800*time.Millisecond)
		sendCommand(t, pw, cmd)

		deadline := time.Now().Add(6 * time.Second)
		for time.Now().Before(deadline) {
			_ = c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
			p, err := pr.ReadPacket()
			if err != nil {
				continue
			}
			switch p.ID {
			case server.IDPlayKeepAliveCB:
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
			case server.IDPlayDamageEvent:
				rd := protocol.NewPacketReader(p.Data)
				_, _ = rd.VarInt() // entity id
				_, _ = rd.VarInt() // damage type
				r.cause, _ = rd.VarInt()
				r.drct, _ = rd.VarInt()
				r.hasPos, _ = rd.Bool()
				if r.hasPos {
					r.x, _ = rd.Double()
					r.y, _ = rd.Double()
					r.z, _ = rd.Double()
				}
				return r
			}
		}
		t.Fatalf("no damage_event after %q", cmd)
		return r
	}

	at := run(targets.vanilla, "damage @e[type=minecraft:pig,limit=1] 1 minecraft:mob_attack at 30.5 -60 -4.5")
	if !at.hasPos {
		t.Fatal("vanilla at-form: expected hasPosition")
	}
	if math.Abs(at.x-30.5) > 1e-9 || math.Abs(at.y+60) > 1e-9 || math.Abs(at.z+4.5) > 1e-9 {
		t.Fatalf("vanilla at pos = (%v,%v,%v)", at.x, at.y, at.z)
	}
	if at.cause != 0 || at.drct != 0 {
		t.Fatalf("vanilla at-form carried entities: cause=%d direct=%d", at.cause, at.drct)
	}

	by := run(targets.vanilla, "damage @e[type=minecraft:pig,limit=1] 1 minecraft:mob_attack by @p")
	if by.hasPos {
		t.Fatal("vanilla by-form: hasPosition should be false")
	}
	if by.cause == 0 {
		t.Fatal("vanilla by-form: expected a cause entity")
	}

	// Differential: mc must match byte-for-byte modulo entity ids.
	for _, cmd := range []string{
		"damage @e[type=minecraft:pig,limit=1] 1 minecraft:mob_attack at 30.5 -60 -4.5",
		"damage @e[type=minecraft:pig,limit=1] 1 minecraft:mob_attack by @p",
	} {
		v, m := run(targets.vanilla, cmd), run(targets.mcgo, cmd)
		if v.hasPos != m.hasPos || v.x != m.x || v.y != m.y || v.z != m.z ||
			(v.cause != 0) != (m.cause != 0) || (v.drct != 0) != (m.drct != 0) {
			t.Fatalf("%q mismatch: vanilla=%+v mc=%+v", cmd, v, m)
		}
	}
}
