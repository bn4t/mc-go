package blackbox

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestDamageTypeHurtSoundParity pins Player.getHurtSound's dispatch
// through the damage_type's `effects` field: burning types all play
// entity.player.hurt_on_fire regardless of name, and thorns shares
// the plain hurt sound. The victim never receives the packet — its
// client synthesizes the sound off the damage event — so a second
// session stands by as the observer and collects `sound` packets.
func TestDamageTypeHurtSoundParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()
	t.Cleanup(func() { vanillaLogTail(t) })

	// type name -> expected entity.player.hurt_* registry id.
	cases := []struct {
		typ string
		snd int32
	}{
		{"generic", 1312},
		// hurt() runs playSecondaryHurtSound after the hurt voice:
		// thorns damage additionally broadcasts enchant.thorns.hit,
		// which lands last in the window.
		{"thorns", 1622},
		{"mob_attack", 1312},
		{"drown", 1313},
		{"freeze", 1314},
		{"in_fire", 1315},
		{"on_fire", 1315},
		{"lava", 1315},
		{"campfire", 1315},
		{"hot_floor", 1315},
		{"fireball", 1315},
		{"unattributed_fireball", 1315},
		{"sulfur_cube_hot", 1315},
		{"sweet_berry_bush", 1316},
	}

	// hurtSoundSet is every id a hurt can legitimately emit —
	// player hurt/death variants plus the thorns secondary sound.
	hurtSoundSet := map[int32]bool{
		1311: true, 1312: true, 1313: true, 1314: true,
		1315: true, 1316: true, 1622: true,
	}

	run := func(addr string) map[string]int32 {
		cv, prv, pwv := joinPlayBlackbox(t, addr, "P")
		defer cv.Close()
		cq, prq, pwq := joinPlayBlackbox(t, addr, "Q")
		defer cq.Close()

		var heard []int32
		// drainP keeps the victim's session alive through keepalives;
		// its stream carries nothing the test reads (the hurt sound
		// skips the victim) — and it shares no writer with sendCommand
		// because drainP only runs between command sends.
		drainP := func(d time.Duration) {
			end := time.Now().Add(d)
			for time.Now().Before(end) {
				_ = cv.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
				p, err := prv.ReadPacket()
				if err != nil {
					return
				}
				if p.ID == server.IDPlayKeepAliveCB {
					_ = pwv.WritePacket(server.IDPlayKeepAliveSB, p.Data)
				}
				if p.ID == server.IDPlaySyncPos {
					confirmMovePos(p.Data, pwv)
				}
			}
		}
		pump := func(d time.Duration) {
			end := time.Now().Add(d)
			for time.Now().Before(end) {
				_ = cq.SetReadDeadline(time.Now().Add(30 * time.Millisecond))
				p, err := prq.ReadPacket()
				if err == nil {
					switch p.ID {
					case server.IDPlayKeepAliveCB:
						_ = pwq.WritePacket(server.IDPlayKeepAliveSB, p.Data)
					case server.IDPlaySyncPos:
						confirmMovePos(p.Data, pwq)
					case 0x75: // sound — holder id is one-based registry id
						rd := protocol.NewPacketReader(p.Data)
						id, err := rd.VarInt()
						// Only the hurt-family ids count — ambient world
						// sounds (footsteps, weather) share the stream.
						if err == nil && hurtSoundSet[id-1] {
							heard = append(heard, id-1)
						}
					}
				}
				drainP(10 * time.Millisecond)
			}
		}

		// The hurt sound reaches listeners within 16 blocks — spawn
		// scatter can exceed that, so pin both players together.
		sendCommand(t, pwv, "gamemode survival P")
		sendCommand(t, pwv, "tp P 0 -60 0 0 0")
		sendCommand(t, pwv, "tp Q 0 -60 0 0 0")
		pump(1500 * time.Millisecond)

		got := map[string]int32{}
		for _, c := range cases {
			base := len(heard)
			sendCommand(t, pwv, "damage P 1 minecraft:"+c.typ)
			// hurtServer's half-second invulnerability window rejects
			// same-size follow-ups, so the cadence is wider than that.
			pump(700 * time.Millisecond)
			for _, id := range heard[base:] {
				got[c.typ] = id
			}
		}
		return got
	}

	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	t.Logf("vanilla %v", van)
	t.Logf("mc-go   %v", mc)
	for _, c := range cases {
		if van[c.typ] != c.snd {
			t.Errorf("vanilla: /damage %s sound = %d, want %d", c.typ, van[c.typ], c.snd)
		}
		if mc[c.typ] != c.snd {
			t.Errorf("mc-go: /damage %s sound = %d, want %d", c.typ, mc[c.typ], c.snd)
		}
	}
}

// confirmMovePos answers a SyncPos packet the way a real client does —
// TeleportConfirmSB with the echoed confirm id, then a MovePosSB echo
// so the server actually adopts the teleported position (vanilla keeps
// the entity at its old spot for broadcast-range checks without it).
func confirmMovePos(data []byte, pw *protocol.Writer) {
	r := protocol.NewPacketReader(data)
	id, err := r.VarInt()
	if err != nil {
		return
	}
	var b protocol.Buffer
	b.VarInt(id)
	_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
	x, _ := r.Double()
	y, _ := r.Double()
	z, _ := r.Double()
	var mb protocol.Buffer
	mb.Double(x)
	mb.Double(y)
	mb.Double(z)
	mb.Byte(0)
	_ = pw.WritePacket(server.IDPlayMovePosSB, mb.Bytes())
}
