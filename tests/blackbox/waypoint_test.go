package blackbox

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
)

// TestWaypointParity pins the tracked_waypoint (0x8A) locator-bar wire:
// TRACK/UNTRACK/UPDATE ops, the VEC3I/CHUNK/AZIMUTH kind selection, the
// emitted contents and the connection lifecycle — all measured on the
// 26.2 jar (ServerWaypointManager + WaypointTransmitter javap, plus a
// live-wire probe: chessboard <= view radius -> VEC3I, further chunk ->
// CHUNK, dist > 332 -> AZIMUTH, azimuth angle atan2(diff.x,-diff.z) with
// a 0.5-degree update gate, manhattan > 1 / chessboard > 1 / range- or
// visibility-changes breaking connections into fresh TRACKs).
func TestWaypointParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("PrjA", "PrjB"),
	}, nil)
	defer targets.cleanup()

	decode := func(raw []byte) (map[string]any, error) {
		r := protocol.NewPacketReader(raw)
		op, err := r.VarInt()
		if err != nil {
			return nil, err
		}
		// identifier Either: bool(1) = UUID.
		isUUID, err := r.Bool()
		if err != nil {
			return nil, err
		}
		out := map[string]any{"op": op, "isUUID": isUUID}
		var uuid [16]byte
		if isUUID {
			if uuid, err = r.UUID(); err != nil {
				return nil, err
			}
			out["uuid"] = fmt.Sprintf("%x", uuid)
		} else {
			s, err := r.String()
			if err != nil {
				return nil, err
			}
			out["uuid"] = "str:" + s
		}
		if op == 1 {
			return out, nil
		}
		style, err := r.String()
		if err != nil {
			return nil, err
		}
		out["style"] = style
		hasColor, err := r.Bool()
		if err != nil {
			return nil, err
		}
		out["hasColor"] = hasColor
		if hasColor {
			c, err := r.Int()
			if err != nil {
				return nil, err
			}
			out["color"] = c
		}
		kind, err := r.VarInt()
		if err != nil {
			return nil, err
		}
		out["kind"] = kind
		switch kind {
		case 1:
			var xyz [3]int32
			for i := range xyz {
				if xyz[i], err = r.VarInt(); err != nil {
					return nil, err
				}
			}
			out["pos"] = xyz
		case 2:
			var cxz [2]int32
			for i := range cxz {
				if cxz[i], err = r.VarInt(); err != nil {
					return nil, err
				}
			}
			out["pos"] = cxz
		case 3:
			f, err := r.Float()
			if err != nil {
				return nil, err
			}
			out["angle"] = f
		}
		return out, nil
	}

	// wpWindow collects this window's 0x8A packets, decoded.
	wpWindow := func(t *testing.T, sink *chatSink, wait time.Duration) []map[string]any {
		t.Helper()
		time.Sleep(wait)
		var out []map[string]any
		for _, l := range sink.take() {
			if !strings.HasPrefix(l, "0x8a:") {
				continue
			}
			d, err := decode([]byte(l[5:]))
			if err != nil {
				t.Fatalf("waypoint decode %q: %v", l, err)
			}
			out = append(out, d)
		}
		sort.Slice(out, func(i, j int) bool {
			return fmt.Sprint(out[i]) < fmt.Sprint(out[j])
		})
		return out
	}

	type expect struct {
		// op: -1 = any, kind: -1 = any; pos/angle compared when set.
		op, kind  int32
		pos       [3]int32
		chunk     [2]int32
		angle     float32
		hasPos    bool
		hasChunk  bool
		hasAngle  bool
		uuid      string // "" = wildcard
		style     string
		ignorePos bool // compare everything but the position
	}

	check := func(t *testing.T, tag string, got []map[string]any, wants []expect) {
		t.Helper()
		if len(got) != len(wants) {
			t.Fatalf("%s: waypoint count %d != %d: %v", tag, len(got), len(wants), got)
		}
		used := make([]bool, len(got))
		for _, w := range wants {
			matched := false
			for i, g := range got {
				if used[i] {
					continue
				}
				if w.op >= 0 && g["op"] != w.op {
					continue
				}
				if w.kind >= 0 && g["kind"] != w.kind {
					continue
				}
				if w.uuid != "" && g["uuid"] != w.uuid {
					continue
				}
				if w.style != "" && g["style"] != w.style {
					continue
				}
				if w.hasPos && !w.ignorePos && g["pos"] != w.pos {
					continue
				}
				if w.hasChunk && g["pos"] != w.chunk {
					continue
				}
				if w.hasAngle {
					a, ok := g["angle"].(float32)
					if !ok || math.Abs(float64(a-w.angle)) > 1e-4 {
						continue
					}
				}
				used[i] = true
				matched = true
				break
			}
			if !matched {
				t.Fatalf("%s: no waypoint packet matching %+v in %v", tag, w, got)
			}
		}
	}

	for _, tg := range []struct{ tag, addr string }{
		{"vanilla", targets.vanilla},
		{"mc-go", targets.mcgo},
	} {
		uuids := map[string]string{}
		connA, prA, pwA := joinPlayBlackbox(t, tg.addr, "PrjA")
		sinkA := newChatSinkTP(prA, pwA)
		sendCommand(t, pwA, "tp @s 0 -60 0 0 0")
		time.Sleep(400 * time.Millisecond)
		sinkA.take()

		connB, prB, pwB := joinPlayBlackbox(t, tg.addr, "PrjB")
		sinkB := newChatSinkTP(prB, pwB)

		// W1: B joined — A tracks B (VEC3I at B's spawn, x/z jitter from
		// spawn radius), B tracks A at A's fixed (0,-60,0).
		gotA := wpWindow(t, sinkA, 1400*time.Millisecond)
		gotB := wpWindow(t, sinkB, 100*time.Millisecond)
		if len(gotA) != 1 || len(gotB) != 1 {
			t.Fatalf("%s: join waypoint counts A=%d B=%d: %v %v", tg.tag, len(gotA), len(gotB), gotA, gotB)
		}
		uuids["B"] = gotA[0]["uuid"].(string)
		uuids["A"] = gotB[0]["uuid"].(string)
		check(t, tg.tag+" joinB", gotA, []expect{
			{op: 0, kind: 1, uuid: uuids["B"], style: "minecraft:default"},
		})
		if gotA[0]["pos"].([3]int32)[1] != -60 {
			t.Fatalf("%s: join TRACK y != -60: %v", tg.tag, gotA)
		}
		check(t, tg.tag+" joinA", gotB, []expect{
			{op: 0, kind: 1, pos: [3]int32{0, -60, 0}, hasPos: true, uuid: uuids["A"], style: "minecraft:default"},
		})

		// W2: tp B inside the view — VEC3I (8,-60,0), TRACK or UPDATE.
		sendCommand(t, pwB, "tp @s 8 -60 0 0 0")
		check(t, tg.tag+" tp8", wpWindow(t, sinkA, 1200*time.Millisecond), []expect{
			{op: -1, kind: 1, pos: [3]int32{8, -60, 0}, hasPos: true, uuid: uuids["B"]},
		})

		// W3: tp B to chunk (3,0) — outside chessboard 2 -> CHUNK.
		sendCommand(t, pwB, "tp @s 50 -60 0 0 0")
		check(t, tg.tag+" tp50", wpWindow(t, sinkA, 1200*time.Millisecond), []expect{
			{op: 0, kind: 2, chunk: [2]int32{3, 0}, hasChunk: true, uuid: uuids["B"]},
		})

		// W4: tp B beyond 332 -> AZIMUTH, angle atan2(-400, -0) = -pi/2.
		sendCommand(t, pwB, "tp @s 400 -60 0 0 0")
		check(t, tg.tag+" tp400", wpWindow(t, sinkA, 1200*time.Millisecond), []expect{
			{op: 0, kind: 3, angle: -math.Pi / 2, hasAngle: true, uuid: uuids["B"]},
		})

		// W5: same-direction move under the 0.5-degree gate -> silence.
		sendCommand(t, pwB, "tp @s 410 -60 0 0 0")
		check(t, tg.tag+" tp410", wpWindow(t, sinkA, 1200*time.Millisecond), nil)

		// W6: azimuth drift past the gate -> UPDATE azimuth.
		sendCommand(t, pwB, "tp @s 400 -60 50 0 0")
		want := float32(math.Atan2(-400, 50))
		check(t, tg.tag+" tp400-50", wpWindow(t, sinkA, 1200*time.Millisecond), []expect{
			{op: 2, kind: 3, angle: want, hasAngle: true, uuid: uuids["B"]},
		})

		// W7: back inside the view -> TRACK VEC3I again.
		sendCommand(t, pwB, "tp @s 8 -60 0 0 0")
		check(t, tg.tag+" tp8back", wpWindow(t, sinkA, 1200*time.Millisecond), []expect{
			{op: 0, kind: 1, pos: [3]int32{8, -60, 0}, hasPos: true, uuid: uuids["B"]},
		})

		// W8: a mob with transmit 0 is silent.
		sendCommand(t, pwA, "summon minecraft:zombie 8 -60 4 {NoAI:1b}")
		check(t, tg.tag+" zombie-silent", wpWindow(t, sinkA, 1200*time.Millisecond), nil)

		// W9: transmit range set -> the mob starts broadcasting VEC3I.
		sendCommand(t, pwA, "attribute @e[type=minecraft:zombie,limit=1] minecraft:waypoint_transmit_range base set 100")
		got := wpWindow(t, sinkA, 1200*time.Millisecond)
		check(t, tg.tag+" zombie-transmit", got, []expect{
			{op: 0, kind: 1, pos: [3]int32{8, -60, 4}, hasPos: true},
		})
		uuids["z"] = got[0]["uuid"].(string)

		// W10: B leaves -> UNTRACK B (zombie stays).
		connB.Close()
		time.Sleep(600 * time.Millisecond)
		check(t, tg.tag+" B-left", wpWindow(t, sinkA, 1200*time.Millisecond), []expect{
			{op: 1, kind: -1, uuid: uuids["B"]},
		})

		// W11: kill the zombie -> UNTRACK it too.
		sendCommand(t, pwA, "kill @e[type=minecraft:zombie]")
		check(t, tg.tag+" zombie-killed", wpWindow(t, sinkA, 1200*time.Millisecond), []expect{
			{op: 1, kind: -1, uuid: uuids["z"]},
		})

		connA.Close()
	}
}
