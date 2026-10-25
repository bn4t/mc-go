package blackbox

import (
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestPlayerFluidParity pins the player-side fluid chain as the jar
// splits it: LivingEntity.baseTick's air block runs every entity tick
// (eye in a water cell drains airSupply one per tick unless creative/
// spectator or a breathing effect; hitting -20 resets it to zero with
// the drown-particle event and 2 damage; elsewhere it refills by four
// to the 300 max — lava does NOT drain air), while the inside-block
// effects (lava's additive ignite + 4.0 lavaHurt, water extinguish,
// fire's ignite + in_fire) live in Entity.move — for the client-
// authoritative player that means a client move packet with a real
// displacement: a same-position echo scans no cells, so a tp'd-in
// player keeps the -20 fire baseline and full immunity until a packet
// actually moves it. Measured on the 26.2 jar: submerged, Air reads
// ~250 after forty ticks and hits the -20 threshold around tick 320;
// teleported into lava a still client reads Fire=-20 and takes no
// damage across thirty-plus ticks; one displaced packet lands
// min(300, fire+300)=280 and the four-damage hit.
func TestPlayerFluidParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("PFP")}, nil)
	defer targets.cleanup()

	for _, s := range []struct{ label, addr string }{
		{"vanilla", targets.vanilla}, {"mcgo", targets.mcgo},
	} {
		t.Run(s.label, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, s.addr, "PFP")
			defer c.Close()
			sendClientInformation(t, pw)
			sink := newChatSinkTP(pr, pw)
			sink.cap = 1 << 20
			time.Sleep(400 * time.Millisecond)
			sendCommand(t, pw, "gamerule do_mob_spawning false")
			sendCommand(t, pw, "kill @e[type=!minecraft:player]")
			for _, cmd := range []string{
				// 4-deep water column at (10,-62..-59,10).
				"fill 10 -62 10 10 -59 10 minecraft:water",
				// A second water column for the extinguish read.
				"fill 14 -62 14 14 -59 14 minecraft:water",
				// Landing pad — a floating hover kicks vanilla.
				"fill 30 -51 30 30 -51 30 minecraft:stone",
			} {
				sendCommand(t, pw, cmd)
			}
			sendCommand(t, pw, "gamemode survival @s")
			sendCommand(t, pw, "tp @s 10.5 -61.5 10.5")
			time.Sleep(200 * time.Millisecond)

			sendCommand(t, pw, "tick sprint 40")
			time.Sleep(400 * time.Millisecond)
			dump := dataGetAll(t, sink, pw, "@s")
			air, ok := snbtInt(t, dump, "Air")
			if !ok {
				t.Fatalf("%s: no Air in %s", s.label, dump)
			}
			// 40 sprinted ticks of drain plus the few wall ticks the
			// tp->sprint window ran — both legs land near 250.
			if air < 235 || air > 275 {
				t.Fatalf("%s: after 40t underwater Air=%d want ~250", s.label, air)
			}

			// Past the -20 reset: air clamps to zero, keeps draining,
			// and the two-damage drown hits start landing.
			sendCommand(t, pw, "tick sprint 330")
			time.Sleep(600 * time.Millisecond)
			t.Logf("%s post-drain: %s", s.label, sink.health())
			dump = dataGetAll(t, sink, pw, "@s")
			air, _ = snbtInt(t, dump, "Air")
			hp, ok := snbtFloat(t, dump, "Health")
			if !ok {
				t.Fatalf("%s: no Health in %s", s.label, dump)
			}
			if air > 0 || air <= -20 {
				t.Fatalf("%s: after drain Air=%d want (-20,0]", s.label, air)
			}
			if hp >= 20 {
				t.Fatalf("%s: drowning Health=%v want <20", s.label, hp)
			}

			// High ground water can't spread to: the supply climbs
			// back by four per tick.
			sendCommand(t, pw, "tp @s 30.5 -50.3 30.5")
			sendCommand(t, pw, "tick sprint 90")
			time.Sleep(400 * time.Millisecond)
			t.Logf("%s post-refill: %s", s.label, sink.health())
			dump = dataGetAll(t, sink, pw, "@s")
			air, _ = snbtInt(t, dump, "Air")
			if air != 300 {
				t.Fatalf("%s: refilled Air=%d want 300 dump=%s", s.label, air, dump)
			}

			// Lava: no air drain at all. The teleport's echoed
			// same-position move still runs the inside-block scan once —
			// igniteForTicks(300) sets Fire outright and the four-damage
			// lava hit lands (measured: Fire=300, Health-4 after the
			// echo). The column sits far from every water cell: vanilla
			// turns lava+water contact into obsidian, which suffocates
			// instead.
			sendCommand(t, pw, "effect give @s minecraft:instant_health 10 10 true")
			sendCommand(t, pw, "fill 24 -62 24 24 -59 24 minecraft:lava")
			sendCommand(t, pw, "tp @s 24.5 -61.5 24.5")
			time.Sleep(300 * time.Millisecond)
			dump = dataGetAll(t, sink, pw, "@s")
			air, _ = snbtInt(t, dump, "Air")
			hp, _ = snbtFloat(t, dump, "Health")
			fire, _ := snbtInt(t, dump, "Fire")
			if air != 300 {
				t.Fatalf("%s: lava Air=%d want 300", s.label, air)
			}
			if fire <= 0 {
				t.Fatalf("%s: lava-echo Fire=%d want >0 (same-pos moves still scan)", s.label, fire)
			}
			hpLava := hp
			// Standing still still burns: the entity's own per-tick move()
			// feeds the scan — measured ~4 lava hits per 30 ticks, paced
			// by invulnerability. Healed to 20 first so the window can't
			// kill either leg; assert only the direction, not the cadence.
			_ = sink.take() // drop earlier traffic
			sendCommand(t, pw, "tick sprint 25")
			time.Sleep(400 * time.Millisecond)
			for _, line := range sink.take() {
				if strings.HasPrefix(line, "0x19:") {
					r := protocol.NewPacketReader([]byte(line[5:]))
					vid, _ := r.VarInt()
					ty, _ := r.VarInt()
					t.Logf("%s damage_event victim=%d type=%d", s.label, vid, ty)
				}
			}
			dump = dataGetAll(t, sink, pw, "@s")
			hp2, _ := snbtFloat(t, dump, "Health")
			fire, _ = snbtInt(t, dump, "Fire")
			if hp2 >= hpLava {
				t.Fatalf("%s: still-in-lava Health %v -> %v; the per-tick scan should hurt", s.label, hpLava, hp2)
			}
			if hp2 <= 0 {
				t.Fatalf("%s: still-in-lava Health=%v; window too long to keep the player alive", s.label, hp2)
			}
			if fire <= 0 {
				t.Fatalf("%s: still-in-lava Fire=%d want >0 (countdown from 300)", s.label, fire)
			}

			// A real displacement inside lava is one more application of
			// the inside-block scan: Fire stays pinned at 300 and the
			// 4.0 hit lands when grace permits — never heals the player.
			beforeMove := hp2
			var mb protocol.Buffer
			mb.Double(24.7)
			mb.Double(-61.5)
			mb.Double(24.5)
			mb.Byte(1)
			_ = pw.WritePacket(server.IDPlayMovePosSB, mb.Bytes())
			time.Sleep(400 * time.Millisecond)
			dump = dataGetAll(t, sink, pw, "@s")
			hp2, _ = snbtFloat(t, dump, "Health")
			fire, _ = snbtInt(t, dump, "Fire")
			t.Logf("%s move-in-lava: Fire=%d Health=%v", s.label, fire, hp2)
			if fire <= 0 {
				t.Fatalf("%s: move-in-lava Fire=%d want >0", s.label, fire)
			}
			if hp2 > beforeMove {
				t.Fatalf("%s: move-in-lava Health %v want <= %v", s.label, hp2, beforeMove)
			}

			// Water extinguish — also move-gated: clearFire drops the
			// countdown to min(0, it) = 0.
			sendCommand(t, pw, "tp @s 14.5 -61.5 14.5")
			var wb protocol.Buffer
			wb.Double(14.7)
			wb.Double(-61.5)
			wb.Double(14.5)
			wb.Byte(1)
			_ = pw.WritePacket(server.IDPlayMovePosSB, wb.Bytes())
			time.Sleep(400 * time.Millisecond)
			dump = dataGetAll(t, sink, pw, "@s")
			fire, _ = snbtInt(t, dump, "Fire")
			if fire > 0 {
				t.Fatalf("%s: water-moved Fire=%d want <=0", s.label, fire)
			}

			// Creative: lavaIgnite's fireImmune check is per-type —
			// the player type isn't immune, so the ignite lands —
			// while the four-damage hurt is invulnerability-gated.
			sendCommand(t, pw, "tp @s 30.5 -50.3 30.5")
			sendCommand(t, pw, "gamemode creative @s")
			hpBefore := hp2
			sendCommand(t, pw, "tp @s 24.5 -61.5 24.5")
			var cb protocol.Buffer
			cb.Double(24.7)
			cb.Double(-61.5)
			cb.Double(24.5)
			cb.Byte(1)
			_ = pw.WritePacket(server.IDPlayMovePosSB, cb.Bytes())
			time.Sleep(400 * time.Millisecond)
			dump = dataGetAll(t, sink, pw, "@s")
			hp, _ = snbtFloat(t, dump, "Health")
			fire, _ = snbtInt(t, dump, "Fire")
			if hp < hpBefore {
				t.Fatalf("%s: creative-in-lava Health=%v want >= %v (hurt is invuln-gated)", s.label, hp, hpBefore)
			}
			if fire <= 0 {
				t.Fatalf("%s: creative-in-lava Fire=%d want >0 (ignite is not invuln-gated)", s.label, fire)
			}

			// in_wall suffocation: Entity.isInWall probes a
			// width*0.8-by-1e-6 plane at the eye position for a non-air
			// block that isSuffocating. Stone at eye level hurts one
			// heart per grace-pacing; leaves — not suffocating — do
			// nothing. Fire is cleared through the water column first
			// so the burn can't confound the reads.
			sendCommand(t, pw, "gamemode survival @s")
			sendCommand(t, pw, "tp @s 14.5 -61.5 14.5")
			var eb protocol.Buffer
			eb.Double(14.7)
			eb.Double(-61.5)
			eb.Double(14.5)
			eb.Byte(1)
			_ = pw.WritePacket(server.IDPlayMovePosSB, eb.Bytes())
			sendCommand(t, pw, "effect give @s minecraft:instant_health 10 10 true")
			sendCommand(t, pw, "fill 26 -60 26 26 -56 26 minecraft:stone")
			sendCommand(t, pw, "tp @s 26.5 -59.0 26.5")
			_ = sink.take() // drop earlier traffic
			sendCommand(t, pw, "tick sprint 25")
			time.Sleep(400 * time.Millisecond)
			wallHits := 0
			for _, line := range sink.take() {
				if strings.HasPrefix(line, "0x19:") {
					r := protocol.NewPacketReader([]byte(line[5:]))
					vid, _ := r.VarInt()
					ty, _ := r.VarInt()
					if ty == 22 { // damage_type in_wall
						wallHits++
					}
					_ = vid
				}
			}
			dump = dataGetAll(t, sink, pw, "@s")
			hp, _ = snbtFloat(t, dump, "Health")
			t.Logf("%s buried-25: Health=%v wallHits=%d", s.label, hp, wallHits)
			if wallHits == 0 {
				t.Fatalf("%s: buried-in-stone saw no in_wall damage events", s.label)
			}
			if hp >= 20 {
				t.Fatalf("%s: buried-in-stone Health=%v want <20", s.label, hp)
			}

			// Leaves cover the eye cell but are not suffocating.
			sendCommand(t, pw, "effect give @s minecraft:instant_health 10 10 true")
			sendCommand(t, pw, "fill 28 -60 28 28 -56 28 minecraft:oak_leaves")
			sendCommand(t, pw, "tp @s 28.5 -59.0 28.5")
			_ = sink.take()
			sendCommand(t, pw, "tick sprint 25")
			time.Sleep(400 * time.Millisecond)
			dump = dataGetAll(t, sink, pw, "@s")
			hp2, _ = snbtFloat(t, dump, "Health")
			if hp2 < 19.5 {
				t.Fatalf("%s: in-leaves Health=%v want ~20 (healed); leaves are not suffocating", s.label, hp2)
			}

			// Powder snow: the inside effect accrues TicksFrozen up to
			// the 140-tick requirement (the record's own inside pass);
			// LivingEntity.baseTick then hurts a fully frozen living
			// entity one heart every forty ticks.
			sendCommand(t, pw, "effect give @s minecraft:instant_health 10 10 true")
			sendCommand(t, pw, "fill 33 -60 33 33 -56 33 minecraft:powder_snow")
			sendCommand(t, pw, "tp @s 33.5 -59.0 33.5")
			_ = sink.take()
			// Vanilla paces the freeze hurt on tickCount%40; the
			// sprint must leave a frozen window wider than forty
			// ticks (accrual caps at 140) so a boundary always lands.
			// The wait has to outlast mc-go's slower sprint rate —
			// the hits near the window's tail emit late, and polling
			// the clock through the sink would eat the events.
			sendCommand(t, pw, "tick sprint 200")
			time.Sleep(2500 * time.Millisecond)
			freezeHits := 0
			frostWire := false
			for _, line := range sink.take() {
				if strings.HasPrefix(line, "0x19:") {
					r := protocol.NewPacketReader([]byte(line[5:]))
					vid, _ := r.VarInt()
					ty, _ := r.VarInt()
					t.Logf("%s snow damage_event victim=%d type=%d", s.label, vid, ty)
					if ty == 17 { // damage_type freeze
						freezeHits++
					}
					_ = vid
				}
				// LivingEntity.baseTick's tryAddFrost: while frozen the
				// server emits update_attributes carrying the transient
				// minecraft:powder_snow MOVEMENT_SPEED modifier (-0.05 *
				// percentFrozen). It is transient — absent from the save
				// — so the wire emit is the observable pin.
				if strings.HasPrefix(line, "0x83:") && strings.Contains(line, "minecraft:powder_snow") {
					frostWire = true
				}
			}
			dump = dataGetAll(t, sink, pw, "@s")
			hp, _ = snbtFloat(t, dump, "Health")
			tf, _ := snbtInt(t, dump, "TicksFrozen")
			t.Logf("%s powder-170: Health=%v freezeHits=%d TicksFrozen=%d", s.label, hp, freezeHits, tf)
			if freezeHits == 0 {
				t.Fatalf("%s: powder-snow saw no freeze damage events", s.label)
			}
			if hp >= 20 {
				t.Fatalf("%s: powder-snow Health=%v want <20", s.label, hp)
			}
			if !frostWire {
				t.Fatalf("%s: frozen player saw no update_attributes with the powder_snow modifier", s.label)
			}
			sendCommand(t, pw, "tp @s 30.5 -50.3 30.5")
		})
	}
}
