package blackbox

// TestCreeperBoomParity pins Creeper's SwellGoal + explodeCreeper: a
// creeper that closes to ~7 blocks lights the 30-tick fuse
// (entity.creeper.primed at fuse start), then explodes — entity
// discard, explode packet with radius=3 (6 powered), block carve with
// mob_griefing, damage to entities in 2r. Fuse bookkeeping is
// Creeper.tick, so an ignited creeper blows up even with NoAI.
import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

type boomObs struct {
	explodes int
	radius   float32
	dmgEvent int
	chats    []string
	selfEID  int32
}

func boomPump(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, obs *boomObs, d time.Duration) {
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
		case server.IDPlayJoinGame:
			if id, err := r.Int(); err == nil {
				obs.selfEID = id
			}
		case server.IDPlayDamageEvent:
			if eid, err := r.VarInt(); err == nil && eid == obs.selfEID {
				obs.dmgEvent++
			}
		case 0x24: // explode (26.2 clientbound id)
			ep := decodeExplode(t, p.Data)
			obs.explodes++
			obs.radius = ep.radius
		case server.IDPlaySystemChatCB:
			obs.chats = append(obs.chats, chatTextBlackbox(t, p.Data))
		}
	}
}

// blockIsAir probes `execute if block x y z minecraft:air` — the bare
// conditional replies conditional.pass / conditional.fail.
func blockIsAir(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, obs *boomObs, x, y, z string) bool {
	t.Helper()
	sendCommand(t, pw, fmt.Sprintf("execute if block %s %s %s minecraft:air", x, y, z))
	base := len(obs.chats)
	boomPump(t, c, pr, pw, obs, 800*time.Millisecond)
	for _, ln := range obs.chats[base:] {
		if strings.Contains(ln, "conditional.pass") || strings.Contains(ln, "passed") {
			return true
		}
		if strings.Contains(ln, "conditional.fail") || strings.Contains(ln, "failed") {
			return false
		}
	}
	return false
}

func TestCreeperBoomParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	run := func(addr string) []string {
		var out []string
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		_ = c.SetDeadline(time.Now().Add(240 * time.Second))
		obs := &boomObs{}
		sendCommand(t, pw, "gamemode survival P")
		sendCommand(t, pw, "gamerule minecraft:natural_health_regeneration false")
		sendCommand(t, pw, "effect give P minecraft:absorption 600 250")
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		sendCommand(t, pw, "time set 6000")
		sendCommand(t, pw, "fill ~-3 ~-1 ~-3 ~30 ~-1 ~3 minecraft:dirt")
		boomPump(t, c, pr, pw, obs, 800*time.Millisecond)

		// boom: creeper in swell range explodes ~1.5s later, radius 3.
		sendCommand(t, pw, "summon minecraft:creeper ~3 ~ ~")
		boomPump(t, c, pr, pw, obs, 5*time.Second)
		out = append(out, fmt.Sprintf("boom explode=%v dmg=%v radius=%v",
			obs.explodes > 0, obs.dmgEvent > 0, obs.radius))

		carved := 0
		for _, x := range []string{"~1", "~2", "~3", "~4"} {
			if blockIsAir(t, c, pr, pw, obs, x, "~-1", "~") {
				carved++
			}
		}
		out = append(out, fmt.Sprintf("carve=%v", carved > 0))

		// charged: powered NBT doubles explosionRadius 3 → 6.
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		sendCommand(t, pw, "fill ~-4 ~-1 ~-4 ~40 ~-1 ~4 minecraft:dirt")
		obs.radius, obs.explodes = 0, 0
		boomPump(t, c, pr, pw, obs, 700*time.Millisecond)
		sendCommand(t, pw, "summon minecraft:creeper ~3 ~ ~ {powered:1b}")
		boomPump(t, c, pr, pw, obs, 5*time.Second)
		out = append(out, fmt.Sprintf("charged explode=%v radius=%v",
			obs.explodes > 0, obs.radius))

		// ignited+NoAI: Creeper.tick bookkeeping blows it up anyway.
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		obs.explodes, obs.radius = 0, 0
		boomPump(t, c, pr, pw, obs, 700*time.Millisecond)
		sendCommand(t, pw, "summon minecraft:creeper ~20 ~ ~ {NoAI:1b,ignited:1b}")
		boomPump(t, c, pr, pw, obs, 4*time.Second)
		out = append(out, fmt.Sprintf("noai explode=%v radius=%v",
			obs.explodes > 0, obs.radius))
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
