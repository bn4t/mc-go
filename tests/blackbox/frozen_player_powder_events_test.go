package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// Capture every event through the completion receipts. A fixed sleep followed
// by a destructive drain can miss a late event and still observe its health loss.
func TestFrozenPlayerPowderEventCadenceParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2"}, map[string]string{"ops.json": opsJSONFor("FrozenPowder")}, nil)
	defer targets.cleanup()
	nativeKind := int32(-1)
	for _, leg := range []struct{ name, addr string }{{"native", targets.vanilla}, {"go", targets.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			conn, pr, pw := joinPlayBlackbox(t, leg.addr, "FrozenPowder")
			defer conn.Close()
			awaitPlayReadyBlackbox(t, leg.addr, conn, pr, pw)
			sink := newChatSinkTP(pr, pw)
			recording := false
			var kinds []int32
			var health []float32
			fullWire, thawWire, thawing := false, false, false
			collect := func(marker string) []string {
				var lines []string
				deadline := time.Now().Add(12 * time.Second)
				for !structureSaidBB(lines, marker) && time.Now().Before(deadline) {
					lines = append(lines, sink.take()...)
					time.Sleep(10 * time.Millisecond)
				}
				if !structureSaidBB(lines, marker) {
					t.Fatal("powder receipt absent", marker)
				}
				for _, line := range lines {
					prefix, body, ok := strings.Cut(line, ":")
					if !ok {
						continue
					}
					if prefix == fmt.Sprintf("%#x", server.IDPlayEntityMetadata) {
						r := protocol.NewPacketReader([]byte(body))
						if _, err := r.VarInt(); err != nil {
							continue
						}
						for {
							index, err := r.Byte()
							if err != nil || index == 0xff {
								break
							}
							serializer, err := r.VarInt()
							if err != nil {
								break
							}
							if index == 7 && serializer == 1 {
								value, err := r.VarInt()
								if err != nil {
									break
								}
								fullWire = fullWire || value == 140
								thawWire = thawWire || (thawing && value > 0 && value < 140)
							} else if serializer > 255 || !skipMetaVal(r, byte(serializer)) {
								break
							}
						}
					}
					if recording && prefix == fmt.Sprintf("%#x", server.IDPlaySetHealth) {
						r := protocol.NewPacketReader([]byte(body))
						h, e0 := r.Float()
						_, e1 := r.VarInt()
						_, e2 := r.Float()
						if e0 != nil || e1 != nil || e2 != nil || r.Remaining() != 0 {
							t.Error("malformed powder health packet")
						} else {
							health = append(health, h)
						}
					}
				}
				if recording {
					for _, line := range lines {
						prefix, body, ok := strings.Cut(line, ":")
						if !ok || prefix != fmt.Sprintf("%#x", server.IDPlayDamageEvent) {
							continue
						}
						r := protocol.NewPacketReader([]byte(body))
						victim, e0 := r.VarInt()
						kind, e1 := r.VarInt()
						cause, e2 := r.VarInt()
						direct, e3 := r.VarInt()
						hasPos, e4 := r.Bool()
						if e0 != nil || e1 != nil || e2 != nil || e3 != nil || e4 != nil || hasPos || r.Remaining() != 0 || victim <= 0 || cause != 0 || direct != 0 {
							t.Errorf("unexpected environmental damage packet victim%d kind%d cause%d direct%d hasPos%v", victim, kind, cause, direct, hasPos)
							continue
						}
						kinds = append(kinds, kind)
					}
				}
				return lines
			}
			expect := func(condition, marker string) {
				sendCommand(t, pw, condition+" run say "+marker)
				sendCommand(t, pw, "say "+marker+"END")
				if !structureSaidBB(collect(marker+"END"), marker) {
					t.Fatal("powder setup rejected", marker)
				}
			}
			setupPlayerTickBarrierBB(t, pw)
			for _, cmd := range []string{"gamemode survival @s", "difficulty normal", "gamerule minecraft:spawn_mobs false", "gamerule minecraft:natural_health_regeneration false", "gamerule minecraft:freeze_damage true", "effect clear @s", "tick freeze", "attribute @s max_health base set 1000", "effect give @s instant_health 1 10 true", "forceload add 32 32", "scoreboard objectives add PowHPa dummy", "scoreboard objectives add PowHPb dummy", "scoreboard objectives add PowTickA dummy", "scoreboard objectives add PowElapsed dummy", "scoreboard objectives add PowFrameDiff dummy", "scoreboard objectives add PowHPLoss dummy", "scoreboard objectives add PowHPSkew dummy", "scoreboard objectives add PowWorldA dummy", "scoreboard objectives add PowWorldB dummy", "scoreboard objectives add PowThawA dummy", "scoreboard objectives add PowThawB dummy", "scoreboard objectives add PowThawTick dummy", "scoreboard objectives add PowThawSpan dummy", "scoreboard objectives add PowThawLoss dummy", "scoreboard objectives add PowThawDiff dummy"} {
				sendCommand(t, pw, cmd)
			}
			waitPlayerTicksBB(t, pw, collect, "POWDERHEALED", 3)
			expect(`execute if data entity @s {Health:1000.0f} unless data entity @s {active_effects:[{id:"minecraft:instant_health"}]}`, "POWDERHEALTH")
			loaded := false
			deadline := time.Now().Add(15 * time.Second)
			for !loaded && time.Now().Before(deadline) {
				sendCommand(t, pw, "execute if loaded 33 -59 33 run say POWDERLOADED")
				sendCommand(t, pw, "say POWDERLOADPOLL")
				loaded = structureSaidBB(collect("POWDERLOADPOLL"), "POWDERLOADED")
			}
			if !loaded {
				t.Fatal("powder column chunk not loaded")
			}
			sendCommand(t, pw, "fill 33 -60 33 34 -56 34 minecraft:powder_snow")
			expect(`execute if block 33 -59 33 minecraft:powder_snow if block 34 -58 34 minecraft:powder_snow`, "POWDERBLOCKS")
			sendCommand(t, pw, "tp @s 33.5 -59.0 33.5")
			sendCommand(t, pw, "say POWDERTP")
			collect("POWDERTP")
			// Apply a real displacement, then observe acceptance without retrying it.
			var move protocol.Buffer
			move.Double(33.75)
			move.Double(-59)
			move.Double(33.5)
			move.Byte(0)
			if err := pw.WritePacket(server.IDPlayMovePosSB, move.Bytes()); err != nil {
				t.Fatal(err)
			}
			moved := false
			deadline = time.Now().Add(5 * time.Second)
			for !moved && time.Now().Before(deadline) {
				sendCommand(t, pw, `execute if data entity @s {Pos:[33.75d]} run say POWDERMOVED`)
				sendCommand(t, pw, "say POWDERMOVEPOLL")
				moved = structureSaidBB(collect("POWDERMOVEPOLL"), "POWDERMOVED")
			}
			if !moved {
				t.Fatal("single displacement not accepted")
			}
			waitPlayerTicksBB(t, pw, collect, "POWDEREXPOSURE", 170)
			expect(`execute if data entity @s {TicksFrozen:140}`, "POWDERFULL")
			if !fullWire {
				waitPlayerTicksBB(t, pw, collect, "POWDERMETADATA", 2)
			}
			if !fullWire {
				t.Fatal("full freeze metadata not observed")
			}
			recording = true
			sendCommand(t, pw, "execute store result score @s PowHPa run data get entity @s Health 1000")
			sendCommand(t, pw, "scoreboard players operation @s PowTickA = @s BBPlayerTick")
			sendCommand(t, pw, "execute store result score @s PowWorldA run time query gametime")
			sendCommand(t, pw, "say POWDERWINDOWSTART")
			collect("POWDERWINDOWSTART")
			waitPlayerTicksBB(t, pw, collect, "POWDERWINDOW", 80)
			sendCommand(t, pw, "execute store result score @s PowHPb run data get entity @s Health 1000")
			sendCommand(t, pw, "scoreboard players operation @s PowElapsed = @s BBPlayerTick")
			sendCommand(t, pw, "scoreboard players operation @s PowElapsed -= @s PowTickA")
			sendCommand(t, pw, "execute store result score @s PowWorldB run time query gametime")
			sendCommand(t, pw, "say POWDERWINDOWEND")
			collect("POWDERWINDOWEND")
			recording = false
			if len(kinds) == 0 {
				t.Fatal("fully frozen applied setup emitted no damage events")
			}
			if leg.name == "native" {
				nativeKind = kinds[0]
			}
			for _, kind := range kinds {
				if kind != nativeKind {
					t.Errorf("powder damage type%d want native%d", kind, nativeKind)
				}
			}
			// One event per40 body ticks, with one event of phase/sample skew.
			sendCommand(t, pw, "scoreboard players operation @s PowFrameDiff = @s PowElapsed")
			sendCommand(t, pw, fmt.Sprintf("scoreboard players remove @s PowFrameDiff %d", len(kinds)*40))
			sendCommand(t, pw, "scoreboard players operation @s PowHPLoss = @s PowHPa")
			sendCommand(t, pw, "scoreboard players operation @s PowHPLoss -= @s PowHPb")
			sendCommand(t, pw, "scoreboard players operation @s PowHPSkew = @s PowHPLoss")
			sendCommand(t, pw, fmt.Sprintf("scoreboard players remove @s PowHPSkew %d", len(kinds)*1000))
			sendCommand(t, pw, `execute if score @s PowElapsed matches 80..140 if score @s PowFrameDiff matches -45..45 if score @s PowHPLoss matches 1.. if score @s PowHPSkew matches -2000..2000 if score @s PowWorldA = @s PowWorldB if data entity @s {TicksFrozen:140} run say POWDERRATE`)
			sendCommand(t, pw, "say POWDEREND")
			if !structureSaidBB(collect("POWDEREND"), "POWDERRATE") {
				t.Errorf("damage events%d type%d should follow actual player ticks at one per40 with frozen world and matching health loss", len(kinds), nativeKind)
			}
			steps := 0
			for i := 1; i < len(health); i++ {
				delta := health[i-1] - health[i]
				if delta == 1 {
					steps++
				} else if delta != 0 {
					t.Errorf("powder health delta%g want1 or duplicate0", delta)
				}
			}
			if steps == 0 {
				t.Errorf("no consecutive one-HP freeze step in%v", health)
			}
			t.Logf("powder damage events=%d type=%d health=%v", len(kinds), kinds[0], health)
			// The rule suppresses environmental and explicit freeze damage,
			// while leaving the applied full-exposure state intact.
			sendCommand(t, pw, "gamerule minecraft:freeze_damage false")
			sendCommand(t, pw, "say POWDERRULESET")
			collect("POWDERRULESET")
			kinds, health = nil, nil
			recording = true
			sendCommand(t, pw, "execute store result score @s PowHPa run data get entity @s Health 1000")
			sendCommand(t, pw, "damage @s 7 minecraft:freeze")
			waitPlayerTicksBB(t, pw, collect, "POWDERRULEWINDOW", 80)
			sendCommand(t, pw, "execute store result score @s PowHPb run data get entity @s Health 1000")
			sendCommand(t, pw, `execute if score @s PowHPa = @s PowHPb if data entity @s {TicksFrozen:140} run say POWDERRULEIMMUNE`)
			sendCommand(t, pw, "say POWDERRULEEND")
			ruleLines := collect("POWDERRULEEND")
			recording = false
			if !structureSaidBB(ruleLines, "POWDERRULEIMMUNE") || len(kinds) != 0 {
				t.Errorf("freeze_damage=false should suppress explicit7 and environmental damage; events%v", kinds)
			}
			thaw := func(marker string) {
				sendCommand(t, pw, "execute store result score @s PowThawA run data get entity @s TicksFrozen")
				sendCommand(t, pw, "scoreboard players operation @s PowThawTick = @s BBPlayerTick")
				waitPlayerTicksBB(t, pw, collect, marker+"TICKS", 10)
				sendCommand(t, pw, "execute store result score @s PowThawB run data get entity @s TicksFrozen")
				sendCommand(t, pw, "scoreboard players operation @s PowThawSpan = @s BBPlayerTick")
				sendCommand(t, pw, "scoreboard players operation @s PowThawSpan -= @s PowThawTick")
				sendCommand(t, pw, "scoreboard players operation @s PowThawLoss = @s PowThawA")
				sendCommand(t, pw, "scoreboard players operation @s PowThawLoss -= @s PowThawB")
				sendCommand(t, pw, "scoreboard players operation @s PowThawDiff = @s PowThawLoss")
				sendCommand(t, pw, "scoreboard players operation @s PowThawDiff -= @s PowThawSpan")
				sendCommand(t, pw, "scoreboard players operation @s PowThawDiff -= @s PowThawSpan")
				expect(`execute if score @s PowThawSpan matches 10..30 if score @s PowThawB matches 1..139 if score @s PowThawDiff matches -6..6`, marker)
			}
			thawing = true
			sendCommand(t, pw, "tp @s 6.5 -60.0 6.5")
			sendCommand(t, pw, "say POWDERDRY")
			collect("POWDERDRY")
			thaw("POWDERTHAW")
			if !thawWire {
				t.Error("thaw metadata not observed")
			}
			sendCommand(t, pw, "item replace entity @s armor.feet with minecraft:leather_boots")
			expect(`execute if items entity @s armor.feet minecraft:leather_boots`, "POWDERBOOTS")
			sendCommand(t, pw, "tp @s 33.5 -59.0 33.5")
			sendCommand(t, pw, "say POWDERPROTECTED")
			collect("POWDERPROTECTED")
			thaw("POWDERARMORTHAW")
			sendCommand(t, pw, "item replace entity @s armor.feet with minecraft:air")
			expect(`execute unless items entity @s armor.feet minecraft:leather_boots if data entity @s {Pos:[33.5d]}`, "POWDERUNPROTECTED")
			sendCommand(t, pw, "execute store result score @s PowThawA run data get entity @s TicksFrozen")
			sendCommand(t, pw, "scoreboard players operation @s PowThawTick = @s BBPlayerTick")
			waitPlayerTicksBB(t, pw, collect, "POWDERREFREEZE", 10)
			sendCommand(t, pw, "execute store result score @s PowThawB run data get entity @s TicksFrozen")
			sendCommand(t, pw, "scoreboard players operation @s PowThawSpan = @s BBPlayerTick")
			sendCommand(t, pw, "scoreboard players operation @s PowThawSpan -= @s PowThawTick")
			sendCommand(t, pw, "scoreboard players operation @s PowThawDiff = @s PowThawB")
			sendCommand(t, pw, "scoreboard players operation @s PowThawDiff -= @s PowThawA")
			sendCommand(t, pw, "scoreboard players operation @s PowThawDiff -= @s PowThawSpan")
			sendCommand(t, pw, "scoreboard players operation @s PowThawDiff -= @s PowThawSpan")
			for _, field := range []string{"PowThawA", "PowThawB", "PowThawSpan", "PowThawDiff"} {
				sendCommand(t, pw, "scoreboard players get @s "+field)
			}
			sendCommand(t, pw, "data get entity @s Pos")
			sendCommand(t, pw, "say POWDERREFREEZEDIAG")
			t.Logf("refreeze diagnostics %q", collect("POWDERREFREEZEDIAG"))
			expect(`execute if score @s PowThawSpan matches 10..30 if score @s PowThawB matches 1..139 if score @s PowThawDiff matches -3..3`, "POWDERREFREEZEGAIN")
			// A second frozen-world interval distinguishes a persistent rate from
			// the equipment transition itself. Keep this below the140 cap.
			measureGain := func(marker string) {
				sendCommand(t, pw, "execute store result score @s PowThawA run data get entity @s TicksFrozen")
				sendCommand(t, pw, "scoreboard players operation @s PowThawTick = @s BBPlayerTick")
				waitPlayerTicksBB(t, pw, collect, marker+"TICKS", 5)
				sendCommand(t, pw, "execute store result score @s PowThawB run data get entity @s TicksFrozen")
				sendCommand(t, pw, "scoreboard players operation @s PowThawSpan = @s BBPlayerTick")
				sendCommand(t, pw, "scoreboard players operation @s PowThawSpan -= @s PowThawTick")
				sendCommand(t, pw, "scoreboard players operation @s PowThawDiff = @s PowThawB")
				sendCommand(t, pw, "scoreboard players operation @s PowThawDiff -= @s PowThawA")
				sendCommand(t, pw, "scoreboard players operation @s PowThawDiff -= @s PowThawSpan")
				sendCommand(t, pw, "scoreboard players operation @s PowThawDiff -= @s PowThawSpan")
				for _, field := range []string{"PowThawA", "PowThawB", "PowThawSpan", "PowThawDiff"} {
					sendCommand(t, pw, "scoreboard players get @s "+field)
				}
				sendCommand(t, pw, "say "+marker+"DIAG")
				t.Logf("%s diagnostics %q", marker, collect(marker+"DIAG"))
				expect(`execute if score @s PowThawSpan matches 5..20 if score @s PowThawB matches 1..139 if score @s PowThawDiff matches -3..3`, marker)
			}
			sendCommand(t, pw, "tp @s 6.5 -60.0 6.5")
			waitPlayerTicksBB(t, pw, collect, "POWDERSECONDCOOL", 25)
			sendCommand(t, pw, "tp @s 33.5 -59.0 33.5")
			sendCommand(t, pw, "say POWDERSECONDCONTACT")
			collect("POWDERSECONDCONTACT")
			if err := pw.WritePacket(server.IDPlayMovePosSB, move.Bytes()); err != nil {
				t.Fatal(err)
			}
			waitPlayerTicksBB(t, pw, collect, "POWDERSECONDPREP", 3)
			expect(`execute if data entity @s {Pos:[33.75d]}`, "POWDERSECONDMOVED")
			measureGain("POWDERSECONDGAIN")
			sendCommand(t, pw, "tp @s 6.5 -60.0 6.5")
			waitPlayerTicksBB(t, pw, collect, "POWDERRESET", 80)
			expect(`execute unless data entity @s TicksFrozen`, "POWDERRESETZERO")
			sendCommand(t, pw, "tick unfreeze")
			sendCommand(t, pw, "tp @s 33.5 -59.0 33.5")
			sendCommand(t, pw, "say POWDERNORMALSTART")
			collect("POWDERNORMALSTART")
			if err := pw.WritePacket(server.IDPlayMovePosSB, move.Bytes()); err != nil {
				t.Fatal(err)
			}
			waitPlayerTicksBB(t, pw, collect, "POWDERNORMALCONTACT", 3)
			expect(`execute if data entity @s {Pos:[33.75d]}`, "POWDERNORMALMOVED")
			measureGain("POWDERNORMALGAIN")

		})
	}
}
