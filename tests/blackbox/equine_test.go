package blackbox

// TestEquineMetaParity pins the AbstractHorse metadata chain on the
// wire: DATA_ID_FLAGS rides index 20 as a byte on every equine
// (0x02 TAME — the saddle lives on equipment.saddle instead), Horse
// adds DATA_TYPE_VARIANT (21, int = type | markings<<8), the llama
// family adds DATA_STRENGTH_ID (21) + DATA_VARIANT_ID (22), Camel
// adds DATA_DASH (21) — all measured on 26.2's decompiled
// defineSynchedData chain. AgeableMob's age_locked (17) rides the
// inherited slot too. Attach packs and dirty packs compare verbatim
// with the leading VarInt entity id stripped.

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestEquineMetaParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	tag := fmt.Sprintf("eq%d", time.Now().UnixNano()%1_000_000)
	sel := func(ty string) string {
		return fmt.Sprintf("@e[type=minecraft:%s,tag=%s,limit=1]", ty, tag)
	}
	cmds := []string{
		// Tamed horse with a packed TypeAndMarking — attach carries
		// idx20 TAME bit + idx21 variant int.
		"summon minecraft:horse 9.5 -59 10.5 {NoAI:1b,Tags:[\"" + tag + "\"],Tame:1b,Variant:259}",
		// Llama family: strength + variant ints at 21/22.
		"summon minecraft:llama 11.5 -59 10.5 {NoAI:1b,Tags:[\"" + tag + "\"],Strength:3,Variant:2}",
		// Camel tamed via NBT — the flags byte alone (Tame opens its
		// inventory screen on sneak-interact too).
		"summon minecraft:camel 13.5 -59 10.5 {NoAI:1b,Tags:[\"" + tag + "\"],Tame:1b}",
		// Age-locked baby donkey — baby at 16 + age_locked at 17.
		"summon minecraft:donkey 15.5 -59 10.5 {NoAI:1b,Tags:[\"" + tag + "\"],Age:-24000,AgeLocked:1b}",
		// A data merge that only moves llama strength emits just the
		// idx21 entry.
		"data merge entity " + sel("llama") + " {Strength:5}",
		// Health merge on the tamed horse: dirty pack carries health
		// alone, not the flags replay.
		"data merge entity " + sel("horse") + " {Health:5f}",
	}

	run := func(addr string) [][]string {
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSink(pr, pw)
		sink.cap = 1 << 20
		time.Sleep(400 * time.Millisecond)
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		time.Sleep(1400 * time.Millisecond)
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		time.Sleep(800 * time.Millisecond)
		sink.take()
		var out [][]string
		for _, cmd := range cmds {
			sink.take()
			sendCommand(t, pw, cmd)
			time.Sleep(450 * time.Millisecond)
			var evs []string
			for _, line := range sink.take() {
				if strings.HasPrefix(line, "0x63:") || strings.HasPrefix(line, "0x83:") {
					body := stripVarInt(line[5:])
					// Kill leftovers drop items/orbs whose index-8
					// metadata is rolled per kill.
					if strings.HasPrefix(body, "\x08\x01") || strings.HasPrefix(body, "\x08\x07") {
						continue
					}
					evs = append(evs, "pkt "+line[:4]+" "+maskUUID(fmt.Sprintf("%q", body)))
					continue
				}
				if tree, ok := componentTree(line, true); ok {
					evs = append(evs, "chat "+maskUUID(tree))
				}
			}
			out = append(out, evs)
		}
		return out
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	for i, cmd := range cmds {
		if fmt.Sprintf("%v", want[i]) != fmt.Sprintf("%v", got[i]) {
			t.Errorf("cmd %q:\n  vanilla %v\n  mc-go   %v", cmd, want[i], got[i])
		}
	}
}

// camelEID polls for the camel's add_entity on conn B, answering
// keepalives/chunk acks/teleport confirms on both sockets.
func camelEID(t *testing.T, v *vehPair, window time.Duration) int32 {
	t.Helper()
	found := int32(0)
	poll := func(c net.Conn, pr *protocol.Reader, pw *protocol.Writer, observe bool) {
		defer c.SetReadDeadline(time.Time{})
		_ = c.SetReadDeadline(time.Now().Add(30 * time.Millisecond))
		for {
			p, err := pr.ReadPacket()
			if err != nil {
				return
			}
			switch int32(p.ID) {
			case server.IDPlayKeepAliveCB:
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
			case server.IDPlayChunkBatchDone:
				_ = ackChunkBatch(pw)
			case server.IDPlaySyncPos:
				if id, err := protocol.NewPacketReader(p.Data).VarInt(); err == nil {
					var b protocol.Buffer
					b.VarInt(id)
					_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
				}
			default:
				if !observe {
					continue
				}
				if int32(p.ID) == 1 {
					r := protocol.NewPacketReader(p.Data)
					eid, _ := r.VarInt()
					r.UUID()
					ty, _ := r.VarInt()
					if n, _ := server.EntityTypeName(ty); n == "camel" {
						found = eid
					}
				}
			}
		}
	}
	end := time.Now().Add(window)
	for time.Now().Before(end) && found == 0 {
		poll(v.ca, v.pra, v.pwa, false)
		poll(v.cb, v.prb, v.pwb, true)
	}
	if found == 0 {
		t.Fatalf("no camel spawn seen within %s", window)
	}
	return found
}

// sendPlayerCommand writes the player_command packet (0x2A):
// VarInt entity id (unused server-side — the ridden vehicle is the
// actor), VarInt action, VarInt data.
func sendPlayerCommand(pw *protocol.Writer, action, data int32) {
	var b protocol.Buffer
	b.VarInt(0)
	b.VarInt(action)
	b.VarInt(data)
	_ = pw.WritePacket(server.IDPlayPlayerCommandSB, b.Bytes())
}

// TestCamelDashParity pins the server-side slice of Camel's dash:
// START_RIDING_JUMP (player_command action 3, data>0) on a saddled
// camel runs handleStartJump — a CAMEL_DASH positional sound (wire
// id 262), an entity_action game event, and DATA_DASH (idx19) true.
// The data write rearms dashCooldown=55 (onSyncedDataUpdated), which
// decays to 0 firing CAMEL_DASH_READY (wire 263); the flag clears
// only once the cooldown is below 50 AND the camel's server-side
// onGround is true — a ridden camel's onGround mirrors the driver's
// move_vehicle packets, so a mounted camel that never receives one
// keeps DASH on (measured: a NoAI camel never runs move(), so its
// onGround is false the whole ride).
func TestCamelDashParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA"),
	}, nil)
	defer targets.cleanup()

	run := func(tag, addr string) {
		v := joinVehPair(t, addr)
		drainChunkWindow(t, tag, v.ca, v.pra, v.pwa, 400*time.Millisecond)
		drainChunkWindow(t, tag, v.cb, v.prb, v.pwb, 400*time.Millisecond)
		sendCommand(t, v.pwa, "tp @s 6.5 -60 8.5 0 0")
		var mv protocol.Buffer
		mv.Double(6.5)
		mv.Double(-60)
		mv.Double(8.5)
		mv.Byte(0x01)
		_ = v.pwa.WritePacket(server.IDPlayMovePosSB, mv.Bytes())
		time.Sleep(300 * time.Millisecond)
		sendCommand(t, v.pwa, `summon minecraft:camel 8.5 -60 8.5 {NoAI:1b,equipment:{saddle:{id:"minecraft:saddle",count:1}}}`)

		eid := camelEID(t, v, 3*time.Second)
		sendInteractEntity(v.pwa, eid)
		if o := vehWatch(t, v, true, eid, 6*time.Second); o.passengers != 1 {
			t.Fatalf("%s: interact mounted %d passengers, want 1", tag, o.passengers)
		}

		// START_RIDING_JUMP data>0: the camel arms the dash —
		// CAMEL_DASH sound plus idx19 true. The flag stays on for
		// the whole ride: the mounted NoAI camel's onGround is
		// frozen false server-side, and the cooldown's <50 gate
		// never gets a ground to pair with.
		sendPlayerCommand(v.pwa, 3, 90)
		o := vehWatch(t, v, true, eid, 6*time.Second)
		sawOn, sawOff := false, false
		for _, d := range o.dashHist {
			parts := strings.Split(d, ":")
			if len(parts) == 2 && parts[0] == fmt.Sprintf("%d", eid) {
				if parts[1] == "1" {
					sawOn = true
				}
				if parts[1] == "0" {
					sawOff = true
				}
			}
		}
		if !sawOn {
			t.Fatalf("%s: dash flag history %v, want eid:1", tag, o.dashHist)
		}
		if sawOff {
			t.Fatalf("%s: dash flag cleared mid-ride %v, want persistent (vanilla keeps it on)", tag, o.dashHist)
		}
		// CAMEL_DASH (262) fires at the arm; the rearmed 55-tick
		// cooldown decays inside the window and fires
		// CAMEL_DASH_READY (263) at 0.
		sawDash, sawReady := false, false
		for _, id := range o.soundIds {
			if id == 262 {
				sawDash = true
			}
			if id == 263 {
				sawReady = true
			}
		}
		if !sawDash || !sawReady {
			t.Fatalf("%s: sounds %v, want 262 (dash) + 263 (dash_ready)", tag, o.soundIds)
		}

		// The driver's move_vehicle reports onGround=true:
		// setOnGroundWithMovement grounds the server-side camel,
		// and the next tick clears DASH — idx19 false.
		sendMoveVehicle(v.pwa, 8.5, -60, 8.5)
		o = vehWatch(t, v, true, eid, 2*time.Second)
		sawOff = false
		for _, d := range o.dashHist {
			parts := strings.Split(d, ":")
			if len(parts) == 2 && parts[0] == fmt.Sprintf("%d", eid) && parts[1] == "0" {
				sawOff = true
			}
		}
		if !sawOff {
			t.Fatalf("%s: move_vehicle ground did not clear DASH, hist %v", tag, o.dashHist)
		}

		// data=0: the handler's else-arm reaches the empty
		// handleStopJump — no flag, no sound.
		sendPlayerCommand(v.pwa, 3, 0)
		if o := vehWatch(t, v, true, eid, 1200*time.Millisecond); len(o.dashHist) != 0 {
			t.Fatalf("%s: data=0 emitted dash flags %v, want none", tag, o.dashHist)
		}
	}

	run("vanilla", targets.vanilla)
	run("mc-go", targets.mcgo)
}
