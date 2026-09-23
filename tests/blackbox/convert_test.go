//go:build blackbox

// Environmental conversion parity (26.2):
//   - Zombie.doUnderWaterConversion: eye submerged 600 ticks starts a
//     300-tick conversion (DATA_DROWNED_CONVERSION_ID, zombie meta 17)
//     that finishes even if the mob surfaces — zombie/zombie_villager
//     -> drowned, husk -> zombie first.
//   - Skeleton.tick's freeze chain: 140 ticks in powder snow, then a
//     300-tick DATA_STRAY_CONVERSION_ID countdown that CANCELS if the
//     mob steps out, converting skeleton -> stray.
// convertTo(single(keepEquipment, preserveName)) spawns the fresh
// entity before discarding the old; a non-silent conversion emits
// level events 1040 (drowned) / 1048 (stray).

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

type convObs struct {
	chats      []string
	lvlEvents  []int32
	spawnTypes []int32
	removes    int
}

func convPump(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, obs *convObs, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		p, err := pr.ReadPacket()
		if err != nil {
			continue
		}
		switch p.ID {
		case server.IDPlayKeepAliveCB:
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
		case server.IDPlaySyncPos:
			r := protocol.NewPacketReader(p.Data)
			id, _ := r.VarInt()
			var b protocol.Buffer
			b.VarInt(id)
			_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
		case server.IDPlaySystemChatCB:
			if txt := chatTextBlackbox(t, p.Data); txt != "" {
				obs.chats = append(obs.chats, txt)
			}
		case server.IDPlayLevelEvent:
			r := protocol.NewPacketReader(p.Data)
			ev, _ := r.Int()
			obs.lvlEvents = append(obs.lvlEvents, ev)
		case server.IDPlaySpawnEntity:
			r := protocol.NewPacketReader(p.Data)
			_, _ = r.VarInt()
			_, _ = r.Read(16)
			typ, _ := r.VarInt()
			obs.spawnTypes = append(obs.spawnTypes, typ)
		case server.IDPlayRemoveEntity:
			obs.removes++
		}
	}
}

// convHasEntity runs `execute if entity <sel>` — the bare conditional
// replies commands.execute.conditional.pass / .fail.
func convHasEntity(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, obs *convObs, sel string) bool {
	sendCommand(t, pw, "execute if entity "+sel)
	base := len(obs.chats)
	deadline := time.Now().Add(900 * time.Millisecond)
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
		p, err := pr.ReadPacket()
		if err != nil {
			continue
		}
		if p.ID == server.IDPlaySystemChatCB {
			if txt := chatTextBlackbox(t, p.Data); txt != "" {
				obs.chats = append(obs.chats, txt)
			}
		} else if p.ID == server.IDPlayKeepAliveCB {
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
		}
	}
	for _, ln := range obs.chats[base:] {
		if strings.Contains(ln, "conditional.pass") {
			return true
		}
		if strings.Contains(ln, "conditional.fail") {
			return false
		}
	}
	return false
}

func TestMobConvertParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	run := func(addr string) []string {
		var out []string
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		_ = c.SetDeadline(time.Now().Add(300 * time.Second))
		obs := &convObs{}
		sendCommand(t, pw, "gamemode creative P")
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		convPump(t, c, pr, pw, obs, 600*time.Millisecond)

		// Drown leg: a walled 3x3x3 water pool keeps both the zombie
		// and the converted drowned inside; night keeps them off the
		// sun-burn path during the 45s wait.
		sendCommand(t, pw, "time set 18000")
		sendCommand(t, pw, "fill ~-2 ~-1 ~-2 ~2 ~3 ~2 minecraft:stone hollow")
		sendCommand(t, pw, "fill ~-1 ~ ~-1 ~1 ~2 ~1 minecraft:water")
		obs.lvlEvents = nil
		obs.spawnTypes = nil
		obs.removes = 0
		sendCommand(t, pw, "summon minecraft:zombie ~ ~1 ~ {}")
		convPump(t, c, pr, pw, obs, 50*time.Second)
		drowned := convHasEntity(t, c, pr, pw, obs, "@e[type=minecraft:drowned]")
		noZombie := !convHasEntity(t, c, pr, pw, obs, "@e[type=minecraft:zombie]")
		saw1040 := false
		for _, ev := range obs.lvlEvents {
			if ev == 1040 {
				saw1040 = true
			}
		}
		out = append(out, fmt.Sprintf("drown drowned=%v gone=%v ev1040=%v", drowned, noZombie, saw1040))
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		sendCommand(t, pw, "fill ~-2 ~-1 ~-2 ~2 ~3 ~2 minecraft:air")
		convPump(t, c, pr, pw, obs, 600*time.Millisecond)

		// Freeze leg: a walled stone box keeps the skeleton inside a
		// 3x3 powder-snow patch — 22s to stray.
		sendCommand(t, pw, "fill ~-2 ~-1 ~-2 ~2 ~2 ~2 minecraft:stone hollow")
		sendCommand(t, pw, "fill ~-1 ~ ~-1 ~1 ~1 ~1 minecraft:powder_snow")
		obs.lvlEvents = nil
		sendCommand(t, pw, "summon minecraft:skeleton ~ ~ ~ {}")
		convPump(t, c, pr, pw, obs, 28*time.Second)
		stray := convHasEntity(t, c, pr, pw, obs, "@e[type=minecraft:stray]")
		noSkel := !convHasEntity(t, c, pr, pw, obs, "@e[type=minecraft:skeleton]")
		saw1048 := false
		for _, ev := range obs.lvlEvents {
			if ev == 1048 {
				saw1048 = true
			}
		}
		out = append(out, fmt.Sprintf("freeze stray=%v gone=%v ev1048=%v", stray, noSkel, saw1048))
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
