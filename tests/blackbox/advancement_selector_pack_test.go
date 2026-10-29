//go:build blackbox

package blackbox

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestCustomAdvancementSelectorParity(t *testing.T) {
	const name = "AdvSelector"
	bodies := map[string]string{
		"pack.mcmeta":                            `{"pack":{"description":"server-owned selector progress","min_format":107,"max_format":107}}`,
		"data/selectorpin/advancement/step.json": `{"criteria":{"a":{"trigger":"minecraft:impossible"},"b":{"trigger":"minecraft:impossible"}},"requirements":[["a"],["b"]]}`,
	}
	directory := t.TempDir()
	files := map[string]string{"ops.json": opsJSONFor(name)}
	for path, body := range bodies {
		rel := "datapacks/selectors/" + path
		files["world/"+rel] = body
		local := filepath.Join(directory, rel)
		if err := os.MkdirAll(filepath.Dir(local), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(local, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	tg := requireBlackboxFiles(t, nil, files, func(s *server.Server) { s.WorldDir = directory })
	defer tg.cleanup()
	run := func(addr string) []bool {
		d := newMenuLifecycleObserver(t, addr, name)
		serial := 0
		barrier := func() []string {
			serial++
			marker := fmt.Sprintf("SELECTOR_END_%d", serial)
			sendCommand(t, d.pw, "say "+marker)
			var lines []string
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				for _, p := range d.sink.take() {
					lines = append(lines, fmt.Sprintf("0x%x:%s", p.id, p.data))
				}
				if structureSaidBB(lines, marker) {
					return lines
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatal("selector progress barrier missing")
			return nil
		}
		var out []bool
		for index, c := range []struct{ command, condition string }{
			{"", "if entity @s[advancements={selectorpin:step=false}]"},
			{"", "if entity @s[advancements={selectorpin:step={}}]"},
			{"", "unless entity @s[advancements={selectorpin:step={missing=false}}]"},
			{"", "unless entity @s[advancements={selectorpin:missing=false}]"},
			{"advancement grant @s only selectorpin:step a", "if entity @s[advancements={selectorpin:step={a=true,b=false}}]"},
			{"", "if entity @s[advancements={selectorpin:step=false}]"},
			{"advancement grant @s only selectorpin:step b", "if entity @s[advancements={selectorpin:step=true}]"},
			{"", "if entity @s[advancements={selectorpin:step={a=true,b=true}}]"},
			{"advancement revoke @s only selectorpin:step a", "if entity @s[advancements={selectorpin:step={a=false,b=true}}]"},
			{"", "if entity @s[advancements={selectorpin:step=false}]"},
			{"", "if entity @s[advancements={minecraft:recipes/decorations/crafting_table=true}]"},
			{"tag @s add selector_tag", `if entity @s[nbt={Tags:["selector_tag"]}]`},
			{"", `unless entity @s[nbt={Tags:["selector_tag","selector_tag"]}]`},
			{"", `unless entity @s[nbt={Tags:[]}]`},
		} {
			if c.command != "" {
				sendCommand(t, d.pw, c.command)
				barrier()
			}
			marker := fmt.Sprintf("SELECTOR_PASS_%d", index)
			sendCommand(t, d.pw, "execute "+c.condition+" run say "+marker)
			ok := structureSaidBB(barrier(), marker)
			if !ok {
				t.Errorf("%s selector control%d failed%s", addr, index, c.condition)
			}
			out = append(out, ok)
		}
		return out
	}
	v, g := run(tg.vanilla), run(tg.mcgo)
	if !reflect.DeepEqual(v, g) {
		t.Errorf("custom selector checks differ vanilla%v mcgo%v", v, g)
	}
}
