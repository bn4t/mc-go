package blackbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"mc-go/conformance"
	"mc-go/internal/nbt"
	publicserver "mc-go/server"
)

type serverDataSlotFixture struct {
	body []byte
	item string
}

// Reuse exact official 775/776 observations rather than the server's slot
// decoder: a named diamond stack inside a shulker box exercises nested storage.
func serverDataNestedFixture(t *testing.T, number int32) serverDataSlotFixture {
	t.Helper()
	raw, err := os.ReadFile("../../internal/server/testdata/slot-profiles/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Cases []struct {
			Protocol            int32
			Label, File, SHA256 string
			Command             string `json:"console_command"`
		}
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, row := range manifest.Cases {
		if row.Protocol != number || row.Label != "container" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "server", "testdata", "slot-profiles", row.File))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != row.SHA256 {
			t.Fatal("official nested fixture checksum mismatch")
		}
		r := bytes.NewReader(raw)
		if _, err := mixedReadVI(r); err != nil {
			t.Fatal(err)
		}
		if _, err := mixedReadVI(r); err != nil {
			t.Fatal(err)
		}
		var slot int16
		if err := binary.Read(r, binary.BigEndian, &slot); err != nil || slot != 36 {
			t.Fatalf("nested fixture slot=%d: %v", slot, err)
		}
		_, item, ok := strings.Cut(row.Command, " with ")
		if !ok {
			t.Fatal("nested fixture command missing item")
		}
		return serverDataSlotFixture{body: append([]byte(nil), raw[len(raw)-r.Len():]...), item: item}
	}
	t.Fatalf("no official nested fixture for%d", number)
	return serverDataSlotFixture{}
}

// Fresh oracles replay only the already measured command and compare its slot
// bytes to the existing checksum-pinned fixture. This does not test JSON saves.
func TestServerDataNestedSlotOracleProfiles(t *testing.T) {
	for _, number := range []int32{775, 776} {
		t.Run(fmt.Sprint(number), func(t *testing.T) {
			jar := blackboxJar(t)
			if number == 775 {
				jar = os.Getenv("MCGO_VANILLA_775_JAR")
				if jar == "" {
					jar = filepath.Join("..", "..", "vanilla", "server-26.1.jar")
				}
				var err error
				jar, err = filepath.Abs(jar)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = os.Stat(jar); err != nil {
					t.Skipf("775 oracle unavailable: %v", err)
				}
			}
			name := fmt.Sprintf("DataSlot%d", number)
			port, ok := freePort()
			if !ok {
				t.Skip("loopback unavailable")
			}
			addr, stop := startVanillaCfgFiles(t, jar, port, map[string]string{"view-distance": "2", "simulation-distance": "2"}, map[string]string{"ops.json": opsJSONFor(name)})
			defer stop()
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			c := compactSaveOpen(t, ctx, addr, number, name)
			fixture := serverDataNestedFixture(t, number)
			initialInventory := false
			for _, packet := range c.InitialPackets() {
				if packet.ID == 0x12 {
					r := bytes.NewReader(packet.Data)
					window, _ := mixedReadVI(r)
					if window == 0 {
						serverDataContent(t, packet.Data, 46, nil)
						initialInventory = true
					}
				}
			}
			if !initialInventory {
				t.Fatal("fresh oracle inventory missing")
			}
			if err := c.Command("item replace entity @s hotbar.0 with " + fixture.item); err != nil {
				t.Fatal(err)
			}
			_, err := c.WaitPacket(ctx, func(p conformance.Packet) bool {
				if p.ID != 0x14 {
					return false
				}
				r := bytes.NewReader(p.Data)
				window, e1 := mixedReadVI(r)
				_, e2 := mixedReadVI(r)
				var slot int16
				e3 := binary.Read(r, binary.BigEndian, &slot)
				if e1 != nil || e2 != nil || e3 != nil || window != 0 || slot != 36 {
					return false
				}
				body := p.Data[len(p.Data)-r.Len():]
				if !bytes.Equal(body, fixture.body) {
					t.Fatalf("protocol%d nested stack differs from official fixture: got%x want%x", number, body, fixture.body)
				}
				return true
			})
			if err != nil {
				t.Fatal(err)
			}
			// Clear first, then independently verify the creative ingress envelope
			// and a copy into a distinct empty slot return the named fixture again.
			mixedBarrier(t, c, "item replace entity @s hotbar.0 with minecraft:stone")
			pollWireFeedback(t, ctx, c, "data get entity @s Inventory", "commands.data.entity.query", func(text string) bool {
				return strings.Contains(text, "minecraft:stone") && !strings.Contains(text, "Nested")
			})
			if err := c.SendPacket(0x38, serverDataCreativeBody(t, fixture)); err != nil {
				t.Fatal(err)
			}
			// The official handler can commit creative ingress after an earlier
			// command response. Poll authoritative state without resending input.
			pollWireFeedback(t, ctx, c, "data get entity @s Inventory", "commands.data.entity.query", func(text string) bool {
				return strings.Contains(text, "shulker_box") && strings.Contains(text, "Nested")
			})
			if err := c.Command("item replace entity @s hotbar.1 from entity @s hotbar.0"); err != nil {
				t.Fatal(err)
			}
			_, err = c.WaitPacket(ctx, func(p conformance.Packet) bool {
				if p.ID != 0x14 {
					return false
				}
				r := bytes.NewReader(p.Data)
				window, e1 := mixedReadVI(r)
				_, e2 := mixedReadVI(r)
				var slot int16
				e3 := binary.Read(r, binary.BigEndian, &slot)
				if e1 != nil || e2 != nil || e3 != nil || window != 0 || slot != 37 {
					return false
				}
				actual, err := serverDataReadStack(r, 0, false)
				wantReader := bytes.NewReader(fixture.body)
				want, werr := serverDataReadStack(wantReader, 0, false)
				if err != nil || werr != nil || r.Len() != 0 || wantReader.Len() != 0 || !reflect.DeepEqual(actual, want) {
					t.Fatalf("creative fixture echo got%+v want%+v: %v/%v", actual, want, err, werr)
				}
				return true
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("protocol %d measured nested command reproduces exact existing official slot bytes; cleared-slot creative ingress and distinct-slot copy preserve the complete measured stack", number)
		})
	}
}

func serverDataRuntime(t *testing.T, worldPath, dataPath string) (*publicserver.Server, string, func()) {
	t.Helper()
	if os.Getenv(targetProp) != "" {
		t.Skipf("%s mode requires local public save completion and restart", targetProp)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("loopback unavailable: %v", err)
	}
	s := publicserver.New(ln.Addr().String())
	s.WorldPath = worldPath
	s.DataPath = dataPath
	s.ViewDistance = 2
	s.SimulationDistance = 2
	// Embeddings load the JSON data store explicitly, just as cmd/mc-go does.
	// Runtime Start loads the world overrides but does not load DataPath.
	if err := s.LoadWorld(); err != nil {
		ln.Close()
		s.Stop()
		t.Fatal(err)
	}
	if err := s.LoadServerData(); err != nil {
		ln.Close()
		s.Stop()
		t.Fatal(err)
	}
	if err := s.InitPlugins(); err != nil {
		ln.Close()
		t.Fatal(err)
	}
	go func() { _ = s.Serve(ln) }()
	var once sync.Once
	stop := func() { once.Do(func() { ln.Close(); s.Stop() }) }
	t.Cleanup(stop)
	return s, ln.Addr().String(), stop
}
func serverDataPlain(item, count int32) []byte {
	if count == 0 {
		return []byte{0}
	}
	b := mixedVI(nil, count)
	b = mixedVI(b, item)
	return append(b, 0, 0)
}
func serverDataItems(t *testing.T, number int32) mixedFacts {
	t.Helper()
	raw, err := os.ReadFile("../../conformance/testdata/mixed-profile-facts.json")
	if err != nil {
		t.Fatal(err)
	}
	var facts struct{ Profiles map[string]mixedFacts }
	if err := json.Unmarshal(raw, &facts); err != nil {
		t.Fatal(err)
	}
	return facts.Profiles[fmt.Sprint(number)]
}

// This fixture-only reader accepts plain stacks plus the two component codecs
// present in the checksum-pinned official container fixture. Both profiles use
// custom_name=6 and container=75. Container arrays may legally retain trailing
// empty slots; normalize those rather than demanding one encoder's padding.
type serverDataWireStack struct {
	ID, Count             int32
	Name                  string
	HasName, HasContainer bool
	Contents              []serverDataWireStack
}

func serverDataReadStack(r *bytes.Reader, depth int, contained bool) (serverDataWireStack, error) {
	if depth > 8 {
		return serverDataWireStack{}, fmt.Errorf("fixture nesting too deep")
	}
	var out serverDataWireStack
	if contained {
		id, e1 := mixedReadVI(r)
		count, e2 := mixedReadVI(r)
		if e1 != nil || e2 != nil || id < 0 || count < 1 {
			return out, fmt.Errorf("contained stack header")
		}
		out.ID, out.Count = id, count
	} else {
		count, err := mixedReadVI(r)
		if err != nil || count < 0 {
			return out, fmt.Errorf("slot count: %v", err)
		}
		if count == 0 {
			return out, nil
		}
		id, err := mixedReadVI(r)
		if err != nil || id < 0 {
			return out, fmt.Errorf("slot id: %v", err)
		}
		out.ID, out.Count = id, count
	}
	added, e1 := mixedReadVI(r)
	removed, e2 := mixedReadVI(r)
	if e1 != nil || e2 != nil || added < 0 || added > 2 || removed != 0 {
		return out, fmt.Errorf("fixture component patch")
	}
	seen := map[int32]bool{}
	for i := int32(0); i < added; i++ {
		kind, err := mixedReadVI(r)
		if err != nil || seen[kind] {
			return out, fmt.Errorf("component type")
		}
		seen[kind] = true
		switch kind {
		case 6:
			out.HasName = true
			v, err := nbt.ReadUnnamed(r)
			if err != nil {
				return out, err
			}
			name, ok := v.(string)
			if !ok {
				return out, fmt.Errorf("fixture custom name is not literal text")
			}
			out.Name = name
		case 75:
			out.HasContainer = true
			n, err := mixedReadVI(r)
			if err != nil || n < 0 || n > 27 {
				return out, fmt.Errorf("fixture container length")
			}
			out.Contents = make([]serverDataWireStack, int(n))
			for index := range out.Contents {
				present, err := r.ReadByte()
				if err != nil || present > 1 {
					return out, fmt.Errorf("container presence")
				}
				if present == 1 {
					child, err := serverDataReadStack(r, depth+1, true)
					if err != nil {
						return out, err
					}
					out.Contents[index] = child
				}
			}
			for len(out.Contents) > 0 && out.Contents[len(out.Contents)-1].Count == 0 {
				out.Contents = out.Contents[:len(out.Contents)-1]
			}
			if len(out.Contents) == 0 {
				out.Contents = nil
			}
		default:
			return out, fmt.Errorf("unmeasured fixture component %d", kind)
		}
	}
	return out, nil
}
func serverDataContent(t *testing.T, body []byte, count int, expected map[int][]byte) int32 {
	t.Helper()
	r := bytes.NewReader(body)
	window, e1 := mixedReadVI(r)
	_, e2 := mixedReadVI(r)
	n, e3 := mixedReadVI(r)
	if e1 != nil || e2 != nil || e3 != nil || int(n) != count {
		t.Fatalf("content header window%d slots%d want%d: %v/%v/%v", window, n, count, e1, e2, e3)
	}
	for slot := 0; slot < count; slot++ {
		raw := expected[slot]
		if raw == nil {
			raw = []byte{0}
		}
		wantReader := bytes.NewReader(raw)
		want, err := serverDataReadStack(wantReader, 0, false)
		if err != nil || wantReader.Len() != 0 {
			t.Fatalf("golden slot%d invalid: %v", slot, err)
		}
		got, err := serverDataReadStack(r, 0, false)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("wire inventory slot%d differs: got%+v want%+v error=%v", slot, got, want, err)
		}
	}
	carried, err := serverDataReadStack(r, 0, false)
	if err != nil || carried.Count != 0 || r.Len() != 0 {
		t.Fatalf("content carried/end framing: carried%+v remaining%d error=%v", carried, r.Len(), err)
	}
	return window
}
func serverDataWaitOffline(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if fetchStatus(t, addr).Players.Online == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("source player did not retire")
}

// Creative ingress adds a byte-array envelope around each top-level
// component payload; nested item templates retain their trusted layout. This
// single-component fixture is already independently captured for both profiles.
func serverDataCreativeBody(t *testing.T, fixture serverDataSlotFixture) []byte {
	t.Helper()
	r := bytes.NewReader(fixture.body)
	values := make([]int32, 5)
	for index := range values {
		value, err := mixedReadVI(r)
		if err != nil {
			t.Fatal(err)
		}
		values[index] = value
	}
	if values[0] != 1 || values[2] != 1 || values[3] != 0 || values[4] != 75 {
		t.Fatal("unexpected top-level fixture shape")
	}
	out := binary.BigEndian.AppendUint16(nil, 36)
	for _, value := range values {
		out = mixedVI(out, value)
	}
	out = mixedVI(out, int32(r.Len()))
	out = append(out, fixture.body[len(fixture.body)-r.Len():]...)
	return out
}

func serverDataPlayerSetup(t *testing.T, ctx context.Context, c *conformance.PlayClient, plainCount, levels int32) []byte {
	t.Helper()
	fixture := serverDataNestedFixture(t, c.Protocol())
	// Replay the measured stack through the measured creative-slot layout.
	// The legacy item command parser omits nested child custom names; do not
	// broaden this persistence slice by changing that separate command path.
	creative := serverDataCreativeBody(t, fixture)
	if err := c.SendPacket(0x38, creative); err != nil {
		t.Fatal(err)
	}
	if err := coldJoinToken(ctx, c, "data-creative-fixture-fence", nil); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{fmt.Sprintf("item replace entity @s hotbar.1 with minecraft:oak_planks %d", plainCount), fmt.Sprintf("item replace entity @s enderchest.0 with minecraft:stone %d", plainCount), fmt.Sprintf("experience add @s %d levels", levels), "experience add @s 3 points"} {
		mixedBarrier(t, c, command)
	}
	// Request a distinct, nonzero XP update and record its three wire fields.
	// Their exact bits must survive persistence; the existing XP differential
	// tests separately establish the gameplay arithmetic and command semantics.
	if err := c.Command("experience add @s 1 points"); err != nil {
		t.Fatal(err)
	}
	var xp []byte
	_, err := c.WaitPacket(ctx, func(p conformance.Packet) bool {
		if p.ID != 0x67 {
			return false
		}
		r := bytes.NewReader(p.Data)
		var fraction uint32
		if err := binary.Read(r, binary.BigEndian, &fraction); err != nil {
			return false
		}
		level, e1 := mixedReadVI(r)
		total, e2 := mixedReadVI(r)
		if e1 == nil && e2 == nil && r.Len() == 0 && level == levels && total == 4 {
			xp = append([]byte(nil), p.Data...)
			return true
		}
		return false
	})
	if err != nil {
		t.Fatal(err)
	}
	return xp
}
func serverDataMenu(t *testing.T, ctx context.Context, c *conformance.PlayClient, x, z, sequence int32, plainCount int32, kind string) {
	t.Helper()
	body := mixedVI(nil, 0)
	body = binary.BigEndian.AppendUint64(body, mixedPos(x, -60, z))
	body = mixedVI(body, 1)
	for _, v := range []uint32{0x3f000000, 0x3f800000, 0x3f000000} {
		body = binary.BigEndian.AppendUint32(body, v)
	}
	body = append(body, 0, 0)
	body = mixedVI(body, sequence)
	if err := c.SendPacket(0x42, body); err != nil {
		t.Fatal(err)
	}
	window := int32(-1)
	fixture := serverDataNestedFixture(t, c.Protocol())
	facts := serverDataItems(t, c.Protocol())
	for {
		p, err := c.ReadPacket(ctx)
		if err != nil {
			t.Fatalf("%s menu: %v", kind, err)
		}
		if p.ID == 0x3b {
			r := bytes.NewReader(p.Data)
			id, e1 := mixedReadVI(r)
			menu, e2 := mixedReadVI(r)
			if e1 != nil || e2 != nil || menu != 2 {
				t.Fatalf("%s menu header id%d type%d: %v/%v", kind, id, menu, e1, e2)
			}
			window = id
		}
		if p.ID == 0x12 && window >= 0 {
			r := bytes.NewReader(p.Data)
			id, _ := mixedReadVI(r)
			if id != window {
				continue
			}
			expected := map[int][]byte{54: fixture.body, 55: serverDataPlain(facts.Items["oak_planks"], plainCount)}
			switch kind {
			case "chest":
				expected[0] = serverDataPlain(facts.Items["stone"], 9)
				expected[2] = fixture.body
			case "barrel":
				expected[7] = serverDataPlain(facts.Items["stone"], 12)
			case "ender":
				expected[0] = serverDataPlain(facts.Items["stone"], plainCount)
			}
			serverDataContent(t, p.Data, 63, expected)
			if err := c.SendPacket(0x13, mixedVI(nil, window)); err != nil {
				t.Fatal(err)
			}
			if err := coldJoinToken(ctx, c, "data-menu-closed-"+kind, nil); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
}
func serverDataVerify(t *testing.T, ctx context.Context, c *conformance.PlayClient, plainCount int32, wantXP []byte) {
	t.Helper()
	fixture := serverDataNestedFixture(t, c.Protocol())
	facts := serverDataItems(t, c.Protocol())
	inventory, xp := false, false
	for _, p := range c.InitialPackets() {
		switch p.ID {
		case 0x12:
			r := bytes.NewReader(p.Data)
			id, _ := mixedReadVI(r)
			if id == 0 {
				serverDataContent(t, p.Data, 46, map[int][]byte{36: fixture.body, 37: serverDataPlain(facts.Items["oak_planks"], plainCount)})
				inventory = true
			}
		case 0x67:
			if !bytes.Equal(p.Data, wantXP) {
				t.Fatalf("protocol%d restored XP bytes%x want%x", c.Protocol(), p.Data, wantXP)
			}
			xp = true
		}
	}
	if !inventory || !xp {
		t.Fatalf("initial restored inventory=%v XP=%v", inventory, xp)
	}
	shape := liveWindowShape(t, c.Protocol())
	w := &liveWindowTrace{}
	center, err := liveWindowCenter(c.InitialPackets())
	if err != nil {
		t.Fatal(err)
	}
	if err := readLiveWindow(ctx, c, shape, w, center, c.InitialPackets(), nil); err != nil {
		t.Fatal(err)
	}
	body := w.loaded[[2]int32{0, 0}]
	_, _, entries, err := parseChunkBlockEntities(body)
	if err != nil {
		t.Fatal(err)
	}
	sign, ok := entries[[3]int{1, -60, 2}]
	if !ok {
		t.Fatal("restored sign absent from chunk")
	}
	front, fok := sign.nbt["front_text"].(map[string]any)
	back, bok := sign.nbt["back_text"].(map[string]any)
	if !fok || !bok {
		t.Fatal("restored sign missing both faces")
	}
	frontLines, _ := front["messages"].(nbt.List)
	backLines, _ := back["messages"].(nbt.List)
	if len(frontLines.Items) != 4 || frontLines.Items[0] != `"alpha"` || len(backLines.Items) != 4 || backLines.Items[0] != `"behind"` || front["color"] != "red" || front["has_glowing_text"] != int8(1) || sign.nbt["is_waxed"] != int8(1) {
		t.Fatalf("restored sign content/style changed: %s", renderNBT(sign.nbt, true))
	}
	serverDataMenu(t, ctx, c, 1, 0, 1, plainCount, "chest")
	serverDataMenu(t, ctx, c, 2, 0, 2, plainCount, "barrel")
	serverDataMenu(t, ctx, c, 3, 0, 3, plainCount, "ender")
	t.Logf("protocol %d: measured nested inventory/chest state, plain slots, XP fields, ender storage, barrel contents and sign faces/style survive restart", c.Protocol())
}

// World overrides and JSON server data are separate snapshots. Keep the block
// fixture unchanged between those saves; this test promises no atomic cut across
// stores. Data completion is copied before the active source disconnects, so a
// disconnect save cannot conceal an early public receipt. An earlier player's
// offline inventory/XP must remain when a different player is subsequently saved.
func TestServerDataSaveCompletionRestartWire(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	dir := t.TempDir()
	worldPath, dataPath := filepath.Join(dir, "world.json"), filepath.Join(dir, "data.json")
	s, addr, stop := serverDataRuntime(t, worldPath, dataPath)
	a := compactSaveOpen(t, ctx, addr, 775, "DataOffline775")
	shape := liveWindowShape(t, 775)
	initial := &liveWindowTrace{}
	center, err := liveWindowCenter(a.InitialPackets())
	if err != nil {
		t.Fatal(err)
	}
	if err := readLiveWindow(ctx, a, shape, initial, center, a.InitialPackets(), nil); err != nil {
		t.Fatal(err)
	}
	mixedBarrier(t, a, "tick freeze")
	for _, command := range []string{"setblock 1 -60 0 minecraft:chest", "setblock 2 -60 0 minecraft:barrel", "setblock 3 -60 0 minecraft:ender_chest", "setblock 1 -60 2 minecraft:oak_sign", "item replace block 1 -60 0 container.0 with minecraft:stone 9", "item replace block 2 -60 0 container.7 with minecraft:stone 12", `data merge block 1 -60 2 {front_text:{color:"red",has_glowing_text:1b,messages:['"alpha"','','','']},back_text:{messages:['"behind"','','','']},is_waxed:1b}`} {
		mixedBarrier(t, a, command)
	}
	xpA := serverDataPlayerSetup(t, ctx, a, 4, 7)
	mixedBarrier(t, a, "item replace block 1 -60 0 container.2 from entity @s hotbar.0")
	// Save the unchanged physical block identities separately from server data.
	if err := s.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	copiedDir := t.TempDir()
	copiedWorld, copiedData := filepath.Join(copiedDir, "world.json"), filepath.Join(copiedDir, "data.json")
	for _, suffix := range []string{"", ".the_nether", ".the_end"} {
		raw, err := os.ReadFile(worldPath + suffix)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(copiedWorld+suffix, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Establish an earlier successful public data completion before retiring A.
	if err := s.SaveServerData(); err != nil {
		t.Fatal(err)
	}
	_ = a.Close()
	serverDataWaitOffline(t, addr)
	b := compactSaveOpen(t, ctx, addr, 776, "DataActive776")
	shape = liveWindowShape(t, 776)
	initial = &liveWindowTrace{}
	center, err = liveWindowCenter(b.InitialPackets())
	if err != nil {
		t.Fatal(err)
	}
	if err := readLiveWindow(ctx, b, shape, initial, center, b.InitialPackets(), nil); err != nil {
		t.Fatal(err)
	}
	xpB := serverDataPlayerSetup(t, ctx, b, 6, 5)
	// The second save contains active B and must preserve cached offline A.
	if err := s.SaveServerData(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(dataPath)
	if err != nil {
		t.Fatal(err)
	}
	var header struct{ Version int }
	if err := json.Unmarshal(raw, &header); err != nil || header.Version != 3 {
		t.Fatalf("server data schema%d want3: %v", header.Version, err)
	}
	if err := os.WriteFile(copiedData, raw, 0600); err != nil {
		t.Fatal(err)
	}
	// Overwrite the source with another generation while the active connection
	// remains alive. The restart must still load the earlier completion's copy.
	for _, command := range []string{"item replace entity @s hotbar.0 with minecraft:stone", "experience add @s 10 levels", "item replace block 1 -60 0 container.0 with minecraft:stone 1", "item replace block 2 -60 0 container.7 with minecraft:stone 1", `data merge block 1 -60 2 {front_text:{messages:['"later"','','','']}}`} {
		mixedBarrier(t, b, command)
	}
	if err := s.SaveServerData(); err != nil {
		t.Fatal(err)
	}
	copied, err := os.ReadFile(copiedData)
	if err != nil || !bytes.Equal(raw, copied) {
		t.Fatalf("completed data copy changed: %v", err)
	}
	_ = b.Close()
	stop()
	_, restarted, _ := serverDataRuntime(t, copiedWorld, copiedData)
	for _, player := range []struct {
		protocol int32
		name     string
		plain    int32
		xp       []byte
	}{{775, "DataOffline775", 4, xpA}, {776, "DataActive776", 6, xpB}} {
		t.Run(fmt.Sprint(player.protocol), func(t *testing.T) {
			c := compactSaveOpen(t, ctx, restarted, player.protocol, player.name)
			serverDataVerify(t, ctx, c, player.plain, player.xp)
		})
	}
}
