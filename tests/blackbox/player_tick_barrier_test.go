package blackbox

import (
	"fmt"
	"testing"
	"time"

	"mc-go/internal/protocol"
)

// Player connection ticks continue under /tick freeze. World gametime and
// /tick step completion do not prove that this separate tick has happened.
func setupPlayerTickBarrierBB(t *testing.T, pw *protocol.Writer) {
	t.Helper()
	sendCommand(t, pw, "scoreboard objectives add BBPlayerTick minecraft.custom:minecraft.play_time")
	sendCommand(t, pw, "scoreboard objectives add BBPlayerGoal dummy")
}

// collect must retain/fold every captured packet through its exact /say
// receipt, so pose/spawn updates that establish readiness are not drained away.
func waitPlayerTicksBB(t *testing.T, pw *protocol.Writer, collect func(string) []string, tag string, ticks int) {
	t.Helper()
	if ticks <= 0 {
		t.Fatal("player tick barrier requires a positive count")
	}
	ready := false
	deadline := time.Now().Add(10 * time.Second)
	for attempt := 0; !ready && time.Now().Before(deadline); attempt++ {
		marker, end := fmt.Sprintf("BBPTREADY%s_%dX", tag, attempt), fmt.Sprintf("BBPTREADYEND%s_%dX", tag, attempt)
		sendCommand(t, pw, "execute if score @s BBPlayerTick matches 0.. run say "+marker)
		sendCommand(t, pw, "say "+end)
		ready = structureSaidBB(collect(end), marker)
	}
	if !ready {
		t.Fatalf("%s player tick counter absent", tag)
	}
	sendCommand(t, pw, "scoreboard players operation @s BBPlayerGoal = @s BBPlayerTick")
	sendCommand(t, pw, fmt.Sprintf("scoreboard players add @s BBPlayerGoal %d", ticks))
	settled := false
	deadline = time.Now().Add(15 * time.Second)
	for attempt := 0; !settled && time.Now().Before(deadline); attempt++ {
		marker, end := fmt.Sprintf("BBPTDONE%s_%dX", tag, attempt), fmt.Sprintf("BBPTEND%s_%dX", tag, attempt)
		sendCommand(t, pw, "execute if score @s BBPlayerTick >= @s BBPlayerGoal run say "+marker)
		sendCommand(t, pw, "say "+end)
		settled = structureSaidBB(collect(end), marker)
	}
	if !settled {
		t.Fatalf("%s %d player ticks did not complete", tag, ticks)
	}
}
