package blackbox

// TestAnimalInteractParity pins the animal item interacts measured on
// the 26.2 jar:
//
//   - AbstractCow/Goat mobInteract BUCKET arm: player.playSound is a
//     POSITIONAL (0x75) packet at the player, PLAYERS source,
//     self-excluded — the milker hears nothing, a bystander does.
//     ItemUtils.createFilledResult swaps bucket -> milk_bucket.
//   - MushroomCow BOWL arm: createFilledResult(bowl -> mushroom_stew);
//     its playSound is self-excluded like the milk arm's — the actor
//     hears nothing, verified live on the jar.
//   - DyeItem.interactLivingEntity on a sheep: level.playSound(player,
//     sheep, DYE_USE, PLAYERS,1,1) — entity-anchored 0x74 on the sheep,
//     excluded for the dyer — then setColor + shrink(1). The sheared
//     wool then lands as <color>_wool (shearing/sheep/<color>).
//
// Each leg runs identically against vanilla and mc-go; the map of
// hit/miss results must match.
//
// Timing: a second player's join streams ~300 view-distance-8 chunks
// through the vanilla main thread — commands and interact packets
// queue behind that burst for several seconds. The test therefore
// waits for the command queue to drain (a `say` echo) before legs
// and uses generous per-probe windows rather than fixed delays.

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// aniSnd is one parsed clientbound sound packet — 0x75 positional or
// 0x74 entity-anchored (wire sound id = registry id + 1).
type aniSnd struct {
	ent        bool
	snd, src   int32
	eid        int32
	x, y, z    float64
	vol, pitch float32
}

func aniParseSounds(lines []string) []aniSnd {
	var out []aniSnd
	for _, l := range lines {
		i := strings.IndexByte(l, ':')
		if i < 0 || (l[:i] != "0x74" && l[:i] != "0x75") {
			continue
		}
		r := protocol.NewPacketReader([]byte(l[i+1:]))
		snd, e1 := r.VarInt()
		src, e2 := r.VarInt()
		if e1 != nil || e2 != nil {
			continue
		}
		s := aniSnd{snd: snd - 1, src: src}
		if l[:i] == "0x74" {
			s.ent = true
			if eid, err := r.VarInt(); err == nil {
				s.eid = eid
			}
			if v, err := r.Float(); err == nil {
				s.vol = v
			}
			if v, err := r.Float(); err == nil {
				s.pitch = v
			}
		} else {
			if v, err := r.Int(); err == nil {
				s.x = float64(v) / 8
			}
			if v, err := r.Int(); err == nil {
				s.y = float64(v) / 8
			}
			if v, err := r.Int(); err == nil {
				s.z = float64(v) / 8
			}
			if v, err := r.Float(); err == nil {
				s.vol = v
			}
			if v, err := r.Float(); err == nil {
				s.pitch = v
			}
		}
		out = append(out, s)
	}
	return out
}

// aniSpawnID finds the entity id a recent add_entity (0x01) packet
// spawned nearest to x,z — the wire that summon's own bundle carried.
func aniSpawnID(lines []string, wantType int32, x, z float64) int32 {
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
		if typ != wantType {
			continue
		}
		d := math.Abs(ex-x) + math.Abs(ez-z)
		if d < bestD {
			best, bestD = eid, d
		}
	}
	return best
}

func TestAnimalInteractParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("AP", "AQ"),
	}, nil)
	defer targets.cleanup()

	run := func(addr string) map[string]string {
		c, pr, pw := joinPlayBlackbox(t, addr, "AP")
		defer c.Close()
		c2, pr2, pw2 := joinPlayBlackbox(t, addr, "AQ")
		defer c2.Close()
		sinkP := newChatSinkTP(pr, pw)
		sinkQ := newChatSinkTP(pr2, pw2)
		// VD 2 (not 8): both join-time chunk storms shrink from ~289 to
		// ~25 chunks each, so the serverbound command queue drains in
		// seconds rather than minutes; every mob is adjacent to the
		// actor, so tracking range is unaffected.
		sendClientInformationVD(t, pw, 2)
		sendClientInformationVD(t, pw2, 2)
		out := map[string]string{}

		// waitProx waits until a `name` mob is within dist of AP —
		// after the selector-tp (command FIFO), AP lands on the mob;
		// the probe proves the click's range gate will pass.
		waitProx := func(name string) {
			sendCommand(t, pw, fmt.Sprintf(`execute if entity @e[type=minecraft:%s,distance=..3] run say ZZPRX`, name))
			end := time.Now().Add(25 * time.Second)
			for time.Now().Before(end) {
				for _, l := range sinkP.take() {
					if strings.Contains(l, "ZZPRX") {
						return
					}
				}
				time.Sleep(30 * time.Millisecond)
			}
		}
		// waitHeld sends one execute-if probe for the held item and
		// waits for its marker: `item replace` and this execute are
		// both commands (strict FIFO), so the marker proves the item
		// is in hand before a click packet — whose ordering against
		// commands is not guaranteed on vanilla — may fire.
		waitHeld := func(item string) {
			sendCommand(t, pw, fmt.Sprintf(`execute if entity @s[nbt={SelectedItem:{id:"minecraft:%s"}}] run say ZZHLD`, item))
			end := time.Now().Add(25 * time.Second)
			for time.Now().Before(end) {
				for _, l := range sinkP.take() {
					if strings.Contains(l, "ZZHLD") {
						return
					}
				}
				time.Sleep(30 * time.Millisecond)
			}
		}
		// drain waits until a `say` echo reaches AP — proof the
		// serverbound command queue is progressing again after the
		// join-time chunk bursts.
		drain := func() {
			end := time.Now().Add(90 * time.Second)
			for time.Now().Before(end) {
				sendCommand(t, pw, "say ZZDRAIN")
				for _, l := range sinkP.take() {
					if strings.Contains(l, "ZZDRAIN") {
						return
					}
				}
				time.Sleep(500 * time.Millisecond)
			}
		}

		sendCommand(t, pw, "gamerule doMobSpawning false")
		sendCommand(t, pw, "time set 18000")
		sendCommand(t, pw, "gamemode survival AP")
		drain()

		// waitFor re-issues an execute-if say until the marker echoes —
		// a self-synchronizing barrier for server-side state.
		waitFor := func(sel, mk string) string {
			end := time.Now().Add(30 * time.Second)
			for time.Now().Before(end) {
				sendCommand(t, pw, fmt.Sprintf("execute if %s run say ZZ%s", sel, mk))
				deadline := time.Now().Add(700 * time.Millisecond)
				for time.Now().Before(deadline) {
					for _, l := range sinkP.take() {
						if strings.Contains(l, "ZZ"+mk) {
							return "hit"
						}
					}
					time.Sleep(30 * time.Millisecond)
				}
			}
			return "miss"
		}
		// snd checks sink lines for a packet of the given kind/sound/src
		// (wire id is registry+1 — the table ids are registry ids).
		hasSnd := func(lines []string, wantEnt bool, wantSnd, wantSrc int32, wantEid int32) bool {
			for _, sp := range aniParseSounds(lines) {
				if sp.ent == wantEnt && sp.snd == wantSnd && sp.src == wantSrc &&
					(!wantEnt || sp.eid == wantEid) {
					return true
				}
			}
			return false
		}
		click := func(eid int32) {
			var ib protocol.Buffer
			ib.VarInt(eid)
			ib.VarInt(0) // main hand
			ib.Raw(lpVec3(0.5, 0.9, 0.5))
			ib.Bool(false)
			if err := pw.WritePacket(server.IDPlayInteractSB, ib.Bytes()); err != nil {
				t.Logf("interact write: %v", err)
			}
		}
		// leg waits for the held item to land, summons a mob and moves
		// actor/bystander beside it, then polls for the typed
		// add_entity (player adds can re-track near the mob).
		leg := func(slotItem, name string, typeID int32, x, z float64, nbt string) int32 {
			waitHeld(slotItem)
			sinkP.take()
			// No trailing space: brigadier parses `"<cmd> "` as an
			// incomplete extra argument -> command.unknown.argument and
			// the summon silently fails on vanilla (mc-go trims it).
			summon := fmt.Sprintf("summon minecraft:%s %.1f -60 %.1f", name, x, z)
			if nbt != "" {
				summon += " " + nbt
			}
			sendCommand(t, pw, summon)
			sendCommand(t, pw, fmt.Sprintf("tp @s %.1f -60 %.1f", x-1.2, z))
			sendCommand(t, pw, fmt.Sprintf("tp AQ %.1f -60 %.1f", x+2, z))
			end := time.Now().Add(25 * time.Second)
			for {
				if eid := aniSpawnID(sinkP.take(), typeID, x, z); eid >= 0 {
					// Mobs wander between spawn and click; park the
					// actor ON the mob so the interact range check
					// holds whenever the packet is processed.
					sendCommand(t, pw, fmt.Sprintf("tp @s @e[type=minecraft:%s,sort=nearest,limit=1]", name))
					waitProx(name)
					return eid
				}
				if time.Now().After(end) {
					t.Logf("leg %s: no add_entity near (%.0f,-60,%.0f)", name, x, z)
					return -1
				}
				time.Sleep(50 * time.Millisecond)
			}
		}

		// — leg A: bucket on a cow —
		sendCommand(t, pw, "item replace entity AP weapon.mainhand with minecraft:bucket")
		cowID := leg("bucket", "cow", 30, 8, 8, "")
		click(cowID)
		out["milkItem"] = waitFor(`entity @s[nbt={SelectedItem:{id:"minecraft:milk_bucket"}}]`, "AM")
		out["milkQ"] = "miss"
		if hasSnd(sinkQ.take(), false, 449, 7, 0) { // entity.cow.milk
			out["milkQ"] = "hit"
		}
		out["milkP"] = "miss"
		if hasSnd(sinkP.take(), false, 449, 7, 0) {
			out["milkP"] = "hit" // the milker must NOT hear it
		}

		// — leg B: bowl on a mooshroom —
		sendCommand(t, pw, "item replace entity AP weapon.mainhand with minecraft:bowl")
		mooID := leg("bowl", "mooshroom", 86, 24, 8, "")
		sinkP.take()
		click(mooID)
		out["stewItem"] = waitFor(`entity @s[nbt={SelectedItem:{id:"minecraft:mushroom_stew"}}]`, "BM")
		out["stewP"] = "miss"
		if hasSnd(sinkP.take(), false, 987, 6, 0) { // entity.mooshroom.milk
			out["stewP"] = "hit"
		}

		// — leg C: bucket on a goat —
		sendCommand(t, pw, "item replace entity AP weapon.mainhand with minecraft:bucket")
		goatID := leg("bucket", "goat", 62, 40, 8, "")
		sinkP.take()
		sinkQ.take()
		click(goatID)
		out["goatItem"] = waitFor(`entity @s[nbt={SelectedItem:{id:"minecraft:milk_bucket"}}]`, "CM")
		out["goatQ"] = "miss"
		if hasSnd(sinkQ.take(), false, 740, 7, 0) { // entity.goat.milk
			out["goatQ"] = "hit"
		}
		out["goatP"] = "miss"
		if hasSnd(sinkP.take(), false, 740, 7, 0) {
			out["goatP"] = "hit"
		}

		// — leg D: red dye on a sheep, then shear —
		sendCommand(t, pw, "item replace entity AP weapon.mainhand with minecraft:red_dye 2")
		sheepID := leg("red_dye", "sheep", 111, 56, 8, "")
		sinkP.take()
		sinkQ.take()
		click(sheepID)
		out["dyeColor"] = waitFor(`entity @e[type=minecraft:sheep,x=56,y=-60,z=8,distance=..8,nbt={Color:14b}]`, "DC")
		out["dyeItem"] = waitFor(`entity @s[nbt={SelectedItem:{id:"minecraft:red_dye",count:1}}]`, "DI")
		out["dyeQ"] = "miss"
		if hasSnd(sinkQ.take(), true, 571, 7, sheepID) { // item.dye.use anchored on the sheep
			out["dyeQ"] = "hit"
		}
		out["dyeP"] = "miss"
		if hasSnd(sinkP.take(), true, 571, 7, sheepID) {
			out["dyeP"] = "hit"
		}
		// Shear the dyed sheep — the wool drop must carry the color.
		sendCommand(t, pw, "item replace entity AP weapon.mainhand with minecraft:shears")
		sendCommand(t, pw, "tp @s @e[type=minecraft:sheep,sort=nearest,limit=1]")
		waitHeld("shears")
		waitProx("sheep")
		click(sheepID)
		// Sheep.shear leads with playSound(null, sheep, SHEEP_SHEAR,
		// PLAYERS, 1, 1) — entity-anchored 0x74, heard by BOTH conns
		// (null except-player, unlike milk/dye's self-exclusion). The
		// actor's sink must be polled BEFORE the wool waitFor — that
		// helper drains sinkP itself.
		out["shearSoundP"] = "miss"
		sndEnd := time.Now().Add(2 * time.Second)
		for time.Now().Before(sndEnd) {
			if hasSnd(sinkP.take(), true, 1441, 7, sheepID) {
				out["shearSoundP"] = "hit"
				break
			}
			time.Sleep(30 * time.Millisecond)
		}
		// The actor stands on the sheep: vanilla vacuums the drop into
		// inventory before the entity probe can see it; either shape
		// proves the color carried.
		out["wool"] = waitFor(`entity @s[nbt={Inventory:[{id:"minecraft:red_wool"}]}]`, "WI")
		if out["wool"] != "hit" {
			out["wool"] = waitFor(`entity @e[type=minecraft:item,sort=nearest,limit=1,nbt={Item:{id:"minecraft:red_wool"}}]`, "DW")
		}
		out["shearSoundQ"] = "miss"
		if hasSnd(sinkQ.take(), true, 1441, 7, sheepID) {
			out["shearSoundQ"] = "hit"
		}
		return out
	}

	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	t.Logf("vanilla %v", van)
	t.Logf("mc-go   %v", mc)
	want := map[string]string{
		"milkItem": "hit", "milkQ": "hit", "milkP": "miss",
		"stewItem": "hit", "stewP": "miss",
		"goatItem": "hit", "goatQ": "hit", "goatP": "miss",
		"dyeColor": "hit", "dyeItem": "hit",
		"dyeQ": "hit", "dyeP": "miss", "wool": "hit",
		"shearSoundQ": "hit", "shearSoundP": "hit",
	}
	for k, w := range want {
		if van[k] != w {
			t.Errorf("leg %s vanilla=%s want %s", k, van[k], w)
		}
		if mc[k] != w {
			t.Errorf("leg %s mcgo=%s want %s", k, mc[k], w)
		}
	}
}
