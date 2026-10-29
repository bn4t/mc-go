//go:build blackbox

package blackbox

import (
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestEnderDragonDeathParity pins the summoned dragon's pairing and
// its death in place: a /summon with a tag (no finalizeSpawn roll)
// pairs in HOVERING with health 200; with the clock frozen, a lethal
// player_attack /damage leaves it hovering (a sitting phase, so
// handleKillingBlow keeps the 0 health) dead with pose DYING; tickDeath
// then runs exactly 200 stepped ticks - global level event 1028 on the
// first, 500 experience in orbs (no fight: 8% every 5 ticks past 150,
// 20% on the last) - before the dragon leaves the level.
func TestEnderDragonDeathParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("DragonProbe")}, nil)
	defer tg.cleanup()
	dragonType, _ := server.EntityTypeID("ender_dragon")
	orbType, _ := server.EntityTypeID("experience_orb")
	run := func(addr string) []string {
		c, pr, pw := joinPlayBlackboxView(t, addr, "DragonProbe", 8)
		defer c.Close()
		var lines []string
		var dragon int32 = -1
		orbs := map[int32]bool{}
		awards := map[string]bool{}
		xp := int32(0)
		record := func(ps []dfPkt) {
			for _, p := range ps {
				r := protocol.NewPacketReader(p.data)
				switch p.id {
				case 0x01:
					s, _ := dfDecodeSpawn(p.data)
					if s.typ == dragonType {
						dragon = s.eid
						lines = append(lines, fmt.Sprintf("spawn dragon pos=(%.3f,%.3f,%.3f) yaw=%d pitch=%d head=%d data=%d", s.x, s.y, s.z, s.yaw, s.pitch, s.headYaw, s.data))
					}
					if s.typ == orbType {
						orbs[s.eid] = true
						// Each award spawns at the rising dragon's height;
						// orbs can merge into a neighbour (random), awards
						// cannot.
						awards[fmt.Sprintf("%.2f", s.y)] = true
					}
				case 0x63:
					eid, _ := r.VarInt()
					rest, _ := r.Read(r.Remaining())
					if eid == dragon {
						lines = append(lines, "data dragon "+hex.EncodeToString(rest))
					}
					if orbs[eid] && len(rest) > 3 && rest[0] == 8 {
						v, _ := protocol.NewPacketReader(rest[2:]).VarInt()
						xp += v
						delete(orbs, eid)
					}
				case 0x22:
					eid, _ := r.Int()
					ev, _ := r.Byte()
					if eid == dragon {
						lines = append(lines, fmt.Sprintf("entity_event dragon %d", ev))
					}
				case 0x2E:
					id, _ := r.Int()
					_, _ = r.Long()
					_, _ = r.Int()
					global, _ := r.Bool()
					lines = append(lines, fmt.Sprintf("level %d global %v", id, global))
				case 0x4D:
					n, _ := r.VarInt()
					for i := int32(0); i < n; i++ {
						if eid, _ := r.VarInt(); eid == dragon {
							lines = append(lines, "remove dragon")
						}
					}
				case 0x79:
					txt := chatTextBlackbox(t, p.data)
					if strings.Contains(txt, "conditional") || strings.Contains(txt, "damage.success") {
						lines = append(lines, "chat "+txt)
					}
				}
			}
		}
		dfTrace(t, c, pr, pw, 3*time.Second, nil)
		sendCommand(t, pw, "tp @s 0.5 -60 0.5")
		dfTrace(t, c, pr, pw, time.Second, nil)
		sendCommand(t, pw, "summon minecraft:ender_dragon 0.5 -40 24.5 {DragonPhase:10}")
		record(dfTrace(t, c, pr, pw, 1500*time.Millisecond, nil))
		sendCommand(t, pw, "tick freeze")
		record(dfTrace(t, c, pr, pw, time.Second, nil))
		sendCommand(t, pw, "damage @e[type=minecraft:ender_dragon,limit=1] 1000 minecraft:player_attack by DragonProbe")
		record(dfTrace(t, c, pr, pw, time.Second, nil))
		sendCommand(t, pw, "tick step 199")
		record(dfTrace(t, c, pr, pw, 14*time.Second, nil))
		sendCommand(t, pw, "execute if entity @e[type=minecraft:ender_dragon]")
		record(dfTrace(t, c, pr, pw, time.Second, nil))
		sendCommand(t, pw, "tick step 1")
		record(dfTrace(t, c, pr, pw, 2*time.Second, nil))
		sendCommand(t, pw, "execute if entity @e[type=minecraft:ender_dragon]")
		record(dfTrace(t, c, pr, pw, time.Second, nil))
		sendCommand(t, pw, "tick unfreeze")
		dfTrace(t, c, pr, pw, time.Second, nil)
		lines = append(lines, fmt.Sprintf("xp awards %d, at most 500 xp: %v", len(awards), xp > 0 && xp <= 500))
		return lines
	}
	v := run(tg.vanilla)
	m := run(tg.mcgo)
	t.Logf("vanilla:\n%s", strings.Join(v, "\n"))
	if strings.Join(v, "\n") != strings.Join(m, "\n") {
		t.Errorf("dragon death differs\nmc-go:\n%s", strings.Join(m, "\n"))
	}
}
