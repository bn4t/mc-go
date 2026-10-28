package blackbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mc-go/conformance"
)

// Real duplicate logins exercise transport retirement for both public profiles.
// Late teardown ordering is controlled separately by the deterministic unit pin.
func TestNativeDisconnectReplacementRestartWire(t *testing.T) {
	for _, number := range []int32{775, 776} {
		t.Run(fmt.Sprint(number), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			source := filepath.Join(t.TempDir(), "source")
			s, addr, stop := nativeSavePublicServer(t, source)
			name := fmt.Sprintf("NativeSwap%d", number)
			old := compactSaveOpen(t, ctx, addr, number, name)
			mixedBarrier(t, old, "item replace entity @s hotbar.0 with minecraft:oak_planks 1")
			current := compactSaveOpen(t, ctx, addr, number, name)
			mixedBarrier(t, current, "item replace entity @s hotbar.0 with minecraft:oak_planks 7")
			mixedBarrier(t, current, "advancement grant @s only minecraft:story/root")
			if err := s.SaveWorld(); err != nil {
				t.Fatal(err)
			}
			copied := filepath.Join(t.TempDir(), "copied")
			if err := os.CopyFS(copied, os.DirFS(source)); err != nil {
				t.Fatal(err)
			}
			// Inspect completed metadata rather than infer it from the later shutdown.
			files, err := filepath.Glob(filepath.Join(copied, "players", "advancements", "*.json"))
			if err != nil || len(files) != 1 {
				t.Fatalf("advancement files=%v,%v", files, err)
			}
			raw, err := os.ReadFile(files[0])
			if err != nil {
				t.Fatal(err)
			}
			var advancements map[string]json.RawMessage
			if err := json.Unmarshal(raw, &advancements); err != nil {
				t.Fatal(err)
			}
			var root struct {
				Done bool `json:"done"`
			}
			if err := json.Unmarshal(advancements["minecraft:story/root"], &root); err != nil {
				t.Fatal(err)
			}
			if !root.Done {
				t.Fatal("successor advancement absent from completed native save")
			}
			old.Close()
			current.Close()
			stop()
			_, restartAddr, _ := nativeSavePublicServer(t, copied)
			restored := compactSaveOpen(t, ctx, restartAddr, number, name)
			facts := serverDataItems(t, number)
			inventory := false
			for _, packet := range restored.InitialPackets() {
				if packet.ID != 0x12 {
					continue
				}
				r := bytes.NewReader(packet.Data)
				id, _ := mixedReadVI(r)
				if id != 0 {
					continue
				}
				serverDataContent(t, packet.Data, 46, map[int][]byte{36: serverDataPlain(facts.Items["oak_planks"], 7)})
				inventory = true
			}
			if !inventory {
				t.Fatal("restarted successor inventory not received")
			}
			if err := coldJoinToken(ctx, restored, "native-replacement-restored", func(_ conformance.Packet) error { return nil }); err != nil {
				t.Fatal(err)
			}
		})
	}
}
