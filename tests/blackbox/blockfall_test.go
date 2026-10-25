//go:build blackbox

package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
)

// TestBlockFallSoundParity pins the fallOn/causeFallDamage tail: a
// mob landing with damage > 0 emits the entity Fallsounds pair plus
// a positional sound packet carrying the landed block's
// SoundType.fallSound, scaled to volume*0.5 / pitch*0.75. Per-block
// fallOn overrides matter too: slime silences the landing entirely,
// honey adds its slide sound plus a second copy of the fall sound,
// hay and honey soften damage to 0.2, beds halve fallDistance, and
// an up-pointing dripstone tip impales for (d+2.5)*2.
func TestBlockFallSoundParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)

	// One caged drop column per surface; a zombie with normal AI
	// falls ~9 blocks onto it (NoAI freezes physics on vanilla — it
	// never falls at all). snd = expected block fall-sound registry
	// id, -1 = silent landing, want = times the packet appears.
	cases := []struct {
		blk  string
		snd  int32
		vol  float32
		pit  float32
		want int
	}{
		{"minecraft:stone", 1599, 0.5, 0.75, 1},
		{"minecraft:grass_block", 756, 0.5, 0.75, 1},
		{"minecraft:sand", 1397, 0.5, 0.75, 1},
		{"minecraft:oak_planks", 1854, 0.5, 0.75, 1},
		{"minecraft:glass", 721, 0.5, 0.75, 1},
		{"minecraft:slime_block", -1, 0, 0, 0},
		{"minecraft:honey_block", 834, 0.5, 0.75, 2},
		{"minecraft:anvil", 48, 0.15, 0.75, 1}, // ANVIL SoundType vol=0.3
		{"minecraft:red_bed", 1854, 0.5, 0.75, 1},
		{"minecraft:hay_block", 756, 0.5, 0.75, 1},
		{"minecraft:pointed_dripstone[thickness=tip,vertical_direction=up]", 554, 0.5, 0.75, 1},
	}
	// Ambient barks, hurt voices, and unrelated server sounds share
	// the 0x75 packet — only fall-relevant ids are captured.
	relevant := map[int32]bool{871: true, 874: true, 694: true, 702: true, 837: true}
	for _, c := range cases {
		relevant[c.snd] = true
	}

	run := func(addr string) map[string]float64 {
		cv, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer cv.Close()
		sink := newChatSinkTP(pr, pw)
		// Mid-test drains for command confirmation consume packet
		// dumps — stash them so the final parse still sees every
		// captured line.
		var stash []string
		// Vanilla's playSound broadcast is radius-gated at
		// max(vol*16, 16) blocks — P stands mid-strip so every column
		// lands inside 16.
		sendCommand(t, pw, "tp P 10 -55 7 0 0")
		sendCommand(t, pw, "time set 18000")
		sendCommand(t, pw, "weather clear")
		sendCommand(t, pw, "kill @e[type=zombie]")
		sendCommand(t, pw, "gamerule max_entity_cramming 0")

		await := func(key string) bool {
			end := time.Now().Add(3 * time.Second)
			for time.Now().Before(end) {
				lines := sink.take()
				stash = append(stash, lines...)
				for _, l := range lines {
					if strings.Contains(l, key) {
						return true
					}
				}
				time.Sleep(30 * time.Millisecond)
			}
			return false
		}
		fillb := func(cmd string) {
			for range 30 {
				sendCommand(t, pw, cmd)
				// commands.fill.failed ends in "fill." too — only the
				// success translate counts.
				if await("fill.success") {
					return
				}
				time.Sleep(200 * time.Millisecond)
			}
			t.Fatalf("fill never landed: %s", cmd)
		}
		// Clear the drop volume, then build each strip: floor cell at
		// y=-61 (the block under test) inside a 1x2 stone cage so the
		// wandering zombie cannot leave its column. Walls are shared
		// between neighbours (spacing 2).
		fillb("fill -3 -61 4 23 -51 7 minecraft:air")
		for i, c := range cases {
			x := i * 2
			fillb(fmt.Sprintf("fill %d -61 5 %d -61 6 %s", x, x, c.blk))
			fillb(fmt.Sprintf("fill %d -61 4 %d -51 4 minecraft:stone", x-1, x-1))
			fillb(fmt.Sprintf("fill %d -61 7 %d -51 7 minecraft:stone", x-1, x-1))
		}
		// Last column's right wall + end caps on the shared walls.
		last := (len(cases)-1)*2 + 1
		fillb(fmt.Sprintf("fill %d -61 4 %d -51 4 minecraft:stone", last, last))
		fillb(fmt.Sprintf("fill %d -61 7 %d -51 7 minecraft:stone", last, last))
		// summon against an unloaded chunk errors — retry until the
		// success reply lands (same pattern as fillb). Extra retries
		// could duplicate a mob on a slow reply, so kill the cell
		// first on retry.
		for i := range cases {
			x := float64(i*2) + 0.5
			cmd := fmt.Sprintf(
				"summon minecraft:zombie %v -51 5.5", x)
			// commands.summon.failed* replies also contain "summon" —
			// only the success translate ends the retry loop.
			for tries := 0; tries < 30; tries++ {
				sendCommand(t, pw, cmd)
				if await("summon.success") {
					break
				}
				sendCommand(t, pw, fmt.Sprintf(
					"kill @e[type=zombie,x=%v,y=-51,z=5.5,distance=..1]", x))
				time.Sleep(200 * time.Millisecond)
				if tries == 29 {
					t.Fatalf("summon never landed: %s", cmd)
				}
			}
		}
		time.Sleep(2500 * time.Millisecond)
		stash = append(stash, sink.take()...)

		// Where did each zombie end up? Pos[1] proves the landing
		// happened, Health proves the damage event did.
		probe := map[string]float64{}
		for i := range cases {
			sel := fmt.Sprintf(
				"@e[type=zombie,x=%v,y=-61,z=5.5,distance=..2,limit=1]",
				float64(i*2)+0.5)
			probe[fmt.Sprintf("s%d.y", i)] = nbtFloatOf(t, sink, pw, sel, "Pos[1]")
			probe[fmt.Sprintf("s%d.hp", i)] = nbtFloatOf(t, sink, pw, sel, "Health")
		}
		stash = append(stash, sink.take()...)
		t.Logf("probes %v", probe)

		out := map[string]float64{}
		// 0x75 positional sound: id varint, src varint, x/y/z int*8,
		// vol float, pitch float, seed long. The zombie stays caged
		// at x in [i*2, i*2+1), so floor(x8/16) maps back to the case.
		for _, l := range stash {
			if !strings.HasPrefix(l, "0x75:") {
				continue
			}
			r := protocol.NewPacketReader([]byte(l[strings.IndexByte(l, ':')+1:]))
			id, err1 := r.VarInt()
			_, err2 := r.VarInt()
			x8, err3 := r.Int()
			_, err4 := r.Int()
			_, err5 := r.Int()
			vol, err6 := r.Float()
			pit, err7 := r.Float()
			if err1 != nil || err2 != nil || err3 != nil || err4 != nil ||
				err5 != nil || err6 != nil || err7 != nil {
				continue
			}
			col := int(x8 / 16)
			if col < 0 || col >= len(cases) || !relevant[id-1] {
				continue
			}
			key := fmt.Sprintf("s%d", col)
			switch id - 1 {
			case 871, 874, 694, 702:
				out[key+".ent"]++ // Fallsounds pair fired
			case 837:
				out[key+".slide"]++ // honey slide
			default:
				out[key+".blk"] = float64(id - 1)
				out[key+".blkcnt"]++
				out[key+".vol"] = float64(vol)
				out[key+".pit"] = float64(pit)
			}
		}
		return out
	}

	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	t.Logf("vanilla %v", van)
	t.Logf("mc-go   %v", mc)
	for i, c := range cases {
		k := fmt.Sprintf("s%d", i)
		for _, suffix := range []string{".ent", ".blk", ".blkcnt", ".vol", ".pit", ".slide"} {
			if van[k+suffix] != mc[k+suffix] {
				t.Errorf("%s%s on %s: vanilla=%v mc-go=%v",
					k, suffix, c.blk, van[k+suffix], mc[k+suffix])
			}
		}
		switch {
		case c.snd < 0:
			if van[k+".blkcnt"] != 0 {
				t.Errorf("vanilla %s emitted a block sound, want silent", c.blk)
			}
		default:
			if van[k+".blk"] != float64(c.snd) {
				t.Errorf("vanilla %s fall sound id=%v, want %v — oracle drifted",
					c.blk, van[k+".blk"], c.snd)
			}
			if van[k+".blkcnt"] != float64(c.want) {
				t.Errorf("vanilla %s fall sound count=%v, want %v",
					c.blk, van[k+".blkcnt"], c.want)
			}
		}
	}
}
