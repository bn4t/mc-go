package server

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// TestAdvancementSplit pins the timestamp splice in buildAdvancements:
// rebuilding with the capture's own award time must reproduce the
// capture byte for byte, and the spliced field must read back as that
// time. The time is the live server's award instant for the
// crafting_table criterion on the captured fresh join.
func TestAdvancementSplit(t *testing.T) {
	const wantTime = int64(1789306661123)
	got := buildAdvancements(wantTime)
	if !bytes.Equal(got, advancementsBody) {
		t.Fatalf("rebuild with capture time: %d bytes, want %d identical", len(got), len(advancementsBody))
	}
	if v := int64(binary.BigEndian.Uint64(got[advancementTimeAt:])); v != wantTime {
		t.Fatalf("spliced time reads back %d, want %d", v, wantTime)
	}
}
