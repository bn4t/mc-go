package blackbox

import (
	"encoding/binary"
	"fmt"
	"math"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Default, unscaled mounts isolate static type dimensions and seat precision.
func TestStaticMountDimensionsSeatsParity(t *testing.T) {
	const actor = "StaticMountRead"
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2", "command-spam-threshold-seconds": "0"}, map[string]string{"ops.json": opsJSONFor(actor)}, nil)
	defer targets.cleanup()
	run := func(addr string) {
		c, pr, pw, actorID := joinPlayEid(t, addr, actor)
		defer c.Close()
		awaitPlayReadyBlackbox(t, addr, c, pr, pw)
		sink := newChatSinkTP(pr, pw)
		serial := 0
		var orbID, itemID int32
		seenUUIDs := map[[16]byte]int32{}
		var orbTakes, itemTakes int32
		observe := func(lines []string) []string {
			for _, line := range lines {
				prefix, data, ok := strings.Cut(line, ":")
				if !ok {
					continue
				}
				id, err := strconv.ParseInt(prefix, 0, 32)
				if err != nil {
					continue
				}
				r := protocol.NewPacketReader([]byte(data))
				switch int32(id) {
				case server.IDPlaySpawnEntity:
					eid, e := r.VarInt()
					uid, e2 := r.UUID()
					typ, e3 := r.VarInt()
					x, e4 := r.Double()
					y, e5 := r.Double()
					z, e6 := r.Double()
					if e == nil && e2 == nil && e3 == nil && e4 == nil && e5 == nil && e6 == nil {
						seenUUIDs[uid] = eid
						if typ == mustEType(t, "experience_orb") && x == 10.5 && y == 162.6 && z == .5 {
							orbID = eid
						} else if typ == mustEType(t, "item") && x == 10.5 && y == 162.6 && z == .5 {
							itemID = eid
						}
					}
				case server.IDPlayTakeItem:
					eid, e := r.VarInt()
					recipient, e2 := r.VarInt()
					count, e3 := r.VarInt()
					if e == nil && e2 == nil && e3 == nil && ((orbID != 0 && eid == orbID) || (itemID != 0 && eid == itemID)) {
						if recipient != actorID {
							t.Errorf("collect recipient%d want%d", recipient, actorID)
						}
						if eid == orbID {
							orbTakes += count
						} else {
							itemTakes += count
						}
					}
				}
			}
			return lines
		}
		collect := func(marker string) []string { return observe(waitForExactSayBB(t, sink, marker, 30*time.Second)) }
		command := func(cmd string) []string {
			serial++
			receipt := fmt.Sprintf("STATIC_MOUNT_RECEIPT_%d", serial)
			sendCommand(t, pw, cmd)
			sendCommand(t, pw, "say "+receipt)
			return collect(receipt)
		}
		command("gamemode creative @s")
		command("tp @s 4.5 162.0 0.5")
		command("tick freeze")
		command("fill 1 161 -1 13 170 1 air")
		if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
			t.Fatal(err)
		}
		setupPlayerTickBarrierBB(t, pw)
		waitPlayerTicksBB(t, pw, collect, "StaticMountInitial", 3)
		command("scoreboard objectives add StaticScale dummy")
		for i, tc := range []struct {
			kind          string
			seatY, probeX float64
			take, narrow  bool
		}{{"horse", 162.84375002384186, 6.2, true, true}, {"donkey", 162.5124999523163, 6.2, true, true}, {"mule", 162.61249997615815, 6.2, true, true}, {"cat", 161.91249998807908, 6.2, false, false}, {"cow", 162.76874997615815, 6.3, false, false}} {
			command("ride @s dismount")
			command("kill @e[tag=StaticMount]")
			command("kill @e[type=experience_orb]")
			command("kill @e[type=item]")
			command("tp @s 4.5 162.0 0.5")
			command(fmt.Sprintf(`summon %s 4.5 162.0 0.5 {Tags:["StaticMount"],NoGravity:1b,NoAI:1b,Tame:1b,Motion:[0d,0d,0d]}`, tc.kind))
			mount := "@e[type=" + tc.kind + ",tag=StaticMount,limit=1]"
			command("execute store result score #scale StaticScale run attribute " + mount + " minecraft:scale get 1000")
			if lines := command("execute if score #scale StaticScale matches 1000 run say STATIC_MOUNT_SCALE"); !structureSaidBB(lines, "STATIC_MOUNT_SCALE") {
				t.Fatal("default effective scale witness missing")
			}
			shulkerStepTicks(t, pw, sink, 1)
			mounted := false
			for _, line := range command("ride @s mount " + mount) {
				key, _, ok := componentArgs(line)
				if ok && key == "commands.ride.mount.success" {
					mounted = true
				}
			}
			if !mounted {
				t.Fatal("mount command failed")
			}
			waitPlayerTicksBB(t, pw, collect, fmt.Sprintf("StaticMountSeat%d", i), 4)
			px, py, pz, ok := dataGetPos(t, sink, pw, "@s")
			if !ok {
				t.Fatal("missing actual rider position")
			}
			for axis, v := range [3]float64{px, py, pz} {
				want := [3]float64{4.5, tc.seatY, .5}[axis]
				if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v-want) > 1e-11 {
					t.Errorf("%s %s rider axis%d %.17g want%.17g", addr, tc.kind, axis, v, want)
				}
			}
			hx, hy, hz, ok := dataGetPos(t, sink, pw, mount)
			if !ok || hx != 4.5 || hy != 162 || hz != .5 {
				t.Fatalf("mount moved [%g %g %g] valid%t", hx, hy, hz, ok)
			}
			narrow := structureSaidBB(command("execute if entity @e[type="+tc.kind+",tag=StaticMount,x=5.0,y=162,z=0,dx=0,dy=0,dz=0] run say STATIC_MOUNT_BOX"), "STATIC_MOUNT_BOX")
			if narrow != tc.narrow {
				t.Errorf("%s %s bounding-box probe%t want%t", addr, tc.kind, narrow, tc.narrow)
			}
			beforeState, before := dataGetSel(t, sink, pw, "@s", "XpTotal")
			if beforeState != "query" || math.IsNaN(before) || math.IsInf(before, 0) {
				t.Fatal("missing baseline XP")
			}
			orbID, itemID, orbTakes, itemTakes = 0, 0, 0, 0

			command(`summon experience_orb 10.5 162.6 0.5 {Tags:["StaticReachOrb"],Count:1,Value:1s,NoGravity:1b,Motion:[0d,0d,0d],Age:0s}`)
			command(`summon item 10.5 162.6 0.5 {Tags:["StaticReachItem"],NoGravity:1b,Motion:[0d,0d,0d],PickupDelay:0s,Item:{id:"minecraft:stone",count:1}}`)
			orb := "@e[type=experience_orb,tag=StaticReachOrb,limit=1]"
			item := "@e[type=item,tag=StaticReachItem,limit=1]"
			deadline := time.Now().Add(5 * time.Second)
			for (orbID == 0 || itemID == 0) && time.Now().Before(deadline) {
				command("say STATIC_MOUNT_PAIR")
				time.Sleep(20 * time.Millisecond)
			}
			if orbID == 0 || itemID == 0 {
				t.Fatal("far sources never paired")
			}
			for j, source := range []struct {
				sel string
				id  int32
			}{{orb, orbID}, {item, itemID}} {
				var actualUUID [16]byte
				found := false
				for uid, id := range seenUUIDs {
					if id == source.id {
						actualUUID = uid
						found = true
						break
					}
				}
				if !found {
					t.Fatal("paired source UUID missing")
				}
				var words [4]int32
				for word := range words {
					words[word] = int32(binary.BigEndian.Uint32(actualUUID[word*4:]))
				}
				bound := strings.TrimSuffix(source.sel, "]") + fmt.Sprintf(",nbt={UUID:[I;%d,%d,%d,%d]}]", words[0], words[1], words[2], words[3])
				marker := fmt.Sprintf("STATIC_SOURCE_UUID_%d_%d", i, j)
				if lines := command("execute if entity " + bound + " run say " + marker); !structureSaidBB(lines, marker) {
					t.Fatalf("source%d UUID predicate did not match actual spawn UUID%x", source.id, actualUUID)
				}
			}
			if state, count := dataGetSel(t, sink, pw, orb, "Count"); state != "query" || count != 1 {
				t.Fatal("far orb precondition missing")
			}
			if state, count := dataGetSel(t, sink, pw, item, "Item.count"); state != "query" || count != 1 {
				t.Fatal("far item precondition missing")
			}
			command(fmt.Sprintf("tp %s %.17g 162.6 0.5", orb, tc.probeX))
			command(fmt.Sprintf("tp %s %.17g 162.6 0.5", item, tc.probeX))
			waitPlayerTicksBB(t, pw, collect, fmt.Sprintf("StaticMountPickup%d", i), 4)
			afterState, after := dataGetSel(t, sink, pw, "@s", "XpTotal")
			if afterState != "query" || math.IsNaN(after) || math.IsInf(after, 0) {
				t.Fatal("missing final XP")
			}
			orbMarker, itemMarker := fmt.Sprintf("STATIC_ORB_EXISTS_%d", i), fmt.Sprintf("STATIC_ITEM_EXISTS_%d", i)
			orbExists := structureSaidBB(command("execute if entity "+orb+" run say "+orbMarker), orbMarker)
			itemExists := structureSaidBB(command("execute if entity "+item+" run say "+itemMarker), itemMarker)
			want := int32(0)
			if tc.take {
				want = 1
			}
			if after-before != float64(want) || orbTakes != want || itemTakes != want || orbExists == tc.take || itemExists == tc.take {
				t.Errorf("%s %s XPdelta%g orbTake%d itemTake%d orbExists%t itemExists%t want take%d", addr, tc.kind, after-before, orbTakes, itemTakes, orbExists, itemExists, want)
			}
			if !tc.take {
				for _, source := range []string{orb, item} {
					x, y, z, ok := dataGetPos(t, sink, pw, source)
					if !ok || x != tc.probeX || y != 162.6 || z != .5 {
						t.Fatalf("negative source not at probe [%g %g %g] valid%t", x, y, z, ok)
					}
				}
			}
			t.Logf("checked %s %s riderY%.17g bbox%t XPdelta%g orbTake%d itemTake%d", addr, tc.kind, py, narrow, after-before, orbTakes, itemTakes)
		}
	}
	run(targets.vanilla)
	run(targets.mcgo)
}
