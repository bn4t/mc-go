package blackbox

import (
	"encoding/binary"
	"fmt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestFrozenMountedScaleCallbackParity(t *testing.T) {
	files := map[string]string{"ops.json": opsJSONFor("FM_Observer")}
	dir := t.TempDir()
	body := ""
	for _, tag := range []string{"Root", "Child", "Free", "NPCRoot", "NPCChild"} {
		for _, field := range []string{"Age", "Fire", "PortalCooldown", "Pos", "UUID"} {
			body += fmt.Sprintf("data modify storage fm:probe %s%s set from entity @e[tag=FM%s,limit=1] %s\n", tag, field, tag, field)
		}
	}
	for _, tag := range []string{"Root", "Child", "Free", "NPCRoot", "NPCChild"} {
		body += fmt.Sprintf("execute store result storage fm:probe %sScale double 0.001 run attribute @e[tag=FM%s,limit=1] minecraft:scale get 1000\nexecute at @e[tag=FM%s,limit=1] run summon marker ~ ~ ~ {Tags:[FMAtomicMarker]}\ndata modify storage fm:probe %sLive set from entity @e[tag=FMAtomicMarker,limit=1] Pos\nkill @e[tag=FMAtomicMarker]\n", tag, tag, tag, tag)
	}
	for _, tag := range []string{"Root", "Child", "Free"} {
		body += fmt.Sprintf("execute at @e[tag=FM%s,limit=1] positioned ~1 ~0.25 ~ store success storage fm:probe %sWide byte 1 if entity @e[tag=FM%s,dx=0,dy=0,dz=0]\n", tag, tag, tag)
	}
	body += "execute store result storage fm:probe observer int 1 run scoreboard players get FM_Observer BBPlayerTick\nexecute store result storage fm:probe playerA int 1 run scoreboard players get FM_A BBPlayerTick\nexecute store result storage fm:probe playerB int 1 run scoreboard players get FM_B BBPlayerTick\nexecute store result storage fm:probe world long 1 run time query gametime\nsay FM_SNAPSHOT\n"
	for p, b := range map[string]string{"pack.mcmeta": `{"pack":{"description":"frozen bodies","min_format":107,"max_format":107}}`, "data/fm/function/snapshot.mcfunction": body} {
		rel := "datapacks/fm/" + p
		files["world/"+rel] = b
		loc := filepath.Join(dir, rel)
		if e := os.MkdirAll(filepath.Dir(loc), 0755); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(loc, []byte(b), 0644); e != nil {
			t.Fatal(e)
		}
	}
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2", "command-spam-threshold-seconds": "0"}, files, func(s *server.Server) { s.WorldDir = dir })
	defer targets.cleanup()
	run := func(addr string) {
		c, pr, pw, _ := joinPlayEid(t, addr, "FM_Observer")
		defer c.Close()
		awaitPlayReadyBlackbox(t, addr, c, pr, pw)
		sink := fmConfirmOnlySink(pr, pw)
		type spawn struct {
			id, typ int32
			uuid    [16]byte
		}
		var fresh []spawn
		phase := "setup"
		wire := map[string][]string{}
		watch := map[int32]string{}
		links := map[int32][]int32{}
		fold := func(lines []string) []string {
			for _, line := range lines {
				prefix, raw, ok := strings.Cut(line, ":")
				if !ok {
					continue
				}
				id, err := strconv.ParseInt(prefix, 0, 32)
				if err != nil {
					continue
				}
				r := protocol.NewPacketReader([]byte(raw))
				switch int32(id) {
				case server.IDPlaySpawnEntity:
					e, a := r.VarInt()
					u, b := r.UUID()
					typ, c := r.VarInt()
					if a == nil && b == nil && c == nil {
						fresh = append(fresh, spawn{e, typ, u})
					}
				case server.LatestVersion().IDs.PlayMoveEntityPos, server.LatestVersion().IDs.PlayMoveEntityPosRot:
					e, a := r.VarInt()
					x, b := r.Short()
					y, c := r.Short()
					z, d := r.Short()
					if a == nil && b == nil && c == nil && d == nil && watch[e] != "" {
						wire[phase] = append(wire[phase], fmt.Sprintf("%s move %d %d %d", watch[e], x, y, z))
					}
				case server.LatestVersion().IDs.PlayEntityTeleportCB:
					e, a := r.VarInt()
					x, b := r.Double()
					y, c := r.Double()
					z, d := r.Double()
					if a == nil && b == nil && c == nil && d == nil && watch[e] != "" {
						wire[phase] = append(wire[phase], fmt.Sprintf("%s absolute %.12f %.12f %.12f", watch[e], x, y, z))
					}
				case server.IDPlayRemoveEntity:
					n, err := r.VarInt()
					if err == nil && n >= 0 && n <= 4096 {
						for i := int32(0); i < n; i++ {
							eid, err := r.VarInt()
							if err != nil {
								t.Fatal(err)
							}
							if watch[eid] != "" {
								t.Logf("TRACK_REMOVE phase%s id%d %s", phase, eid, watch[eid])
							}
						}
					}
				case server.LatestVersion().IDs.PlaySetPassengers:
					e, a := r.VarInt()
					n, b := r.VarInt()
					if a != nil || b != nil || n < 0 || n > 16 {
						t.Fatal("bad passengers")
					}
					row := make([]int32, n)
					for i := range row {
						row[i], err = r.VarInt()
						if err != nil {
							t.Fatal(err)
						}
					}
					links[e] = row
				}
			}
			return lines
		}
		seq := 0
		cmd := func(s string) []string {
			seq++
			tag := fmt.Sprintf("FM_RECEIPT%d", seq)
			sendCommand(t, pw, s)
			sendCommand(t, pw, "say "+tag)
			lines := fold(waitForExactSayBB(t, sink, tag, 30*time.Second))
			if strings.HasPrefix(s, "ride ") {
				for _, line := range lines {
					key, _, ok := componentArgs(line)
					if ok {
						t.Logf("ACTION %s => %s %s", s, key, chatTextPieces(line))
					}
				}
			}
			return lines
		}
		ca, pra, pwa, actorA := joinPlayEid(t, addr, "FM_A")
		defer ca.Close()
		awaitPlayReadyBlackbox(t, addr, ca, pra, pwa)
		sinkA := fmConfirmOnlySink(pra, pwa)
		_ = sinkA
		c2, pr2, pw2, actorB := joinPlayEid(t, addr, "FM_B")
		defer c2.Close()
		awaitPlayReadyBlackbox(t, addr, c2, pr2, pw2)
		// Keep B's reader draining throughout owner-frame waits.
		sink2 := fmConfirmOnlySink(pr2, pw2)
		_ = sink2
		cmd("tick freeze")
		cmd("tp FM_Observer 0.5 162 -4.5")
		cmd("tp FM_A 0.5 162 0.5")
		cmd("tp FM_B 1.5 162 0.5")
		setupPlayerTickBarrierBB(t, pw)
		observed := map[string]spawn{}
		for i, tag := range []string{"Root", "Child", "Free", "NPCRoot", "NPCChild"} {
			typ := "cow"
			if tag == "Root" || tag == "Free" || tag == "Child" {
				typ = "camel"
			}
			fresh = nil
			cmd(fmt.Sprintf("summon %s %d.5 162 0.5 {Tags:[FM%s],NoAI:1b,NoGravity:1b,Invulnerable:1b,Silent:1b,Age:10000,Fire:10000s,PortalCooldown:10000}", typ, i*3, tag))
			for _, e := range fresh {
				if e.typ == mustEType(t, typ) {
					observed[tag] = e
				}
			}
			e, ok := observed[tag]
			watch[e.id] = tag
			if !ok {
				t.Fatal("actual body spawn absent", tag)
			}
			u := e.uuid
			n := fmt.Sprintf("UUID:[I;%d,%d,%d,%d]", int32(binary.BigEndian.Uint32(u[0:4])), int32(binary.BigEndian.Uint32(u[4:8])), int32(binary.BigEndian.Uint32(u[8:12])), int32(binary.BigEndian.Uint32(u[12:16])))
			if !structureSaidBB(cmd("execute if entity @e[tag=FM"+tag+",limit=1,nbt={"+n+"}] run say FM_ID"), "FM_ID") {
				t.Fatal("body UUID mismatch", tag)
			}
		}
		sel := func(tag string) string { return "@e[tag=FM" + tag + ",limit=1]" }
		cmd("ride " + sel("NPCChild") + " mount " + sel("NPCRoot"))
		type snapshot struct {
			nums      map[string]float64
			positions map[string][3]float64
		}
		snap := func(label string) snapshot {
			lines := cmd("function fm:snapshot")
			if !structureSaidBB(lines, "FM_SNAPSHOT") {
				t.Fatal("snapshot receipt absent")
			}
			out := snapshot{map[string]float64{}, map[string][3]float64{}}
			fields := []string{"observer", "playerA", "playerB", "world", "RootWide", "ChildWide", "FreeWide"}
			for _, tag := range []string{"Root", "Child", "Free", "NPCRoot", "NPCChild"} {
				for _, f := range []string{"Age", "Fire", "PortalCooldown", "Scale", "Pos", "Live"} {
					fields = append(fields, tag+f)
				}
			}
			for _, field := range fields {
				found := false
				for _, line := range cmd("data get storage fm:probe " + field) {
					if strings.Contains(line, "data.storage.query") {
						found = true
						if strings.HasSuffix(field, "Pos") || strings.HasSuffix(field, "Live") {
							v, ok := parseSNBT(line)
							row, yes := v.([]any)
							if !ok || !yes || len(row) != 3 {
								t.Fatal("missing position", field)
							}
							var pos [3]float64
							for i := range pos {
								n, ok := hrNum(row[i])
								if !ok {
									t.Fatal("bad coordinate")
								}
								pos[i] = n
							}
							out.positions[field] = pos
						} else {
							n := lastNumber(chatTextPieces(line))
							if n < 0 {
								t.Fatal("bad scalar", field)
							}
							out.nums[field] = n
						}
					}
				}
				if !found {
					t.Fatalf("snapshot %s absent", field)
				}
			}
			t.Logf("%s %s scalars%v positions%v links%v", addr, label, out.nums, out.positions, links)
			return out
		}
		frames := func(label string) {
			waitPlayerTicksBB(t, pw, func(tag string) []string { return fold(waitForExactSayBB(t, sink, tag, 30*time.Second)) }, label, 30)
		}
		checkLinks := func(root, child []int32) {
			for attempt := 0; attempt < 8; attempt++ {
				cmd("say FM_LINK_BARRIER")
				if fmt.Sprint(links[observed["Root"].id]) == fmt.Sprint(root) && fmt.Sprint(links[observed["Child"].id]) == fmt.Sprint(child) && fmt.Sprint(links[observed["NPCRoot"].id]) == fmt.Sprint([]int32{observed["NPCChild"].id}) {
					return
				}

				for _, player := range []string{"FM_A", "FM_B"} {
					for _, line := range cmd("data get entity " + player + " Pos") {
						if strings.Contains(line, "data.entity.query") {
							t.Logf("OBSERVER_POS %s %s", player, chatTextPieces(line))
						}
					}
				}
				t.Logf("WAIT root%v child%v want%v/%v authoritativeA%t B%t", links[observed["Root"].id], links[observed["Child"].id], root, child, structureSaidBB(cmd("execute as FM_A on vehicle run say FM_A_VEH"), "FM_A_VEH"), structureSaidBB(cmd("execute as FM_B on vehicle run say FM_B_VEH"), "FM_B_VEH"))
				frames(fmt.Sprintf("link%d_%d", seq, attempt))
			}
			t.Fatalf("ordered links root%v child%v want%v/%v", links[observed["Root"].id], links[observed["Child"].id], root, child)
		}
		window := func(label string, ticking ...string) snapshot {
			phase = label
			a := snap(label + "Before")
			frames(label)
			b := snap(label + "After")
			d := b.nums["playerA"] - a.nums["playerA"]
			if d < 30 || b.nums["observer"]-a.nums["observer"] != d || b.nums["playerB"]-a.nums["playerB"] != d || b.nums["world"] != a.nums["world"] {
				t.Fatal("owner/world receipt invalid")
			}
			for _, tag := range []string{"Root", "Child", "Free", "NPCRoot", "NPCChild"} {
				want := 0.0
				for _, yes := range ticking {
					if tag == yes {
						want = -d
					}
				}
				for _, f := range []string{"Age", "Fire", "PortalCooldown"} {
					got := b.nums[tag+f] - a.nums[tag+f]
					if got != want {
						if want != 0 {
							t.Logf("RETAINED_BODY_GAP %s %s %s delta%g want%g in%g ownerframes", addr, label, tag+f, got, want, d)
						} else {
							t.Errorf("frozen control %s %s delta%g want0", label, tag+f, got)
						}
					}
				}
			}
			if label == "npcSiblingScale" && (b.positions["ChildLive"] != a.positions["ChildLive"] || b.positions["ChildPos"] != a.positions["ChildPos"]) {
				t.Errorf("riding callback changed anchor before%v/%v after%v/%v", a.positions["ChildLive"], a.positions["ChildPos"], b.positions["ChildLive"], b.positions["ChildPos"])
			}
			return b
		}
		checkLinks(nil, nil)
		window("unmounted")
		cmd("ride FM_A mount " + sel("Root"))
		checkLinks([]int32{actorA}, nil)
		window("directOne", "Root")
		cmd("ride FM_B mount " + sel("Root"))
		checkLinks([]int32{actorA, actorB}, nil)
		window("directTwo", "Root")
		phase = "scaleCommand"
		cmd("attribute " + sel("Root") + " minecraft:scale base set 1.5")
		cmd("attribute " + sel("Free") + " minecraft:scale base set 1.5")
		b := window("directScale", "Root")
		if b.nums["RootScale"] != 1.5 || b.nums["FreeScale"] != 1.5 {
			t.Fatal("effective scale receipt absent")
		}
		if b.positions["RootLive"][1] != 161.40625 || b.positions["RootPos"] != b.positions["RootLive"] {
			t.Errorf("mounted live resize missing %v/%v", b.positions["RootLive"], b.positions["RootPos"])
		}
		if b.positions["FreeLive"][1] != 162 || b.nums["FreeWide"] != 0 {
			t.Error("unmounted pending scale consumed")
		}
		if b.nums["RootWide"] != 1 {
			t.Error("mounted scale box not refreshed")
		}
		cmd("ride FM_A dismount")
		cmd("ride FM_B dismount")
		checkLinks(nil, nil)
		window("unlinked")
		cmd("ride " + sel("Child") + " mount " + sel("Root"))
		cmd("ride FM_A mount " + sel("Child"))
		checkLinks([]int32{observed["Child"].id}, []int32{actorA})
		window("indirectOne", "Root", "Child")
		cmd("ride FM_B mount " + sel("Child"))
		checkLinks([]int32{observed["Child"].id}, []int32{actorA, actorB})
		window("indirectTwo", "Root", "Child")
		cmd("ride FM_A dismount")
		cmd("ride FM_B dismount")
		checkLinks([]int32{observed["Child"].id}, nil)
		cmd("ride " + sel("Child") + " dismount")
		checkLinks(nil, nil)
		cmd("ride FM_A mount " + sel("Root"))
		checkLinks([]int32{actorA}, nil)
		cmd("ride " + sel("Child") + " mount " + sel("Root"))
		checkLinks([]int32{actorA, observed["Child"].id}, nil)
		window("npcSibling", "Root", "Child")
		cmd("attribute " + sel("Child") + " minecraft:scale base set 1.5")
		b = window("npcSiblingScale", "Root", "Child")
		if b.nums["ChildScale"] != 1.5 {
			t.Fatal("child effective scale absent")
		}
		if b.nums["ChildWide"] != 1 {
			t.Error("NPC sibling scale callback absent")
		}
		phase = "step8"
		a := snap("stepBefore")
		cmd("tick step 8")
		deadline := time.Now().Add(15 * time.Second)
		var z snapshot
		for time.Now().Before(deadline) {
			z = snap("stepPoll")
			d := z.nums["world"] - a.nums["world"]
			if d > 8 {
				t.Fatal("step overrun")
			}
			if d == 8 {
				break
			}
		}
		if z.nums["world"]-a.nums["world"] != 8 {
			t.Fatal("step8 missing")
		}
		d := z.nums["playerA"] - a.nums["playerA"]
		if z.nums["observer"]-a.nums["observer"] != d || z.nums["playerB"]-a.nums["playerB"] != d {
			t.Fatal("step owner clock mismatch")
		}
		for _, tag := range []string{"Root", "Child", "Free", "NPCRoot", "NPCChild"} {
			want := -8.0
			if tag == "Root" || tag == "Child" {
				want = -d
			}
			for _, f := range []string{"Age", "Fire", "PortalCooldown"} {
				if got := z.nums[tag+f] - a.nums[tag+f]; got != want {
					if tag == "Root" || tag == "Child" {
						t.Logf("RETAINED_BODY_GAP step8 %s delta%g want%g owner%g", tag+f, got, want, d)
					} else {
						t.Errorf("step8 control %s delta%g want%g", tag+f, got, want)
					}
				}
			}
		}

		t.Logf("%s wire%v", addr, wire)
		count := 0
		for phase, events := range wire {
			for _, event := range events {
				if !strings.HasPrefix(event, "Root ") {
					continue
				}
				if event == "Root move 0 0 0" {
					continue
				}
				if (phase != "scaleCommand" && phase != "directScale") || event != "Root move 0 -2432 0" {
					t.Errorf("root callback packet phase%s value%s", phase, event)
				}
				count++
			}
		}
		if count != 1 {
			t.Errorf("root callback nonzero packet count%d want1", count)
		}
	}
	run(targets.vanilla)
	run(targets.mcgo)
}

// Vehicle-driven movement does not seed the generic sink's cached teleport
// coordinates. This observer acknowledges teleports without inventing a
// client movement echo from a packet-relative coordinate.
func fmConfirmOnlySink(pr *protocol.Reader, pw *protocol.Writer) *chatSink {
	s := &chatSink{ids: map[int32]int{}, cap: 4096}
	go func() {
		for {
			p, err := pr.ReadPacket()
			if err != nil {
				s.mu.Lock()
				s.err = err
				s.mu.Unlock()
				return
			}
			s.mu.Lock()
			s.total++
			s.ids[p.ID]++
			if len(p.Data) < s.cap {
				s.chat = append(s.chat, fmt.Sprintf("%#x:%s", p.ID, string(p.Data)))
			}
			s.mu.Unlock()
			switch p.ID {
			case server.IDPlayKeepAliveCB:
				err = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
			case server.IDPlayChunkBatchDone:
				err = ackChunkBatch(pw)
			case server.IDPlaySyncPos:
				r := protocol.NewPacketReader(p.Data)
				var id int32
				id, err = r.VarInt()
				if err == nil {
					var b protocol.Buffer
					b.VarInt(id)
					err = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
				}
			}
			if err != nil {
				s.mu.Lock()
				s.err = err
				s.mu.Unlock()
				return
			}
		}
	}()
	return s
}
