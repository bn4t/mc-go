//go:build blackbox

package blackbox

import (
	"bytes"
	"fmt"
	"math"
	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"strings"
	"testing"
	"time"
)

func TestMovePlayerNumericParity(t *testing.T) {
	cases := []struct {
		label      string
		pos        [3]float64
		yaw, pitch float32
		noOp       bool
		kick       bool
		kind       int
		ready      int
	}{
		{label: "finite"}, {label: "x_nan", kick: true, pos: [3]float64{math.NaN(), 0, 0}}, {label: "y_nan", kick: true, pos: [3]float64{0, math.NaN(), 0}}, {label: "z_nan", kick: true, pos: [3]float64{0, 0, math.NaN()}},
		{label: "yaw_nan", kick: true, yaw: float32(math.NaN())}, {label: "pitch_nan", kick: true, pitch: float32(math.NaN())},
		{label: "yaw_pos_inf", kick: true, yaw: float32(math.Inf(1))}, {label: "yaw_neg_inf", kick: true, yaw: float32(math.Inf(-1))}, {label: "pitch_pos_inf", kick: true, pitch: float32(math.Inf(1))}, {label: "pitch_neg_inf", kick: true, pitch: float32(math.Inf(-1))},
		{label: "x_pos_inf", pos: [3]float64{math.Inf(1), 0, 0}}, {label: "x_neg_inf", pos: [3]float64{math.Inf(-1), 0, 0}}, {label: "y_pos_inf", pos: [3]float64{0, math.Inf(1), 0}}, {label: "y_neg_inf", pos: [3]float64{0, math.Inf(-1), 0}}, {label: "z_pos_inf", pos: [3]float64{0, 0, math.Inf(1)}}, {label: "z_neg_inf", pos: [3]float64{0, 0, math.Inf(-1)}},
		{label: "huge_finite_coords", pos: [3]float64{math.MaxFloat64, math.MaxFloat64, -math.MaxFloat64}}, {label: "huge_finite_angles", yaw: math.MaxFloat32, pitch: -math.MaxFloat32},
		{label: "no_op_x_nan", kick: true, pos: [3]float64{math.NaN(), 0, 0}, noOp: true},
		{label: "pos_only_x_nan", kick: true, pos: [3]float64{math.NaN(), 0, 0}, kind: 1},
		{label: "rot_only_yaw_inf", kick: true, yaw: float32(math.Inf(1)), kind: 2},
		{label: "status_only", kind: 3},
		{label: "not_loaded_x_nan", kick: true, pos: [3]float64{math.NaN(), 0, 0}, ready: 1},
		{label: "awaiting_tp_pitch_nan", kick: true, pitch: float32(math.NaN()), ready: 2},
		{label: "unready_pending_z_nan", kick: true, pos: [3]float64{0, 0, math.NaN()}, ready: 3},
	}
	var ops []string
	for i, tc := range cases {
		if !tc.noOp {
			ops = append(ops, fmt.Sprintf("PlayerNum%d", i))
		}
	}
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor(ops...)}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
		s.OpsRequired = true
		for _, name := range ops {
			s.SetOpLevel(name, 4)
		}
	})
	defer tg.cleanup()
	for _, target := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(target.name, func(t *testing.T) {
			for i, tc := range cases {
				name := fmt.Sprintf("PlayerNum%d", i)
				c, pr, pw := joinPlayBlackboxView(t, target.addr, name, 1)
				defer c.Close()
				drainToJoinBlackbox(t, target.name, pr)
				// Observe both initial synchronization and attributes. For
				// authority-ordering controls, withhold exactly the requested
				// acknowledgment(s) before injecting the numeric packet.
				c.SetDeadline(time.Now().Add(60 * time.Second))
				sawPosition, sawAttributes := false, false
				for !sawPosition || !sawAttributes {
					p, e := pr.ReadPacket()
					if e != nil {
						t.Fatal(e)
					}
					switch p.ID {
					case server.IDPlaySyncPos:
						sawPosition = true
						id, e := protocol.NewPacketReader(p.Data).VarInt()
						if e != nil {
							t.Fatal(e)
						}
						if tc.ready&2 == 0 {
							var ack protocol.Buffer
							ack.VarInt(id)
							if e := pw.WritePacket(server.IDPlayTeleportConfirmSB, ack.Bytes()); e != nil {
								t.Fatal(e)
							}
						}
					case server.IDPlayUpdateAttributes:
						sawAttributes = true
						if tc.ready&1 == 0 {
							if e := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); e != nil {
								t.Fatal(e)
							}
						}
					case server.IDPlayKeepAliveCB:
						if e := pw.WritePacket(server.IDPlayKeepAliveSB, p.Data); e != nil {
							t.Fatal(e)
						}
					case server.IDPlayChunkBatchDone:
						if e := ackChunkBatch(pw); e != nil {
							t.Fatal(e)
						}
					}
				}
				c.SetDeadline(time.Time{})
				var sink *chatSink
				if tc.ready&2 != 0 {
					sink = newChatSink(pr, pw)
				} else {
					sink = newChatSinkTP(pr, pw)
				}
				var b protocol.Buffer
				id := int32(server.IDPlayMovePosRotSB)
				if tc.kind == 1 {
					id = server.IDPlayMovePosSB
				} else if tc.kind == 2 {
					id = server.IDPlayMoveRotSB
				} else if tc.kind == 3 {
					id = server.IDPlayMoveStatusSB
				}
				if tc.kind == 0 || tc.kind == 1 {
					for _, v := range tc.pos {
						b.Double(v)
					}
				}
				if tc.kind == 0 || tc.kind == 2 {
					b.Float(tc.yaw)
					b.Float(tc.pitch)
				}
				b.Byte(1)
				if err := pw.WritePacket(id, b.Bytes()); err != nil {
					t.Fatal(err)
				}
				marker := fmt.Sprintf("PLAYER_NUMERIC_%d", i)

				// Chat command handling can overtake a queued movement kick.
				// Invalid inputs therefore await the disconnect itself.
				if !tc.noOp && !tc.kick {
					sendCommand(t, pw, "say "+marker)
				}
				outcome, reason := "timeout", ""
				deadline := time.Now().Add(5 * time.Second)
				var lines []string
				for time.Now().Before(deadline) {
					lines = append(lines, sink.take()...)
					for _, line := range lines {
						prefix := fmt.Sprintf("%#x:", server.IDPlayDisconnect)
						if strings.HasPrefix(line, prefix) {
							root, e := nbt.ReadUnnamed(bytes.NewReader([]byte(strings.TrimPrefix(line, prefix))))
							if e != nil {
								t.Fatal(e)
							}
							outcome = "disconnect"
							reason = fmt.Sprintf("%#v", root)
						}
					}
					if outcome == "disconnect" {
						break
					}
					if structureSaidBB(lines, marker) {
						outcome = "alive"
						break
					}
					sink.mu.Lock()
					closed := sink.err != nil
					sink.mu.Unlock()
					if closed {
						lines = append(lines, sink.take()...)
						outcome = "closed"
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				// A close can race the first capture drain; inspect the final
				// queued frame before classifying a close without a reason.
				lines = append(lines, sink.take()...)
				for _, line := range lines {
					prefix := fmt.Sprintf("%#x:", server.IDPlayDisconnect)
					if strings.HasPrefix(line, prefix) {
						root, e := nbt.ReadUnnamed(bytes.NewReader([]byte(strings.TrimPrefix(line, prefix))))
						if e != nil {
							t.Fatal(e)
						}
						outcome = "disconnect"
						reason = fmt.Sprintf("%#v", root)
					}
				}
				if tc.kick {
					expected := fmt.Sprintf("%#v", map[string]any{"translate": "multiplayer.disconnect.invalid_player_movement"})
					if outcome != "disconnect" || reason != expected {
						t.Errorf("%s outcome%s reason%s wanttypeddisconnect%s", tc.label, outcome, reason, expected)
					}
					for time.Now().Before(deadline) {
						sink.mu.Lock()
						closed := sink.err != nil
						sink.mu.Unlock()
						if closed {
							break
						}
						time.Sleep(10 * time.Millisecond)
					}
					sink.mu.Lock()
					closed := sink.err != nil
					sink.mu.Unlock()
					if !closed {
						t.Errorf("%s disconnect did not close connection", tc.label)
					}
				} else if outcome != "alive" || reason != "" {
					t.Errorf("%s outcome%s reason%s wantalive", tc.label, outcome, reason)
				}

				c.Close()
			}
		})
	}
}
