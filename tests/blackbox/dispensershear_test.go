package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
)

// TestDispenserShearParity pins ShearsDispenseItemBehavior: a
// powered dispenser shears a Shearable in its mouth cell — the emit
// is the entity-anchored 0x74 shear sound with SoundSource.BLOCKS
// (src=4), the loot drops at the mob, and the shears wear.
func TestDispenserShearParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("DP"),
	}, nil)
	defer targets.cleanup()

	// snd74 scans 0x74 sound_entity lines for (snd,src,eid).
	snd74 := func(lines []string, wantSnd, wantSrc, wantEid int32) bool {
		for _, l := range lines {
			i := strings.IndexByte(l, ':')
			if i < 0 || l[:i] != "0x74" {
				continue
			}
			r := protocol.NewPacketReader([]byte(l[i+1:]))
			snd, e1 := r.VarInt()
			src, e2 := r.VarInt()
			eid, e3 := r.VarInt()
			if e1 != nil || e2 != nil || e3 != nil {
				continue
			}
			if snd-1 == wantSnd && src == wantSrc && eid == wantEid {
				return true
			}
		}
		return false
	}

	run := func(addr string) map[string]string {
		c, pr, pw := joinPlayBlackbox(t, addr, "DP")
		defer c.Close()
		sink := newChatSinkTP(pr, pw)
		sendClientInformationVD(t, pw, 2)
		out := map[string]string{}
		lines := []string{}
		take := func() []string { l := sink.take(); lines = append(lines, l...); return l }

		drain := func() {
			end := time.Now().Add(90 * time.Second)
			for time.Now().Before(end) {
				sendCommand(t, pw, "say ZZDRAIN")
				for _, l := range take() {
					if strings.Contains(l, "ZZDRAIN") {
						return
					}
				}
				time.Sleep(500 * time.Millisecond)
			}
		}
		waitFor := func(sel, mk string) string {
			end := time.Now().Add(30 * time.Second)
			for time.Now().Before(end) {
				sendCommand(t, pw, fmt.Sprintf("execute if %s run say ZZ%s", sel, mk))
				deadline := time.Now().Add(700 * time.Millisecond)
				for time.Now().Before(deadline) {
					for _, l := range take() {
						if strings.Contains(l, "ZZ"+mk) {
							return "hit"
						}
					}
					time.Sleep(30 * time.Millisecond)
				}
			}
			return "miss"
		}
		spawnNear := func(typeID int32, x, z float64) int32 {
			end := time.Now().Add(25 * time.Second)
			for {
				if eid := aniSpawnID(take(), typeID, x, z); eid >= 0 {
					return eid
				}
				if time.Now().After(end) {
					t.Logf("spawnNear: no add_entity type %d near (%.0f,%.0f)", typeID, x, z)
					return -1
				}
				time.Sleep(50 * time.Millisecond)
			}
		}

		sendCommand(t, pw, "gamerule doMobSpawning false")
		sendCommand(t, pw, "time set 18000")
		sendCommand(t, pw, "gamemode survival DP")
		sendCommand(t, pw, "tp DP 0 -60 20")
		drain()

		// Leg A: dispenser facing north, shears inside, sheep at the
		// mouth cell — power via redstone_block neighbor.
		sendCommand(t, pw, "setblock 8 -60 8 minecraft:dispenser[facing=north]")
		sendCommand(t, pw, "item replace block 8 -60 8 container.0 with minecraft:shears")
		sendCommand(t, pw, `summon minecraft:sheep 8.5 -60 7.5 {NoAI:1b}`)
		sheepID := spawnNear(111, 8.5, 7.5)
		lines = lines[:0]
		take()
		sendCommand(t, pw, "setblock 9 -60 8 minecraft:redstone_block")
		// dispense tick is +4; give it a window to fire
		time.Sleep(1500 * time.Millisecond)
		take()
		out["sheepSnd"] = "miss"
		if snd74(lines, 1441, 4, sheepID) {
			out["sheepSnd"] = "hit"
		}
		out["sheepSheared"] = waitFor(`entity @e[type=minecraft:sheep,x=8.5,y=-60,z=7.5,distance=..6,limit=1,sort=nearest,nbt={Sheared:true}]`, "SS")
		out["woolDrop"] = waitFor(`entity @e[type=minecraft:item,sort=nearest,limit=1,nbt={Item:{id:"minecraft:white_wool"}}]`, "WD")
		out["woolInv"] = waitFor(`entity @s[nbt={Inventory:[{id:"minecraft:white_wool"}]}]`, "WI")
		if out["woolDrop"] == "hit" || out["woolInv"] == "hit" {
			out["wool"] = "hit"
		} else {
			out["wool"] = "miss"
		}
		sendCommand(t, pw, "kill @e[type=minecraft:sheep]")
		sendCommand(t, pw, "kill @e[type=minecraft:item]")
		sendCommand(t, pw, "item replace entity DP weapon.mainhand with minecraft:air")
		sendCommand(t, pw, "clear DP")

		// Leg B: brown mooshroom → cow + brown mushrooms + shear snd.
		sendCommand(t, pw, "setblock 24 -60 8 minecraft:dispenser[facing=north]")
		sendCommand(t, pw, "item replace block 24 -60 8 container.0 with minecraft:shears")
		sendCommand(t, pw, `summon minecraft:mooshroom 24.5 -60 7.5 {Type:"brown",NoAI:1b}`)
		mooID := spawnNear(86, 24.5, 7.5)
		// Sound range is 16*max(1,vol) — DP must stand within range
		// of the mouth before the dispenser fires.
		sendCommand(t, pw, "tp DP 22 -60 10")
		lines = lines[:0]
		take()
		sendCommand(t, pw, "setblock 25 -60 8 minecraft:redstone_block")
		time.Sleep(1500 * time.Millisecond)
		take()
		out["mooSnd"] = "miss"
		if snd74(lines, 989, 4, mooID) {
			out["mooSnd"] = "hit"
		}
		out["cowSpawn"] = waitFor(`entity @e[type=minecraft:cow,x=24.5,y=-60,z=7.5,distance=..4,limit=1,sort=nearest]`, "CS")
		out["brnInv"] = waitFor(`entity @s[nbt={Inventory:[{id:"minecraft:brown_mushroom"}]}]`, "BI")
		out["brnMush"] = waitFor(`entity @e[type=minecraft:item,sort=nearest,limit=1,nbt={Item:{id:"minecraft:brown_mushroom"}}]`, "BM")
		if out["brnMush"] == "hit" || out["brnInv"] == "hit" {
			out["brown"] = "hit"
		} else {
			out["brown"] = "miss"
		}
		out["redAbsent"] = "hit"
		if waitFor(`entity @e[type=minecraft:item,sort=nearest,limit=1,nbt={Item:{id:"minecraft:red_mushroom"}}]`, "RM") == "hit" {
			out["redAbsent"] = "miss"
		}
		return out
	}

	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	t.Logf("vanilla %v", van)
	t.Logf("mc-go   %v", mc)
	want := map[string]string{
		"sheepSnd": "hit", "sheepSheared": "hit", "wool": "hit",
		"mooSnd": "hit", "cowSpawn": "hit", "brown": "hit",
		"redAbsent": "hit",
	}
	for k, w := range want {
		if van[k] != w {
			t.Errorf("leg %s vanilla=%s want %s", k, van[k], w)
		}
		if mc[k] != w {
			t.Errorf("leg %s mc-go=%s want %s", k, mc[k], w)
		}
	}
}
