package blackbox

// TestEquineFlagsParity pins AbstractHorse's transient DATA_ID_FLAGS
// bits 0x20 (standing) and 0x40 (open mouth) on the wire. A stick
// interact on an untamed horse runs Horse.mobInteract's !isTamed arm:
// makeMad → standIfPossible → setStanding(20) flips flag 0x20 and
// plays entity.horse.angry; tick()'s standCounter countdown clears
// the flag ~20 ticks later. A wheat interact runs handleEating:
// temper +3 (Temper NBT lands on 3), eating() → openMouth flips flag
// 0x40 and plays entity.horse.eat (pitch randomized 1±0.2 — the
// pitch+seed bytes are masked); mouthCounter clears the flag at >30.
// The wheat consume fires only in survival; the final data get pins
// Temper:3.
//
// The horse is summoned on glass — a graze-able grass_block below
// would let the 1/300 eating-roll pulse flag 0x10 nondeterministically.
// Positional sound packets keep only id/src/coords: pitch and seed
// are random on vanilla, so the last 12 bytes (float + long) are cut.
// Ambient voice emits (horse.breathe etc.) are filtered out — only
// the two pinned ids pass the 0x75 prefix gate.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestEquineFlagsParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	tag := fmt.Sprintf("eq%d", time.Now().UnixNano()%1_000_000)
	sel := fmt.Sprintf("@e[type=minecraft:horse,tag=%s,limit=1]", tag)
	// ZZINTERACT steps send the raw interact packet; everything else
	// is a chat command.
	steps := []string{
		"fill 13 -61 7 16 -59 10 minecraft:glass hollow",
		"summon minecraft:horse 14.5 -60 8.5 {NoAI:1b,PersistenceRequired:1b,Tags:[\"" + tag + "\"]}",
		"tp P 12.5 -60 8.5",
		"item replace entity P weapon.mainhand with minecraft:stick",
		"ZZINTERACT:mad",
		"tick sprint 25",
		"item replace entity P weapon.mainhand with minecraft:wheat",
		"ZZINTERACT:feed",
		"tick sprint 35",
		"data get entity " + sel,
	}

	horseType, _ := server.EntityTypeID("horse")
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
		sendCommand(t, pw, "gamemode survival P")
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		time.Sleep(1400 * time.Millisecond)
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		time.Sleep(800 * time.Millisecond)

		sink.take()
		var out [][]string
		var all []string
		var horseEid int32
		for _, cmd := range steps {
			sink.take()
			if strings.HasPrefix(cmd, "ZZINTERACT:") {
				// The summon step's add_entity already drained into
				// `all` — the eid is stable by now.
				if horseEid == 0 {
					horseEid = aniSpawnID(all, horseType, 14.5, 8.5)
				}
				var ib protocol.Buffer
				ib.VarInt(horseEid)
				ib.VarInt(0) // action: interact
				ib.VarInt(0) // main hand
				ib.Bool(false)
				_ = pw.WritePacket(server.IDPlayInteractSB, ib.Bytes())
				// A command bracket: the reply lands after the
				// interact's emits on both servers' read ordering.
				sendCommand(t, pw, "say ZZ"+cmd[11:])
				time.Sleep(550 * time.Millisecond)
			} else {
				sendCommand(t, pw, cmd)
				time.Sleep(550 * time.Millisecond)
			}
			var evs []string
			for _, line := range sink.take() {
				all = append(all, line)
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
				// Positional play_sound (0x75): keep only the angry
				// (852 = varint d4 06) and eat (860 = dc 06) wire
				// ids — ambient voice emits are random-timed and
				// drop out. Mask pitch+seed — the last 12 bytes.
				if strings.HasPrefix(line, "0x75:") {
					body := line[5:]
					if len(body) < 2 || (byte(body[0]) != 0xd4 && byte(body[0]) != 0xdc) || byte(body[1]) != 0x06 {
						continue
					}
					if len(body) > 12 {
						body = body[:len(body)-12]
					}
					evs = append(evs, "pkt "+line[:4]+" "+fmt.Sprintf("%q", body))
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
	for i, cmd := range steps {
		if fmt.Sprintf("%v", want[i]) != fmt.Sprintf("%v", got[i]) {
			t.Errorf("step %q:\n  vanilla %v\n  mc-go   %v", cmd, want[i], got[i])
		}
	}
}
