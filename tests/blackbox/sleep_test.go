package blackbox

import (
	"bytes"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// pumpSleepClient keeps a connection protocol-live so clicks actually
// go through on vanilla: any join/teleport SyncPos must be confirmed or
// every use_item_on is rejected as "build too high".
func pumpSleepClient(c net.Conn, pr *protocol.Reader, pw *protocol.Writer, d time.Duration, collect func(*protocol.Packet)) {
	defer c.SetReadDeadline(time.Time{})
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

// sinkPkt is one parsed line off a chatSink: the packet id plus its
// raw body.
type sinkPkt struct {
	id   int32
	data []byte
}

// takePkts drains a chatSink for d and parses the stored lines back
// into packet id + raw data. The sink owns the connection's read loop
// and already answers keepalives, chunk batches, and (with
// newChatSinkTP) SyncPos teleports, so routed reads stay race-free.
func takePkts(sink *chatSink, d time.Duration) []sinkPkt {
	var out []sinkPkt
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		for _, line := range sink.take() {
			i := strings.IndexByte(line, ':')
			if i <= 0 {
				continue
			}
			id, err := strconv.ParseInt(line[:i], 0, 64)
			if err != nil {
				continue
			}
			out = append(out, sinkPkt{id: int32(id), data: []byte(line[i+1:])})
		}
		time.Sleep(20 * time.Millisecond)
	}
	return out
}

var sleepPosRe = regexp.MustCompile(`-?\d+(?:\.\d+)?`)

func near(a, b float64) bool { return a-b < 0.01 && b-a < 0.01 }

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
		sinkA := newChatSinkTP(prA, pwA)

		posOf := func(name string) (x, y, z float64) {
			l := dataGetLine(t, sinkA, pwA, "@e[type=minecraft:player,name="+name+",limit=1]", "Pos")
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
		_ = takePkts(sinkA, 700*time.Millisecond)

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
		aPkts := takePkts(sinkA, 2200*time.Millisecond)

		swings := 0
		var meta63 []byte
		var dataResps [][]byte
		for _, p := range aPkts {
			switch p.id {
			case server.IDPlayEntityAnimation:
				swings++
			case server.IDPlayEntityMetadata:
				meta63 = append(meta63, p.data...)
			case server.IDPlaySystemChatCB:
				if bytes.Contains(p.data, []byte("data.entity.query")) {
					dataResps = append(dataResps, p.data)
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
		// The head half's occupied flip lands as a block update at the
		// head pos — either a lone 0x08 or inside a 0x54 section
		// update, whichever the server's batching emits.
		headLong := int64(protocol.EncodePosition(int32(hx), int32(hy), int32(hz)))
		headLocal := (hx&15)<<8 | (fz+1&15)<<4 | (hy & 15)
		headHit := false
		for _, p := range aPkts {
			if p.id == server.IDPlayBlockUpdateCB || p.id == 0x08 {
				r := protocol.NewPacketReader(p.data)
				if v, err := r.Long(); err == nil && v == headLong {
					headHit = true
				}
			}
			if p.id == 0x54 && len(p.data) >= 9 {
				r := protocol.NewPacketReader(p.data[8:])
				if n, err := r.VarInt(); err == nil {
					for i := int32(0); i < n; i++ {
						if vl, err := r.VarLong(); err == nil && int(vl&0xfff) == headLocal {
							headHit = true
						}
					}
				}
			}
		}
		if !headHit {
			ids := map[int32]int{}
			for _, p := range aPkts {
				ids[p.id]++
			}
			t.Fatalf("%s: no block update at the bed's head pos for the occupied flip; ids %v headLong %x headLocal %x", s.label, ids, headLong, headLocal)
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
		// setPosToBed snaps the sleeper's entity position to the head
		// half's center +0.6875 — the server's Pos NBT shows it.
		if px, py, pz := posOf("SlB"); !near(px, float64(hx)+0.5) || !near(py, float64(hy)+0.6875) || !near(pz, float64(hz)+0.5) {
			t.Fatalf("%s: sleeper Pos (%.3f,%.3f,%.3f), want on-bed (%.3f,%.3f,%.3f)", s.label, px, py, pz, float64(hx)+0.5, float64(hy)+0.6875, float64(hz)+0.5)
		}

		// --- occupied leg: A clicks the same bed while B sleeps ---
		_ = takePkts(sinkA, 80*time.Millisecond)
		sendUseItemOn(t, pwA, int32(fx), int32(fy), int32(fz), 1, 2)
		occ := false
		for _, p := range takePkts(sinkA, 1200*time.Millisecond) {
			if p.id == server.IDPlaySystemChatCB && bytes.Contains(p.data, []byte("bed.occupied")) {
				occ = true
			}
		}
		if !occ {
			t.Fatalf("%s: no bed.occupied for a click on a slept-in bed", s.label)
		}

		// --- phase 2: A sleeps in bed 2 — now everyone is in bed and the
		// 100-tick skip runs to morning ---
		_ = takePkts(sinkA, 150*time.Millisecond)
		sendUseItemOn(t, pwA, int32(gx), int32(gy), int32(gz), 1, 3)
		aPkts2 := takePkts(sinkA, 7000*time.Millisecond)
		skipOverlay := -1
		var wakePose bool
		wakeSec := 0
		for _, p := range aPkts2 {
			if p.id == server.IDPlaySystemChatCB && bytes.Contains(p.data, []byte("sleep.skipping_night")) {
				skipOverlay = int(p.data[len(p.data)-1])
			}
			if p.id == server.IDPlayEntityMetadata &&
				bytes.Contains(p.data, []byte{0x06, 0x14, 0x00, 0x0e, 0x0b, 0x00}) {
				wakePose = true
			}
			if p.id == 0x54 || p.id == 0x08 {
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
		// findStandUpPosition walks the offsets from the sleeper's
		// facing side: yaw 0 keeps the west side, so the first free
		// cell is one block west of the head — on flat ground both
		// players stand at (head.x-1).5 beside their bed.
		if px, py, pz := posOf("SlB"); !near(px, float64(hx)-0.5) || !near(py, float64(hy)) || !near(pz, float64(hz)+0.5) {
			t.Fatalf("%s: B wake Pos (%.3f,%.3f,%.3f), want stand-up (%.3f,%.3f,%.3f)", s.label, px, py, pz, float64(hx)-0.5, float64(hy), float64(hz)+0.5)
		}
		if px, py, pz := posOf("SlA"); !near(px, float64(gx)-0.5) || !near(py, float64(gy)) || !near(pz, float64(gz)+1.5) {
			t.Fatalf("%s: A wake Pos (%.3f,%.3f,%.3f), want stand-up (%.3f,%.3f,%.3f)", s.label, px, py, pz, float64(gx)-0.5, float64(gy), float64(gz)+1.5)
		}

		// --- day leg: A clicks outside the sleep window ---
		sendCommand(t, pwA, "time set 1000")
		_ = takePkts(sinkA, 400*time.Millisecond)
		sendUseItemOn(t, pwA, int32(gx), int32(gy), int32(gz), 1, 4)
		aPkts3 := takePkts(sinkA, 1200*time.Millisecond)
		noSleep := false
		for _, p := range aPkts3 {
			if p.id == server.IDPlaySystemChatCB && bytes.Contains(p.data, []byte("bed.no_sleep")) {
				noSleep = true
			}
		}
		if !noSleep {
			t.Fatalf("%s: no bed.no_sleep on a daytime click", s.label)
		}
	}
}
