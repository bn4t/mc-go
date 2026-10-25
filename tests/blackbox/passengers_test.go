package blackbox

// TestSetPassengersParity mounts and dismounts a boat while a second
// client watches, and diffs the set_passengers packets the watcher
// sees on each server. Vanilla broadcasts set_passengers whenever a
// vehicle's passenger list changes (Entity.startRiding/stopRiding via
// ServerEntity.sync); dismount is the client pressing shift — the
// player_input shift flag drives Player.wantsToStopRiding -> stopRiding
// in vanilla's tick, there is no dismount packet in 26.2.

import (
	"fmt"
	"net"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

type passengerEvent struct {
	vehicle int32
	riders  []int32
}

// collectPassengers records set_passengers packets (0x6B) until the
// deadline, answering keep-alives.
func collectPassengers(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, d time.Duration) []passengerEvent {
	t.Helper()
	var out []passengerEvent
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		_ = c.SetReadDeadline(end)
		p, err := pr.ReadPacket()
		if err != nil {
			break
		}
		switch p.ID {
		case server.IDPlayKeepAliveCB:
			var kb protocol.Buffer
			kb.Raw(p.Data)
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, kb.Bytes())
		case 0x6B: // set_passengers
			r := protocol.NewPacketReader(p.Data)
			veh, e1 := r.VarInt()
			n, e2 := r.VarInt()
			if e1 != nil || e2 != nil {
				continue
			}
			ev := passengerEvent{vehicle: veh}
			for i := int32(0); i < n; i++ {
				id, e3 := r.VarInt()
				if e3 != nil {
					break
				}
				ev.riders = append(ev.riders, id)
			}
			out = append(out, ev)
		}
	}
	return out
}

func TestSetPassengersParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Rider", "Watcher"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	run := func(addr string) []passengerEvent {
		// The watcher joins first so it is already spawned when the
		// boat appears.
		wc, wpr, wpw := joinPlayBlackbox(t, addr, "Watcher")
		defer wc.Close()
		_ = wc.SetDeadline(time.Time{})
		_ = wpw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sendCommand(t, wpw, "tp @s 8.5 -60 11.5 180 0")
		advCollect(t, wc, wpr, wpw, 900*time.Millisecond, &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}})

		rc, rpr, rpw := joinPlayBlackbox(t, addr, "Rider")
		defer rc.Close()
		_ = rc.SetDeadline(time.Time{})
		_ = rpw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sendCommand(t, rpw, "summon minecraft:oak_boat 8.5 -60 8.5")
		sendCommand(t, rpw, "tp @s 8.5 -60 9.8 180 0")

		var boatID int32
		end := time.Now().Add(10 * time.Second)
		for time.Now().Before(end) && boatID == 0 {
			_ = rc.SetReadDeadline(time.Now().Add(time.Until(end)))
			p, err := rpr.ReadPacket()
			if err != nil {
				break
			}
			switch p.ID {
			case server.IDPlayKeepAliveCB:
				var kb protocol.Buffer
				kb.Raw(p.Data)
				_ = rpw.WritePacket(server.IDPlayKeepAliveSB, kb.Bytes())
			case server.IDPlaySpawnEntity:
				r := protocol.NewPacketReader(p.Data)
				id, e1 := r.VarInt()
				_, e2 := r.Read(16)
				typ, e3 := r.VarInt()
				if e1 == nil && e2 == nil && e3 == nil && typ == 89 {
					boatID = id
				}
			case server.IDPlaySyncPos:
				if id, err := protocol.NewPacketReader(p.Data).VarInt(); err == nil {
					var tb protocol.Buffer
					tb.VarInt(id)
					_ = rpw.WritePacket(server.IDPlayTeleportConfirmSB, tb.Bytes())
				}
			}
		}
		if boatID == 0 {
			t.Fatalf("no boat spawn seen")
		}
		advCollect(t, wc, wpr, wpw, 400*time.Millisecond, &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}})

		// Mount: rider Interact on the boat, sneak flag clear.
		var ib protocol.Buffer
		ib.VarInt(boatID)
		ib.VarInt(0) // main hand
		ib.Byte(0)   // LpVec3 zero
		ib.Bool(false)
		_ = rpw.WritePacket(server.IDPlayInteractSB, ib.Bytes())
		time.Sleep(400 * time.Millisecond)
		mountEv := collectPassengers(t, wc, wpr, wpw, 1200*time.Millisecond)

		// Dismount: shift key down via player_input (bit 0x20 is the
		// shift bit for the 26.2 input flags byte).
		var pb protocol.Buffer
		pb.Byte(0x20) // shift bit in the 26.2 input flags byte
		_ = rpw.WritePacket(server.IDPlayPlayerInputSB, pb.Bytes())
		dismountEv := collectPassengers(t, wc, wpr, wpw, 1500*time.Millisecond)

		return append(mountEv, dismountEv...)
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	if len(want) == 0 {
		t.Fatalf("vanilla: no set_passengers observed")
	}
	// Entity ids differ per server (join order, spawned entities), so
	// compare shapes: first event must carry one rider, last must be
	// an empty list — count and arity of events is what parity covers.
	norm := func(evs []passengerEvent) []string {
		out := make([]string, 0, len(evs))
		for _, e := range evs {
			out = append(out, fmt.Sprintf("riders=%d", len(e.riders)))
		}
		return out
	}
	wn, gn := norm(want), norm(got)
	if len(wn) != len(gn) {
		t.Fatalf("set_passengers count: vanilla %v, mc-go %v", wn, gn)
	}
	for i := range wn {
		if wn[i] != gn[i] {
			t.Fatalf("set_passengers[%d]: vanilla %v, mc-go %v (all: %v vs %v)", i, wn[i], gn[i], wn, gn)
		}
	}
	if wn[0] != "riders=1" || wn[len(wn)-1] != "riders=0" {
		t.Fatalf("vanilla shape unexpected: %v", wn)
	}
}

// TestJoinSeesExistingEntities summons a wolf while only one player is
// online, then joins a second player and checks the newcomer receives
// the wolf's add_entity — vanilla's entity tracker pairs every tracked
// entity to a joining player, not just other players.
func TestJoinSeesExistingEntities(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("First", "Second"),
	}, nil)
	defer targets.cleanup()

	run := func(addr string) (bool, bool) {
		ac, apr, apw := joinPlayBlackbox(t, addr, "First")
		defer ac.Close()
		_ = ac.SetDeadline(time.Time{})
		sendCommand(t, apw, "summon minecraft:wolf 8.5 -60 8.5 {NoAI:1b}")
		sendCommand(t, apw, "tp @s 8.5 -60 4.5 180 0")
		advCollect(t, ac, apr, apw, 900*time.Millisecond, &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}})

		bc, bpr, bpw := joinPlayBlackbox(t, addr, "Second")
		defer bc.Close()
		_ = bc.SetDeadline(time.Time{})
		sendCommand(t, bpw, "tp @s 8.5 -60 4.5 180 0")

		seen, meta := false, false
		var wolfID int32
		end := time.Now().Add(12 * time.Second)
		for time.Now().Before(end) {
			_ = bc.SetReadDeadline(time.Now().Add(time.Until(end)))
			p, err := bpr.ReadPacket()
			if err != nil {
				break
			}
			switch p.ID {
			case server.IDPlayKeepAliveCB:
				var kb protocol.Buffer
				kb.Raw(p.Data)
				_ = bpw.WritePacket(server.IDPlayKeepAliveSB, kb.Bytes())
			case server.IDPlaySyncPos:
				if id, err := protocol.NewPacketReader(p.Data).VarInt(); err == nil {
					var tb protocol.Buffer
					tb.VarInt(id)
					_ = bpw.WritePacket(server.IDPlayTeleportConfirmSB, tb.Bytes())
				}
			case server.IDPlaySpawnEntity:
				r := protocol.NewPacketReader(p.Data)
				id, e1 := r.VarInt()
				if e1 != nil {
					continue
				}
				if _, e2 := r.Read(16); e2 != nil {
					continue
				}
				typ, e3 := r.VarInt()
				if e3 == nil && typ == 149 { // wolf
					seen = true
					wolfID = id
				}
			case server.IDPlayEntityMetadata:
				if id, err := protocol.NewPacketReader(p.Data).VarInt(); err == nil && id == wolfID && wolfID != 0 {
					meta = true
				}
			}
			if seen && meta {
				break
			}
		}
		return seen, meta
	}

	seen, meta := run(targets.vanilla)
	if !seen || !meta {
		t.Fatalf("vanilla: joining player wolf spawn=%v meta=%v", seen, meta)
	}
	seen, meta = run(targets.mcgo)
	if !seen || !meta {
		t.Fatalf("mc-go: joining player wolf spawn=%v meta=%v", seen, meta)
	}
}
