package blackbox

import (
	"bytes"
	"fmt"
	"net"
	"regexp"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// pumpSleepClient keeps a connection protocol-live so clicks actually
// go through on vanilla: any join/teleport SyncPos must be confirmed or
// every use_item_on is rejected as "build too high".
func pumpSleepClient(c net.Conn, pr *protocol.Reader, pw *protocol.Writer, d time.Duration, collect func(*protocol.Packet)) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(time.Now().Add(80 * time.Millisecond))
		for {
			p, err := pr.ReadPacket()
			if err != nil {
				break
			}
			switch p.ID {
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
			}
			if collect != nil {
				collect(&p)
			}
		}
	}
}

// pumpReadWindow reads a connection for d, acking keepalives, chunk
// batches, and SyncPos teleports, collecting every packet.
func pumpReadWindow(c net.Conn, pr *protocol.Reader, pw *protocol.Writer, d time.Duration) []protocol.Packet {
	var out []protocol.Packet
	pumpSleepClient(c, pr, pw, d, func(p *protocol.Packet) { out = append(out, *p) })
	return out
}

var sleepPosRe = regexp.MustCompile(`-?\d+(?:\.\d+)?`)

// TestSleepBedParity pins the whole vanilla wire surface of sleeping in
// a bed, measured on the real 26.2 server:
//
//   - the arm-swing EntityAnimation is broadcast twice to trackers and
//     the sleeper;
//   - set_entity_data carries idx 6 pose=SLEEPING(2) and idx 14
//     sleeping_pos as the OPTIONAL_BLOCK_POS of the bed's HEAD half;
//   - both halves flip occupied=true, emitted via section_blocks_update;
//   - "block.minecraft.set_spawn" is system chat overlay=false while
//     "sleep.skipping_night" is an action bar (overlay=true);
//   - sleeping_pos lands in the player's NBT ([I;x,y,z]) with a live
//     SleepTimer while asleep;
//   - an occupied click shows "bed.occupied", a daytime click shows
//     "bed.no_sleep" — both action bars;
//   - the skip needs every player in the dimension asleep; on the
//     100-tick morning the pose and sleeping_pos clear and occupied
//     flips back.
func TestSleepBedParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("SlA")}, nil)
	defer targets.cleanup()

	for _, s := range []struct{ label, addr string }{
		{"vanilla", targets.vanilla}, {"mcgo", targets.mcgo},
	} {
		cA, prA, pwA := joinPlayBlackbox(t, s.addr, "SlA")
		defer cA.Close()
		sendClientInformation(t, pwA)
		cB, prB, pwB := joinPlayBlackbox(t, s.addr, "SlB")
		defer cB.Close()
		sendClientInformation(t, pwB)
		pumpSleepClient(cB, prB, pwB, 900*time.Millisecond, nil)
		drainChunkWindow(t, s.label, cA, prA, pwA, 400*time.Millisecond)
		sink := newChatSink(prA, pwA)

		posOf := func(name string) (x, y, z float64) {
			l := dataGetLine(t, sink, pwA, "@e[type=minecraft:player,name="+name+",limit=1]", "Pos")
			nums := sleepPosRe.FindAllString(l, -1)
			if len(nums) < 3 {
				t.Fatalf("%s: no %s Pos in %q", s.label, name, l)
			}
			fmt.Sscanf(nums[len(nums)-3], "%f", &x)
			fmt.Sscanf(nums[len(nums)-2], "%f", &y)
			fmt.Sscanf(nums[len(nums)-1], "%f", &z)
			return
		}

		// Bed 1 beside B, bed 2 beside A — both facing south (head +1z).
		bx, by, bz := posOf("SlB")
		fx, fy, fz := int(bx)+1, int(by), int(bz)
		hx, hy, hz := fx, fy, fz+1
		sendCommand(t, pwA, fmt.Sprintf("setblock %d %d %d minecraft:red_bed[part=foot,facing=south]", fx, fy, fz))
		sendCommand(t, pwA, fmt.Sprintf("setblock %d %d %d minecraft:red_bed[part=head,facing=south]", fx, fy, hz))
		// Bed 2 beside bed 1 — A clicks it from the spot next to B's bed.
		gx, gy, gz := fx-2, fy, fz
		sendCommand(t, pwA, fmt.Sprintf("setblock %d %d %d minecraft:red_bed[part=foot,facing=south]", gx, gy, gz))
		sendCommand(t, pwA, fmt.Sprintf("setblock %d %d %d minecraft:red_bed[part=head,facing=south]", gx, gy, gz+1))
		sendCommand(t, pwA, "time set 13000")
		// Put A in interaction range of B's bed for the occupied click.
		sendCommand(t, pwA, fmt.Sprintf("tp SlA %d.5 %d %d.5", fx-3, fy, fz))
		pumpSleepClient(cA, prA, pwA, 700*time.Millisecond, nil)

		// --- phase 1: B sleeps alone; A stays awake so the skip never
		// runs and B stays in bed for the whole window ---
		var bPkts []protocol.Packet
		sendUseItemOn(t, pwB, int32(fx), int32(fy), int32(fz), 1, 1)
		pumpSleepClient(cB, prB, pwB, 700*time.Millisecond, func(p *protocol.Packet) {
			bPkts = append(bPkts, *p)
		})
		// NBT queries while B sleeps (no 100-tick deadline — A is awake).
		sendCommand(t, pwA, "data get entity @e[type=minecraft:player,name=SlB,limit=1] sleeping_pos")
		sendCommand(t, pwA, "data get entity @e[type=minecraft:player,name=SlB,limit=1] SleepTimer")
		aPkts := pumpReadWindow(cA, prA, pwA, 2200*time.Millisecond)

		swings := 0
		var meta63, sec54 []byte
		var dataResps [][]byte
		for _, p := range aPkts {
			switch p.ID {
			case server.IDPlayEntityAnimation:
				swings++
			case server.IDPlayEntityMetadata:
				meta63 = append(meta63, p.Data...)
			case 0x54:
				sec54 = append(sec54, p.Data...)
			case server.IDPlaySystemChatCB:
				if bytes.Contains(p.Data, []byte("data.entity.query")) {
					dataResps = append(dataResps, p.Data)
				}
			}
		}
		if swings != 2 {
			t.Fatalf("%s: swing EntityAnimation count %d, want 2", s.label, swings)
		}
		if !bytes.Contains(meta63, []byte{0x06, 0x14, 0x02}) {
			t.Fatalf("%s: no pose=SLEEPING in meta %x", s.label, meta63)
		}
		headPacked := protocol.EncodePosition(int32(hx), int32(hy), int32(hz))
		var hp [8]byte
		for i := 0; i < 8; i++ {
			hp[i] = byte(headPacked >> (56 - 8*i))
		}
		if !bytes.Contains(meta63, append([]byte{0x0e, 0x0b, 0x01}, hp[:]...)) {
			t.Fatalf("%s: no sleeping_pos head meta %x want %x", s.label, meta63, hp)
		}
		if len(sec54) == 0 {
			t.Fatalf("%s: no section_blocks_update for bed occupied", s.label)
		}
		if len(dataResps) < 2 {
			t.Fatalf("%s: sleeping NBT queries answered %d, want 2", s.label, len(dataResps))
		}
		if !bytes.Contains(dataResps[0], []byte("-60")) {
			t.Fatalf("%s: sleeping_pos response lacks the bed's y coord: %x", s.label, dataResps[0])
		}
		if !bytes.Contains(dataResps[1], []byte{0x08, 0x00, 0x04, 't', 'e', 'x', 't', 0x00, 0x01, 's'}) {
			t.Fatalf("%s: SleepTimer response lacks the 's' short suffix: %x", s.label, dataResps[1])
		}
		// B's own stream: its swing plus "Respawn point set" system chat
		// (overlay=false — only the sleeper receives it).
		bSwings := 0
		setSpawnOverlay := -1
		for _, p := range bPkts {
			if p.ID == server.IDPlayEntityAnimation {
				bSwings++
			}
			if p.ID == server.IDPlaySystemChatCB && bytes.Contains(p.Data, []byte("block.minecraft.set_spawn")) {
				setSpawnOverlay = int(p.Data[len(p.Data)-1])
			}
		}
		if bSwings != 2 {
			t.Fatalf("%s: sleeper's own swing count %d, want 2", s.label, bSwings)
		}
		if setSpawnOverlay != 0 {
			t.Fatalf("%s: set_spawn overlay %d, want 0 (system chat)", s.label, setSpawnOverlay)
		}

		// --- occupied leg: A clicks the same bed while B sleeps ---
		_ = cA.SetReadDeadline(time.Now().Add(60 * time.Millisecond))
		for {
			if _, err := prA.ReadPacket(); err != nil {
				break
			}
		}
		sendUseItemOn(t, pwA, int32(fx), int32(fy), int32(fz), 1, 2)
		occ := false
		for _, p := range pumpReadWindow(cA, prA, pwA, 1200*time.Millisecond) {
			if p.ID == server.IDPlaySystemChatCB && bytes.Contains(p.Data, []byte("bed.occupied")) {
				occ = true
			}
		}
		if !occ {
			t.Fatalf("%s: no bed.occupied for a click on a slept-in bed", s.label)
		}

		// --- phase 2: A sleeps in bed 2 — now everyone is in bed and the
		// 100-tick skip runs to morning ---
		pumpSleepClient(cA, prA, pwA, 150*time.Millisecond, nil)
		sendUseItemOn(t, pwA, int32(gx), int32(gy), int32(gz), 1, 3)
		aPkts2 := pumpReadWindow(cA, prA, pwA, 7000*time.Millisecond)
		skipOverlay := -1
		var wakePose bool
		wakeSec := 0
		for _, p := range aPkts2 {
			if p.ID == server.IDPlaySystemChatCB && bytes.Contains(p.Data, []byte("sleep.skipping_night")) {
				skipOverlay = int(p.Data[len(p.Data)-1])
			}
			if p.ID == server.IDPlayEntityMetadata &&
				bytes.Contains(p.Data, []byte{0x06, 0x14, 0x00, 0x0e, 0x0b, 0x00}) {
				wakePose = true
			}
			if p.ID == 0x54 {
				wakeSec++
			}
		}
		if skipOverlay != 1 {
			t.Fatalf("%s: sleep.skipping_night overlay %d, want 1 (action bar)", s.label, skipOverlay)
		}
		if !wakePose {
			t.Fatalf("%s: no standing-pose meta on morning wake", s.label)
		}
		if wakeSec == 0 {
			t.Fatalf("%s: no occupied=false section update on wake", s.label)
		}

		// --- day leg: A clicks outside the sleep window ---
		sendCommand(t, pwA, "time set 1000")
		pumpSleepClient(cA, prA, pwA, 400*time.Millisecond, nil)
		sendUseItemOn(t, pwA, int32(gx), int32(gy), int32(gz), 1, 4)
		aPkts3 := pumpReadWindow(cA, prA, pwA, 1200*time.Millisecond)
		noSleep := false
		for _, p := range aPkts3 {
			if p.ID == server.IDPlaySystemChatCB && bytes.Contains(p.Data, []byte("bed.no_sleep")) {
				noSleep = true
			}
		}
		if !noSleep {
			t.Fatalf("%s: no bed.no_sleep on a daytime click", s.label)
		}
	}
}
