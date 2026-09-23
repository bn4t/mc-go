package blackbox

// TestMobFleeParity pins AvoidEntityGoal: a feared entity inside the
// rule's trigger distance sends the fleer away — creeper↔cat and
// skeleton↔wolf at 6 blocks, rabbit↔player at 8 — and the goal keeps
// running past the trigger band toward a ~16-block escape point, so
// separations overshoot. The feared entities are NoAI-anchored so a
// wolf can't chase its own flee test.
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
func fleePos(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, obs *fleeObs, sel string) [3]float64 {
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
	t.Fatalf("no Pos reply for %s; chats=%v", sel, obs.chats)
	return [3]float64{}
}

func TestMobFleeParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	run := func(addr string) []string {
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

		leg := func(name, setup string, aSel, bSel string, want float64) {
			for _, cmd := range splitCmds(setup) {
				sendCommand(t, pw, cmd)
			}
			fleePump(t, c, pr, pw, obs, 6*time.Second)
			a := fleePos(t, c, pr, pw, obs, aSel)
			b := fleePos(t, c, pr, pw, obs, bSel)
			dx, dy, dz := a[0]-b[0], a[1]-b[1], a[2]-b[2]
			dist := dx*dx + dy*dy*0.25 + dz*dz
			out = append(out, fmt.Sprintf("%s sep>%.0f=%v", name, want, dist > want*want))
			sendCommand(t, pw, "kill @e[type=!minecraft:player]")
			fleePump(t, c, pr, pw, obs, 600*time.Millisecond)
		}

		leg("creeper_cat",
			"summon minecraft:creeper ~15 ~ ~|summon minecraft:cat ~17 ~ ~ {NoAI:1b}",
			"@e[type=minecraft:creeper,limit=1]", "@e[type=minecraft:cat,limit=1]", 6)
		leg("skeleton_wolf",
			"summon minecraft:skeleton ~15 ~ ~|summon minecraft:wolf ~17 ~ ~ {NoAI:1b}",
			"@e[type=minecraft:skeleton,limit=1]", "@e[type=minecraft:wolf,limit=1]", 6)
		leg("rabbit_player",
			"summon minecraft:rabbit ~5 ~ ~",
			"@e[type=minecraft:rabbit,limit=1]", "@e[type=minecraft:player,limit=1]", 8)
		c.Close()
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
