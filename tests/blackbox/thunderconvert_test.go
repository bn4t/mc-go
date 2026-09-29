//go:build blackbox

package blackbox

import (
	"strings"
	"testing"
	"time"
)

// TestThunderConvertParity pins LightningBolt.tick's strikeEntities
// per-type thunderHit overrides on the /summon bolt: pig converts to
// zombified_piglin (custom name + saddle ride along), villager to
// witch, creeper takes the hit then latches powered, and a plain cow
// just takes the 5-damage lightning hit.
func TestThunderConvertParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("TH", "TQ"),
	}, nil)
	defer targets.cleanup()

	run := func(addr string) map[string]string {
		c, pr, pw := joinPlayBlackbox(t, addr, "TH")
		defer c.Close()
		sink := newChatSinkTP(pr, pw)
		sendClientInformationVD(t, pw, 2)
		out := map[string]string{}
		var lines []string
		take := func() { lines = append(lines, sink.take()...) }

		drain := func() {
			end := time.Now().Add(90 * time.Second)
			for time.Now().Before(end) {
				sendCommand(t, pw, "say ZZDRAIN")
				take()
				for _, l := range lines {
					if strings.Contains(l, "ZZDRAIN") {
						return
					}
				}
				time.Sleep(500 * time.Millisecond)
			}
		}
		probe := func(cmd, marker string) {
			sendCommand(t, pw, cmd)
			end := time.Now().Add(25 * time.Second)
			for time.Now().Before(end) {
				take()
				for _, l := range lines {
					if strings.Contains(l, marker) {
						out[marker] = "hit"
						return
					}
				}
				time.Sleep(30 * time.Millisecond)
			}
			out[marker] = "miss"
		}

		sendCommand(t, pw, "gamerule doMobSpawning false")
		sendCommand(t, pw, "gamerule doFireTick false")
		sendCommand(t, pw, "time set 18000")
		sendCommand(t, pw, "gamemode spectator TH")
		sendCommand(t, pw, `summon minecraft:pig 0 -60 0 {NoAI:1b,Saddle:1b,CustomName:'"ZapPig"'}`)
		sendCommand(t, pw, `summon minecraft:villager 0.8 -60 0 {NoAI:1b}`)
		sendCommand(t, pw, `summon minecraft:creeper -0.8 -60 0 {NoAI:1b}`)
		sendCommand(t, pw, `summon minecraft:cow 0 -60 0.8 {NoAI:1b}`)
		probe(`execute if entity @e[type=minecraft:pig,name=ZapPig,limit=1] run say ZZPRENAME`, "ZZPRENAME")
		probe(`execute if entity @e[type=minecraft:pig,nbt={CustomName:'"ZapPig"'},limit=1] run say ZZPRENBT1`, "ZZPRENBT1")
		probe(`execute if entity @e[type=minecraft:pig,nbt={CustomName:"ZapPig"},limit=1] run say ZZPRENBT2`, "ZZPRENBT2")
		probe(`execute if entity @e[type=minecraft:pig,nbt={CustomName:{text:"ZapPig"}},limit=1] run say ZZPRENBT3`, "ZZPRENBT3")
		sendCommand(t, pw, `summon minecraft:lightning_bolt 0 -60 0`)
		time.Sleep(700 * time.Millisecond)
		drain()

		probe(`execute if entity @e[type=minecraft:zombified_piglin] run say ZZPIG`, "ZZPIG")
		// Dump the raw save maps so the name/saddle shapes are visible
		// on both servers before the subset probes below.
		for _, sel := range []string{"zombified_piglin", "witch", "creeper", "cow"} {
			sendCommand(t, pw, `data get entity @e[type=minecraft:`+sel+`,limit=1]`)
		}
		time.Sleep(1200 * time.Millisecond)
		take()
		for _, l := range lines {
			for _, k := range []string{"ZapPig", "saddle", "powered", "ExplosionRadius", "Health"} {
				if strings.Contains(l, k) {
					t.Logf("raw[%s]: %.1400s", k, l)
					break
				}
			}
		}
		probe(`execute unless entity @e[type=minecraft:pig] run say ZZNOPIG`, "ZZNOPIG")
		probe(`execute if entity @e[type=minecraft:witch] run say ZZWITCH`, "ZZWITCH")
		probe(`execute unless entity @e[type=minecraft:villager] run say ZZNOVILL`, "ZZNOVILL")
		probe(`execute if entity @e[type=minecraft:zombified_piglin,nbt={equipment:{mainhand:{id:"minecraft:golden_sword"}}},limit=1] run say ZZSWORD`, "ZZSWORD")
		probe(`execute if entity @e[type=minecraft:zombified_piglin,nbt={PersistenceRequired:1b},limit=1] run say ZZPERSIST`, "ZZPERSIST")
		probe(`execute if entity @e[type=minecraft:witch,nbt={PersistenceRequired:1b},limit=1] run say ZZWPERSIST`, "ZZWPERSIST")
		probe(`execute if entity @e[type=minecraft:zombified_piglin,nbt={CustomName:'"ZapPig"'},limit=1] run say ZZNAME`, "ZZNAME")
		probe(`execute if entity @e[type=minecraft:creeper,nbt={powered:1b},limit=1] run say ZZPWR`, "ZZPWR")
		probe(`execute unless entity @e[type=minecraft:creeper,nbt={Health:20.0f},limit=1] run say ZZCHP`, "ZZCHP")
		probe(`execute unless entity @e[type=minecraft:cow,nbt={Health:20.0f},limit=1] run say ZZCOHP`, "ZZCOHP")
		return out
	}

	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	t.Logf("vanilla %v", van)
	t.Logf("mc-go   %v", mc)
	for k, w := range map[string]string{
		"ZZPRENBT1": "hit", "ZZPRENBT2": "miss", "ZZPRENBT3": "miss",
		"ZZPIG": "hit", "ZZNOPIG": "hit", "ZZWITCH": "hit", "ZZNOVILL": "hit",
		"ZZSWORD": "hit", "ZZPERSIST": "hit", "ZZWPERSIST": "hit", "ZZNAME": "hit",
		"ZZPWR": "hit", "ZZCHP": "hit", "ZZCOHP": "hit",
	} {
		if van[k] != w {
			t.Errorf("leg %s vanilla=%s want %s", k, van[k], w)
		}
		if mc[k] != w {
			t.Errorf("leg %s mc-go=%s want %s", k, mc[k], w)
		}
	}
}
