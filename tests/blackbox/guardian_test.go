package blackbox

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// Guardian.GuardianAttackGoal: after acquiring a player at more than
// three blocks, attackTime walks -10 up to getAttackDuration (80 ticks,
// elder 60) — the beam-start broadcast (entity event 21 plus the
// DATA_ID_ATTACK_TARGET sync at metadata index 17, int serializer)
// lands at attackTime==0, the zap at >=duration is a same-tick pair of
// indirect_magic (1.0 base) and doHurtTarget melee (attack_damage 6)
// that i-frames fold to a single damage event, then the target is
// released and the synced id clears to 0.
func TestGuardianBeamParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	type result struct {
		event21   bool
		beamSet   bool // idx17 synced to the player eid
		beamClear bool // idx17 back to 0 after the zap
		zapHealth float32
		zapGap    time.Duration // event-21 → zap
		zaps      int           // damage_event packets seen
		spawns    int           // guardian spawn packets seen
	}
	gid, _ := server.EntityTypeID("guardian")

	run := func(addr string) result {
		var r result
		r.zapHealth = -1
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(180 * time.Second))
		obs := &fleeObs{}

		sendCommand(t, pw, "gamemode creative P")
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		sendCommand(t, pw, "tp P 0.5 -60 0.5")
		fleePump(t, c, pr, pw, obs, 600*time.Millisecond)
		// Summon while still creative — vanilla reliably adds the entity
		// to the client's tracker in this order; a survival-mode summon
		// loses the spawn bundle most runs (the zap still lands, but the
		// beam wire is unobservable).
		sendCommand(t, pw, "summon minecraft:guardian 7.5 -59 0.5")
		fleePump(t, c, pr, pw, obs, 1500*time.Millisecond)
		sendCommand(t, pw, "gamemode survival P")

		var guardEID int32 = -1
		var beamAt time.Time
		// Vanilla's entity tracking flakes — a guardian that isn't
		// tracked by this client still zaps it (the damage event is
		// self-referenced) but its spawn/metadata never arrive. Each
		// attempt re-summons and watches for the full beam wire.
		summonAt := time.Now()
		for attempt := 0; ; {
			_ = c.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
			p, err := pr.ReadPacket()
			if err != nil {
				if time.Since(summonAt) > 15*time.Second {
					if r.zapHealth >= 0 && r.beamSet && r.beamClear {
						break
					}
					attempt++
					if attempt >= 3 {
						break
					}
					sendCommand(t, pw, "kill @e[type=minecraft:guardian]")
					sendCommand(t, pw, "gamemode creative P")
					sendCommand(t, pw, "tp P 0.5 -60 0.5")
					fleePump(t, c, pr, pw, obs, 1500*time.Millisecond)
					sendCommand(t, pw, "summon minecraft:guardian 7.5 -59 0.5")
					sendCommand(t, pw, "gamemode survival P")
					guardEID = -1
					summonAt = time.Now()
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
				if typ == gid {
					guardEID = id
					r.spawns++
				}
			case server.IDPlayEntityEvent:
				rd := protocol.NewPacketReader(p.Data)
				eid, _ := rd.Int()
				status, _ := rd.Byte()
				// The event precedes the synced id by a tick.
				if status == 21 && (eid == guardEID || guardEID == -1) {
					r.event21 = true
					beamAt = time.Now()
					if guardEID == -1 {
						guardEID = eid
					}
				}
			case server.IDPlayEntityMetadata:
				// Raw scan: index 17, int serializer (type 1) is the
				// guardian's DATA_ID_ATTACK_TARGET — a value of the
				// player eid arms the beam, 0 releases it.
				data := p.Data
				rd := protocol.NewPacketReader(data)
				eid, _ := rd.VarInt()
				if guardEID != -1 && eid != guardEID {
					break
				}
				for i := 0; i+3 < len(data); i++ {
					if data[i] == 0x11 && data[i+1] == 0x01 {
						v := data[i+2]
						if v != 0 {
							r.beamSet = true
							guardEID = eid
						} else if r.beamSet {
							r.beamClear = true
						}
					}
				}
			case 0x19: // damage_event
				r.zaps++
			case server.IDPlaySetHealth:
				if r.zapHealth < 0 && r.beamSet {
					rd := protocol.NewPacketReader(p.Data)
					if h, err := rd.Float(); err == nil && h < 20 {
						r.zapHealth = h
						if !beamAt.IsZero() {
							r.zapGap = time.Since(beamAt)
						}
					}
				}
			}
		}
		return r
	}

	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	t.Logf("van %+v", van)
	t.Logf("mc  %+v", mc)
	for name, r := range map[string]result{"vanilla": van, "mc-go": mc} {
		if !r.event21 {
			t.Errorf("%s: guardian never broadcast entity event 21 (beam start)", name)
		}
		if !r.beamSet {
			t.Errorf("%s: DATA_ID_ATTACK_TARGET (idx 17) never synced to the player", name)
		}
		// Zap: 1.0 indirect_magic + the 6.0 melee swing's 5.0 delta
		// through i-frames = 6.0 total → 14.0 health.
		if r.zapHealth < 13.5 || r.zapHealth > 14.5 {
			t.Errorf("%s: beam zap health %.1f, want ~14.0", name, r.zapHealth)
		}
		if r.zapGap < 3500*time.Millisecond || r.zapGap > 5500*time.Millisecond {
			t.Errorf("%s: beam start→zap gap %v, want ~4s (80 ticks)", name, r.zapGap)
		}
		if !r.beamClear {
			t.Errorf("%s: attack-target metadata never cleared after the zap", name)
		}
	}
}
