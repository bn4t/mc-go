package blackbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestElytraParity pins the server half of elytra glide — measured on
// the 26.2 jar (ServerGamePacketListenerImpl.handlePlayerCommand,
// LivingEntity.tryToStartFallFlying / canGlide / updateFallFlying,
// ServerPlayer.checkMovementStatistics):
//
//   - START_FALL_FLYING (player-command action 6) sets shared flag 7
//     (0x80 in the index-0 flags byte) only when canGlide holds —
//     airborne, no vehicle, no levitation, a glide-capable chest item
//     (elytra whose damage is below max-1) — and the player is not in
//     water. Any other state calls stopFallFlying, which clears the
//     flag — the packet toggles the glide off too.
//   - updateFallFlying clears the flag the tick canGlide lapses
//     (landing, riding, effect, break), and every twentieth glide tick
//     wears the glider one damage.
//   - While the flag is set, moved distance banks aviate_one_cm
//     (3D), not the airborne fly_one_cm bucket.
//
// Player subjects are watched through a second session's entity-
// metadata stream — the flags broadcast is one index-0 entry per
// packet, so {0,0,flags,0xFF} tails are unambiguous.
func TestElytraParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "survival"}, map[string]string{
		"ops.json": opsJSONFor("P", "Q"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer tg.cleanup()

	metaPrefix := fmt.Sprintf("%#x:", server.IDPlayEntityMetadata)
	// flagBytes returns (set, clear): how many index-0 flag broadcasts
	// carried the 0x80 glide bit vs. cleared it. The shared-flag
	// broadcast is exactly VarInt(eid)+{0,0,flags,0xFF}; anything else
	// is ignored.
	flagBytes := func(lines []string) (set, clr int) {
		for _, l := range lines {
			if !strings.HasPrefix(l, metaPrefix) {
				continue
			}
			body := l[len(metaPrefix):]
			i := 0
			for i < len(body) && i < 5 {
				if body[i]&0x80 == 0 {
					i++
					break
				}
				i++
			}
			rest := body[i:]
			if len(rest) == 4 && rest[0] == 0 && rest[1] == 0 && rest[3] == 0xFF {
				if rest[2]&0x80 != 0 {
					set++
				} else {
					clr++
				}
			}
		}
		return
	}

	run := func(addr, name string) []string {
		var res []string
		cP, prP, pwP := joinPlayBlackbox(t, addr, "P")
		defer cP.Close()
		sinkP := newChatSinkTP(prP, pwP)
		defer func() {
			if t.Failed() {
				t.Logf("%s P stream: %s", name, sinkP.health())
				logs, _ := filepath.Glob(filepath.Join(os.TempDir(), "TestElytraParity*", "*", "vanilla.log"))
				for _, p := range logs {
					if b, err := os.ReadFile(p); err == nil {
						s := string(b)
						if len(s) > 8000 {
							s = s[len(s)-8000:]
						}
						t.Logf("%s vanilla.log tail:\n%s", name, s)
					}
				}
			}
		}()
		cQ, prQ, pwQ := joinPlayBlackbox(t, addr, "Q")
		defer cQ.Close()
		sinkQ := newChatSinkTP(prQ, pwQ)
		defer func() {
			if t.Failed() {
				t.Logf("%s Q stream: %s", name, sinkQ.health())
			}
		}()
		time.Sleep(500 * time.Millisecond)
		sinkP.take()
		sinkQ.take()

		action6 := func() {
			var b protocol.Buffer
			b.VarInt(0) // entity id (server reads the packet's sender)
			b.VarInt(6) // START_FALL_FLYING
			b.VarInt(0)
			if err := pwP.WritePacket(server.IDPlayPlayerCommandSB, b.Bytes()); err != nil {
				t.Fatal(err)
			}
		}
		move := func(x, y, z float64, og bool) {
			var b protocol.Buffer
			b.Double(x)
			b.Double(y)
			b.Double(z)
			var f byte
			if og {
				f = 0x01
			}
			b.Byte(f)
			if err := pwP.WritePacket(server.IDPlayMovePosSB, b.Bytes()); err != nil {
				t.Fatal(err)
			}
		}
		moveRot := func(x, y, z float64, yaw, pitch float32, og bool) {
			var b protocol.Buffer
			b.Double(x)
			b.Double(y)
			b.Double(z)
			b.Float(yaw)
			b.Float(pitch)
			var f byte
			if og {
				f = 0x01
			}
			b.Byte(f)
			if err := pwP.WritePacket(server.IDPlayMovePosRotSB, b.Bytes()); err != nil {
				t.Fatal(err)
			}
		}
		say := func(cmd, marker string) bool {
			sendCommand(t, pwP, cmd)
			time.Sleep(450 * time.Millisecond)
			return sawMarker(sinkQ.take(), marker)
		}
		flags := func(d time.Duration) (int, int) {
			time.Sleep(d)
			return flagBytes(sinkQ.take())
		}

		sendCommand(t, pwP, "scoreboard objectives add zav minecraft.custom:minecraft.aviate_one_cm")
		sendCommand(t, pwP, "scoreboard objectives add zfly minecraft.custom:minecraft.fly_one_cm")
		sendCommand(t, pwP, "tp P 8 -56 8")
		move(8.5, -56, 8.5, false) // airborne claim before the first deploy
		flags(500 * time.Millisecond)

		// No glider equipped: the packet is a no-op.
		action6()
		if set, _ := flags(700 * time.Millisecond); set > 0 {
			res = append(res, "no item blocked:miss")
		} else {
			res = append(res, "no item blocked:hit")
		}

		// Equip + deploy: flag goes on.
		sendCommand(t, pwP, "item replace entity P armor.chest with minecraft:elytra")
		move(8.5, -56, 8.5, false)
		action6()
		if set, _ := flags(700 * time.Millisecond); set > 0 {
			res = append(res, "deploy sets flag:hit")
		} else {
			res = append(res, "deploy sets flag:miss")
		}

		// The same packet toggles the glide off (stopFallFlying).
		action6()
		if _, clr := flags(700 * time.Millisecond); clr > 0 {
			res = append(res, "toggle clears:hit")
		} else {
			res = append(res, "toggle clears:miss")
		}

		// Landing lapses canGlide — the flag clears without a packet.
		action6()
		move(8.5, -60, 8.5, true)
		if _, clr := flags(900 * time.Millisecond); clr > 0 {
			res = append(res, "land clears:hit")
		} else {
			res = append(res, "land clears:miss")
		}

		// Grounded deploy is gated by canGlide's onGround check.
		action6()
		if set, _ := flags(700 * time.Millisecond); set > 0 {
			res = append(res, "grounded blocked:miss")
		} else {
			res = append(res, "grounded blocked:hit")
		}

		// Airborne glide banks aviate_one_cm — never the fly bucket.
		// zfly2 is fresh here: earlier airborne moves already banked
		// fly_one_cm, so a zero probe needs an objective with no
		// history.
		sendCommand(t, pwP, "scoreboard objectives add zfly2 minecraft.custom:minecraft.fly_one_cm")
		move(8.5, -56, 8.5, false)
		action6()
		for i := 0; i < 4; i++ {
			move(8.5+float64(i)*0.5, -56, 8.5, false)
			time.Sleep(80 * time.Millisecond)
		}
		flags(500 * time.Millisecond)
		if say(`execute if score P zav matches 150.. run say ZEL-aviate`, "ZEL-aviate") {
			res = append(res, "glide banks aviate:hit")
		} else {
			res = append(res, "glide banks aviate:miss")
		}
		if say(`execute if score P zfly2 matches 0 run say ZEL-nofly`, "ZEL-nofly") {
			res = append(res, "glide skips fly:hit")
		} else {
			res = append(res, "glide skips fly:miss")
		}

		// Twenty glide ticks wear the elytra one point. The damage
		// sub-predicate's MinMaxBounds form is what vanilla parses:
		// {damage:0} stays pristine, {damage:{min:1}} any wear.
		for i := 0; i < 6; i++ {
			move(10.5, -56, 8.5, false)
			time.Sleep(180 * time.Millisecond)
		}
		if say(`execute if items entity P armor.chest minecraft:elytra[minecraft:damage~{damage:0}] run say ZEL-nowear`, "ZEL-nowear") {
			res = append(res, "elytra still pristine:hit")
		} else {
			res = append(res, "elytra still pristine:miss")
		}
		if say(`execute if items entity P armor.chest minecraft:elytra[minecraft:damage~{damage:{min:1}}] run say ZEL-wear`, "ZEL-wear") {
			res = append(res, "glide wears elytra:hit")
		} else {
			res = append(res, "glide wears elytra:miss")
		}
		move(8.5, -60, 8.5, true)
		flags(500 * time.Millisecond)

		// Levitation is a canGlide gate — and clearing the effect
		// unblocks the deploy.
		sendCommand(t, pwP, "effect give P minecraft:levitation 60")
		move(8.5, -56, 8.5, false)
		action6()
		if set, _ := flags(700 * time.Millisecond); set > 0 {
			res = append(res, "levitation blocked:miss")
		} else {
			res = append(res, "levitation blocked:hit")
		}
		sendCommand(t, pwP, "effect clear P minecraft:levitation")
		move(8.5, -56, 8.5, false)
		action6()
		move(8.5, -60, 8.5, true)
		flags(600 * time.Millisecond)

		// In-water deploy is gated by tryToStartFallFlying's own check.
		sendCommand(t, pwP, "setblock 8 -56 8 minecraft:water")
		sendCommand(t, pwP, "tp P 8 -56 8")
		move(8.5, -56, 8.5, false)
		if say(`execute if block 8 -56 8 minecraft:water run say ZEL-water`, "ZEL-water") {
			res = append(res, "feet in water:hit")
		} else {
			res = append(res, "feet in water:miss")
		}
		action6()
		if set, _ := flags(700 * time.Millisecond); set > 0 {
			res = append(res, "water blocked:miss")
		} else {
			res = append(res, "water blocked:hit")
		}
		sendCommand(t, pwP, "setblock 8 -56 8 minecraft:air")

		// A passenger cannot deploy.
		sendCommand(t, pwP, "summon minecraft:pig 8 -56 8 {NoAI:1b}")
		sendCommand(t, pwP, "tp P 8 -56 8")
		move(8.5, -56, 8.5, false)
		sendCommand(t, pwP, "ride P mount @e[type=minecraft:pig,limit=1]")
		time.Sleep(300 * time.Millisecond)
		action6()
		if set, _ := flags(700 * time.Millisecond); set > 0 {
			res = append(res, "riding blocked:miss")
		} else {
			res = append(res, "riding blocked:hit")
		}
		sendCommand(t, pwP, "ride P dismount")

		// An elytra at the break threshold (damage >= max-1) does not
		// glide.
		sendCommand(t, pwP, "item replace entity P armor.chest with minecraft:elytra[minecraft:damage=431]")
		if say(`execute if items entity P armor.chest minecraft:elytra[minecraft:damage~{damage:431}] run say ZEL-dmg`, "ZEL-dmg") {
			res = append(res, "damage=431 lands:hit")
		} else {
			res = append(res, "damage=431 lands:miss")
		}
		move(8.5, -56, 8.5, false)
		action6()
		if set, _ := flags(700 * time.Millisecond); set > 0 {
			res = append(res, "broken elytra blocked:miss")
		} else {
			res = append(res, "broken elytra blocked:hit")
		}
		sendCommand(t, pwP, "item replace entity P armor.chest with minecraft:elytra")

		// Creative mode is no gate — canGlide never checks it.
		sendCommand(t, pwP, "gamemode creative P")
		move(8.5, -56, 8.5, false)
		action6()
		if set, _ := flags(700 * time.Millisecond); set > 0 {
			res = append(res, "creative deploys:hit")
		} else {
			res = append(res, "creative deploys:miss")
		}
		move(8.5, -60, 8.5, true)
		flags(500 * time.Millisecond)

		// fly_into_wall: while flag-7 is set the server sim runs
		// updateFallFlyingMovement each tick — the delta builds
		// unclamped, Entity.move sweeps it against the world, and
		// restituteMovementAfterCollisions zeroes the components on
		// axes that hit. handleFallFlyingCollisions pays
		// (oldH-newH)·10-3 when horizontalCollision and the loss
		// tops 0.3. Hovering pressed to the wall never hits: the
		// delta dies every collide tick and can't rebuild (measured
		// on vanilla — Motion[0] stays 0, no damage events). A real
		// client hits by arriving with speed — claims advance into
		// the wall. So build speed in open air, then advance claims
		// to the stone at x=20 like an approaching client would.
		sendCommand(t, pwP, "gamemode survival P")
		sendCommand(t, pwP, "fill 20 -61 4 20 -50 12 minecraft:stone")
		sendCommand(t, pwP, "effect give P minecraft:instant_health 1 10")
		// /tp relocations: a move packet jumping >10 blocks is
		// rejected as "moved too quickly" on vanilla.
		sendCommand(t, pwP, "tp P 15 -56 8")
		moveRot(15, -56, 8, -90, -30, false)
		action6()
		flags(400 * time.Millisecond)
		// ~1s of buildup ticks to reach terminal glide speed, then a
		// ~4-block approach ending pressed on the wall face.
		for i := 0; i < 12; i++ {
			moveRot(15, -56, 8, -90, -30, false)
			time.Sleep(80 * time.Millisecond)
		}
		for _, x := range []float64{15.7, 16.4, 17.1, 17.8, 18.5, 19.1, 19.7, 19.7, 19.7, 19.7} {
			moveRot(x, -56, 8, -90, -30, false)
			time.Sleep(80 * time.Millisecond)
		}
		// Damage events, not health — regen masks small hits by the
		// time /data get reads it. Bucketed: the hit count depends
		// on packet/tick phasing, whether it hit doesn't.
		de := 0
		for _, l := range sinkP.take() {
			if strings.HasPrefix(l, fmt.Sprintf("%#x:", server.IDPlayDamageEvent)) {
				de++
			}
		}
		if de > 0 {
			res = append(res, "fly_into_wall:hit")
		} else {
			res = append(res, "fly_into_wall:safe")
		}
		// The same approach in open air collides with nothing.
		sendCommand(t, pwP, "effect give P minecraft:instant_health 1 10")
		sendCommand(t, pwP, "tp P 15 -56 30")
		moveRot(15, -56, 30, -90, -30, false)
		time.Sleep(300 * time.Millisecond)
		for i := 0; i < 12; i++ {
			moveRot(15, -56, 30, -90, -30, false)
			time.Sleep(80 * time.Millisecond)
		}
		for _, x := range []float64{15.7, 16.4, 17.1, 17.8, 18.5, 19.1, 19.7, 19.7, 19.7, 19.7} {
			moveRot(x, -56, 30, -90, -30, false)
			time.Sleep(80 * time.Millisecond)
		}
		de = 0
		for _, l := range sinkP.take() {
			if strings.HasPrefix(l, fmt.Sprintf("%#x:", server.IDPlayDamageEvent)) {
				de++
			}
		}
		if de > 0 {
			res = append(res, "open glide:hit")
		} else {
			res = append(res, "open glide:safe")
		}

		return res
	}

	want := run(tg.vanilla, "vanilla")
	got := run(tg.mcgo, "mcgo")
	if len(want) != len(got) {
		t.Fatalf("leg count: vanilla %d, mc-go %d\n%v\n%v", len(want), len(got), want, got)
	}
	for i := range want {
		wl, gl := strings.SplitN(want[i], ":", 2), strings.SplitN(got[i], ":", 2)
		if len(wl) != 2 || len(gl) != 2 || wl[0] != gl[0] {
			t.Fatalf("leg %d misaligned: vanilla %q, mc-go %q", i, want[i], got[i])
		}
		t.Logf("%-24s vanilla=%s mcgo=%s", wl[0], wl[1], gl[1])
		if want[i] != got[i] {
			t.Errorf("leg %q: vanilla %s, mc-go %s", wl[0], wl[1], gl[1])
		}
	}
}
