//go:build blackbox

package blackbox

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestRaidParity pins the organic raid lifecycle against the real
// server: a claimed bed turns the position into a village, Bad Omen on
// a player inside it converts to a 600-tick Raid Omen (the effect
// swap on the wire — update 34 in, remove 30 out), and on expiry
// createOrExtendRaid anchors a raid at the POI centroid. The bar add
// (event.minecraft.raid, red notched-10), the 300-tick countdown
// progress ramp, the wave-1 horn and the raider spawn/equipment
// packets are all collected and compared between the two targets.
//
// /raid itself is NOT covered here — RaidCommand is registered only
// for the integrated server (CommandSelection), so dedicated vanilla
// answers command.unknown.command; those literals are pinned
// mc-go-side by TestRaidCommand.
//
// Village setup is real: the bed must sit at pathable ground level or
// AcquirePoi never lands the claim (a floating bed at feet+1 silently
// never converts). Waves land on the shrinking candidate ring
// (f4 = 0.22*(cooldown/20) - 0.24), ~98 blocks out at full cooldown,
// so the raid is observed through packets rather than entity probes.
func TestRaidParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()
	t.Cleanup(func() { vanillaLogTail(t) })

	run := func(addr string) []string {
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		sendClientInformation(t, pw)
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSinkTP(pr, pw)
		bars := raidBarStream{}
		time.Sleep(600 * time.Millisecond)

		sendCmd(t, pw, "tp @s 2000.5 -60 2000.5 0 0")
		sendCmd(t, pw, "setblock 2002 -60 2000 white_bed[part=foot]")
		sendCmd(t, pw, "setblock 2003 -60 2000 white_bed[part=head]")
		// AcquirePoi rides the villager's core package in every
		// activity window; the clock jump keeps the run daytime so no
		// hostile schedule behavior intervenes.
		sendCmd(t, pw, "time set 6100")
		sendCmd(t, pw, "summon minecraft:villager 2001.5 -60 2000.5 {VillagerData:{type:plains,profession:none,level:1}}")
		sendCmd(t, pw, "difficulty normal")
		waitDrained(t, pw, sink)

		var evs []string
		countdown := false
		collect := func(ms int) {
			time.Sleep(time.Duration(ms) * time.Millisecond)
			for _, ev := range raidCollect(sink, &bars, countdown) {
				// Filtered out of the compared stream:
				// - data get poll replies echo the full active_effects
				//   NBT — durations differ per target.
				// - snd/spawn/equip: ambient sound spam plus the wave's
				//   packets, which depend on the random spawn-ring
				//   geometry (~98 out, outside tracking range) and the
				//   horn's 64-block audible radius — all run-varying.
				if strings.Contains(ev, "commands.data.entity.query") ||
					strings.HasPrefix(ev, "snd ") ||
					strings.HasPrefix(ev, "spawn type=") ||
					strings.HasPrefix(ev, "equip ") {
					continue
				}
				evs = append(evs, ev)
			}
		}

		// Bad Omen converts the first tick the player is inside the
		// village — the villager's claim may lag the effect by a beat.
		sink.take()
		sendCmd(t, pw, "effect give @s minecraft:bad_omen 120 0")
		collect(1500)
		omen := false
		for dl := time.Now().Add(45 * time.Second); time.Now().Before(dl) && !omen; {
			// The poll's reply is filtered out of the stream; the
			// witness for conversion is the raid_omen update packet.
			sendCmd(t, pw, "data get entity @s active_effects")
			collect(1300)
			for _, ev := range evs {
				if strings.Contains(ev, "eff+ id=34") {
					omen = true
				}
			}
		}
		if !omen {
			dumpRaidEvents(t, addr, evs, &bars)
			t.Fatalf("%s: bad_omen never converted to raid_omen (village never claimed?)", addr)
		}

		// Raid Omen lasts 600 ticks; its expiry creates the raid, so
		// the boss bar add lands ~30s after the grant.
		countdown = true
		barSeen := false
		for dl := time.Now().Add(45 * time.Second); time.Now().Before(dl) && !barSeen; {
			collect(1000)
			if strings.Contains(bars.String(), "event.minecraft.raid") {
				barSeen = true
			}
		}
		if !barSeen {
			dumpRaidEvents(t, addr, evs, &bars)
			t.Fatalf("%s: raid never started — no boss bar add", addr)
		}

		// 300 ticks of countdown progress, then wave 1: the horn and
		// the raider ring spawn ~98 blocks out (outside tracking range,
		// so spawn packets are unreliable — the entity probe below is
		// the wave's witness).
		for dl := time.Now().Add(22 * time.Second); time.Now().Before(dl); {
			collect(1000)
		}
		countdown = false

		sink.take()
		sendCmd(t, pw, "execute if entity @e[type=minecraft:pillager,distance=32..] run say RAID_WAVE_SEEN")
		collect(1400)
		sink.take()
		sendCmd(t, pw, "kill @e[type=minecraft:pillager]")
		collect(1200)
		// The kill reply carries the wave-1 headcount — a bonus roll —
		// so digit runs flatten in chat events only (effect ids and
		// durations stay verbatim).
		for i, ev := range evs {
			if strings.HasPrefix(ev, "chat ") {
				evs[i] = raidNormalizeDigits(ev)
			}
		}

		evs = append(evs, "bars "+bars.String())
		return evs
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	mismatch := len(want) != len(got)
	if mismatch {
		t.Errorf("event streams differ in length: vanilla %d, mc-go %d", len(want), len(got))
	}
	for i := 0; i < len(want) && i < len(got); i++ {
		if want[i] != got[i] {
			t.Errorf("event %d:\n  vanilla %s\n  mc-go   %s", i, want[i], got[i])
			mismatch = true
		}
	}
	if mismatch {
		dumpRaidEvents(t, "vanilla", want, nil)
		dumpRaidEvents(t, "mcgo", got, nil)
	}
}

// TestRaidCommand pins mc-go's /raid surface against the javap-verified
// literals — the dedicated vanilla jar gates RaidCommand behind
// CommandSelection.INTEGRATED, so these probes can only run mc-go-side.
// Replies are asserted as decoded component substrings (the rendered
// NBT trees are pinned for the parse-error paths too).
func TestRaidCommand(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	c, pr, pw := joinPlayBlackbox(t, targets.mcgo, "P")
	defer c.Close()
	sendClientInformation(t, pw)
	_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
	sink := newChatSinkTP(pr, pw)
	time.Sleep(600 * time.Millisecond)

	sendCmd(t, pw, "tp @s 2000.5 -60 2000.5 0 0")
	sendCmd(t, pw, "setblock 2002 -60 2000 white_bed[part=foot]")
	sendCmd(t, pw, "setblock 2003 -60 2000 white_bed[part=head]")
	sendCmd(t, pw, "time set 6100")
	sendCmd(t, pw, "summon minecraft:villager 2001.5 -60 2000.5 {VillagerData:{type:plains,profession:none,level:1}}")
	waitDrained(t, pw, sink)

	// The raid survives only while its center stays a village — the
	// villager's POI claim lands on the first scan tick, so poll
	// start+check until one sticks.
	alive := false
	for dl := time.Now().Add(40 * time.Second); time.Now().Before(dl) && !alive; {
		sink.take()
		sendCommand(t, pw, "raid start 0")
		time.Sleep(500 * time.Millisecond)
		sink.take()
		sendCommand(t, pw, "raid check")
		time.Sleep(1400 * time.Millisecond)
		for _, line := range sink.take() {
			if tree, ok := raidChatTree(line); ok && strings.Contains(tree, "Found a started raid") {
				alive = true
			}
		}
	}
	if !alive {
		t.Fatalf("%s: villager never claimed the bed", targets.mcgo)
	}
	sendCommand(t, pw, "raid stop")
	time.Sleep(500 * time.Millisecond)

	steps := [][]string{}
	probe := func(cmd string, quietMS int) {
		sink.take()
		sendCommand(t, pw, cmd)
		time.Sleep(time.Duration(quietMS) * time.Millisecond)
		var evs []string
		for _, line := range sink.take() {
			i := strings.Index(line, ":")
			if i >= 0 {
				if id, err := strconv.ParseInt(line[:i], 0, 32); err == nil && id == 0x75 {
					evs = append(evs, "snd "+raidSoundBody(line[i+1:]))
					continue
				}
			}
			if tree, ok := raidChatTree(line); ok {
				evs = append(evs, "chat "+maskUUID(tree))
			}
		}
		steps = append(steps, evs)
	}

	probe("raid check", 700)
	probe("raid start 1", 700)
	probe("raid check", 700)
	probe("raid start 1", 700)
	probe("raid setomen -2", 700)
	probe("raid setomen 6", 700)
	probe("raid setomen 2", 700)
	// Stop before the sound probes: wave 1 lands ~15s after start and
	// its horn would otherwise leak into their windows.
	probe("raid stop", 700)
	probe("raid bogus", 700)
	probe("raid", 700)
	probe("raid start x", 700)
	probe("raid spawnleader", 1300)
	probe("raid sound local", 700)
	probe(`raid sound {"text":"far"}`, 700)
	probe("raid stop", 700)
	probe("raid check", 700)

	// javap-derived expected reply substrings per probe, in order.
	wantSubs := [][]string{
		{"Found no started raids"},
		{"Created a raid in your local village"},
		{"Found a started raid!", "Num groups spawned:0 Raid omen level:1"},
		{"Raid already started close by"},
		{"argument.integer.low"},
		{"Sorry, the max raid omen level you can set is 5"},
		{"Changed village's raid omen level from 1 to 2"},
		{"Stopped raid"},
		{"command.unknown"},
		{"command.unknown"},
		{"parsing.int.expected"},
		{"Spawned a raid captain"},
		{"snd event.raid.horn"},
		{},
		{"No raid here"},
		{"Found no started raids"},
	}
	for i, subs := range wantSubs {
		var evs []string
		if i < len(steps) {
			evs = steps[i]
		}
		for _, sub := range subs {
			found := false
			for _, ev := range evs {
				if strings.Contains(ev, sub) {
					found = true
				}
			}
			if !found {
				t.Errorf("probe %d: want reply containing %q, got %v", i, sub, evs)
			}
		}
	}
	// The non-"local" component must NOT have sounded the horn
	// (ambient sounds are ignored — only the raid horn counts).
	for _, ev := range steps[13] {
		if strings.Contains(ev, "event.raid.horn") {
			t.Errorf(`raid sound {"text":"far"}: unexpected horn %q`, ev)
		}
	}
}

// dumpRaidEvents writes the collected stream to a /tmp file on a
// fatal — the organic raid's wire is the only evidence of why a stage
// didn't fire.
func dumpRaidEvents(t *testing.T, addr string, evs []string, bars *raidBarStream) {
	t.Helper()
	name := fmt.Sprintf("/tmp/raid_events_%d.txt", time.Now().UnixNano())
	var b strings.Builder
	for _, ev := range evs {
		b.WriteString(ev)
		b.WriteByte('\n')
	}
	if bars != nil {
		b.WriteString("bars " + bars.String() + "\n")
		for _, raw := range bars.rawAdds {
			b.WriteString("rawadd " + raw + "\n")
		}
	}
	if err := os.WriteFile(name, []byte(b.String()), 0o644); err != nil {
		t.Logf("dump: %v", err)
		return
	}
	t.Logf("%s: raid event stream dumped to %s", addr, name)
}

// raidChatTree is componentTree plus the TAG_String root: unstyled
// literal replies (most of /raid's success lines) serialize compactly
// as a bare string, which the map-shaped componentTree rejects.
func raidChatTree(line string) (string, bool) {
	if tree, ok := componentTree(line, true); ok {
		return tree, true
	}
	i := strings.Index(line, ":")
	if i < 0 {
		return "", false
	}
	id, err := strconv.ParseInt(line[:i], 0, 32)
	if err != nil || int32(id) != server.IDPlaySystemChatCB {
		return "", false
	}
	root, err := nbt.ReadUnnamed(strings.NewReader(line[i+1:]))
	if err != nil {
		return "", false
	}
	if s, ok := root.(string); ok {
		return strconv.Quote(s), true
	}
	return "", false
}

// raidBarStream accumulates boss_event packets phase-insensitively:
// add/remove/name ops verbatim (uuid masked), progress ops quantized
// to an ordered-unique run of 1/30 buckets — the countdown ramp is
// deterministic regardless of which wall-clock window caught which
// tick, while wave-health fractions land on the same lattice and are
// suppressed outside the countdown windows.
type raidBarStream struct {
	events     []string
	rawAdds    []string
	lastBucket int
	progSeen   bool
}

var raidBossDump = os.Getenv("MCGO_RAID_DUMP")

func (b *raidBarStream) add(line string, countdown bool) {
	body := line
	if raidBossDump != "" {
		if f, err := os.OpenFile(raidBossDump, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintf(f, "%x\n", body)
			f.Close()
		}
	}
	if len(body) < 16 {
		return
	}
	payload := body[16:]
	r := protocol.NewPacketReader([]byte(payload))
	op, err := r.VarInt()
	if err != nil {
		return
	}
	switch op {
	case 0: // add: name component + progress + color + style + flags
		name := raidComponentName(payload, r)
		b.events = append(b.events, "add name="+name)
		b.rawAdds = append(b.rawAdds, fmt.Sprintf("%x", body))
		b.progSeen = false
	case 1:
		b.events = append(b.events, "remove")
		b.progSeen = false
	case 2: // progress — quantized bucket run during the countdown
		p, err := r.Float()
		if err != nil {
			return
		}
		if !countdown {
			return
		}
		bucket := int(math.Round(float64(p) * 30))
		if !b.progSeen || bucket != b.lastBucket {
			b.events = append(b.events, fmt.Sprintf("prog~%d", bucket))
			b.lastBucket = bucket
			b.progSeen = true
		}
	case 3: // name
		name := raidComponentName(payload, r)
		b.events = append(b.events, "name "+name)
	}
}

func (b *raidBarStream) String() string { return strings.Join(b.events, " | ") }

// raidComponentName reads the component payload at r's position inside
// body and renders the full NBT tree — vanilla's raid bar name arrives
// as {"translate":"event.minecraft.raid"} but may carry wrapping text
// or extra fields, so the whole component is the comparison surface.
func raidComponentName(body string, r *protocol.PacketReader) string {
	start := len(body) - r.Remaining()
	root, err := nbt.ReadUnnamed(bytes.NewReader([]byte(body)[start:]))
	if err != nil {
		return "?"
	}
	if s, ok := root.(string); ok {
		return strconv.Quote(s)
	}
	return renderNBT(root, true)
}

// raidCollect normalizes one drain window into comparable events:
// chat payloads render as their component tree, named_sound as
// name/category/volume/pitch (position and seed are random per run),
// entity_spawn as its type id, set_equipment as the raw bytes after
// the masked entity id, and mob-effect update/remove as their effect
// fields with the entity id masked. Boss_event packets go to the
// stream.
func raidCollect(s *chatSink, bars *raidBarStream, countdown bool) []string {
	var evs []string
	for _, line := range s.take() {
		i := strings.Index(line, ":")
		if i < 0 {
			continue
		}
		id, err := strconv.ParseInt(line[:i], 0, 32)
		if err != nil {
			continue
		}
		body := line[i+1:]
		switch int32(id) {
		case 0x09: // boss_event
			bars.add(body, countdown)
		case 0x75: // named_sound
			evs = append(evs, "snd "+raidSoundBody(body))
		case 0x84: // update_mob_effect
			if ev := raidEffectAddBody(body); ev != "" {
				evs = append(evs, ev)
			}
		case 0x4e: // remove_mob_effect
			if ev := raidEffectRmBody(body); ev != "" {
				evs = append(evs, ev)
			}
		case server.IDPlaySpawnEntity:
			if typ := raidSpawnType(body); typ >= 0 {
				evs = append(evs, fmt.Sprintf("spawn type=%d", typ))
			}
		case server.IDPlaySetEquipment:
			if eq := raidEquipBody(body); eq != "" {
				evs = append(evs, "equip "+eq)
			}
		default:
			if tree, ok := componentTree(line, true); ok {
				evs = append(evs, "chat "+maskUUID(tree))
			}
		}
	}
	return evs
}

// raidEffectAddBody decodes update_mob_effect to
// "eff+ id=N amp=A dur=D flags=F" with the entity id masked.
func raidEffectAddBody(body string) string {
	r := protocol.NewPacketReader([]byte(body))
	if _, err := r.VarInt(); err != nil { // entity id
		return ""
	}
	eid, err := r.VarInt()
	if err != nil {
		return ""
	}
	amp, err := r.Byte()
	if err != nil {
		return ""
	}
	dur, err := r.VarInt()
	if err != nil {
		return ""
	}
	flags, err := r.Byte()
	if err != nil {
		return ""
	}
	return fmt.Sprintf("eff+ id=%d amp=%d dur=%d flags=%d", eid, amp, dur, flags)
}

// raidEffectRmBody decodes remove_mob_effect to "eff- id=N".
func raidEffectRmBody(body string) string {
	r := protocol.NewPacketReader([]byte(body))
	if _, err := r.VarInt(); err != nil {
		return ""
	}
	eid, err := r.VarInt()
	if err != nil {
		return ""
	}
	return fmt.Sprintf("eff- id=%d", eid)
}

// raidNormalizeDigits flattens every digit run in an event to '#',
// for replies whose counts ride a random roll.
func raidNormalizeDigits(ev string) string {
	var b strings.Builder
	inDigit := false
	for _, r := range ev {
		if r >= '0' && r <= '9' {
			if !inDigit {
				b.WriteByte('#')
				inDigit = true
			}
			continue
		}
		inDigit = false
		b.WriteRune(r)
	}
	return b.String()
}

// raidSoundBody normalizes a named_sound payload to its comparable
// fields: sound name, category, volume, pitch. Position and seed are
// raid-spawn-position random.
func raidSoundBody(body string) string {
	r := protocol.NewPacketReader([]byte(body))
	if holder, err := r.VarInt(); err != nil || holder != 0 {
		return "holder-registry"
	}
	name, err := r.String()
	if err != nil {
		return body
	}
	cat, err := r.VarInt()
	if err != nil {
		return body
	}
	for i := 0; i < 3; i++ { // fixed-point position
		if _, err := r.Int(); err != nil {
			return body
		}
	}
	vol, err := r.Float()
	if err != nil {
		return body
	}
	pitch, err := r.Float()
	if err != nil {
		return body
	}
	return fmt.Sprintf("%s cat=%d vol=%g pitch=%g", name, cat, vol, pitch)
}

// raidSpawnType decodes the entity type id from an entity_spawn packet
// (eid, uuid, type), returning -1 on a short body.
func raidSpawnType(body string) int32 {
	r := protocol.NewPacketReader([]byte(body))
	if _, err := r.VarInt(); err != nil {
		return -1
	}
	if _, err := r.Read(16); err != nil {
		return -1
	}
	typ, err := r.VarInt()
	if err != nil {
		return -1
	}
	return typ
}

// raidEquipBody strips the entity id off a set_equipment payload and
// hex-encodes the rest — the slot-mask + Slot bytes compare verbatim.
func raidEquipBody(body string) string {
	r := protocol.NewPacketReader([]byte(body))
	if _, err := r.VarInt(); err != nil {
		return ""
	}
	return fmt.Sprintf("%x", []byte(body)[len(body)-r.Remaining():])
}
