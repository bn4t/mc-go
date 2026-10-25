//go:build blackbox

// Blackbox parity pin for the fishing *catch* path: while the bobber
// bobs, vanilla runs the catchingFish model — lured countdown, splash
// teases, the hooked countdown with its fishing-approach stream, then
// a bite (DATA_BITING=true + bubbles/fishing bursts) with a 20-40-tick
// nibble window. Reeling in while biting rolls the fishing loot table,
// throws the catch at the angler, spawns a 1-6 XP orb, fires the
// fishing_rod_hooked trigger, and wears the rod by 1. Measured from
// FishingHook.java + fishing.json on the 26.2 jar.
package blackbox

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestFishingCatchParity casts into an open-water pool, waits for the
// DATA_BITING emit, reels in, and pins on BOTH servers: the biting emit
// arrives as a single-field metadata packet, the catch lands as an item
// entity, an XP orb spawns, and the rod takes exactly 1 durability.
// `/tick rate 40` halves the lured wait; the catch assertions are
// tick-based so they hold at any rate.
func TestFishingCatchParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB", "BOATC"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	type catchLeg struct {
		bitHist  []string // "eid:v" idx9 boolean emits, unscoped
		metaHist []string // "eid:i1/i2/.." per metadata packet
		partHist []int32  // particle_type ids seen
		adds     map[string]int
		bobbers  []int32
		stDmg    string
		dmg      float64
	}

	run := func(t *testing.T, name, addr string) catchLeg {
		v := joinVehPair(t, addr)
		defer v.ca.Close()
		defer v.cb.Close()
		cc, prc, pwc := joinPlayBlackbox(t, addr, "BOATC")
		defer cc.Close()
		drainChunkWindow(t, "fishcatch", v.ca, v.pra, v.pwa, 0)
		sink := newChatSinkTP(prc, pwc)

		leg := catchLeg{adds: map[string]int{}}
		fold := func(o vehObs) {
			leg.bitHist = append(leg.bitHist, o.bitHist...)
			leg.metaHist = append(leg.metaHist, o.metaHist...)
			leg.partHist = append(leg.partHist, o.partHist...)
			leg.bobbers = append(leg.bobbers, o.bobberEIDs...)
			for k, n := range o.adds {
				leg.adds[k] += n
			}
		}

		sendCommand(t, v.pwa, "fill 4 -61 4 24 -61 24 minecraft:stone")
		sendCommand(t, v.pwa, "fill 4 -62 4 24 -62 24 minecraft:stone")
		sendCommand(t, v.pwa, "fill 6 -62 6 12 -61 12 minecraft:water")
		sendCommand(t, v.pwa, "gamemode spectator BOATB")
		sendCommand(t, v.pwa, "tp BOATB 60 -60 60")
		sendCommand(t, v.pwa, "gamemode spectator BOATC")
		sendCommand(t, v.pwa, "tp BOATC 60 -60 60")
		// Survival: creative hurtAndBreak is a no-op on vanilla, so the
		// rod wear read needs BOATA holding the rod in survival.
		sendCommand(t, v.pwa, "gamemode survival BOATA")
		sendCommand(t, v.pwa, "tp BOATA 8.5 -60 14.5 180 30")
		sendCommand(t, v.pwa, "item replace entity BOATA weapon.mainhand with minecraft:fishing_rod")
		sendCommand(t, v.pwa, "tick rate 40")
		time.Sleep(600 * time.Millisecond)
		sink.take()

		yaw, pitch := float32(180), float32(30)
		use := func() {
			var ub protocol.Buffer
			ub.VarInt(0)
			ub.VarInt(0)
			ub.Float(yaw)
			ub.Float(pitch)
			_ = v.pwa.WritePacket(server.IDPlayUseItemSB, ub.Bytes())
		}

		use() // cast
		// lured is 100-600 ticks + hooked 20-80 before the first bite
		// window — worst case ~17s wall at 40 tps; poll with slack.
		biting := false
		bitEid := int32(0)
		deadline := time.Now().Add(60 * time.Second)
		for !biting && time.Now().Before(deadline) {
			fold(vehWatch(t, v, false, 0, 300*time.Millisecond))
			for _, h := range leg.bitHist {
				if strings.HasSuffix(h, ":1") {
					biting = true
					e, _ := strconv.Atoi(h[:strings.Index(h, ":")])
					bitEid = int32(e)
				}
			}
		}
		if !biting {
			t.Fatalf("%s: bobber never bit within 60s; bitHist=%v metaTail=%v",
				name, leg.bitHist,
				leg.metaHist[max(0, len(leg.metaHist)-8):])
		}
		// Reel in inside the nibble window (20-40 ticks = 0.5-1s wall).
		use()
		for i := 0; i < 6; i++ {
			fold(vehWatch(t, v, false, 0, 400*time.Millisecond))
		}
		leg.stDmg, leg.dmg = dataGetSel(t, sink, pwc, "BOATA",
			`SelectedItem.components."minecraft:damage"`)

		// Pin the emit shape now while bitEid is known: the metadata
		// packet that carried idx9 on the biting bobber must carry
		// exactly one field — vanilla emits each dirty field alone.
		for _, m := range leg.metaHist {
			colon := strings.Index(m, ":")
			e, _ := strconv.Atoi(m[:colon])
			if int32(e) != bitEid {
				continue
			}
			fields := strings.Split(m[colon+1:], "/")
			has9 := false
			for _, f := range fields {
				if f == "9" {
					has9 = true
				}
			}
			if has9 && len(fields) != 1 {
				t.Fatalf("%s: biting emit bundled with other fields: %s",
					name, m)
			}
		}
		return leg
	}

	vanilla := run(t, "vanilla", targets.vanilla)
	mcgo := run(t, "mc-go", targets.mcgo)

	for i, leg := range []catchLeg{vanilla, mcgo} {
		name := []string{"vanilla", "mc-go"}[i]
		if len(leg.bobbers) == 0 {
			t.Fatalf("%s: no fishing_bobber spawn seen", name)
		}
		// The catch must land: an item entity plus an XP orb.
		if leg.adds["item"] < 1 {
			t.Fatalf("%s: no item entity spawned on retrieve; adds=%v",
				name, leg.adds)
		}
		if leg.adds["experience_orb"] < 1 {
			t.Fatalf("%s: no experience_orb spawned on retrieve; adds=%v",
				name, leg.adds)
		}
		// A caught fish costs 1 rod durability (3/5 only on entity hooks).
		if leg.stDmg != "query" || leg.dmg != 1 {
			t.Fatalf("%s: rod damage after catch = (%s,%v), want (query,1)",
				name, leg.stDmg, leg.dmg)
		}
	}
}
