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

// TestLeashParity pins the 26.2 leash model — Leashable, LeadItem,
// LeashFenceKnotEntity (all bytecode-measured):
//
//   - Lead on a leashed-or-free mob: re-attach to the player — lead
//     shrinks, `leash` NBT appears (UUID int-array for a player
//     holder), set_entity_link goes out.
//   - Empty hand on a mob leashed to you: dropLeash — the leash key
//     clears and a lead item entity drops.
//   - Lead on a fences-tag block: bindPlayerMobs re-attaches every
//     leashee in the 32-cube to a knot spawned at the fence —
//     `leash` becomes the [I;x,y,z] block-pos form and a leash_knot
//     entity exists.
//   - Clicking the knot returns its leashees to the player; a knot
//     with zero leashees discards.
//   - tickLeash snaps the leash when holder distance exceeds 12 —
//     the key clears and a lead drops.
//   - Shears drop the mob's own leash (and every leash it holds)
//     with the wear+snip — shearOffAllLeashConnections.
//   - Sneak-interact re-leashes the player's whole 32-cube flock
//     onto the clicked leashable — the leash survives the player
//     walking past snap distance, since the holder is now the mob.
func TestLeashParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Leash"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	for _, tc := range []struct{ tag, addr string }{
		{"vanilla", targets.vanilla},
		{"mc-go", targets.mcgo},
	} {
		t.Run(tc.tag, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, "Leash")
			defer c.Close()
			drainToJoinBlackbox(t, tc.tag, pr)
			if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
				t.Fatal(err)
			}

			probe := func(spec string, i int) string {
				sendCommand(t, pw, fmt.Sprintf("execute if %s run say ZLB-ok-%d", spec, i))
				sendCommand(t, pw, fmt.Sprintf("execute unless %s run say ZLB-miss-%d", spec, i))
				deadline := time.Now().Add(8 * time.Second)
				for time.Now().Before(deadline) {
					for _, e := range eggDrainFor(t, c, pr, pw, 400*time.Millisecond) {
						if e.kind != "chat" {
							continue
						}
						if strings.Contains(e.chat, fmt.Sprintf("ZLB-ok-%d", i)) {
							return "hit"
						}
						if strings.Contains(e.chat, fmt.Sprintf("ZLB-miss-%d", i)) {
							return "miss"
						}
					}
				}
				return "none"
			}

			interact := func(eid int32, sneak bool) {
				var ib protocol.Buffer
				ib.VarInt(eid)
				ib.VarInt(0) // hand: main
				ib.Raw(lpVec3(0, 1, 0))
				ib.Bool(sneak)
				_ = pw.WritePacket(server.IDPlayInteractSB, ib.Bytes())
			}

			// lastSpawn returns the newest spawned entity of the
			// given type seen while draining dur.
			lastSpawn := func(typeName string, dur time.Duration) int32 {
				tid, _ := server.EntityTypeID(typeName)
				var id int32
				for _, e := range eggDrainFor(t, c, pr, pw, dur) {
					if e.kind == "spawn" && e.etype == tid {
						id = e.eid
					}
				}
				return id
			}

			sendCommand(t, pw, "gamemode survival @s")
			sendCommand(t, pw, "tp @s 12.5 -60 12.5 0 0")
			sendCommand(t, pw, "clear @s")
			eggDrainFor(t, c, pr, pw, 1500*time.Millisecond)

			// === Leg 1: lead attach — the lead shrinks and the
			// leash key appears.
			sendCommand(t, pw, "give @s minecraft:lead 1")
			sendCommand(t, pw, `summon minecraft:sheep 12 -60 14 {NoAI:1b}`)
			// The summon broadcast can lag the tp's chunk stream — give
			// the entity-spawn packet room to arrive.
			var sheepID int32
			for i := 0; i < 10 && sheepID == 0; i++ {
				sheepID = lastSpawn("sheep", 1500*time.Millisecond)
			}
			if sheepID == 0 {
				t.Fatalf("%s: sheep spawn packet never seen", tc.tag)
			}
			interact(sheepID, false)
			eggDrainFor(t, c, pr, pw, 700*time.Millisecond)
			if p := probe("entity @e[type=minecraft:sheep]", 2); p != "hit" {
				t.Fatalf("%s: leg1 sheep gone — %s", tc.tag, p)
			}
			if p := probe(`entity @s[nbt={Inventory:[{id:"minecraft:lead"}]}]`, 3); p != "miss" {
				t.Fatalf("%s: leg1 lead-consumed probe = %s — want lead spent", tc.tag, p)
			}
			if p := probe("data entity @e[type=minecraft:sheep,limit=1] leash", 4); p != "hit" {
				t.Fatalf("%s: leg1 leash-key probe = %s — want a leash key", tc.tag, p)
			}

			// === Leg 2: empty-hand unleash — key clears, lead drops.
			// The dropped lead lands at the mob's feet; move the player
			// out of pickup range first or it gets vacuumed before the
			// item probe runs.
			sendCommand(t, pw, "clear @s")
			interact(sheepID, false)
			eggDrainFor(t, c, pr, pw, 400*time.Millisecond)
			sendCommand(t, pw, "tp @s 40 -60 40 0 0")
			eggDrainFor(t, c, pr, pw, 800*time.Millisecond)
			if p := probe("entity @e[type=minecraft:sheep]", 5); p != "hit" {
				t.Fatalf("%s: leg2 sheep gone — %s", tc.tag, p)
			}
			if p := probe(`entity @e[type=minecraft:item,nbt={Item:{id:"minecraft:lead"}}]`, 6); p != "hit" {
				t.Fatalf("%s: leg2 lead-drop probe = %s — want a lead item entity", tc.tag, p)
			}
			// The leash key must be gone.
			if p := probe("data entity @e[type=minecraft:sheep,limit=1] leash", 7); p != "miss" {
				t.Fatalf("%s: leg2 leash-cleared probe = %s — want no leash key", tc.tag, p)
			}
			sendCommand(t, pw, "kill @e[type=minecraft:item]")
			eggDrainFor(t, c, pr, pw, 800*time.Millisecond)

			// === Leg 3: fence bind — knot spawns, leash turns into
			// the block-pos form.
			sendCommand(t, pw, "tp @s 12.5 -60 12.5 0 0")
			sendCommand(t, pw, "give @s minecraft:lead 1")
			sendCommand(t, pw, "item replace entity @s hotbar.0 with minecraft:lead 4")
			sendCommand(t, pw, "setblock 12 -60 12 minecraft:oak_fence")
			// Chat commands are queued per tick; let them land before
			// the interact packet needs the lead in hand.
			eggDrainFor(t, c, pr, pw, 600*time.Millisecond)
			interact(sheepID, false)
			eggDrainFor(t, c, pr, pw, 700*time.Millisecond)
			sendUseItemOn(t, pw, 12, -60, 12, 1, 21)
			var knotID int32
			for i := 0; i < 10 && knotID == 0; i++ {
				knotID = lastSpawn("leash_knot", 1000*time.Millisecond)
			}
			if knotID == 0 {
				t.Fatalf("%s: leg3 leash_knot spawn packet never seen", tc.tag)
			}
			if p := probe("entity @e[type=minecraft:leash_knot]", 8); p != "hit" {
				t.Fatalf("%s: leg3 knot-present probe = %s — want the knot entity", tc.tag, p)
			}
			if p := probe("entity @e[type=minecraft:sheep,nbt={leash:[I;12,-60,12]}]", 9); p != "hit" {
				t.Fatalf("%s: leg3 knot-leash probe = %s — want leash:[I;12,-60,12]", tc.tag, p)
			}

			// === Leg 4: knot click — the leashee comes back to the
			// player; the knot discards itself.
			interact(knotID, false)
			eggDrainFor(t, c, pr, pw, 900*time.Millisecond)
			if p := probe("entity @e[type=minecraft:leash_knot]", 10); p != "miss" {
				t.Fatalf("%s: leg4 knot-discard probe = %s — want knot gone", tc.tag, p)
			}
			if p := probe("data entity @e[type=minecraft:sheep,limit=1] leash", 11); p != "hit" {
				t.Fatalf("%s: leg4 leash-back probe = %s — want the leash key back", tc.tag, p)
			}

			// === Leg 5: snap — walk past 12 blocks, the leash drops
			// a lead at the sheep.
			sendCommand(t, pw, "tp @s 40 -60 40 0 0")
			eggDrainFor(t, c, pr, pw, 1500*time.Millisecond)
			if p := probe("entity @e[type=minecraft:sheep]", 12); p != "hit" {
				t.Fatalf("%s: leg5 sheep gone — %s", tc.tag, p)
			}
			if p := probe("data entity @e[type=minecraft:sheep,limit=1] leash", 13); p != "miss" {
				t.Fatalf("%s: leg5 leash-snap probe = %s — want no leash key", tc.tag, p)
			}
			if p := probe(`entity @e[type=minecraft:item,nbt={Item:{id:"minecraft:lead"}}]`, 14); p != "hit" {
				t.Fatalf("%s: leg5 lead-drop probe = %s — want the snapped lead item", tc.tag, p)
			}
			sendCommand(t, pw, "kill @e[type=minecraft:item]")
			sendCommand(t, pw, "tp @s 12.5 -60 12.5 0 0")
			eggDrainFor(t, c, pr, pw, 800*time.Millisecond)

			// === Leg 6: shears — the mob's own leash drops with it.
			sendCommand(t, pw, "item replace entity @s hotbar.0 with minecraft:lead")
			sendCommand(t, pw, "clear @s minecraft:shears")
			eggDrainFor(t, c, pr, pw, 600*time.Millisecond)
			interact(sheepID, false)
			eggDrainFor(t, c, pr, pw, 700*time.Millisecond)
			sendCommand(t, pw, "item replace entity @s hotbar.0 with minecraft:shears")
			eggDrainFor(t, c, pr, pw, 600*time.Millisecond)
			interact(sheepID, false)
			eggDrainFor(t, c, pr, pw, 400*time.Millisecond)
			sendCommand(t, pw, "tp @s 40 -60 40 0 0")
			eggDrainFor(t, c, pr, pw, 800*time.Millisecond)
			if p := probe("data entity @e[type=minecraft:sheep,limit=1] leash", 15); p != "miss" {
				t.Fatalf("%s: leg6 shear-off probe = %s — want no leash key", tc.tag, p)
			}
			if p := probe(`entity @e[type=minecraft:item,nbt={Item:{id:"minecraft:lead"}}]`, 16); p != "hit" {
				t.Fatalf("%s: leg6 lead-drop probe = %s — want a dropped lead", tc.tag, p)
			}
			sendCommand(t, pw, "kill @e[type=minecraft:item]")
			sendCommand(t, pw, "clear @s")
			sendCommand(t, pw, "tp @s 12.5 -60 12.5 0 0")
			eggDrainFor(t, c, pr, pw, 800*time.Millisecond)

			// === Leg 7: sneak-transfer — the flock re-leashes onto
			// the clicked cow; the leash survives the player leaving
			// snap range.
			sendCommand(t, pw, `summon minecraft:cow 12 -60 16 {NoAI:1b}`)
			var cowID int32
			for i := 0; i < 10 && cowID == 0; i++ {
				cowID = lastSpawn("cow", 1500*time.Millisecond)
			}
			if cowID == 0 {
				t.Fatalf("%s: cow spawn packet never seen", tc.tag)
			}
			sendCommand(t, pw, "item replace entity @s hotbar.0 with minecraft:lead 8")
			eggDrainFor(t, c, pr, pw, 600*time.Millisecond)
			interact(sheepID, false)
			eggDrainFor(t, c, pr, pw, 700*time.Millisecond)
			interact(cowID, true) // sneak-transfer the leashed sheep
			eggDrainFor(t, c, pr, pw, 700*time.Millisecond)
			sendCommand(t, pw, "tp @s 60 -60 60 0 0")
			eggDrainFor(t, c, pr, pw, 1600*time.Millisecond)
			if p := probe("data entity @e[type=minecraft:sheep,limit=1] leash", 17); p != "hit" {
				t.Fatalf("%s: leg7 transfer-survives probe = %s — want sheep still leashed", tc.tag, p)
			}
			if p := probe(`entity @e[type=minecraft:item,nbt={Item:{id:"minecraft:lead"}}]`, 18); p != "miss" {
				t.Fatalf("%s: leg7 no-snap probe = %s — cow-held leash must not drop", tc.tag, p)
			}
		})
	}
}
