//go:build blackbox

package blackbox

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"mc-go/internal/data"
	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

type displayedAdvancementProbe struct {
	Parent             string
	Display            bool
	Title, Description any
	Icon               string
	Frame, Flags       int32
	Background         string
	X, Y               float32
	Raw                []byte
}

func TestDisplayedAdvancementPackParity(t *testing.T) {
	const name = "AdvDisplayPin"
	type node struct {
		id, parent string
		displayed  bool
	}
	nodes := []node{
		{"00_root", "", true}, {"01_chain", "00_root", true}, {"02_chain", "01_chain", true},
		{"10_root", "", true}, {"11_left", "10_root", true}, {"12_mid", "10_root", true}, {"13_right", "10_root", true}, {"14_lleaf", "11_left", true}, {"15_lleaf", "11_left", true}, {"16_rleaf", "13_right", true}, {"17_rleaf", "13_right", true},
		{"20_root", "", true}, {"21_left", "20_root", true}, {"22_mid", "20_root", true}, {"23_right", "20_root", true}, {"24_chain", "21_left", true}, {"25_leaf", "24_chain", true}, {"26_leaf", "23_right", true}, {"27_leaf", "23_right", true},
		{"30_root", "", false}, {"31_child", "30_root", true}, {"32_child", "31_child", true},
		{"40_root", "", true}, {"41_bridge", "40_root", false}, {"42_child", "41_bridge", true},
		{"50_count_limit", "", true},
	}
	bodies := map[string]string{"pack.mcmeta": `{"pack":{"description":"displayed advancement measurements","min_format":107,"max_format":107}}`}
	for i, node := range nodes {
		raw := map[string]any{"criteria": map[string]any{"manual": map[string]any{"trigger": "minecraft:impossible"}}}
		if node.parent != "" {
			raw["parent"] = "displaypin:" + node.parent
		}
		if node.displayed {
			display := map[string]any{"title": "displaypin:" + node.id, "description": map[string]any{"text": "Description " + node.id}, "icon": map[string]any{"id": "minecraft:paper"}, "announce_to_chat": true}
			if node.parent == "" {
				display["background"] = "minecraft:gui/advancements/backgrounds/adventure"
				display["show_toast"] = false
				display["announce_to_chat"] = false
			}
			if i%3 == 1 {
				display["frame"] = "goal"
			}
			if i%3 == 2 {
				display["frame"] = "challenge"
			}
			if node.id == "50_count_limit" {
				display["icon"] = map[string]any{"id": "minecraft:paper", "count": 99}
			}
			if node.id == "02_chain" {
				display["title"] = map[string]any{"text": "displaypin:" + node.id, "color": "red", "bold": true}
				display["description"] = map[string]any{"text": "Styled description", "italic": false}
				display["icon"] = map[string]any{"id": "minecraft:diamond", "count": 3, "components": map[string]any{"minecraft:custom_name": "Display icon"}}
				display["hidden"] = true
				display["show_toast"] = false
			}
			raw["display"] = display
		}
		encoded, err := json.Marshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		bodies["data/displaypin/advancement/"+node.id+".json"] = string(encoded)
	}
	bodies["data/displaypin/advancement/invalid_count.json"] = `{"criteria":{"manual":{"trigger":"minecraft:impossible"}},"display":{"title":"displaypin:invalid_count","description":"invalid count","icon":{"id":"minecraft:paper","count":100}}}`
	directory := t.TempDir()
	files := map[string]string{"ops.json": opsJSONFor(name)}
	for path, body := range bodies {
		rel := "datapacks/displaypin/" + path
		files["world/"+rel] = body
		file := filepath.Join(directory, rel)
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	tg := requireBlackboxFiles(t, nil, files, func(s *server.Server) { s.WorldDir = directory })
	defer tg.cleanup()
	run := func(addr string) (map[string]displayedAdvancementProbe, []string) {
		d := newMenuLifecycleObserver(t, addr, name)
		definitions := map[string]displayedAdvancementProbe{}
		var chats []string
		finished := false
		fold := func(ps []usePkt) {
			for _, p := range ps {
				if structureSaidBB([]string{fmt.Sprintf("0x%x:%s", p.id, p.data)}, "DISPLAY-PIN-END") { // disguised_chat: actual /say output
					finished = true
				}
				if p.id == server.IDPlayUpdateAdvance {
					decodeDisplayedAdvancementPacket(t, p.data, definitions)
				}
				if p.id == server.IDPlaySystemChatCB {
					if bytes.Contains(p.data, []byte("displaypin:")) && bytes.Contains(p.data, []byte("chat.type.advancement")) {
						reader := bytes.NewReader(p.data)
						value, err := nbt.ReadUnnamed(reader)
						if err != nil {
							t.Fatal(err)
						}
						chats = append(chats, fmt.Sprint(value))
					}
				}
			}
		}
		for _, root := range []string{"00_root", "10_root", "20_root", "30_root", "40_root", "50_count_limit", "invalid_count", "quiet"} {
			if root == "quiet" {
				sendCommand(t, d.pw, "gamerule minecraft:show_advancement_messages false")
				settleUse(t, d.sink, fold, "display-chat-rule")
				sendCommand(t, d.pw, "advancement revoke @s from displaypin:00_root")
				settleUse(t, d.sink, fold, "display-revoke")
				root = "00_root"
			}
			sendCommand(t, d.pw, "advancement grant @s from displaypin:"+root)
			settleUse(t, d.sink, fold, "displayed-advancement")
		}
		sendCommand(t, d.pw, "say DISPLAY-PIN-END")
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			fold(d.sink.take())
			if finished && len(definitions) >= len(nodes) && len(chats) >= 19 {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if !finished {
			t.Fatal("display advancement command barrier missing")
		}
		settleUse(t, d.sink, fold, "display-final")

		if len(definitions) != len(nodes) {
			t.Errorf("%s definitions=%d wanted%d", addr, len(definitions), len(nodes))
		}
		ids := make([]string, 0, len(definitions))
		for id := range definitions {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			p := definitions[id]
			t.Logf("%s %s parent=%s display=%v icon=%s frame=%d flags=%d bg=%s xy=%g,%g title=%v desc=%v", addr, id, p.Parent, p.Display, p.Icon, p.Frame, p.Flags, p.Background, p.X, p.Y, p.Title, p.Description)
		}
		checkDisplayedGeometry(t, definitions)
		if len(chats) != 19 {
			t.Errorf("%s custom advancement chats=%d wanted19 (quiet regrant must stay silent)", addr, len(chats))
		}
		sort.Strings(chats)
		return definitions, chats
	}
	v, vc := run(tg.vanilla)
	g, gc := run(tg.mcgo)
	if !reflect.DeepEqual(normalizeDisplayedY(v), normalizeDisplayedY(g)) {
		t.Errorf("displayed advancement definitions differ: vanilla%v mcgo%v", v, g)
	}
	if !reflect.DeepEqual(vc, gc) {
		t.Errorf("displayed advancement chats differ: vanilla%v mcgo%v", vc, gc)
	}
}

func decodeDisplayedAdvancementPacket(t *testing.T, body []byte, out map[string]displayedAdvancementProbe) {
	decodeDisplayedAdvancementPacketMatching(t, body, out, func(id string) bool {
		return strings.HasPrefix(id, "displaypin:") || strings.HasPrefix(id, "minecraft:story/")
	})
}

func decodeDisplayedAdvancementPacketMatching(t *testing.T, body []byte, out map[string]displayedAdvancementProbe, accepts func(string) bool) {
	t.Helper()
	r := protocol.NewPacketReader(body)
	check := func(err error) {
		if err != nil {
			t.Fatalf("display advancement packet: %v", err)
		}
	}
	_, err := r.Bool()
	check(err)
	count, err := r.VarInt()
	check(err)
	for range count {
		start := len(body) - r.Remaining()
		id, err := r.String()
		check(err)
		if !accepts(id) {
			captured := data.AdvDefWire[id]
			if captured == nil {
				t.Fatalf("unknown unrelated definition%s", id)
			}
			_, err = r.Read(len(captured) - (len(body) - r.Remaining() - start))
			check(err)
			continue
		}
		var p displayedAdvancementProbe
		yOffset := -1
		parent, err := r.Bool()
		check(err)
		if parent {
			p.Parent, err = r.String()
			check(err)
		}
		p.Display, err = r.Bool()
		check(err)
		if p.Display {
			readComponent := func() any {
				offset := len(body) - r.Remaining()
				reader := bytes.NewReader(body[offset:])
				value, err := nbt.ReadUnnamed(reader)
				check(err)
				_, err = r.Read(len(body) - offset - reader.Len())
				check(err)
				return value
			}
			p.Title = readComponent()
			p.Description = readComponent()
			item, err := r.VarInt()
			check(err)
			quantity, err := r.VarInt()
			check(err)
			stack, _, _, _, err := readSlotBBTail(r, quantity, item)
			check(err)
			p.Icon = stackKey(stack)
			p.Frame, err = r.VarInt()
			check(err)
			p.Flags, err = r.Int()
			check(err)
			if p.Flags&1 != 0 {
				p.Background, err = r.String()
				check(err)
			}
			p.X, err = r.Float()
			check(err)
			yOffset = len(body) - r.Remaining()
			p.Y, err = r.Float()
			check(err)
		}
		groups, err := r.VarInt()
		check(err)
		for range groups {
			names, err := r.VarInt()
			check(err)
			for range names {
				_, err = r.String()
				check(err)
			}
		}
		_, err = r.Bool()
		check(err)
		p.Raw = append([]byte(nil), body[start:len(body)-r.Remaining()]...)
		if yOffset >= 0 && (nondeterministicDisplayY(id) || strings.HasPrefix(id, "minecraft:story/")) {
			clear(p.Raw[yOffset-start : yOffset-start+4])
		}
		out[id] = p
	}
	count, err = r.VarInt()
	check(err)
	for range count {
		_, err = r.String()
		check(err)
	}
	count, err = r.VarInt()
	check(err)
	for range count {
		_, err = r.String()
		check(err)
		criteria, err := r.VarInt()
		check(err)
		for range criteria {
			_, err = r.String()
			check(err)
			done, err := r.Bool()
			check(err)
			if done {
				_, err = r.Long()
				check(err)
			}
		}
	}
	_, err = r.Bool()
	check(err)
	if r.Remaining() != 0 {
		t.Fatalf("display advancement packet trailing%d", r.Remaining())
	}
}

// AdvancementNode uses an identity-based set for children. Two unchanged
// vanilla JVM runs produce different sibling order/Y positions; all other
// encoded fields and deterministic chain/bridge coordinates remain exact.
func nondeterministicDisplayY(id string) bool {
	return strings.HasPrefix(id, "displaypin:1") || strings.HasPrefix(id, "displaypin:2")
}
func normalizeDisplayedY(in map[string]displayedAdvancementProbe) map[string]displayedAdvancementProbe {
	out := map[string]displayedAdvancementProbe{}
	for id, p := range in {
		if nondeterministicDisplayY(id) {
			p.Y = 0
		}
		out[id] = p
	}
	return out
}
func checkDisplayedGeometry(t *testing.T, definitions map[string]displayedAdvancementProbe) {
	t.Helper()
	children := map[string][]string{}
	for id, p := range definitions {
		children[p.Parent] = append(children[p.Parent], id)
	}
	var renderedChildren func(string) []string
	renderedChildren = func(id string) []string {
		var out []string
		for _, child := range children[id] {
			if definitions[child].Display {
				out = append(out, child)
			} else {
				out = append(out, renderedChildren(child)...)
			}
		}
		return out
	}
	levels := map[string]map[float32][]float32{}
	minY, maxY := map[string]float32{}, map[string]float32{}
	leaves := map[string]int{}
	for id, p := range definitions {
		if !p.Display {
			continue
		}
		root := id
		depth := 0
		for parent := p.Parent; parent != ""; {
			ancestor, ok := definitions[parent]
			if !ok {
				t.Errorf("%s missing parent%s", id, parent)
				break
			}
			root = parent
			if ancestor.Display {
				depth++
			}
			parent = ancestor.Parent
		}
		if !definitions[root].Display {
			if p.X != 0 || p.Y != 0 {
				t.Errorf("displayless root descendant%s layout=%g,%g wanted0,0", id, p.X, p.Y)
			}
			continue
		}
		if p.X != float32(depth) || p.Y < 0 {
			t.Errorf("%s layout=%g,%g wanteddepth%d/nonnegativeY", id, p.X, p.Y, depth)
		}
		if levels[root] == nil {
			levels[root] = map[float32][]float32{}
			minY[root] = p.Y
			maxY[root] = p.Y
		}
		levels[root][p.X] = append(levels[root][p.X], p.Y)
		minY[root] = min(minY[root], p.Y)
		maxY[root] = max(maxY[root], p.Y)
		visible := renderedChildren(id)
		if len(visible) == 0 {
			leaves[root]++
			continue
		}
		low, high := definitions[visible[0]].Y, definitions[visible[0]].Y
		for _, child := range visible {
			low = min(low, definitions[child].Y)
			high = max(high, definitions[child].Y)
		}
		if math.Abs(float64(p.Y-(low+high)/2)) > 1e-5 {
			t.Errorf("%s not centered: y%g children%g..%g", id, p.Y, low, high)
		}
	}
	for root, byDepth := range levels {
		if minY[root] != 0 || maxY[root] > float32(leaves[root]-1) {
			t.Errorf("%s layout range%g..%g exceeds%d leaves", root, minY[root], maxY[root], leaves[root])
		}
		for depth, ys := range byDepth {
			sort.Slice(ys, func(i, j int) bool { return ys[i] < ys[j] })
			for i := 1; i < len(ys); i++ {
				if ys[i]-ys[i-1] < 1-1e-5 {
					t.Errorf("%s depth%g overlaps at%v", root, depth, ys)
				}
			}
		}
	}
}

func TestDisplayedBuiltinTabOverrideParity(t *testing.T) {
	for _, override := range []string{"mine_stone", "root"} {
		t.Run(override, func(t *testing.T) {
			const name = "AdvTabOverride"
			parent := ""
			if override != "root" {
				parent = `"parent":"minecraft:story/root",`
			}
			bodies := map[string]string{
				"pack.mcmeta": `{"pack":{"description":"builtin advancement tab override","min_format":107,"max_format":107}}`,
				"data/minecraft/advancement/story/" + override + ".json": `{` + parent + `"criteria":{"manual":{"trigger":"minecraft:impossible"}}}`,
			}
			directory := t.TempDir()
			files := map[string]string{"ops.json": opsJSONFor(name)}
			for path, body := range bodies {
				rel := "datapacks/override/" + path
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
			run := func(addr string) map[string]displayedAdvancementProbe {
				conn, pr, pw := joinPlayBlackbox(t, addr, name)
				t.Cleanup(func() { conn.Close() })
				sink := newUseClient(pr, pw, true, server.IDPlayUpdateAdvance)
				sendClientInformation(t, pw)
				definitions := map[string]displayedAdvancementProbe{}
				finished := false
				fold := func(ps []usePkt) {
					for _, p := range ps {
						if p.id == server.IDPlayUpdateAdvance {
							decodeDisplayedAdvancementPacket(t, p.data, definitions)
						}
						if structureSaidBB([]string{fmt.Sprintf("0x%x:%s", p.id, p.data)}, "TAB-OVERRIDE-END") {
							finished = true
						}
					}
				}
				sendCommand(t, pw, "advancement grant @s from minecraft:story/root")
				sendCommand(t, pw, "say TAB-OVERRIDE-END")
				deadline := time.Now().Add(15 * time.Second)
				for time.Now().Before(deadline) {
					fold(sink.take())
					if finished && len(definitions) >= 10 {
						break
					}
					time.Sleep(20 * time.Millisecond)
				}
				if !finished {
					t.Fatal("builtin override barrier missing")
				}
				settleUse(t, sink, fold, "builtin-tab-override")
				if len(definitions) < 10 {
					t.Fatalf("%s insufficient builtin definitions%d", addr, len(definitions))
				}
				checkDisplayedGeometry(t, definitions)
				if definitions["minecraft:story/"+override].Display {
					t.Errorf("%s overridden node retained display", addr)
				}
				for _, id := range []string{"root", "upgrade_tools", "smelt_iron", "iron_tools", "obtain_armor"} {
					p, exists := definitions["minecraft:story/"+id]
					if !exists {
						t.Errorf("%s missing story/%s", addr, id)
						continue
					}
					expected := map[string]float32{"root": 0, "upgrade_tools": 1, "smelt_iron": 2, "iron_tools": 3, "obtain_armor": 3}[id]
					if override == "root" {
						expected = 0
					}
					if p.X != expected || override == "root" && p.Y != 0 {
						t.Errorf("%s story/%s coords%g,%g wantedX%g (displayless root requiresY0)", addr, id, p.X, p.Y, expected)
					}
				}
				if override == "root" {
					for id, p := range definitions {
						if strings.HasPrefix(id, "minecraft:story/") && p.Display && (p.X != 0 || p.Y != 0) {
							t.Errorf("%s %s retained stale coords%g,%g below displaylessroot", addr, id, p.X, p.Y)
						}
					}
				}
				return definitions
			}
			v, g := run(tg.vanilla), run(tg.mcgo)
			for id, p := range v {
				if strings.HasPrefix(id, "minecraft:story/") {
					q, ok := g[id]
					if !ok {
						t.Errorf("missing%s", id)
						continue
					}
					p.Y = 0
					q.Y = 0
					if !reflect.DeepEqual(p, q) {
						t.Errorf("builtin definition%s differs beyond identity-dependentY", id)
					}
				}
			}
		})
	}
}
