//go:build blackbox

package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// spawnerCase places one spawner (absolute centre x, z; the spawner sits
// one block above the -61 grass floor), optionally inside a hollow box
// (stone: dark; glass: lit, keeping wanderers in range), and stands
// the player off along +x.
type spawnerCase struct {
	name string
	nbt  string
	box  string  // enclosing hollow box block ("" for none)
	far  float64 // player offset along +x
	pre  []string
	mob  string
	wait time.Duration
}

const spawnerFast = `{SpawnData:{entity:{id:"minecraft:%s"}},Delay:5s,MinSpawnDelay:10s,MaxSpawnDelay:11s,SpawnCount:2s,MaxNearbyEntities:3s}`

var spawnerCases = []spawnerCase{
	{name: "zombie dark box", nbt: fmt.Sprintf(spawnerFast, "zombie"), box: "stone", far: 8, mob: "zombie", wait: 6 * time.Second},
	{name: "zombie rule off", nbt: fmt.Sprintf(spawnerFast, "zombie"), box: "stone", far: 8, mob: "zombie", wait: 4 * time.Second,
		pre: []string{"gamerule spawner_blocks_work false"}},
	{name: "zombie player far", nbt: fmt.Sprintf(spawnerFast, "zombie"), box: "stone", far: 20, mob: "zombie", wait: 4 * time.Second},
	{name: "pig on grass", nbt: fmt.Sprintf(spawnerFast, "pig"), box: "glass", far: 8, mob: "pig", wait: 6 * time.Second},
	{name: "zombie daylight", nbt: fmt.Sprintf(spawnerFast, "zombie"), far: 8, mob: "zombie", wait: 4 * time.Second},
	{name: "empty spawner", nbt: "", box: "stone", far: 8, mob: "pig", wait: 3 * time.Second},
}

// TestSpawnerParity drives BaseSpawner.serverTick: a dark box fills to
// MaxNearbyEntities, the gamerule and the player range gate it, animals
// need their spawnable block and light, monsters the dark, and an empty
// spawner only re-delays (one block event). Each case compares the 2004
// spawn level events and the spawner's block events.
func TestSpawnerParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("Spawny")}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
	})
	defer tg.cleanup()
	run := func(addr string) []string {
		c, pr, pw := joinPlayQuiet(t, addr, "Spawny")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		sink := newChatSinkTP(pr, pw)
		sink.cap = 1 << 16
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sendCommand(t, pw, "gamerule random_tick_speed 0")
		sendCommand(t, pw, "difficulty easy")
		sendCommand(t, pw, "time set 6000")
		sendCommand(t, pw, "gamerule advance_time false")
		sendCommand(t, pw, "gamemode spectator @s")
		time.Sleep(2 * time.Second)
		var out []string
		for i, sc := range spawnerCases {
			x, y, z := 200+60*i, -60, 40
			sendCommand(t, pw, "gamerule spawner_blocks_work true")
			sendCommand(t, pw, fmt.Sprintf("tp @s %v %d %d", float64(x)+0.5+sc.far, y, z))
			time.Sleep(1500 * time.Millisecond)
			sendCommand(t, pw, "gamemode survival @s")
			if sc.box != "" {
				sendCommand(t, pw, fmt.Sprintf("fill %d %d %d %d %d %d %s hollow", x-5, y-1, z-5, x+5, y+4, z+5, sc.box))
				if sc.box == "glass" {
					// Keep the grass floor the pigs spawn on.
					sendCommand(t, pw, fmt.Sprintf("fill %d %d %d %d %d %d grass_block", x-4, y-1, z-4, x+4, y-1, z+4))
				}
			}
			for _, p := range sc.pre {
				sendCommand(t, pw, p)
			}
			time.Sleep(500 * time.Millisecond)
			sink.take()
			sendCommand(t, pw, fmt.Sprintf("setblock %d %d %d spawner%s", x, y+1, z, sc.nbt))
			time.Sleep(sc.wait)
			sendCommand(t, pw, "gamemode spectator @s")
			sendCommand(t, pw, fmt.Sprintf("kill @e[type=minecraft:%s,x=%d,y=%d,z=%d,distance=..12]", sc.mob, x, y, z))
			time.Sleep(time.Second)
			spawns, events := 0, 0
			for _, l := range sink.take() {
				i := strings.Index(l, ":")
				if i < 0 {
					continue
				}
				var id int32
				fmt.Sscanf(l[:i], "%v", &id)
				r := protocol.NewPacketReader([]byte(l[i+1:]))
				switch id {
				case server.IDPlayLevelEvent:
					ev, _ := r.Int()
					px, py, pz, _ := r.Position()
					if ev == 2004 && px == int32(x) && py == int32(y+1) && pz == int32(z) {
						spawns++
					}
				case 0x07: // block_event
					px, py, pz, _ := r.Position()
					if px == int32(x) && py == int32(y+1) && pz == int32(z) {
						events++
					}
				}
			}
			out = append(out, fmt.Sprintf("spawn-events=%d block-events=%d", spawns, events))
		}
		return out
	}
	want := run(tg.vanilla)
	got := run(tg.mcgo)
	for i, c := range spawnerCases {
		if want[i] != got[i] {
			t.Errorf("%s:\n  vanilla %s\n  mc-go   %s", c.name, want[i], got[i])
		} else {
			t.Logf("%s: %s", c.name, got[i])
		}
	}
}
