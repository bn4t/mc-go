//go:build blackbox

// Jukebox use pair + JukeboxSongPlayer wire (insert/eject/song events)
// and the allay amethyst-shard duplication arm, measured on the vanilla
// 26.2 jar:
//
//   - useItemOn, has_record=true      → popOutTheItem: item entity at
//     pos+(0.5,1.01,0.5)±0.7xz, pickup delay 10; has_record=false flag-2
//     write; onSongChanged → songPlayer.stop (self-guarded: only when a
//     song is still set — after a natural finish the second stop
//     early-returns) = STOP_PLAY game event + level_event 1011 data 0.
//   - useItemOn, has_record=false + JUKEBOX_PLAYABLE → setTheItem +
//     consume(1, player) (hasInfiniteMaterials gates creative),
//     has_record=true flag-2 write, block_change game event;
//     songPlayer.play = level_event 1010 data = jukebox_song registry id
//     (cat=5 in 26.2's 22-entry table); non-disc → PASS (item places).
//   - useWithoutItem, has_record=true → popOutTheItem (same as above).
//   - songPlayer.tick: hasFinished → stop(); else every 20 ticks
//     (ticksSinceSongStarted % 20 == 0) JUKEBOX_PLAY + NOTE particle at
//     bottom-center +1.2, count 0, xd=nextInt(4)/24, speed 1.
//   - JUKEBOX_PLAY/STOP_PLAY are register(name,10) game events with NO
//     sculk frequency (not in the vibrations/warden_can_listen tags) —
//     only the allay's JukeboxListener (DynamicGameEventListener,
//     radius 10) reacts: PLAY → setJukeboxPlaying → DATA_DANCING(idx16)
//     only when not already dancing; STOP → clears jukeboxPos only when
//     it matches (or is unset); shouldStopDancing %20: dist >10 or block
//     not jukebox → clear. The disc need not still be playing.
//   - Allay.aiStep: tickCount%10==0 && alive → heal(1).
//   - mobInteract first arm: isDancing && held #duplicates_allays
//     (=amethyst_shard) && DATA_CAN_DUPLICATE → duplicateAllay (new allay
//     snapTo pos/yaw/pitch, persistence required, 6000-tick cooldown on
//     BOTH) + entity_event 18 + amethyst chime sound (except interactor)
//   - stack.consume(1, player) → SUCCESS.
//   - NBT: DuplicationCooldown putLong always; getIntOr(0) on load.
package blackbox

import (
	"testing"
	"time"
)

// TestJukeboxParity pins the insert / eject / song wire on both legs.
func TestJukeboxParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB"),
	}, nil)
	defer targets.cleanup()

	run := func(t *testing.T, addr string) {
		v := joinVehPair(t, addr)
		pr, pw := v.pra, v.pwa
		// BOT_A within ~1.5 of the allay and the jukebox (interact range
		// is ~3.0 blocks).
		sendCommand(t, pw, "tp @s 8.5 -59 9 0 0")
		sendCommand(t, pw, "setblock 8 -59 8 jukebox")
		// Allay inside the 10-block listener radius; second one outside.
		a1 := summonTrackedNBT(t, v.ca, pr, pw, pw, "allay", 8.5, -58, 10.5, "{}")
		if a1 == 0 {
			t.Fatalf("allay not tracked")
		}
		a2 := summonTrackedNBT(t, v.ca, pr, pw, pw, "allay", 20.5, -58, 10.5, "{}")
		if a2 == 0 {
			t.Fatalf("allay2 not tracked")
		}
		// NoAI allay inside the radius: setDancing's isEffectiveAi gate
		// keeps DATA_DANCING false even though jukeboxPos is stored.
		a3 := summonTrackedNBT(t, v.ca, pr, pw, pw, "allay", 8.5, -58, 11.5, "{NoAI:1b}")
		if a3 == 0 {
			t.Fatalf("allay3 not tracked")
		}
		vehWatch(t, v, false, a1, 200*time.Millisecond)

		// Insert a disc: has_record=true write + level_event 1010 id 5
		// (cat) + block_change; the in-radius allay starts dancing.
		sendCommand(t, pw, "item replace entity BOATA weapon.mainhand with music_disc_cat")
		sendUseItemOn(t, pw, 8, -59, 8, 1, 0)
		o := vehWatch(t, v, false, a1, 600*time.Millisecond)
		// Vanilla emits the flag-2 sendBlockUpdated immediately AND the
		// dirty-section broadcast at tick end — count ≥1.
		if o.blockUps[[3]int32{8, -59, 8}] < 1 {
			t.Fatalf("insert: no block_update at jukebox")
		}
		if !hasLevelEv(o, 1010, 5) {
			t.Fatalf("insert: no level_event 1010/5: %v", o.levelEv)
		}
		// DATA_DANCING emits ~1 tick after the first JUKEBOX_PLAY —
		// possibly inside this window already; accumulate it either way.
		dancing := o.boolMeta[16]
		o = vehWatch(t, v, false, a1, 900*time.Millisecond)
		if !dancing && !o.boolMeta[16] {
			t.Fatalf("insert: DATA_DANCING not set; boolMeta=%v", o.boolMeta)
		}
		// The out-of-radius allay must stay still; the in-radius NoAI
		// allay must not flip DATA_DANCING either.
		oA2 := vehWatch(t, v, false, a2, 400*time.Millisecond)
		if oA2.boolMeta[16] {
			t.Fatalf("out-of-radius allay danced")
		}
		oA3 := vehWatch(t, v, false, a3, 400*time.Millisecond)
		if oA3.boolMeta[16] {
			t.Fatalf("NoAI allay danced")
		}

		// Eject with empty hand (useWithoutItem → popOutTheItem): item
		// entity spawn + has_record=false write + STOP_PLAY → dance end.
		sendCommand(t, pw, "item replace entity BOATA weapon.mainhand with air")
		sendUseItemOn(t, pw, 8, -59, 8, 1, 0)
		o = vehWatch(t, v, false, a1, 700*time.Millisecond)
		if len(o.itemEIDs) != 1 {
			t.Fatalf("eject: item spawns = %d, want 1", len(o.itemEIDs))
		}
		if o.blockUps[[3]int32{8, -59, 8}] < 1 {
			t.Fatalf("eject: no block_update at jukebox")
		}
		if !hasLevelEv(o, 1011, 0) {
			t.Fatalf("eject: no level_event 1011/0: %v", o.levelEv)
		}
		dancing = false
		o = vehWatch(t, v, false, a1, 900*time.Millisecond)
		if o.boolMeta[16] {
			t.Fatalf("eject: allay still dancing after STOP")
		}
		sendCommand(t, pw, "kill @e[type=minecraft:allay]")
		sendCommand(t, pw, "kill @e[type=minecraft:item]")
		vehWatch(t, v, false, a1, 400*time.Millisecond)
	}

	t.Run("vanilla", func(t *testing.T) { run(t, targets.vanilla) })
	t.Run("mc-go", func(t *testing.T) { run(t, targets.mcgo) })
}

// TestAllayDuplicateParity pins the shard-duplication interact arm.
func TestAllayDuplicateParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB"),
	}, nil)
	defer targets.cleanup()

	run := func(t *testing.T, addr string) {
		v := joinVehPair(t, addr)
		pr, pw := v.pra, v.pwa
		sendCommand(t, pw, "tp @s 8.5 -59 9 0 0")
		sendCommand(t, pw, "setblock 8 -59 8 jukebox")
		a := summonTrackedNBT(t, v.ca, pr, pw, pw, "allay", 8.5, -58, 10.5, "{Tags:[\"dupsrc\"]}")
		if a == 0 {
			t.Fatalf("allay not tracked")
		}
		sendCommand(t, pw, "item replace entity BOATA weapon.mainhand with amethyst_shard")
		vehWatch(t, v, false, a, 200*time.Millisecond)

		// Not dancing yet: click must NOT duplicate.
		sendInteractEntity(pw, a)
		o := vehWatch(t, v, false, a, 500*time.Millisecond)
		if o.adds["allay"] != 0 || len(o.entEv) != 0 {
			t.Fatalf("undancing click: adds=%d entEv=%v", o.adds["allay"], o.entEv)
		}

		// Start the song → allay dances (emits ~1 tick after PLAY).
		sendCommand(t, pw, "item replace entity BOATA weapon.mainhand with music_disc_cat")
		sendUseItemOn(t, pw, 8, -59, 8, 1, 0)
		o = vehWatch(t, v, false, a, 2500*time.Millisecond)
		if !o.boolMeta[16] {
			t.Fatalf("pre-dup: allay not dancing; boolMeta=%v", o.boolMeta)
		}

		// Shard click → duplicate: +1 allay add_entity, entity_event 18
		// on the source, idx17=false (6000-tick cooldown, both allays).
		sendCommand(t, pw, "item replace entity BOATA weapon.mainhand with amethyst_shard")
		sendInteractEntity(pw, a)
		o = vehWatch(t, v, false, a, 800*time.Millisecond)
		if o.adds["allay"] != 1 {
			t.Fatalf("dup: allay adds = %d, want 1", o.adds["allay"])
		}
		if len(o.entEv) != 1 || o.entEv[0] != 18 {
			t.Fatalf("dup: entEv = %v, want [18]", o.entEv)
		}
		if o.boolMeta[17] {
			t.Fatalf("dup: idx17 (can_dup) still true on source")
		}
		// The source's cooldown is ~6000 (measured one tick later).
		cd, ok := dataGetNum(t, v, "data get entity @e[tag=dupsrc,limit=1] DuplicationCooldown")
		if !ok || cd < 5900 || cd > 6000 {
			t.Fatalf("DuplicationCooldown = %v ok=%v, want ~6000", cd, ok)
		}
		// Second click while cooling: no further duplication.
		sendInteractEntity(pw, a)
		o = vehWatch(t, v, false, a, 500*time.Millisecond)
		if o.adds["allay"] != 0 || len(o.entEv) != 0 {
			t.Fatalf("cooling click: adds=%d entEv=%v", o.adds["allay"], o.entEv)
		}
		// A non-shard while still dancing: nothing.
		sendCommand(t, pw, "item replace entity BOATA weapon.mainhand with stick")
		sendInteractEntity(pw, a)
		o = vehWatch(t, v, false, a, 400*time.Millisecond)
		if o.adds["allay"] != 0 || len(o.entEv) != 0 {
			t.Fatalf("stick click: adds=%d entEv=%v", o.adds["allay"], o.entEv)
		}
		sendCommand(t, pw, "kill @e[type=minecraft:allay]")
		vehWatch(t, v, false, a, 400*time.Millisecond)
	}

	t.Run("vanilla", func(t *testing.T) { run(t, targets.vanilla) })
	t.Run("mc-go", func(t *testing.T) { run(t, targets.mcgo) })
}

func hasLevelEv(o vehObs, event, data int32) bool {
	for _, le := range o.levelEv {
		if le[0] == event && le[1] == data {
			return true
		}
	}
	return false
}
