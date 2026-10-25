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

// TestKillScoreParity pins the scoreboard kill criteria: a player death
// bumps deathCount on the victim, and getKillCredit().awardKillScore —
// the last player to hurt within the 100-tick window — bumps
// totalKillCount (+playerKillCount for player victims) on the killer,
// teamkill.<victim's team color> on the killer, and
// killedByTeam.<killer's team color> on the victim. Mob killers run
// Entity's empty awardKillScore, so they bump nothing; a self-kill is
// not credited; and an unattributed finish still credits the last
// player hurter inside the window.
func TestKillScoreParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{
			"ops.json": opsJSONFor("P", "Q"),
		}, nil)
	defer targets.cleanup()

	setup := []string{
		`scoreboard objectives add zdc deathCount`,
		`scoreboard objectives add ztk totalKillCount`,
		`scoreboard objectives add zpk playerKillCount`,
		`scoreboard objectives add ztkb teamkill.blue`,
		`scoreboard objectives add ztkr teamkill.red`,
		`scoreboard objectives add zkbb killedByTeam.blue`,
		`team add tred`,
		`team add tblu`,
		`team modify tred color red`,
		`team modify tblu color blue`,
		`team join tred P`,
		`team join tblu Q`,
		`gamemode survival P`,
		`gamemode survival Q`,
	}

	legs := []struct {
		name  string
		cmds  []string
		probe string
	}{
		{"killer unset", nil, `execute unless score Q ztk matches -2147483648..2147483647 run say ZKS-ok-%d`},
		{"mob kill tkc", []string{
			`summon minecraft:zombie ~ ~-2 ~ {NoAI:1b,Tags:["zks"]}`,
			`team join tblu @e[type=minecraft:zombie,tag=zks]`,
			`damage @e[type=minecraft:zombie,tag=zks,limit=1] 100 minecraft:mob_attack by Q`,
		}, `execute if score Q ztk matches 1 run say ZKS-ok-%d`},
		{"mob kill pkc", nil, `execute unless score Q zpk matches -2147483648..2147483647 run say ZKS-ok-%d`},
		{"mob kill teamkill blue", nil, `execute if score Q ztkb matches 1 run say ZKS-ok-%d`},
		{"mob kill teamkill red", nil, `execute unless score Q ztkr matches -2147483648..2147483647 run say ZKS-ok-%d`},
		{"player death dc", []string{
			`damage P 100 minecraft:mob_attack by Q`,
		}, `execute if score P zdc matches 1 run say ZKS-ok-%d`},
		{"player kill tkc", nil, `execute if score Q ztk matches 2 run say ZKS-ok-%d`},
		{"player kill pkc", nil, `execute if score Q zpk matches 1 run say ZKS-ok-%d`},
		{"player kill teamkill red", nil, `execute if score Q ztkr matches 1 run say ZKS-ok-%d`},
		{"victim killedByTeam blue", nil, `execute if score P zkbb matches 1 run say ZKS-ok-%d`},
		{"self kill dc", []string{
			`damage Q 100 minecraft:mob_attack by Q`,
		}, `execute if score Q zdc matches 1 run say ZKS-ok-%d`},
		{"self kill no credit", nil, `execute if score Q ztk matches 2 run say ZKS-ok-%d`},
		{"mob kill no score", []string{
			`summon minecraft:zombie ~ ~-2 ~ {NoAI:1b,Tags:["zkm"]}`,
			`damage P 100 minecraft:mob_attack by @e[type=minecraft:zombie,tag=zkm,limit=1]`,
		}, `execute if score P zdc matches 2 run say ZKS-ok-%d`},
		{"mob kill victim unchanged", nil, `execute if score P zkbb matches 1 run say ZKS-ok-%d`},
		{"mob kill killer unchanged", nil, `execute if score Q ztk matches 2 run say ZKS-ok-%d`},
		{"env kill last-hurt credit", []string{
			`summon minecraft:zombie ~ ~-2 ~ {NoAI:1b,Tags:["zke"]}`,
			`team join tblu @e[type=minecraft:zombie,tag=zke]`,
			`damage @e[type=minecraft:zombie,tag=zke,limit=1] 5 minecraft:mob_attack by Q`,
			`damage @e[type=minecraft:zombie,tag=zke,limit=1] 100 minecraft:generic`,
		}, `execute if score Q ztk matches 3 run say ZKS-ok-%d`},
		{"env kill teamkill blue", nil, `execute if score Q ztkb matches 2 run say ZKS-ok-%d`},
		{"kill cmd dc", []string{
			`kill P`,
		}, `execute if score P zdc matches 3 run say ZKS-ok-%d`},
		{"kill cmd no credit", nil, `execute if score Q ztk matches 3 run say ZKS-ok-%d`},
		{"player env credit dc", []string{
			`SLEEP:1800`,
			`damage P 5 minecraft:mob_attack by Q`,
			`SLEEP:1200`,
			`damage P 100 minecraft:generic`,
		}, `execute if score P zdc matches 4 run say ZKS-ok-%d`},
		{"player env credit tkc", nil, `execute if score Q ztk matches 4 run say ZKS-ok-%d`},
		{"player env credit pkc", nil, `execute if score Q zpk matches 2 run say ZKS-ok-%d`},
		{"player env credit teamkill", nil, `execute if score Q ztkr matches 2 run say ZKS-ok-%d`},
		{"player env credit killedByTeam", nil, `execute if score P zkbb matches 2 run say ZKS-ok-%d`},
		{"credit last hurter", []string{
			`summon minecraft:zombie ~ ~-2 ~ {NoAI:1b,Tags:["zkh"]}`,
			`damage @e[type=minecraft:zombie,tag=zkh,limit=1] 5 minecraft:mob_attack by P`,
			`damage @e[type=minecraft:zombie,tag=zkh,limit=1] 100 minecraft:generic`,
		}, `execute if score P ztk matches 1 run say ZKS-ok-%d`},
		{"credit expiry dc", []string{
			`damage Q 5 minecraft:mob_attack by P`,
			`SLEEP:6000`,
			`damage Q 100 minecraft:generic`,
		}, `execute if score Q zdc matches 2 run say ZKS-ok-%d`},
		{"credit expiry no award", nil, `execute if score P ztk matches 1 run say ZKS-ok-%d`},
	}

	run := func(addr string) []string {
		c, pr, pw := joinPlayQuiet(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		sendClientInformation(t, pw)
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSinkTP(pr, pw)
		sink.cap = 1 << 20

		c2, pr2, pw2 := joinPlayQuiet(t, addr, "Q")
		defer c2.Close()
		_ = c2.SetDeadline(time.Time{})
		sendClientInformation(t, pw2)
		_ = pw2.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink2 := newChatSinkTP(pr2, pw2)
		sink2.cap = 1 << 20

		time.Sleep(4 * time.Second)
		sink.take()
		sink2.take()
		for _, cmd := range setup {
			sendCommand(t, pw, cmd)
		}
		time.Sleep(1500 * time.Millisecond)
		sink.take()

		var out []string
		for i, leg := range legs {
			for _, cmd := range leg.cmds {
				if strings.HasPrefix(cmd, "SLEEP:") {
					ms, _ := strconv.Atoi(strings.TrimPrefix(cmd, "SLEEP:"))
					time.Sleep(time.Duration(ms) * time.Millisecond)
					continue
				}
				sendCommand(t, pw, cmd)
				time.Sleep(250 * time.Millisecond)
			}
			time.Sleep(600 * time.Millisecond)
			// Respawn anyone the leg killed so later damage legs can
			// target them again (dead players are not damage targets).
			var b protocol.Buffer
			b.VarInt(0) // client status: perform respawn
			_ = pw.WritePacket(server.IDPlayClientStatusSB, b.Bytes())
			_ = pw2.WritePacket(server.IDPlayClientStatusSB, b.Bytes())
			time.Sleep(400 * time.Millisecond)
			sendCommand(t, pw, fmt.Sprintf(leg.probe, i))
			sendCommand(t, pw, fmt.Sprintf("say ZKM%d", i))
			marker := fmt.Sprintf("ZKM%d", i)
			hit := "miss"
			deadline := time.Now().Add(8 * time.Second)
			for time.Now().Before(deadline) {
				done := false
				for _, line := range sink.take() {
					if strings.Contains(line, marker) {
						done = true
					}
					if strings.Contains(line, fmt.Sprintf("ZKS-ok-%d", i)) {
						hit = "hit"
					}
				}
				sink2.take()
				if done {
					break
				}
				time.Sleep(40 * time.Millisecond)
			}
			out = append(out, hit)
		}
		return out
	}

	vanilla := run(targets.vanilla)
	mcgo := run(targets.mcgo)
	for i, leg := range legs {
		t.Logf("%-28s vanilla=%s mcgo=%s", leg.name, vanilla[i], mcgo[i])
		if vanilla[i] != mcgo[i] {
			t.Errorf("leg %q: vanilla %s, mc-go %s", leg.name, vanilla[i], mcgo[i])
		}
	}
}
