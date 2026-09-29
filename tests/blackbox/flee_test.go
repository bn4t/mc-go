package blackbox

// TestMobFleeParity pins AvoidEntityGoal: a feared entity inside the
// rule's trigger distance sends the fleer away — creeper↔cat and
// skeleton↔wolf at 6 blocks, rabbit↔player at 8, fox↔wolf at 8 —
// and the goal keeps running past the trigger band toward a
// ~16-block escape point, so separations overshoot. Vanilla's
// escape target is a RANDOM cell inside ±90° of the away vector
// (getPosAway), which the "side" marker pins: the fleer always ends
// on its own side of the threat, never crossing through it. The fox
// legs pin the rule predicates — a TAMED wolf and a SNEAKING
// (discrete) player are both exempt. The feared entities are
// NoAI-anchored so a wolf can't chase its own flee test. The mob
// pairs run ~100 blocks out in absolute coords: a feared mob on the
// far side can send an escape path near the player, where the
// creeper legitimately swells and detonates.
import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// fleeObs collects the system-chat text so `data get entity ... Pos`
// replies can be read back.
type fleeObs struct {
	chats []string
}

func fleePump(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, obs *fleeObs, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(time.Now().Add(120 * time.Millisecond))
		p, err := pr.ReadPacket()
		if err != nil {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		switch p.ID {
		case server.IDPlayKeepAliveCB:
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
		case server.IDPlaySyncPos:
			if id, err := r.VarInt(); err == nil {
				var b protocol.Buffer
				b.VarInt(id)
				_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
			}
		case server.IDPlaySystemChatCB:
			obs.chats = append(obs.chats, chatTextBlackbox(t, p.Data))
		}
	}
}

// posNum finds one SNBT double — the d suffix is always present;
// separators between components flatten away in chat text, so match
// numbers independently.
var posNum = regexp.MustCompile(`-?[0-9]+\.?[0-9]*(?:[eE][-+]?[0-9]+)?d`)

// fleePos queries `data get entity @e[type=...,limit=1] Pos` (or the
// named player) and parses the coordinate list out of the reply.
func fleePos(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, obs *fleeObs, leg, sel string) [3]float64 {
	t.Helper()
	sendCommand(t, pw, "data get entity "+sel+" Pos")
	base := len(obs.chats)
	fleePump(t, c, pr, pw, obs, 900*time.Millisecond)
	for i := len(obs.chats) - 1; i >= base; i-- {
		nums := posNum.FindAllString(obs.chats[i], -1)
		if len(nums) >= 3 {
			var out [3]float64
			for k := 0; k < 3; k++ {
				v, err := strconv.ParseFloat(strings.TrimSuffix(nums[k], "d"), 64)
				if err != nil {
					t.Fatalf("pos component %q: %v", nums[k], err)
				}
				out[k] = v
			}
			return out
		}
	}
	t.Fatalf("%s: no Pos reply for %s; chats=%v", leg, sel, obs.chats)
	return [3]float64{}
}

func TestMobFleeParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	run := func(legName, addr string) []string {
		var out []string
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		_ = c.SetDeadline(time.Now().Add(240 * time.Second))
		obs := &fleeObs{}
		sendCommand(t, pw, "gamemode survival P")
		sendCommand(t, pw, "gamerule minecraft:natural_health_regeneration false")
		sendCommand(t, pw, "effect give P minecraft:absorption 600 250")
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		sendCommand(t, pw, "time set 6000")
		fleePump(t, c, pr, pw, obs, 800*time.Millisecond)

		// leg runs one flee scenario: summon the pair, read the
		// fleer's pre-run pos, wait 6s of goal ticks, then read both
		// positions again. `sep` is the separation-overshoot check;
		// `side` pins the ±90° half-space — (m1−f)·(m0−f) > 0 keeps
		// the fleer on its own side of the threat no matter which
		// random cell the escape roll picked.
		leg := func(name, setup string, aSel, bSel string, want float64) {
			for _, cmd := range splitCmds(setup) {
				sendCommand(t, pw, cmd)
			}
			fleePump(t, c, pr, pw, obs, 500*time.Millisecond)
			a0 := fleePos(t, c, pr, pw, obs, legName, aSel)
			b0 := fleePos(t, c, pr, pw, obs, legName, bSel)
			fleePump(t, c, pr, pw, obs, 6*time.Second)
			a1 := fleePos(t, c, pr, pw, obs, legName, aSel)
			b1 := fleePos(t, c, pr, pw, obs, legName, bSel)
			dx, dy, dz := a1[0]-b1[0], a1[1]-b1[1], a1[2]-b1[2]
			dist := dx*dx + dy*dy*0.25 + dz*dz
			side := (a1[0]-b0[0])*(a0[0]-b0[0])+(a1[2]-b0[2])*(a0[2]-b0[2]) > -0.5
			out = append(out, fmt.Sprintf("%s sep>%.0f=%v side=%v", name, want, dist > want*want, side))
			sendCommand(t, pw, "kill @e[type=!minecraft:player]")
			fleePump(t, c, pr, pw, obs, 600*time.Millisecond)
		}

		leg("creeper_cat",
			"summon minecraft:creeper 100.5 -60 0.5|summon minecraft:cat 98.5 -60 0.5 {NoAI:1b}",
			"@e[type=minecraft:creeper,limit=1]", "@e[type=minecraft:cat,limit=1]", 6)
		leg("skeleton_wolf",
			"summon minecraft:skeleton 100.5 -60 0.5|summon minecraft:wolf 98.5 -60 0.5 {NoAI:1b}",
			"@e[type=minecraft:skeleton,limit=1]", "@e[type=minecraft:wolf,limit=1]", 6)
		leg("fox_wolf",
			"summon minecraft:fox 100.5 -60 0.5|summon minecraft:wolf 98.5 -60 0.5 {NoAI:1b}",
			"@e[type=minecraft:fox,limit=1]", "@e[type=minecraft:wolf,limit=1]", 6)
		// A tame wolf is exempt from the fox's wolf rule —
		// vanilla's TamableAnimal.load sets isTame when Owner
		// parses, and the fox predicate requires !isTame. `tamed`
		// verifies the Owner reference actually stored on each
		// server. `near` checks the fox is still inside 9 blocks of
		// the wolf ~1.2s after summon: a flee commits within ~2
		// ticks and sprints 8-10 blocks in that window; wander
		// barely drifts that early. (Distance alone over a longer
		// window can't pin the negative leg — the fox's chained
		// strolls legitimately carry it 16+ blocks on both
		// servers.) `fledAfter` then summons a second UNTAMED wolf
		// beside the fox — the rule must still fire for it.
		for _, cmd := range splitCmds(
			"summon minecraft:fox 100.5 -60 0.5|summon minecraft:wolf 98.5 -60 0.5 {NoAI:1b,Owner:[I;0,0,0,1]}") {
			sendCommand(t, pw, cmd)
		}
		fleePump(t, c, pr, pw, obs, 1200*time.Millisecond)
		f0 := fleePos(t, c, pr, pw, obs, legName, "@e[type=minecraft:fox,limit=1]")
		w0 := fleePos(t, c, pr, pw, obs, legName, "@e[type=minecraft:wolf,limit=1]")
		near := (f0[0]-w0[0])*(f0[0]-w0[0])+(f0[2]-w0[2])*(f0[2]-w0[2]) < 81
		sendCommand(t, pw, "data get entity @e[type=minecraft:wolf,limit=1]")
		base := len(obs.chats)
		fleePump(t, c, pr, pw, obs, 2*time.Second)
		tamed := false
		for i := len(obs.chats) - 1; i >= base; i-- {
			// The flattened NBT reply carries "Owner" only when the
			// reference actually stored (an untamed wolf emits none).
			if strings.Contains(obs.chats[i], "Owner") {
				tamed = true
			}
		}
		// Phase B: an untamed wolf beside the fox must still
		// trigger the rule — the tame exemption can't over-exempt.
		// The tamed wolf is removed first so the fox's only feared
		// entity is the untamed one.
		sendCommand(t, pw, "kill @e[type=minecraft:wolf]")
		fleePump(t, c, pr, pw, obs, 300*time.Millisecond)
		// Summon the untamed wolf 2 blocks off the fox's CURRENT
		// (wandered) position — it lands inside the 8-block trigger
		// regardless of how far the fox drifted.
		fm := fleePos(t, c, pr, pw, obs, legName, "@e[type=minecraft:fox,limit=1]")
		sendCommand(t, pw, fmt.Sprintf("summon minecraft:wolf %.1f -60 %.1f {NoAI:1b}", fm[0]+2, fm[2]))
		fleePump(t, c, pr, pw, obs, 4*time.Second)
		f1 := fleePos(t, c, pr, pw, obs, legName, "@e[type=minecraft:fox,limit=1]")
		w1 := fleePos(t, c, pr, pw, obs, legName, "@e[type=minecraft:wolf,limit=1]")
		fledAfter := (f1[0]-w1[0])*(f1[0]-w1[0])+(f1[2]-w1[2])*(f1[2]-w1[2]) > 36
		t.Logf("%s fox_tamed_wolf: near=%v tamed=%v fledAfter=%v f1=%v w1=%v", legName, near, tamed, fledAfter, f1, w1)
		out = append(out, fmt.Sprintf("fox_tamed_wolf tamed=%v near=%v fledAfter=%v", tamed, near, fledAfter))
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		fleePump(t, c, pr, pw, obs, 600*time.Millisecond)
		// rabbit flees a still player inside 8.
		leg("rabbit_player",
			"summon minecraft:rabbit ~5 ~ ~",
			"@e[type=minecraft:rabbit,limit=1]", "@e[type=minecraft:player,limit=1]", 8)

		// fox_sneak leg: the fox's AVOID_PLAYERS predicate exempts a
		// DISCRETE player — sneaking inside the 16-block trigger the
		// fox stays within the trigger range; releasing sneak inside
		// it sends the fox out past 6 blocks. Separation is compared
		// to the anchored player, not the fox's own displacement
		// (a wild fox still wanders a little while staying put).
		// Sneak BEFORE the summon — a fox that sees the player
		// non-discrete for even a tick commits to an escape path it
		// then finishes inside the sneak window.
		sendCommand(t, pw, "tp P 90.5 -60 0.5")
		if err := pw.WritePacket(server.IDPlayPlayerInputSB, []byte{0x20}); err != nil { // sneak on
			t.Fatalf("%s sneak pkt: %v", legName, err)
		}
		fleePump(t, c, pr, pw, obs, 400*time.Millisecond)
		sendCommand(t, pw, "summon minecraft:fox 100.5 -60 0.5")
		fleePump(t, c, pr, pw, obs, 300*time.Millisecond)
		fleePump(t, c, pr, pw, obs, 4*time.Second)
		foxS := fleePos(t, c, pr, pw, obs, legName, "@e[type=minecraft:fox,limit=1]")
		sepS := (foxS[0]-90.5)*(foxS[0]-90.5) + (foxS[2]-0.5)*(foxS[2]-0.5)
		if err := pw.WritePacket(server.IDPlayPlayerInputSB, []byte{0x00}); err != nil { // sneak off
			t.Fatalf("%s unsneak pkt: %v", legName, err)
		}
		fleePump(t, c, pr, pw, obs, 5*time.Second)
		foxU := fleePos(t, c, pr, pw, obs, legName, "@e[type=minecraft:fox,limit=1]")
		sepU := (foxU[0]-90.5)*(foxU[0]-90.5) + (foxU[2]-0.5)*(foxU[2]-0.5)
		out = append(out, fmt.Sprintf("fox_sneak stay=%v fleeAfter=%v",
			sepS < 256, sepU > 36))
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		sendCommand(t, pw, "tp P 0.5 -60 0.5")
		fleePump(t, c, pr, pw, obs, 600*time.Millisecond)
		c.Close()
		return out
	}

	van := run("vanilla", targets.vanilla)
	mc := run("mc-go", targets.mcgo)
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

func splitCmds(s string) []string {
	var out []string
	cur := ""
	for _, ch := range s {
		if ch == '|' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(ch)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
