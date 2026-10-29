package server

import (
	"bytes"
	"testing"
)

// Native functions reload the base flag twice in the same frame. The
// final value equals the sent value, but the dirty write still survives.
func TestCrystalNoGravityReloadDirtyLatch(t *testing.T) {
	for _, start := range []bool{false, true} {
		e := newEndCrystal(dimOverworld, .5, 165, .5)
		e.setNoGravity(start)
		_ = withEntityNoGravityMetadata(nil, e, true)
		value := func(v bool) int8 {
			if v {
				return 1
			}
			return 0
		}
		applyCrystalSave(e, map[string]any{"NoGravity": value(!start)}, false)
		applyCrystalSave(e, map[string]any{"NoGravity": value(start)}, false)
		if e.NoGravity != start {
			t.Fatalf("reloadfinal%t want%t", e.NoGravity, start)
		}
		want := []byte{5, 8, byte(value(start)), 255}
		if got := withEntityNoGravityMetadata(nil, e, true); !bytes.Equal(got, want) {
			t.Fatalf("same-final%t dirty%v want%v", start, got, want)
		}
		applyCrystalSave(e, map[string]any{"NoGravity": value(start)}, false)
		if got := withEntityNoGravityMetadata(nil, e, true); got != nil {
			t.Fatalf("no-op reload dirty%v", got)
		}
	}
}
