//go:build blackbox

package blackbox

import (
	"fmt"
	"net"
	"regexp"
	"strings"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestLocateParity diffs /locate replies between vanilla and mc-go:
// biome search hits the flat the_void world's uniform biome (vanilla's
// findClosestBiome3d first sample is the source block pos with a
// clamped Y), structure/poi always miss, and the resource/resource_or_tag
// argument errors pin cursor positions for bad-charset, malformed-id,
// tag, and leftover-token cases.
func TestLocateParity(t *testing.T) {
	numRe := regexp.MustCompile(`d\(-?[0-9.e+]+\)|f\(-?[0-9.e+]+\)|i\(-?[0-9]+\)|b\([0-9]+\)|l\(-?[0-9]+\)|"-?[0-9]+(?:\.[0-9]+)?"`)

	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": `[{"uuid":"14075747-e146-3809-a161-83abe512191e","name":"P","level":2,"bypassesPlayerLimit":false}]`},
		func(s *server.Server) {
			s.OpsRequired = true
			s.SetOpLevel("P", 2)
		})
	defer targets.cleanup()

	run := func(addr string) [][]string {
		var c net.Conn
		var pw *protocol.Writer
		var sink *chatSink
		rejoin := func() {
			if c != nil {
				c.Close()
			}
			var pr *protocol.Reader
			c, pr, pw = joinPlayQuiet(t, addr, "P")
			_ = c.SetDeadline(time.Time{})
			sink = newChatSink(pr, pw)
			sink.cap = 1 << 20
			time.Sleep(2 * time.Second)
		}
		rejoin()
		defer c.Close()

		send := func(cmd string) error {
			var b protocol.Buffer
			b.String(cmd)
			return pw.WritePacket(server.IDPlayChatCommandSB, b.Bytes())
		}
		collect := func(marker string) []string {
			var evs []string
			end := time.Now().Add(5 * time.Second)
			quietUntil := time.Now()
			got := false
			for time.Now().Before(end) {
				for _, line := range sink.take() {
					if strings.Contains(line, marker) {
						got = true
						continue
					}
					var ev string
					if tree, ok := componentTree(line, true); ok {
						ev = tree
					} else if strings.HasPrefix(line, "0x79:") {
						i2 := strings.Index(line, ":")
						if root, err := nbt.ReadUnnamed(strings.NewReader(line[i2+1:])); err == nil {
							ev = renderNBT(root, true)
						}
					}
					if ev != "" {
						evs = append(evs, numRe.ReplaceAllString(maskUUID(ev), "N"))
						quietUntil = time.Now().Add(150 * time.Millisecond)
					}
				}
				if got && time.Now().After(quietUntil) {
					break
				}
				time.Sleep(30 * time.Millisecond)
			}
			return evs
		}

		// Each slot is sent atomically and re-sent after a vanilla spam
		// kick so tp+locate pairs never straddle a rejoin.
		cases := [][]string{
			{"tp @s 8.5 -60 3.2", "locate"},
			{"locate biome"}, {"locate structure"}, {"locate poi"},
			{"locate foo"}, {"locate biome minecraft:the_void"},
			{"locate biome the_void"}, {"locate biome minecraft:plains"},
			{"locate structure village"}, {"locate structure stronghold"},
			{"locate structure village_plains"}, {"locate structure the_void"},
			{"locate structure minecraft:stronghold"},
			{"locate poi minecraft:bee_nest"}, {"locate poi minecraft:nether_portal"},
			{"locate poi minecraft:beehive"}, {"locate poi minecraft:bee_home"},
			{"locate biome BADNAME"}, {"locate biome badname"},
			{"locate biome minecraft:BADNAME"}, {"locate biome minecraft:the_Void"},
			{"locate biome minecraft:"}, {"locate biome minecraft:the_void:"},
			{"locate biome minecraft:the_void extra"},
			{"locate biome minecraft:the_void extra extra2"},
			{"locate structure village_plains extra extra2"},
			{"locate structure minecraft:stronghold extra"},
			{"locate poi minecraft:nether_portal extra"},
			{"locate structure BADNAME"}, {"locate structure BAD.NAME"},
			{"locate poi BADNAME"},
			{"locate structure #minecraft:village"},
			{"locate structure #minecraft:eye_of_ender_located"},
			{"locate structure #minecraft:bad_tag"},
			{"locate structure #minecraft:foo"},
			{"locate poi #minecraft:acquirable_job_site"},
			{"locate poi #minecraft:village"}, {"locate poi #minecraft:bad_tag"},
			{"locate biome #minecraft:the_void"}, {"locate biome #minecraft:bad_tag"},
			{"locate biome #minecraft:is_overworld"},
			{"locate biome #minecraft:without_wandering_trader_spawns"},
			{"locate biome minecraft:the_void#x"},
			{"locate structure mine_craft2:stronghold"},
			{"tp @s 8.9 -60 3.9", "locate biome the_void"},
			{"tp @s -33.5 -60 7.7", "locate biome minecraft:the_void"},
			{"tp @s 0 -3000 0", "locate biome the_void"},
			{"tp @s 1000.3 -60 -500.9", "locate biome the_void"},
		}
		var out [][]string
		for i, slot := range cases {
			marker := fmt.Sprintf("ZZL%d", i)
			var evs []string
			for attempt := 0; attempt < 4; attempt++ {
				sink.take()
				ok := true
				for _, cmd := range slot {
					if err := send(cmd); err != nil {
						ok = false
						break
					}
					time.Sleep(150 * time.Millisecond)
				}
				if !ok {
					rejoin()
					continue
				}
				if err := send("me " + marker); err != nil {
					rejoin()
					continue
				}
				evs = collect(marker)
				break
			}
			out = append(out, evs)
		}
		return out
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	if len(want) != len(got) {
		t.Fatalf("case count %d vs %d", len(want), len(got))
	}
	for i := range want {
		if len(want[i]) != len(got[i]) {
			t.Errorf("case %d: vanilla %v\n  mc-go %v", i, want[i], got[i])
			continue
		}
		for j := range want[i] {
			if want[i][j] != got[i][j] {
				t.Errorf("case %d event %d:\n  vanilla %s\n  mc-go   %s", i, j, want[i][j], got[i][j])
			}
		}
	}
}
