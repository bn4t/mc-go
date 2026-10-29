package blackbox

import (
	"testing"
	"time"
)

// waitForExactSayBB retains the full capture until a decoded /say message
// equals marker. Error command context, sender text and marker suffixes cannot
// complete the barrier (the shared structureSaidBB matcher pins those cases).
func waitForExactSayBB(t *testing.T, sink *chatSink, marker string, timeout time.Duration) []string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lines []string
	for time.Now().Before(deadline) {
		lines = append(lines, sink.take()...)
		if structureSaidBB(lines, marker) {
			return lines
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("exact /say marker %q never arrived; sink health: %s", marker, sink.health())
	return lines
}
