package blackbox

// TestSculkSensorParity: the VibrationSystem pipeline — a gameEvent
// broadcast lands in every sculk sensor's radius-8 notification box,
// the strict dist²<64 gate drops the rest, the wool-occlusion ray
// march silences occluded listeners, the single-slot selector keeps
// the highest-frequency same-tick candidate, the vibration travels
// floor(dist) ticks and arrives as activate: phase flips to ACTIVE,
// power = 15 - floor(dist*15/8) clamps to >=1, last_vibration_frequency
// latches the event id, and the sensor steps ACTIVE->(30t)->COOLDOWN
// ->(10t)->INACTIVE. A calibrated sensor's back redstone signal
// filters every frequency but its own. container_open/container_close
// and entity_damage/entity_die/explode are the emit sites under test.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestSculkSensorParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Sculk"),
	}, nil)
	defer targets.cleanup()

	blockProbe := func(pw *protocol.Writer, sink *chatSink, spec string, i int) string {
		sendCommand(t, pw, fmt.Sprintf("execute if block %s run say ZS-ok-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("execute unless block %s run say ZS-miss-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("say ZSK%d", i))
		hit, marker := "none", fmt.Sprintf("ZSK%d", i)
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, marker) {
					return hit
				}
				if strings.Contains(line, fmt.Sprintf("ZS-ok-%d", i)) {
					hit = "hit"
				}
				if strings.Contains(line, fmt.Sprintf("ZS-miss-%d", i)) {
					hit = "miss"
				}
			}
			time.Sleep(40 * time.Millisecond)
		}
		return hit
	}

	// dataGetBlock reads a numeric NBT path via /data get block — the
	// value renders before the commands.data.block.query key.
	dataGetBlock := func(sink *chatSink, pw *protocol.Writer, pos, path string) float64 {
		sendCommand(t, pw, "data get block "+pos+" "+path)
		sendCommand(t, pw, "say ZSEND")
		deadline := time.Now().Add(6 * time.Second)
		got := ""
		for time.Now().Before(deadline) {
			end := false
			for _, line := range sink.take() {
				if i := strings.Index(line, "data.block.query"); i >= 0 {
					got = line[:i]
				}
				if strings.Contains(line, "ZSEND") {
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

	// sprint steps the server `n` ticks forward and waits for the
	// completion report — deterministic event timing on both targets.
	// The report rides the wire as the commands.tick.sprint.report
	// translate key, so match the key, not its English render.
	sprint := func(pw *protocol.Writer, sink *chatSink, n int) {
		sendCommand(t, pw, fmt.Sprintf("tick sprint %d", n))
		deadline := time.Now().Add(90 * time.Second)
		for time.Now().Before(deadline) {
			done := false
			for _, line := range sink.take() {
				if strings.Contains(line, "tick.sprint.report") ||
					strings.Contains(line, "Sprint completed") {
					done = true
				}
			}
			if done {
				return
			}
			time.Sleep(80 * time.Millisecond)
		}
	}

	probes := []struct{ name, spec string }{}
	pIdx := 0
	nextIdx := func() int {
		pIdx++
		return pIdx
	}
	out := []string{}

	run := func(addr string) []string {
		pIdx, out = 0, nil
		probes = probes[:0]
		c, pr, pw := joinPlayBlackbox(t, addr, "Sculk")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		sink := newChatSinkTP(pr, pw)
		sendClientInformation(t, pw)
		time.Sleep(300 * time.Millisecond)
		sendCommand(t, pw, "tp @s 44 -59 20 0 0")
		time.Sleep(1200 * time.Millisecond)

		// Arena: stone pad, five plain sensors in a row plus an
		// occlusion cell, a chest cell, an explosion cell, and two
		// calibrated sensors (one back-powered to 15 by a floor lever
		// on its south face, one free).
		for _, cmd := range []string{
			`fill 27 -62 26 65 -61 36 minecraft:stone`,
			`fill 27 -60 26 65 -59 36 minecraft:air`,
			`setblock 31 -60 30 minecraft:sculk_sensor`,
			`setblock 34 -60 30 minecraft:sculk_sensor`,
			`setblock 37 -60 30 minecraft:sculk_sensor`,
			`setblock 35 -60 34 minecraft:sculk_sensor`,
			`setblock 38 -60 34 minecraft:sculk_sensor`,
			`fill 37 -61 34 37 -59 34 minecraft:white_wool`,
			`setblock 44 -60 32 minecraft:sculk_sensor`,
			`setblock 44 -60 30 minecraft:chest`,
			`setblock 63 -60 30 minecraft:sculk_sensor`,
			`setblock 50 -60 30 minecraft:calibrated_sculk_sensor[facing=north]`,
			`setblock 50 -60 31 minecraft:lever[face=floor,powered=true]`,
			`setblock 50 -60 34 minecraft:calibrated_sculk_sensor[facing=north]`,
			`setblock 54 -60 30 minecraft:sculk_sensor`,
			`setblock 55 -60 30 minecraft:amethyst_block`,
			`setblock 62 -60 30 minecraft:sculk_sensor`,
			`summon minecraft:sheep 30.5 -60 30.5 {NoAI:1b}`,
			`summon minecraft:sheep 36.5 -60 34.5 {NoAI:1b}`,
			`summon minecraft:sheep 48.5 -60 30.5 {NoAI:1b}`,
			`summon minecraft:sheep 53.5 -60 30.5 {NoAI:1b}`,
		} {
			sendCommand(t, pw, cmd)
			time.Sleep(60 * time.Millisecond)
		}
		sink.take()

		// Leg A — entity_damage at (30.5,-60,30.5): the power falloff
		// runs on the floored-BlockPos distance, so 1 -> power 14,
		// 4 -> 8, 7 -> 2.
		sendCommand(t, pw, "damage @e[type=minecraft:sheep,x=30,y=-60,z=30,distance=..1.5,limit=1] 1")
		sprint(pw, sink, 8)
		probes = append(probes,
			struct{ name, spec string }{"near sensor power 14", "31 -60 30 minecraft:sculk_sensor[sculk_sensor_phase=active,power=14]"},
			struct{ name, spec string }{"mid sensor power 8", "34 -60 30 minecraft:sculk_sensor[sculk_sensor_phase=active,power=8]"},
			struct{ name, spec string }{"far sensor power 2", "37 -60 30 minecraft:sculk_sensor[sculk_sensor_phase=active,power=2]"},
		)
		for _, p := range probes[pIdx:] {
			i := nextIdx()
			out = append(out, fmt.Sprintf("%s: %s", p.name, blockProbe(pw, sink, p.spec, i)))
		}
		// 30t active + 10t cooldown: well past both, all inactive.
		sprint(pw, sink, 45)
		probes = append(probes,
			struct{ name, spec string }{"near sensor back inactive", "31 -60 30 minecraft:sculk_sensor[sculk_sensor_phase=inactive]"},
			struct{ name, spec string }{"mid sensor back inactive", "34 -60 30 minecraft:sculk_sensor[sculk_sensor_phase=inactive]"},
		)
		for _, p := range probes[pIdx:] {
			i := nextIdx()
			out = append(out, fmt.Sprintf("%s: %s", p.name, blockProbe(pw, sink, p.spec, i)))
		}
		out = append(out, fmt.Sprintf("near last freq: %g",
			dataGetBlock(sink, pw, "31 -60 30", "last_vibration_frequency")))

		// Leg B — wool occlusion: the walled sensor stays dark, the
		// open control sees the same damage event; the free
		// calibrated hears it ungated at distSqr 196 (blockDist
		// 14 -> power 2) and the powered one's back gate drops
		// freq 7.
		sendCommand(t, pw, "damage @e[type=minecraft:sheep,x=36,y=-60,z=34,distance=..1.5,limit=1] 1")
		sprint(pw, sink, 16)
		probes = append(probes,
			struct{ name, spec string }{"occluded sensor dark", "38 -60 34 minecraft:sculk_sensor[sculk_sensor_phase=inactive]"},
			struct{ name, spec string }{"open control active", "35 -60 34 minecraft:sculk_sensor[sculk_sensor_phase=active]"},
			struct{ name, spec string }{"free cal hears across arena", "50 -60 34 minecraft:calibrated_sculk_sensor[sculk_sensor_phase=active,power=2]"},
			struct{ name, spec string }{"powered cal gates freq7", "50 -60 30 minecraft:calibrated_sculk_sensor[sculk_sensor_phase=inactive]"},
		)
		for _, p := range probes[pIdx:] {
			i := nextIdx()
			out = append(out, fmt.Sprintf("%s: %s", p.name, blockProbe(pw, sink, p.spec, i)))
		}
		sprint(pw, sink, 45)

		// Leg C — calibrated back-signal: the powered sensor's back
		// lever reads 15, so freq 7 is gated out while the free
		// sensor accepts it; freq 15 (entity_die via /kill) passes
		// the powered gate.
		sendCommand(t, pw, "damage @e[type=minecraft:sheep,x=48,y=-60,z=30,distance=..1.5,limit=1] 1")
		sprint(pw, sink, 8)
		probes = append(probes,
			struct{ name, spec string }{"powered cal rejects freq7", "50 -60 30 minecraft:calibrated_sculk_sensor[sculk_sensor_phase=inactive]"},
			struct{ name, spec string }{"free cal power 11", "50 -60 34 minecraft:calibrated_sculk_sensor[sculk_sensor_phase=active,power=11]"},
		)
		for _, p := range probes[pIdx:] {
			i := nextIdx()
			out = append(out, fmt.Sprintf("%s: %s", p.name, blockProbe(pw, sink, p.spec, i)))
		}
		sendCommand(t, pw, "kill @e[type=minecraft:sheep,x=48,y=-60,z=30,distance=..1.5]")
		sprint(pw, sink, 8)
		probes = append(probes,
			struct{ name, spec string }{"powered cal accepts freq15", "50 -60 30 minecraft:calibrated_sculk_sensor[sculk_sensor_phase=active,power=14]"},
			struct{ name, spec string }{"free cal still busy", "50 -60 34 minecraft:calibrated_sculk_sensor[sculk_sensor_phase=active]"},
		)
		for _, p := range probes[pIdx:] {
			i := nextIdx()
			out = append(out, fmt.Sprintf("%s: %s", p.name, blockProbe(pw, sink, p.spec, i)))
		}
		sprint(pw, sink, 45)
		out = append(out, fmt.Sprintf("powered cal last freq: %g",
			dataGetBlock(sink, pw, "50 -60 30", "last_vibration_frequency")))
		out = append(out, fmt.Sprintf("free cal last freq: %g",
			dataGetBlock(sink, pw, "50 -60 34", "last_vibration_frequency")))

		// Leg R — amethyst resonance: an activated sensor re-emits
		// resonate_<freq> at each adjacent vibration_resonators
		// block, so listeners beyond the event's own reach hear the
		// relayed frequency (distSqr 81/100 > 64 blocks the direct
		// signal — only the relay can reach them).
		sendCommand(t, pw, "damage @e[type=minecraft:sheep,x=53,y=-60,z=30,distance=..1.5,limit=1] 1")
		sprint(pw, sink, 12)
		probes = append(probes,
			struct{ name, spec string }{"relay source active", "54 -60 30 minecraft:sculk_sensor[sculk_sensor_phase=active,power=14]"},
			struct{ name, spec string }{"resonate hop 7 power 2", "62 -60 30 minecraft:sculk_sensor[sculk_sensor_phase=active,power=2]"},
			struct{ name, spec string }{"resonate hop 8 power 1", "63 -60 30 minecraft:sculk_sensor[sculk_sensor_phase=active,power=1]"},
			struct{ name, spec string }{"powered cal gates resonate7", "50 -60 30 minecraft:calibrated_sculk_sensor[sculk_sensor_phase=inactive]"},
		)
		for _, p := range probes[pIdx:] {
			i := nextIdx()
			out = append(out, fmt.Sprintf("%s: %s", p.name, blockProbe(pw, sink, p.spec, i)))
		}
		sprint(pw, sink, 45)
		out = append(out, fmt.Sprintf("hop7 last freq: %g",
			dataGetBlock(sink, pw, "62 -60 30", "last_vibration_frequency")))
		out = append(out, fmt.Sprintf("hop8 last freq: %g",
			dataGetBlock(sink, pw, "63 -60 30", "last_vibration_frequency")))

		// Leg D — chest container_open (freq 10) then
		// container_close (freq 9) once the sensor settles. The
		// player must stand inside block-interaction range or the
		// use packet is rejected server-side.
		sendCommand(t, pw, "tp @s 44 -59 27 0 0")
		time.Sleep(600 * time.Millisecond)
		sendUseItemOn(t, pw, 44, -60, 30, 1, 1)
		sprint(pw, sink, 8)
		probes = append(probes,
			struct{ name, spec string }{"chest open powers sensor", "44 -60 32 minecraft:sculk_sensor[sculk_sensor_phase=active,power=12]"},
		)
		for _, p := range probes[pIdx:] {
			i := nextIdx()
			out = append(out, fmt.Sprintf("%s: %s", p.name, blockProbe(pw, sink, p.spec, i)))
		}
		out = append(out, fmt.Sprintf("chest last freq: %g",
			dataGetBlock(sink, pw, "44 -60 32", "last_vibration_frequency")))
		sprint(pw, sink, 45)
		winID := -1
		prefix := fmt.Sprintf("%#x:", server.IDPlayOpenScreen)
		for _, line := range sink.take() {
			if strings.HasPrefix(line, prefix) && len(line) > len(prefix) {
				winID = int(line[len(prefix)])
			}
		}
		if winID < 0 {
			winID = 1 // both servers use 1 for the first window
		}
		var closeB protocol.Buffer
		closeB.VarInt(int32(winID))
		_ = pw.WritePacket(server.IDPlayCloseContainerSB, closeB.Bytes())
		sprint(pw, sink, 8)
		probes = append(probes,
			struct{ name, spec string }{"chest close re-powers", "44 -60 32 minecraft:sculk_sensor[sculk_sensor_phase=active,power=12]"},
		)
		for _, p := range probes[pIdx:] {
			i := nextIdx()
			out = append(out, fmt.Sprintf("%s: %s", p.name, blockProbe(pw, sink, p.spec, i)))
		}
		out = append(out, fmt.Sprintf("close last freq: %g",
			dataGetBlock(sink, pw, "44 -60 32", "last_vibration_frequency")))
		sprint(pw, sink, 45)

		// Leg E — tnt explode (freq 15) at (56.5,-60,30.5): the
		// sensor sits 7.02 away (power 2), outside the crater a
		// power-4 blast carves.
		sendCommand(t, pw, "summon minecraft:tnt 56.5 -60 30.5 {fuse:8}")
		sprint(pw, sink, 20)
		probes = append(probes,
			struct{ name, spec string }{"explode powers sensor", "63 -60 30 minecraft:sculk_sensor[sculk_sensor_phase=active,power=2]"},
		)
		for _, p := range probes[pIdx:] {
			i := nextIdx()
			out = append(out, fmt.Sprintf("%s: %s", p.name, blockProbe(pw, sink, p.spec, i)))
		}
		out = append(out, fmt.Sprintf("explode last freq: %g",
			dataGetBlock(sink, pw, "63 -60 30", "last_vibration_frequency")))
		probes = append(probes,
			struct{ name, spec string }{"explode sensor survived", "63 -60 30 minecraft:sculk_sensor"},
		)
		for _, p := range probes[pIdx:] {
			i := nextIdx()
			out = append(out, fmt.Sprintf("%s: %s", p.name, blockProbe(pw, sink, p.spec, i)))
		}

		return out
	}

	vOut := run(targets.vanilla)
	mOut := run(targets.mcgo)
	if len(vOut) != len(mOut) {
		t.Fatalf("probe count mismatch vanilla=%d mcgo=%d", len(vOut), len(mOut))
	}
	for i := range vOut {
		if vOut[i] != mOut[i] {
			t.Errorf("probe %d: vanilla=%q mcgo=%q", i, vOut[i], mOut[i])
		}
	}
	if t.Failed() {
		for i := range vOut {
			t.Logf("all %02d: vanilla=%q mcgo=%q", i, vOut[i], mOut[i])
		}
	}
}
