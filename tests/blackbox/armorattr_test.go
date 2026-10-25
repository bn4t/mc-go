// Armor toughness, knockback resistance, and the no_knockback tag.
// Measured on the vanilla 26.2 jar (CombatRules.getDamageAfterAbsorb /
// LivingEntity.hurtServer's dealDefaultKnockback):
//
//   - armor reduction: defense = clamp(armor − damage/(2+toughness/4),
//     armor·0.2, 20); taken = damage·(1 − defense/25). Toughness makes
//     heavy armor soak large hits: a 20-damage blow lands 8.0 on a
//     diamond set (tough 8) but 16.0 on iron (tough 0).
//   - hurt knockback: an entity-sourced hurt whose type isn't in the
//     no_knockback tag shoves the victim 0.4·(1−kb_resist) away from
//     the source's position; a full netherite set eats 40%.
//   - /damage ... by <entity> takes the same path — "generic" is in
//     no_knockback, "mob_attack" is not.
//
// The victim's own set_entity_motion is read on A's conn: vanilla's
// hurtMarked delta sync goes to the hurt player too — that packet is
// what makes the client's own view lurch.
package blackbox

import (
	"fmt"
	"math"
	"net"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// attrObs folds set_health and the victim's entity_motion on one conn.
type attrObs struct {
	health    float32
	sawHealth bool
	motion    [3]float64
	sawMotion bool
}

// joinPlayEid is joinPlayBlackbox plus the entity id from the join
// game packet.
func joinPlayEid(t *testing.T, addr, name string) (net.Conn, *protocol.Reader, *protocol.Writer, int32) {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, name)
	for i := 0; i < 300; i++ {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("%s join stream: %v", addr, err)
		}
		switch p.ID {
		case server.IDPlayKeepAliveCB:
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
		case server.IDPlayJoinGame:
			r := protocol.NewPacketReader(p.Data)
			eid, err := r.Int()
			if err != nil {
				t.Fatalf("%s join eid: %v", addr, err)
			}
			return c, pr, pw, eid
		}
	}
	t.Fatalf("%s: join game packet never arrived", addr)
	return nil, nil, nil, 0
}

// pumpAttr drains A's conn for d, answering keepalives/teleports and
// recording the last health + the motion packet addressed to aID.
func pumpAttr(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, d time.Duration, aID int32, obs *attrObs) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(time.Now().Add(time.Until(deadline)))
		p, err := pr.ReadPacket()
		if err != nil {
			break
		}
		switch p.ID {
		case server.IDPlayKeepAliveCB:
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
		case server.IDPlaySyncPos:
			r := protocol.NewPacketReader(p.Data)
			if id, err := r.VarInt(); err == nil {
				var b protocol.Buffer
				b.VarInt(id)
				_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
			}
		case server.IDPlaySetHealth:
			r := protocol.NewPacketReader(p.Data)
			if f, err := r.Float(); err == nil {
				obs.health, obs.sawHealth = f, true
			}
		case server.IDPlayEntityMotion:
			r := protocol.NewPacketReader(p.Data)
			eid, err := r.VarInt()
			if err != nil {
				continue
			}
			x, y, z, ok := decodeLpVec3(r)
			if !ok || eid != aID {
				continue
			}
			obs.motion, obs.sawMotion = [3]float64{x, y, z}, true
		}
	}
}

// summonAt runs /summon at absolute coords and polls /data get on a
// typed selector until the entity answers — the existence check stays
// target-agnostic (both servers emit a query reply or notfound).
func summonAt(t *testing.T, sink *chatSink, pw *protocol.Writer, mob string, x, y, z float64) {
	t.Helper()
	sel := fmt.Sprintf("@e[type=%s,limit=1]", mob)
	sendCommand(t, pw, fmt.Sprintf("summon %s %v %v %v", mob, x, y, z))
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		sendCommand(t, pw, "data get entity "+sel+" Health")
		sendCommand(t, pw, "say ZEND")
		dl := time.Now().Add(2 * time.Second)
		got := ""
		for time.Now().Before(dl) {
			end := false
			for _, line := range sink.take() {
				if i := strings.Index(line, "data.entity.query"); i >= 0 {
					got = line[:i]
				}
				if strings.Contains(line, "ZEND") {
					end = true
				}
			}
			if end {
				break
			}
		}
		if got != "" && lastNumber(got) > 0 {
			return
		}
	}
	t.Fatalf("%s never spawned at %v,%v,%v", mob, x, y, z)
}

// nbtFloatOf reads a numeric NBT path off an entity via /data get —
// the value renders right before the commands.data.entity.query
// translate key. Returns -1 when the path is absent, -2 on no reply.
func nbtFloatOf(t *testing.T, sink *chatSink, pw *protocol.Writer, sel, path string) float64 {
	t.Helper()
	sendCommand(t, pw, "data get entity "+sel+" "+path)
	sendCommand(t, pw, "say ZEND")
	deadline := time.Now().Add(6 * time.Second)
	got := ""
	for time.Now().Before(deadline) {
		end := false
		for _, line := range sink.take() {
			if i := strings.Index(line, "data.entity.query"); i >= 0 {
				got = line[:i]
			}
			if strings.Contains(line, "ZEND") {
				end = true
			}
		}
		if end {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if got == "" {
		return -2
	}
	return lastNumber(got)
}

// equipArmor fills A's four armor slots (nil item clears the slot).
func equipArmor(t *testing.T, pw *protocol.Writer, armor []string) {
	t.Helper()
	slots := []string{"armor.head", "armor.chest", "armor.legs", "armor.feet"}
	for i, it := range armor {
		if it == "" {
			it = "minecraft:air"
		}
		sendCommand(t, pw, "item replace entity A "+slots[i]+" with "+it)
	}
}

// waitSurvival confirms A is in survival (and alive) on both servers:
// intermittently a rejoining A has been observed to stay invulnerable
// — dead-state carryover or a dropped gamemode packet — which makes
// /damage answer commands.damage.invulnerable forever. Kill+respawn
// fixes that; then poll the gamemode until a selector sees it.
func waitSurvival(t *testing.T, sinkC *chatSink, pwC, pwA *protocol.Writer) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if h := nbtFloatOf(t, sinkC, pwC, "A", "Health"); h <= 0 {
			// Dead state survives rejoin — the respawn must come from
			// A's own client-status packet.
			var b protocol.Buffer
			b.VarInt(0)
			if err := pwA.WritePacket(server.IDPlayClientStatusSB, b.Bytes()); err == nil {
				time.Sleep(800 * time.Millisecond)
			}
		}
		sendCommand(t, pwC, "gamemode survival A")
		sinkC.take()
		sendCommand(t, pwC, "execute if entity @a[name=A,gamemode=survival] run say AGM")
		deadline2 := time.Now().Add(400 * time.Millisecond)
		for time.Now().Before(deadline2) {
			for _, line := range sinkC.take() {
				if strings.Contains(line, "AGM") {
					return
				}
			}
			time.Sleep(60 * time.Millisecond)
		}
	}
	t.Fatalf("A never reached survival mode")
}

func TestArmorAttrParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("A", "C")}, nil)
	defer targets.cleanup()

	srvs := []struct{ label, addr string }{
		{"vanilla", targets.vanilla},
		{"mc-go", targets.mcgo},
	}

	// dropLeg equips A, /damage A 20 (no entity source — no kb or
	// thorns), and compares the health actually lost.
	dropLeg := func(armor []string, want float32) func(*testing.T, string) {
		return func(t *testing.T, addr string) {
			connA, prA, pwA, aID := joinPlayEid(t, addr, "A")
			defer connA.Close()
			connC, prC, pwC := joinPlayBlackbox(t, addr, "C")
			defer connC.Close()
			sinkC := newChatSinkTP(prC, pwC)
			sendCommand(t, pwC, "gamemode spectator C")
			sendCommand(t, pwC, "gamemode survival A")
			sendCommand(t, pwC, "gamerule minecraft:natural_health_regeneration false")
			sendCommand(t, pwC, "tp A 0.5 -60 0.5")
			var obs attrObs
			pumpAttr(t, connA, prA, pwA, 800*time.Millisecond, aID, &obs)
			equipArmor(t, pwC, armor)
			pumpAttr(t, connA, prA, pwA, 400*time.Millisecond, aID, &obs)
			// Health persists across rejoin — earlier legs may have left
			// A wounded or dead. Read the baseline, and when it can't
			// absorb the hit, kill+respawn: a fresh spawn always lands
			// at 20.
			baseline := nbtFloatOf(t, sinkC, pwC, "A", "Health")
			if baseline < float64(want)+2 {
				sendCommand(t, pwC, "kill A")
				pumpAttr(t, connA, prA, pwA, 500*time.Millisecond, aID, &obs)
				var b protocol.Buffer
				b.VarInt(0) // client status: perform respawn
				if err := pwA.WritePacket(server.IDPlayClientStatusSB, b.Bytes()); err != nil {
					t.Fatalf("%s respawn request: %v", addr, err)
				}
				// Respawn carries a short invulnerability window on
				// vanilla — wait it out before the measured hit.
				pumpAttr(t, connA, prA, pwA, 2500*time.Millisecond, aID, &obs)
				sendCommand(t, pwC, "gamemode survival A")
				sendCommand(t, pwC, "tp A 0.5 -60 0.5")
				pumpAttr(t, connA, prA, pwA, 800*time.Millisecond, aID, &obs)
				// Death drops the set on both servers — re-equip
				// unconditionally.
				equipArmor(t, pwC, armor)
				pumpAttr(t, connA, prA, pwA, 300*time.Millisecond, aID, &obs)
				baseline = nbtFloatOf(t, sinkC, pwC, "A", "Health")
				t.Logf("%s post-respawn baseline %v", addr, baseline)
			}
			if baseline < float64(want)+1 {
				t.Fatalf("%s: A health baseline %v too low for drop %v", addr, baseline, want)
			}
			obs.sawHealth = false
			sendCommand(t, pwA, "damage A 20 minecraft:mob_attack")
			pumpAttr(t, connA, prA, pwA, 700*time.Millisecond, aID, &obs)
			if !obs.sawHealth {
				t.Fatalf("%s: no health update", addr)
			}
			got := float32(baseline) - obs.health
			if math.Abs(float64(got-want)) > 0.01 {
				t.Fatalf("%s 20-damage drop = %v, want %v", addr, got, want)
			}
		}
	}

	for _, name := range []struct {
		tag   string
		armor []string
		want  float32
	}{
		{"diamond", []string{"minecraft:diamond_helmet", "minecraft:diamond_chestplate", "minecraft:diamond_leggings", "minecraft:diamond_boots"}, 8.0},
		{"netherite", []string{"minecraft:netherite_helmet", "minecraft:netherite_chestplate", "minecraft:netherite_leggings", "minecraft:netherite_boots"}, 7.2},
		{"iron", []string{"minecraft:iron_helmet", "minecraft:iron_chestplate", "minecraft:iron_leggings", "minecraft:iron_boots"}, 16.0},
		{"leather", []string{"minecraft:leather_helmet", "minecraft:leather_chestplate", "minecraft:leather_leggings", "minecraft:leather_boots"}, 18.88},
	} {
		leg := dropLeg(name.armor, name.want)
		for _, s := range srvs {
			t.Run(name.tag+"/"+s.label, func(t *testing.T) { leg(t, s.addr) })
		}
	}

	// kbLeg damages A by a zombie standing ~9 blocks away and returns
	// the horizontal magnitude of A's motion packet as seen by the
	// observer O (0 when none arrives — vanilla ships the victim's
	// hurt delta to chunk trackers only, never to the victim).
	kbLeg := func(t *testing.T, addr string, armor []string, dtype string) float64 {
		connA, prA, pwA, aID := joinPlayEid(t, addr, "A")
		defer connA.Close()
		connC, prC, pwC := joinPlayBlackbox(t, addr, "C")
		defer connC.Close()
		sinkC := newChatSinkTP(prC, pwC)
		sendCommand(t, pwC, "gamemode spectator C")
		sendCommand(t, pwC, "gamerule minecraft:natural_health_regeneration false")
		sendCommand(t, pwC, "tp A 0.5 -60 0.5")
		waitSurvival(t, sinkC, pwC, pwA)
		var obs attrObs
		pumpAttr(t, connA, prA, pwA, 600*time.Millisecond, aID, &obs)
		// The damage cause must never move or melee A itself — an
		// armor stand idles where summoned, unlike a zombie.
		summonAt(t, sinkC, pwC, "minecraft:armor_stand", 9.5, -60, 8.5)
		equipArmor(t, pwC, armor)
		// Vanilla's ~3s join invulnerability silently eats /damage —
		// retry the hit across a few windows until a motion packet
		// reaches A's own conn (victims get their hurt delta too).
		pumpAttr(t, connA, prA, pwA, 3600*time.Millisecond, aID, &obs)
		for i := 0; i < 4; i++ {
			obs.sawMotion = false
			sendCommand(t, pwC, "damage A 4 "+dtype+" by @e[type=minecraft:armor_stand,limit=1]")
			pumpAttr(t, connA, prA, pwA, 700*time.Millisecond, aID, &obs)
			if obs.sawMotion {
				return math.Hypot(obs.motion[0], obs.motion[2])
			}
		}
		t.Logf("%s: no motion for aID=%d; replies=%q", addr, aID, sinkC.take())
		return 0
	}

	t.Run("hurt-kb", func(t *testing.T) {
		for _, s := range srvs {
			mag := kbLeg(t, s.addr, nil, "minecraft:mob_attack")
			if mag < 0.3 || mag > 0.5 {
				t.Fatalf("%s hurt-kb magnitude = %v, want ~0.4", s.label, mag)
			}
		}
	})
	t.Run("hurt-kb-resist", func(t *testing.T) {
		for _, s := range srvs {
			mag := kbLeg(t, s.addr, []string{"minecraft:netherite_helmet", "minecraft:netherite_chestplate", "minecraft:netherite_leggings", "minecraft:netherite_boots"}, "minecraft:mob_attack")
			if mag < 0.18 || mag > 0.3 {
				t.Fatalf("%s netherite hurt-kb magnitude = %v, want ~0.24", s.label, mag)
			}
		}
	})
	t.Run("no-knockback", func(t *testing.T) {
		for _, s := range srvs {
			if mag := kbLeg(t, s.addr, nil, "minecraft:generic"); mag != 0 {
				t.Fatalf("%s generic hurt-kb magnitude = %v, want none", s.label, mag)
			}
		}
	})
	t.Run("mob-melee-kb", func(t *testing.T) {
		for _, s := range srvs {
			connA, prA, pwA, aID := joinPlayEid(t, s.addr, "A")
			defer connA.Close()
			connC, prC, pwC := joinPlayBlackbox(t, s.addr, "C")
			defer connC.Close()
			sinkC := newChatSinkTP(prC, pwC)
			sendCommand(t, pwC, "gamemode spectator C")
			sendCommand(t, pwC, "time set night") // zombies must not burn
			sendCommand(t, pwC, "gamerule minecraft:natural_health_regeneration false")
			sendCommand(t, pwC, "tp A 0.5 -60 0.5")
			waitSurvival(t, sinkC, pwC, pwA)
			var obs attrObs
			pumpAttr(t, connA, prA, pwA, 800*time.Millisecond, aID, &obs)
			sendCommand(t, pwC, "kill @e[type=minecraft:zombie]")
			summonAt(t, sinkC, pwC, "minecraft:zombie", 1.5, -60, 0.5)
			// Early zombie hits land inside A's join invulnerability;
			// melee retries every second, so give it headroom.
			deadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) {
				pumpAttr(t, connA, prA, pwA, 400*time.Millisecond, aID, &obs)
				if obs.sawMotion && math.Hypot(obs.motion[0], obs.motion[2]) > 0.1 {
					return
				}
				obs.sawMotion = false
			}
			t.Fatalf("%s zombie melee produced no hurt-kb on A", s.label)
		}
	})
}
