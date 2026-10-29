package blackbox

import (
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/world"
)

// Oracle: official26.2 SHA-1 823e2250d24b3ddac457a60c92a6a941943fcd6a;
// wire plus Temurin25 javap BundleItem.onDestroyed, ItemUtils and ItemEntity.
func TestBundleDestructionParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("BundleBurn")}, nil)
	defer tg.cleanup()
	run := func(addr string) [][]string {
		item := `minecraft:bundle[bundle_contents=[{id:"minecraft:netherite_ingot",count:3},{id:"minecraft:ancient_debris",count:2},{id:"minecraft:blue_bundle",count:1,components:{"minecraft:bundle_contents":[{id:"minecraft:netherite_scrap",count:4}]}}]]`
		d := newClickDriverOn(t, addr, "BundleBurn", "minecraft:chest", 2, [][2]string{{"hotbar.0", item}}, nil)
		d.close()
		d.click("player-sync", 9, 0, 0, true, true)
		offset := 0
		for _, c := range []byte("BundleBurn") {
			offset = (offset*31 + int(c)) & 15
		}
		x0 := 6 + 32*offset
		serial := 0
		command := func(text string) {
			sendCommand(t, d.pw, text)
			serial++
			if !digSentinel(t, d.pw, d.sink, fmt.Sprintf("BBURN_%d", serial), 15*time.Second) {
				t.Fatalf("command failed %s", text)
			}
		}
		command("tick freeze")
		command(fmt.Sprintf("tp @s %d.5 70 5.5 0 0", x0+5))
		command(fmt.Sprintf("fill %d 69 3 %d 74 8 air", x0+3, x0+8))
		command(fmt.Sprintf("fill %d 71 4 %d 73 7 lava", x0+4, x0+7))
		d.click("drop-bundle", 36, 1, 4, true, false)
		if d.snap.p0[36] != "-" {
			t.Fatalf("bundle throw did not commit: held=%s", d.snap.p0[36])
		}

		command(fmt.Sprintf("tp @s %d.5 70 0.5", x0))
		d.sink.take()
		active := map[int32]server.ItemStack{}
		fold := func(packets []usePkt) {
			for _, p := range packets {
				r := protocol.NewPacketReader(p.data)
				if p.id == server.IDPlaySpawnEntity {
					_, _ = r.VarInt()
					_, _ = r.UUID()
					typ, e := r.VarInt()
					if e == nil {
						if name, ok := server.EntityTypeName(typ); ok && name == "item" {
							_, _ = r.Double()
							_, _ = r.Double()
							_, _ = r.Double()
							vx, vy, vz, ok := decodeLpVec3(r)
							if !ok || vx < -.101 || vx > .101 || vy < .199 || vy > .201 || vz < -.101 || vz > .101 {
								t.Errorf("fresh spill spawn velocity %v %v %v valid=%v", vx, vy, vz, ok)
							}
						}
					}
					continue
				}

				if p.id == server.IDPlayRemoveEntity {
					n, e := r.VarInt()
					if e == nil {
						for range n {
							id, e := r.VarInt()
							if e == nil {
								delete(active, id)
							}
						}
					}
					continue
				}
				if p.id != server.IDPlayEntityMetadata {
					continue
				}
				eid, e := r.VarInt()
				if e != nil {
					continue
				}
				for {
					idx, e := r.Byte()
					if e != nil || idx == 255 {
						break
					}
					ser, e := r.Byte()
					if e != nil {
						break
					}
					if idx == 8 && ser == 7 {
						it, e := readSlotBB(r)
						if e == nil {
							active[eid] = it
						}
						break
					}
					if !skipMetaVal(r, ser) {
						break
					}
				}
			}
		}
		snapshot := func() []string {
			var out []string
			for _, it := range active {
				if !it.Empty() {
					out = append(out, stackKey(it))
				}
			}
			sort.Strings(out)
			return out
		}
		step := func(n int) {
			sendCommand(t, d.pw, fmt.Sprintf("tick step %d", n))
			settleUse(t, d.sink, fold, "bundle-burn-step")
		}
		step(6)
		first := snapshot()
		step(6)
		last := snapshot()
		expected := []string{fmt.Sprintf("%d#3", world.ItemIDs["netherite_ingot"]), fmt.Sprintf("%d#2", world.ItemIDs["ancient_debris"]), fmt.Sprintf("%d#4", world.ItemIDs["netherite_scrap"])}
		sort.Strings(expected)
		if !reflect.DeepEqual(last, expected) {
			t.Errorf("final spill=%v want%v", last, expected)
		}
		if len(first) != 3 {
			t.Errorf("initial spill=%v", first)
		}
		return [][]string{first, last}
	}
	want := run(tg.vanilla)
	got := run(tg.mcgo)
	t.Logf("vanilla %v; mcgo %v", want, got)
	if !reflect.DeepEqual(want, got) {
		t.Fatal("bundle destruction differs")
	}
}
