package blackbox

import (
	"net"
	"testing"
	"time"

	"mc-go/internal/protocol"
)

// TestLavaBurnSoundParity: Entity.lavaHurt plays GENERIC_BURN on
// every landed lava tick — shouldPlayLavaHurtSound is true for all
// living entities (ItemEntity's health>0||tick%10 is the only
// override). Measured on the 26.2 jar: hurt(lava,4) → GENERIC_BURN
// at vol 0.4 / pitch 2+rand·0.4 on the entity's own sound source.
// A pig takes ~20 ticks to die in lava, so the wire carries one
// entity.generic.burn (registry id 695, sent as holder 696 or a
// direct name) per tick until the corpse — then the cadence stops.
func TestLavaBurnSoundParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("LS")}, nil)
	defer targets.cleanup()

	countBurns := func(c net.Conn, pr *protocol.Reader, d time.Duration) int {
		n := 0
		deadline := time.Now().Add(d)
		for time.Now().Before(deadline) {
			_ = c.SetReadDeadline(time.Now().Add(time.Until(deadline)))
			p, err := pr.ReadPacket()
			if err != nil {
				break
			}
			if p.ID != 0x75 { // sound (named event holder)
				continue
			}
			r := protocol.NewPacketReader(p.Data)
			id, e := r.VarInt()
			if e != nil {
				continue
			}
			switch {
			case id == 0:
				// Direct holder — the event name rides a String.
				if nm, e2 := r.String(); e2 == nil &&
					nm == "minecraft:entity.generic.burn" {
					n++
				}
			case id == 695+1:
				// Registry holder — wire value is protocol id + 1.
				n++
			}
		}
		return n
	}

	for _, s := range []struct{ label, addr string }{
		{"vanilla", targets.vanilla}, {"mcgo", targets.mcgo},
	} {
		t.Run(s.label, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, s.addr, "LS")
			defer c.Close()
			sendClientInformation(t, pw)
			time.Sleep(400 * time.Millisecond)
			drainChunkWindow(t, s.label, c, pr, pw, 300*time.Millisecond)
			sendCommand(t, pw, "gamerule do_mob_spawning false")
			sendCommand(t, pw, "kill @e[type=!minecraft:player]")
			time.Sleep(300 * time.Millisecond)
			// One-cell lava pool in a walled pit.
			for _, cmd := range []string{
				"fill 9 -60 9 11 -56 9 minecraft:stone",
				"fill 9 -60 11 11 -56 11 minecraft:stone",
				"fill 9 -60 9 9 -56 11 minecraft:stone",
				"fill 11 -60 9 11 -56 11 minecraft:stone",
				"fill 10 -60 10 10 -60 10 minecraft:lava",
			} {
				sendCommand(t, pw, cmd)
			}
			// A 10hp pig dies on its third lava tick — expect ~3 burn
			// packets. Resummon once on a vanilla summon drop.
			n := 0
			for attempt := 0; attempt < 2 && n == 0; attempt++ {
				sendCommand(t, pw,
					`summon minecraft:pig 10.5 -59.5 10.5 {NoAI:1b}`)
				n = countBurns(c, pr, 1500*time.Millisecond)
			}
			if n < 2 {
				t.Fatalf("%s: only %d GENERIC_BURN packets while pig burned in lava", s.label, n)
			}
			// The cadence stops with the corpse (lava's own ambient
			// bubbles ride a different id and don't count).
			if n2 := countBurns(c, pr, 2200*time.Millisecond); n2 > 1 {
				t.Fatalf("%s: %d GENERIC_BURN packets after the pig died — cadence did not stop", s.label, n2)
			}
		})
	}
}
