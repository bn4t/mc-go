package blackbox

import (
	"net"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
)

// equipSegRe marks slot-segment starts inside a flattened
// `data get entity @s equipment` reply: a slot key followed by its
// `count` or (component-carrying stacks) `components` subkey. The
// count and item id are then found inside the segment — flattened
// SNBT glues keys with no separators, so everything must key off
// segment boundaries.
var equipSegRe = regexp.MustCompile(`(mainhand|offhand|head|chest|legs|feet|body|saddle)(?:count\d|components)`)
var equipCountRe = regexp.MustCompile(`count(\d+)`)
var equipIDRe = regexp.MustCompile(`idminecraft:([a-z0-9_.]+)`)

// equipScan summons `n` of a mob type spread along +x, queries every
// entity's equipment, and tallies slot→item hits.
func equipScan(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, obs *fleeObs, typ string, n int, tag string) map[string]map[string]int {
	t.Helper()
	for i := 0; i < n; i++ {
		cmd := "summon minecraft:" + typ + " " + itoaF(104.5+float64(i%20)) + " -59 " + itoaF(0.5+float64(i/20))
		if tag != "" {
			cmd += " " + tag
		}
		sendCommand(t, pw, cmd)
	}
	fleePump(t, c, pr, pw, obs, 1500*time.Millisecond)
	sendCommand(t, pw, "execute as @e[type=minecraft:"+typ+"] run data get entity @s equipment")
	base := len(obs.chats)
	fleePump(t, c, pr, pw, obs, 1200*time.Millisecond)
	out := map[string]map[string]int{}
	for _, ln := range obs.chats[base:] {
		if !strings.Contains(ln, "entity.minecraft."+typ) {
			continue
		}
		idx := equipSegRe.FindAllStringSubmatchIndex(ln, -1)
		for i, m := range idx {
			end := len(ln)
			if i+1 < len(idx) {
				end = idx[i+1][0]
			}
			seg := ln[m[0]:end]
			cnt := 1
			if cm := equipCountRe.FindStringSubmatch(seg); cm != nil {
				cnt, _ = strconv.Atoi(cm[1])
			}
			idm := equipIDRe.FindStringSubmatch(seg)
			if idm == nil {
				continue
			}
			slot, item := ln[m[2]:m[3]], idm[1]
			if out[slot] == nil {
				out[slot] = map[string]int{}
			}
			out[slot][item] += cnt
		}
	}
	return out
}

func itoaF(f float64) string {
	return strconv.FormatFloat(f, 'f', 1, 64)
}

// flagScan counts entities of typ whose scalar NBT key reads 1b.
func flagScan(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, obs *fleeObs, typ, key string) int {
	t.Helper()
	sendCommand(t, pw, "execute as @e[type=minecraft:"+typ+"] run data get entity @s "+key)
	base := len(obs.chats)
	fleePump(t, c, pr, pw, obs, 1200*time.Millisecond)
	n := 0
	for _, ln := range obs.chats[base:] {
		if strings.Contains(ln, "entity.minecraft."+typ) && strings.HasSuffix(ln, "1b") {
			n++
		}
	}
	return n
}

// finalizeSpawn equipment on /summon. Vanilla's generic armor cascade
// is gated by specialMultiplier=0 on a fresh flat world (effective
// difficulty id·0.75 < 2), so zombies/skeletons never wear armor and
// nothing rolls enchantments; the unconditional rolls carry the
// signal: piglin 50% crossbow + per-slot 10% gold armor, drowned ~6%
// trident / ~4% rod / 3% offhand nautilus, pillager always crossbow,
// skeleton always bow.
func TestSummonEquipmentParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	type row map[string]map[string]int
	type result struct {
		piglin, drowned, zombie, pillager, skeleton row
		babies, lefties                             int
	}
	run := func(addr string) result {
		var r result
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(200 * time.Second))
		obs := &fleeObs{}
		sendCommand(t, pw, "gamemode creative P")
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		sendCommand(t, pw, "time set 6000")
		sendCommand(t, pw, "tp P 100.5 -60 0.5")
		fleePump(t, c, pr, pw, obs, 800*time.Millisecond)

		// Bare summons only — vanilla skips finalizeSpawn entirely
		// when an NBT tag is present, so a tagged summon is always
		// bare-handed. Piglins zombify at ~15s; the scan finishes
		// well before conversion.
		r.piglin = equipScan(t, c, pr, pw, obs, "piglin", 40, "")
		r.babies = flagScan(t, c, pr, pw, obs, "piglin", "IsBaby")
		r.drowned = equipScan(t, c, pr, pw, obs, "drowned", 48, "")
		r.zombie = equipScan(t, c, pr, pw, obs, "zombie", 30, "")
		r.pillager = equipScan(t, c, pr, pw, obs, "pillager", 60, "")
		r.skeleton = equipScan(t, c, pr, pw, obs, "skeleton", 10, "")
		r.lefties = flagScan(t, c, pr, pw, obs, "zombie", "LeftHanded")
		return r
	}

	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	t.Logf("van %+v", van)
	t.Logf("mc  %+v", mc)

	for name, r := range map[string]result{"vanilla": van, "mc-go": mc} {
		// Piglin: 20% baby roll gates everything — each adult gets a
		// weapon (~50% crossbow, 5% spear), each adult armor slot 10%.
		cb := r.piglin["mainhand"]["crossbow"]
		sword := r.piglin["mainhand"]["golden_sword"]
		spear := r.piglin["mainhand"]["golden_spear"]
		weapons := cb + sword + spear
		if weapons < 22 || weapons > 40 {
			t.Errorf("%s piglin weapons=%d (%d/%d/%d) outside ~80%% band", name, weapons, cb, sword, spear)
		}
		if cb < weapons/4 || cb > 3*weapons/4 {
			t.Errorf("%s piglin crossbows=%d/%d outside ~50%% band", name, cb, weapons)
		}
		gold := 0
		for _, slot := range []string{"head", "chest", "legs", "feet"} {
			for item, n := range r.piglin[slot] {
				if !strings.HasPrefix(item, "golden_") {
					t.Errorf("%s piglin %s rolled non-golden %s", name, slot, item)
				}
				gold += n
			}
		}
		if gold > weapons {
			t.Errorf("%s piglin gold pieces=%d exceeds armed adults=%d", name, gold, weapons)
		}
		// Drowned: ~10% weapon (trident≈rod×1.67), ~3% offhand shell,
		// never armor.
		tri := r.drowned["mainhand"]["trident"]
		rod := r.drowned["mainhand"]["fishing_rod"]
		if tri > 12 || rod > 8 || tri+rod > 14 {
			t.Errorf("%s drowned weapons trident=%d rod=%d outside ~10%% band", name, tri, rod)
		}
		if n := r.drowned["offhand"]["nautilus_shell"]; n > 5 {
			t.Errorf("%s drowned nautilus=%d outside ~3%% band", name, n)
		}
		for _, slot := range []string{"head", "chest", "legs", "feet"} {
			if len(r.drowned[slot]) > 0 {
				t.Errorf("%s drowned rolled armor %v — drowned skips the cascade", name, r.drowned[slot])
			}
		}
		// Fresh world → specialMultiplier 0: no armor cascade, no
		// enchantments, no zombie weapon.
		if len(r.zombie) > 2 {
			t.Errorf("%s zombie slots %v — cascade should not fire", name, r.zombie)
		}
		if n := r.pillager["mainhand"]["crossbow"]; n != 60 {
			t.Errorf("%s pillager crossbows=%d/60", name, n)
		}
		// PatrolLeader roll: ~6% of non-patrol spawns get the ominous
		// (white) banner on HEAD.
		if n := r.pillager["head"]["white_banner"]; n > 10 {
			t.Errorf("%s pillager patrol banners=%d outside ~6%% band", name, n)
		}
		for item := range r.pillager["head"] {
			if item != "white_banner" {
				t.Errorf("%s pillager head rolled %s — only white_banner allowed", name, item)
			}
		}
		if n := r.skeleton["mainhand"]["bow"]; n != 10 {
			t.Errorf("%s skeleton bows=%d/10", name, n)
		}

		// Piglin 20% baby roll; zombie-family 5% left-handed roll.
		if r.babies < 2 || r.babies > 16 {
			t.Errorf("%s piglin babies=%d/40 outside ~20%% band", name, r.babies)
		}
		if r.lefties > 8 {
			t.Errorf("%s zombie left-handed=%d/30 outside ~5%% band", name, r.lefties)
		}
	}
}
