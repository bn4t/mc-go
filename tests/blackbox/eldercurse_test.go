package blackbox

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// ElderGuardian.customServerAiStep: when (tickCount+entityId)%1200 hits
// zero — every 60 s — each survival-mode player inside 50 blocks gets
// Mining Fatigue III (amplifier 2, 6000 ticks) pushed as
// update_mob_effect, plus the GUARDIAN_ELDER_EFFECT game event (type
// 10, param 1.0 — 0.0 only when the elder is silent). The elder sits
// ~20 blocks out: inside the curse radius but past follow_range 16 so
// the attack beam never arms during the wait.
func TestElderCurseParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	type result struct {
		event  bool
		param  float32
		effect bool
		amp    int
		dur    int
	}
	fatigueID, _ := server.MobEffectID("mining_fatigue")

	run := func(addr string) result {
		var r result
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(120 * time.Second))
		obs := &fleeObs{}

		sendCommand(t, pw, "gamemode creative P")
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		sendCommand(t, pw, "tp P 0.5 -60 0.5")
		fleePump(t, c, pr, pw, obs, 600*time.Millisecond)
		sendCommand(t, pw, "summon minecraft:elder_guardian 20.5 -59 0.5")
		sendCommand(t, pw, "gamemode survival P")

		start := time.Now()
		for time.Since(start) < 72*time.Second {
			_ = c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
			p, err := pr.ReadPacket()
			if err != nil {
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
			case server.IDPlayGameEvent:
				rd := protocol.NewPacketReader(p.Data)
				typ, _ := rd.Byte()
				v, _ := rd.Float()
				if typ == 10 && !r.event {
					r.event = true
					r.param = v
				}
			case server.IDPlayUpdateMobEffect:
				rd := protocol.NewPacketReader(p.Data)
				rd.VarInt() // entity id
				eff, _ := rd.VarInt()
				amp, _ := rd.Byte()
				dur, _ := rd.VarInt()
				if eff == fatigueID && !r.effect {
					r.effect = true
					r.amp = int(amp)
					r.dur = int(dur)
				}
			}
			if r.event && r.effect {
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
		if !r.event {
			t.Errorf("%s: no GUARDIAN_ELDER_EFFECT game event within 72s", name)
		} else if r.param < 0.9 || r.param > 1.1 {
			t.Errorf("%s: curse event param %v, want 1.0", name, r.param)
		}
		if !r.effect {
			t.Errorf("%s: no mining_fatigue update_mob_effect within 72s", name)
		} else {
			if r.amp != 2 {
				t.Errorf("%s: mining_fatigue amplifier %d, want 2", name, r.amp)
			}
			if r.dur < 5900 || r.dur > 6000 {
				t.Errorf("%s: mining_fatigue duration %d, want 6000", name, r.dur)
			}
		}
	}
}
