package blackbox

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestPhantomSweepParity pins the Phantom goal set (javap-measured, see
// internal/server/phantom.go):
//
//   - Bite leg: a bare-summoned phantom (finalizeSpawn anchors 5 above
//     its block) above a survival player scans for targets after
//     reducedTickDelay(20) evaluations — 20 ticks — and AttackStrategy
//     flips to SWOOP adjusted(10) evaluations later, so
//     entity.phantom.swoop (vol 10) lands ~1.5 s after the spawn
//     (timed from add_entity, which can trail the summon by several
//     ticks on vanilla — hence the loose lower bound). The
//     swoop ends in a bite: doHurtTarget's attack_damage — a size-0
//     phantom keeps the supplier's 2, since writing the define
//     default never fires onSyncedDataUpdated — with level event 1039
//     beside it. ID_SIZE (index 16) stays off the wire at 0.
//   - Size leg: {size:3} rebases attack_damage to 6+3, so the first
//     bite takes 9, and ID_SIZE packs 3.
//   - Sun leg: in daylight the burn_in_daylight tick lights it (shared
//     flags bit 0) within a few seconds.
func TestPhantomSweepParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()
	phType, _ := server.EntityTypeID("phantom")
	swoopID, _ := server.SoundEventID("entity.phantom.swoop")

	type leg struct {
		spawned  time.Time
		swoop    time.Time
		bite     time.Time
		bitePos  bool
		health   float32
		sizeMeta int64 // -1 = absent
	}
	type result struct {
		small, big leg
		burned     bool
	}

	run := func(addr string) result {
		var r result
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		fw := startFlyerWire(t, c, pr, pw)
		sendCommand(t, pw, "gamemode creative P")
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		sendCommand(t, pw, "gamerule minecraft:natural_health_regeneration false")
		sendCommand(t, pw, "time set 18000")
		sendCommand(t, pw, "tp P 0.5 -60 0.5 0 0")
		time.Sleep(800 * time.Millisecond)

		bite := func(summon string) leg {
			l := leg{health: -1, sizeMeta: -1}
			sendCommand(t, pw, "effect give P minecraft:instant_health 1 10 true")
			time.Sleep(500 * time.Millisecond)
			m := fw.mark()
			sendCommand(t, pw, summon)
			sendCommand(t, pw, "gamemode survival P")
			var eid int32 = -1
			deadline := time.Now().Add(20 * time.Second)
			for time.Now().Before(deadline) && l.health < 0 {
				time.Sleep(100 * time.Millisecond)
				for _, p := range fw.since(m) {
					switch p.id {
					case server.IDPlaySpawnEntity:
						if s, ok := fwDecodeSpawn(p); ok && s.typ == phType && eid < 0 {
							eid = s.eid
							l.spawned = p.at
						}
					case server.IDPlayEntityMetadata:
						id, ents := fwDecodeMeta(p.data)
						if id == eid {
							for _, e := range ents {
								if e.idx == 16 && e.typ == 1 {
									l.sizeMeta = e.i
								}
							}
						}
					case 0x75:
						rd := protocol.NewPacketReader(p.data)
						if v, err := rd.VarInt(); err == nil && v == swoopID+1 && l.swoop.IsZero() {
							l.swoop = p.at
						}
					case server.IDPlayLevelEvent:
						if ev, _, _, _, ok := fwLevelEvent(p); ok && ev == 1039 && l.bite.IsZero() {
							l.bite = p.at
						}
					case server.IDPlaySetHealth:
						if h, ok := fwHealth(p); ok && h < 20 && l.health < 0 {
							l.health = h
						}
					}
				}
			}
			sendCommand(t, pw, "gamemode creative P")
			sendCommand(t, pw, "kill @e[type=minecraft:phantom]")
			time.Sleep(500 * time.Millisecond)
			return l
		}
		r.small = bite("summon minecraft:phantom 0.5 -52 6.5")
		r.big = bite("summon minecraft:phantom 0.5 -52 6.5 {size:3}")

		// Sun leg.
		sendCommand(t, pw, "time set 6000")
		m := fw.mark()
		sendCommand(t, pw, "summon minecraft:phantom 20.5 -40 0.5 {NoAI:1b}")
		var eid int32 = -1
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) && !r.burned {
			time.Sleep(100 * time.Millisecond)
			for _, p := range fw.since(m) {
				switch p.id {
				case server.IDPlaySpawnEntity:
					if s, ok := fwDecodeSpawn(p); ok && s.typ == phType {
						eid = s.eid
					}
				case server.IDPlayEntityMetadata:
					id, ents := fwDecodeMeta(p.data)
					if id != eid {
						break
					}
					for _, e := range ents {
						if e.idx == 0 && e.typ == 0 && e.i&1 != 0 {
							r.burned = true
						}
					}
				}
			}
		}
		sendCommand(t, pw, "kill @e[type=minecraft:phantom]")
		return r
	}

	check := func(name string, r result) {
		for _, lg := range []struct {
			tag  string
			l    leg
			want float32
			size int64
		}{{"size0", r.small, 18, -1}, {"size3", r.big, 11, 3}} {
			l := lg.l
			t.Logf("%s %s: spawn→swoop %v spawn→bite %v health %.1f sizeMeta %d", name, lg.tag,
				l.swoop.Sub(l.spawned), l.bite.Sub(l.spawned), l.health, l.sizeMeta)
			if l.spawned.IsZero() {
				t.Errorf("%s %s: phantom spawn never seen", name, lg.tag)
				continue
			}
			if l.swoop.IsZero() {
				t.Errorf("%s %s: no entity.phantom.swoop", name, lg.tag)
			} else if g := l.swoop.Sub(l.spawned); g < 600*time.Millisecond || g > 2600*time.Millisecond {
				t.Errorf("%s %s: spawn→swoop %v, want ~1.5s (20+10 ticks)", name, lg.tag, g)
			}
			if l.health != lg.want {
				t.Errorf("%s %s: bite left health %.1f, want %.1f", name, lg.tag, l.health, lg.want)
			}
			if l.bite.IsZero() {
				t.Errorf("%s %s: no level event 1039 for the bite", name, lg.tag)
			}
			if l.sizeMeta != lg.size {
				t.Errorf("%s %s: ID_SIZE on the wire %d, want %d", name, lg.tag, l.sizeMeta, lg.size)
			}
		}
		if !r.burned {
			t.Errorf("%s: phantom never caught fire in daylight", name)
		}
	}
	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	check("vanilla", van)
	check("mc-go", mc)
}
