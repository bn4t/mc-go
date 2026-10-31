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

// TestPlayerFluidParity preserves the player fluid outcomes with applied-state
// receipts and actual player-frame completion. World age is frozen so fluid
// spread and unrelated world steps cannot alter the apparatus. Capture packets
// through state queries rather than dropping events during later drains.
func TestPlayerFluidParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"gamemode": "survival", "view-distance": "2", "simulation-distance": "2"},
		map[string]string{"ops.json": opsJSONFor("PFP")}, nil)
	defer targets.cleanup()

	for _, s := range []struct{ label, addr string }{
		{"vanilla", targets.vanilla}, {"mcgo", targets.mcgo},
	} {
		t.Run(s.label, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, s.addr, "PFP")
			defer c.Close()
			awaitPlayReadyBlackbox(t, s.addr, c, pr, pw)
			sink := newChatSinkTP(pr, pw)
			sink.cap = 1 << 20
			var captured []string
			collect := func(marker string) []string {
				var lines []string
				deadline := time.Now().Add(15 * time.Second)
				for !structureSaidBB(lines, marker) && time.Now().Before(deadline) {
					batch := sink.take()
					lines = append(lines, batch...)
					captured = append(captured, batch...)
					time.Sleep(10 * time.Millisecond)
				}
				if !structureSaidBB(lines, marker) {
					t.Fatal("player fluid receipt absent", marker)
				}
				return lines
			}
			expect := func(condition, marker string) {
				sendCommand(t, pw, condition+" run say "+marker)
				sendCommand(t, pw, "say "+marker+"END")
				if !structureSaidBB(collect(marker+"END"), marker) {
					t.Fatal("player fluid setup rejected", marker)
				}
			}
			sequence := 0
			advance := func(ticks int) {
				// Stay inside the shared receipt helper's fixed15-second budget.
				for ticks > 0 {
					part := min(ticks, 150)
					sequence++
					waitPlayerTicksBB(t, pw, collect, fmt.Sprintf("PFPTICKS%d", sequence), part)
					ticks -= part
				}
			}
			teleport := func(x, y, z float64) {
				sequence++
				sendCommand(t, pw, fmt.Sprintf("tp @s %.3f %.3f %.3f", x, y, z))
				expect(fmt.Sprintf("execute if data entity @s {Pos:[%gd,%gd,%gd]}", x, y, z), fmt.Sprintf("PFPPOS%d", sequence))
			}
			dumpPlayer := func() string {
				sequence++
				marker := fmt.Sprintf("PFPDUMP%d", sequence)
				sendCommand(t, pw, "data get entity @s")
				sendCommand(t, pw, "say "+marker)
				for _, line := range collect(marker) {
					if strings.HasPrefix(line, "0x79:") && strings.Contains(line, "commands.data.entity.query") {
						return snbtFromChat(line)
					}
				}
				t.Fatal("player fluid data body absent")
				return ""
			}
			heal := func(health int) {
				sendCommand(t, pw, fmt.Sprintf("attribute @s max_health base set %d", health))
				sendCommand(t, pw, "effect give @s minecraft:instant_health 1 10 true")
				advance(3)
				sequence++
				expect(fmt.Sprintf(`execute if data entity @s {Health:%d.0f} unless data entity @s {active_effects:[{id:"minecraft:instant_health"}]}`, health), fmt.Sprintf("PFPHEALED%d", sequence))
			}
			awaitPosition := func(pos, marker string) {
				deadline := time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) {
					sendCommand(t, pw, "execute if data entity @s {Pos:["+pos+"]} run say "+marker)
					sendCommand(t, pw, "say "+marker+"POLL")
					if structureSaidBB(collect(marker+"POLL"), marker) {
						return
					}
				}
				t.Logf("unaccepted movement player state %s", dumpPlayer())
				if s.label == "vanilla" {
					if body, err := os.ReadFile(filepath.Join(vanillaDirForDebug, "logs", "latest.log")); err == nil {
						lines := strings.Split(string(body), "\n")
						if len(lines) > 25 {
							lines = lines[len(lines)-25:]
						}
						t.Logf("native movement diagnostics\n%s", strings.Join(lines, "\n"))
					}
				}
				t.Fatal("single movement not accepted", marker)
			}
			clearCaptured := func() { captured = nil }
			takeCaptured := func() []string { return captured }
			setupPlayerTickBarrierBB(t, pw)
			sendCommand(t, pw, "scoreboard objectives add PFPFire dummy")
			for _, cmd := range []string{"gamemode survival @s", "difficulty normal", "gamerule minecraft:spawn_mobs false", "gamerule minecraft:natural_health_regeneration false", "tick freeze", "kill @e[type=!minecraft:player]", "forceload add 0 0", "forceload add 16 16", "forceload add 32 32"} {
				sendCommand(t, pw, cmd)
			}
			for index, pos := range []string{"10 -61 10", "24 -61 24", "33 -59 33"} {
				loaded := false
				deadline := time.Now().Add(20 * time.Second)
				marker := fmt.Sprintf("PFPLOADED%d", index)
				for !loaded && time.Now().Before(deadline) {
					sendCommand(t, pw, "execute if loaded "+pos+" run say "+marker)
					sendCommand(t, pw, "say "+marker+"POLL")
					loaded = structureSaidBB(collect(marker+"POLL"), marker)
				}
				if !loaded {
					t.Fatal("player fluid chunk absent", pos)
				}
			}

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
			for _, pos := range []string{"10 -62 10", "10 -61 10", "10 -60 10", "10 -59 10", "14 -62 14", "14 -61 14", "14 -60 14", "14 -59 14"} {
				sequence++
				expect("execute if block "+pos+" minecraft:water", fmt.Sprintf("PFPWATER%d", sequence))
			}
			expect("execute if block 30 -51 30 minecraft:stone", "PFPPAD")
			expect(`execute if data entity @s {Air:300s,Health:20.0f,playerGameType:0}`, "PFPAIRSTART")
			teleport(10.5, -61.5, 10.5)

			advance(40)
			dump := dumpPlayer()
			air, ok := snbtInt(t, dump, "Air")
			if !ok {
				t.Fatalf("%s: no Air in %s", s.label, dump)
			}
			// Forty completed player frames plus small setup/receipt skew
			// leave the original bounded air outcome near250.
			if air < 235 || air > 275 {
				t.Fatalf("%s: after 40t underwater Air=%d want ~250", s.label, air)
			}

			// Past the -20 reset: air clamps to zero, keeps draining,
			// and the two-damage drown hits start landing.
			advance(330)
			t.Logf("%s post-drain: %s", s.label, sink.health())
			dump = dumpPlayer()
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
			teleport(30.5, -50.0, 30.5)
			advance(90)
			t.Logf("%s post-refill: %s", s.label, sink.health())
			dump = dumpPlayer()
			air, _ = snbtInt(t, dump, "Air")
			if air != 300 {
				t.Fatalf("%s: refilled Air=%d want 300 dump=%s", s.label, air, dump)
			}

			// Lava leaves air full, ignites and hurts. A larger pool keeps the
			// actor alive across a real25-frame window and the following move;
			// later absolute-health controls restore max health20.
			heal(100)
			sendCommand(t, pw, "fill 24 -62 24 24 -59 24 minecraft:lava")
			expect("execute if block 24 -62 24 minecraft:lava if block 24 -61 24 minecraft:lava if block 24 -60 24 minecraft:lava if block 24 -59 24 minecraft:lava", "PFPLAVA")
			teleport(24.5, -61.5, 24.5)
			advance(6)
			dump = dumpPlayer()
			air, _ = snbtInt(t, dump, "Air")
			hp, _ = snbtFloat(t, dump, "Health")
			fire, _ := snbtInt(t, dump, "Fire")
			if air != 300 {
				t.Fatalf("%s: lava Air=%d want 300", s.label, air)
			}
			if fire <= 0 {
				t.Fatalf("%s: settled lava Fire=%d want >0 after six player frames", s.label, fire)
			}
			hpLava := hp
			// Preserve the stationary damage direction and positive-life checks;
			// this fixture does not infer exact lava cadence from its window.
			clearCaptured() // drop earlier traffic
			advance(25)
			for _, line := range takeCaptured() {
				if strings.HasPrefix(line, "0x19:") {
					r := protocol.NewPacketReader([]byte(line[5:]))
					vid, _ := r.VarInt()
					ty, _ := r.VarInt()
					t.Logf("%s damage_event victim=%d type=%d", s.label, vid, ty)
				}
			}
			dump = dumpPlayer()
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
			if err := pw.WritePacket(server.IDPlayMovePosSB, mb.Bytes()); err != nil {
				t.Fatal(err)
			}
			awaitPosition("24.7d,-61.5d,24.5d", "PFPLAVAMOVED")
			advance(3)
			dump = dumpPlayer()
			hp2, _ = snbtFloat(t, dump, "Health")
			fire, _ = snbtInt(t, dump, "Fire")
			t.Logf("%s move-in-lava: Fire=%d Health=%v", s.label, fire, hp2)
			if fire <= 0 {
				t.Fatalf("%s: move-in-lava Fire=%d want >0", s.label, fire)
			}
			if hp2 > beforeMove {
				t.Fatalf("%s: move-in-lava Health %v want <= %v", s.label, hp2, beforeMove)
			}

			// Water extinguishes the positive burn; retain one actual accepted
			// displacement and the nonpositive fire assertion.
			teleport(14.5, -61.5, 14.5)
			var wb protocol.Buffer
			wb.Double(14.7)
			wb.Double(-61.5)
			wb.Double(14.5)
			wb.Byte(1)
			if err := pw.WritePacket(server.IDPlayMovePosSB, wb.Bytes()); err != nil {
				t.Fatal(err)
			}
			awaitPosition("14.7d,-61.5d,14.5d", "PFPWATERMOVED")
			advance(3)
			dump = dumpPlayer()
			fire, _ = snbtInt(t, dump, "Fire")
			if fire > 0 {
				t.Fatalf("%s: water-moved Fire=%d want <=0", s.label, fire)
			}

			// Creative: lavaIgnite's fireImmune check is per-type —
			// the player type isn't immune, so the ignite lands —
			// while the four-damage hurt is invulnerability-gated.
			teleport(30.5, -50.0, 30.5)
			sendCommand(t, pw, "gamemode creative @s")
			expect(`execute if data entity @s {playerGameType:1}`, "PFPCREATIVEMODE")
			advance(3)
			// Sample after leaving water and admitting creative mode. The earlier
			// lava sample precedes survival burn frames during the water transit.
			hpBefore, ok := snbtFloat(t, dumpPlayer(), "Health")
			if !ok {
				t.Fatal("creative baseline Health absent")
			}
			teleport(24.5, -61.5, 24.5)
			var cb protocol.Buffer
			cb.Double(24.7)
			cb.Double(-61.5)
			cb.Double(24.5)
			cb.Byte(1)
			if err := pw.WritePacket(server.IDPlayMovePosSB, cb.Bytes()); err != nil {
				t.Fatal(err)
			}
			awaitPosition("24.7d,-61.5d,24.5d", "PFPCREATIVEMOVED")
			advance(3)
			dump = dumpPlayer()
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
			// point per grace-pacing; leaves — not suffocating — do
			// nothing. Fire is cleared through the water column first
			// so the burn can't confound the reads.
			sendCommand(t, pw, "gamemode survival @s")
			teleport(14.5, -61.5, 14.5)
			var eb protocol.Buffer
			eb.Double(14.7)
			eb.Double(-61.5)
			eb.Double(14.5)
			eb.Byte(1)
			if err := pw.WritePacket(server.IDPlayMovePosSB, eb.Bytes()); err != nil {
				t.Fatal(err)
			}
			awaitPosition("14.7d,-61.5d,14.5d", "PFPRESETMOVED")
			advance(3)
			heal(20)
			sendCommand(t, pw, "fill 26 -60 26 26 -56 26 minecraft:stone")
			expect("execute if block 26 -60 26 minecraft:stone if block 26 -58 26 minecraft:stone if block 26 -56 26 minecraft:stone", "PFPSTONE")
			sendCommand(t, pw, "execute store result score @s PFPFire run data get entity @s Fire")
			expect(`execute if score @s PFPFire matches ..0`, "PFPNOBURN")
			teleport(26.5, -59.0, 26.5)
			clearCaptured() // drop earlier traffic
			advance(25)
			dump = dumpPlayer()
			wallHits := 0
			for _, line := range takeCaptured() {
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
			hp, _ = snbtFloat(t, dump, "Health")
			t.Logf("%s buried-25: Health=%v wallHits=%d", s.label, hp, wallHits)
			if wallHits == 0 {
				t.Fatalf("%s: buried-in-stone saw no in_wall damage events", s.label)
			}
			if hp >= 20 {
				t.Fatalf("%s: buried-in-stone Health=%v want <20", s.label, hp)
			}

			// Leaves cover the eye cell but are not suffocating. Establish the
			// health baseline outside stone so transit frames cannot suffocate.
			teleport(30.5, -50.0, 30.5)
			advance(3)
			heal(20)
			sendCommand(t, pw, "fill 28 -60 28 28 -56 28 minecraft:oak_leaves")
			expect("execute if block 28 -60 28 minecraft:oak_leaves if block 28 -58 28 minecraft:oak_leaves if block 28 -56 28 minecraft:oak_leaves", "PFPLEAVES")
			teleport(28.5, -59.0, 28.5)
			clearCaptured()
			advance(25)
			dump = dumpPlayer()
			hp2, _ = snbtFloat(t, dump, "Health")
			if hp2 < 19.5 {
				t.Fatalf("%s: in-leaves Health=%v want ~20 (healed); leaves are not suffocating", s.label, hp2)
			}

			// Powder snow: the inside effect accrues TicksFrozen up to
			// the 140-tick requirement (the record's own inside pass);
			// A fully frozen eligible player takes one point every forty
			// actual player ticks.
			heal(20)
			sendCommand(t, pw, "fill 33 -60 33 33 -56 33 minecraft:powder_snow")
			expect("execute if block 33 -60 33 minecraft:powder_snow if block 33 -58 33 minecraft:powder_snow if block 33 -56 33 minecraft:powder_snow", "PFPSNOW")
			teleport(33.5, -59.0, 33.5)
			clearCaptured()
			// A completed200-player-frame exposure leaves more than40 fully
			// frozen frames; capture through the following data receipt.
			advance(200)
			dump = dumpPlayer()
			freezeHits := 0
			frostWire := false
			for _, line := range takeCaptured() {
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
			hp, _ = snbtFloat(t, dump, "Health")
			tf, _ := snbtInt(t, dump, "TicksFrozen")
			t.Logf("%s powder-200: Health=%v freezeHits=%d TicksFrozen=%d", s.label, hp, freezeHits, tf)
			if freezeHits == 0 {
				t.Fatalf("%s: powder-snow saw no freeze damage events", s.label)
			}
			if hp >= 20 {
				t.Fatalf("%s: powder-snow Health=%v want <20", s.label, hp)
			}
			if !frostWire {
				t.Fatalf("%s: frozen player saw no update_attributes with the powder_snow modifier", s.label)
			}
			teleport(30.5, -50.0, 30.5)
		})
	}
}
