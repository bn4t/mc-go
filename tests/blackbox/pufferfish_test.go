package blackbox

// TestPufferfishParity pins Pufferfish's DATA_PUFF_STATE chain on the
// wire: the PuffState NBT int (read clamped to ≤2) rides idx17 varint
// on attach and on /data merge, AbstractFish's FROM_BUCKET bool rides
// idx16 on every bucketable fish, and PufferfishPuffGoal's live cycle
// inflates state 0→1→2 while a scary LivingEntity sits inside
// bbox.inflate(2.0) (counter >40 escalates) then deflates back through
// deflateTimer >60 / >100 once the target leaves. The tp-onto-fish leg
// also pins two side behaviors: the 1-cell pocket forces the
// updatePlayerPose fit ladder to SWIMMING (standing and crouching both
// clip the glass), and at peaceful Pufferfish.playerTouch is a full
// no-op — mobAttack scales to 0 so hurtServer returns false and no
// sting, event-9 or poison emits.
//
// Two glass pockets with two-cell water keep every window
// deterministic: fish A is NoAI (its state machine is frozen by
// isEffectiveAi, so a summoned PuffState:2 never deflates) and hosts
// the merges + the touch leg; fish B shares the far pocket with a NoAI
// rabbit for the inflate/deflate cycle, far enough that the player's
// touch window can't reach it.

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

// sprintReport masks the sprint report's measured args — elapsed
// milliseconds and ms/tick are real timings that differ between any
// two servers and any two runs.
var sprintReport = regexp.MustCompile(`commands.tick.sprint.report", "with": \[\{"": i\(\d+\)\}, \{"": "[0-9.]+"\}\]`)

func TestPufferfishParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	tag := fmt.Sprintf("pf%d", time.Now().UnixNano()%1_000_000)
	selA := fmt.Sprintf("@e[type=minecraft:pufferfish,tag=%sa,limit=1]", tag)
	cmds := []string{
		// Pocket 1 (x14-15): fish A's merge + touch stage. Pocket 2
		// (x20-21): fish B + the rabbit for the live goal cycle.
		"fill 13 -61 7 16 -59 10 minecraft:glass hollow",
		"fill 14 -60 8 15 -60 9 minecraft:water",
		"fill 19 -61 7 22 -59 10 minecraft:glass hollow",
		"fill 20 -60 8 21 -60 9 minecraft:water",
		// Fish A: PuffState reads clamped ≤2; NoAI freezes the state
		// machine so the emitted values stay stable across merges.
		"summon minecraft:pufferfish 14.5 -60 8.5 {NoAI:1b,Tags:[\"" + tag + "a\"],PuffState:2,PersistenceRequired:1b}",
		"data merge entity " + selA + " {PuffState:1}",
		"data merge entity " + selA + " {PuffState:5}",
		"data merge entity " + selA + " {FromBucket:1b}",
		"data get entity " + selA,
		// Fish B: plain summon, AI on — nothing scary in range yet.
		"summon minecraft:pufferfish 20.5 -60 8.5 {Tags:[\"" + tag + "b\"],PersistenceRequired:1b}",
		// Rabbit inside the pocket → the goal arms next tick; the
		// state-1 emit lands inside this window. Invulnerable keeps it
		// alive through the sting ticks so the kill leg is
		// deterministic; RabbitType pins the variant emit that
		// vanilla's random spawn roll leaves nondeterministic.
		"summon minecraft:rabbit 21.5 -60 9.5 {NoAI:1b,PersistenceRequired:1b,Invulnerable:1b,RabbitType:5,Tags:[\"" + tag + "\"]}",
		// inflateCounter crosses >40 mid-sprint → the state-2 emit.
		"tick sprint 45",
		// Player leg: survival puts the player in hurtServer's reach,
		// but at peaceful mobAttack scales to 0 and hurtServer
		// returns false — playerTouch emits nothing on either
		// server. The 1-cell pocket still forces the squeeze-pose
		// emit: standing doesn't fit, so updatePlayerPose walks the
		// fit ladder to SWIMMING (idx6=3).
		"gamemode survival P",
		"tp P 20.5 -60 8.5",
		// A sprint window so the touch tick lands inside a captured
		// window on both clocks.
		"tick sprint 10",
		// No poison to clear — the peaceful sting never landed; the
		// clear reports its failure identically on both.
		"effect clear P",
		"tp P 8.5 -60 8.5",
		"gamemode creative P",
		// Killing the rabbit drops canUse → inflateCounter zeroes and
		// the deflate clock starts. Invulnerable yields to /kill's
		// generic-kill source, which bypasses invulnerability.
		"kill @e[type=minecraft:rabbit,tag=" + tag + "]",
		// deflateTimer >60 → back to 1.
		"tick sprint 70",
		// deflateTimer >100 → 0.
		"tick sprint 45",
	}

	run := func(addr string) [][]string {
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSinkTP(pr, pw)
		sink.cap = 1 << 20
		time.Sleep(400 * time.Millisecond)
		sendCommand(t, pw, "gamerule doMobSpawning false")
		time.Sleep(200 * time.Millisecond)
		sendCommand(t, pw, "difficulty peaceful")
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		time.Sleep(1400 * time.Millisecond)
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		time.Sleep(800 * time.Millisecond)
		sink.take()
		var out [][]string
		for _, cmd := range cmds {
			sink.take()
			sendCommand(t, pw, cmd)
			time.Sleep(550 * time.Millisecond)
			var evs []string
			for _, line := range sink.take() {
				// set_entity_data (0x63): strip the eid, drop the
				// air-supply / equipment-slot ambient entries.
				if strings.HasPrefix(line, "0x63:") {
					body := stripVarInt(line[5:])
					if strings.HasPrefix(body, "\x08\x01") || strings.HasPrefix(body, "\x08\x07") {
						continue
					}
					if body = stripAirEntry(body); body == "" {
						continue
					}
					evs = append(evs, "pkt "+line[:4]+" "+maskUUID(fmt.Sprintf("%q", body)))
					continue
				}
				// The touch leg's payload ids: set_health (0x68) and
				// game_event (0x26) carry no entity id — compare
				// verbatim; update/remove_mob_effect (0x84/0x4e)
				// lead with the player eid — strip it.
				if strings.HasPrefix(line, "0x68:") || strings.HasPrefix(line, "0x26:") {
					evs = append(evs, "pkt "+line[:4]+" "+maskUUID(fmt.Sprintf("%q", line[5:])))
					continue
				}
				if strings.HasPrefix(line, "0x84:") || strings.HasPrefix(line, "0x4e:") {
					evs = append(evs, "pkt "+line[:4]+" "+maskUUID(fmt.Sprintf("%q", stripVarInt(line[5:]))))
					continue
				}
				if tree, ok := componentTree(line, true); ok {
					evs = append(evs, "chat "+maskUUID(sprintReport.ReplaceAllString(tree, `commands.tick.sprint.report", "with": [{"": i(~)}, {"": "~"}]`)))
				}
			}
			out = append(out, evs)
		}
		return out
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	for i, cmd := range cmds {
		if fmt.Sprintf("%v", want[i]) != fmt.Sprintf("%v", got[i]) {
			t.Errorf("cmd %q:\n  vanilla %v\n  mc-go   %v", cmd, want[i], got[i])
		}
	}
}
