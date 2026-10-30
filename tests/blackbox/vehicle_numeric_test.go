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

func TestMoveVehicleNumericParity(t *testing.T) {
	cases := []struct {
		label      string
		pos        [3]float64
		yaw, pitch float32
		noOp       bool
		kick       bool
	}{
		{label: "finite"}, {label: "x_nan", kick: true, pos: [3]float64{math.NaN(), 0, 0}}, {label: "y_nan", kick: true, pos: [3]float64{0, math.NaN(), 0}}, {label: "z_nan", kick: true, pos: [3]float64{0, 0, math.NaN()}},
		{label: "yaw_nan", kick: true, yaw: float32(math.NaN())}, {label: "pitch_nan", kick: true, pitch: float32(math.NaN())},
		{label: "yaw_pos_inf", kick: true, yaw: float32(math.Inf(1))}, {label: "yaw_neg_inf", kick: true, yaw: float32(math.Inf(-1))}, {label: "pitch_pos_inf", kick: true, pitch: float32(math.Inf(1))}, {label: "pitch_neg_inf", kick: true, pitch: float32(math.Inf(-1))},
		{label: "x_pos_inf", pos: [3]float64{math.Inf(1), 0, 0}}, {label: "x_neg_inf", pos: [3]float64{math.Inf(-1), 0, 0}}, {label: "y_pos_inf", pos: [3]float64{0, math.Inf(1), 0}}, {label: "y_neg_inf", pos: [3]float64{0, math.Inf(-1), 0}}, {label: "z_pos_inf", pos: [3]float64{0, 0, math.Inf(1)}}, {label: "z_neg_inf", pos: [3]float64{0, 0, math.Inf(-1)}},
		{label: "huge_finite_coords", pos: [3]float64{math.MaxFloat64, math.MaxFloat64, -math.MaxFloat64}}, {label: "huge_finite_angles", yaw: math.MaxFloat32, pitch: -math.MaxFloat32},
		{label: "no_op_x_nan", kick: true, pos: [3]float64{math.NaN(), 0, 0}, noOp: true},
	}
	var ops []string
	for i, tc := range cases {
		if !tc.noOp {
			ops = append(ops, fmt.Sprintf("VehNum%d", i))
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
				name := fmt.Sprintf("VehNum%d", i)
				c, pr, pw := joinPlayBlackboxView(t, target.addr, name, 1)
				defer c.Close()
				drainToJoinBlackbox(t, target.name, pr)
				awaitPlayReadyBlackbox(t, target.name, c, pr, pw)
				sink := newChatSinkTP(pr, pw)
				var b protocol.Buffer
				for _, v := range tc.pos {
					b.Double(v)
				}
				b.Float(tc.yaw)
				b.Float(tc.pitch)
				b.Bool(true)
				if err := pw.WritePacket(server.IDPlayMoveVehicleSB, b.Bytes()); err != nil {
					t.Fatal(err)
				}
				marker := fmt.Sprintf("VEH_NUMERIC_%d", i)

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
					expected := fmt.Sprintf("%#v", map[string]any{"translate": "multiplayer.disconnect.invalid_vehicle_movement"})
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
