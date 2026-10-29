package server

import (
	"bytes"
	"testing"
)

// Native observations distinguish pairing's true value from the pending
// post-load dirty true value, and retain an explicit false on reset.
func TestEntityNoGravityMetadataLifecycle(t *testing.T) {
	for _, kind := range []string{"cow", "armor_stand", "item", "oak_boat", "vex"} {
		t.Run(kind, func(t *testing.T) {
			e := &Entity{Type: mustEntityType(kind), NoGravity: true}
			if got := withEntityNoGravityMetadata(nil, e, false); !bytes.Equal(got, []byte{5, 8, 1, 255}) {
				t.Fatalf("pair%v", got)
			}
			if got := withEntityNoGravityMetadata(nil, e, true); !bytes.Equal(got, []byte{5, 8, 1, 255}) {
				t.Fatalf("postload dirty%v", got)
			}
			if got := withEntityNoGravityMetadata(nil, e, true); got != nil {
				t.Fatalf("clean emitted%v", got)
			}
			e.setNoGravity(false)
			if got := withEntityNoGravityMetadata(nil, e, true); !bytes.Equal(got, []byte{5, 8, 0, 255}) {
				t.Fatalf("reset%v", got)
			}
			e.setNoGravity(false)
			if got := withEntityNoGravityMetadata(nil, e, true); got != nil {
				t.Fatalf("repeat reset%v", got)
			}
			e.setNoGravity(true)
			family := []byte{0, 0, 0, 255}
			if got := withEntityNoGravityMetadata(family, e, true); !bytes.Equal(got, []byte{5, 8, 1, 0, 0, 0, 255}) {
				t.Fatalf("merged restore%v", got)
			}
			if got := withEntityNoGravityMetadata(nil, e, false); !bytes.Equal(got, []byte{5, 8, 1, 255}) {
				t.Fatalf("late pair%v", got)
			}
			if got := withEntityNoGravityMetadata(nil, e, true); got != nil {
				t.Fatalf("pair dirtied global state%v", got)
			}
		})
	}
}
