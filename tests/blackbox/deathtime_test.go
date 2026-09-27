package blackbox

import (
	"net"
	"testing"
	"time"

	"mc-go/internal/protocol"
)

// TestDeathTimeParity: a hurt-killed mob lies as a corpse for 20
// ticks — LivingEntity.tickDeath does deathTime++ and at 20
// broadcasts entity event 60 + remove(KILLED). On the wire that's
// entity_event(3) now, then entity_event(60)+remove_entities about
// a second later. `/kill` removes outright (no corpse) — only
// hurt() death enters the window; selectors don't match corpses on
// either server, so the pin rides raw packets.
func TestDeathTimeParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("DT")}, nil)
	defer targets.cleanup()

	type dthEv struct {
		kind string // "spawn" | "event" | "remove"
		eid  int32
		b0   int32
		at   time.Time
	}
	collect := func(c net.Conn, pr *protocol.Reader, d time.Duration) []dthEv {
		out := []dthEv{}
		deadline := time.Now().Add(d)
		for time.Now().Before(deadline) {
			_ = c.SetReadDeadline(time.Now().Add(time.Until(deadline)))
			p, err := pr.ReadPacket()
			if err != nil {
				break
			}
			switch p.ID {
			case 0x01: // spawn_entity — first VarInt is the entity id
				r := protocol.NewPacketReader(p.Data)
				if eid, err := r.VarInt(); err == nil {
					out = append(out, dthEv{"spawn", eid, 0, time.Now()})
				}
			case 0x22: // entity_event
				r := protocol.NewPacketReader(p.Data)
				eid, e1 := r.Int()
				st, e2 := r.Byte()
				if e1 == nil && e2 == nil {
					out = append(out, dthEv{"event", eid, int32(st), time.Now()})
				}
			case 0x4D: // remove_entities
				r := protocol.NewPacketReader(p.Data)
				n, err := r.VarInt()
				if err != nil {
					break
				}
				for i := int32(0); i < n; i++ {
					if id, e := r.VarInt(); e == nil {
						out = append(out, dthEv{"remove", id, 0, time.Now()})
					}
				}
			}
		}
		return out
	}

	for _, s := range []struct{ label, addr string }{
		{"vanilla", targets.vanilla}, {"mcgo", targets.mcgo},
	} {
		t.Run(s.label, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, s.addr, "DT")
			defer c.Close()
			sendClientInformation(t, pw)
			time.Sleep(400 * time.Millisecond)
			drainChunkWindow(t, s.label, c, pr, pw, 300*time.Millisecond)
			sendCommand(t, pw, "gamerule do_mob_spawning false")
			sendCommand(t, pw, "kill @e[type=!minecraft:player]")
			time.Sleep(300 * time.Millisecond)
			collect(c, pr, 500*time.Millisecond)
			sendCommand(t, pw, `summon minecraft:pig 10.5 -60 10.5 {NoAI:1b}`)
			time.Sleep(400 * time.Millisecond)
			ev0 := collect(c, pr, 400*time.Millisecond)
			var pig int32
			for _, e := range ev0 {
				if e.kind == "spawn" {
					pig = e.eid
				}
			}
			if pig == 0 {
				t.Fatalf("%s: pig spawn packet never seen", s.label)
			}
			sendCommand(t, pw,
				"damage @e[type=minecraft:pig,limit=1] 999 minecraft:generic")
			evs := collect(c, pr, 3000*time.Millisecond)
			var ev3At, rmAt time.Time
			ev3, ev60, rm := false, false, false
			for _, e := range evs {
				if e.eid != pig {
					continue
				}
				if e.kind == "event" && e.b0 == 3 {
					ev3, ev3At = true, e.at
				}
				if e.kind == "event" && e.b0 == 60 {
					ev60 = true
				}
				if e.kind == "remove" {
					rm, rmAt = true, e.at
				}
			}
			if !ev3 {
				t.Fatalf("%s: no entity_event 3 for the dying pig", s.label)
			}
			if !rm {
				t.Fatalf("%s: corpse never removed (no remove_entities)", s.label)
			}
			if !ev60 {
				t.Fatalf("%s: no entity_event 60 death poof", s.label)
			}
			gap := rmAt.Sub(ev3At)
			if gap < 600*time.Millisecond || gap > 2500*time.Millisecond {
				t.Fatalf("%s: corpse removed %v after death event — want ~20 ticks (1s)", s.label, gap)
			}
		})
	}
}
