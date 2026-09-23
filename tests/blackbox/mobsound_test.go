//go:build blackbox

package blackbox

import (
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// mobVoiceIds mirrors the mc-go voice table: hurt, death and
// secondary-death sound registry ids per summonable type, all measured
// on vanilla 26.2. {0,0} marks a silent pair (armor stands).
var mobVoiceIds = map[string][3]int32{
	"pig":              {1269, 1270, 0},
	"cow":              {451, 452, 0},
	"chicken":          {357, 358, 0},
	"sheep":            {1440, 1439, 0},
	"zombie":           {1881, 1874, 0},
	"zombie_villager":  {1904, 1903, 0},
	"villager":         {1698, 1697, 0},
	"wandering_trader": {1730, 1726, 0},
	"skeleton":         {1490, 1481, 0},
	"creeper":          {480, 479, 0},
	"spider":           {1580, 1579, 0},
	"sniffer":          {1555, 1556, 0},
	"iron_golem":       {900, 899, 898},
	"horse":            {862, 857, 0},
	"wolf":             {1806, 1804, 0},
	"cat":              {291, 292, 0},
	"ocelot":           {1187, 1189, 0},
	"parrot":           {1213, 1210, 0},
	"armadillo":        {55, 59, 0},
	"cod":              {382, 380, 0},
	"salmon":           {1395, 1393, 0},
	"pufferfish":       {1346, 1344, 0},
	"tropical_fish":    {1640, 1638, 0},
	"pillager":         {1299, 1298, 0},
	"ghast":            {707, 706, 0},
	"piglin":           {1286, 1284, 0},
	"sulfur_cube":      {1964, 1963, 0},
	"allay":            {3, 2, 0},
	"goat":             {738, 736, 0},
	"strider":          {1541, 1540, 0},
	"armor_stand":      {0, 0, 0},
	"husk":             {880, 879, 0},
	"drowned":          {566, 564, 0},
	"stray":            {1607, 1606, 0},
	"wither_skeleton":  {1787, 1786, 0},
	"cave_spider":      {1580, 1579, 0},
	"witch":            {1778, 1776, 0},
	"blaze":            {177, 176, 0},
	"phantom":          {1259, 1257, 0},
	"zombified_piglin": {1898, 1897, 0},
	"turtle":           {1662, 1657, 0},
	"ender_dragon":     {0, 0, 0},
}

// voiceSound is a sound/sound_entity packet decoded far enough to
// compare voices: holder id (minus the +1 inline offset), source,
// position (fixed-point *8) and volume.
type voiceSound struct {
	pkt     string
	id, src int32
	x, y, z int32
	vol     float32
}

func voiceSounds(lines []string) []voiceSound {
	var out []voiceSound
	for _, line := range lines {
		i := strings.IndexByte(line, ':')
		if i < 0 {
			continue
		}
		idHex, payload := line[:i], line[i+1:]
		if idHex != "0x74" && idHex != "0x75" {
			continue
		}
		r := protocol.NewPacketReader([]byte(payload))
		snd, err := r.VarInt()
		if err != nil {
			continue
		}
		srcID, err := r.VarInt()
		if err != nil {
			continue
		}
		ev := voiceSound{pkt: idHex, id: snd - 1, src: srcID}
		if idHex == "0x74" {
			if _, err := r.VarInt(); err != nil {
				continue
			}
		} else {
			var e error
			if ev.x, e = r.Int(); e != nil {
				continue
			}
			if ev.y, e = r.Int(); e != nil {
				continue
			}
			if ev.z, e = r.Int(); e != nil {
				continue
			}
		}
		vol, err := r.Float()
		if err != nil {
			continue
		}
		ev.vol = vol
		out = append(out, ev)
	}
	return out
}

// TestMobHurtDeathSoundParity asserts every summonable mob's hurt and
// death voice against vanilla: a 1-point mob_attack plays the type's
// hurt sound, a lethal hit plays the death sound (plus the iron
// golem's secondary damage groan) — both positional packets at the
// entity's feet, before the dying entity_event, and nothing for armor
// stands. Voices are compared as (packet id, sound id, source, x, y,
// z, volume) tuples; pitch is random on both sides and ignored.
// Ambient sounds, splash extras and the witch's random potion drink
// are filtered out — only the type's own voice ids count.
func TestMobHurtDeathSoundParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	// Left out of the sweep: enderman teleports away on hurt, so
	// whether the sound packet reaches a listener is position-random;
	// the four fish and the sulfur cube were measured live (their ids
	// are in the table) but the fixture can't hold them reliably —
	// fish despawn inside the leg window and the cube's summon
	// occasionally lands at another size with different voices.
	order := []string{
		"pig", "cow", "chicken", "sheep", "zombie", "zombie_villager",
		"villager", "wandering_trader", "skeleton", "creeper", "spider",
		"sniffer", "iron_golem", "horse", "wolf", "cat", "ocelot",
		"parrot", "armadillo",
		"pillager", "ghast", "piglin",
		"allay", "goat", "strider", "armor_stand", "husk", "drowned",
		"stray", "wither_skeleton", "cave_spider", "witch", "blaze",
		"phantom", "zombified_piglin", "turtle", "ender_dragon",
	}

	voiceSet := map[int32]bool{1793: true, 1792: true} // wolf_armor.damage/crack stay comparable
	for _, v := range mobVoiceIds {
		for _, id := range v {
			if id != 0 {
				voiceSet[id] = true
			}
		}
	}
	filter := func(evs []voiceSound) []voiceSound {
		var out []voiceSound
		for _, ev := range evs {
			if voiceSet[ev.id] {
				out = append(out, ev)
			}
		}
		return out
	}

	run := func(addr string) map[string][]voiceSound {
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSink(pr, pw)
		sink.cap = 1 << 22
		time.Sleep(400 * time.Millisecond)
		for _, cmd := range []string{
			"gamemode creative @s",
			"gamerule do_daylight_cycle false",
			"gamerule do_mob_spawning false",
			"gamerule do_mob_loot false",
			"gamerule doMobGriefing false",
			"gamerule max_entity_cramming 0",
			"time set 18000",
			"kill @e[type=minecraft:slime]",
		} {
			func() { defer func() { recover() }(); sendCommand(t, pw, cmd) }()
			time.Sleep(250 * time.Millisecond)
		}
		sink.take()
		out := map[string][]voiceSound{}
		for _, tp := range order {
			pos := "3.5 -60 3.5"
			func() {
				defer func() { recover() }()
				sendCommand(t, pw, "summon minecraft:"+tp+" "+pos+" {NoAI:1b,PersistenceRequired:1b}")
			}()
			time.Sleep(700 * time.Millisecond)
			sink.take()
			sel := `@e[type=minecraft:` + tp + `,sort=nearest,limit=1]`
			for _, leg := range []struct{ key, cmd string }{
				{"damage", "damage " + sel + " 1 minecraft:mob_attack"},
				{"kill", "kill " + sel},
			} {
				func() { defer func() { recover() }(); sendCommand(t, pw, leg.cmd) }()
				time.Sleep(900 * time.Millisecond)
				out[tp+":"+leg.key] = append(out[tp+":"+leg.key], filter(voiceSounds(sink.take()))...)
			}
		}
		return out
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	for _, tp := range order {
		v := mobVoiceIds[tp]
		for _, leg := range []string{"damage", "kill"} {
			w := want[tp+":"+leg]
			g := got[tp+":"+leg]
			if len(w) != len(g) {
				t.Errorf("%s %s: vanilla sent %v, mc-go sent %v", tp, leg, w, g)
				continue
			}
			for i := range w {
				if w[i] != g[i] {
					t.Errorf("%s %s: event %d vanilla %+v != mc-go %+v", tp, leg, i, w[i], g[i])
				}
			}
			// Anchors: the hurt leg must carry the measured hurt voice
			// first, the kill leg the death voice first.
			if leg == "damage" && (len(w) == 0) != (v[0] == 0) {
				t.Errorf("%s hurt: vanilla silent=%v but table id=%d", tp, len(w) == 0, v[0])
			}
			if leg == "damage" && len(w) > 0 && w[0].id != v[0] {
				t.Errorf("%s hurt: vanilla first voice %d, table has %d", tp, w[0].id, v[0])
			}
			if leg == "kill" && (len(w) == 0) != (v[1] == 0) {
				t.Errorf("%s death: vanilla silent=%v but table id=%d", tp, len(w) == 0, v[1])
			}
			if leg == "kill" && len(w) > 0 && w[0].id != v[1] {
				t.Errorf("%s death: vanilla first voice %d, table has %d", tp, w[0].id, v[1])
			}
		}
	}
}

// TestPlayerHurtSoundParity pins the player hurt/death voice: vanilla
// emits a positional sound at the victim's feet on the PLAYERS source
// to every listener EXCEPT the victim — a player's own client plays
// the hurt locally off the damage event. The damage type picks the
// variant (drown/freeze/on_fire/sweet_berry_bush); a lethal hit plays
// entity.player.death.
func TestPlayerHurtSoundParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P", "Q"),
	}, nil)
	defer targets.cleanup()

	types := []string{
		"mob_attack", "drown", "in_fire", "on_fire", "freeze",
		"sweet_berry_bush", "sting", "fall", "cramming", "magic",
		"explosion", "generic", "fly_into_wall",
	}
	// Only player-voice ids are compared — ambient and secondary noise
	// (e.g. the bee-sting playback) stay out of the diff.
	pvoice := map[int32]bool{1311: true, 1312: true, 1313: true, 1314: true, 1315: true, 1316: true}
	pfilter := func(evs []voiceSound) []voiceSound {
		var out []voiceSound
		for _, ev := range evs {
			if pvoice[ev.id] {
				out = append(out, ev)
			}
		}
		return out
	}

	type result struct {
		heard  [][]voiceSound // per damage type, as Q heard it
		victim [][]voiceSound // what P heard of its own hits
		death  []voiceSound
	}
	run := func(addr string) result {
		c1, pr1, pw1 := joinPlayBlackbox(t, addr, "P")
		defer c1.Close()
		c2, pr2, pw2 := joinPlayBlackbox(t, addr, "Q")
		defer c2.Close()
		_ = c1.SetDeadline(time.Time{})
		_ = c2.SetDeadline(time.Time{})
		_ = pw1.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		_ = pw2.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sinkP := newChatSink(pr1, pw1)
		sinkP.cap = 1 << 22
		sinkQ := newChatSink(pr2, pw2)
		sinkQ.cap = 1 << 22
		time.Sleep(600 * time.Millisecond)
		for _, cmd := range []string{
			"gamemode survival P", "gamemode spectator Q",
			"tp P 0.5 -60 0.5", "tp Q 2 -60 2",
			"attribute P minecraft:max_health base set 100",
			"data merge entity P {Health:100f}",
		} {
			func() { defer func() { recover() }(); sendCommand(t, pw2, cmd) }()
			time.Sleep(300 * time.Millisecond)
		}
		sinkP.take()
		sinkQ.take()
		var r result
		for _, tp := range types {
			func() { defer func() { recover() }(); sendCommand(t, pw1, "damage @s 1 minecraft:"+tp) }()
			time.Sleep(1100 * time.Millisecond)
			r.heard = append(r.heard, pfilter(voiceSounds(sinkQ.take())))
			r.victim = append(r.victim, pfilter(voiceSounds(sinkP.take())))
			// Heal back up for the next leg.
			func() { defer func() { recover() }(); sendCommand(t, pw1, "data merge entity @s {Health:100f}") }()
			time.Sleep(300 * time.Millisecond)
			sinkP.take()
			sinkQ.take()
		}
		func() { defer func() { recover() }(); sendCommand(t, pw1, "damage @s 1000 minecraft:generic_kill") }()
		time.Sleep(1200 * time.Millisecond)
		r.death = pfilter(voiceSounds(sinkQ.take()))
		return r
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	for i, tp := range types {
		w, g := want.heard[i], got.heard[i]
		if len(w) != len(g) {
			t.Errorf("%s: listener saw %v vs %v", tp, w, g)
			continue
		}
		for j := range w {
			if w[j] != g[j] {
				t.Errorf("%s: event %d vanilla %+v != mc-go %+v", tp, j, w[j], g[j])
			}
		}
		if len(w) == 0 {
			t.Errorf("%s: vanilla emitted no player voice — anchor drifted", tp)
		}
		if len(want.victim[i]) != len(got.victim[i]) {
			t.Errorf("%s: victim self-voice vanilla %v vs mc-go %v", tp, want.victim[i], got.victim[i])
		}
	}
	if len(want.death) != len(got.death) {
		t.Errorf("death: listener saw %v vs %v", want.death, got.death)
	}
}
