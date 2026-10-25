package server

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"mc-go/internal/protocol"
)

func TestJavaSpectatorSelectionMatchesOfficialWire(t *testing.T) {
	raw, err := os.ReadFile("../../conformance/testdata/spectator-actions.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Profiles []struct {
			Protocol int32 `json:"protocol"`
			Target   int32 `json:"target_entity_id"`
			Player   int32 `json:"player_entity_id"`
			Cases    []struct {
				Label string `json:"label"`
				Sent  struct {
					ID   int32  `json:"id"`
					Body string `json:"body"`
				} `json:"sent_packet"`
				Packets []struct {
					ID  int32  `json:"id"`
					Hex string `json:"hex"`
				} `json:"position_camera_packets"`
			} `json:"cases"`
		} `json:"profiles"`
	}
	if err = json.Unmarshal(raw, &corpus); err != nil {
		t.Fatal(err)
	}
	if len(corpus.Profiles) != 2 {
		t.Fatal("missing official profile evidence")
	}
	for _, oracle := range corpus.Profiles {
		t.Run(fmt.Sprint(oracle.Protocol), func(t *testing.T) {
			s := New("unused")
			t.Cleanup(s.Stop)
			sess := menuProjectionSession(t, s, oracle.Protocol, "Observer")
			sess.setPos(0.5, -60, 0.5, 0, 0, true)
			player := s.entities.Spawn(&Entity{Type: mustEntityType("player"), UUID: sess.getUUID(), Dim: dimOverworld, X: 0.5, Y: -60, Z: 0.5})
			sess.entityID.Store(player.ID)
			pig := s.entities.Spawn(&Entity{Type: mustEntityType("pig"), Dim: dimOverworld, X: 5.5, Y: -60, Z: 1.5, NoAI: true})
			if pig.ID != oracle.Target || player.ID != oracle.Player {
				t.Fatal("fixture IDs drifted")
			}
			sess.teleportNext.Store(2) // initial login teleport1 has already completed
			sess.queued = map[[2]int]bool{}
			sess.sent = map[[2]int]bool{}
			for x := -3; x <= 3; x++ {
				for z := -3; z <= 3; z++ {
					sess.queued[[2]int{x, z}] = true
					sess.sent[[2]int{x, z}] = true
				}
			}
			sess.opened, sess.centerSet = true, true
			sess.centerX, sess.centerZ = 0, 0
			for _, tc := range oracle.Cases {
				if tc.Label == "nonspectator_target" {
					sess.setMode(GameModeCreative)
				} else {
					sess.setMode(GameModeSpectator)
				}
				body, err := hex.DecodeString(tc.Sent.Body)
				if err != nil {
					t.Fatal(err)
				}
				s.handleSpectatorAction(sess, protocol.Packet{ID: tc.Sent.ID, Data: body})
				if len(sess.sendCh) != len(tc.Packets) {
					t.Fatalf("%s response packets=%d,want%d", tc.Label, len(sess.sendCh), len(tc.Packets))
				}
				for _, expected := range tc.Packets {
					got := materializeOutboundPacket(t, sess, <-sess.sendCh)
					want, err := hex.DecodeString(expected.Hex)
					if err != nil {
						t.Fatal(err)
					}
					if got.id != expected.ID || !bytes.Equal(got.data, want) {
						t.Fatalf("%s wire differs: id%d body%x; want%d %x", tc.Label, got.id, got.data, expected.ID, want)
					}
				}
			}
			x, y, z, _, _, _ := sess.Pos()
			if x != pig.X || y != pig.Y || z != pig.Z {
				t.Fatalf("camera selection left canonical position %v,%v,%v", x, y, z)
			}
			// A complete valid body is required; trailing input cannot activate a
			// camera, and selecting an entity in another dimension is ignored.
			sess.setMode(GameModeSpectator)
			trailing := byte(2)
			if oracle.Protocol == 776 {
				trailing = 3
			}
			for _, bad := range [][]byte{{}, {0x80}, {0xff, 0xff, 0xff, 0xff, 0x0f}, {trailing, 0}} {
				s.handleSpectatorAction(sess, protocol.Packet{ID: IDPlaySpectatorActionSB, Data: bad})
				if len(sess.sendCh) != 0 {
					t.Fatal("malformed selection emitted packets")
				}
			}
			pig.Dim = dimNether
			var b protocol.Buffer
			target := pig.ID
			if oracle.Protocol == 776 {
				target++
			}
			b.VarInt(target)
			s.handleSpectatorAction(sess, protocol.Packet{ID: IDPlaySpectatorActionSB, Data: b.Bytes()})
			if len(sess.sendCh) != 0 {
				t.Fatal("cross-dimension selection emitted packets")
			}
		})
	}
}
