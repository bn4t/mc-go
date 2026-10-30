//go:build blackbox

package blackbox

import (
	"bytes"
	"fmt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPlayerObserverHeadParity(t *testing.T)       { playerObserverHeadParity(t, false) }
func TestPlayerObserverFrozenHeadParity(t *testing.T) { playerObserverHeadParity(t, true) }

// Body packet selection remains a separate measured gap. This pin asserts
// actual initial pairing and fresh head updates, including unchanged-head silence.
func playerObserverHeadParity(t *testing.T, frozen bool) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor("ObserverDriver", "ObserverPeer")}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
		s.SetOpLevel("ObserverDriver", 4)
		s.SetOpLevel("ObserverPeer", 4)
	})
	defer tg.cleanup()
	for _, target := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(target.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackboxView(t, target.addr, "ObserverDriver", 1)
			defer c.Close()
			var eid int32
			for eid == 0 {
				p, e := pr.ReadPacket()
				if e != nil {
					t.Fatal(e)
				}
				if p.ID == server.IDPlayJoinGame {
					eid, e = protocol.NewPacketReader(p.Data).Int()
					if e != nil {
						t.Fatal(e)
					}
				}
			}
			awaitPlayReadyBlackbox(t, target.name, c, pr, pw)
			driver := newChatSinkTP(pr, pw)
			driver.mu.Lock()
			driver.cap = 1 << 20
			driver.mu.Unlock()
			serial := 0
			command := func(cmd string) {
				serial++
				marker := fmt.Sprintf("OBSERVER_CONTROL_%d", serial)
				sendCommand(t, pw, cmd)
				sendCommand(t, pw, "say "+marker)
				waitForExactSayBB(t, driver, marker, 10*time.Second)
			}
			setupPlayerTickBarrierBB(t, pw)
			command("tp ObserverDriver 0.5 -60 0.5 0 0")
			oc, opr, opw := joinPlayBlackboxView(t, target.addr, "ObserverPeer", 1)
			defer oc.Close()
			drainToJoinBlackbox(t, target.name, opr)
			var initialSpawns []string
			ready := false
			_ = oc.SetDeadline(time.Now().Add(60 * time.Second))
			for !ready {
				packet, e := opr.ReadPacket()
				if e != nil {
					t.Fatal(e)
				}
				switch packet.ID {
				case server.LatestVersion().IDs.PlaySpawnEntity:
					initialSpawns = append(initialSpawns, fmt.Sprintf("%#x:%s", packet.ID, string(packet.Data)))
				case server.IDPlayKeepAliveCB:
					if e := opw.WritePacket(server.IDPlayKeepAliveSB, packet.Data); e != nil {
						t.Fatal(e)
					}
				case server.IDPlayChunkBatchDone:
					if e := ackChunkBatch(opw); e != nil {
						t.Fatal(e)
					}
				case server.IDPlaySyncPos:
					id, e := protocol.NewPacketReader(packet.Data).VarInt()
					if e != nil {
						t.Fatal(e)
					}
					var ack protocol.Buffer
					ack.VarInt(id)
					if e := opw.WritePacket(server.IDPlayTeleportConfirmSB, ack.Bytes()); e != nil {
						t.Fatal(e)
					}
				case server.IDPlayUpdateAttributes:
					if e := opw.WritePacket(server.IDPlayPlayerLoadedSB, nil); e != nil {
						t.Fatal(e)
					}
					ready = true
				}
			}
			_ = oc.SetDeadline(time.Time{})
			observer := newChatSinkTP(opr, opw)
			observer.mu.Lock()
			observer.cap = 1 << 20
			observer.chat = append(initialSpawns, observer.chat...)
			observer.mu.Unlock()
			ticks := func(n int64) {
				before := gameTimeOf(t, pw, driver)
				deadline := time.Now().Add(10 * time.Second)
				for gameTimeOf(t, pw, driver) < before+n {
					if time.Now().After(deadline) {
						t.Fatal("tick receipt missing")
					}
				}
			}
			command("tp ObserverPeer 2.5 -60 0.5 180 0")
			ids := server.LatestVersion().IDs
			decode := func(line string) (int32, *protocol.PacketReader, bool) {
				colon := strings.IndexByte(line, ':')
				if colon < 0 {
					return 0, nil, false
				}
				id, e := strconv.ParseInt(line[:colon], 0, 32)
				if e != nil {
					return 0, nil, false
				}
				r := protocol.NewPacketReader([]byte(line[colon+1:]))
				got, e := r.VarInt()
				return int32(id), r, e == nil && got == eid
			}
			spawned := false
			deadline := time.Now().Add(10 * time.Second)
			for !spawned && time.Now().Before(deadline) {
				for _, line := range observer.take() {
					id, r, ok := decode(line)
					if !ok || id != ids.PlaySpawnEntity {
						continue
					}
					if _, e := r.UUID(); e != nil {
						t.Fatal(e)
					}
					kind, e := r.VarInt()
					if e != nil || kind != ids.EntityTypePlayer {
						t.Fatalf("pairing type%d error%v", kind, e)
					}
					x, _ := r.Double()
					y, _ := r.Double()
					z, _ := r.Double()
					if x != .5 || y != -60 || z != .5 {
						continue
					}
					if _, _, _, e := r.ReadLpVec3(); e != nil {
						t.Fatal(e)
					}
					pitch, e := r.Byte()
					if e != nil {
						t.Fatal(e)
					}
					yaw, e := r.Byte()
					if e != nil {
						t.Fatal(e)
					}
					head, e := r.Byte()
					if e != nil {
						t.Fatal(e)
					}
					if pitch != 0 || yaw != 0 || head != 0 {
						t.Fatalf("spawn baseline%d/%d/%d", yaw, pitch, head)
					}
					spawned = true
				}
				if !spawned {
					time.Sleep(10 * time.Millisecond)
				}
			}
			if !spawned {
				t.Fatalf("fresh player pairing spawn missing eid%d initial%d %s", eid, len(initialSpawns), observer.health())
			}
			ticks(4)
			observer.take()
			if frozen {
				command("tick freeze")
			}
			frozenAge := gameTimeOf(t, pw, driver)
			var move protocol.Buffer
			move.Float(-.1)
			move.Float(-.1)
			move.Byte(1)
			if e := pw.WritePacket(server.IDPlayMoveRotSB, move.Bytes()); e != nil {
				t.Fatal(e)
			}
			receipt := func(transaction int32) {
				var query protocol.Buffer
				query.VarInt(transaction)
				query.VarInt(eid)
				if e := pw.WritePacket(server.IDPlayEntityTagQuerySB, query.Bytes()); e != nil {
					t.Fatal(e)
				}
				receipt := false
				deadline := time.Now().Add(10 * time.Second)
				for !receipt && time.Now().Before(deadline) {
					for _, line := range driver.take() {
						prefix := fmt.Sprintf("%#x:", server.IDPlayTagQueryCB)
						if !strings.HasPrefix(line, prefix) {
							continue
						}
						tx, e := protocol.ReadVarInt(bytes.NewReader([]byte(strings.TrimPrefix(line, prefix))))
						if e == nil && tx == transaction {
							receipt = true
						}
					}
					if !receipt {
						time.Sleep(10 * time.Millisecond)
					}
				}
				if !receipt {
					t.Fatal("movement owner receipt missing")
				}
			}
			receipt(1001)
			sendCommand(t, pw, "say OBSERVER_FINAL")
			waitForExactSayBB(t, driver, "OBSERVER_FINAL", 10*time.Second)
			lines := waitForExactSayBB(t, observer, "OBSERVER_FINAL", 10*time.Second)
			hasHead := func() bool {
				for _, line := range lines {
					id, r, ok := decode(line)
					if ok && id == ids.PlayRotateHead {
						value, e := r.Byte()
						if e == nil && value == 255 {
							return true
						}
					}
				}
				return false
			}
			deadline = time.Now().Add(10 * time.Second)
			for !hasHead() && time.Now().Before(deadline) {
				lines = append(lines, observer.take()...)
				time.Sleep(10 * time.Millisecond)
			}
			if after := gameTimeOf(t, pw, driver); frozen && after != frozenAge {
				t.Fatalf("world advanced while frozen: %d to%d", frozenAge, after)
			}
			body := false
			head := false
			var yaw, pitch float32
			var bodyGround bool
			var events []string
			for _, line := range lines {
				id, r, ok := decode(line)
				if !ok {
					continue
				}
				switch id {
				case ids.PlayMoveEntityRot:
					y, _ := r.Byte()
					p, _ := r.Byte()
					yaw, pitch = float32(int8(y))*360/256, float32(int8(p))*360/256
					ground, e := r.Bool()
					if e != nil {
						t.Fatal(e)
					}
					bodyGround = ground
					body = true
					events = append(events, fmt.Sprintf("rot%d/%d", y, p))
				case ids.PlayMoveEntityPosRot:
					r.Short()
					r.Short()
					r.Short()
					y, _ := r.Byte()
					p, _ := r.Byte()
					yaw, pitch = float32(int8(y))*360/256, float32(int8(p))*360/256
					ground, e := r.Bool()
					if e != nil {
						t.Fatal(e)
					}
					bodyGround = ground
					body = true
					events = append(events, fmt.Sprintf("posrot%d/%d", y, p))
				case ids.PlayEntityTeleportCB:
					for range 6 {
						r.Double()
					}
					yaw, _ = r.Float()
					pitch, _ = r.Float()
					ground, e := r.Bool()
					if e != nil {
						t.Fatal(e)
					}
					bodyGround = ground
					body = true
					events = append(events, fmt.Sprintf("sync%g/%g", yaw, pitch))
				case ids.PlayRotateHead:
					h, e := r.Byte()
					if e != nil || r.Remaining() != 0 {
						t.Fatalf("malformed head packet: %v remaining%d", e, r.Remaining())
					}
					events = append(events, fmt.Sprintf("head%d", h))
					if h == 255 {
						head = true
					}
				}
			}
			t.Logf("observer body%v yaw%g pitch%g ground%v head255%v events%q", body, yaw, pitch, bodyGround, head, events)
			if !body || !head {
				t.Fatalf("fresh body receipt or head255 missing")
			}
			headCount := 0
			for _, line := range lines {
				id, _, ok := decode(line)
				if ok && id == ids.PlayRotateHead {
					headCount++
				}
			}
			if headCount != 1 {
				t.Fatalf("changed head emitted%d packets, want1", headCount)
			}
			var pitchOnly protocol.Buffer
			pitchOnly.Float(-.1)
			pitchOnly.Float(.2)
			pitchOnly.Byte(1)
			if e := pw.WritePacket(server.IDPlayMoveRotSB, pitchOnly.Bytes()); e != nil {
				t.Fatal(e)
			}
			receipt(1002)
			waitPlayerTicksBB(t, pw, func(marker string) []string { return waitForExactSayBB(t, driver, marker, 10*time.Second) }, "HEADSILENT", 4)
			sendCommand(t, pw, "data get entity ObserverDriver Rotation")
			sendCommand(t, pw, "say OBSERVER_PITCH_APPLIED")
			applied := false
			for _, line := range waitForExactSayBB(t, driver, "OBSERVER_PITCH_APPLIED", 10*time.Second) {
				key, args, ok := componentArgs(line)
				if ok && key == "commands.data.entity.query" && len(args) == 2 && playerRotationPairBB(playerRotationTextBB(args[1])) == ([2]float32{-.1, .2}) {
					applied = true
				}
			}
			if !applied {
				t.Fatal("pitch-only control did not apply to the authoritative player pose")
			}
			sendCommand(t, pw, "say OBSERVER_UNCHANGED")
			waitForExactSayBB(t, driver, "OBSERVER_UNCHANGED", 10*time.Second)
			for _, line := range waitForExactSayBB(t, observer, "OBSERVER_UNCHANGED", 10*time.Second) {
				id, _, ok := decode(line)
				if ok && id == ids.PlayRotateHead {
					t.Fatal("unchanged yaw emitted a new head packet")
				}
			}
			if after := gameTimeOf(t, pw, driver); frozen && after != frozenAge {
				t.Fatalf("world advanced during frozen head control: %d to%d", frozenAge, after)
			}
		})
	}
}
