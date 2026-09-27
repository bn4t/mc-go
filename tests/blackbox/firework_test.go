//go:build blackbox

package blackbox

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestFireworkBoostParity pins the firework rocket's use + entity
// halves — measured on the 26.2 jar (FireworkRocketItem.use/useOn,
// FireworkRocketEntity tick/explode/dealExplosionDamage):
//
//   - use on air is only live while the sender is fall-flying: an
//     attached rocket spawns at the player's position with
//     DATA_ATTACHED_TO_TARGET set (entity-data index 9,
//     optional_unsigned_int VarInt eid+1), held leashes drop with
//     LEAD_BREAK, the stack consumes one (creative keeps it), and
//     ITEM_USED awards.
//   - useOn a block is a PASS while gliding; otherwise a free rocket
//     spawns at the click point pushed 0.15 toward the face and the
//     stack shrinks one in EVERY gamemode.
//   - life==0 fires the FIREWORK_ROCKET_LAUNCH ambient sound; life >
//     lifetime explodes — entity-event 17 plus 5+2·explosions damage
//     to the attached entity, the sqrt falloff inside 5 blocks for
//     everyone else, then discard.
func TestFireworkBoostParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "survival"}, map[string]string{
		"ops.json": opsJSONFor("P", "Q"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer tg.cleanup()

	spawnPrefix := fmt.Sprintf("%#x:", server.IDPlaySpawnEntity)
	metaPrefix := fmt.Sprintf("%#x:", server.IDPlayEntityMetadata)
	eventPrefix := fmt.Sprintf("%#x:", server.IDPlayEntityEvent)
	soundPrefix := fmt.Sprintf("%#x:", int32(0x75))
	healthPrefix := fmt.Sprintf("%#x:", server.IDPlaySetHealth)

	readVarInt := func(body string, i int) (int, int) {
		v := 0
		for k := 0; k < 5 && i+k < len(body); k++ {
			v |= int(body[i+k]&0x7F) << (7 * k)
			if body[i+k]&0x80 == 0 {
				return v, i + k + 1
			}
		}
		return v, -1
	}
	// rocketSpawns returns (eid,x,y,z) for every add_entity whose
	// entity-type VarInt reads 53.
	rocketSpawns := func(lines []string) [][4]float64 {
		var out [][4]float64
		for _, l := range lines {
			if !strings.HasPrefix(l, spawnPrefix) {
				continue
			}
			body := l[len(spawnPrefix):]
			eid, i := readVarInt(body, 0)
			if i < 0 || i+16 > len(body) {
				continue
			}
			et, j := readVarInt(body, i+16)
			if j < 0 || et != 53 || j+24 > len(body) {
				continue
			}
			x := math.Float64frombits(uint64(body[j])<<56 | uint64(body[j+1])<<48 | uint64(body[j+2])<<40 | uint64(body[j+3])<<32 | uint64(body[j+4])<<24 | uint64(body[j+5])<<16 | uint64(body[j+6])<<8 | uint64(body[j+7]))
			y := math.Float64frombits(uint64(body[j+8])<<56 | uint64(body[j+9])<<48 | uint64(body[j+10])<<40 | uint64(body[j+11])<<32 | uint64(body[j+12])<<24 | uint64(body[j+13])<<16 | uint64(body[j+14])<<8 | uint64(body[j+15]))
			z := math.Float64frombits(uint64(body[j+16])<<56 | uint64(body[j+17])<<48 | uint64(body[j+18])<<40 | uint64(body[j+19])<<32 | uint64(body[j+20])<<24 | uint64(body[j+21])<<16 | uint64(body[j+22])<<8 | uint64(body[j+23]))
			out = append(out, [4]float64{float64(eid), x, y, z})
		}
		return out
	}
	// rocketMeta scans a metadata payload for the rocket's {9,19,
	// varint} attached-id entry and its {10,8,byte} angle flag —
	// vanilla packs non-defaults only, so an absent entry reads as
	// its default (unattached / not shot at angle).
	rocketMeta := func(lines []string) (attached int, angle int, saw bool) {
		for _, l := range lines {
			if !strings.HasPrefix(l, metaPrefix) {
				continue
			}
			body := l[len(metaPrefix):]
			if i := strings.Index(body, "\x09\x13"); i >= 0 {
				if v, _ := readVarInt(body, i+2); v >= 0 {
					attached = v
					saw = true
				}
			}
			if i := strings.Index(body, "\x0a\x08"); i >= 0 && i+2 < len(body) {
				angle = int(body[i+2])
				saw = true
			}
		}
		return
	}
	rocketEvents := func(lines []string) (pops int) {
		for _, l := range lines {
			if !strings.HasPrefix(l, eventPrefix) {
				continue
			}
			body := l[len(eventPrefix):]
			if len(body) == 5 && body[4] == 17 {
				pops++
			}
		}
		return
	}
	launchSounds := func(lines []string) (n int) {
		for _, l := range lines {
			if !strings.HasPrefix(l, soundPrefix) {
				continue
			}
			body := l[len(soundPrefix):]
			if v, _ := readVarInt(body, 0); v == 633 { // firework_rocket.launch + 1
				n++
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
				logs, _ := filepath.Glob(filepath.Join(os.TempDir(), "TestFireworkBoostParity*", "*", "vanilla.log"))
				for _, p := range logs {
					if b, err := os.ReadFile(p); err == nil {
						s := string(b)
						if len(s) > 12000 {
							s = s[len(s)-12000:]
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
			b.VarInt(0)
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
		useAir := func() {
			var b protocol.Buffer
			b.VarInt(0) // main hand
			b.VarInt(0) // seq
			b.Float(0)  // yaw
			b.Float(0)  // pitch
			if err := pwP.WritePacket(server.IDPlayUseItemSB, b.Bytes()); err != nil {
				t.Fatal(err)
			}
		}
		useOn := func(x, y, z, face int32, cx, cy, cz float64) {
			var b protocol.Buffer
			b.VarInt(0)
			b.Position(x, y, z)
			b.VarInt(face)
			b.Float(float32(cx))
			b.Float(float32(cy))
			b.Float(float32(cz))
			b.Bool(true)  // insideBlock
			b.Bool(false) // missed (world-border hit flag on 26.2)
			b.VarInt(0)   // seq
			if err := pwP.WritePacket(server.IDPlayUseItemOnSB, b.Bytes()); err != nil {
				t.Fatal(err)
			}
		}
		say := func(cmd, marker string) bool {
			sendCommand(t, pwP, cmd)
			time.Sleep(450 * time.Millisecond)
			return sawMarker(sinkQ.take(), marker)
		}

		sendCommand(t, pwP, "scoreboard objectives add zuse minecraft.used:minecraft.firework_rocket")
		sendCommand(t, pwP, "tp P 8 -56 8")
		sendCommand(t, pwP, "item replace entity P armor.chest with minecraft:elytra")
		sendCommand(t, pwP, "clear P")
		sendCommand(t, pwP, "item replace entity P armor.chest with minecraft:elytra")
		sendCommand(t, pwP, "give P minecraft:firework_rocket 1")
		move(8.5, -56, 8.5, false)

		// Grounded use on air is vanilla's PASS — no rocket spawns.
		sinkQ.take()
		useAir()
		time.Sleep(700 * time.Millisecond)
		if len(rocketSpawns(sinkQ.take())) == 0 {
			res = append(res, "grounded use:quiet")
		} else {
			res = append(res, "grounded use:spawned")
		}
		if say(`execute if items entity P weapon.mainhand minecraft:firework_rocket run say ZFW-hold`, "ZFW-hold") {
			res = append(res, "grounded keeps stack:hit")
		} else {
			res = append(res, "grounded keeps stack:miss")
		}

		// Deploy the glide, then use: an attached rocket spawns at the
		// player's position, the launch sound plays, the stack
		// consumes, ITEM_USED awards.
		move(8.5, -56, 8.5, false)
		action6()
		time.Sleep(700 * time.Millisecond)
		sinkQ.take()
		useAir()
		time.Sleep(500 * time.Millisecond)
		lines := sinkQ.take()
		spawns := rocketSpawns(lines)
		attached, _, _ := rocketMeta(lines)
		if len(spawns) == 1 && attached > 0 {
			res = append(res, fmt.Sprintf("boost spawn:attached@%.1f", spawns[0][1]))
		} else {
			res = append(res, fmt.Sprintf("boost spawn:n=%d attached=%d", len(spawns), attached))
		}
		if launchSounds(lines) > 0 {
			res = append(res, "launch sound:hit")
		} else {
			res = append(res, "launch sound:miss")
		}
		if say(`execute unless items entity P weapon.mainhand minecraft:* run say ZFW-empty`, "ZFW-empty") {
			res = append(res, "boost consumed:hit")
		} else {
			res = append(res, "boost consumed:miss")
		}
		if say(`execute if score P zuse matches 1 run say ZFW-used`, "ZFW-used") {
			res = append(res, "used stat:hit")
		} else {
			res = append(res, "used stat:miss")
		}

		// useOn while gliding falls through to the boost — the rocket
		// spawns attached and the stack still consumes.
		sendCommand(t, pwP, "give P minecraft:firework_rocket 1")
		move(8.5, -58, 8.5, false)
		action6()
		time.Sleep(700 * time.Millisecond)
		sinkQ.take()
		useOn(8, -61, 8, 1, 0.5, 1.0, 0.5)
		time.Sleep(500 * time.Millisecond)
		if len(rocketSpawns(sinkQ.take())) == 0 {
			res = append(res, "glide useOn:quiet")
		} else {
			res = append(res, "glide useOn:spawned")
		}
		if say(`execute if items entity P weapon.mainhand minecraft:firework_rocket run say ZFW-hold`, "ZFW-hold") {
			res = append(res, "glide useOn keeps stack:hit")
		} else {
			res = append(res, "glide useOn keeps stack:miss")
		}

		// A starred boost hurts its own rider when it pops —
		// dealExplosionDamage's attached-entity flat 5+2·n.
		sendCommand(t, pwP, `give P minecraft:firework_rocket[minecraft:fireworks={flight_duration:1,explosions:[{shape:"small_ball",colors:[11743532]}]}] 1`)
		move(8.5, -56, 8.5, false)
		action6()
		time.Sleep(700 * time.Millisecond)
		sinkQ.take()
		useAir()
		time.Sleep(2600 * time.Millisecond) // flight-1 lifetime 20-31 ticks
		lines = sinkQ.take()
		if rocketEvents(lines) > 0 {
			res = append(res, "boost explode:event17")
		} else {
			res = append(res, "boost explode:quiet")
		}
		// Vanilla's exploded rocket rides ~the hand offset off the
		// rider, so its exact falloff drifts — pin hurt vs safe.
		if nbtFloatOf(t, sinkP, pwP, "P", "Health") < 20 {
			res = append(res, "boost self-damage:hurt")
		} else {
			res = append(res, "boost self-damage:safe")
		}

		// Creative still fires the boost — but consume(1, player)
		// keeps the stack.
		sendCommand(t, pwP, "gamemode creative P")
		sendCommand(t, pwP, "give P minecraft:firework_rocket 1")
		move(8.5, -56, 8.5, false)
		action6()
		time.Sleep(700 * time.Millisecond)
		sinkQ.take()
		useAir()
		time.Sleep(500 * time.Millisecond)
		if len(rocketSpawns(sinkQ.take())) == 1 {
			res = append(res, "creative boost:spawned")
		} else {
			res = append(res, "creative boost:quiet")
		}
		if say(`execute if items entity P weapon.mainhand minecraft:firework_rocket run say ZFW-hold`, "ZFW-hold") {
			res = append(res, "creative keeps stack:hit")
		} else {
			res = append(res, "creative keeps stack:miss")
		}
		// But useOn's raw shrink charges the rocket even in creative.
		move(8.5, -60, 8.5, true)
		time.Sleep(400 * time.Millisecond)
		sinkQ.take()
		useOn(8, -61, 8, 1, 0.5, 1.0, 0.5)
		time.Sleep(500 * time.Millisecond)
		lines = sinkQ.take()
		if len(rocketSpawns(lines)) == 1 {
			res = append(res, "creative useOn:spawned")
		} else {
			res = append(res, "creative useOn:quiet")
		}
		if say(`execute unless items entity P weapon.mainhand minecraft:* run say ZFW-empty`, "ZFW-empty") {
			res = append(res, "creative useOn consumes:hit")
		} else {
			res = append(res, "creative useOn consumes:miss")
		}
		sendCommand(t, pwP, "gamemode survival P")

		// Grounded useOn places a free rocket at the click + face·0.15
		// point, unattached, not shot at angle.
		sendCommand(t, pwP, "tp P 8 -60 8")
		sendCommand(t, pwP, "give P minecraft:firework_rocket 1")
		move(8.5, -60, 8.5, true)
		time.Sleep(400 * time.Millisecond)
		sinkQ.take()
		useOn(8, -61, 8, 1, 0.5, 1.0, 0.5)
		time.Sleep(500 * time.Millisecond)
		lines = sinkQ.take()
		spawns = rocketSpawns(lines)
		attached, angle, _ := rocketMeta(lines)
		if len(spawns) == 1 && attached == 0 {
			res = append(res, fmt.Sprintf("free spawn:x%.2f", spawns[0][1]))
		} else {
			res = append(res, fmt.Sprintf("free spawn:n=%d attached=%d", len(spawns), attached))
		}
		res = append(res, fmt.Sprintf("angle flag:%d", angle))

		// Summon NBT — a starless rocket pops with the event but deals
		// no damage; a starred one hurts a pig inside its 5-block
		// sweep; ShotAtAngle lands in entity data.
		sendCommand(t, pwP, "effect give P minecraft:instant_health 1 10")
		time.Sleep(500 * time.Millisecond)
		sendCommand(t, pwP, "summon minecraft:firework_rocket 4 -59 4 {LifeTime:1}")
		time.Sleep(600 * time.Millisecond)
		if rocketEvents(sinkQ.take()) > 0 {
			res = append(res, "summon pop:event17")
		} else {
			res = append(res, "summon pop:quiet")
		}
		if h := nbtFloatOf(t, sinkP, pwP, "P", "Health"); h >= 20 {
			res = append(res, fmt.Sprintf("starless no-damage:%v", h))
		} else {
			res = append(res, fmt.Sprintf("starless DAMAGE:%v", h))
		}
		sendCommand(t, pwP, "summon minecraft:pig 4 -59 4 {NoAI:1b}")
		sendCommand(t, pwP, `summon minecraft:firework_rocket 4 -59 4 {LifeTime:1,FireworksItem:{id:"minecraft:firework_rocket",count:1,components:{"minecraft:fireworks":{flight_duration:1,explosions:[{shape:"small_ball",colors:[11743532]}]}}}}`)
		time.Sleep(700 * time.Millisecond)
		if rocketEvents(sinkQ.take()) > 0 {
			res = append(res, "starred pop:event17")
		} else {
			res = append(res, "starred pop:quiet")
		}
		// The launch jitter is unseeded RNG on both servers, so the
		// explode position — and the sqrt falloff — drifts run to
		// run; pin hurt vs safe, not the exact float.
		if h := nbtFloatOf(t, sinkP, pwP, "@e[type=minecraft:pig,limit=1]", "Health"); h < 20 && h >= 0 {
			res = append(res, "area damage:hurt")
		} else {
			res = append(res, fmt.Sprintf("area damage:safe %.1f", h))
		}
		sinkQ.take()
		sendCommand(t, pwP, "summon minecraft:firework_rocket 4 -59 4 {ShotAtAngle:1b,LifeTime:60}")
		time.Sleep(500 * time.Millisecond)
		_, angle2, saw2 := rocketMeta(sinkQ.take())
		if saw2 && angle2 == 1 {
			res = append(res, "angle flag:1")
		} else {
			res = append(res, fmt.Sprintf("angle flag:%d saw=%v", angle2, saw2))
		}
		return res
	}

	want := run(tg.vanilla, "vanilla")
	got := run(tg.mcgo, "mcgo")
	_ = healthPrefix // reserved for future legs
	if len(want) != len(got) {
		t.Fatalf("leg count differs: vanilla=%v mcgo=%v", want, got)
	}
	for i := range want {
		if want[i] != got[i] {
			t.Errorf("leg %d: vanilla=%q mcgo=%q", i, want[i], got[i])
		}
	}
}

// TestFireworkRocketHitParity pins the rocket's onHit pipeline and the
// attached-boost mechanics — measured on the 26.2 jar
// (FireworkRocketEntity.tick/onHitEntity/onHitBlock,
// ProjectileUtil.getHitResultOnMoveVector/computeMargin,
// Entity.getHandHoldingItemAngle):
//
//   - BOTH the attached and free branches run the move-vector hit
//     scan: block clip first, then the nearest entity box inflated by
//     computeMargin (0→0.3 over 22 ticks). onHitEntity explodes
//     unconditionally — even a starless rocket pops on an entity;
//     onHitBlock explodes only when the rocket has explosions.
//   - The explosion's area damage runs a two-ray LOS check — rocket →
//     victim feet and rocket → victim mid; both blocked means safe.
//   - While the owner fall-flies the rocket's delta copies the
//     owner's and the owner gains look·0.1 + (look·1.5−d)·0.5 each
//     tick (the boost converges to ~1.7·look); the rocket rides at
//     getHandHoldingItemAngle — view(0°, yaw±80°)·0.5.
func TestFireworkRocketHitParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "survival"}, map[string]string{
		"ops.json": opsJSONFor("P", "Q"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer tg.cleanup()

	eventPrefix := fmt.Sprintf("%#x:", server.IDPlayEntityEvent)

	readVarInt := func(body string, i int) (int, int) {
		v := 0
		for k := 0; k < 5 && i+k < len(body); k++ {
			v |= int(body[i+k]&0x7F) << (7 * k)
			if body[i+k]&0x80 == 0 {
				return v, i + k + 1
			}
		}
		return v, -1
	}
	rocketPops := func(lines []string) int {
		n := 0
		for _, l := range lines {
			if !strings.HasPrefix(l, eventPrefix) {
				continue
			}
			body := l[len(eventPrefix):]
			if _, i := readVarInt(body, 0); i >= 0 && i < len(body) && body[i] == 17 {
				n++
			}
		}
		return n
	}

	starItem := `FireworksItem:{id:"minecraft:firework_rocket",count:1,components:{"minecraft:fireworks":{flight_duration:1,explosions:[{shape:"small_ball",colors:[11743532]}]}}}`

	run := func(addr, name string) []string {
		var res []string
		cP, prP, pwP := joinPlayBlackbox(t, addr, "P")
		defer cP.Close()
		sinkP := newChatSinkTP(prP, pwP)
		defer func() {
			if t.Failed() {
				t.Logf("%s P stream: %s", name, sinkP.health())
			}
		}()
		cQ, prQ, pwQ := joinPlayBlackbox(t, addr, "Q")
		defer cQ.Close()
		sinkQ := newChatSinkTP(prQ, pwQ)
		defer func() {
			if t.Failed() {
				t.Logf("%s Q stream: %s", name, sinkQ.health())
				logs, _ := filepath.Glob(filepath.Join(os.TempDir(), "TestFireworkRocketHitParity*", "*", "vanilla.log"))
				for _, p := range logs {
					if b, err := os.ReadFile(p); err == nil {
						s := string(b)
						if len(s) > 12000 {
							s = s[len(s)-12000:]
						}
						t.Logf("%s vanilla.log tail:\n%s", name, s)
					}
				}
			}
		}()
		time.Sleep(500 * time.Millisecond)
		sinkP.take()
		sinkQ.take()

		action6 := func() {
			var b protocol.Buffer
			b.VarInt(0)
			b.VarInt(6)
			b.VarInt(0)
			if err := pwP.WritePacket(server.IDPlayPlayerCommandSB, b.Bytes()); err != nil {
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
		useAir := func() {
			var b protocol.Buffer
			b.VarInt(0)
			b.VarInt(0)
			b.Float(0)
			b.Float(0)
			if err := pwP.WritePacket(server.IDPlayUseItemSB, b.Bytes()); err != nil {
				t.Fatal(err)
			}
		}

		sendCommand(t, pwP, "time set 18000")
		sendCommand(t, pwP, "gamemode survival P")
		sendCommand(t, pwP, "effect give P minecraft:instant_health 1 10")
		sendCommand(t, pwP, "tp P 30 -60 30")
		time.Sleep(400 * time.Millisecond)

		// === Leg A: entity hit — a starred shot-at-angle rocket
		// crossing a pillager's box explodes on contact and hurts it.
		sendCommand(t, pwP, "summon minecraft:pillager 20.5 -60 8.5 {NoAI:1b}")
		time.Sleep(300 * time.Millisecond)
		sinkQ.take()
		sendCommand(t, pwP, "summon minecraft:firework_rocket 16.5 -59.5 8.5 {ShotAtAngle:1b,Motion:[0.8d,0.0d,0.0d],"+starItem+"}")
		time.Sleep(1200 * time.Millisecond)
		if rocketPops(sinkQ.take()) > 0 {
			res = append(res, "entity hit:pop")
		} else {
			res = append(res, "entity hit:quiet")
		}
		if h := nbtFloatOf(t, sinkP, pwP, "@e[type=minecraft:pillager,limit=1]", "Health"); h < 20 && h >= 0 {
			res = append(res, fmt.Sprintf("entity hit:hurt %.1f", h))
		} else {
			res = append(res, fmt.Sprintf("entity hit:safe %.1f", h))
		}
		sendCommand(t, pwP, "kill @e[type=minecraft:pillager]")

		// === Leg B: block hit — starred explodes on the wall face,
		// starless keeps pushing till its fuse.
		sendCommand(t, pwP, "fill 24 -60 14 24 -55 18 minecraft:stone")
		sinkQ.take()
		sendCommand(t, pwP, "summon minecraft:firework_rocket 16.5 -59.5 16 {ShotAtAngle:1b,Motion:[0.8d,0.0d,0.0d],"+starItem+"}")
		time.Sleep(900 * time.Millisecond)
		if rocketPops(sinkQ.take()) > 0 {
			res = append(res, "block hit starred:pop")
		} else {
			res = append(res, "block hit starred:quiet")
		}
		sendCommand(t, pwP, "summon minecraft:firework_rocket 16.5 -59.5 16 {ShotAtAngle:1b,Motion:[0.8d,0.0d,0.0d],LifeTime:120}")
		time.Sleep(700 * time.Millisecond)
		if rocketPops(sinkQ.take()) == 0 {
			res = append(res, "block hit starless:quiet")
		} else {
			res = append(res, "block hit starless:pop")
		}
		time.Sleep(1500 * time.Millisecond)
		sinkQ.take()

		// === Leg C: LOS — a starred pop behind a two-thick ceiling
		// deals nothing; the same pop in open air hurts.
		sendCommand(t, pwP, "fill 29 -58 29 31 -57 31 minecraft:stone")
		sendCommand(t, pwP, "summon minecraft:firework_rocket 30 -55.5 30 {Life:200b,"+starItem+"}")
		time.Sleep(800 * time.Millisecond)
		sinkQ.take()
		if h := nbtFloatOf(t, sinkP, pwP, "P", "Health"); h >= 20 {
			res = append(res, "los blocked:safe")
		} else {
			res = append(res, fmt.Sprintf("los blocked:hurt %.1f", h))
		}
		sendCommand(t, pwP, "fill 29 -58 29 31 -57 31 minecraft:air")
		sendCommand(t, pwP, "effect give P minecraft:instant_health 1 10")
		time.Sleep(500 * time.Millisecond)
		sendCommand(t, pwP, "summon minecraft:firework_rocket 30 -55.5 30 {Life:200b,"+starItem+"}")
		time.Sleep(800 * time.Millisecond)
		sinkQ.take()
		if h := nbtFloatOf(t, sinkP, pwP, "P", "Health"); h < 20 {
			res = append(res, fmt.Sprintf("los open:hurt %.1f", h))
		} else {
			res = append(res, fmt.Sprintf("los open:safe %.1f", h))
		}
		sendCommand(t, pwP, "effect give P minecraft:instant_health 1 10")

		// === Leg D+E: boost — the attached rocket rides at the
		// getHandHoldingItemAngle offset (view(0,yaw+80)·0.5) and the
		// owner's glide delta converges toward 1.7·look.
		sendCommand(t, pwP, "item replace entity P armor.chest with minecraft:elytra")
		sendCommand(t, pwP, "item replace entity P weapon.mainhand with minecraft:firework_rocket")
		sendCommand(t, pwP, "tp P 15 -56 30")
		moveRot(15, -56, 30, -90, -30, false)
		action6()
		time.Sleep(600 * time.Millisecond)
		for i := 0; i < 10; i++ {
			moveRot(15, -56, 30, -90, -30, false)
			time.Sleep(70 * time.Millisecond)
		}
		sinkQ.take()
		useAir()
		moveRot(15, -56, 30, -90, -30, false)
		time.Sleep(600 * time.Millisecond)
		sinkQ.take()
		// view(0, -90+80=-10°)·0.5 = (0.087, 0, 0.492) — vanilla moves
		// the riding rocket with relative-move packets, so probe its
		// Pos through /data get rather than decoding the wire.
		rx := nbtFloatOf(t, sinkP, pwP, "@e[type=minecraft:firework_rocket,limit=1]", "Pos[0]")
		rz := nbtFloatOf(t, sinkP, pwP, "@e[type=minecraft:firework_rocket,limit=1]", "Pos[2]")
		if math.Abs(rx-(15+0.087)) < 0.15 && math.Abs(rz-(30+0.492)) < 0.15 {
			res = append(res, "hand offset:hit")
		} else {
			res = append(res, fmt.Sprintf("hand offset:miss %.3f,%.3f", rx, rz))
		}
		if mx := nbtFloatOf(t, sinkP, pwP, "P", "Motion[0]"); mx > 1.0 {
			res = append(res, fmt.Sprintf("boost delta:%.2f", mx))
		} else {
			res = append(res, fmt.Sprintf("boost delta:LOW %.2f", mx))
		}
		return res
	}

	want := run(tg.vanilla, "vanilla")
	got := run(tg.mcgo, "mcgo")
	if len(want) != len(got) {
		t.Fatalf("leg count differs: vanilla=%v mcgo=%v", want, got)
	}
	for i := range want {
		if want[i] != got[i] {
			t.Errorf("leg %d: vanilla=%q mcgo=%q", i, want[i], got[i])
		}
	}
}
