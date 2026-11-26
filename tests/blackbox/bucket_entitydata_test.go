package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestBucketEntityDataParity carries a caught mob's state through the
// bucket: a cod summoned with Health 2, NoAI and Silent is bucketed
// (Bucketable.saveDefaultDataToBucketTag), the held cod_bucket shows
// the minecraft:bucket_entity_data component, and releasing it spawns
// a cod with the same health and flags (loadDefaultDataFromBucketTag).
func TestBucketEntityDataParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{
		"gamemode": "survival",
	}, map[string]string{
		"ops.json": opsJSONFor("BE"),
	}, func(s *server.Server) { s.GameMode = server.GameModeSurvival })
	defer tg.cleanup()

	codType, _ := server.EntityTypeID("cod")

	run := func(addr string) map[string]string {
		c, pr, pw := joinPlayBlackbox(t, addr, "BE")
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(120 * time.Second))
		sink := newChatSinkTP(pr, pw)
		sink.cap = 1 << 16
		var lines []string
		collect := func() { lines = append(lines, sink.take()...) }
		waitFor := func(sel, marker string) string {
			end := time.Now().Add(20 * time.Second)
			for time.Now().Before(end) {
				sendCommand(t, pw, fmt.Sprintf("execute if %s run say %s", sel, marker))
				deadline := time.Now().Add(700 * time.Millisecond)
				for time.Now().Before(deadline) {
					for _, l := range sink.take() {
						lines = append(lines, l)
						if strings.Contains(l, marker) {
							return "hit"
						}
					}
					time.Sleep(20 * time.Millisecond)
				}
			}
			return "miss"
		}
		out := map[string]string{}
		if waitFor("entity @s", "BEREADY") != "hit" {
			t.Fatalf("%s: commands never answered", addr)
		}
		for _, cmd := range []string{
			"gamerule doMobSpawning false",

			"setblock 32 -60 32 minecraft:water",
			"tp @s 31.5 -60 32.5 90 0",
			"item replace entity @s hotbar.0 with minecraft:water_bucket",
		} {
			sendCommand(t, pw, cmd)
		}
		// The teleport command has to land before the client's own move
		// packet, or the server sees a 31-block movement jump and kicks.
		sendCommand(t, pw, "say BESETUP")
		deadline := time.Now().Add(10 * time.Second)
		for !strings.Contains(strings.Join(sink.take(), ""), "BESETUP") {
			if time.Now().After(deadline) {
				t.Fatalf("%s: setup commands never drained", addr)
			}
			time.Sleep(20 * time.Millisecond)
		}
		_ = sendMovePosRotPitch(pw, 31.5, -60, 32.5, 90, 0)
		time.Sleep(300 * time.Millisecond)
		collect()
		sendCommand(t, pw, `summon minecraft:cod 32.5 -60 32.5 {Health:2.0f,NoAI:1b,Silent:1b}`)
		codID := int32(-1)
		for try := 0; try < 25 && codID < 0; try++ {
			collect()
			codID = aniSpawnID(lines, codType, 32.5, 32.5)
			time.Sleep(100 * time.Millisecond)
		}
		if codID < 0 {
			t.Fatalf("%s: summed cod never paired", addr)
		}
		// The real client's interact shape: a nonzero LpVec3 hit point.
		var ib protocol.Buffer
		ib.VarInt(codID)
		ib.VarInt(0) // hand: main
		ib.Raw([]byte{0xf9, 0xff, 0x5f, 0xff, 0x2b, 0x81})
		ib.Bool(false)
		if err := pw.WritePacket(server.IDPlayInteractSB, ib.Bytes()); err != nil {
			t.Fatal(err)
		}
		time.Sleep(700 * time.Millisecond)
		collect()
		out["caught"] = waitFor("items entity @s weapon.mainhand minecraft:cod_bucket", "BECAUGHT")

		// The bucket stack itself carries the payload; /data get prints
		// the component and its typed fields.
		sendCommand(t, pw, "data get entity @s Inventory")
		time.Sleep(600 * time.Millisecond)
		collect()
		joined := strings.Join(lines, "")
		if strings.Contains(joined, "bucket_entity_data") && strings.Contains(joined, "Health") {
			out["itemData"] = "hit"
		} else {
			out["itemData"] = "miss"
		}

		// Release the held cod bucket into the cell at our feet.
		sendUseItemAim(t, pw, 0, 3001, 0, 90)
		time.Sleep(600 * time.Millisecond)
		collect()
		out["released"] = waitFor(`entity @e[type=minecraft:cod,nbt={NoAI:1b,Silent:1b,Health:2.0f}]`, "BERELEASED")
		out["water"] = waitFor("block 31 -60 32 minecraft:water", "BEWATER")
		return out
	}

	van := run(tg.vanilla)
	mc := run(tg.mcgo)
	t.Logf("vanilla %v", van)
	t.Logf("mc-go   %v", mc)
	for _, k := range []string{"caught", "itemData", "released", "water"} {
		if van[k] != "hit" {
			t.Errorf("vanilla %s=%s, want hit", k, van[k])
		}
		if mc[k] != "hit" {
			t.Errorf("mc-go %s=%s, want hit", k, mc[k])
		}
	}
}
