package blackbox

import (
	"mc-go/internal/protocol"
	"testing"
)

func TestResolvedSinkTeleportPosition(t *testing.T) {
	packet := func(position [3]float64, flags int32) []byte {
		var b protocol.Buffer
		b.VarInt(7)
		for _, v := range position {
			b.Double(v)
		}
		for i := 0; i < 3; i++ {
			b.Double(0)
		}
		b.Float(0)
		b.Float(0)
		b.Int(flags)
		return b.Bytes()
	}
	var position [3]float64
	var known [3]bool
	for _, step := range []struct {
		raw   [3]float64
		flags int32
		want  [3]float64
	}{
		{[3]float64{8.5, -58, 8.5}, 0, [3]float64{8.5, -58, 8.5}},
		{[3]float64{50.5, -58, 50.5}, 0, [3]float64{50.5, -58, 50.5}},
		{[3]float64{-40, 0, -42}, 7, [3]float64{10.5, -58, 8.5}},
		{[3]float64{2, -59, 1}, 5, [3]float64{12.5, -59, 9.5}},
	} {
		id, next, nextKnown, valid := resolveSinkTeleport(packet(step.raw, step.flags), position, known)
		if !valid || id != 7 || next != step.want || nextKnown != [3]bool{true, true, true} {
			t.Fatalf("raw%v mask%d got%v known%v valid%v", step.raw, step.flags, next, nextKnown, valid)
		}
		position, known = next, nextKnown
	}
	_, _, unknown, valid := resolveSinkTeleport(packet([3]float64{1, 2, 3}, 1), [3]float64{}, [3]bool{})
	if !valid || unknown[0] || !unknown[1] || !unknown[2] {
		t.Fatal("relative axis without a base became known")
	}
	for _, bad := range [][]byte{nil, {7}, packet([3]float64{}, 0)[:20]} {
		if _, _, _, valid := resolveSinkTeleport(bad, position, known); valid {
			t.Fatal("accepted truncated SyncPos")
		}
	}
}
