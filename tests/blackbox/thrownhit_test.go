//go:build blackbox

package blackbox

import (
	"testing"
	"time"

	"mc-go/internal/server"
)

// TestThrownHitParity pins the entity-hit half of thrown items —
// ThrowableItemProjectile.onHitEntity's hurt(source, 0) lands a
// zero-damage hit that still runs the full hurt tail on the wire:
// a villager victim broadcasts VILLAGER_ANGRY (entity_event 13)
// ahead of the damage_event (minecraft:thrown, id 45) carrying the
// victim + cause/direct entity references, the victim's hurt voice,
// and a knockback set_entity_motion. Egg/Snowball.onHit also
// broadcast entity_event 3 on the projectile — measured shipping on
// entity hits only — while the xp bottle broadcasts nothing. A
// second hit inside the invulnerability window is graced: event 3
// still ships but no damage_event, no sound, no knockback, no
// VILLAGER_ANGRY.
func TestThrownHitParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("ThrownHit"),
	}, nil)
	defer targets.cleanup()

	for _, tc := range []struct{ tag, addr string }{
		{"vanilla", targets.vanilla},
		{"mc-go", targets.mcgo},
	} {
		t.Run(tc.tag, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, "ThrownHit")
			defer c.Close()
			drainToJoinBlackbox(t, tc.tag, pr)
			sendCommand(t, pw, "gamemode survival @s")
			sendCommand(t, pw, "tp @s 60.5 -60 60.5 -90 0")
			// NoAI + NoGravity villager 3 blocks east, floated 0.5 so
			// its hitbox centers on the eye-line throw (~1.0 margin
			// both ways — spread misses at the old ~0.4 top margin let
			// a follow-up throw land as a fresh hit on either server).
			sendCommand(t, pw, "summon minecraft:villager 63.5 -59.5 60.5 {NoAI:1b,NoGravity:1b}")
			sendCommand(t, pw, "give @s minecraft:egg 16")
			ev := eggDrainFor(t, c, pr, pw, 1200*time.Millisecond)
			villagerID := findSpawn(t, tc.tag, ev, "villager")

			hurt, _ := server.SoundEventID("entity.villager.hurt")
			countKind := func(ev []eggEvent, kind string, eid int32) int {
				n := 0
				for _, e := range ev {
					if e.kind == kind && e.eid == eid {
						n++
					}
				}
				return n
			}
			angryCountFor := func(eid int32, ev []eggEvent) int {
				n := 0
				for _, e := range ev {
					if e.kind == "event" && e.eid == eid && e.b0 == 13 {
						n++
					}
				}
				return n
			}
			angryCount := func(ev []eggEvent) int {
				return angryCountFor(villagerID, ev)
			}
			assertHitWire := func(ev []eggEvent, projName string, projEids []int32, seq int32) {
				t.Helper()
				assertAckCovers(t, tc.tag, ev, seq)
				dmgIdx, angryIdx := -1, -1
				for i := range ev {
					if ev[i].kind == "dmg" && ev[i].eid == villagerID {
						dmgIdx = i
					}
					if ev[i].kind == "event" && ev[i].eid == villagerID && ev[i].b0 == 13 {
						angryIdx = i
					}
				}
				if dmgIdx < 0 {
					t.Fatalf("%s: no damage_event for victim %d on %s hit", tc.tag, villagerID, projName)
				}
				dmg := ev[dmgIdx]
				if dmg.etype != 45 {
					t.Fatalf("%s: damage_event type = %d — vanilla sends damage_type thrown (45)", tc.tag, dmg.etype)
				}
				hit := false
				for _, id := range projEids {
					if dmg.data == id+1 {
						hit = true
					}
				}
				if !hit {
					t.Fatalf("%s: damage_event direct = %d — want one of projectile ids %v (+1 encoded)", tc.tag, dmg.data, projEids)
				}
				if angryIdx < 0 {
					t.Fatalf("%s: no VILLAGER_ANGRY (event 13) on victim %d — villager reputation hook", tc.tag, villagerID)
				}
				if angryIdx > dmgIdx {
					t.Fatalf("%s: VILLAGER_ANGRY must precede damage_event on the wire", tc.tag)
				}
				motion, snd := false, false
				for _, e := range ev {
					if e.kind == "motion" && e.eid == villagerID {
						motion = true
					}
					if e.kind == "sound" && e.eid == hurt+1 {
						snd = true
					}
				}
				if !motion {
					t.Fatalf("%s: no set_entity_motion for victim %d — vanilla applies hurt knockback", tc.tag, villagerID)
				}
				if !snd {
					t.Fatalf("%s: no entity.villager.hurt sound on the landed hit", tc.tag)
				}
				for _, id := range projEids {
					assertRemoved(t, tc.tag, ev, id)
				}
			}

			// Legs 1+2 — two eggs back-to-back inside one
			// invulnerability window: the first lands the full hurt
			// tail; the second hits ~1-2 ticks later and is graced —
			// its egg still breaks (event 3 + remove) but hurt()
			// returns false: no damage_event, no sound, no knockback,
			// no VILLAGER_ANGRY.
			sendUseItemAim(t, pw, 0, 1, -90, 0)
			sendUseItemAim(t, pw, 0, 2, -90, 0)
			ev = eggDrainFor(t, c, pr, pw, 2500*time.Millisecond)
			assertAckCovers(t, tc.tag, ev, 1)
			assertAckCovers(t, tc.tag, ev, 2)
			var eggIDs []int32
			for _, e := range ev {
				if e.kind == "spawn" && e.etype == mustEType(t, "egg") {
					eggIDs = append(eggIDs, e.eid)
				}
			}
			if len(eggIDs) != 2 {
				t.Fatalf("%s: expected 2 egg spawns, got %d", tc.tag, len(eggIDs))
			}
			assertHitWire(ev, "egg", eggIDs, 1)
			if !hasEvent3(t, ev, eggIDs[0]) || !hasEvent3(t, ev, eggIDs[1]) {
				t.Fatalf("%s: both eggs must broadcast entity_event 3 on impact", tc.tag)
			}
			if dmgCount := countKind(ev, "dmg", villagerID); dmgCount != 1 {
				t.Fatalf("%s: %d damage_events for two hits — the graced second must produce none", tc.tag, dmgCount)
			}
			if sndCount := countKind(ev, "sound", hurt+1); sndCount != 1 {
				t.Fatalf("%s: %d hurt sounds for two hits — the graced second must produce none", tc.tag, sndCount)
			}
			if n := angryCount(ev); n != 1 {
				t.Fatalf("%s: %d VILLAGER_ANGRY events for two hits — the graced second must produce none", tc.tag, n)
			}
			assertRemoved(t, tc.tag, ev, eggIDs[1])

			// A leg-1/2 egg can randomly hatch a chicken that wanders
			// into the lane and eats later throws — clear hatchlings.
			sendCommand(t, pw, "kill @e[type=minecraft:chicken]")

			// Leg 3 — snowball, after the invulnerability window.
			sendCommand(t, pw, "clear @s")
			sendCommand(t, pw, "give @s minecraft:snowball 16")
			eggDrainFor(t, c, pr, pw, 1500*time.Millisecond)
			sendUseItemAim(t, pw, 0, 3, -90, 0)
			ev = eggDrainFor(t, c, pr, pw, 1500*time.Millisecond)
			sbID := assertThrowSpawn(t, tc.tag, ev, "snowball")
			assertHitWire(ev, "snowball", []int32{sbID}, 3)
			if !hasEvent3(t, ev, sbID) {
				t.Fatalf("%s: snowball entity-hit produced no entity_event 3", tc.tag)
			}

			// Leg 4 — xp bottle: ThrownExperienceBottle never
			// overrides onHitEntity, so the entity hit produces NO
			// hurt tail (no damage_event/sound/knockback/ANGRY) and
			// no entity_event on the projectile — only the 2002
			// splash + orb award.
			sendCommand(t, pw, "clear @s")
			sendCommand(t, pw, "give @s minecraft:experience_bottle 16")
			eggDrainFor(t, c, pr, pw, 1500*time.Millisecond)
			sendUseItemAim(t, pw, 0, 4, -90, 0)
			ev = eggDrainFor(t, c, pr, pw, 2500*time.Millisecond)
			xbID := assertThrowSpawn(t, tc.tag, ev, "experience_bottle")
			assertAckCovers(t, tc.tag, ev, 4)
			if n := countKind(ev, "dmg", villagerID); n != 0 {
				t.Fatalf("%s: %d damage_events on xp-bottle entity hit — vanilla produces none", tc.tag, n)
			}
			if n := countKind(ev, "sound", hurt+1); n != 0 {
				t.Fatalf("%s: %d hurt sounds on xp-bottle entity hit — vanilla produces none", tc.tag, n)
			}
			if n := countKind(ev, "motion", villagerID); n != 0 {
				t.Fatalf("%s: %d set_entity_motion on xp-bottle entity hit — vanilla produces none", tc.tag, n)
			}
			if n := angryCount(ev); n != 0 {
				t.Fatalf("%s: %d VILLAGER_ANGRY events on xp-bottle entity hit — vanilla produces none", tc.tag, n)
			}
			assertNoEntityEvent(t, tc.tag, ev, xbID)
			assertRemoved(t, tc.tag, ev, xbID)
			assertXpSplash(t, tc.tag, ev)
			assertXpOrbs(t, tc.tag, ev)

			// A leg-1/2 egg can randomly hatch a chicken that wanders
			// into the fireball lane and eats a ball — clear any
			// hatchlings so only the villager can be hit.
			sendCommand(t, pw, "kill @e[type=minecraft:chicken]")
			eggDrainFor(t, c, pr, pw, 800*time.Millisecond)

			// Leg 5 — small_fireball entity hit, two back-to-back
			// summoned shots: SmallFireball.onHitEntity ignites the
			// victim for 5s then hurts 5.0 through
			// unattributed_fireball (no owner on a summoned ball —
			// the source entity reports as the projectile itself).
			// The second ball lands inside the grace window: hurt()
			// returns false, its fire ticks restore, and it ships no
			// damage_event — only the remove. No entity_event ever.
			sendCommand(t, pw, "summon minecraft:small_fireball 60.5 -58.4 60.5 {Motion:[0.4d,0d,0d]}")
			sendCommand(t, pw, "summon minecraft:small_fireball 60.5 -58.4 60.5 {Motion:[0.4d,0d,0d]}")
			ev = eggDrainFor(t, c, pr, pw, 2500*time.Millisecond)
			var fbIDs []int32
			for _, e := range ev {
				if e.kind == "spawn" && e.etype == mustEType(t, "small_fireball") {
					fbIDs = append(fbIDs, e.eid)
				}
			}
			if len(fbIDs) != 2 {
				t.Fatalf("%s: expected 2 small_fireball spawns, got %d", tc.tag, len(fbIDs))
			}
			fbDmg := 0
			var fbEvt *eggEvent
			for i := range ev {
				if ev[i].kind == "dmg" && ev[i].eid == villagerID && ev[i].etype == 47 {
					fbDmg++
					fbEvt = &ev[i]
				}
			}
			if fbDmg != 1 {
				t.Fatalf("%s: %d fireball damage_events for two hits — the graced second must produce none", tc.tag, fbDmg)
			}
			if (fbEvt.data != fbIDs[0]+1 && fbEvt.data != fbIDs[1]+1) || fbEvt.b0 != fbEvt.data {
				t.Fatalf("%s: fireball damage_event cause/direct = %d/%d — ownerless shots report the hit projectile %v (+1 encoded)", tc.tag, fbEvt.b0, fbEvt.data, fbIDs)
			}
			fireFlag := false
			for _, e := range ev {
				if e.kind == "meta" && e.eid == villagerID && e.hasOval && e.b0&0x01 != 0 {
					fireFlag = true
				}
			}
			if !fireFlag {
				t.Fatalf("%s: no on_fire shared-flags byte on the victim — igniteForSeconds(5) missing", tc.tag)
			}
			if countKind(ev, "sound", hurt+1) < 1 {
				t.Fatalf("%s: no entity.villager.hurt sound on the fireball hit", tc.tag)
			}
			if countKind(ev, "motion", villagerID) < 1 {
				t.Fatalf("%s: no knockback motion on the fireball hit", tc.tag)
			}
			for _, id := range fbIDs {
				assertNoEntityEvent(t, tc.tag, ev, id)
				assertRemoved(t, tc.tag, ev, id)
			}

			// Leg 6 — wind_charge entity hit, two back-to-back
			// summoned charges at a fresh villager lane (leg 5's
			// victim may be knocked, burning or dead):
			// AbstractWindCharge.onHitEntity hurts 1.0 through the
			// wind_charge damage type — the ownerless summon wires
			// cause 0 — then always bursts at the charge's position
			// (explode packet + explosion push + remove). The graced
			// second charge produces no damage_event but still bursts.
			sendCommand(t, pw, "summon minecraft:villager 63.5 -59.5 64.5 {NoAI:1b,NoGravity:1b}")
			sendCommand(t, pw, "summon minecraft:wind_charge 60.5 -58.4 64.5 {Motion:[0.4d,0d,0d]}")
			sendCommand(t, pw, "summon minecraft:wind_charge 60.5 -58.4 64.5 {Motion:[0.4d,0d,0d]}")
			ev = eggDrainFor(t, c, pr, pw, 3000*time.Millisecond)
			villager2 := int32(0)
			var wcIDs []int32
			for _, e := range ev {
				if e.kind == "spawn" && e.etype == mustEType(t, "villager") && e.eid != villagerID {
					villager2 = e.eid
				}
				if e.kind == "spawn" && e.etype == mustEType(t, "wind_charge") {
					wcIDs = append(wcIDs, e.eid)
				}
			}
			if villager2 == 0 {
				t.Fatalf("%s: second villager never spawned", tc.tag)
			}
			if len(wcIDs) != 2 {
				t.Fatalf("%s: expected 2 wind_charge spawns, got %d", tc.tag, len(wcIDs))
			}
			wcDmg := 0
			var wcEvt *eggEvent
			for i := range ev {
				if ev[i].kind == "dmg" && ev[i].eid == villager2 && ev[i].etype == 48 {
					wcDmg++
					wcEvt = &ev[i]
				}
			}
			if wcDmg != 1 {
				t.Fatalf("%s: %d wind_charge damage_events for two hits — the graced second must produce none", tc.tag, wcDmg)
			}
			if wcEvt.data != wcIDs[0]+1 {
				t.Fatalf("%s: wind_charge damage_event direct = %d — want charge id %d (+1 encoded)", tc.tag, wcEvt.data, wcIDs[0])
			}
			if wcEvt.b0 != 0 {
				t.Fatalf("%s: wind_charge damage_event cause = %d — the ownerless summon wires no cause", tc.tag, wcEvt.b0)
			}
			explodes := 0
			for _, e := range ev {
				if e.kind == "explode" {
					explodes++
				}
			}
			if explodes != 2 {
				t.Fatalf("%s: %d explode packets — both charges burst, even the graced one", tc.tag, explodes)
			}
			if countKind(ev, "motion", villager2) < 1 {
				t.Fatalf("%s: no knockback motion on the wind-charge hit villager", tc.tag)
			}
			if countKind(ev, "sound", hurt+1) < 1 {
				t.Fatalf("%s: no entity.villager.hurt sound on the wind-charge hit", tc.tag)
			}
			for _, id := range wcIDs {
				assertNoEntityEvent(t, tc.tag, ev, id)
				assertRemoved(t, tc.tag, ev, id)
			}
			if n := angryCountFor(villager2, ev); n != 0 {
				t.Fatalf("%s: %d VILLAGER_ANGRY events on the ownerless wind-charge hit", tc.tag, n)
			}

			// Leg 7 — ender_pearl entity hit: ThrownEnderpearl hurts
			// the victim twice through thrown (the grace eats the
			// second call — one wire event + ANGRY since the thrower
			// owns it), teleports the thrower to the pearl's pre-tick
			// position (sync_pos), hurts them 5.0 through the
			// entity-less ender_pearl damage type (cause 0, direct 0)
			// and plays entity.player.teleport. Its 32 addParticle
			// calls are a server-side no-op — no particle packets.
			// The pearl discards.
			sendCommand(t, pw, "tp @s 60.5 -60 64.5 -90 0")
			sendCommand(t, pw, "clear @s")
			sendCommand(t, pw, "give @s minecraft:ender_pearl 1")
			eggDrainFor(t, c, pr, pw, 1500*time.Millisecond)
			sendUseItemAim(t, pw, 0, 20, -90, 0)
			ev = eggDrainFor(t, c, pr, pw, 3000*time.Millisecond)
			var pearlID int32
			for _, e := range ev {
				if e.kind == "spawn" && e.etype == mustEType(t, "ender_pearl") {
					pearlID = e.eid
				}
			}
			if pearlID == 0 {
				t.Fatalf("%s: ender_pearl never spawned", tc.tag)
			}
			// The victim's thrown hurt — cause is the player (we
			// can't name the server-assigned id, but it must be
			// non-zero) and direct is the pearl.
			pearlDmg := 0
			for i := range ev {
				if ev[i].kind == "dmg" && ev[i].eid == villager2 && ev[i].etype == 45 {
					pearlDmg++
					if ev[i].data != pearlID+1 {
						t.Fatalf("%s: pearl damage_event direct = %d — want pearl id %d (+1)", tc.tag, ev[i].data, pearlID)
					}
					if ev[i].b0 == 0 {
						t.Fatalf("%s: pearl damage_event cause = 0 — the player thrower owns it", tc.tag)
					}
				}
			}
			if pearlDmg != 1 {
				t.Fatalf("%s: %d thrown damage_events for the pearl hit — the double hurt collapses to one", tc.tag, pearlDmg)
			}
			// The thrower's own 5.0 ender_pearl hurt: damage type id
			// 8, entity-less source → cause 0, direct 0.
			selfDmg := 0
			for _, e := range ev {
				if e.kind == "dmg" && e.etype == 8 {
					selfDmg++
					if e.b0 != 0 || e.data != 0 {
						t.Fatalf("%s: ender_pearl damage_event cause/direct = %d/%d — the source has no entities", tc.tag, e.b0, e.data)
					}
				}
			}
			if selfDmg != 1 {
				t.Fatalf("%s: %d ender_pearl damage_events — the thrower must eat 5.0 once", tc.tag, selfDmg)
			}
			// Vanilla loops 32 Level.addParticle calls in onHit, but
			// the dedicated server's addParticle is an empty no-op —
			// zero particle packets ship on either server.
			if n := countKind(ev, "particles", 0); n != 0 {
				t.Fatalf("%s: %d particle packets — the dedicated server's addParticle is a no-op", tc.tag, n)
			}
			// The thrower teleports to the pearl's pre-tick spot —
			// east of the 60.5 start, inside the villager lane.
			teleports := 0
			for _, e := range ev {
				if e.kind == "syncpos" && e.etype > 6100 && e.etype < 6500 {
					teleports++
				}
			}
			if teleports != 1 {
				t.Fatalf("%s: %d sync_pos teleports — the pearl must pull the thrower once", tc.tag, teleports)
			}
			if countKind(ev, "sound", 1323) != 1 {
				t.Fatalf("%s: no entity.player.teleport sound at the landing", tc.tag)
			}
			if n := angryCountFor(villager2, ev); n != 1 {
				t.Fatalf("%s: %d VILLAGER_ANGRY events — the player-owned pearl must anger the villager", tc.tag, n)
			}
			assertRemoved(t, tc.tag, ev, pearlID)
		})
	}
}

// findSpawn locates the first spawn_entity of the named type in the
// collected events (the summon ack always lands inside the drain).
func findSpawn(t *testing.T, tag string, ev []eggEvent, entName string) int32 {
	t.Helper()
	want, _ := server.EntityTypeID(entName)
	for _, e := range ev {
		if e.kind == "spawn" && e.etype == want {
			return e.eid
		}
	}
	t.Fatalf("%s: no spawn_entity for %s", tag, entName)
	return 0
}

func mustEType(t *testing.T, entName string) int32 {
	t.Helper()
	et, _ := server.EntityTypeID(entName)
	return et
}

func hasEvent3(t *testing.T, ev []eggEvent, eid int32) bool {
	t.Helper()
	for _, e := range ev {
		if e.kind == "event" && e.eid == eid && e.b0 == 3 {
			return true
		}
	}
	return false
}
