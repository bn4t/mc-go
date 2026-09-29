//go:build blackbox

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
		event7    bool
		event6    int
		ownerSeen bool
		flags     int // last idx18 value seen, -1 none
		sit1      float64
		sit2      float64
		sit3      float64
		ownerQ    string
		poseFlags int // AI wolf's last idx18, -1 none
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
			"item replace entity BOATA weapon.mainhand with minecraft:bone 8")
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

		// AI wolf: same tame flow, then SitWhenOrderedToGoal packs
		// the pose bit — idx18 4→5 within a tick or two.
		awolf := summonTrackedNBT(t, v.ca, v.pra, v.pwa, pwc,
			"wolf", 12.5, -60, 8.5, "{Tags:[\"WT\"]}")
		if awolf != 0 {
			sendCommand(t, v.pwa,
				"item replace entity BOATA weapon.mainhand with minecraft:bone 8")
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
		t.Logf("%s: ev7=%v smokes=%d owner=%v flags=%d sit=%v/%v/%v aiPose=%d Owner=%s",
			name, o.event7, o.event6, o.ownerSeen, o.flags, o.sit1, o.sit2, o.sit3,
			o.poseFlags, o.ownerQ)
	}

	check(t, "vanilla", run(t, "vanilla", targets.vanilla))
	check(t, "mc-go", run(t, "mc-go", targets.mcgo))
}
