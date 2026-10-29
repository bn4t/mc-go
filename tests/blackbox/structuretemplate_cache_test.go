package blackbox

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/server"
)

// Structure assertions accept actual /say packets, not syntax-error context
// that can echo the requested marker in a System Chat packet.
func structureSaidBB(lines []string, marker string) bool {
	for _, line := range lines {
		if !strings.HasPrefix(line, "0x21:") {
			continue
		}
		value, err := nbt.ReadUnnamed(strings.NewReader(strings.TrimPrefix(line, "0x21:")))
		if err != nil {
			continue
		}
		switch text := value.(type) {
		case string:
			if text == marker {
				return true
			}
		case map[string]any:
			if text["text"] == marker {
				return true
			}
		}
	}
	return false
}

func TestStructureSayMarkerRejectsErrorContext(t *testing.T) {
	marker := "STRUCTURE_SAY_ONLY"
	packet := func(id int32, content any, sender string) string {
		var body bytes.Buffer
		if err := nbt.WriteUnnamed(&body, content); err != nil {
			t.Fatal(err)
		}
		if id == 0x21 {
			body.WriteByte(1) // Registry holder reference.
			if err := nbt.WriteUnnamed(&body, sender); err != nil {
				t.Fatal(err)
			}
		}
		body.WriteByte(0) // Optional target / System Chat overlay flag.
		return fmt.Sprintf("%#x:%s", id, body.String())
	}
	errorEcho := packet(server.IDPlaySystemChatCB, map[string]any{"text": "", "extra": nbt.List{Elem: nbt.TagCompound, Items: []any{map[string]any{"text": "... run say " + marker, "underlined": int8(1)}}}}, "")
	if !sawMarker([]string{errorEcho}, marker) {
		t.Fatal("error fixture must contain the echoed command marker")
	}
	for _, tc := range []struct {
		name, line string
		want       bool
	}{
		{"literal", packet(0x21, marker, "Server"), true},
		{"compound", packet(0x21, map[string]any{"text": marker}, "Server"), true},
		{"syntax error context", errorEcho, false},
		{"sender contains marker", packet(0x21, "different content", marker), false},
		{"different message", packet(0x21, marker+"_suffix", "Server"), false},
		{"malformed", "0x21:" + marker, false},
	} {
		if got := structureSaidBB([]string{tc.line}, marker); got != tc.want {
			t.Errorf("%s got%v want%v", tc.name, got, tc.want)
		}
	}
}

func structureCacheFileBB(t *testing.T, path, state string) {
	t.Helper()
	ints := func(v ...any) nbt.List { return nbt.List{Elem: nbt.TagInt, Items: v} }
	value := map[string]any{"DataVersion": int32(4903), "size": ints(int32(1), int32(1), int32(1)), "palette": nbt.List{Elem: nbt.TagCompound, Items: []any{map[string]any{"Name": "minecraft:" + state}}}, "blocks": nbt.List{Elem: nbt.TagCompound, Items: []any{map[string]any{"pos": ints(int32(0), int32(0), int32(0)), "state": int32(0)}}}, "entities": nbt.List{Elem: nbt.TagEnd}}
	var raw bytes.Buffer
	zw := gzip.NewWriter(&raw)
	if err := nbt.WriteNamed(zw, "", value); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestStructureTemplateCacheReloadParity(t *testing.T) {
	mcDir := t.TempDir()
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor("StructureCache")}, func(s *server.Server) { s.GameMode = server.GameModeCreative; s.WorldDir = mcDir })
	defer tg.cleanup()
	for _, tc := range []struct{ tag, addr, dir string }{{"vanilla", tg.vanilla, filepath.Join(vanillaDirForDebug, "world")}, {"mc-go", tg.mcgo, mcDir}} {
		t.Run(tc.tag, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, "StructureCache")
			defer c.Close()
			drainToJoinBlackbox(t, tc.tag, pr)
			awaitPlayReadyBlackbox(t, tc.tag, c, pr, pw)
			sink := newChatSinkTP(pr, pw)
			serial := 0
			barrier := func() []string {
				serial++
				marker := fmt.Sprintf("STRUCTURE_CACHE_END_%d", serial)
				sendCommand(t, pw, "say "+marker)
				return waitForMarker(t, sink, marker, 10*time.Second)
			}
			check := func(state string) {
				serial++
				marker := fmt.Sprintf("STRUCTURE_CACHE_OK_%d", serial)
				sendCommand(t, pw, "execute if block 4 -59 4 "+state+" run say "+marker)
				if !structureSaidBB(barrier(), marker) {
					t.Errorf("expected target%s", state)
				}
			}
			loadNamed := func(name string, size [3]byte, want string) map[string]any {
				sink.take()
				writeStructureEditBB(t, pw, [3]int32{4, -60, 4}, 2, 1, name, [3]byte{0, 1, 0}, size, 0, 0, "", 1, 17, 1)
				deadline := time.Now().Add(10 * time.Second)
				var feedback string
				var tag map[string]any
				for time.Now().Before(deadline) {
					batch := sink.take()
					for _, update := range structureBEUpdatesBB(t, batch) {
						if update.pos == [3]int32{4, -60, 4} && update.tag["name"] == name && update.tag["mode"] == "LOAD" {
							tag = update.tag
						}
					}
					for _, line := range batch {
						for _, kind := range []string{"load_not_found", "load_prepare", "load_success"} {
							if strings.Contains(line, "structure_block."+kind) {
								feedback = kind
							}
						}
					}
					if feedback != "" && tag != nil {
						if feedback != want {
							t.Errorf("load feedback%s want%s", feedback, want)
						}
						return tag
					}
					time.Sleep(10 * time.Millisecond)
				}
				t.Fatal("missing load feedback or applied BE tag")
				return nil
			}
			load := func(want string) { loadNamed("test:cache_late", [3]byte{1, 1, 1}, want) }
			reload := func() {
				// Play update_tags (official776 packet0x86) follows resource apply.
				// Count it even though its large payload is excluded from chat capture.
				sink.mu.Lock()
				before := sink.ids[0x86]
				sink.mu.Unlock()
				sendCommand(t, pw, "reload")
				deadline := time.Now().Add(30 * time.Second)
				for time.Now().Before(deadline) {
					sink.mu.Lock()
					after := sink.ids[0x86]
					sink.mu.Unlock()
					if after > before {
						return
					}
					time.Sleep(10 * time.Millisecond)
				}
				t.Fatal("reload never delivered resource update_tags")
			}
			sendCommand(t, pw, "tick freeze")
			sendCommand(t, pw, "setblock 4 -60 4 structure_block[mode=load]")
			sendCommand(t, pw, "setblock 4 -59 4 air")
			barrier()
			load("load_not_found")
			path := filepath.Join(tc.dir, "generated/test/structure/cache_late.nbt")
			structureCacheFileBB(t, path, "gold_block")
			load("load_not_found")
			check("air") // A missing lookup is cached too.
			reload()
			load("load_success")
			check("gold_block")
			structureCacheFileBB(t, path, "diamond_block")
			sendCommand(t, pw, "setblock 4 -59 4 air")
			barrier()
			load("load_success")
			check("gold_block") // Positive cache remains until reload.
			reload()
			load("load_success")
			check("diamond_block")
			// A malformed generated file does not mask the next source.
			// Jar NBT facts: igloo/top size7x5x8, snow at relative(0,0,3).
			var broken bytes.Buffer
			zw := gzip.NewWriter(&broken)
			_, _ = zw.Write([]byte{1, 2, 3})
			if err := zw.Close(); err != nil {
				t.Fatal(err)
			}
			corrupt := filepath.Join(tc.dir, "generated/minecraft/structure/igloo/top.nbt")
			if err := os.MkdirAll(filepath.Dir(corrupt), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(corrupt, broken.Bytes(), 0644); err != nil {
				t.Fatal(err)
			}
			loadNamed("minecraft:igloo/top", [3]byte{7, 5, 8}, "load_success")
			serial++
			marker := fmt.Sprintf("STRUCTURE_FALLBACK_OK_%d", serial)
			sendCommand(t, pw, "execute if block 4 -59 7 snow_block run say "+marker)
			if !structureSaidBB(barrier(), marker) {
				t.Error("malformed generated source masked builtin snow block")
			}
			for _, kind := range []string{"valid", "crc", "footer", "body"} {
				structureCacheFileBB(t, corrupt, "gold_block")
				raw, err := os.ReadFile(corrupt)
				if err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "crc":
					raw[len(raw)-8] ^= 1
				case "footer":
					raw = raw[:len(raw)-8]
				case "body":
					raw = raw[:len(raw)/2]
				}
				if err := os.WriteFile(corrupt, raw, 0644); err != nil {
					t.Fatal(err)
				}
				sendCommand(t, pw, "fill 4 -59 4 10 -55 11 air")
				barrier()
				reload()
				tag := loadNamed("minecraft:igloo/top", [3]byte{}, "load_prepare")
				match := func(condition string) bool {
					serial++
					marker := fmt.Sprintf("STRUCTURE_GZIP_%d", serial)
					sendCommand(t, pw, "execute "+condition+" run say "+marker)
					return structureSaidBB(barrier(), marker)
				}
				// Inspect the actual applied tag rather than relying on the
				// command predicate implementation to verify its own state.
				generated := tag["sizeX"] == int32(1) && tag["sizeY"] == int32(1) && tag["sizeZ"] == int32(1)
				builtin := tag["sizeX"] == int32(7) && tag["sizeY"] == int32(5) && tag["sizeZ"] == int32(8)
				t.Logf("gzip%s generated%v builtin%v", kind, generated, builtin)
				if generated == builtin {
					t.Fatalf("gzip%s did not resolve exactly one source", kind)
				}
				wantGenerated := kind != "body" // Measured26.2 root-read boundary.
				if generated != wantGenerated || builtin == wantGenerated {
					t.Errorf("gzip%s generated%v/builtin%v want generated%v", kind, generated, builtin, wantGenerated)
				}
				if generated {
					loadNamed("minecraft:igloo/top", [3]byte{1, 1, 1}, "load_success")
					check("gold_block")
				} else {
					loadNamed("minecraft:igloo/top", [3]byte{7, 5, 8}, "load_success")
					if !match("if block 4 -59 7 snow_block") {
						t.Errorf("gzip%s missing builtin snow", kind)
					}
				}
			}

		})
	}
}
