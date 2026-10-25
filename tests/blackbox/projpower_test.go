package blackbox

// TestProjectilePowerParity covers ServerEntity.sendChanges' bundled
// motion emit for AbstractHurtingProjectile, measured from 26.2
// bytecode: when a hurting projectile's delta drifts far enough to
// re-emit set_entity_motion (0x65), vanilla wraps it in a
// ClientboundBundlePacket together with projectile_power (0x87):
// bundle_delimiter (0x00), motion, power, delimiter — every other
// entity type still ships the bare motion packet.
// accelerationPower constructs and reads back at 0.1 (NBT
// acceleration_power overrides it), applyInertia adds
// normalize(delta)*accelerationPower each tick — wind charges at
// inertia 1.0, the rest 0.95.
//
// Emit counts are inherently jittery across servers: the
// updateInterval (%10) gate boundaries don't align between two
// independent tick loops, and live entities can deflect or explode
// mid-window. The stable pin is the wire SHAPE (each power emit is
// bundled with a motion emit for the same id) plus the sorted set of
// power values each window produces.

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
)

// hurtingWindow inspects one probe window. Only hurting projectiles
// emit 0x87, wrapped with their motion emit in one bundle — so each
// 0x87 must sit as {delim, 0x65(id), 0x87(id), delim}; bare 0x65
// emits belong to other entity types and are ignored. Returns the
// distinct power values seen this window (sorted, deduped — emit
// counts and entity-emit order are jittery across servers).
func hurtingWindow(lines []string) ([]string, string) {
	powers := map[string]bool{}
	for i, l := range lines {
		if !strings.HasPrefix(l, "0x87:") {
			continue
		}
		// Shape: previous a 0x65 for the same id, a delimiter
		// before that, a delimiter after.
		if i < 2 || !strings.HasPrefix(lines[i-1], "0x65:") ||
			!strings.HasPrefix(lines[i-2], "0x0:") ||
			i+1 >= len(lines) || !strings.HasPrefix(lines[i+1], "0x0:") {
			return nil, fmt.Sprintf("power emit not bundled as {delim,motion,power,delim} around line %d", i)
		}
		r := protocol.NewPacketReader([]byte(l[5:]))
		id, _ := r.VarInt()
		p, _ := r.Double()
		rm := protocol.NewPacketReader([]byte(lines[i-1][5:]))
		mid, _ := rm.VarInt()
		if mid != id {
			return nil, fmt.Sprintf("power packet eid=%d does not match motion eid=%d", id, mid)
		}
		powers[fmt.Sprintf("%.4f", p)] = true
	}
	var seq []string
	for p := range powers {
		seq = append(seq, p)
	}
	sort.Strings(seq)
	return seq, ""
}

func TestProjectilePowerParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Prj"),
	}, nil)
	defer targets.cleanup()

	// Tiny initial power: applyInertia adds accelerationPower to the
	// delta each tick, so a hurting projectile accelerates out of the
	// 64-block tracking range within a couple seconds unless the
	// initial power is minuscule — the exit boundary itself is
	// jittery. Keep the wind charge at 0.005 until the merge flips it
	// to 0.02; the skull's 0.1 stays in range long enough for a couple
	// of emits. The comparison is the UNION of power values seen
	// across the whole probe — which window an emit lands in is
	// jittery, the values are not.
	steps := []string{
		"summon minecraft:wind_charge 3 -40 0 {Motion:[0.02,0.0,0.0],acceleration_power:0.005}",
		"", // let it emit at its initial power
		// The merged value must hit the wire while the charge is
		// still alive — hurting projectiles only live a few seconds
		// before they collide or leave the 64-block tracking range,
		// so the merge goes early in the probe.
		"data merge entity @e[type=minecraft:wind_charge,limit=1] {acceleration_power:0.02}",
		"", // merged power now rides the bundle payload
		"data merge entity @e[type=minecraft:wind_charge,limit=1] {Motion:[0.05,0.0,0.0]}",
		"", // needsSync re-emit, power unchanged
		"summon minecraft:wither_skull 3 -40 5 {Motion:[0.0,0.0,0.02]}",
		"", // skull emits at default power 0.1
	}

	run := func(addr string) []string {
		conn, pr, pw := joinPlayBlackbox(t, addr, "Prj")
		sink := newChatSinkTP(pr, pw)
		defer conn.Close()
		sendCommand(t, pw, "tp @s 0 -58 0 0 0")
		time.Sleep(400 * time.Millisecond)
		sink.take()
		set := map[string]bool{}
		var got []string
		for _, c := range steps {
			if c != "" {
				sendCommand(t, pw, c)
			}
			time.Sleep(1400 * time.Millisecond)
			seq, viol := hurtingWindow(sink.take())
			if viol != "" {
				t.Fatalf("bundle shape violation: %s", viol)
			}
			for _, p := range seq {
				if !set[p] {
					set[p] = true
					got = append(got, p)
				}
			}
		}
		sort.Strings(got)
		return got
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	if len(want) == 0 {
		t.Fatalf("vanilla emitted no hurting-projectile motion bundle")
	}
	if len(got) == 0 {
		t.Fatalf("mc-go emitted no hurting-projectile motion bundle")
	}
	if fmt.Sprint(want) != fmt.Sprint(got) {
		t.Fatalf("power union: vanilla %v vs mc-go %v", want, got)
	}
}
