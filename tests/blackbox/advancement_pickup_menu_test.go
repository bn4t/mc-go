//go:build blackbox

package blackbox

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/world"
)

// A real inventory_changed reward replaces the backing chest during pickup.
// The play_time scoreboard update marks the end of that same player tick.
func TestAdvancementPickupClosesMenuBeforeTickScoreParity(t *testing.T) {
	names := []string{"RewardPickupV", "RewardPickupM"}
	for i := range names {
		for suffix := 'a'; suffix <= 'p'; suffix++ {
			candidate := names[i] + string(suffix)
			hash := 0
			for _, b := range []byte(candidate) {
				hash = (31*hash + int(b)) & 15
			}
			if hash == 0 {
				names[i] = candidate
				break
			}
		}
	}
	directory := t.TempDir()
	files := map[string]string{"ops.json": opsJSONFor(names...)}
	bodies := map[string]string{
		"pack.mcmeta": `{"pack":{"description":"pickup reward menu validity","min_format":107,"max_format":107}}`,
		"data/pickupreward/advancement/inventory.json":     `{"criteria":{"picked":{"trigger":"minecraft:inventory_changed","conditions":{"items":[{"items":"minecraft:amethyst_shard"}]}}},"rewards":{"function":"pickupreward:invalidate"}}`,
		"data/pickupreward/function/invalidate.mcfunction": "setblock 8 -61 0 minecraft:air\n",
	}
	for name, body := range bodies {
		rel := "datapacks/pickupreward/" + name
		files["world/"+rel] = body
		path := filepath.Join(directory, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	tg := requireBlackboxFiles(t, nil, files, func(s *server.Server) { s.WorldDir = directory })
	defer tg.cleanup()
	run := func(addr, name string) {
		d := newClickDriverOn(t, addr, name, "minecraft:chest", 2, [][2]string{{"hotbar.0", "minecraft:diamond 3"}}, nil)
		command := func(c string) {
			sendCommand(t, d.pw, c)
			settleUse(t, d.sink, func(ps []usePkt) { d.foldPkts(ps) }, "pickup-reward-setup")
		}
		command("tick freeze")
		d.click("reward-cursor", 54, 0, 0, false, false)
		command("scoreboard objectives add reward_clock minecraft.custom:minecraft.play_time")
		command("scoreboard objectives setdisplay sidebar reward_clock")
		d.sink.take()
		take, closed, mark := -1, -1, -1
		sequence := 0
		sendCommand(t, d.pw, `summon minecraft:item 6 -60 2 {Item:{id:"minecraft:amethyst_shard",count:1},PickupDelay:0s}`)
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			packets := d.sink.take()
			d.foldPkts(packets)
			for _, p := range packets {
				sequence++
				switch p.id {
				case server.IDPlayTakeItem:
					if take < 0 {
						take = sequence
					}
				case server.IDPlayContainerClose:
					if closed < 0 {
						closed = sequence
					}
				case server.IDPlayUpdateScore:
					r := protocol.NewPacketReader(p.data)
					holder, _ := r.String()
					objective, _ := r.String()
					if take >= 0 && mark < 0 && holder == name && objective == "reward_clock" {
						mark = sequence
					}
				}
			}
			if take >= 0 && closed >= 0 && mark >= 0 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !(take >= 0 && take < closed && closed < mark) {
			t.Errorf("%s pickup/close/tick-score order = %d/%d/%d", name, take, closed, mark)
		}
		settleUse(t, d.sink, func(ps []usePkt) { d.foldPkts(ps) }, "pickup-reward-return")
		d.click("reward-return-resync", 9, 0, 6, true, true)
		diamond := fmt.Sprintf("%d#3", world.ItemIDs["diamond"])
		shard := fmt.Sprintf("%d#1", world.ItemIDs["amethyst_shard"])
		foundDiamond, foundShard := false, false
		for _, stack := range d.snap.p0 {
			foundDiamond = foundDiamond || stack == diamond
			foundShard = foundShard || stack == shard
		}
		if !d.sawClose || d.snap.carried != "-" || !foundDiamond || !foundShard {
			t.Errorf("%s closed=%v cursor=%s inventory=%v", name, d.sawClose, d.snap.carried, d.snap.p0)
		}
		t.Logf("%s order=%d/%d/%d closed=%v cursor=%s diamond=%v shard=%v", name, take, closed, mark, d.sawClose, d.snap.carried, foundDiamond, foundShard)
		command("scoreboard objectives setdisplay sidebar")
		command("tick unfreeze")
	}
	run(tg.vanilla, names[0])
	run(tg.mcgo, names[1])
}
