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
			angryCount := func(ev []eggEvent) int {
				n := 0
				for _, e := range ev {
					if e.kind == "event" && e.eid == villagerID && e.b0 == 13 {
						n++
					}
				}
				return n
			}
			assertHitWire := func(ev []eggEvent, projName string, projEid int32, seq int32) {
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
				if dmg.data != projEid+1 {
					t.Fatalf("%s: damage_event direct = %d — want projectile id %d (+1 encoded)", tc.tag, dmg.data, projEid)
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
				assertRemoved(t, tc.tag, ev, projEid)
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
			assertHitWire(ev, "egg", eggIDs[0], 1)
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
			assertHitWire(ev, "snowball", sbID, 3)
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
			if fbEvt.data != fbIDs[0]+1 {
				t.Fatalf("%s: fireball damage_event direct = %d — want projectile id %d (+1 encoded)", tc.tag, fbEvt.data, fbIDs[0])
			}
			if fbEvt.b0 != fbIDs[0]+1 {
				t.Fatalf("%s: fireball damage_event cause = %d — ownerless shots report the projectile %d", tc.tag, fbEvt.b0, fbIDs[0])
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
