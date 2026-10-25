package blackbox

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// LivingEntity.hurt marks Entity.hurtMarked on every landed hit
// outside the damage_type:no_impact tag (drown only); the next
// ServerEntity.sendChanges then emits set_entity_motion
// unconditionally — the drift gate is bypassed, so even a
// zero-knockback hurt that leaves the delta untouched still flushes
// one motion packet, and the emit is self-inclusive
// (sendToTrackingPlayersAndSelf) so a player victim's own conn sees
// it too. Pinned with `damage ... minecraft:generic`: generic sits in
// no_knockback so the delta never moves, making the hurtMarked arm
// the only thing that can produce a motion packet on a rested
// entity.
func TestHurtMarkedMotionParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	type result struct {
		pigMotion  int
		selfMotion int
		zap        bool
	}
	pigID, _ := server.EntityTypeID("pig")

	run := func(addr string) result {
		var r result
		c, pr, pw, selfEID := joinPlayEid(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(60 * time.Second))
		obs := &fleeObs{}

		sendCommand(t, pw, "gamemode creative P")
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		sendCommand(t, pw, "tp P 0.5 -60 0.5")
		fleePump(t, c, pr, pw, obs, 600*time.Millisecond)
		sendCommand(t, pw, "summon minecraft:pig 12.5 -60 0.5 {NoAI:1b}")

		var pigEID int32 = -1
		pigDead := false
		hurtSent := false
		selfHurtSent := false
		var hurtAt time.Time
		lastSummon := time.Now()
		deadline := time.Now().Add(25 * time.Second)
		for time.Now().Before(deadline) {
			_ = c.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
			p, err := pr.ReadPacket()
			if err != nil {
				if pigEID != -1 && !hurtSent && time.Since(lastSummon) > 2*time.Second {
					// Baseline is the settle window: a rested NoAI
					// pig emits nothing until the hurt lands.
					hurtSent = true
					hurtAt = time.Now()
					sendCommand(t, pw, "damage @e[type=minecraft:pig,limit=1] 1 minecraft:generic")
				}
				if r.zap && hurtSent && time.Since(hurtAt) > 1500*time.Millisecond && !selfHurtSent {
					selfHurtSent = true
					sendCommand(t, pw, "gamemode survival P")
					sendCommand(t, pw, "damage @s 1 minecraft:generic")
				}
				if selfHurtSent && time.Since(hurtAt) > 4*time.Second {
					break
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
					pigEID = id
					pigDead = false
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
				if eid == pigEID && pigEID != -1 && hurtSent {
					r.pigMotion++
				}
				if eid == selfEID && selfHurtSent {
					r.selfMotion++
				}
			case 0x17: // entity death / remove tracking
				_ = pigDead
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
		if r.pigMotion < 1 {
			t.Errorf("%s: no set_entity_motion on a zero-knockback hurt — hurtMarked arm missing", name)
		}
		if r.selfMotion < 1 {
			t.Errorf("%s: player victim never received its own hurtMarked motion emit", name)
		}
	}
}
