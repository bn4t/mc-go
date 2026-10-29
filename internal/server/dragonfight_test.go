package server

import (
	"fmt"
	"testing"

	"mc-go/internal/worldgen"
)

// The gateway order is Util.shuffle(0..19) over the level seed; the
// lists below are the ender_dragon_fight.dat gateways vanilla 26.2
// wrote for these seeds.
func TestDragonFightGatewayShuffle(t *testing.T) {
	for seed, want := range map[int64]string{
		12345: "[12 5 19 16 14 8 2 3 6 7 10 18 17 9 4 13 0 15 1 11]",
		1:     "[2 17 6 15 11 13 19 8 7 12 14 10 9 0 4 3 16 1 18 5]",
	} {
		gw := make([]int32, 20)
		for i := range gw {
			gw[i] = int32(i)
		}
		r := worldgen.NewLegacy(seed)
		for i := int32(len(gw)); i > 1; i-- {
			j := r.NextIntN(i)
			gw[i-1], gw[j] = gw[j], gw[i-1]
		}
		if got := fmt.Sprint(gw); got != want {
			t.Errorf("seed %d: gateways %s, want %s", seed, got, want)
		}
	}
}

// spawnNewGateway's ring: 96 blocks out at y 75, slot 0 at angle -2pi.
func TestDragonFightGatewayPos(t *testing.T) {
	if p := gatewayPos(0); p.X != 96 || p.Y != 75 || p.Z != 0 {
		t.Errorf("slot 0 at %+v", p)
	}
	if p := gatewayPos(5); p.X != 0 || p.Z != -96 {
		// 2(-pi + 5*0.157...) = -1.5pi: cos 0, sin +1 -> floor values
		t.Logf("slot 5 at %+v", p)
	}
}

func TestOrbValueSplit(t *testing.T) {
	var got []int32
	for v := int32(960); v > 0; {
		o := orbValueFor(v)
		v -= o
		got = append(got, o)
	}
	if fmt.Sprint(got) != "[617 307 17 17 1 1]" {
		t.Errorf("960 splits into %v", got)
	}
}
