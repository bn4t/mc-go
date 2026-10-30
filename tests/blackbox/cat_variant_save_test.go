package blackbox

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/server"
	"mc-go/internal/world"
)

// Use native-written cat records, with the default cat's two optional holder
// keys removed before each load. This distinguishes hydration from creation.
func TestCatVariantNativeSaveParity(t *testing.T) {
	jar := blackboxJar(t)
	dir := t.TempDir()
	port, ok := freePort()
	if !ok {
		t.Skip("loopback unavailable")
	}
	props := map[string]string{"level-name": "world", "spawn-protection": "0"}
	files := map[string]string{"ops.json": opsJSONFor("CatCodec")}
	_, say, wait := vanillaConsole(t, jar, port, props, files, dir)
	say("tick freeze")
	say(`summon minecraft:cat 0 -60 0 {Tags:["catExplicit"],NoAI:1b,NoGravity:1b,PersistenceRequired:1b,variant:"minecraft:all_black",sound_variant:"minecraft:royal"}`)
	say(`summon minecraft:cat 2 -60 0 {Tags:["catDefault"],NoAI:1b,NoGravity:1b,PersistenceRequired:1b}`)
	say("save-all flush")
	say("stop")
	if code := wait(); code != 0 {
		t.Fatal("native creator exit", code)
	}
	dim := world.DimensionDir(filepath.Join(dir, "world"), "minecraft:overworld")
	removeDefaultKeys := func() {
		entities, err := world.LoadEntityRegions(dim)
		if err != nil {
			t.Fatal(err)
		}
		if len(entities) != 2 {
			t.Fatal("native cat record count", len(entities))
		}
		defaults := 0
		for _, entity := range entities {
			t.Logf("native record variant=%v sound=%v tags=%v", entity["variant"], entity["sound_variant"], entity["Tags"])
			tags, _ := entity["Tags"].(nbt.List)
			isDefault := false
			for _, tag := range tags.Items {
				if tag == "catDefault" {
					isDefault = true
				}
			}
			if isDefault {
				delete(entity, "variant")
				delete(entity, "sound_variant")
				defaults++
			}
		}
		if defaults != 1 {
			t.Fatal("native default cat record", defaults)
		}
		if err := world.SaveEntityRegions(dim, map[[2]int32][]map[string]any{{0, 0}: entities}); err != nil {
			t.Fatal(err)
		}
	}
	check := func(t *testing.T, addr string) {
		conn, pr, pw := joinPlayBlackbox(t, addr, "CatCodec")
		defer conn.Close()
		awaitPlayReadyBlackbox(t, addr, conn, pr, pw)
		sink := newChatSinkResolvedTP(pr, pw)
		for _, cmd := range []string{
			"data get entity @e[type=minecraft:cat,tag=catDefault,limit=1] variant",
			"data get entity @e[type=minecraft:cat,tag=catDefault,limit=1] sound_variant",
			`execute if data entity @e[type=minecraft:cat,tag=catExplicit,limit=1] {variant:"minecraft:all_black",sound_variant:"minecraft:royal"} run say CATEXPLICIT`,
			`execute if data entity @e[type=minecraft:cat,tag=catDefault,limit=1] {variant:"minecraft:black",sound_variant:"minecraft:classic"} run say CATDEFAULT`,
			"say CATCODECEND",
		} {
			sendCommand(t, pw, cmd)
		}
		lines := waitForExactSayBB(t, sink, "CATCODECEND", 15*time.Second)
		for _, marker := range []string{"CATEXPLICIT", "CATDEFAULT"} {
			if !structureSaidBB(lines, marker) {
				t.Errorf("native load missing %s: %v", marker, lines)
			}
		}

	}
	removeDefaultKeys()
	port, ok = freePort()
	if !ok {
		t.Skip("loopback unavailable")
	}
	addr, say2, wait2 := vanillaConsole(t, jar, port, props, files, dir)
	t.Run("native_reload", func(t *testing.T) { check(t, addr) })
	say2("save-all flush")
	say2("stop")
	if code := wait2(); code != 0 {
		t.Fatal("native loader exit", code)
	}
	removeDefaultKeys()
	admin := t.TempDir()
	if err := os.WriteFile(filepath.Join(admin, "ops.json"), []byte(opsJSONFor("CatCodec")), 0600); err != nil {
		t.Fatal(err)
	}
	port, ok = freePort()
	if !ok {
		t.Skip("loopback unavailable")
	}
	addr = startMcGoCfg(t, port, func(s *server.Server) {
		s.WorldDir = filepath.Join(dir, "world")
		s.ServerDir = admin
		s.OpsRequired = true
		s.SetFrozen(true)
		if err := s.LoadAdminFiles(); err != nil {
			t.Error(err)
		}
	})
	t.Run("go_native_reload", func(t *testing.T) { check(t, addr) })
}
