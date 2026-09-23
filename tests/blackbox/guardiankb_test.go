package blackbox

import (
	"math"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// LivingEntity.hurt applies dealDefaultKnockback to every landed
// entity-sourced hit outside the no_knockback tag: a 0.4 push away
// from the damage source's entity. Pinned through `/damage ... by`:
// a NoAI pig is stationary, so any set_entity_motion packet on its
// eid is the knockback — horizontal ~0.4 directed away from the
// player who dealt the hit.
func TestDamageKnockbackParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	type result struct {
		motion bool
		vx     float64
		vy     float64
		vz     float64
		zap    bool
	}
	pigID, _ := server.EntityTypeID("pig")

	run := func(addr string) result {
		var r result
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(60 * time.Second))
		obs := &fleeObs{}

		sendCommand(t, pw, "gamemode creative P")
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		sendCommand(t, pw, "tp P 0.5 -60 0.5")
		fleePump(t, c, pr, pw, obs, 600*time.Millisecond)
		// Pig floats mid-air at -60 under NoAI, 12 blocks east of the
		// player — knockback direction is +x. Summon inside the loop
		// window so the spawn bundle isn't drained by the pump.
		sendCommand(t, pw, "summon minecraft:pig 12.5 -60 0.5 {NoAI:1b}")

		var pigEID int32 = -1
		sent := false
		lastSummon := time.Now()
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			_ = c.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
			p, err := pr.ReadPacket()
			if err != nil {
				if pigEID != -1 {
					if !sent {
						sent = true
						sendCommand(t, pw, "damage @e[type=minecraft:pig,limit=1] 1 minecraft:mob_attack by @p")
					}
				} else if time.Since(lastSummon) > 2*time.Second {
					// tracking flake — kill + re-summon keeps the tag
					// unique until the pig is tracked
					sendCommand(t, pw, "kill @e[type=minecraft:pig]")
					sendCommand(t, pw, "summon minecraft:pig 12.5 -60 0.5 {NoAI:1b}")
					lastSummon = time.Now()
				}
				continue
			}
			switch p.ID {
			case server.IDPlayKeepAliveCB:
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
			case server.IDPlaySyncPos:
				rd := protocol.NewPacketReader(p.Data)
				if id, err := rd.VarInt(); err == nil {
					var b protocol.Buffer
					b.VarInt(id)
					_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
				}
			case server.IDPlaySpawnEntity:
				rd := protocol.NewPacketReader(p.Data)
				id, _ := rd.VarInt()
				rd.UUID()
				typ, _ := rd.VarInt()
				if typ == pigID {
					// re-summons replace the pig — track the latest eid
					pigEID = id
				}
			case 0x19: // damage_event
				rd := protocol.NewPacketReader(p.Data)
				eid, _ := rd.VarInt()
				if eid == pigEID && pigEID != -1 {
					r.zap = true
				}
			case 0x65: // set_entity_motion — VarInt id + LpVec3
				rd := protocol.NewPacketReader(p.Data)
				eid, _ := rd.VarInt()
				vx, vy, vz, _ := rd.ReadLpVec3()
				if eid == pigEID && pigEID != -1 && sent {
					r.motion = true
					r.vx = vx
					r.vy = vy
					r.vz = vz
				}
			}
			if r.motion && r.zap {
				break
			}
		}
		return r
	}

	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	t.Logf("van %+v", van)
	t.Logf("mc  %+v", mc)
	for name, r := range map[string]result{"vanilla": van, "mc-go": mc} {
		if !r.zap {
			t.Errorf("%s: pig never took the /damage hit", name)
		}
		if !r.motion {
			t.Errorf("%s: no set_entity_motion on the pig — dealDefaultKnockback missing", name)
			continue
		}
		// Push away from the player: +x ~0.4, |z| small. vy is the
		// grounded 0.4 pop on vanilla; mc's NoAI mob never grounds
		// (gravity is frozen) so only the horizontal push is pinned.
		if math.Abs(r.vx-0.4) > 0.12 || math.Abs(r.vz) > 0.1 {
			t.Errorf("%s: knockback vector (%.3f,%.3f,%.3f), want ~(0.4,?,0)", name, r.vx, r.vy, r.vz)
		}
	}
}
