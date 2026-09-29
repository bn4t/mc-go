//go:build blackbox

package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestGoatHornParity pins InstrumentItem.use: the entity-anchored
// (0x74) horn sound at volume range/16 with SoundSource.RECORDS and
// the tooter excluded, the 140-tick cooldown + reuse gate, and the
// instrument component's FAIL/no-op when removed.
func TestGoatHornParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("HP", "HQ"),
	}, nil)
	defer targets.cleanup()

	// hornSnd scans 0x74 lines for (snd reg id, src).
	hornSnd := func(lines []string, wantSnd, wantSrc int32) bool {
		for _, l := range lines {
			i := strings.IndexByte(l, ':')
			if i < 0 || l[:i] != "0x74" {
				continue
			}
			r := protocol.NewPacketReader([]byte(l[i+1:]))
			snd, e1 := r.VarInt()
			src, e2 := r.VarInt()
			if e1 != nil || e2 != nil {
				continue
			}
			if snd-1 == wantSnd && src == wantSrc {
				return true
			}
		}
		return false
	}
	// hasCooldown finds the 0x16 set_cooldown packet for goat_horn:
	// UTF identifier + VarInt ticks (use_duration 7.0 -> 140).
	hasCooldown := func(lines []string) bool {
		for _, l := range lines {
			i := strings.IndexByte(l, ':')
			if i < 0 || l[:i] != "0x16" {
				continue
			}
			r := protocol.NewPacketReader([]byte(l[i+1:]))
			id, e1 := r.String()
			ticks, e2 := r.VarInt()
			if e1 == nil && e2 == nil && id == "minecraft:goat_horn" && ticks == 140 {
				return true
			}
		}
		return false
	}

	run := func(addr string) map[string]string {
		c, pr, pw := joinPlayBlackbox(t, addr, "HP")
		defer c.Close()
		c2, pr2, pw2 := joinPlayBlackbox(t, addr, "HQ")
		defer c2.Close()
		sinkP := newChatSinkTP(pr, pw)
		sinkQ := newChatSinkTP(pr2, pw2)
		sendClientInformationVD(t, pw, 2)
		sendClientInformationVD(t, pw2, 2)
		out := map[string]string{}
		pLines, qLines := []string{}, []string{}
		take := func() { pLines = append(pLines, sinkP.take()...); qLines = append(qLines, sinkQ.take()...) }

		drain := func() {
			end := time.Now().Add(90 * time.Second)
			for time.Now().Before(end) {
				sendCommand(t, pw, "say ZZDRAIN")
				take()
				for _, l := range pLines {
					if strings.Contains(l, "ZZDRAIN") {
						return
					}
				}
				time.Sleep(500 * time.Millisecond)
			}
		}
		waitHeld := func(item string) {
			sendCommand(t, pw, fmt.Sprintf(`execute if entity @s[nbt={SelectedItem:{id:"minecraft:%s"}}] run say ZZHLD`, item))
			end := time.Now().Add(25 * time.Second)
			for time.Now().Before(end) {
				take()
				for _, l := range pLines {
					if strings.Contains(l, "ZZHLD") {
						return
					}
				}
				time.Sleep(30 * time.Millisecond)
			}
		}
		useItem := func() {
			var ib protocol.Buffer
			ib.VarInt(0) // main hand
			ib.VarInt(0) // sequence
			ib.Float(0)  // yaw
			ib.Float(0)  // pitch
			if err := pw.WritePacket(server.IDPlayUseItemSB, ib.Bytes()); err != nil {
				t.Logf("use write: %v", err)
			}
		}
		// hornLeg gives `patch` to HP, uses it, and returns the emit
		// flags found in the accumulated lines.
		hornLeg := func(patch string, sndID int32, mk string) {
			sendCommand(t, pw, "clear HP")
			if patch != "" {
				sendCommand(t, pw, fmt.Sprintf("item replace entity HP weapon.mainhand with minecraft:goat_horn%s", patch))
			} else {
				sendCommand(t, pw, "item replace entity HP weapon.mainhand with minecraft:goat_horn")
			}
			waitHeld("goat_horn")
			pLines = pLines[:0]
			qLines = qLines[:0]
			take()
			useItem()
			time.Sleep(800 * time.Millisecond)
			take()
			out[mk+"Q"] = "miss"
			if hornSnd(qLines, sndID, 2) {
				out[mk+"Q"] = "hit"
			}
			out[mk+"P"] = "hit" // present means BAD here — tooter is excluded
			if hornSnd(pLines, sndID, 2) {
				out[mk+"P"] = "leak"
			}
			out[mk+"CD"] = "miss"
			if hasCooldown(pLines) {
				out[mk+"CD"] = "hit"
			}
		}

		sendCommand(t, pw, "gamerule doMobSpawning false")
		sendCommand(t, pw, "time set 18000")
		sendCommand(t, pw, "gamemode survival HP")
		sendCommand(t, pw, "gamemode survival HQ")
		sendCommand(t, pw, "tp HP 0 -60 0")
		sendCommand(t, pw, "tp HQ 3 -60 0")
		drain()

		// Leg A: bare goat_horn — ponder default, snd.0=841.
		hornLeg("", 841, "pon")
		// Leg B: reuse inside the 140-tick cooldown — silent.
		pLines = pLines[:0]
		qLines = qLines[:0]
		take()
		useItem()
		time.Sleep(500 * time.Millisecond)
		take()
		out["hotQ"] = "hit"
		if hornSnd(qLines, 841, 2) {
			out["hotQ"] = "leak"
		}
		// Leg C: seek patch — snd.2=843. Sprint past the 140-tick
		// cooldown first on both servers.
		sendCommand(t, pw, "tick sprint 150")
		drain()
		hornLeg(`[minecraft:instrument="minecraft:seek_goat_horn"]`, 843, "sk")
		// Leg D: !instrument — FAIL, nothing emits.
		sendCommand(t, pw, "tick sprint 150")
		drain()
		hornLeg(`[!minecraft:instrument]`, 841, "no")
		return out
	}

	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	t.Logf("vanilla %v", van)
	t.Logf("mc-go   %v", mc)
	want := map[string]string{
		"ponQ": "hit", "ponP": "hit", "ponCD": "hit",
		"hotQ": "hit",
		"skQ":  "hit", "skP": "hit", "skCD": "hit",
		"noQ": "miss", "noP": "hit", "noCD": "miss",
	}
	for k, w := range want {
		if van[k] != w {
			t.Errorf("leg %s vanilla=%s want %s", k, van[k], w)
		}
		if mc[k] != w {
			t.Errorf("leg %s mc-go=%s want %s", k, mc[k], w)
		}
	}
}
