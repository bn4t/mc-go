package blackbox

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// GuardianAttackSelector also admits Squid and Axolotl — measured:
// a squid inside follow_range and past 3 blocks gets the beam; the
// idx-17 target sync points at the squid's entity id, the zap lands
// as a damage_event on the squid, and the id clears to 0. The player
// stays creative so it can never win the selector.
func TestGuardianSquidBeamParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	type result struct {
		event21     bool
		beamToSquid bool // idx17 synced to the squid eid
		beamClear   bool
		squidZaps   int
	}
	gid, _ := server.EntityTypeID("guardian")
	squidID, _ := server.EntityTypeID("squid")

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
		sendCommand(t, pw, "summon minecraft:guardian 7.5 -59 0.5")
		sendCommand(t, pw, "summon minecraft:squid 12.5 -59 0.5")

		var guardEID, squEID int32 = -1, -1
		summonAt := time.Now()
		// Same vanilla tracking flake as the player-target beam —
		// retry the summons near world spawn until the client's
		// tracker reports both entities.
		for attempt := 0; ; {
			_ = c.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
			p, err := pr.ReadPacket()
			if err != nil {
				if time.Since(summonAt) > 15*time.Second {
					if r.beamToSquid && r.squidZaps > 0 && r.beamClear {
						break
					}
					attempt++
					if attempt >= 3 {
						break
					}
					sendCommand(t, pw, "kill @e[type=!minecraft:player]")
					fleePump(t, c, pr, pw, obs, 1500*time.Millisecond)
					sendCommand(t, pw, "summon minecraft:guardian 7.5 -59 0.5")
					sendCommand(t, pw, "summon minecraft:squid 12.5 -59 0.5")
					guardEID, squEID = -1, -1
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
				}
				if typ == squidID {
					squEID = id
				}
			case server.IDPlayEntityEvent:
				rd := protocol.NewPacketReader(p.Data)
				eid, _ := rd.Int()
				status, _ := rd.Byte()
				if status == 21 && eid == guardEID {
					r.event21 = true
				}
			case server.IDPlayEntityMetadata:
				rd := protocol.NewPacketReader(p.Data)
				eid, _ := rd.VarInt()
				if guardEID == -1 || eid != guardEID {
					break
				}
				for i := 0; i+3 < len(p.Data); i++ {
					if p.Data[i] == 0x11 && p.Data[i+1] == 0x01 {
						if int32(p.Data[i+2]) == squEID && squEID != -1 {
							r.beamToSquid = true
						} else if p.Data[i+2] == 0 && r.beamToSquid {
							r.beamClear = true
						}
					}
				}
			case 0x19: // damage_event
				rd := protocol.NewPacketReader(p.Data)
				eid, _ := rd.VarInt()
				if eid == squEID && squEID != -1 {
					r.squidZaps++
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
			t.Errorf("%s: guardian never broadcast entity event 21", name)
		}
		if !r.beamToSquid {
			t.Errorf("%s: beam target metadata never pointed at the squid", name)
		}
		if r.squidZaps == 0 {
			t.Errorf("%s: squid never took a beam zap", name)
		}
		if !r.beamClear {
			t.Errorf("%s: attack-target metadata never cleared", name)
		}
	}
}
