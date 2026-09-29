package blackbox

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// breedLane is one equine breeding lane: the two parents' types, the
// item that puts them in love, and the baby's expected type.
type breedLane struct {
	name            string
	momType, paType string
	loveItem        string
	babyType        string
}

// breedResult is what one leg of the breed probe observed.
type breedResult struct {
	babySpawned bool   // add_entity of the baby type near the parents
	parentHeart int    // entity_event 18 count on the two parent eids
	babyData    string // the baby's whole-compound data get, as visible text
}

// equineBreedObserve runs the lane: summon two tamed adults 1.5 blocks
// apart (NBT summon skips finalizeSpawn, so vanilla leaves health at
// the 53 supplier base — canParent's health>=max gate passes on both
// servers), feed each its love item, and tick sprint until the breed
// contact (~60 ticks of approach). Parents are summoned at 15.5 and
// 17.0 so the pair sits inside the player's 8-block scan square.
func equineBreedObserve(t *testing.T, tag, addr string, lane breedLane) breedResult {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, "P")
	defer c.Close()
	sink := newChatSinkTP(pr, pw)
	sink.cap = 1 << 20
	time.Sleep(400 * time.Millisecond)

	for _, ty := range []string{lane.momType, lane.paType, lane.babyType} {
		sendCommand(t, pw, "kill @e[type=minecraft:"+ty+"]")
	}
	sendCommand(t, pw, "fill 13 -61 7 18 -59 11 minecraft:glass hollow")
	nbt := "{Tame:1b,PersistenceRequired:1b}"
	sendCommand(t, pw, fmt.Sprintf("summon minecraft:%s 15.5 -60 9.5 %s", lane.momType, nbt))
	sendCommand(t, pw, fmt.Sprintf("summon minecraft:%s 17.0 -60 9.5 %s", lane.paType, nbt))
	sendCommand(t, pw, "tp @s 16.25 -60 9.5")
	sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:"+lane.loveItem)
	time.Sleep(600 * time.Millisecond)

	momT, _ := server.EntityTypeID(lane.momType)
	paT, _ := server.EntityTypeID(lane.paType)
	babyT, _ := server.EntityTypeID(lane.babyType)
	var momEid, paEid int32 = -1, -1
	var spawnLines []string
	for i := 0; i < 30 && (momEid <= 0 || paEid <= 0); i++ {
		spawnLines = append(spawnLines, sink.take()...)
		// Resolve the two parents disjointly: each parent takes its
		// nearest add_entity of its own type, excluding the other's
		// already-assigned eid — one spawn can't fill both slots.
		momEid = aniSpawnID(spawnLines, momT, 15.5, 9.5)
		paEid = aniSpawnIDExcept(spawnLines, paT, 17.0, 9.5, momEid)
		if momEid <= 0 || paEid <= 0 {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if momEid <= 0 || paEid <= 0 {
		t.Fatalf("%s: parent add_entity not observed (mom=%d pa=%d)", tag, momEid, paEid)
	}
	// Feed each parent the love item — fedFood's in-love arm fires
	// entity_event 18 on the fed animal.
	var res breedResult
	for _, pid := range []int32{momEid, paEid} {
		var ib protocol.Buffer
		ib.VarInt(pid)
		ib.VarInt(0)
		ib.VarInt(0)
		ib.Bool(false)
		_ = pw.WritePacket(server.IDPlayInteractSB, ib.Bytes())
	}
	time.Sleep(700 * time.Millisecond)
	fed := sink.take()
	res.parentHeart = countEntityEvent(fed, momEid, 18) +
		countEntityEvent(fed, paEid, 18)
	if res.parentHeart < 2 {
		t.Fatalf("%s: feeding %s/%s got %d in-love hearts, want 2", tag, lane.momType, lane.paType, res.parentHeart)
	}

	sendCommand(t, pw, "tick sprint 200")
	var lines []string
	for i := 0; i < 8; i++ {
		time.Sleep(500 * time.Millisecond)
		lines = append(lines, sink.take()...)
	}
	for _, l := range lines {
		body, ok := packetBody(l)
		if !ok {
			continue
		}
		switch body[0] {
		case 0x22:
			if entityEventOn(body, momEid, 18) || entityEventOn(body, paEid, 18) {
				res.parentHeart++
			}
		case 0x01: // add_entity — baby spawns at the parent's position
			if eid := aniSpawnID([]string{l}, babyT, 15.5, 9.5); eid > 0 {
				res.babySpawned = true
			}
		}
	}
	// Each horse replies with its own compound; the baby is the one
	// with Age:-24000.
	got := dataGetCompound(t, sink, pw, "@e[type=minecraft:"+lane.babyType+"]")
	res.babyData = got
	return res
}

// aniSpawnIDExcept is aniSpawnID with one eid excluded — used so the
// two parent slots can never resolve to the same add_entity packet.
func aniSpawnIDExcept(lines []string, wantType int32, x, z float64, except int32) int32 {
	best, bestD := int32(-1), 4.0
	for _, l := range lines {
		i := strings.IndexByte(l, ':')
		if i < 0 || l[:i] != "0x1" {
			continue
		}
		r := protocol.NewPacketReader([]byte(l[i+1:]))
		eid, e1 := r.VarInt()
		if _, e2 := r.UUID(); e2 != nil {
			continue
		}
		typ, _ := r.VarInt()
		ex, e4 := r.Double()
		_, e5 := r.Double()
		ez, e6 := r.Double()
		if e1 != nil || e4 != nil || e5 != nil || e6 != nil {
			continue
		}
		if typ != wantType || eid == except {
			continue
		}
		d := math.Abs(ex-x) + math.Abs(ez-z)
		if d < bestD {
			best, bestD = eid, d
		}
	}
	return best
}

// countEntityEvent counts set_entity_event packets on eid with the
// given status byte inside a sink batch.
func countEntityEvent(lines []string, eid int32, status byte) int {
	n := 0
	for _, l := range lines {
		body, ok := packetBody(l)
		if !ok || body[0] != 0x22 {
			continue
		}
		if entityEventOn(body, eid, status) {
			n++
		}
	}
	return n
}

// entityEventOn: entity_event packs Int eid + Byte status.
func entityEventOn(body []byte, eid int32, status byte) bool {
	if len(body) < 6 {
		return false
	}
	v := int32(body[1])<<24 | int32(body[2])<<16 | int32(body[3])<<8 | int32(body[4])
	return v == eid && body[5] == status
}

// TestEquineBreedParity pins Animal.spawnChildFromBreeding on the
// equine family: canParent gates (tamed, full health, no vehicle/
// passenger), getBreedOffspring's type and rolls — horse×donkey makes
// a mule, the baby's three attributes blend the parents' bases, the
// horse baby's Variant packs the skin/marking mix — and the hearts
// entity_event 18 lands on the parent, not the baby.
func TestEquineBreedParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	lanes := []breedLane{
		{"horses", "horse", "horse", "golden_carrot", "horse"},
		{"mule", "horse", "donkey", "golden_carrot", "mule"},
		{"llamas", "llama", "llama", "hay_block", "llama"},
	}
	for _, tc := range []struct{ tag, addr string }{
		{"vanilla", targets.vanilla},
		{"mc-go", targets.mcgo},
	} {
		for _, lane := range lanes {
			t.Run(tc.tag+"_"+lane.name, func(t *testing.T) {
				res := equineBreedObserve(t, tc.tag, tc.addr, lane)
				if !res.babySpawned {
					t.Fatalf("%s %s: no %s baby add_entity after breed window", tc.tag, lane.name, lane.babyType)
				}
				// 2 feed hearts + 1 breed heart on the parent — the
				// baby never gets one.
				if res.parentHeart < 3 {
					t.Fatalf("%s %s: %d parent hearts, want ≥3 (2 feed + 1 breed)", tc.tag, lane.name, res.parentHeart)
				}
				if !strings.Contains(res.babyData, "Age-") {
					t.Fatalf("%s %s: baby should carry a negative Age, got %q", tc.tag, lane.name, res.babyData)
				}
				if !strings.Contains(res.babyData, "Tame0b") {
					t.Fatalf("%s %s: baby should spawn untamed, got %q", tc.tag, lane.name, res.babyData)
				}
				// setOffspringAttributes materializes the baby's
				// blended instances — jump_strength and max_health
				// only exist in the pack on the bred baby (the
				// parents' NBT summon never touched them).
				for _, attr := range []string{"jump_strength", "max_health"} {
					if !strings.Contains(res.babyData, "idminecraft:"+attr) {
						t.Fatalf("%s %s: baby missing blended attr %s, got %q", tc.tag, lane.name, attr, res.babyData)
					}
				}
				if lane.babyType == "horse" && !strings.Contains(res.babyData, "Variant") {
					t.Fatalf("%s %s: horse baby should carry a Variant int, got %q", tc.tag, lane.name, res.babyData)
				}
				if lane.babyType == "llama" && !strings.Contains(res.babyData, "Strength") {
					t.Fatalf("%s %s: llama baby should carry a Strength int, got %q", tc.tag, lane.name, res.babyData)
				}
			})
		}
	}
}
