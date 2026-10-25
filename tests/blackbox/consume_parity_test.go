package blackbox

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestConsumeParity exercises the full ItemStack.finishUsingItem surface:
// the generated food table (nutrition, saturationByModifier*2, eatTicks,
// always-edible), the ConsumableListener stack (potion_contents, stew
// effects, ominous amplifier), the onConsume effect list (apply_status /
// remove / clear_all / teleport_randomly), and the post-use side effects
// (use_remainder slot conversions, use_cooldown packets and the
// on-cooldown silent FAIL). Probes are execute-if markers + data gets so
// both servers answer the same questions.
func TestConsumeParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Eater"),
	}, nil)
	defer targets.cleanup()

	var results []string
	for _, tc := range []struct{ tag, addr string }{
		{"vanilla", targets.vanilla},
		{"mc-go", targets.mcgo},
	} {
		t.Run(tc.tag, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, "Eater")
			defer c.Close()
			drainToJoinBlackbox(t, tc.tag, pr)
			if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
				t.Fatal(err)
			}
			seq := int32(100)

			probe := func(spec string, i int) string {
				sendCommand(t, pw, fmt.Sprintf("execute if %s run say ZEA-ok-%d", spec, i))
				sendCommand(t, pw, fmt.Sprintf("execute unless %s run say ZEA-miss-%d", spec, i))
				deadline := time.Now().Add(8 * time.Second)
				for time.Now().Before(deadline) {
					for _, e := range consumeDrain(t, c, pr, pw, 400*time.Millisecond) {
						if e.kind != "chat" {
							continue
						}
						if strings.Contains(e.chat, fmt.Sprintf("ZEA-ok-%d", i)) {
							return "hit"
						}
						if strings.Contains(e.chat, fmt.Sprintf("ZEA-miss-%d", i)) {
							return "miss"
						}
					}
				}
				return "none"
			}
			get := func(path string) string {
				sendCommand(t, pw, "data get entity @s "+path)
				sendCommand(t, pw, "say ZEA-end")
				deadline := time.Now().Add(8 * time.Second)
				got := ""
				for time.Now().Before(deadline) {
					end := false
					for _, e := range consumeDrain(t, c, pr, pw, 300*time.Millisecond) {
						if e.kind != "chat" {
							continue
						}
						if strings.Contains(e.chat, "data.entity.query") {
							got = e.chat
						}
						if strings.Contains(e.chat, "ZEA-end") {
							end = true
						}
					}
					if end {
						break
					}
				}
				return got
			}
			// reset wipes the stage: empty hand, no effects, food 10,
			// survival so eating is allowed.
			reset := func() {
				sendCommand(t, pw, "gamemode survival @s")
				sendCommand(t, pw, "clear @s")
				sendCommand(t, pw, "effect clear @s")
				sendCommand(t, pw, "data merge entity @s {foodLevel:10,foodSaturationLevel:0.0}")
				sendCommand(t, pw, "tp @s 8.5 -60 8.5 0 0")
				consumeDrain(t, c, pr, pw, 600*time.Millisecond)
			}
			// eat arms hotbar.0 with the item and holds use until the
			// eat finishes (1.6s = 32t) or the caller's window ends. The
			// use packet only goes out once SelectedItem confirms the
			// replace landed — racing the give produced silent no-eats.
			eat := func(item, id string) []consumeEvent {
				sendCommand(t, pw, "item replace entity @s hotbar.0 with "+item)
				deadline := time.Now().Add(6 * time.Second)
				for time.Now().Before(deadline) {
					ok := false
					sendCommand(t, pw, `execute if entity @s[nbt={SelectedItem:{id:"`+id+`"}}] run say ZEC-sel`)
					for _, e := range consumeDrain(t, c, pr, pw, 300*time.Millisecond) {
						if strings.Contains(e.chat, "ZEC-sel") {
							ok = true
						}
					}
					if ok {
						break
					}
				}
				sendUseItem(t, pw, 0, seq)
				seq++
				return consumeDrain(t, c, pr, pw, 2600*time.Millisecond)
			}
			// starve drains the food bar below 20 with a hunger effect
			// (player /data writes are rejected on both servers), then
			// clears it and returns the measured foodLevel so the leg
			// can assert the post-eat delta itself.
			starve := func() int {
				sendCommand(t, pw, "effect give @s minecraft:hunger 30 127")
				before := -1
				deadline := time.Now().Add(12 * time.Second)
				for time.Now().Before(deadline) {
					if v := foodLevel(get("foodLevel")); v >= 0 && v < 18 {
						break
					}
				}
				sendCommand(t, pw, "effect clear @s minecraft:hunger")
				consumeDrain(t, c, pr, pw, 500*time.Millisecond)
				// Re-measure after the clear: the drain kept running
				// through the poll window, so the poll value was stale.
				if v := foodLevel(get("foodLevel")); v >= 0 {
					before = v
				}
				return before
			}

			// stillHeld probes the held id in SelectedItem — "miss" means
			// the item left the slot (consumed, or flipped to a remainder).
			stillHeld := func(item string, i int) string {
				return probe(fmt.Sprintf(`entity @s[nbt={SelectedItem:{id:"%s"}}]`, item), i)
			}
			// foodProbe pins the post-eat foodLevel when a pre-eat level
			// was measured: min(20, before+nutrition); "none" when the
			// pre-level is unknown (starve poll timed out).
			foodProbe := func(before, nutrition, i int) string {
				if before < 0 {
					return "none"
				}
				want := before + nutrition
				if want > 20 {
					want = 20
				}
				return probe(fmt.Sprintf("entity @s[nbt={foodLevel:%d}]", want), i)
			}

			// Leg 1 — plain food: starved below 20, apple adds exactly 4,
			// and the held apple id is gone from SelectedItem.
			reset()
			before := starve()
			eat("minecraft:apple", "minecraft:apple")
			results = append(results, fmt.Sprintf("%s apple before=%d food=%s still=%s", tc.tag, before,
				foodProbe(before, 4, 1),
				stillHeld("minecraft:apple", 2)))

			// Leg 2 — always-edible: golden apple at a full 20 eats
			// anyway and grants regen+absorption (durations differ —
			// presence is the parity surface).
			reset()
			eat("minecraft:golden_apple", "minecraft:golden_apple")
			results = append(results, fmt.Sprintf("%s gapple regen=%s absorb=%s", tc.tag,
				probe(`entity @s[nbt={active_effects:[{id:"minecraft:regeneration"}]}]`, 3),
				probe(`entity @s[nbt={active_effects:[{id:"minecraft:absorption"}]}]`, 4)))

			// Leg 3 — enchanted golden apple: all four effects, plus
			// the amplifier is byte-read on bad_omen legs below.
			reset()
			eat("minecraft:enchanted_golden_apple", "minecraft:enchanted_golden_apple")
			results = append(results, fmt.Sprintf("%s egapple fx=%s,%s,%s,%s", tc.tag,
				probe(`entity @s[nbt={active_effects:[{id:"minecraft:regeneration"}]}]`, 5),
				probe(`entity @s[nbt={active_effects:[{id:"minecraft:resistance"}]}]`, 6),
				probe(`entity @s[nbt={active_effects:[{id:"minecraft:fire_resistance"}]}]`, 7),
				probe(`entity @s[nbt={active_effects:[{id:"minecraft:absorption",amplifier:3b}]}]`, 8)))

			// Leg 4 — milk clears every effect and the emptied slot
			// receives the bucket remainder.
			reset()
			sendCommand(t, pw, "effect give @s minecraft:speed 60 0 false")
			sendCommand(t, pw, "effect give @s minecraft:slowness 60 0 false")
			eat("minecraft:milk_bucket", "minecraft:milk_bucket")
			results = append(results, fmt.Sprintf("%s milk speed=%s slow=%s bucket=%s", tc.tag,
				probe(`entity @s[nbt={active_effects:[{id:"minecraft:speed"}]}]`, 9),
				probe(`entity @s[nbt={active_effects:[{id:"minecraft:slowness"}]}]`, 10),
				probe(`entity @s[nbt={SelectedItem:{id:"minecraft:bucket"}}]`, 11)))

			// Leg 5 — honey bottle removes only poison and converts
			// to a glass bottle (drink, 40-tick), +6 food.
			reset()
			sendCommand(t, pw, "effect give @s minecraft:poison 60 0 false")
			sendCommand(t, pw, "effect give @s minecraft:speed 60 0 false")
			eat("minecraft:honey_bottle", "minecraft:honey_bottle")
			results = append(results, fmt.Sprintf("%s honey poison=%s speed=%s bottle=%s food=%s", tc.tag,
				probe(`entity @s[nbt={active_effects:[{id:"minecraft:poison"}]}]`, 12),
				probe(`entity @s[nbt={active_effects:[{id:"minecraft:speed"}]}]`, 13),
				probe(`entity @s[nbt={SelectedItem:{id:"minecraft:glass_bottle"}}]`, 14),
				probe(`entity @s[nbt={foodLevel:20}]`, 15)))

			// Leg 6 — potion_contents listener: night_vision drink
			// applies the potion's effect list and converts to a bottle.
			reset()
			eat(`minecraft:potion[minecraft:potion_contents="minecraft:night_vision"]`, "minecraft:potion")
			results = append(results, fmt.Sprintf("%s potion nv=%s bottle=%s", tc.tag,
				probe(`entity @s[nbt={active_effects:[{id:"minecraft:night_vision"}]}]`, 16),
				probe(`entity @s[nbt={SelectedItem:{id:"minecraft:glass_bottle"}}]`, 17)))

			// Leg 7 — suspicious_stew_effects listener: the stew's
			// stored effect applies, the bowl converts, +6 food.
			reset()
			eat(`minecraft:suspicious_stew[minecraft:suspicious_stew_effects=[{id:"minecraft:fire_resistance",duration:400}]]`, "minecraft:suspicious_stew")
			results = append(results, fmt.Sprintf("%s stew fr=%s bowl=%s food=%s", tc.tag,
				probe(`entity @s[nbt={active_effects:[{id:"minecraft:fire_resistance"}]}]`, 18),
				probe(`entity @s[nbt={SelectedItem:{id:"minecraft:bowl"}}]`, 19),
				probe(`entity @s[nbt={foodLevel:20}]`, 20)))

			// Leg 8 — ominous_bottle_amplifier listener: bad_omen at
			// the component's amplifier, icon-only wire flags.
			reset()
			eat(`minecraft:ominous_bottle[minecraft:ominous_bottle_amplifier=2]`, "minecraft:ominous_bottle")
			results = append(results, fmt.Sprintf("%s ominous omen=%s", tc.tag,
				probe(`entity @s[nbt={active_effects:[{id:"minecraft:bad_omen",amplifier:2b}]}]`, 21)))

			// Leg 9 — stews convert too: mushroom_stew → bowl, +6 food.
			reset()
			before = starve()
			eat("minecraft:mushroom_stew", "minecraft:mushroom_stew")
			results = append(results, fmt.Sprintf("%s mstew before=%d bowl=%s food=%s", tc.tag, before,
				probe(`entity @s[nbt={SelectedItem:{id:"minecraft:bowl"}}]`, 22),
				foodProbe(before, 6, 23)))

			// Leg 10 — pufferfish: the three-effect apply list always
			// lands (shared prob 1): poison II, hunger III, nausea, +1.
			reset()
			before = starve()
			eat("minecraft:pufferfish", "minecraft:pufferfish")
			results = append(results, fmt.Sprintf("%s puffer before=%d %s,%s,%s food=%s", tc.tag, before,
				probe(`entity @s[nbt={active_effects:[{id:"minecraft:poison",amplifier:1b}]}]`, 24),
				probe(`entity @s[nbt={active_effects:[{id:"minecraft:hunger",amplifier:2b}]}]`, 25),
				probe(`entity @s[nbt={active_effects:[{id:"minecraft:nausea"}]}]`, 26),
				foodProbe(before, 1, 27)))

			// Leg 11 — chorus fruit teleports (Pos changes), the
			// cooldown packet ships, and the chorus sound plays.
			reset()
			posBefore := get("Pos[0]")
			ev := eat("minecraft:chorus_fruit", "minecraft:chorus_fruit")
			after := get("Pos[0]")
			cd, _ := consumeCooldown(ev)
			snd := consumeHasSound(ev, "item.chorus_fruit.teleport")
			moved := posBefore != after && posBefore != "" && after != ""
			results = append(results, fmt.Sprintf("%s chorus moved=%v cd=%s snd=%v", tc.tag, moved, cd, snd))

			// Leg 12 — ender pearl: the throw's cooldown packet
			// carries minecraft:ender_pearl/20, and a second use within
			// 20t is a silent FAIL (the follow-up use at ~2.5s throws
			// again once the cooldown lapses).
			reset()
			sendCommand(t, pw, "item replace entity @s hotbar.0 with minecraft:ender_pearl 8")
			consumeDrain(t, c, pr, pw, 700*time.Millisecond)
			sendUseItem(t, pw, 0, seq)
			seq++
			sendUseItem(t, pw, 0, seq) // immediate: inside the 20t window
			seq++
			ev = consumeDrain(t, c, pr, pw, 1200*time.Millisecond)
			cd, _ = consumeCooldown(ev)
			spawned := consumeSpawnCount(ev, "ender_pearl")
			consumeDrain(t, c, pr, pw, 1200*time.Millisecond)
			sendUseItem(t, pw, 0, seq)
			seq++
			ev2 := consumeDrain(t, c, pr, pw, 800*time.Millisecond)
			spawned += consumeSpawnCount(ev2, "ender_pearl")
			results = append(results, fmt.Sprintf("%s pearl cd=%s spawned=%d", tc.tag, cd, spawned))

			// Leg 13 — wind charge: spawned entity + 10-tick cooldown.
			reset()
			sendCommand(t, pw, "item replace entity @s hotbar.0 with minecraft:wind_charge 8")
			consumeDrain(t, c, pr, pw, 700*time.Millisecond)
			sendUseItem(t, pw, 0, seq)
			seq++
			ev = consumeDrain(t, c, pr, pw, 1500*time.Millisecond)
			cd, _ = consumeCooldown(ev)
			spawns := consumeSpawnCount(ev, "wind_charge")
			results = append(results, fmt.Sprintf("%s wind cd=%s spawns=%d", tc.tag, cd, spawns))

			// Leg 14 — dried kelp eats fast (16t): starved below 20 the
			// +1 food lands and the stack shrank.
			reset()
			before = starve()
			eat("minecraft:dried_kelp", "minecraft:dried_kelp")
			results = append(results, fmt.Sprintf("%s kelp before=%d food=%s still=%s", tc.tag, before,
				foodProbe(before, 1, 28),
				stillHeld("minecraft:dried_kelp", 29)))
		})
	}

	for _, r := range results {
		t.Log(r)
	}
}

// consumeEvent is one wire packet the drain found interesting.
type consumeEvent struct {
	kind  string // "chat","sound","cooldown","spawn"
	chat  string
	eid   int32 // sound registry id
	gid   string
	ticks int32
	etype int32
}

// foodLevel parses a `data get entity @s <scalar>` reply: the value
// text node is the last ASCII digit run before the trailing translate
// key (the with[] entries — including the raw-UUID hover — precede it).
func foodLevel(reply string) int {
	end := strings.LastIndex(reply, "commands.data.entity.query")
	if end < 0 {
		return -1
	}
	stop := end
	for stop > 0 && !isDigit(reply[stop-1]) {
		stop--
	}
	if stop == 0 {
		return -1
	}
	start := stop
	for start > 0 && isDigit(reply[start-1]) {
		start--
	}
	if start == stop {
		return -1
	}
	n, err := strconv.Atoi(reply[start:stop])
	if err != nil {
		return -1
	}
	return n
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func consumeDrain(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, dur time.Duration) []consumeEvent {
	t.Helper()
	var out []consumeEvent
	deadline := time.Now().Add(dur)
	for {
		if time.Now().After(deadline) {
			break
		}
		_ = c.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		p, err := pr.ReadPacket()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			t.Fatalf("consume drain: %v", err)
		}
		switch p.ID {
		case server.IDPlayKeepAliveCB:
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
		case server.IDPlayChunkBatchDone:
			_ = ackChunkBatch(pw)
		case server.IDPlaySyncPos:
			r := protocol.NewPacketReader(p.Data)
			id, e1 := r.VarInt()
			x, e2 := r.Double()
			y, e3 := r.Double()
			z, e4 := r.Double()
			if e1 == nil && e2 == nil && e3 == nil && e4 == nil {
				var b protocol.Buffer
				b.VarInt(id)
				_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
				var mb protocol.Buffer
				mb.Double(x)
				mb.Double(y)
				mb.Double(z)
				mb.Byte(0)
				_ = pw.WritePacket(server.IDPlayMovePosSB, mb.Bytes())
			}
		case 0x79, 0x21: // system_chat feedback + disguised_chat /say
			out = append(out, consumeEvent{kind: "chat", chat: string(p.Data)})
		case 0x16: // cooldown: Identifier + VarInt ticks
			r := protocol.NewPacketReader(p.Data)
			g, e1 := r.String()
			tk, e2 := r.VarInt()
			if e1 == nil && e2 == nil {
				out = append(out, consumeEvent{kind: "cooldown", gid: g, ticks: tk})
			}
		case 0x75: // sound (named): id, category, pos, vol, pitch, seed
			r := protocol.NewPacketReader(p.Data)
			id, e1 := r.VarInt()
			if e1 == nil {
				out = append(out, consumeEvent{kind: "sound", eid: id})
			}
		case 0x01: // spawn_entity
			r := protocol.NewPacketReader(p.Data)
			eid, e1 := r.VarInt()
			_, e2 := r.UUID()
			et, e3 := r.VarInt()
			if e1 == nil && e2 == nil && e3 == nil {
				out = append(out, consumeEvent{kind: "spawn", eid: eid, etype: et})
			}
		}
	}
	return out
}

// consumeCooldown returns the first cooldown packet's group and ticks.
func consumeCooldown(ev []consumeEvent) (string, bool) {
	for _, e := range ev {
		if e.kind == "cooldown" {
			return fmt.Sprintf("%s=%d", e.gid, e.ticks), true
		}
	}
	return "none", false
}

func consumeHasSound(ev []consumeEvent, name string) bool {
	want, _ := server.SoundEventID(name)
	for _, e := range ev {
		if e.kind == "sound" && e.eid == want+1 {
			return true
		}
	}
	return false
}

func consumeSpawnCount(ev []consumeEvent, name string) int {
	want, _ := server.EntityTypeID(name)
	n := 0
	for _, e := range ev {
		if e.kind == "spawn" && e.etype == want {
			n++
		}
	}
	return n
}
