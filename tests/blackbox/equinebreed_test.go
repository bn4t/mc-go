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
		{"camels", "camel", "camel", "cactus", "camel"},
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
				// Camel.isTamed() is hardcoded true — the baby
				// writes Tame1b like its parents; every other
				// equine baby is untamed.
				wantTame := "Tame0b"
				if lane.babyType == "camel" {
					wantTame = "Tame1b"
				}
				if !strings.Contains(res.babyData, wantTame) {
					t.Fatalf("%s %s: baby Tame flag should be %s, got %q", tc.tag, lane.name, wantTame, res.babyData)
				}
				// setOffspringAttributes materializes the baby's
				// blended instances — jump_strength and max_health
				// only exist in the pack on the bred baby (the
				// parents' NBT summon never touched them). Camel's
				// getBreedOffspring skips the blend entirely — the
				// camel baby keeps the 32 supplier health.
				if lane.babyType == "camel" {
					if !strings.Contains(res.babyData, "Health32") {
						t.Fatalf("%s %s: camel baby keeps supplier health 32, got %q", tc.tag, lane.name, res.babyData)
					}
				} else {
					for _, attr := range []string{"jump_strength", "max_health"} {
						if !strings.Contains(res.babyData, "idminecraft:"+attr) {
							t.Fatalf("%s %s: baby missing blended attr %s, got %q", tc.tag, lane.name, attr, res.babyData)
						}
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

// camelFeedLane is Camel.handleEating's three arms probed per type:
// the love arm fires on any adult camel (isTamed is always true, but
// camel_husk's canFallInLove is false — rabbit_foot heals it only),
// the ageUp arm grows a baby 10 seconds (+200 ticks), and a second
// feed while all arms are inapplicable returns PASS — no hearts.
// summonX spaces the lanes so a previous lane's lingering corpse
// (killed entities still re-emit add_entity into a fresh session for
// their death-anim window) can't collide on the eid scan.
type camelFeedLane struct {
	name      string
	camelType string
	food      string
	summonX   float64
	nbt       string // extra summon NBT merged into the parents' tag
	wantHeart int    // expected event-18 count on the fed eid
	wantAgeGT int32  // Age must read above this (0 = skip the check)
	wantData  string // substring the fed camel's data get must show
}

func camelFeedObserve(t *testing.T, tag, addr string, lane camelFeedLane) (hearts int, age int32, data string) {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, "P")
	defer c.Close()
	sink := newChatSinkTP(pr, pw)
	sink.cap = 1 << 20
	time.Sleep(400 * time.Millisecond)

	for _, ty := range []string{"camel", "camel_husk"} {
		sendCommand(t, pw, "kill @e[type=minecraft:"+ty+"]")
	}
	sendCommand(t, pw, "fill 13 -61 7 22 -59 11 minecraft:glass hollow")
	nbt := "{PersistenceRequired:1b" + lane.nbt + "}"
	sx := fmt.Sprintf("%.1f", lane.summonX)
	sendCommand(t, pw, fmt.Sprintf("summon minecraft:%s %s -60 9.5 %s", lane.camelType, sx, nbt))
	sendCommand(t, pw, "tp @s "+sx+" -60 7.5")
	sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:"+lane.food)
	time.Sleep(600 * time.Millisecond)

	camelT, _ := server.EntityTypeID(lane.camelType)
	var eid int32 = -1
	var spawnLines []string
	for i := 0; i < 30 && eid <= 0; i++ {
		spawnLines = append(spawnLines, sink.take()...)
		eid = aniSpawnID(spawnLines, camelT, lane.summonX, 9.5)
		if eid <= 0 {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if eid <= 0 {
		t.Fatalf("%s: camel add_entity not observed", tag)
	}
	// Feed twice — the second feed's PASS arm (nothing applicable)
	// must not emit another heart.
	for i := 0; i < 2; i++ {
		var ib protocol.Buffer
		ib.VarInt(eid)
		ib.VarInt(0)
		ib.VarInt(0)
		ib.Bool(false)
		_ = pw.WritePacket(server.IDPlayInteractSB, ib.Bytes())
		time.Sleep(400 * time.Millisecond)
	}
	hearts = countEntityEvent(sink.take(), eid, 18)
	data = dataGetCompound(t, sink, pw, "@e[type=minecraft:"+lane.camelType+"]")
	age = parseAgeField(data)
	return hearts, age, data
}

// parseAgeField reads the Age int out of a flattened data-get reply:
// the visible text carries `Age-1973` (or `Age6000` on a cooldown).
// AgeLocked/AgeKey-like keys sort first, so scan for Age followed by
// a digit or a minus sign.
func parseAgeField(data string) int32 {
	i, off := -1, 0
	for {
		k := strings.Index(data[off:], "Age")
		if k < 0 {
			return 0
		}
		i = off + k
		j := i + 3
		if j < len(data) && (data[j] == '-' || (data[j] >= '0' && data[j] <= '9')) {
			break
		}
		off = i + 3
	}
	j := i + 3
	neg := false
	if j < len(data) && data[j] == '-' {
		neg = true
		j++
	}
	var v int32
	for ; j < len(data) && data[j] >= '0' && data[j] <= '9'; j++ {
		v = v*10 + int32(data[j]-'0')
	}
	if neg {
		return -v
	}
	return v
}

// TestCamelFeedParity pins Camel.mobInteract's fedFood arm: cactus
// for camels, rabbit_foot for camel_husk. The love arm needs no tame
// (Camel.isTamed is always true); the second feed on an already-loved,
// full-health adult is a PASS — no hearts, no consume.
func TestCamelFeedParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	lanes := []camelFeedLane{
		// camel_love: any adult camel loves (no tame needed). The
		// second feed is a PASS — the heart count stays 1.
		{"camel_love", "camel", "cactus", 14.5, "", 1, 0, ""},
		// camel_husk can't fall in love (canFallInLove false) — the
		// only applicable arm is heal: 30+2 hits the 32 max.
		{"husk_heal", "camel_husk", "rabbit_foot", 17.5, ",Health:30f", 0, 0, "Health32"},
		// Two feeds each ageUp(10)=+200 ticks — from -2000 the Age
		// reads around -1560..-1600 (plus passive +1/tick).
		{"baby_ageup", "camel", "cactus", 20.5, ",Age:-2000", 0, -1700, ""},
	}
	for _, tc := range []struct{ tag, addr string }{
		{"vanilla", targets.vanilla},
		{"mc-go", targets.mcgo},
	} {
		for _, lane := range lanes {
			t.Run(tc.tag+"_"+lane.name, func(t *testing.T) {
				hearts, age, data := camelFeedObserve(t, tc.tag, tc.addr, lane)
				if hearts != lane.wantHeart {
					t.Fatalf("%s %s: got %d in-love hearts, want %d", tc.tag, lane.name, hearts, lane.wantHeart)
				}
				if lane.wantAgeGT != 0 && age <= lane.wantAgeGT {
					t.Fatalf("%s %s: baby Age %d should have grown past %d (two ageUp(10) feeds), got %q", tc.tag, lane.name, age, lane.wantAgeGT, data)
				}
				if lane.wantData != "" && !strings.Contains(data, lane.wantData) {
					t.Fatalf("%s %s: data get missing %q, got %q", tc.tag, lane.name, lane.wantData, data)
				}
			})
		}
	}
}

// passengersCount decodes a set_passengers body (VarInt vehicle,
// VarInt count, VarInt ids) — returns the vehicle eid + seat count.
func passengersCount(body []byte) (veh int32, n int, ok bool) {
	r := protocol.NewPacketReader(body[1:])
	v, err := r.VarInt()
	if err != nil {
		return 0, 0, false
	}
	c, err := r.VarInt()
	if err != nil || c < 0 || c > 8 {
		return 0, 0, false
	}
	return v, int(c), true
}

// lastPassengerCount scans a batch for the newest set_passengers on
// vehEid and returns its seat count (-1 = none seen).
func lastPassengerCount(lines []string, vehEid int32) int {
	got := -1
	for _, l := range lines {
		body, ok := packetBody(l)
		if !ok || body[0] != 0x6B {
			continue
		}
		v, n, ok := passengersCount(body)
		if ok && v == vehEid {
			got = n
		}
	}
	return got
}

// TestCamelRideParity pins Camel.mobInteract's two-seat mount:
// getPassengers().size()<2 lets a second player board — each
// interact re-emits set_passengers with the grown passenger array.
func TestCamelRideParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P1", "P2"),
	}, nil)
	defer targets.cleanup()

	for _, tc := range []struct{ tag, addr string }{
		{"vanilla", targets.vanilla},
		{"mc-go", targets.mcgo},
	} {
		t.Run(tc.tag, func(t *testing.T) {
			c1, pr1, pw1 := joinPlayBlackbox(t, tc.addr, "P1")
			defer c1.Close()
			sink1 := newChatSinkTP(pr1, pw1)
			c2, pr2, pw2 := joinPlayBlackbox(t, tc.addr, "P2")
			defer c2.Close()
			_ = newChatSinkTP(pr2, pw2)
			time.Sleep(500 * time.Millisecond)

			sendCommand(t, pw1, "kill @e[type=minecraft:camel]")
			sendCommand(t, pw1, "fill 13 -61 7 18 -59 11 minecraft:glass hollow")
			sendCommand(t, pw1, "summon minecraft:camel 15.5 -60 9.5 {PersistenceRequired:1b}")
			sendCommand(t, pw1, "tp @s 16.5 -60 9.5")
			sendCommand(t, pw2, "tp @s 16.5 -60 8.5")
			time.Sleep(600 * time.Millisecond)

			camelT, _ := server.EntityTypeID("camel")
			var eid int32 = -1
			var spawnLines []string
			for i := 0; i < 30 && eid <= 0; i++ {
				spawnLines = append(spawnLines, sink1.take()...)
				eid = aniSpawnID(spawnLines, camelT, 15.5, 9.5)
				if eid <= 0 {
					time.Sleep(100 * time.Millisecond)
				}
			}
			if eid <= 0 {
				t.Fatalf("%s: camel add_entity not observed", tc.tag)
			}
			// P1 boards, then P2 — the second mount must land in
			// seat two, not be refused.
			var seatCounts []int
			for _, pw := range []*protocol.Writer{pw1, pw2} {
				var ib protocol.Buffer
				ib.VarInt(eid)
				ib.VarInt(0)
				ib.VarInt(0)
				ib.Bool(false)
				_ = pw.WritePacket(server.IDPlayInteractSB, ib.Bytes())
				time.Sleep(500 * time.Millisecond)
				seatCounts = append(seatCounts, lastPassengerCount(sink1.take(), eid))
			}
			if seatCounts[0] != 1 {
				t.Fatalf("%s: after P1 mounts, set_passengers should list 1 rider, got %d", tc.tag, seatCounts[0])
			}
			if seatCounts[1] != 2 {
				t.Fatalf("%s: camel seats two — after P2 mounts, set_passengers should list 2 riders, got %d", tc.tag, seatCounts[1])
			}
		})
	}
}
