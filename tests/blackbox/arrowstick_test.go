// Blackbox: AbstractArrow's in-ground lifecycle — the 0.05-into-the-
// face stick site, shakeTime=7 window, life→1200 despawn, lastState
// un-stick, water inertia (0.6 pre-move drag), and the playerTouch
// pickup gate — pinned by driving the identical scenario against
// vanilla 26.2 and mc-go and comparing NBT probes, motion samples,
// and the take/remove packet stream.
//
// Vanilla oracle: AbstractArrow.tick (shake-- head, inGround →
// lastState-mismatch un-stick else tickDespawn, inGroundTime++,
// flying → inWater*0.6 pre-move, !inWater*0.99+gravity post-move),
// onHitBlock (setPos(pos − signum(normal)·0.05), shakeTime=7, crit/
// pierce reset, hit-ground SoundEvent), playerTouch (inGround &&
// shakeTime≤0 → Pickup ordinal gate → player.take + discard), and
// addAdditionalSaveData's arrow tail (life/inBlockState/shake/
// inGround/pickup/damage/crit/PierceLevel/SoundEvent/item).
package blackbox

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

var nbtTokenRe = regexp.MustCompile(`minecraft:[a-z._]+`)

// dataGetFloats returns the scalar element queries for a list path —
// Pos/Motion become Pos[0..2] so each reply is a single number.
func dataGetFloats(t *testing.T, sink *chatSink, pw *protocol.Writer, sel, path string) []float64 {
	t.Helper()
	out := make([]float64, 0, 3)
	for i := 0; i < 3; i++ {
		_, v := dataGetSel(t, sink, pw, sel, path+"["+strconv.Itoa(i)+"]")
		out = append(out, v)
	}
	return out
}

// dataGetTokens returns the last minecraft:* id token before the
// translate key — the value component's token (name-hover tokens
// come earlier in the reply).
func dataGetTokens(t *testing.T, sink *chatSink, pw *protocol.Writer, sel, path string) []string {
	t.Helper()
	line := dataGetLine(t, sink, pw, sel, path)
	if i := strings.Index(line, "data.entity.query"); i >= 0 {
		line = line[:i]
	}
	toks := nbtTokenRe.FindAllString(line, -1)
	if len(toks) == 0 {
		return nil
	}
	return toks[len(toks)-1:]
}

// arrowLeg records everything the per-leg run observed.
type arrowLeg struct {
	// Lane A — wall stick.
	posA      []float64
	motA      []float64
	inGroundA float64
	stuckA    []string // inBlockState tokens
	shakeA    float64
	lifeA     string // data-get status for life
	pickupA   float64
	damageA   float64
	critA     float64
	pierceA   float64
	soundA    []string // SoundEvent tokens
	itemA     []string
	stickSnd  int // entity.arrow.hit (85 → wire 86) packets

	// Lane B — un-stick after the wall breaks.
	posB1      []float64
	posB2      []float64
	inGroundB2 float64

	// Despawn — remove_entities carrying the lane eids.
	removedA, removedB bool

	// Lane D — water inertia, sampled via NBT after a fixed sprint.
	waterMot []float64
	waterPos []float64

	// Lane E — pickup gate.
	takeAllowed    bool
	takeDisallowed bool
	pickupE        float64
}

func TestArrowStickParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Arrow"),
	}, nil)
	defer targets.cleanup()

	legs := map[string]*arrowLeg{}
	for _, tg := range []struct{ tag, addr string }{
		{"vanilla", targets.vanilla},
		{"mc-go", targets.mcgo},
	} {
		legs[tg.tag] = runArrowLeg(t, tg.addr)
	}
	van, mine := legs["vanilla"], legs["mc-go"]

	// Position lanes compare within a small epsilon — vanilla's AABB
	// ray clip returns margin-adjusted face points (ulp-level), while
	// mc-go lands on the exact face; the semantic position matches.
	eq := func(name string, a, b []float64) {
		t.Helper()
		if len(a) != len(b) {
			t.Fatalf("%s len %v vs %v (%v vs %v)", name, len(a), len(b), a, b)
		}
		for i := range a {
			if a[i] != b[i] {
				t.Fatalf("%s[%d]: %v vs %v (full %v vs %v)", name, i, a[i], b[i], a, b)
			}
		}
	}
	eqEps := func(name string, a, b []float64) {
		t.Helper()
		if len(a) != len(b) {
			t.Fatalf("%s len %v vs %v (%v vs %v)", name, len(a), len(b), a, b)
		}
		for i := range a {
			if math.Abs(a[i]-b[i]) > 1e-6 {
				t.Fatalf("%s[%d]: %v vs %v (full %v vs %v)", name, i, a[i], b[i], a, b)
			}
		}
	}
	eqEps("posA", van.posA, mine.posA)
	eq("motA", van.motA, mine.motA)
	if van.inGroundA != mine.inGroundA {
		t.Fatalf("inGroundA %v vs %v", van.inGroundA, mine.inGroundA)
	}
	if strings.Join(van.stuckA, ",") != strings.Join(mine.stuckA, ",") {
		t.Fatalf("stuckA %v vs %v", van.stuckA, mine.stuckA)
	}
	if van.shakeA != mine.shakeA {
		t.Fatalf("shakeA %v vs %v", van.shakeA, mine.shakeA)
	}
	if van.lifeA != "query" || mine.lifeA != "query" {
		t.Fatalf("life probe status %q vs %q", van.lifeA, mine.lifeA)
	}
	for _, row := range []struct {
		name string
		a, b float64
	}{
		{"pickupA", van.pickupA, mine.pickupA},
		{"damageA", van.damageA, mine.damageA},
		{"critA", van.critA, mine.critA},
		{"pierceA", van.pierceA, mine.pierceA},
		{"inGroundB2", van.inGroundB2, mine.inGroundB2},
		{"pickupE", van.pickupE, mine.pickupE},
	} {
		if row.a != row.b {
			t.Fatalf("%s %v vs %v", row.name, row.a, row.b)
		}
	}
	if strings.Join(van.soundA, ",") != strings.Join(mine.soundA, ",") {
		t.Fatalf("soundA %v vs %v", van.soundA, mine.soundA)
	}
	if strings.Join(van.itemA, ",") != strings.Join(mine.itemA, ",") {
		t.Fatalf("itemA %v vs %v", van.itemA, mine.itemA)
	}
	if van.stickSnd == 0 || mine.stickSnd == 0 {
		t.Fatalf("stick sound count %d vs %d", van.stickSnd, mine.stickSnd)
	}
	// The un-stick lane must move (posB2 differs from posB1) and land
	// identically across legs.
	eqEps("posB2", van.posB2, mine.posB2)
	movedV, movedM := false, false
	for i := range van.posB1 {
		if i < len(van.posB2) && van.posB1[i] != van.posB2[i] {
			movedV = true
		}
		if i < len(mine.posB1) && i < len(mine.posB2) && mine.posB1[i] != mine.posB2[i] {
			movedM = true
		}
	}
	if !movedV || !movedM {
		t.Fatalf("un-stick did not move the arrow: %v→%v / %v→%v", van.posB1, van.posB2, mine.posB1, mine.posB2)
	}
	if van.removedA != mine.removedA || van.removedB != mine.removedB {
		t.Fatalf("despawn removals %v/%v vs %v/%v", van.removedA, van.removedB, mine.removedA, mine.removedB)
	}
	// Water lane: both legs sampled the arrow's Motion/Pos after the
	// same step count — the 0.6-drag decay must match element-
	// for-element within float epsilon.
	eqEps("waterMot", van.waterMot, mine.waterMot)
	eqEps("waterPos", van.waterPos, mine.waterPos)
	if van.takeAllowed != mine.takeAllowed || van.takeDisallowed != mine.takeDisallowed {
		t.Fatalf("pickup gate %v/%v vs %v/%v", van.takeAllowed, van.takeDisallowed, mine.takeAllowed, mine.takeDisallowed)
	}
}

func runArrowLeg(t *testing.T, addr string) *arrowLeg {
	conn, pr, pw := joinPlayBlackbox(t, addr, "Arrow")
	defer conn.Close()
	sink := newChatSinkTP(pr, pw)
	leg := &arrowLeg{}

	sendClientInformationVD(t, pw, 24)
	// z=-4 keeps the player inside the ~16-block range of the wall-
	// stick sound; lane E teleports them back to -20 for the pickup
	// probes.
	sendCommand(t, pw, "tp @s 0 -59 -4 0 0")
	arrowTid, ok := server.EntityTypeID("arrow")
	if !ok {
		t.Fatalf("arrow entity type id unknown")
	}

	// Stage: lane-A wall at z=8 over x[-2,2], lane-B wall at z=8 over
	// x[18,22], a water column for the inertia lane, an air corridor
	// through each, and a stone floor under the player.
	sendCommand(t, pw, "fill -3 -61 3 3 -55 12 minecraft:air")
	sendCommand(t, pw, "fill 17 -61 3 23 -55 12 minecraft:air")
	sendCommand(t, pw, "fill 29 -61 -10 35 -55 0 minecraft:air")
	sendCommand(t, pw, "fill -2 -59 -23 2 -55 -2 minecraft:air")
	sendCommand(t, pw, "fill -2 -60 -23 2 -60 -2 minecraft:stone")
	sendCommand(t, pw, "fill -2 -61 8 2 -55 8 minecraft:stone")
	sendCommand(t, pw, "fill 18 -61 8 22 -55 8 minecraft:stone")
	sendCommand(t, pw, "fill 30 -61 -6 34 -56 -2 minecraft:water")
	sendCommand(t, pw, "kill @e[type=minecraft:arrow]")
	sendCommand(t, pw, "kill @e[type=minecraft:spectral_arrow]")
	sendCommand(t, pw, "kill @e[type=minecraft:trident]")
	// Drain: the fills+kill must land before the summons.
	dataGetLine(t, sink, pw, "@s", "Pos")

	var arrowEids []int32
	takeIds := map[int32]bool{}
	harvest := func() {
		for _, l := range sink.take() {
			pref, rest, ok := strings.Cut(l, ":")
			if !ok {
				continue
			}
			switch pref {
			case "0x1": // add_entity: id + UUID16 + type + pos + rot
				r := protocol.NewPacketReader([]byte(rest))
				id, e1 := r.VarInt()
				_, e2 := r.Read(16)
				typ, e3 := r.VarInt()
				if e1 != nil || e2 != nil || e3 != nil {
					continue
				}
				if typ == arrowTid {
					arrowEids = append(arrowEids, id)
				}
			case "0x65": // set_entity_motion: id + LpVec3 — unused
				// (delta emit cadence is interval-latched; lane D probes
				// NBT instead).
			case "0x4d": // remove_entities: varint count + ids
				r := protocol.NewPacketReader([]byte(rest))
				for {
					id, e1 := r.VarInt()
					if e1 != nil {
						break
					}
					for i, aid := range arrowEids {
						if id == aid {
							if i == 0 {
								leg.removedA = true
							}
							if i == 1 {
								leg.removedB = true
							}
						}
					}
				}
			case "0x75": // sound: wire id = registry id + 1
				r := protocol.NewPacketReader([]byte(rest))
				sid, e1 := r.VarInt()
				if e1 != nil {
					continue
				}
				if sid == 86 { // entity.arrow.hit
					leg.stickSnd++
				}
			case "0x7C": // take_item_entity: collected + collector + count
				r := protocol.NewPacketReader([]byte(rest))
				id, e1 := r.VarInt()
				if e1 != nil {
					continue
				}
				takeIds[id] = true
			}
		}
	}

	// Lanes A + B: arrows fly +z into the walls.
	sendCommand(t, pw, "summon minecraft:arrow 0 -59 4 {Motion:[0.0d,0.0d,0.6d]}")
	sendCommand(t, pw, "summon minecraft:arrow 20 -59 4 {Motion:[0.0d,0.0d,0.6d]}")
	time.Sleep(1500 * time.Millisecond)
	harvest()

	// Lane-A NBT probes — the AbstractArrow save tail.
	selA := "@e[type=minecraft:arrow,x=-2,y=-62,z=6,dx=4,dy=5,dz=4,limit=1]"
	leg.posA = dataGetFloats(t, sink, pw, selA, "Pos")
	leg.motA = dataGetFloats(t, sink, pw, selA, "Motion")
	_, v := dataGetSel(t, sink, pw, selA, "inGround")
	leg.inGroundA = v
	leg.stuckA = dataGetTokens(t, sink, pw, selA, "inBlockState")
	_, leg.shakeA = dataGetSel(t, sink, pw, selA, "shake")
	leg.lifeA, _ = dataGetSel(t, sink, pw, selA, "life")
	_, leg.pickupA = dataGetSel(t, sink, pw, selA, "pickup")
	_, leg.damageA = dataGetSel(t, sink, pw, selA, "damage")
	_, leg.critA = dataGetSel(t, sink, pw, selA, "crit")
	_, leg.pierceA = dataGetSel(t, sink, pw, selA, "PierceLevel")
	leg.soundA = dataGetTokens(t, sink, pw, selA, "SoundEvent")
	leg.itemA = dataGetTokens(t, sink, pw, selA, "item")

	// Lane-B probe before the wall break.
	selB := "@e[type=minecraft:arrow,x=17,y=-62,z=6,dx=6,dy=5,dz=4,limit=1]"
	leg.posB1 = dataGetFloats(t, sink, pw, selB, "Pos")

	// Break lane B's wall — lastState mismatch un-sticks the arrow,
	// it falls, and re-sticks on whatever it lands on.
	sendCommand(t, pw, "fill 18 -61 8 22 -55 8 minecraft:air")
	time.Sleep(1200 * time.Millisecond)
	leg.posB2 = dataGetFloats(t, sink, pw, selB, "Pos")
	_, leg.inGroundB2 = dataGetSel(t, sink, pw, selB, "inGround")

	// Despawn: sprint past life=1200.
	sendCommand(t, pw, "tick sprint 1210")
	sendCommand(t, pw, "say ZEND")
	time.Sleep(4 * time.Second)
	harvest()

	// Lane D — water inertia: a large water box so the arrow rides
	// deep-interior source cells (zero getFlow) — boundary cells push
	// a current, a separate parity gap. Freeze + step a fixed tick
	// count so the decay is deterministic on both legs.
	sendCommand(t, pw, "fill 27 -61 -11 39 -55 3 minecraft:air")
	sendCommand(t, pw, "fill 28 -61 -10 38 -55 2 minecraft:water")
	sendCommand(t, pw, "tick freeze")
	sendCommand(t, pw, "summon minecraft:arrow 32 -59 -8 {Motion:[0.0d,0.0d,0.5d]}")
	sendCommand(t, pw, "tick step 8")
	// /tick step is not atomic: the queued steps run over real time,
	// so an immediate /data get races into the middle of the step
	// count. The world stays frozen afterward, so waiting out the
	// steps gives a deterministic state to probe.
	time.Sleep(1200 * time.Millisecond)
	dataGetLine(t, sink, pw, "@s", "Pos") // drain step echo
	selD := "@e[type=minecraft:arrow,x=29,y=-62,z=-10,dx=6,dy=6,dz=12,limit=1]"
	leg.waterMot = dataGetFloats(t, sink, pw, selD, "Motion")
	leg.waterPos = dataGetFloats(t, sink, pw, selD, "Pos")
	sendCommand(t, pw, "tick unfreeze")
	harvest()

	// Lane E — pickup gate: a stuck ALLOWED arrow at the player's
	// feet, then a DISALLOWED one.
	sendCommand(t, pw, "tp @s 0 -59 -20 0 0")
	sendCommand(t, pw, "summon minecraft:arrow 0 -59 -20 {inGround:1b,inBlockState:{Name:\"minecraft:stone\"},pickup:1b}")
	time.Sleep(1600 * time.Millisecond)
	harvest()
	selE := "@e[type=minecraft:arrow,x=-2,y=-62,z=-23,dx=4,dy=5,dz=6,limit=1]"
	if len(arrowEids) > 3 && takeIds[arrowEids[3]] {
		leg.takeAllowed = true
	}
	sendCommand(t, pw, "summon minecraft:arrow 0 -59 -20 {inGround:1b,inBlockState:{Name:\"minecraft:stone\"},pickup:0b}")
	time.Sleep(1600 * time.Millisecond)
	harvest()
	_, leg.pickupE = dataGetSel(t, sink, pw, selE, "pickup")
	if len(arrowEids) > 4 && takeIds[arrowEids[4]] {
		leg.takeDisallowed = true
	}
	return leg
}
