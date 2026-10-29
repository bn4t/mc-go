//go:build blackbox

package blackbox

import (
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// crystalLog renders a trace into comparable lines: crystal spawns and
// data by role (c1, c2, ... in spawn order), removals of crystals,
// explosions, sounds and chat. Item entities and block updates are
// left out (drops are random).
type crystalLog struct {
	roles map[int32]string
	next  int
	lines []string
}

func (l *crystalLog) role(eid int32) (string, bool) {
	r, ok := l.roles[eid]
	return r, ok
}

func (l *crystalLog) add(t *testing.T, ps []dfPkt) {
	crystal, _ := server.EntityTypeID("end_crystal")
	for _, p := range ps {
		switch p.id {
		case 0x01:
			s, ok := dfDecodeSpawn(p.data)
			if !ok || s.typ != crystal {
				continue
			}
			l.next++
			r := fmt.Sprintf("c%d", l.next)
			l.roles[s.eid] = r
			l.lines = append(l.lines, fmt.Sprintf("spawn %s pos=(%.3f,%.3f,%.3f) pitch=%d yaw=%d head=%d data=%d",
				r, s.x, s.y, s.z, s.pitch, s.yaw, s.headYaw, s.data))
		case 0x63:
			r := protocol.NewPacketReader(p.data)
			eid, _ := r.VarInt()
			if role, ok := l.role(eid); ok {
				rest, _ := r.Read(r.Remaining())
				l.lines = append(l.lines, "data "+role+" "+hex.EncodeToString(rest))
			}
		case 0x4D:
			r := protocol.NewPacketReader(p.data)
			n, _ := r.VarInt()
			for i := int32(0); i < n; i++ {
				eid, _ := r.VarInt()
				if role, ok := l.role(eid); ok {
					l.lines = append(l.lines, "remove "+role)
				}
			}
		case 0x24:
			ep := decodeExplode(t, p.data)
			// Each ray's strength rolls radius*(0.7+0.6r): the block
			// count is random, its presence is not.
			l.lines = append(l.lines, fmt.Sprintf("explode (%.3f,%.3f,%.3f) r=%v blocks>0=%v kb=%v", ep.x, ep.y, ep.z, ep.radius, ep.blockCount > 0, ep.hasKB))
		case 0x75:
			r := protocol.NewPacketReader(p.data)
			snd, _ := r.VarInt()
			src, _ := r.VarInt()
			if snd-1 == 911 {
				continue // /give's pickup sound, outside this pin
			}
			l.lines = append(l.lines, fmt.Sprintf("sound %d src %d", snd-1, src))
		case 0x79:
			txt := chatTextBlackbox(t, p.data)
			if i := strings.Index(txt, "UUIDI"); i >= 0 {
				if j := strings.Index(txt[i:], "Rotation"); j >= 0 {
					txt = txt[:i] + "UUID<random>" + txt[i+j:]
				}
			}
			if strings.HasPrefix(txt, "commands.kill.success") {
				txt = "commands.kill.success" // the blast's random drops
			}
			l.lines = append(l.lines, "chat "+txt)
		}
	}
}

// crystalStep sends one command (or runs an action) and records what
// follows for d.
func crystalStep(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, l *crystalLog, d time.Duration, act func()) {
	t.Helper()
	act()
	l.add(t, dfTrace(t, c, pr, pw, d, nil))
}

// TestEndCrystalParity pins EndCrystal in the overworld of the flat
// harness world (no dragon fight, so no fire): a bare summon pairs with
// no data; a tagged summon (ShowBottom 0, beam_target, Invulnerable)
// pairs with both entries and re-sends them dirty; /data get renders
// the save keys; a creative punch removes even an invulnerable crystal,
// the blast (power 6, block interaction) removes the other crystal in
// range without a second blast, then the attack sound; the item
// places a bottomless crystal on obsidian with room above and fails on
// an occupied or non-obsidian block.
func TestEndCrystalParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("CrystalProbe")}, nil)
	defer tg.cleanup()
	run := func(addr string) []string {
		c, pr, pw := joinPlayBlackboxView(t, addr, "CrystalProbe", 8)
		defer c.Close()
		l := &crystalLog{roles: map[int32]string{}}
		cmd := func(s string) func() { return func() { sendCommand(t, pw, s) } }
		dfTrace(t, c, pr, pw, 3*time.Second, nil)
		crystalStep(t, c, pr, pw, l, 1500*time.Millisecond, cmd("tp @s 0.5 -60 0.5 0 0"))
		l.lines = nil
		crystalStep(t, c, pr, pw, l, time.Second, cmd("summon end_crystal 0.5 -60 3.5"))
		crystalStep(t, c, pr, pw, l, time.Second, cmd("summon end_crystal 3.5 -60 3.5 {ShowBottom:0b,beam_target:[I;1,2,3],Invulnerable:1b,Rotation:[45f,0f]}"))
		crystalStep(t, c, pr, pw, l, time.Second, cmd("data get entity @e[type=end_crystal,limit=1,sort=furthest]"))
		var invulnerable int32
		for eid, r := range l.roles {
			if r == "c2" {
				invulnerable = eid
			}
		}
		crystalStep(t, c, pr, pw, l, 2*time.Second, func() {
			var b protocol.Buffer
			b.VarInt(invulnerable)
			_ = pw.WritePacket(server.IDPlayAttackSB, b.Bytes())
		})
		crystalStep(t, c, pr, pw, l, time.Second, cmd("kill @e[type=item]"))
		crystalStep(t, c, pr, pw, l, time.Second, cmd("setblock 0 -61 -3 obsidian"))
		crystalStep(t, c, pr, pw, l, time.Second, cmd("give @s end_crystal"))
		crystalStep(t, c, pr, pw, l, time.Second, cmd("tp @s 0.5 -60 0.5 180 60"))
		crystalStep(t, c, pr, pw, l, 1500*time.Millisecond, func() { sendUseItemOn(t, pw, 0, -61, -3, 1, 1) })
		crystalStep(t, c, pr, pw, l, time.Second, cmd("data get entity @e[type=end_crystal,limit=1,sort=nearest]"))
		crystalStep(t, c, pr, pw, l, 1500*time.Millisecond, func() { sendUseItemOn(t, pw, 0, -61, -3, 1, 2) })
		crystalStep(t, c, pr, pw, l, time.Second, cmd("setblock 2 -61 -3 dirt"))
		crystalStep(t, c, pr, pw, l, 1500*time.Millisecond, func() { sendUseItemOn(t, pw, 2, -61, -3, 1, 3) })
		crystalStep(t, c, pr, pw, l, time.Second, cmd("execute if entity @e[type=end_crystal]"))
		return l.lines
	}
	v := run(tg.vanilla)
	m := run(tg.mcgo)
	t.Logf("vanilla:\n%s", strings.Join(v, "\n"))
	if strings.Join(v, "\n") != strings.Join(m, "\n") {
		t.Errorf("crystal trace differs\nvanilla:\n%s\nmc-go:\n%s", strings.Join(v, "\n"), strings.Join(m, "\n"))
	}
}
