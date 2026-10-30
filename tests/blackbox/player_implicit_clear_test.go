//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/server"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPlayerImplicitSelfClearBindingParity(t *testing.T) {
	files := map[string]string{"ops.json": opsJSONFor("BindCaller", "BindOther")}
	directory := t.TempDir()
	for path, body := range map[string]string{
		"pack.mcmeta":                           `{"pack":{"description":"player source binding","min_format":107,"max_format":107}}`,
		"data/bind/function/neutral.mcfunction": "execute as @a[name=BindOther] run clear\n",
	} {
		relative := "datapacks/bind/" + path
		files["world/"+relative] = body
		local := filepath.Join(directory, relative)
		if err := os.MkdirAll(filepath.Dir(local), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(local, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	targets := requireBlackboxFiles(t, nil, files, func(s *server.Server) { s.WorldDir = directory })
	defer targets.cleanup()
	for _, target := range []struct{ name, addr string }{{"vanilla", targets.vanilla}, {"mc-go", targets.mcgo}} {
		t.Run(target.name, func(t *testing.T) {
			caller, reader, writer := joinPlayBlackbox(t, target.addr, "BindCaller")
			defer caller.Close()
			drainToJoinBlackbox(t, target.name, reader)
			awaitPlayReadyBlackbox(t, target.name, caller, reader, writer)
			sink := newChatSinkTP(reader, writer)
			other, otherReader, otherWriter := joinPlayBlackbox(t, target.addr, "BindOther")
			defer other.Close()
			drainToJoinBlackbox(t, "other", otherReader)
			awaitPlayReadyBlackbox(t, "other", other, otherReader, otherWriter)
			_ = newChatSinkTP(otherReader, otherWriter)
			serial := 0
			command := func(text string) []string {
				serial++
				sendCommand(t, writer, text)
				marker := fmt.Sprintf("PLAYER_BIND_END_%d", serial)
				sendCommand(t, writer, "say "+marker)
				return waitForExactSayBB(t, sink, marker, 10*time.Second)
			}
			score := func(holder string, want int) {
				t.Helper()
				marker := fmt.Sprintf("PLAYER_BIND_EXPECT_%d", serial)
				if !structureSaidBB(command(fmt.Sprintf("execute if score %s bind matches %d run say %s", holder, want, marker)), marker) {
					t.Errorf("%s expected%d", holder, want)
				}
			}
			inventory := func(player string, want int) {
				t.Helper()
				command("scoreboard players set observed bind 999")
				command("execute store result score observed bind run clear " + player + " minecraft:diamond 0")
				score("observed", want)
			}
			command("tick freeze")
			command("scoreboard objectives add bind dummy")
			command("give BindCaller minecraft:diamond 1")
			command("give BindOther minecraft:diamond 2")
			command("summon minecraft:armor_stand 8 -58 8 {Tags:[\"bindstand\"],NoGravity:1b,Marker:1b}")
			command("scoreboard players set blocked bind 999")
			command("execute as @e[type=minecraft:armor_stand,tag=bindstand,limit=1] store success score blocked bind run clear")
			score("blocked", 0)
			inventory("BindCaller", 1)
			inventory("BindOther", 2)
			command("scoreboard players set removed bind 999")
			command("execute as BindOther store result score removed bind run clear")
			score("removed", 2)
			inventory("BindCaller", 1)
			inventory("BindOther", 0)
			command("give BindOther minecraft:diamond 2")
			command("schedule function bind:neutral 1t")
			command("tick step 1")
			inventory("BindCaller", 1)
			inventory("BindOther", 0)
		})
	}
}
