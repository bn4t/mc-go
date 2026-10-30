//go:build blackbox

package blackbox

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/server"
	"mc-go/internal/world"
)

type savedTickTargetBB struct {
	domain          string
	x, z            int32
	owner           string
	delay, priority int32
}

func savedTickTargetsBB() []savedTickTargetBB {
	return []savedTickTargetBB{
		{"block_ticks", 4, 4, "stone_button", 17, 0}, {"block_ticks", 8, 4, "stone_button", 17, 0}, {"block_ticks", 12, 4, "repeater", 5, -1},
		{"fluid_ticks", 4, 12, "water", 2, 0}, {"fluid_ticks", 8, 12, "flowing_water", 2, 0},
		{"fluid_ticks", 12, 12, "lava", 27, 0}, {"fluid_ticks", 8, 8, "flowing_lava", 27, 0},
	}
}
func savedTickRegionBB(dir string) string {
	return filepath.Join(world.DimensionDir(filepath.Join(dir, "world"), "minecraft:overworld"), "region", "r.0.0.mca")
}
func awaitSavedTickTargetsBB(t *testing.T, path string) {
	t.Helper()
	var last string
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		chunks, err := world.ReadRegionFile(path)
		if err != nil {
			last = err.Error()
			time.Sleep(10 * time.Millisecond)
			continue
		}
		chunk := chunks[[2]int32{}]
		good := true
		for _, target := range savedTickTargetsBB() {
			want := map[string]any{"i": "minecraft:" + target.owner, "x": target.x, "y": int32(-60), "z": target.z, "t": target.delay, "p": target.priority}
			list, _ := chunk[target.domain].(nbt.List)
			found := false
			for _, v := range list.Items {
				if reflect.DeepEqual(v, want) {
					found = true
					break
				}
			}
			if !found {
				good = false
				last = fmt.Sprintf("want%#v in %s, got%#v", want, target.domain, list)
				break
			}
		}
		if good {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("fresh typed save records absent: %s", last)
}
func scheduledSaveJoinBB(t *testing.T, kind, addr, name string) (*scheduledProbeBB, func()) {
	c, pr, pw := joinPlayBlackbox(t, addr, name)
	drainToJoinBlackbox(t, kind, pr)
	awaitPlayReadyBlackbox(t, kind, c, pr, pw)
	p := &scheduledProbeBB{t: t, pw: pw, sink: newChatSinkTP(pr, pw)}
	p.command("tick freeze")
	return p, func() { _ = c.Close() }
}
func armScheduledSaveBB(p *scheduledProbeBB) {
	for _, x := range []int{4, 8} {
		p.command(fmt.Sprintf("setblock %d -60 4 stone_button[face=floor]", x))
		p.command(fmt.Sprintf("tp @s %g -60 5.5 180 56", float64(x)+.5))
		p.moveReady(float64(x)+.6, -60, 5.5)
		sendUseItemOnCursor(p.t, p.pw, int32(x), -60, 4, 1, .5, .125, .5, int32(x))
		p.await(fmt.Sprintf("if block %d -60 4 stone_button[powered=true]", x), fmt.Sprintf("save_armed_%d", x))
	}
	p.command("setblock 4 -60 4 oak_button[face=floor,powered=true] strict")
	p.command("setblock 12 -60 4 repeater[facing=north,delay=4]")
	p.command("setblock 12 -60 3 redstone_block")
	p.command("setblock 4 -60 12 water")
	p.command("setblock 8 -60 12 water[level=1]")
	p.command("setblock 12 -60 12 lava")
	p.command("setblock 8 -60 8 lava[level=1]")
	p.step(3)
	p.command("save-all flush")
}
func copyScheduledSaveBB(t *testing.T, source, dest string) {
	t.Helper()
	for _, relative := range []string{"level.dat", "data/minecraft/world_gen_settings.dat", "data/minecraft/world_clocks.dat", "data/minecraft/game_rules.dat", "dimensions/minecraft/overworld/region/r.0.0.mca"} {
		raw, err := os.ReadFile(filepath.Join(source, "world", relative))
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dest, "world", relative)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0644); err != nil {
			t.Fatal(err)
		}
	}
}

// Extend one copied delay only after exact original records were witnessed.
// This makes native readback robust to startup ticks without a load-function
// permission override. The writer's original remaining17 remains mandatory.
func extendCopiedButtonTickBB(t *testing.T, path string) {
	t.Helper()
	chunks, err := world.ReadRegionFile(path)
	if err != nil {
		t.Fatal(err)
	}
	list, _ := chunks[[2]int32{}]["block_ticks"].(nbt.List)
	found := false
	for _, v := range list.Items {
		m, _ := v.(map[string]any)
		if m["i"] == "minecraft:stone_button" && m["x"] == int32(8) && m["y"] == int32(-60) && m["z"] == int32(4) && m["t"] == int32(17) {
			m["t"] = int32(1000)
			found = true
		}
	}
	if !found {
		t.Fatal("copied original button tick absent")
	}
	// Region format: two header sectors, then length/compression-prefixed chunks.
	keys := make([][2]int32, 0, len(chunks))
	for k := range chunks {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	out := make([]byte, 8192)
	for _, key := range keys {
		if key[0] < 0 || key[0] > 31 || key[1] < 0 || key[1] > 31 {
			t.Fatal("invalid local chunk", key)
		}
		var raw, compressed bytes.Buffer
		if err := nbt.WriteNamed(&raw, "", chunks[key]); err != nil {
			t.Fatal(err)
		}
		zw := zlib.NewWriter(&compressed)
		if _, err := zw.Write(raw.Bytes()); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		size := compressed.Len() + 5
		sectors := (size + 4095) / 4096
		if sectors > 255 {
			t.Fatal("fixture chunk too large")
		}
		offset := len(out) / 4096
		slot := int(key[0] + key[1]*32)
		binary.BigEndian.PutUint32(out[slot*4:], uint32(offset<<8|sectors))
		data := make([]byte, sectors*4096)
		binary.BigEndian.PutUint32(data, uint32(compressed.Len()+1))
		data[4] = 2
		copy(data[5:], compressed.Bytes())
		out = append(out, data...)
	}
	if err := os.WriteFile(path, out, 0644); err != nil {
		t.Fatal(err)
	}
}
func awaitNativeButtonRemainingBB(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var last any
	for time.Now().Before(deadline) {
		chunks, err := world.ReadRegionFile(path)
		if err == nil {
			list, _ := chunks[[2]int32{}]["block_ticks"].(nbt.List)
			for _, v := range list.Items {
				m, _ := v.(map[string]any)
				if m["i"] != "minecraft:stone_button" || m["x"] != int32(8) || m["y"] != int32(-60) || m["z"] != int32(4) {
					continue
				}
				last = m
				if remaining, ok := m["t"].(int32); ok && remaining > 1 && remaining < 1000 && m["p"] == int32(0) {
					return int(remaining)
				}
			}
		} else {
			last = err
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("native did not preserve loaded pending button with fresh remaining delay: %v", last)
	return 0
}

func TestScheduledTickChunkSaveParity(t *testing.T) {
	jar := blackboxJar(t)
	for _, kind := range []string{"vanilla", "mc-go"} {
		t.Run(kind, func(t *testing.T) {
			source := t.TempDir()
			port, ok := freePort()
			if !ok {
				t.Skip("loopback unavailable")
			}
			var addr string
			var stop func()
			if kind == "vanilla" {
				addr, stop = startVanillaInDir(t, jar, port, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor("ScheduledSave")}, source)
			} else {
				if err := os.WriteFile(filepath.Join(source, "ops.json"), []byte(opsJSONFor("ScheduledSave")), 0644); err != nil {
					t.Fatal(err)
				}
				var runtime *server.Server
				addr = startMcGoCfg(t, port, func(s *server.Server) {
					runtime = s
					s.GameMode = server.GameModeCreative
					s.WorldDir = filepath.Join(source, "world")
					s.ServerDir = source
				})
				stop = runtime.Stop
			}
			defer stop()
			p, closeClient := scheduledSaveJoinBB(t, kind, addr, "ScheduledSave")
			defer closeClient()
			armScheduledSaveBB(p)
			awaitSavedTickTargetsBB(t, savedTickRegionBB(source))
			restart := t.TempDir()
			copyScheduledSaveBB(t, source, restart)
			extendCopiedButtonTickBB(t, savedTickRegionBB(restart))
			closeClient()
			stop()
			// Every source save is read by the real native server, including Go output.
			port, ok = freePort()
			if !ok {
				t.Skip("loopback unavailable")
			}
			addr, stopNative := startVanillaInDir(t, jar, port, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor("ScheduledRead")}, restart)
			defer stopNative()
			read, closeRead := scheduledSaveJoinBB(t, "native-readback", addr, "ScheduledRead")
			defer closeRead()
			read.expect("if block 8 -60 4 stone_button[powered=true]", "native_readback_button_pending")
			read.step(1)
			read.command("save-all flush")
			remaining := awaitNativeButtonRemainingBB(t, savedTickRegionBB(restart))
			read.command("tick rate 1000")
			read.step(remaining - 1)
			read.expect("if block 8 -60 4 stone_button[powered=true]", "native_readback_before_deadline")
			read.step(1)
			read.expect("if block 8 -60 4 stone_button[powered=false]", "native_readback_deadline")
			read.expect("if block 4 -60 4 oak_button[powered=true]", "native_readback_stale_owner_ignored")
			read.expect("if block 4 -60 13 water", "native_readback_fluid_tick_loaded")
		})
	}
}
