package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestMooshroomVariantParity pins MushroomCow's variant surface:
// Type NBT + DATA_TYPE metadata (index 18, int), the brown variant's
// flower-eat -> stew_effects -> suspicious-stew swap, thunder
// MOOSHROOM_CONVERT, and variant-aware shear drops.
func TestMooshroomVariantParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("MP", "MQ"),
	}, nil)
	defer targets.cleanup()

	// mooVar scans 0x63 set_entity_data lines for index 18 (INT) — the
	// DATA_TYPE entry — returning its value or -1.
	mooVar := func(lines []string) int32 {
		for _, l := range lines {
			i := strings.IndexByte(l, ':')
			if i < 0 || l[:i] != "0x63" {
				continue
			}
			r := protocol.NewPacketReader([]byte(l[i+1:]))
			if _, e := r.VarInt(); e != nil {
				continue
			}
		SCAN:
			for {
				idx, err := r.Byte()
				if err != nil || idx == 0xFF {
					break
				}
				ser, err2 := r.VarInt()
				if err2 != nil {
					break
				}
				if idx == 18 && ser == 1 {
					v, _ := r.VarInt()
					return v
				}
				switch ser {
				case 0:
					r.Byte()
				case 1:
					r.VarInt()
				case 3:
					r.Float()
				case 8:
					r.Byte()
				default:
					break SCAN
				}
			}
		}
		return -1
	}

	run := func(addr string) map[string]string {
		c, pr, pw := joinPlayBlackbox(t, addr, "MP")
		defer c.Close()
		c2, pr2, pw2 := joinPlayBlackbox(t, addr, "MQ")
		defer c2.Close()
		sinkP := newChatSinkTP(pr, pw)
		sinkQ := newChatSinkTP(pr2, pw2)
		sendClientInformationVD(t, pw, 2)
		sendClientInformationVD(t, pw2, 2)
		out := map[string]string{}

		// pLines/qLines accumulate every line the probes drain — waitFor
		// loops otherwise discard sound/metadata packets they read.
		pLines, qLines := []string{}, []string{}
		takeQ := func() { qLines = append(qLines, sinkQ.take()...) }
		takeP := func() []string {
			l := sinkP.take()
			pLines = append(pLines, l...)
			takeQ()
			return l
		}

		waitProx := func(name string) {
			sendCommand(t, pw, fmt.Sprintf(`execute if entity @e[type=minecraft:%s,distance=..3] run say ZZPRX`, name))
			end := time.Now().Add(25 * time.Second)
			for time.Now().Before(end) {
				for _, l := range takeP() {
					if strings.Contains(l, "ZZPRX") {
						return
					}
				}
				time.Sleep(30 * time.Millisecond)
			}
		}
		waitHeld := func(item string) {
			sendCommand(t, pw, fmt.Sprintf(`execute if entity @s[nbt={SelectedItem:{id:"minecraft:%s"}}] run say ZZHLD`, item))
			end := time.Now().Add(25 * time.Second)
			for time.Now().Before(end) {
				for _, l := range takeP() {
					if strings.Contains(l, "ZZHLD") {
						return
					}
				}
				time.Sleep(30 * time.Millisecond)
			}
		}
		drain := func() {
			end := time.Now().Add(90 * time.Second)
			for time.Now().Before(end) {
				sendCommand(t, pw, "say ZZDRAIN")
				for _, l := range takeP() {
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
					for _, l := range takeP() {
						if strings.Contains(l, "ZZ"+mk) {
							return "hit"
						}
					}
					time.Sleep(30 * time.Millisecond)
				}
			}
			return "miss"
		}
		click := func(eid int32) {
			var ib protocol.Buffer
			ib.VarInt(eid)
			ib.VarInt(0)
			ib.Raw(lpVec3(0.5, 0.9, 0.5))
			ib.Bool(false)
			if err := pw.WritePacket(server.IDPlayInteractSB, ib.Bytes()); err != nil {
				t.Logf("interact write: %v", err)
			}
		}
		// park re-tps AP onto the mob (it wanders between clicks) and
		// proves interact range before the packet goes out.
		park := func(name string) {
			sendCommand(t, pw, fmt.Sprintf("tp @s @e[type=minecraft:%s,sort=nearest,limit=1]", name))
			waitProx(name)
		}
		leg := func(slotItem, name string, typeID int32, x, z float64, nbt string) int32 {
			if slotItem != "" {
				waitHeld(slotItem)
			}
			takeP()
			summon := fmt.Sprintf("summon minecraft:%s %.1f -60 %.1f", name, x, z)
			if nbt != "" {
				summon += " " + nbt
			}
			sendCommand(t, pw, summon)
			sendCommand(t, pw, fmt.Sprintf("tp @s %.1f -60 %.1f", x-1.2, z))
			sendCommand(t, pw, fmt.Sprintf("tp MQ %.1f -60 %.1f", x+2, z))
			end := time.Now().Add(25 * time.Second)
			for {
				if eid := aniSpawnID(takeP(), typeID, x, z); eid >= 0 {
					park(name)
					return eid
				}
				if time.Now().After(end) {
					t.Logf("leg %s: no add_entity near (%.0f,-60,%.0f)", name, x, z)
					return -1
				}
				time.Sleep(50 * time.Millisecond)
			}
		}

		sendCommand(t, pw, "gamerule doMobSpawning false")
		sendCommand(t, pw, "gamerule doFireTick false")
		sendCommand(t, pw, "time set 18000")
		sendCommand(t, pw, "gamemode survival MP")
		sendCommand(t, pw, "gamemode survival MQ")
		drain()

		// — leg A: red mooshroom — Type:"red"; a flower feed falls
		// through to super (item stays, no stew_effects).
		sendCommand(t, pw, "item replace entity MP weapon.mainhand with minecraft:poppy")
		red := leg("poppy", "mooshroom", 86, 8, 8, "")
		out["redType"] = waitFor(`entity @e[type=minecraft:mooshroom,x=8,y=-60,z=8,distance=..8,limit=1,sort=nearest,nbt={Type:"red"}]`, "RT")
		park("mooshroom")
		click(red)
		out["redFeedItem"] = waitFor(`entity @s[nbt={SelectedItem:{id:"minecraft:poppy"}}]`, "RF")
		out["redFeedEff"] = waitFor(`entity @e[type=minecraft:mooshroom,x=8,y=-60,z=8,distance=..8,limit=1,sort=nearest,nbt={stew_effects:[{id:"minecraft:night_vision"}]}]`, "RE")

		// — leg B: brown via summon NBT — Type + idx18 int=1; feed a
		// poppy (give 2 so the second-feed arm still has one), then the
		// already-fed click keeps the item.
		sendCommand(t, pw, "item replace entity MP weapon.mainhand with minecraft:poppy 2")
		brown := leg("poppy", "mooshroom", 86, 24, 8, `{Type:"brown"}`)
		out["brnType"] = waitFor(`entity @e[type=minecraft:mooshroom,x=24,y=-60,z=8,distance=..8,limit=1,sort=nearest,nbt={Type:"brown"}]`, "BT")
		out["brnMeta"] = "miss"
		if v := mooVar(pLines); v == 1 {
			out["brnMeta"] = "hit"
		}
		park("mooshroom")
		click(brown)
		out["fedEff"] = waitFor(`entity @e[type=minecraft:mooshroom,x=24,y=-60,z=8,distance=..8,limit=1,sort=nearest,nbt={stew_effects:[{id:"minecraft:night_vision"}]}]`, "FE")
		out["eatP"] = "miss"
		out["eatQ"] = "miss"
		for _, sp := range aniParseSounds(pLines) {
			if !sp.ent && sp.snd == 986 && sp.src == 6 {
				out["eatP"] = "hit"
			}
		}
		for _, sp := range aniParseSounds(qLines) {
			if !sp.ent && sp.snd == 986 && sp.src == 6 {
				out["eatQ"] = "hit"
			}
		}
		// second feed — already-fed arm: smoke, item stays
		park("mooshroom")
		click(brown)
		out["fed2Item"] = waitFor(`entity @s[nbt={SelectedItem:{id:"minecraft:poppy"}}]`, "F2")

		// — leg C: bowl → suspicious stew with the effects component —
		sendCommand(t, pw, "item replace entity MP weapon.mainhand with minecraft:bowl")
		waitHeld("bowl")
		park("mooshroom")
		click(brown)
		out["stewItem"] = waitFor(`entity @s[nbt={SelectedItem:{id:"minecraft:suspicious_stew"}}]`, "SI")
		out["susP"] = "miss"
		out["susQ"] = "miss"
		for _, sp := range aniParseSounds(pLines) {
			if !sp.ent && sp.snd == 988 && sp.src == 6 {
				out["susP"] = "hit"
			}
		}
		for _, sp := range aniParseSounds(qLines) {
			if !sp.ent && sp.snd == 988 && sp.src == 6 {
				out["susQ"] = "hit"
			}
		}

		// — leg D: thunder converts red→brown (idx18 int=1 + convert
		// sound); read the packets, the mob may later burn out.
		leg("", "mooshroom", 86, 40, 8, "")
		sendCommand(t, pw, "summon minecraft:lightning_bolt 40 -60 8")
		end := time.Now().Add(15 * time.Second)
		for time.Now().Before(end) && mooVar(pLines) != 1 {
			takeP()
			time.Sleep(50 * time.Millisecond)
		}
		// the convert sound rides after the idx18 packet — drain once more
		time.Sleep(400 * time.Millisecond)
		takeP()
		out["convMeta"] = "miss"
		if v := mooVar(pLines); v == 1 {
			out["convMeta"] = "hit"
		}
		out["convP"] = "miss"
		out["convQ"] = "miss"
		for _, sp := range aniParseSounds(pLines) {
			if !sp.ent && sp.snd == 985 && sp.src == 6 {
				out["convP"] = "hit"
			}
		}
		for _, sp := range aniParseSounds(qLines) {
			if !sp.ent && sp.snd == 985 && sp.src == 6 {
				out["convQ"] = "hit"
			}
		}

		// — leg E: shear brown → 5 brown_mushroom —
		bsh := leg("", "mooshroom", 86, 56, 8, `{Type:"brown"}`)
		sendCommand(t, pw, "item replace entity MP weapon.mainhand with minecraft:shears")
		waitHeld("shears")
		park("mooshroom")
		click(bsh)
		// AP is parked on the mob — the 5 drops may be vacuumed into
		// Inventory before the item-entity probe runs.
		out["brownMush"] = waitFor(`entity @s[nbt={Inventory:[{id:"minecraft:brown_mushroom"}]}]`, "BM")
		if out["brownMush"] != "hit" {
			out["brownMush"] = waitFor(`entity @e[type=minecraft:item,sort=nearest,limit=1,nbt={Item:{id:"minecraft:brown_mushroom"}}]`, "BM2")
		}
		return out
	}

	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	t.Logf("vanilla %v", van)
	t.Logf("mc-go   %v", mc)
	want := map[string]string{
		"redType": "hit", "redFeedItem": "hit", "redFeedEff": "miss",
		"brnType": "hit", "brnMeta": "hit",
		"fedEff": "hit", "eatP": "hit", "eatQ": "hit", "fed2Item": "hit",
		"stewItem": "hit", "susP": "hit", "susQ": "hit",
		"convMeta": "hit", "convP": "hit", "convQ": "hit",
		"brownMush": "hit",
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
