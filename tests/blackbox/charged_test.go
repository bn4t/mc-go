//go:build blackbox

// Charged-creeper riders parity (26.2):
//   - Creeper.killedEntity: a powered creeper's blast drops the
//     victim's head via the CHARGED_CREEPER loot table
//     (zombie->zombie_head, skeleton->skeleton_skull, creeper->
//     creeper_head, piglin->piglin_head, wither_skeleton->
//     wither_skeleton_skull), gated by the creeper's own
//     shouldDropLoot (!baby && doMobLoot) and latched by
//     droppedSkulls — at most one head per creeper.
//   - explodeCreeper's spawnLingeringCloud: a creeper carrying
//     active effects leaves an area_effect_cloud (radius 2.5,
//     wait 10, duration 300) at the blast point.
// It also pins that explosion kills run die() at all: victims must
// vanish from the world, not linger at zero health.

package blackbox

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// chgItems runs `execute as @e[type=item] run data get entity @s
// Item.id` — the reply stream is the multiset of ground-item ids in
// the loaded area. The player stays in creative for the whole run so
// nothing is picked up off the ground.
func chgItems(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, obs *convObs) []string {
	sendCommand(t, pw, "execute as @e[type=minecraft:item] run data get entity @s Item.id")
	base := len(obs.chats)
	deadline := time.Now().Add(900 * time.Millisecond)
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
		p, err := pr.ReadPacket()
		if err != nil {
			continue
		}
		switch p.ID {
		case server.IDPlaySystemChatCB:
			if txt := chatTextBlackbox(t, p.Data); txt != "" {
				obs.chats = append(obs.chats, txt)
			}
		case server.IDPlayKeepAliveCB:
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
		}
	}
	return obs.chats[base:]
}

func chgCountHeads(chats []string, head string) int {
	n := 0
	for _, ln := range chats {
		if strings.Contains(ln, head) {
			n++
		}
	}
	return n
}

func TestChargedCreeperParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	run := func(addr string) []string {
		var out []string
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		_ = c.SetDeadline(time.Now().Add(300 * time.Second))
		obs := &convObs{}
		// Creative staging: immune to the blasts and never picks up
		// items. Leg E flips to survival only for the cloud dose —
		// a powered blast at staging range is lethal in survival.
		sendCommand(t, pw, "gamemode creative P")
		sendCommand(t, pw, "time set 18000")
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		convPump(t, c, pr, pw, obs, 600*time.Millisecond)

		// Leg A: powered creeper kills a zombie — zombie_head drops.
		// Victims carry Health:1f so any blast impact is lethal on
		// both servers; NoAI everywhere keeps the staging static
		// (ignited creepers pathfind in vanilla — here they can't).
		sendCommand(t, pw, "tp P 8.5 -60 0.5")
		sendCommand(t, pw, "summon minecraft:zombie 15.5 -60 0.5 {NoAI:1b,Health:1f}")
		sendCommand(t, pw, "summon minecraft:creeper 18.5 -60 0.5 {powered:1b,ignited:1b,NoAI:1b}")
		convPump(t, c, pr, pw, obs, 2800*time.Millisecond)
		zombieGone := !convHasEntity(t, c, pr, pw, obs, "@e[type=minecraft:zombie]")
		heads := chgCountHeads(chgItems(t, c, pr, pw, obs), "zombie_head")
		out = append(out, fmt.Sprintf("skull gone=%v heads=%d", zombieGone, heads))
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		convPump(t, c, pr, pw, obs, 600*time.Millisecond)

		// Leg B: two zombies in one powered blast — droppedSkulls
		// latches after the first drop, so exactly one head total.
		sendCommand(t, pw, "tp P 40.5 -60 0.5")
		sendCommand(t, pw, "summon minecraft:zombie 45.5 -60 -1.5 {NoAI:1b,Health:1f}")
		sendCommand(t, pw, "summon minecraft:zombie 45.5 -60 3.5 {NoAI:1b,Health:1f}")
		sendCommand(t, pw, "summon minecraft:creeper 45.5 -60 1.0 {powered:1b,ignited:1b,NoAI:1b}")
		convPump(t, c, pr, pw, obs, 2800*time.Millisecond)
		heads2 := chgCountHeads(chgItems(t, c, pr, pw, obs), "zombie_head")
		out = append(out, fmt.Sprintf("one-shot heads=%d", heads2))
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		convPump(t, c, pr, pw, obs, 600*time.Millisecond)

		// Leg C: unpowered creeper — victim dies (die() wiring) but
		// no head drops without the powered flag.
		sendCommand(t, pw, "tp P 72.5 -60 0.5")
		sendCommand(t, pw, "summon minecraft:zombie 77.5 -60 0.5 {NoAI:1b,Health:1f}")
		sendCommand(t, pw, "summon minecraft:creeper 80.5 -60 0.5 {ignited:1b,NoAI:1b}")
		convPump(t, c, pr, pw, obs, 2800*time.Millisecond)
		ungone := !convHasEntity(t, c, pr, pw, obs, "@e[type=minecraft:zombie]")
		noHeads := chgCountHeads(chgItems(t, c, pr, pw, obs), "zombie_head")
		out = append(out, fmt.Sprintf("uncharged gone=%v heads=%d", ungone, noHeads))
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		convPump(t, c, pr, pw, obs, 600*time.Millisecond)

		// Leg D: a creeper carrying an effect leaves a lingering
		// cloud; a clean one leaves nothing.
		sendCommand(t, pw, "tp P 104.5 -60 0.5")
		sendCommand(t, pw, "summon minecraft:creeper 110.5 -60 0.5 {ignited:1b,NoAI:1b}")
		sendCommand(t, pw, "effect give @e[type=minecraft:creeper] minecraft:slowness 30")
		convPump(t, c, pr, pw, obs, 2800*time.Millisecond)
		cloud := convHasEntity(t, c, pr, pw, obs, "@e[type=minecraft:area_effect_cloud,distance=..8]")
		sendCommand(t, pw, "summon minecraft:creeper 104.5 -60 8.5 {ignited:1b,NoAI:1b}")
		convPump(t, c, pr, pw, obs, 2800*time.Millisecond)
		noCloud := !convHasEntity(t, c, pr, pw, obs, "@e[type=minecraft:area_effect_cloud,distance=..3,x=104.5,z=8.5]")
		out = append(out, fmt.Sprintf("cloud yes=%v no=%v", cloud, noCloud))
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		convPump(t, c, pr, pw, obs, 600*time.Millisecond)

		// Leg E: a player standing inside the cloud catches the
		// scaled effect — creeper carried slowness 30s, the cloud's
		// potionDurationScale 0.25 lands ~150 ticks on the victim.
		sendCommand(t, pw, "tp P 140.5 -60 0.5")
		sendCommand(t, pw, "summon minecraft:creeper 146.5 -60 0.5 {ignited:1b,NoAI:1b}")
		sendCommand(t, pw, "effect give @e[type=minecraft:creeper] minecraft:slowness 30")
		convPump(t, c, pr, pw, obs, 2800*time.Millisecond)
		// Blast is over — step inside as survival so potion effects
		// apply, then read active_effects.
		sendCommand(t, pw, "gamemode survival P")
		sendCommand(t, pw, "tp P 146.5 -60 0.5")
		convPump(t, c, pr, pw, obs, 1600*time.Millisecond)
		sendCommand(t, pw, "data get entity P active_effects")
		convPump(t, c, pr, pw, obs, 800*time.Millisecond)
		dosed := false
		for i := len(obs.chats) - 8; i < len(obs.chats); i++ {
			if i >= 0 && strings.Contains(obs.chats[i], "slowness") {
				dosed = true
			}
		}
		out = append(out, fmt.Sprintf("dose slowness=%v", dosed))
		sendCommand(t, pw, "effect clear P")
		return out
	}

	van := run(targets.vanilla)
	got := run(targets.mcgo)
	for k := range van {
		t.Logf("leg %d vanilla: %s", k, van[k])
		t.Logf("leg %d mc-go:   %s", k, got[k])
		if van[k] != got[k] {
			t.Errorf("leg %d mismatch: %q vs %q", k, van[k], got[k])
		}
	}
}
