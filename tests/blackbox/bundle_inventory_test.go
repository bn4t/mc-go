package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// Oracle: 26.2 SHA-1 823e2250d24b3ddac457a60c92a6a941943fcd6a,
// wire plus Temurin25 measurements of BundleItem click overrides,
// BundleContents.Mutable and AbstractContainerMenu.setSelectedBundleItemIndex.
func TestBundleInventoryParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("BundleV", "BundleM")}, nil)
	defer tg.cleanup()
	run := func(addr, name string) []string {
		d := newClickDriverOn(t, addr, name, "minecraft:chest", 2, [][2]string{{"hotbar.0", "minecraft:bundle"}, {"hotbar.1", "minecraft:stone 20"}, {"hotbar.2", "minecraft:dirt 11"}, {"hotbar.3", "minecraft:stone 60"}, {"hotbar.4", "minecraft:egg 2"}, {"hotbar.5", "minecraft:bundle"}}, nil)
		var out []string
		check := func(tag string, slots ...int32) {
			line := tag + " cursor=" + d.snap.carried
			for _, slot := range slots {
				line += fmt.Sprintf(" %d=%s", slot, d.snap.menu[slot])
			}
			out = append(out, line)
		}
		d.click("lift-bundle", 54, 0, 0, false, false)
		d.click("insert-stone", 55, 0, 0, false, false)
		d.click("insert-dirt", 56, 0, 0, false, false)
		check("two-types", 55, 56)
		d.click("extract-newest", 0, 1, 0, false, false)
		check("newest", 0)
		d.click("reinsert-dirt", 0, 0, 0, false, false)
		d.click("store-bundle", 2, 0, 0, false, false)
		selectIndex := func(index int32) {
			var b protocol.Buffer
			b.VarInt(2)
			b.VarInt(index)
			_ = d.pw.WritePacket(server.IDPlayBundleItemSelectedSB, b.Bytes())
			if !digSentinel(t, d.pw, d.sink, "BUNDLE_SELECTED", 10*time.Second) {
				t.Fatal("selection barrier")
			}
		}
		selectIndex(1)
		d.click("extract-selected", 2, 1, 0, false, false)
		check("selected", 2)
		if !strings.Contains(d.snap.carried, "#20") {
			t.Fatalf("%s selected extraction=%s", name, d.snap.carried)
		}
		d.click("put-back", 2, 0, 0, false, false)
		selectIndex(1)
		selectIndex(1)
		d.click("toggle-clears", 2, 1, 0, false, false)
		check("toggle", 2)
		d.click("stash-extracted", 3, 0, 0, false, false)
		selectIndex(99)
		d.click("invalid-clears", 2, 1, 0, false, false)
		check("invalid", 2, 3)
		d.click("stash-last", 4, 0, 0, false, false)
		d.click("lift-empty", 2, 0, 0, false, false)
		d.click("insert-sixty", 57, 0, 0, false, false)
		d.click("insert-egg-cap", 58, 0, 0, false, false)
		check("capacity", 57, 58)
		d.click("extract-egg", 5, 1, 0, false, false)
		d.click("insert-empty-bundle", 59, 0, 0, false, false)
		check("nested-overhead", 5, 58, 59)

		return out
	}
	want := run(tg.vanilla, "BundleV")
	got := run(tg.mcgo, "BundleM")
	for i := range want {
		t.Logf("vanilla %s; mcgo %s", want[i], got[i])
		if want[i] != got[i] {
			t.Errorf("bundle step%d differs", i)
		}
	}
}

func TestBundleUseParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("BundleUseV", "BundleUseM")}, nil)
	defer tg.cleanup()
	run := func(addr, name string) string {
		d := newClickDriverOn(t, addr, name, "minecraft:chest", 2, [][2]string{{"hotbar.0", `minecraft:bundle[bundle_contents=[{id:"minecraft:stone",count:4},{id:"minecraft:dirt",count:3},{id:"minecraft:sand",count:2}]]`}}, nil)
		d.close()
		d.click("sync-player", 9, 0, 0, true, true)
		d.sink.take()
		d.soundIDs = map[int32]int{}
		sendUseItem(t, d.pw, 0, 100)
		var packets []usePkt
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
			batch := d.sink.take()
			packets = append(packets, batch...)
			d.foldPkts(batch)
			if !strings.Contains(d.snap.p0[36], "bundle:") {
				break
			}
		}
		var release protocol.Buffer
		release.VarInt(5)
		release.Position(0, 0, 0)
		release.Byte(0)
		release.VarInt(101)
		_ = d.pw.WritePacket(server.IDPlayPlayerActionSB, release.Bytes())
		time.Sleep(200 * time.Millisecond)
		tail := d.sink.take()
		packets = append(packets, tail...)
		d.foldPkts(tail)
		spawns := 0
		for _, packet := range packets {
			if packet.id != server.IDPlaySpawnEntity {
				continue
			}
			r := protocol.NewPacketReader(packet.data)
			_, _ = r.VarInt()
			_, _ = r.UUID()
			typ, err := r.VarInt()
			if err == nil {
				if n, ok := server.EntityTypeName(typ); ok && n == "item" {
					spawns++
				}
			}
		}
		removeID, _ := server.SoundEventID("item.bundle.remove_one")
		dropID, _ := server.SoundEventID("item.bundle.drop_contents")
		result := fmt.Sprintf("held=%s spawns=%d remove=%d drop=%d", d.snap.p0[36], spawns, d.soundIDs[removeID+1], d.soundIDs[dropID+1])
		if strings.Contains(d.snap.p0[36], "bundle:") || spawns != 3 || d.soundIDs[removeID+1] != 0 || d.soundIDs[dropID+1] != 3 {
			t.Errorf("%s incomplete ejection: %s", name, result)
		}
		return result
	}
	want := run(tg.vanilla, "BundleUseV")
	got := run(tg.mcgo, "BundleUseM")
	t.Logf("vanilla %s; mcgo %s", want, got)
	if want != got {
		t.Fatal("bundle use differs")
	}
}
