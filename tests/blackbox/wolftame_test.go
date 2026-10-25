// Wolf tame + sit parity vs the vanilla 26.2 server, measured off
// Wolf.mobInteract / TamableAnimal / SitWhenOrderedToGoal on
// server-26.2.jar.
//
// 26.2 split sitting in two: orderedToSit is a plain field that
// round-trips only the "Sitting" NBT tag; the idx18 flags byte packs
// inSittingPose (0x01) — applied by SitWhenOrderedToGoal, which never
// runs without AI — and tame (0x04). An untamed wolf + bone consumes
// one and rolls tryToTame's nextInt(3): success packs idx18=4 +
// idx19=<owner uuid> + entity_event 7 (hearts); failures emit event 6
// alone. Owner clicks flip orderedToSit silently — Sitting:1b→0b on
// data get, zero wire traffic; a non-owner click changes nothing. On
// an AI wolf the pose bit lands 4→5 a tick after tame.
//
// Collar dye (WOLF_COLLAR_DYES / CAT_COLLAR_DYES = #dyes): owner +
// dye repaints DATA_COLLAR_COLOR (idx21 wolf / idx23 cat, ser1) and
// consumes one. Divergence: wolf's same-color dye falls to
// super.mobInteract past the toggle arm — a no-op — while cat's
// falls INTO the owner toggle; a non-owner dye does nothing on both.
//
// Wolf armor (BODY slot): owner + wolf_armor equips + consumes; while
// worn every non-bypasses_wolf_armor hit absorbs into durability —
// wolf HP untouched; owner + armadillo_scute on a sitting wolf
// repairs max*0.125 and plays item.wolf_armor.repair.
package blackbox

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestWolfTameParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB", "BOATC"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	type wolfObs struct {
		event7       bool
		event6       int
		ownerSeen    bool
		flags        int // last idx18 value seen, -1 none
		sit1         float64
		sit2         float64
		sit3         float64
		ownerQ       string
		poseFlags    int // AI wolf's last idx18, -1 none
		collar       int32
		collarNBT    float64
		heldDye      float64
		sameColor    float64 // Sitting after a same-color dye click
		noOwnerDye   float64 // CollarColor after a non-owner dye click
		catCollar    int32
		catSit       float64 // cat toggles sit on a same-color dye
		bodyEquipped bool
		armorDmg1    float64
		armorDmg2    float64
		wolfHP       float64
		repairSnd    bool
		scuteN       float64
		unsit        float64 // Sitting after melee hits (hurtServer clears it)
		reposed      bool    // idx18 bit0 relands after the re-sit click
	}

	run := func(t *testing.T, name, addr string) wolfObs {
		o := wolfObs{flags: -1, poseFlags: -1}
		v := joinVehPair(t, addr)
		defer v.ca.Close()
		defer v.cb.Close()
		cc, prc, pwc := joinPlayBlackbox(t, addr, "BOATC")
		defer cc.Close()
		drainChunkWindow(t, name, v.ca, v.pra, v.pwa, 0)
		sink := newChatSinkTP(prc, pwc)

		sendCommand(t, v.pwa, "gamemode survival BOATA")
		sendCommand(t, v.pwa, "gamemode survival BOATC")
		sendCommand(t, v.pwa, "tp BOATA 8.5 -60 8.5")
		sendCommand(t, v.pwa, "tp BOATC 10.5 -60 8.5")
		wolf := summonTrackedNBT(t, v.ca, v.pra, v.pwa, pwc,
			"wolf", 9.5, -60, 8.5, "{NoAI:1b,Tags:[\"WT\"]}")
		if wolf == 0 {
			t.Fatalf("%s: no wolf spawn packet", name)
		}
		wolfStr := strconv.Itoa(int(wolf))

		var allFl []string
		var allEv []byte
		flagsOf := func() (int, bool) {
			for i := len(allFl) - 1; i >= 0; i-- {
				f := allFl[i]
				if strings.HasPrefix(f, wolfStr+":") {
					bv, err := strconv.Atoi(strings.SplitN(f, ":", 2)[1])
					return bv, err == nil
				}
			}
			return 0, false
		}
		watch := func(eid int32, window time.Duration) {
			lo := vehWatch(t, v, false, eid, window)
			allFl = append(allFl, lo.beeFlHist...)
			allEv = append(allEv, lo.entEv...)
			for _, st := range lo.entEv {
				if st == 7 {
					o.event7 = true
				}
				if st == 6 {
					o.event6++
				}
			}
			if _, ok := lo.uuidMeta[[2]int32{eid, 19}]; ok {
				o.ownerSeen = true
			}
		}
		sitNBT := func(sel string) float64 {
			st, val := dataGetSel(t, sink, pwc, sel, "Sitting")
			if st != "query" {
				return -1
			}
			return val
		}

		// One bone a click; tryToTame rolls nextInt(3) — success
		// packs idx18=4 (tame only — orderedToSit is a field) +
		// idx19=uuid + event 7; failures emit event 6.
		sendCommand(t, v.pwa,
			"item replace entity BOATA weapon.mainhand with minecraft:bone 24")
		time.Sleep(400 * time.Millisecond)
		sel := "@e[type=minecraft:wolf,tag=WT,limit=1]"
		tamed := false
		for i := 0; i < 24 && !tamed; i++ {
			sendInteractEntity(v.pwa, wolf)
			watch(wolf, 400*time.Millisecond)
			if fl, ok := flagsOf(); ok && fl == 4 {
				tamed = true
			}
		}
		if !tamed {
			heldQ, heldV := dataGetSel(t, sink, pwc, "BOATA", "SelectedItem.count")
			t.Fatalf("%s: 24 bone clicks never tamed (smokes=%d ev7=%v held=%s:%v fl=%v ev=%v)",
				name, o.event6, o.event7, heldQ, heldV, allFl, allEv)
		}
		o.flags, _ = flagsOf()
		o.sit1 = sitNBT(sel) // Sitting:1b — tame ordered the sit

		// Owner clicks flip the field silently: no wire emits,
		// Sitting 1→0→1 on data get.
		sendCommand(t, v.pwa,
			"item replace entity BOATA weapon.mainhand with minecraft:air")
		time.Sleep(300 * time.Millisecond)
		sendInteractEntity(v.pwa, wolf)
		watch(wolf, 300*time.Millisecond)
		o.sit2 = sitNBT(sel)
		sendInteractEntity(v.pwa, wolf)
		watch(wolf, 300*time.Millisecond)
		o.sit3 = sitNBT(sel)

		// Non-owner click: Sitting stays 1 — must not move the field.
		sendInteractEntity(pwc, wolf)
		watch(wolf, 300*time.Millisecond)
		if n := sitNBT(sel); n != o.sit3 {
			t.Fatalf("%s: non-owner click moved Sitting %v→%v", name, o.sit3, n)
		}
		st2, _ := dataGetSel(t, sink, pwc, sel, "Owner")
		o.ownerQ = st2

		// Collar dye: owner + cyan_dye repaints — idx21 emits the
		// dye id (cyan=9) and a bone is consumed. A second cyan
		// click repaints nothing and (wolf) does NOT toggle sit.
		sendCommand(t, v.pwa,
			"item replace entity BOATA weapon.mainhand with minecraft:cyan_dye 4")
		time.Sleep(300 * time.Millisecond)
		o.collar = -1
		sendInteractEntity(v.pwa, wolf)
		{
			lo := vehWatch(t, v, false, wolf, 400*time.Millisecond)
			allFl = append(allFl, lo.beeFlHist...)
			if c, ok := lo.intMeta[21]; ok {
				o.collar = c
			}
		}
		st3, v3 := dataGetSel(t, sink, pwc, sel, "CollarColor")
		if st3 == "query" {
			o.collarNBT = v3
		}
		_, hv := dataGetSel(t, sink, pwc, "BOATA", "SelectedItem.count")
		o.heldDye = hv
		sendInteractEntity(v.pwa, wolf) // same color — swallows
		watch(wolf, 300*time.Millisecond)
		o.sameColor = sitNBT(sel)
		sendInteractEntity(pwc, wolf) // non-owner dye — nothing
		watch(wolf, 300*time.Millisecond)
		_, o.noOwnerDye = dataGetSel(t, sink, pwc, sel, "CollarColor")

		// Cat leg: the same-color dye DOES toggle — the only
		// divergent arm vs wolf. Tame with cod (1-in-3 + sit order).
		cat := summonTrackedNBT(t, v.ca, v.pra, v.pwa, pwc,
			"cat", 11.5, -60, 8.5, "{NoAI:1b}")
		o.catCollar = -1
		if cat != 0 {
			catSel := "@e[type=minecraft:cat,limit=1]"
			sendCommand(t, v.pwa,
				"item replace entity BOATA weapon.mainhand with minecraft:cod 24")
			time.Sleep(300 * time.Millisecond)
			for i := 0; i < 24; i++ {
				sendInteractEntity(v.pwa, cat)
				lo := vehWatch(t, v, false, cat, 300*time.Millisecond)
				if _, ok := lo.uuidMeta[[2]int32{cat, 19}]; ok {
					break
				}
			}
			sendCommand(t, v.pwa,
				"item replace entity BOATA weapon.mainhand with minecraft:cyan_dye 4")
			time.Sleep(300 * time.Millisecond)
			sendInteractEntity(v.pwa, cat) // repaint
			lo := vehWatch(t, v, false, cat, 400*time.Millisecond)
			if c, ok := lo.intMeta[23]; ok {
				o.catCollar = c
			}
			before := sitNBT(catSel)
			sendInteractEntity(v.pwa, cat) // same color → toggle
			vehWatch(t, v, false, cat, 300*time.Millisecond)
			after := sitNBT(catSel)
			if before != after {
				o.catSit = 1
			}
		}

		// AI wolf: same tame flow, then SitWhenOrderedToGoal packs
		// the pose bit — idx18 4→5 within a tick or two.
		awolf := summonTrackedNBT(t, v.ca, v.pra, v.pwa, pwc,
			"wolf", 12.5, -60, 8.5, "{Tags:[\"WTAI\"]}")
		if awolf != 0 {
			sendCommand(t, v.pwa,
				"item replace entity BOATA weapon.mainhand with minecraft:bone 24")
			time.Sleep(300 * time.Millisecond)
			awStr := strconv.Itoa(int(awolf))
			for i := 0; i < 24; i++ {
				sendInteractEntity(v.pwa, awolf)
				watch(awolf, 400*time.Millisecond)
				done := false
				for _, f := range allFl {
					if strings.HasPrefix(f, awStr+":") {
						if bv, _ := strconv.Atoi(strings.SplitN(f, ":", 2)[1]); bv >= 4 {
							o.poseFlags = bv
							done = true
						}
					}
				}
				if done {
					break
				}
			}

			// Wolf armor: the sitting AI wolf takes wolf_armor onto
			// BODY (equipment.body.id answers, one stack consumed),
			// hits then absorb into durability — Health untouched,
			// damage>0 — and armadillo_scute repairs 12.5% of the
			// 64 max (8) with item.wolf_armor.repair (wire 1795).
			awSel := "@e[type=minecraft:wolf,tag=WTAI,limit=1]"
			sendCommand(t, v.pwa, "tp BOATA 11 -60 8.5")
			sendCommand(t, v.pwa,
				"item replace entity BOATA weapon.mainhand with minecraft:wolf_armor 1")
			time.Sleep(300 * time.Millisecond)
			sendInteractEntity(v.pwa, awolf)
			watch(awolf, 300*time.Millisecond)
			st4, _ := dataGetSel(t, sink, pwc, awSel, "equipment.body.id")
			o.bodyEquipped = st4 == "query"
			if st4 != "query" {
				hq, hv := dataGetSel(t, sink, pwc, "BOATA", "SelectedItem.id")
				cq, cv := dataGetSel(t, sink, pwc, awSel, "CollarColor")
				sq, sv := dataGetSel(t, sink, pwc, awSel, "Sitting")
				t.Logf("%s: equip miss held=%s:%v collar=%s:%v sit=%s:%v",
					name, hq, hv, cq, cv, sq, sv)
			}
			sendCommand(t, v.pwa,
				"item replace entity BOATA weapon.mainhand with minecraft:iron_sword")
			time.Sleep(300 * time.Millisecond)
			for i := 0; i < 4; i++ {
				sendAttackEntity(v.pwa, awolf)
				watch(awolf, 250*time.Millisecond)
			}
			st5, v5 := dataGetSel(t, sink, pwc, awSel,
				`equipment.body.components."minecraft:damage"`)
			if st5 == "query" {
				o.armorDmg1 = v5
			}
			_, o.wolfHP = dataGetSel(t, sink, pwc, awSel, "Health")
			// hurtServer clears orderedToSit on every accepted hit —
			// the wolf stands back up even while the armor absorbs.
			_, o.unsit = dataGetSel(t, sink, pwc, awSel, "Sitting")
			sendCommand(t, v.pwa,
				"item replace entity BOATA weapon.mainhand with minecraft:armadillo_scute 4")
			time.Sleep(300 * time.Millisecond)
			// First scute click: not in sitting pose → the repair arm
			// misses and the click falls to the owner toggle, which
			// re-orders the sit. Watch idx18 for the pose bit relanding.
			sendInteractEntity(v.pwa, awolf)
			for i := 0; i < 10; i++ {
				lo := vehWatch(t, v, false, awolf, 200*time.Millisecond)
				allFl = append(allFl, lo.beeFlHist...)
				for j := len(allFl) - 1; j >= 0; j-- {
					f := allFl[j]
					if strings.HasPrefix(f, awStr+":") {
						bv, _ := strconv.Atoi(strings.SplitN(f, ":", 2)[1])
						if bv&1 != 0 {
							o.reposed = true
						}
						break
					}
				}
				if o.reposed {
					break
				}
			}
			// Second scute click: sitting pose → repair arm fires.
			sendInteractEntity(v.pwa, awolf)
			lo := vehWatch(t, v, false, awolf, 400*time.Millisecond)
			for _, sid := range lo.soundIds {
				if sid == 1795 { // wire id = registry+1
					o.repairSnd = true
				}
			}
			st6, v6 := dataGetSel(t, sink, pwc, awSel,
				`equipment.body.components."minecraft:damage"`)
			if st6 == "query" {
				o.armorDmg2 = v6
			}
			_, o.scuteN = dataGetSel(t, sink, pwc, "BOATA", "SelectedItem.count")
		}
		return o
	}

	check := func(t *testing.T, name string, o wolfObs) {
		if !o.event7 {
			t.Fatalf("%s: never saw entity_event 7 (hearts) on tame", name)
		}
		if !o.ownerSeen {
			t.Fatalf("%s: no idx19 owner-uuid emit on tame", name)
		}
		if o.flags != 4 {
			t.Fatalf("%s: tame flags=%d want 4 (tame bit only)", name, o.flags)
		}
		if o.sit1 != 1 || o.sit2 != 0 || o.sit3 != 1 {
			t.Fatalf("%s: Sitting NBT %v→%v→%v want 1→0→1", name, o.sit1, o.sit2, o.sit3)
		}
		if o.ownerQ != "query" {
			t.Fatalf("%s: data get Owner=%s want a uuid answer", name, o.ownerQ)
		}
		if o.poseFlags != 5 {
			t.Fatalf("%s: AI wolf pose flags=%d want 5 (sit goal applied)", name, o.poseFlags)
		}
		if o.collar != 9 {
			t.Fatalf("%s: no idx21=9 collar emit on cyan dye (got %d)", name, o.collar)
		}
		if o.collarNBT != 9 {
			t.Fatalf("%s: CollarColor NBT=%v want 9", name, o.collarNBT)
		}
		if o.heldDye != 3 {
			t.Fatalf("%s: dye count=%v want 3 (one consumed)", name, o.heldDye)
		}
		if o.sameColor != 1 {
			t.Fatalf("%s: same-color dye toggled Sitting (=%v want 1)", name, o.sameColor)
		}
		if o.noOwnerDye != 9 {
			t.Fatalf("%s: non-owner dye repainted collar (=%v want 9)", name, o.noOwnerDye)
		}
		if o.catCollar != 9 {
			t.Fatalf("%s: cat collar idx23=%d want 9", name, o.catCollar)
		}
		if o.catSit != 1 {
			t.Fatalf("%s: cat same-color dye never toggled Sitting", name)
		}
		if !o.bodyEquipped {
			t.Fatalf("%s: wolf_armor click never equipped BODY (equipment.body.id)", name)
		}
		if o.armorDmg1 <= 0 {
			t.Fatalf("%s: armor took no damage from hits (=%v)", name, o.armorDmg1)
		}
		if o.wolfHP != 40 {
			t.Fatalf("%s: wolf lost health through armor (=%v want 40)", name, o.wolfHP)
		}
		if o.unsit != 0 {
			t.Fatalf("%s: hurt wolf stayed ordered-sitting (Sitting=%v)", name, o.unsit)
		}
		if !o.reposed {
			t.Fatalf("%s: scute click never re-ordered the sit (idx18 bit0)", name)
		}
		want2 := o.armorDmg1 - 8
		if want2 < 0 {
			want2 = 0
		}
		if o.armorDmg2 != want2 {
			t.Fatalf("%s: repair damage=%v want %v (%v-8)", name, o.armorDmg2, want2, o.armorDmg1)
		}
		if !o.repairSnd {
			t.Fatalf("%s: no item.wolf_armor.repair sound on scute click", name)
		}
		if o.scuteN != 3 {
			t.Fatalf("%s: scute count=%v want 3 (shrink 1)", name, o.scuteN)
		}
		t.Logf("%s: ev7=%v smokes=%d owner=%v flags=%d sit=%v/%v/%v aiPose=%d collar=%d/%v dye=%v cat=%d/%v armor=%v:%v→%v hp=%v unsit=%v reposed=%v snd=%v scute=%v Owner=%s",
			name, o.event7, o.event6, o.ownerSeen, o.flags, o.sit1, o.sit2, o.sit3,
			o.poseFlags, o.collar, o.collarNBT, o.heldDye, o.catCollar, o.catSit,
			o.bodyEquipped, o.armorDmg1, o.armorDmg2, o.wolfHP, o.unsit, o.reposed,
			o.repairSnd, o.scuteN, o.ownerQ)
	}

	check(t, "vanilla", run(t, "vanilla", targets.vanilla))
	check(t, "mc-go", run(t, "mc-go", targets.mcgo))
}
