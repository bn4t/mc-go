//go:build blackbox

// FishingHook's entity-hook path, measured on the vanilla 26.2 jar:
//
//   - FLYING's checkCollision: ProjectileUtil.getHitResultOnMoveVector
//     sweeps the movement ray against canHitEntity targets — living
//     mobs, live ItemEntity stacks, other players, the owner
//     excluded — and the closest hit becomes hookedIn.
//   - setHookedEntity writes DATA_HOOKED_ENTITY (metadata idx8,
//     varint) = hooked eid + 1; the next tick flips to
//     HOOKED_IN_ENTITY, which rides the entity at getY(0.8) until it
//     leaves or dies, then unhooks back to FLYING with idx8 → 0.
//   - retrieve's hooked branch: pullEntity adds (owner − bobber)·0.1
//     to the entity's delta, fishing_rod_hooked fires with an empty
//     caught list, entity_event 31 emits on the bobber, and the wear
//     is 3 for an ItemEntity, 5 for anything else. A bobber resting
//     onGround retrieves for 2.
package blackbox

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"strconv"
)

// fishHookLeg is one leg's measured wire facts: the bobber eids seen,
// every idx8 emit (hook is eid+1, unhook is 0), the entity_event-31
// carrier, motion counts per hooked eid, and the rod damage read
// after each retrieve.
type fishHookLeg struct {
	bobbers   []int32
	hookMeta  map[int32]int32
	hookHist  []string
	hookEvent int32
	motion    map[int32]int
	hookedA   int32
	unhookEid int32
	pullA     int
	dmgA      float64
	dmgC      float64
	stC       string
}

func TestFishingHookParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB", "BOATC"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	run := func(t *testing.T, name, addr string) fishHookLeg {
		v := joinVehPair(t, addr)
		defer v.ca.Close()
		defer v.cb.Close()
		// Query conn: vehWatch and summonTrackedNBT read conns A and B
		// directly, so a chatSink there would race them mid-packet and
		// desync silently. BOATC's sink is the sole reader on its conn.
		cc, prc, pwc := joinPlayBlackbox(t, addr, "BOATC")
		defer cc.Close()
		drainChunkWindow(t, "fishhook", v.ca, v.pra, v.pwa, 0)
		sink := newChatSinkTP(prc, pwc)

		leg := fishHookLeg{hookMeta: map[int32]int32{}, motion: map[int32]int{}}

		// Bank + open pool: the player fishes north off the z=14.5
		// bank edge; the water column runs z 6..12. The stone pad is
		// deliberately deep — a grounded bobber slides ~2 blocks from
		// residual cast speed and must never reach natural water.
		sendCommand(t, v.pwa, "fill 4 -61 4 24 -61 24 minecraft:stone")
		sendCommand(t, v.pwa, "fill 4 -62 4 24 -62 24 minecraft:stone")
		sendCommand(t, v.pwa, "fill 6 -62 6 12 -61 12 minecraft:water")
		sendCommand(t, v.pwa, "gamemode spectator BOATB")
		sendCommand(t, v.pwa, "tp BOATB 60 -60 60")
		sendCommand(t, v.pwa, "gamemode spectator BOATC")
		sendCommand(t, v.pwa, "tp BOATC 60 -60 60")
		// The vanilla world defaults to creative, where hurtAndBreak is
		// a no-op — the rod must be held in survival for the wear reads.
		sendCommand(t, v.pwa, "gamemode survival BOATA")
		sendCommand(t, v.pwa, "tp BOATA 8.5 -60 14.5 180 45")

		rod := func() {
			sendCommand(t, v.pwa, "item replace entity BOATA weapon.mainhand with minecraft:fishing_rod")
		}
		pitch := float32(45)
		yaw := float32(180)
		use := func() {
			var ub protocol.Buffer
			ub.VarInt(0) // main hand
			ub.VarInt(0) // sequence
			ub.Float(yaw)
			ub.Float(pitch) // vanilla casts from the packet's own rotation
			_ = v.pwa.WritePacket(server.IDPlayUseItemSB, ub.Bytes())
		}
		fold := func(o vehObs) {
			leg.bobbers = append(leg.bobbers, o.bobberEIDs...)
			leg.hookHist = append(leg.hookHist, o.hookHist...)
			for rid := range o.removed {
				leg.hookHist = append(leg.hookHist,
					"rm:"+strconv.Itoa(int(rid)))
			}
			for be, v8 := range o.hookMeta {
				leg.hookMeta[be] = v8
			}
			if o.hookEvent != 0 {
				leg.hookEvent = o.hookEvent
			}
			for me, n := range o.motion {
				leg.motion[me] += n
			}
		}
		// hooked returns the newest nonzero idx8 emit (hooked eid+1).
		// The hooked entity is whatever sits on the ray — the test
		// doesn't pin which summon the ray clipped first, only that a
		// hook fired and the emit is eid+1.
		hooked := func() int32 {
			var hv int32
			for _, v8 := range leg.hookMeta {
				if v8 > 0 {
					hv = v8
				}
			}
			return hv
		}
		isBobber := func(eid int32) bool {
			for _, be := range leg.bobbers {
				if be == eid {
					return true
				}
			}
			return false
		}
		// cast retries like fishing_test: a rare cast that threads
		// the ray around the box is reeled back in and re-cast, and
		// each attempt holds a FRESH rod so the damage read after the
		// hooked retrieve is exact (a missed bobber's own retrieve
		// wear never lands on the measured rod).
		cast := func() bool {
			for attempt := 0; attempt < 4 && hooked() == 0; attempt++ {
				rod()
				use() // cast
				deadline := time.Now().Add(2500 * time.Millisecond)
				for time.Now().Before(deadline) && hooked() == 0 {
					fold(vehWatch(t, v, false, 0, 300*time.Millisecond))
				}
				if hooked() == 0 {
					use() // retrieve the missed bobber
					for i := 0; i < 3; i++ {
						fold(vehWatch(t, v, false, 0, 300*time.Millisecond))
					}
				}
			}
			return hooked() != 0
		}
		reel := func() {
			use()
			for i := 0; i < 3; i++ {
				fold(vehWatch(t, v, false, 0, 700*time.Millisecond))
			}
		}
		rodDmg := func() (string, float64) {
			return dataGetSel(t, sink, pwc, "BOATA", `SelectedItem.components."minecraft:damage"`)
		}

		// Case A — a pig on the bank edge in the descent path.
		leg.hookMeta = map[int32]int32{}
		rod()
		if summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa, "pig",
			8.5, -60, 13.5, "{NoAI:1b}") == 0 {
			t.Fatalf("%s: pig not tracked", name)
		}
		if !cast() {
			t.Fatalf("%s: bobber never hooked the pig (idx8): hookMeta=%v bobbers=%v",
				name, leg.hookMeta, leg.bobbers)
		}
		leg.hookedA = hooked()
		fold(vehWatch(t, v, false, 0, 400*time.Millisecond))
		reel()
		_, leg.dmgA = rodDmg()
		leg.pullA = leg.motion[leg.hookedA-1]

		// Case B — a fresh pig under a fresh hook dies: HOOKED_IN_ENTITY
		// sees the hooked entity removed, clears hookedIn back to
		// FLYING, and re-emits idx8 → 0.
		sendCommand(t, v.pwa, "kill @e[type=minecraft:pig]")
		leg.hookMeta = map[int32]int32{}
		rod()
		if summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa, "pig",
			8.5, -60, 13.5, "{NoAI:1b}") == 0 {
			t.Fatalf("%s: second pig not tracked", name)
		}
		if !cast() {
			t.Fatalf("%s: bobber never hooked the second pig (idx8): hookMeta=%v bobbers=%v",
				name, leg.hookMeta, leg.bobbers)
		}
		sendCommand(t, v.pwa, "kill @e[type=minecraft:pig]")
		leg.hookMeta = map[int32]int32{}
		var unhook int32
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) && unhook == 0 {
			fold(vehWatch(t, v, false, 0, 300*time.Millisecond))
			for be, v8 := range leg.hookMeta {
				if v8 == 0 && isBobber(be) {
					unhook = be
				}
			}
		}
		leg.unhookEid = unhook
		reel()

		// Case C — a steep cast onto the bank lands the bobber
		// onGround; retrieving it wears the rod for 2. The bobber
		// keeps ~0.6 blocks/tick of forward speed after touchdown and
		// slides several blocks along the bank — face the player south
		// so the slide runs deeper onto the stone, never off the pool
		// edge.
		rod()
		sendCommand(t, v.pwa, "tp BOATA 8.5 -60 13.5 0 75")
		pitch = 75
		yaw = 0
		use()
		for i := 0; i < 3; i++ {
			fold(vehWatch(t, v, false, 0, 700*time.Millisecond))
		}
		reel()
		stC, dmgC := rodDmg()
		leg.dmgC = dmgC
		leg.stC = stC
		return leg
	}

	vl := run(t, "vanilla", targets.vanilla)
	ml := run(t, "mc-go", targets.mcgo)
	for name, leg := range map[string]fishHookLeg{"vanilla": vl, "mc-go": ml} {
		if len(leg.bobbers) == 0 {
			t.Fatalf("%s: no fishing_bobber spawned", name)
		}
		if leg.hookEvent == 0 {
			t.Fatalf("%s: entity_event 31 never emitted on retrieve", name)
		}
		if leg.dmgA != 5 {
			t.Fatalf("%s: mob-hook retrieve damage %v, want 5", name, leg.dmgA)
		}
		// Vanilla's onGround is recomputed by Entity.move() every tick —
		// a bobber resting on the bank oscillates grounded↔airborne, so
		// a dry-land retrieve wears the rod for either 0 or 2 depending
		// on tick phase. Both outcomes are pinned.
		if !(leg.stC == "query" && leg.dmgC == 2) &&
			!(leg.stC == "nothing_found" && leg.dmgC == -1) {
			t.Fatalf("%s: grounded retrieve damage %v (status %s), want 2 or fresh rod", name, leg.dmgC, leg.stC)
		}
		if leg.pullA == 0 {
			t.Fatalf("%s: hooked mob (eid %d) never emitted set_entity_motion", name, leg.hookedA-1)
		}
		if leg.unhookEid == 0 {
			t.Fatalf("%s: killing the hooked entity never re-emitted idx8=0", name)
		}
	}
}
