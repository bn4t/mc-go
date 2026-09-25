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
			if wcEvt.data != wcIDs[0]+1 && wcEvt.data != wcIDs[1]+1 {
				t.Fatalf("%s: wind_charge damage_event direct = %d — want a charge id %v (+1 encoded)", tc.tag, wcEvt.data, wcIDs)
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

			// Leg 8 — breeze_wind_charge entity hit: same
			// AbstractWindCharge.onHitEntity (hurt 1.0 wind_charge
			// type + unconditional burst) but the breeze profile
			// explodes radius 3.0 — the decoded radius field is the
			// discriminator.
			sendCommand(t, pw, "summon minecraft:villager 63.5 -59.5 68.5 {NoAI:1b,NoGravity:1b}")
			sendCommand(t, pw, "summon minecraft:breeze_wind_charge 60.5 -58.4 68.5 {Motion:[0.4d,0d,0d]}")
			sendCommand(t, pw, "summon minecraft:breeze_wind_charge 60.5 -58.4 68.5 {Motion:[0.4d,0d,0d]}")
			ev = eggDrainFor(t, c, pr, pw, 3000*time.Millisecond)
			villager3 := int32(0)
			var bwcIDs []int32
			for _, e := range ev {
				if e.kind == "spawn" && e.etype == mustEType(t, "villager") && e.eid != villagerID && e.eid != villager2 {
					villager3 = e.eid
				}
				if e.kind == "spawn" && e.etype == mustEType(t, "breeze_wind_charge") {
					bwcIDs = append(bwcIDs, e.eid)
				}
			}
			if villager3 == 0 {
				t.Fatalf("%s: third villager never spawned", tc.tag)
			}
			if len(bwcIDs) != 2 {
				t.Fatalf("%s: expected 2 breeze_wind_charge spawns, got %d", tc.tag, len(bwcIDs))
			}
			bwcDmg := 0
			for i := range ev {
				if ev[i].kind == "dmg" && ev[i].eid == villager3 && ev[i].etype == 48 {
					bwcDmg++
					if ev[i].b0 != 0 || (ev[i].data != bwcIDs[0]+1 && ev[i].data != bwcIDs[1]+1) {
						t.Fatalf("%s: breeze damage_event cause/direct = %d/%d — ownerless wires cause 0, direct a charge %v", tc.tag, ev[i].b0, ev[i].data, bwcIDs)
					}
				}
			}
			if bwcDmg != 1 {
				t.Fatalf("%s: %d wind_charge damage_events for two breeze hits — the graced second must produce none", tc.tag, bwcDmg)
			}
			bwcBoom := 0
			for _, e := range ev {
				if e.kind == "explode" && e.etype == 300 {
					bwcBoom++
				}
			}
			if bwcBoom != 2 {
				for _, e := range ev {
					if e.kind == "explode" || e.kind == "remove" || e.kind == "dmg" {
						t.Logf("%s: dbg %+v", tc.tag, e)
					}
				}
				t.Fatalf("%s: %d radius-3.0 explode packets — both breeze charges burst", tc.tag, bwcBoom)
			}
			if countKind(ev, "motion", villager3) < 1 {
				t.Fatalf("%s: no knockback motion on the breeze-charge hit villager", tc.tag)
			}
			for _, id := range bwcIDs {
				assertNoEntityEvent(t, tc.tag, ev, id)
				assertRemoved(t, tc.tag, ev, id)
			}

			// leg 9: two ownerless wither_skulls into the SAME
			// villager4 — WitherSkull.onHitEntity's ownerless arm
			// hurts 5.0 through the entity-less magic type (the
			// living-owner arm would be 8.0 wither_skull), applies a
			// NORMAL-difficulty 10s wither effect when the hit lands,
			// and onHit runs an unconditional power-1.0 MOB
			// explosion at the skull's position — both skulls
			// explode, and the wither tick fires its 1.0 hurt every
			// 20 ticks of remaining duration.
			sendCommand(t, pw, "summon minecraft:villager 59.5 -60 68.5 {NoAI:1b,NoGravity:1b}")
			sendCommand(t, pw, "summon minecraft:wither_skull 58.0 -59.7 68.5 {Motion:[1.0d,0.0d,0.0d]}")
			sendCommand(t, pw, "summon minecraft:wither_skull 58.0 -59.7 68.5 {Motion:[1.0d,0.0d,0.0d]}")
			ev = eggDrainFor(t, c, pr, pw, 3000*time.Millisecond)
			var villager4 int32
			var wsIDs []int32
			for _, e := range ev {
				if e.kind == "spawn" && e.etype == mustEType(t, "villager") &&
					e.eid != villagerID && e.eid != villager2 && e.eid != villager3 {
					villager4 = e.eid
				}
				if e.kind == "spawn" && e.etype == mustEType(t, "wither_skull") {
					wsIDs = append(wsIDs, e.eid)
				}
			}
			if villager4 == 0 {
				t.Fatalf("%s: fourth villager never spawned", tc.tag)
			}
			if len(wsIDs) != 2 {
				t.Fatalf("%s: expected 2 wither_skull spawns, got %d", tc.tag, len(wsIDs))
			}
			wsDmg := 0
			for i := range ev {
				if ev[i].kind == "dmg" && ev[i].eid == villager4 && ev[i].etype == 27 {
					wsDmg++
					if ev[i].b0 != 0 || ev[i].data != 0 {
						t.Fatalf("%s: ownerless magic damage_event cause/direct = %d/%d — entity-less magic wires 0/0", tc.tag, ev[i].b0, ev[i].data)
					}
				}
			}
			if wsDmg != 1 {
				for _, e := range ev {
					if e.kind == "dmg" && e.eid == villager4 {
						t.Logf("%s: dbg dmg %+v", tc.tag, e)
					}
				}
				t.Fatalf("%s: %d magic damage_events for two skull hits — the graced second must produce none", tc.tag, wsDmg)
			}
			wsBoom := 0
			for _, e := range ev {
				if e.kind == "explode" && e.etype == 100 {
					wsBoom++
				}
			}
			if wsBoom != 2 {
				t.Fatalf("%s: %d radius-1.0 explode packets — both skulls explode on hit", tc.tag, wsBoom)
			}
			witherTicks := 0
			for _, e := range ev {
				if e.kind == "dmg" && e.eid == villager4 && e.etype == 49 {
					witherTicks++
					if e.b0 != 0 || e.data != 0 {
						t.Fatalf("%s: wither tick damage_event cause/direct = %d/%d — entity-less wither wires 0/0", tc.tag, e.b0, e.data)
					}
				}
			}
			if witherTicks < 1 {
				t.Fatalf("%s: no wither-type damage_events — the 10s wither effect must tick 1.0/20t", tc.tag)
			}
			for _, id := range wsIDs {
				assertNoEntityEvent(t, tc.tag, ev, id)
				assertRemoved(t, tc.tag, ev, id)
			}

			// leg 10: two ownerless fireballs into the SAME villager5
			// — LargeFireball.onHitEntity hurts 6.0 through
			// fireball(ball, owner) unconditionally (ownerless wires
			// cause 0, direct the ball), and onHit explodes with
			// explosionPower=1 under ExplosionInteraction.MOB on ANY
			// hit — both balls burst and remove.
			sendCommand(t, pw, "summon minecraft:villager 63.5 -60 72.5 {NoAI:1b,NoGravity:1b}")
			sendCommand(t, pw, "summon minecraft:fireball 62.0 -59.3 72.5 {Motion:[1.0d,0.0d,0.0d]}")
			sendCommand(t, pw, "summon minecraft:fireball 60.0 -59.3 72.5 {Motion:[1.0d,0.0d,0.0d]}")
			ev = eggDrainFor(t, c, pr, pw, 3000*time.Millisecond)
			var villager5 int32
			var lfbIDs []int32
			for _, e := range ev {
				if e.kind == "spawn" && e.etype == mustEType(t, "villager") &&
					e.eid != villagerID && e.eid != villager2 && e.eid != villager3 && e.eid != villager4 {
					villager5 = e.eid
				}
				if e.kind == "spawn" && e.etype == mustEType(t, "fireball") {
					lfbIDs = append(lfbIDs, e.eid)
				}
			}
			if villager5 == 0 {
				t.Fatalf("%s: fifth villager never spawned", tc.tag)
			}
			if len(lfbIDs) != 2 {
				t.Fatalf("%s: expected 2 fireball spawns, got %d", tc.tag, len(lfbIDs))
			}
			lfbDmg := 0
			var lfbEvt *eggEvent
			for i := range ev {
				if ev[i].kind == "dmg" && ev[i].eid == villager5 && ev[i].etype == 47 {
					lfbDmg++
					lfbEvt = &ev[i]
				}
			}
			if lfbDmg != 1 {
				for _, e := range ev {
					if e.kind == "dmg" && e.eid == villager5 {
						t.Logf("%s: dbg dmg %+v", tc.tag, e)
					}
				}
				t.Fatalf("%s: %d unattributed_fireball damage_events for two hits — the graced second must produce none", tc.tag, lfbDmg)
			}
			if (lfbEvt.b0 != lfbIDs[0]+1 && lfbEvt.b0 != lfbIDs[1]+1) || lfbEvt.b0 != lfbEvt.data {
				t.Fatalf("%s: unattributed_fireball damage_event cause/direct = %d/%d — both wire the ball id %v (+1)", tc.tag, lfbEvt.b0, lfbEvt.data, lfbIDs)
			}
			lfbBoom := 0
			for _, e := range ev {
				if e.kind == "explode" && e.etype == 100 {
					lfbBoom++
				}
			}
			if lfbBoom != 2 {
				t.Fatalf("%s: %d radius-1.0 explode packets — both fireballs burst on hit", tc.tag, lfbBoom)
			}
			if countKind(ev, "motion", villager5) < 1 {
				t.Fatalf("%s: no knockback motion on the fireball-hit villager", tc.tag)
			}
			for _, id := range lfbIDs {
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
