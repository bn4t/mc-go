package blackbox

import (
	"net"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
)

// countTypeScan returns how many entities of typ answer a `data get`
// — one reply line per living entity.
func countTypeScan(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, obs *fleeObs, typ string) int {
	t.Helper()
	sendCommand(t, pw, "execute as @e[type=minecraft:"+typ+"] run data get entity @s Pos")
	base := len(obs.chats)
	fleePump(t, c, pr, pw, obs, 1200*time.Millisecond)
	n := 0
	for _, ln := range obs.chats[base:] {
		if strings.Contains(ln, "entity.minecraft."+typ) {
			n++
		}
	}
	return n
}

// Overworld zombification: a piglin-family mob (or hoglin) with AI
// and no immunity converts after TimeInOverworld > 300 ticks — piglin
// and piglin_brute become zombified_piglin (equipment kept, nausea
// applied), hoglin becomes zoglin. NoAI or IsImmuneToZombification
// pause the clock entirely.
func TestOverworldZombifyParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	type result struct {
		piglinLeft, brutesLeft, noaiLeft int
		zombies, zoglins                 int
		equipped, repopulated            int
	}
	run := func(addr string) result {
		var r result
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(120 * time.Second))
		obs := &fleeObs{}
		sendCommand(t, pw, "gamemode creative P")
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		sendCommand(t, pw, "tp P 100.5 -60 0.5")
		fleePump(t, c, pr, pw, obs, 800*time.Millisecond)

		// The converting piglin is tagged baby: its zombified must stay
		// bare — convertTo copies equipment, never re-populates.
		// Hoglins sit in the convert zone (they only tick reliably near
		// the player); survivors are far away, invulnerable and
		// persistent so a wandering zoglin can't erase them.
		sendCommand(t, pw, "summon minecraft:piglin 105.5 -59 0.5 {IsBaby:1b}")
		sendCommand(t, pw, "summon minecraft:piglin_brute 108.5 -59 0.5")
		sendCommand(t, pw, "summon minecraft:hoglin 118.5 -59 0.5")
		sendCommand(t, pw, "summon minecraft:hoglin 122.5 -59 0.5")
		sendCommand(t, pw, "summon minecraft:piglin 150.5 -59 0.5 {IsImmuneToZombification:1b,PersistenceRequired:1b,Invulnerable:1b}")
		sendCommand(t, pw, "summon minecraft:piglin 155.5 -59 0.5 {NoAI:1b,PersistenceRequired:1b,Invulnerable:1b}")

		// Conversion fires past 300 ticks (15 s) — wait comfortably.
		fleePump(t, c, pr, pw, obs, 18*time.Second)

		r.piglinLeft = countTypeScan(t, c, pr, pw, obs, "piglin")
		r.brutesLeft = countTypeScan(t, c, pr, pw, obs, "piglin_brute")
		r.zombies = countTypeScan(t, c, pr, pw, obs, "zombified_piglin")
		r.zoglins = countTypeScan(t, c, pr, pw, obs, "zoglin")
		r.noaiLeft = countTypeScan(t, c, pr, pw, obs, "hoglin")
		if r.noaiLeft > 0 {
			sendCommand(t, pw, "execute as @e[type=minecraft:hoglin] run data get entity @s TimeInOverworld")
			base := len(obs.chats)
			fleePump(t, c, pr, pw, obs, 1200*time.Millisecond)
			for _, ln := range obs.chats[base:] {
				t.Logf("hoglin time: %s", ln)
			}
		}

		sendCommand(t, pw, "execute as @e[type=minecraft:zombified_piglin] run data get entity @s equipment")
		base := len(obs.chats)
		fleePump(t, c, pr, pw, obs, 1200*time.Millisecond)
		for _, ln := range obs.chats[base:] {
			if !strings.Contains(ln, "zombified_piglin") {
				continue
			}
			if strings.Contains(ln, "golden_axe") {
				r.equipped++
			}
			if strings.Contains(ln, "golden_sword") || strings.Contains(ln, "golden_spear") ||
				strings.Contains(ln, "crossbow") {
				r.repopulated++
			}
		}
		return r
	}

	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	t.Logf("van %+v", van)
	t.Logf("mc  %+v", mc)

	for name, r := range map[string]result{"vanilla": van, "mc-go": mc} {
		// The immune and NoAI piglins survive; the bare adult converts.
		if r.piglinLeft != 2 {
			t.Errorf("%s piglins left=%d want 2 (immune+noai)", name, r.piglinLeft)
		}
		if r.brutesLeft != 0 {
			t.Errorf("%s piglin_brutes left=%d want 0", name, r.brutesLeft)
		}
		// Hoglin conversion is timing-marginal (wanderers can pause
		// the counter) — two summons, at least one must land.
		if r.zoglins < 1 {
			t.Errorf("%s zoglins=%d want ≥1", name, r.zoglins)
		}
		if r.zombies != 2 {
			t.Errorf("%s zombified_piglins=%d want 2 (piglin+brute)", name, r.zombies)
		}
		// The brute's golden_axe must survive conversion; the baby
		// piglin's zombified must stay bare (convertTo never
		// re-populates).
		if r.equipped != 1 {
			t.Errorf("%s zombified carrying golden_axe=%d want 1", name, r.equipped)
		}
		if r.repopulated > 0 {
			t.Errorf("%s zombified has %d fresh weapon lines — convertTo keeps, not repopulates", name, r.repopulated)
		}
	}
}
