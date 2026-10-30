//go:build blackbox

package blackbox

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// Use delay follows admitted player ticks; remembered contacts follow level
// game time. Keep both clocks observable instead of assuming one advances the other.
func TestSpearUseAndContactClocksParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "survival", "allow-flight": "true"}, map[string]string{"ops.json": opsJSONFor("P")}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
		s.OpsRequired = true
		s.SetOpLevel("P", 4)
	})
	defer tg.cleanup()
	for _, leg := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		c, r, w := joinPlayBlackbox(t, leg.addr, "P")
		defer c.Close()
		sendClientInformation(t, w)
		sink := newChatSinkResolvedTP(r, w)
		qc, qr, qw := joinPlayBlackbox(t, leg.addr, "Q")
		defer qc.Close()
		sendClientInformation(t, qw)
		observer := newChatSinkResolvedTP(qr, qw)
		actorID := int32(-1)
		sourceActive := false
		serial := 0
		collect := func(marker string) []string {
			lines := waitForExactSayBB(t, sink, marker, 15*time.Second)
			for _, line := range observer.take() {
				prefix, body, ok := strings.Cut(line, ":")
				if !ok {
					continue
				}
				reader := protocol.NewPacketReader([]byte(body))
				if prefix == fmt.Sprintf("%#x", server.IDPlaySpawnEntity) {
					id, e := reader.VarInt()
					uuid, e2 := reader.UUID()
					if e == nil && e2 == nil && uuid == offlineUUIDBlackbox("P") {
						actorID = id
					}
				}
				if prefix == fmt.Sprintf("%#x", server.IDPlayEntityMetadata) {
					id, e := reader.VarInt()
					index, e2 := reader.Byte()
					kind, e3 := reader.VarInt()
					if e == nil && e2 == nil && e3 == nil && id == actorID && index == 8 && kind == 0 {
						flags, e := reader.Byte()
						if e == nil {
							sourceActive = flags&1 != 0
						}
					}
				}
			}
			return lines
		}
		barrier := func() []string {
			serial++
			marker := fmt.Sprintf("SPEARCLOCK_END_%d", serial)
			sendCommand(t, w, "say "+marker)
			return collect(marker)
		}
		command := func(cmd string) []string { sendCommand(t, w, cmd); return barrier() }
		witness := func(condition string) {
			deadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) {
				serial++
				marker := fmt.Sprintf("SPEARCLOCK_READY_%d", serial)
				if structureSaidBB(command("execute "+condition+" run say "+marker), marker) {
					return
				}
			}
			t.Fatal("applied-state witness", leg.name, condition)
		}

		read := func(target, path string) string {
			for _, line := range command("data get entity " + target + " " + path) {
				key, args, ok := componentArgs(line)
				if ok && key == "commands.data.entity.query" && len(args) == 2 {
					return mobKBComponentTextBB(args[1])
				}
			}
			t.Fatal("missing data", leg.name, target, path)
			return ""
		}
		health := func(target string) float64 {
			value := read(target, "Health")
			number, err := strconv.ParseFloat(strings.TrimSuffix(value, "f"), 64)
			if err != nil {
				t.Fatal(value, err)
			}
			return number
		}
		gametime := func() int64 {
			for _, line := range command("time query gametime") {
				key, args, ok := componentArgs(line)
				if ok && key == "commands.time.query.gametime" && len(args) == 1 {
					switch n := args[0].(type) {
					case int64:
						return n
					case int32:
						return int64(n)
					}
				}
			}
			t.Fatal("missing game time", leg.name)
			return 0
		}
		step := func(n int64) {
			before := gametime()
			command(fmt.Sprintf("tick step %d", n))
			deadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) {
				now := gametime()
				if now == before+n {
					return
				}
				if now > before+n {
					t.Fatal("simulation step overshoot", before, n, now)
				}
			}
			t.Fatal("simulation step incomplete")
		}
		release := func() {
			var b protocol.Buffer
			b.VarInt(5)
			b.Position(0, 0, 0)
			b.Byte(0)
			b.VarInt(0)
			if err := w.WritePacket(server.IDPlayPlayerActionSB, b.Bytes()); err != nil {
				t.Fatal(err)
			}
		}
		move := func(boat string, z float64) {
			var b protocol.Buffer
			b.Double(.5)
			b.Double(-60)
			b.Double(z)
			b.Float(0)
			b.Float(0)
			b.Bool(true)
			if err := w.WritePacket(server.IDPlayMoveVehicleSB, b.Bytes()); err != nil {
				t.Fatal(err)
			}
			witness(fmt.Sprintf("if data entity %s {Pos:[0.5d,-60d,%.17gd]}", boat, z))
		}
		endFrame := func() {
			if err := w.WritePacket(server.IDPlayClientTickEndSB, nil); err != nil {
				t.Fatal(err)
			}
			barrier()
		}
		mounted := func(vehicle string) bool {
			serial++
			marker := fmt.Sprintf("SPEARCLOCK_LINK_%d", serial)
			return structureSaidBB(command("execute if data entity "+vehicle+" Passengers run say "+marker), marker)
		}
		command("tick freeze")
		setupPlayerTickBarrierBB(t, w)
		barrier()
		command("scoreboard objectives add SpearUseLimit dummy")
		command("tp Q 30.5 -60 6.5")
		deadline := time.Now().Add(15 * time.Second)
		for actorID < 0 && time.Now().Before(deadline) {
			barrier()
		}
		if actorID < 0 {
			t.Fatal("observer did not track source", leg.name)
		}
		for index, empty := range []bool{false, true} {
			release()
			barrier()
			command("ride @s dismount")
			command("tp @e[tag=SPEAR_CLOCK_SCENE] 40 -60 40")
			command("tp @s .5 -60 .5 0 0")
			command("fill -2 -57 -2 2 -57 6 stone")
			boatTag, victimTag, mountTag := fmt.Sprintf("clock_actor_%d", index), fmt.Sprintf("clock_victim_%d", index), fmt.Sprintf("clock_mount_%d", index)
			boat, victim, mount := "@e[tag="+boatTag+",limit=1]", "@e[tag="+victimTag+",limit=1]", "@e[tag="+mountTag+",limit=1]"
			command(fmt.Sprintf("summon oak_boat .5 -60 .5 {Tags:[\"SPEAR_CLOCK_SCENE\",\"%s\"],NoGravity:1b}", boatTag))
			command("ride @s mount " + boat)
			command(fmt.Sprintf("summon villager .5 -59.5 4.5 {Tags:[\"SPEAR_CLOCK_SCENE\",\"%s\"],NoAI:1b,NoGravity:1b}", victimTag))
			command("attribute " + victim + " max_health base set 100")
			command("attribute " + victim + " knockback_resistance base set 1")
			command("data merge entity " + victim + " {Health:100f}")
			command(fmt.Sprintf("summon oak_boat .5 -60 4.5 {Tags:[\"SPEAR_CLOCK_SCENE\",\"%s\"],NoGravity:1b,Invulnerable:1b}", mountTag))
			command("ride " + victim + " mount " + mount)
			step(1)
			witness("if data entity " + mount + " Passengers")
			command("item replace entity @s weapon.mainhand with wooden_spear")
			witness("if items entity @s weapon.mainhand wooden_spear")
			waitPlayerTicksBB(t, w, collect, fmt.Sprintf("SpearGear%d", index), 40)
			if health(victim) != 100 {
				t.Fatal("health preparation", leg.name)
			}
			move(boat, 1.25)
			endFrame()
			if empty {
				endFrame()
			}
			command("scoreboard players operation @s SpearUseLimit = @s BBPlayerTick")
			command("scoreboard players add @s SpearUseLimit 100")
			var use protocol.Buffer
			use.VarInt(0)
			use.VarInt(int32(index + 1))
			use.Float(0)
			use.Float(0)
			if err := w.WritePacket(server.IDPlayUseItemSB, use.Bytes()); err != nil {
				t.Fatal(err)
			}
			barrier()
			frozen := gametime()
			waitPlayerTicksBB(t, w, collect, fmt.Sprintf("SpearUse%d", index), 20)
			if now := gametime(); now != frozen {
				t.Fatal("player-use wait advanced simulation", frozen, now)
			}
			expected := float64(89)
			if empty {
				expected = 100
			}
			if got := health(victim); got != expected {
				t.Fatal("frozen use health", leg.name, empty, got, expected)
			}
			if got := mounted(mount); got != empty {
				t.Fatal("frozen use attachment", leg.name, empty, got)
			}
			if !empty {
				if leg.name == "vanilla" && !sourceActive {
					t.Fatal("native source use stopped")
				}
				refresh := func() { move(boat, .5); move(boat, 1.25); endFrame() }
				remount := func() {
					command("ride " + victim + " dismount")
					command("ride " + victim + " mount " + mount)
					witness("if data entity " + mount + " Passengers")
				}
				remount()
				refresh()
				waitPlayerTicksBB(t, w, collect, "SpearContactFrozen", 20)
				if now := gametime(); now != frozen {
					t.Fatal("contact wait advanced simulation", frozen, now)
				}
				if !mounted(mount) || health(victim) != 89 {
					t.Fatal("player ticks expired level-time contact cooldown", leg.name)
				}
				remount()
				refresh()
				step(10)
				waitPlayerTicksBB(t, w, collect, "SpearContactAfterStep", 1)
				witness("if score @s BBPlayerTick <= @s SpearUseLimit")
				if mounted(mount) || health(victim) != 78 {
					t.Fatal("simulation contact cooldown did not expire", leg.name, health(victim))
				}
				if now := gametime(); now != frozen+10 {
					t.Fatal("contact simulation delta", frozen, now)
				}
			}
			release()
			barrier()
			t.Logf("%s emptyFrame=%v health=%g levelTime=%d", leg.name, empty, health(victim), gametime())
		}
	}
}
