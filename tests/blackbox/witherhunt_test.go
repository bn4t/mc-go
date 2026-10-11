//go:build blackbox

// Blackbox parity pin for WitherSkeleton's goal and doHurtTarget set,
// measured on the 26.2 decompile: registerGoals runs
// AbstractSkeleton's set plus NearestAttackableTargetGoal
// (AbstractPiglin, mustSee) at priority 3; finalizeSpawn re-bases
// ATTACK_DAMAGE to 4.0; the skeleton family's MeleeAttackGoal chases
// at 1.2×movement_speed (0.3) and swings on the 20-tick cooldown;
// doHurtTarget lands WITHER 200 on every LivingEntity hit with no
// difficulty gate; getArrow ignites the spawned arrow
// (igniteForSeconds(100)) and doPostHurtEffects sets the victim
// alight for 5s; canBeAffected vetoes WITHER itself.
// Sources: WitherSkeleton.java, AbstractSkeleton.java,
// MeleeAttackGoal.java, NearestAttackableTargetGoal.java.
package blackbox

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// wsObs is one leg's wire facts for the wither-skeleton window.
type wsObs struct {
	flagSet   int     // idx15 emits carrying bit2 (aggressive)
	hits      int     // BOATA health decreases while polling
	hpEnd     float64 // BOATA health at window end
	plEff     bool    // BOATA active_effects query answered post-hit
	pigHp     float64 // piglin's last answered Health (16 at spawn)
	pigGone   bool    // piglin selector stopped answering (dead)
	pigEff    bool    // piglin active_effects query answered post-hit
	immuneOK  bool    // /effect give wither left active_effects absent
	arrowFire float64 // Fire NBT on the wither skeleton's arrow
}

func TestWitherSkeletonParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB", "BOATC"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	run := func(t *testing.T, name, addr string) wsObs {
		o := wsObs{pigHp: 16}
		v := joinVehPair(t, addr)
		defer v.ca.Close()
		defer v.cb.Close()
		cc, prc, pwc := joinPlayBlackbox(t, addr, "BOATC")
		defer cc.Close()
		drainChunkWindow(t, name, v.ca, v.pra, v.pwa, 0)
		sink := newChatSinkTP(prc, pwc)

		sendCommand(t, v.pwa, "gamemode spectator BOATB")
		sendCommand(t, v.pwa, "gamemode spectator BOATC")
		sendCommand(t, v.pwa, "gamerule do_mob_spawning false")
		sendCommand(t, v.pwa, "kill @e[type=!player]")
		sendCommand(t, v.pwa, "gamemode survival BOATA")
		sendCommand(t, v.pwa, "tp BOATA 13.5 -60 8.5")
		sendCommand(t, v.pwa, "tick rate 40")
		sendCommand(t, v.pwa, "effect clear BOATA")

		// The piglin is summoned up front — it sits NoAI at 4.5 for
		// the whole run (the arm-A skeleton prefers the @2 player
		// mark over the @3 piglin hunt, so its presence changes
		// nothing until the player leaves range).
		sendCommand(t, pwc,
			"summon minecraft:piglin 4.5 -60 8.5 {NoAI:1b,IsImmuneToZombification:1b}")

		// Arm A — the player melee run: a bare /summon 5 blocks out
		// (inside follow_range 16, outside the ~1.48 reach) chases and
		// swings; each landed hit deals 4 and applies WITHER 200.
		wsid := summonNoNBT(t, v.ca, v.pra, v.pwa, v.pwa,
			"minecraft:wither_skeleton", 8.5, -60, 8.5)
		if wsid == 0 {
			t.Fatalf("%s: no wither_skeleton spawn packet", name)
		}
		fold := func(wo vehObs) {
			for _, f := range wo.flagsHist {
				if !strings.HasPrefix(f, strconv.Itoa(int(wsid))+":") {
					continue
				}
				bv, _ := strconv.Atoi(strings.SplitN(f, ":", 2)[1])
				if bv&4 != 0 {
					o.flagSet++
				}
			}
		}
		_, hp := dataGetSel(t, sink, pwc, "BOATA", "Health")
		deadline := time.Now().Add(14 * time.Second)
		for time.Now().Before(deadline) && hp > 14 {
			fold(vehWatch(t, v, false, 0, 350*time.Millisecond))
			_, h := dataGetSel(t, sink, pwc, "BOATA", "Health")
			if h > 0 && h < hp {
				o.hits++
				hp = h
			}
			if h == 0 {
				// Dead players stop seeing spawn packets — the rest
				// of the run depends on a live connection.
				t.Fatalf("%s: BOATA died during the melee window", name)
			}
		}
		o.hpEnd = hp
		// Damage landed (hp <= 14 = a 4-damage swing plus the wither
		// ticks that only start after one) — pull BOATA out of
		// follow_range while it's still alive, then verify the
		// WITHER instance the hit applied.
		sendCommand(t, v.pwa, "tp BOATA 40.5 -60 8.5")
		st, _ := dataGetSel(t, sink, pwc, "BOATA", "active_effects")
		o.plEff = st == "query"

		// Arm B — the AbstractPiglin hunt: the player is already
		// outside follow_range so the @3 target selector owns the
		// mark; the piglin holds still (NoAI) and never converts.
		sendCommand(t, v.pwa, "effect clear BOATA")
		sendCommand(t, v.pwa, "effect give BOATA minecraft:instant_health 1 60 true")
		sendCommand(t, v.pwa, "kill @e[type=minecraft:wither_skeleton]")
		stb, hpb := dataGetSel(t, sink, pwc, "BOATA", "Health")
		t.Logf("%s: BOATA health before hunt st=%s h=%.1f", name, stb, hpb)
		wsid = summonNoNBT(t, v.ca, v.pra, v.pwa, v.pwa,
			"minecraft:wither_skeleton", 8.5, -60, 8.5)
		if wsid == 0 {
			t.Fatalf("%s: no wither_skeleton spawn packet (hunt)", name)
		}
		pigSel := "@e[type=minecraft:piglin,limit=1,sort=nearest]"
		deadline = time.Now().Add(16 * time.Second)
		for time.Now().Before(deadline) {
			fold(vehWatch(t, v, false, 0, 400*time.Millisecond))
			st, h := dataGetSel(t, sink, pwc, pigSel, "Health")
			if st == "query" {
				o.pigHp = h
			} else {
				o.pigGone = true
				break
			}
			st, _ = dataGetSel(t, sink, pwc, pigSel, "active_effects")
			if st == "query" {
				o.pigEff = true
			}
			if o.pigHp < 16 && o.pigEff {
				break
			}
		}

		// Arm C — canBeAffected: WITHER refused on the skeleton itself.
		wsSel := "@e[type=minecraft:wither_skeleton,limit=1,sort=nearest]"
		sendCommand(t, v.pwa, fmt.Sprintf("effect give %s minecraft:wither 5", wsSel))
		st, _ = dataGetSel(t, sink, pwc, wsSel, "active_effects")
		o.immuneOK = st != "query"

		// Arm D — getArrow's ignite: swap in a bow and watch the next
		// shot's Fire NBT. BOATA returns inside bow range (and out of
		// sight competition once the piglin is gone).
		sendCommand(t, v.pwa, fmt.Sprintf(
			"item replace entity %s weapon.mainhand with minecraft:bow", wsSel))
		sendCommand(t, v.pwa, "effect give BOATA minecraft:instant_health 1 10 true")
		sendCommand(t, v.pwa, "tp BOATA 0.5 -60 8.5")
		deadline = time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			vehWatch(t, v, false, 0, 300*time.Millisecond)
			st, fv := dataGetSel(t, sink, pwc,
				"@e[type=minecraft:arrow,limit=1,sort=nearest]", "Fire")
			if st == "query" && fv > 0 {
				o.arrowFire = fv
				break
			}
		}
		return o
	}

	check := func(t *testing.T, name string, o wsObs) {
		if o.hits < 1 {
			t.Fatalf("%s: no melee hit landed on the player", name)
		}
		if o.hpEnd > 16 {
			t.Fatalf("%s: BOATA hp %.1f — one swing at attack_damage 4 wants <=16 (extra hits/wither ticks only lower it)",
				name, o.hpEnd)
		}
		if !o.plEff {
			t.Fatalf("%s: player never carried an effect after landed hits (WITHER 200 missing)", name)
		}
		if o.flagSet < 1 {
			t.Fatalf("%s: wither_skeleton never emitted the aggressive flag (idx15 bit2)", name)
		}
		if o.pigHp >= 16 && !o.pigGone {
			t.Fatalf("%s: piglin never took melee damage (hp %.1f, still alive)", name, o.pigHp)
		}
		if !o.pigEff && !o.pigGone {
			t.Fatalf("%s: piglin never carried WITHER after the hunt landed", name)
		}
		if !o.immuneOK {
			t.Fatalf("%s: /effect give wither landed on the wither_skeleton — canBeAffected veto missing", name)
		}
		if o.arrowFire <= 0 {
			t.Fatalf("%s: wither skeleton's arrow never carried Fire ticks (getArrow ignite missing)", name)
		}
		t.Logf("%s: hits=%d hpEnd=%.1f flag=%d pigHp=%.1f pigGone=%v pigEff=%v immune=%v arrowFire=%.0f",
			name, o.hits, o.hpEnd, o.flagSet, o.pigHp, o.pigGone, o.pigEff, o.immuneOK, o.arrowFire)
	}

	vo := run(t, "vanilla", targets.vanilla)
	mo := run(t, "mc-go", targets.mcgo)
	check(t, "vanilla", vo)
	check(t, "mc-go", mo)
}

// summonNoNBT is summonTrackedNBT with no NBT argument — a bare
// /summon runs Mob.finalizeSpawn (wither_skeleton's attack_damage
// rebase, follow_range bonus) where an explicit {} tag skips it.
func summonNoNBT(t *testing.T, c net.Conn, pr *protocol.Reader, pw, pwc *protocol.Writer, mob string, x, y, z float64) int32 {
	t.Helper()
	typeID, ok := server.EntityTypeID(strings.TrimPrefix(mob, "minecraft:"))
	if !ok {
		t.Fatalf("unknown entity type %s", mob)
	}
	for tries := 0; tries < 8; tries++ {
		sendCommand(t, pwc, fmt.Sprintf("summon %s %v %v %v", mob, x, y, z))
		deadline := time.Now().Add(2500 * time.Millisecond)
		found := int32(-1)
		for time.Now().Before(deadline) && found < 0 {
			_ = c.SetReadDeadline(time.Now().Add(time.Until(deadline)))
			p, err := pr.ReadPacket()
			if err != nil {
				break
			}
			switch p.ID {
			case server.IDPlayKeepAliveCB:
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
			case server.IDPlayChunkBatchDone:
				_ = ackChunkBatch(pw)
			case server.IDPlaySyncPos:
				r := protocol.NewPacketReader(p.Data)
				if id, err := r.VarInt(); err == nil {
					var b protocol.Buffer
					b.VarInt(id)
					_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
				}
			case server.IDPlaySpawnEntity:
				r := protocol.NewPacketReader(p.Data)
				eid, e1 := r.VarInt()
				_, e2 := r.Read(16)
				typ, e3 := r.VarInt()
				if e1 == nil && e2 == nil && e3 == nil && typ == typeID {
					found = eid
				}
			}
		}
		if found >= 0 {
			_ = c.SetReadDeadline(time.Time{})
			return found
		}
	}
	return 0
}
