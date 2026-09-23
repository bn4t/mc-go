package blackbox

// TestAmbientVoiceParity pins vanilla 26.2's ambient-voice cadence
// (Mob.baseTick: ambientSoundTime climbs per tick, fires when
// random.nextInt(1000) < count, resets to -getAmbientSoundInterval)
// and the per-type sound/volume table — zombies voice
// entity.zombie.ambient at vol 1 every ~120 ticks mean, pigs
// entity.pig.ambient at vol 1 on the Animal interval. The baby leg
// pins getVoicePitch: babies roll (r-r)*0.2+1.5 (vs adult +1.0), so a
// baby pig's ambient pitch lands inside [1.3, 1.7].
import (
	"fmt"
	"math"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestAmbientVoiceParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	zid, _ := server.SoundEventID("entity.zombie.ambient")
	pid, _ := server.SoundEventID("entity.pig.ambient")

	run := func(addr string) []string {
		var out []string
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(300 * time.Second))
		sendCommand(t, pw, "gamemode survival P")
		sendCommand(t, pw, "gamerule minecraft:do_mob_spawning false")
		// Zombies burn in daylight on vanilla — the hurts drown the
		// ambient count and each hurt resets ambientSoundTime. Night
		// gives the clean voice channel on both servers.
		sendCommand(t, pw, "time set 13000")

		snds := map[int32][]float32{} // sound id -> pitches seen
		pump := func(d time.Duration) {
			deadline := time.Now().Add(d)
			for time.Now().Before(deadline) {
				_ = c.SetReadDeadline(time.Now().Add(120 * time.Millisecond))
				p, err := pr.ReadPacket()
				if err != nil {
					continue
				}
				if p.ID == server.IDPlayKeepAliveCB {
					_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
					continue
				}
				if p.ID != 0x75 { // sound
					continue
				}
				r := protocol.NewPacketReader(p.Data)
				sid, err := r.VarInt()
				if err != nil {
					continue
				}
				if _, err := r.VarInt(); err != nil { // source
					continue
				}
				if _, err := r.Int(); err != nil {
					continue
				}
				if _, err := r.Int(); err != nil {
					continue
				}
				if _, err := r.Int(); err != nil {
					continue
				}
				vol, err := r.Float()
				if err != nil {
					continue
				}
				pitch, err := r.Float()
				if err != nil {
					continue
				}
				_ = vol
				snds[sid-1] = append(snds[sid-1], pitch)
			}
		}
		pump(600 * time.Millisecond)

		// --- zombie: four summoned, ~20s window — independent
		// cadences make "at least one ambient" near-certain.
		for i := 0; i < 4; i++ {
			sendCommand(t, pw, fmt.Sprintf("summon minecraft:zombie ~%d ~ ~ {NoAI:1b}", 3+i))
		}
		pump(20 * time.Second)
		t.Logf("%s zombie ambient n=%d pitches=%v", addr, len(snds[zid]), snds[zid])
		out = append(out, fmt.Sprintf("zombie heard=%v pitchOk=%v",
			len(snds[zid]) > 0,
			len(snds[zid]) > 0 && math.Abs(float64(snds[zid][0])-1.0) <= 0.2))

		// --- pig + baby pigs: pitch centers differ by 0.5. Three
		// babies keep a quiet window from flaking the heard flag.
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		sendCommand(t, pw, "summon minecraft:pig ~2 ~ ~ {NoAI:1b}")
		for i := 0; i < 3; i++ {
			// AgeableMob babies come from Age, not IsBaby — IsBaby on a
			// pig is dead NBT vanilla ignores. -24000 is vanilla's
			// natural-baby age; -1 would grow up next tick.
			sendCommand(t, pw, fmt.Sprintf("summon minecraft:pig ~%d ~ ~ {NoAI:1b,Age:-24000}", -2-i))
		}
		snds = map[int32][]float32{}
		pump(20 * time.Second)
		adultLo, babyLo := 10.0, 10.0
		for _, p := range snds[pid] {
			if float64(p) < adultLo {
				adultLo = float64(p)
			}
		}
		bid, _ := server.SoundEventID("entity.baby_pig.ambient")
		// 26.2 pigs voice via the sound variant — a baby's ambient is
		// entity.baby_pig.ambient on a different id.
		for _, p := range snds[bid] {
			if float64(p) < babyLo {
				babyLo = float64(p)
			}
		}
		out = append(out, fmt.Sprintf("pig heard=%v babyHeard=%v adultPitch<1.2=%v babyPitch>1.3=%v",
			len(snds[pid]) > 0, len(snds[bid]) > 0,
			adultLo < 1.2, len(snds[bid]) > 0 && babyLo > 1.3))
		return out
	}

	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	for i := range van {
		t.Logf("leg %d vanilla: %s", i, van[i])
		t.Logf("leg %d mc-go:   %s", i, mc[i])
	}
	if len(van) != len(mc) {
		t.Fatalf("leg count %d vs %d", len(van), len(mc))
	}
	for i := range van {
		if van[i] != mc[i] {
			t.Errorf("leg %d mismatch: vanilla %q mc-go %q", i, van[i], mc[i])
		}
	}
}

// TestUndeadSunBurnParity pins Mob.burnUndead: a #burn_in_daylight
// mob under open daylight sky ignites (igniteForSeconds 8 → the
// shared-flags on-fire bit plus on_fire hurts every twenty ticks),
// a helmeted one burns through the helmet instead of igniting, and
// nothing happens at night. The legs pin qualitative parity — the
// sun roll is random, so counts stay logs, not comparisons.
func TestUndeadSunBurnParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	zid, _ := server.SoundEventID("entity.zombie.hurt")

	type burnObs struct {
		fireSeen bool
		hurtN    int
	}
	run := func(addr string) []string {
		var out []string
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(300 * time.Second))
		sendCommand(t, pw, "gamemode survival P")
		sendCommand(t, pw, "gamerule minecraft:do_mob_spawning false")

		obs := &burnObs{}
		zomEids := map[int32]bool{}
		pump := func(d time.Duration) {
			deadline := time.Now().Add(d)
			for time.Now().Before(deadline) {
				_ = c.SetReadDeadline(time.Now().Add(120 * time.Millisecond))
				p, err := pr.ReadPacket()
				if err != nil {
					continue
				}
				if p.ID == server.IDPlayKeepAliveCB {
					_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
					continue
				}
				switch p.ID {
				case 0x75: // sound
					r := protocol.NewPacketReader(p.Data)
					if sid, err := r.VarInt(); err == nil && sid-1 == zid {
						obs.hurtN++
					}
				case server.IDPlayEntityMetadata:
					r := protocol.NewPacketReader(p.Data)
					eid, err := r.VarInt()
					if err != nil {
						continue
					}
					idx, err := r.Byte()
					if err != nil || idx != 0 {
						continue
					}
					if ty, _ := r.VarInt(); ty != 0 {
						continue
					}
					if v, err := r.Byte(); err == nil && zomEids[eid] && v&0x01 != 0 {
						obs.fireSeen = true
					}
				case server.IDPlaySpawnEntity:
					r := protocol.NewPacketReader(p.Data)
					eid, err := r.VarInt()
					if err != nil {
						continue
					}
					if _, err := r.UUID(); err != nil {
						continue
					}
					ty, err := r.VarInt()
					if err != nil {
						continue
					}
					if name, _ := server.EntityTypeName(ty); name == "zombie" {
						zomEids[eid] = true
					}
				}
			}
		}
		pump(500 * time.Millisecond)

		// --- noon: bare zombie ignites; a helmeted one does not.
		sendCommand(t, pw, "time set 6000")
		sendCommand(t, pw, "weather clear")
		sendCommand(t, pw, "summon minecraft:zombie ~3 ~ ~ {NoAI:1b}")
		sendCommand(t, pw, `summon minecraft:zombie ~-3 ~ ~ {NoAI:1b,equipment:{head:{id:"minecraft:leather_helmet",count:1}}}`)
		pump(30 * time.Second)
		out = append(out, fmt.Sprintf("noon fireSeen=%v hurtN>0=%v", obs.fireSeen, obs.hurtN > 0))
		t.Logf("%s noon leg: fireSeen=%v hurtN=%d", addr, obs.fireSeen, obs.hurtN)

		// --- night: nothing burns.
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		sendCommand(t, pw, "time set 23000")
		obs = &burnObs{}
		zomEids = map[int32]bool{}
		sendCommand(t, pw, "summon minecraft:zombie ~3 ~ ~ {NoAI:1b}")
		pump(20 * time.Second)
		out = append(out, fmt.Sprintf("night fireSeen=%v hurtN=%d", obs.fireSeen, obs.hurtN))
		return out
	}

	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	for i := range van {
		t.Logf("leg %d vanilla: %s", i, van[i])
		t.Logf("leg %d mc-go:   %s", i, mc[i])
	}
	for i := range van {
		if i < len(mc) && van[i] != mc[i] {
			t.Errorf("leg %d mismatch: vanilla %q mc-go %q", i, van[i], mc[i])
		}
	}
}
