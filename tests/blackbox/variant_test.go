package blackbox

import (
	"bytes"
	"fmt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"strings"
	"testing"
	"time"
)

// TestVariantParity pins the plain-int variant metadata sweep measured on
// vanilla 26.2: axolotl DATA_VARIANT (18, int) + DATA_FROM_BUCKET (20,
// bool), parrot DATA_VARIANT_ID (20, int), rabbit DATA_TYPE_ID (18, int —
// plus the killer-bunny auto-name at 99), tropical_fish
// DATA_ID_TYPE_VARIANT (17, int), fox DATA_TYPE_ID (18) + DATA_FLAGS_ID
// (19, byte), panda gene ordinal bytes at 21/22. It decodes every
// set_entity_data entry the summon burst sends and compares idx->(type,
// value) across both servers, then re-verifies the save-map keys via
// nbt= selector probes.
func TestVariantParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Var"),
	}, nil)
	defer targets.cleanup()

	for _, addr := range []string{targets.vanilla, targets.mcgo} {
		c, pr, pw := joinPlayBlackbox(t, addr, "Var")
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSink(pr, pw)
		time.Sleep(400 * time.Millisecond)
		sendCommand(t, pw, "gamemode spectator Var")
		sendCommand(t, pw, "tp @s 0 -60 0")
		time.Sleep(400 * time.Millisecond)
		sink.take()

		summons := []struct{ name, nbt string }{
			{"axolotl", `{Variant:4,FromBucket:1b,NoAI:1b}`},
			{"parrot", `{Variant:2,NoAI:1b}`},
			{"rabbit", `{RabbitType:5,MoreCarrotTicks:99,NoAI:1b}`},
			{"rabbit", `{RabbitType:99,NoAI:1b}`},
			{"tropical_fish", `{Variant:918273,NoAI:1b}`},
			{"fox", `{Type:"snow",Sitting:1b,Crouching:1b,NoAI:1b}`},
			{"panda", `{MainGene:"lazy",HiddenGene:"worried",NoAI:1b}`},
			// Trusted is a list of [I;] int-array uuids
			// (TRUSTED_LIST_CODEC = listOf) — the flat [I;]
			// form is rejected.
			{"fox", `{Type:"snow",Trusted:[[I;1,2,3,4]],NoAI:1b}`},
		}
		// want = idx -> "type:value" for every entry vanilla's spawn
		// burst packs beyond the always-present idx9 health / idx15
		// mob-flags pair.
		wantMeta := []map[int]string{
			{9: "3:14", 15: "0:1", 18: "1:4", 20: "8:1"},
			{9: "3:6", 15: "0:1", 20: "1:2"},
			{9: "3:3", 15: "0:1", 18: "1:5"},
			// RabbitType 99 also fires setRabbitType's auto-name —
			// idx2 carries the killer_bunny translate component.
			{2: "6:name", 9: "3:3", 15: "0:1", 18: "1:99"},
			{9: "3:3", 15: "0:1", 17: "1:918273"},
			{9: "3:10", 15: "0:1", 18: "1:1", 19: "0:5"},
			{9: "3:20", 15: "0:1", 21: "0:1", 22: "0:2"},
			// idx20 = optional_living_entity_reference (serializer
			// 13): bool + uuid — rendered last byte is the uuid tail
			// (4); the nbt probe pins the whole value.
			{9: "3:10", 15: "0:1", 18: "1:1", 20: "13:4"},
		}

		for i, s := range summons {
			// eid 1 is the player; summons allocate 2.. in order.
			sendCommand(t, pw, fmt.Sprintf("summon minecraft:%s %d -60 %d %s", s.name, i*2, 3, s.nbt))
			time.Sleep(120 * time.Millisecond)
		}
		time.Sleep(1200 * time.Millisecond)

		gotMeta := map[int]map[int]metaEnt{}
		for _, l := range sink.take() {
			i := strings.IndexByte(l, ':')
			if i < 0 || l[:i] != "0x63" {
				continue
			}
			r := protocol.NewPacketReader([]byte(l[i+1:]))
			eid, _ := r.VarInt()
			slot := int(eid) - 2
			if slot < 0 || slot >= len(summons) {
				continue
			}
			rest, _ := r.Read(r.Remaining())
			ents := decodeMetaEntries(t, rest)
			// Later dirty packs are partial — keep the fullest.
			if len(ents) > len(gotMeta[slot]) {
				gotMeta[slot] = ents
			}
		}
		for i, s := range summons {
			got := gotMeta[i]
			if len(got) == 0 {
				t.Fatalf("%s: %s emitted no 0x63 metadata packet", addr, s.name)
			}
			for idx, want := range wantMeta[i] {
				e, ok := got[idx]
				if !ok {
					t.Fatalf("%s: %s missing metadata idx %d (have %v)", addr, s.name, idx, got)
				}
				gotv := metaEntryValue(e)
				if want == "6:name" {
					if e.typ != 6 || !bytes.Contains(e.val, []byte("killer_bunny")) {
						t.Fatalf("%s: %s idx2 = typ%d %x, want killer_bunny component", addr, s.name, e.typ, e.val)
					}
					continue
				}
				if gotv != want {
					t.Fatalf("%s: %s idx%d = %s, want %s (all %v)", addr, s.name, idx, gotv, want, got)
				}
			}
			for idx := range got {
				if _, ok := wantMeta[i][idx]; !ok {
					t.Fatalf("%s: %s unexpected metadata idx %d typ%d val%x", addr, s.name, idx, got[idx].typ, got[idx].val)
				}
			}
		}

		// Save-map keys: nbt= selector probes.
		probes := []struct{ sel, mark string }{
			{`@e[type=minecraft:axolotl,nbt={Variant:4,FromBucket:1b}]`, "ZZAXV"},
			{`@e[type=minecraft:parrot,nbt={Variant:2}]`, "ZZPAV"},
			{`@e[type=minecraft:rabbit,nbt={RabbitType:99}]`, "ZZRBV"},
			{`@e[type=minecraft:rabbit,nbt={MoreCarrotTicks:99}]`, "ZZRBC"},
			{`@e[type=minecraft:rabbit,nbt={CustomName:{translate:"entity.minecraft.killer_bunny"}}]`, "ZZRBN"},
			{`@e[type=minecraft:tropical_fish,nbt={Variant:918273}]`, "ZZTFV"},
			{`@e[type=minecraft:fox,nbt={Type:"snow",Sitting:1b,Crouching:1b}]`, "ZZFXV"},
			{`@e[type=minecraft:fox,nbt={Trusted:[]}]`, "ZZFXT"},
			{`@e[type=minecraft:fox,nbt={Trusted:[[I;1,2,3,4]]}]`, "ZZFTR"},
			{`@e[type=minecraft:panda,nbt={MainGene:"lazy",HiddenGene:"worried"}]`, "ZZPDG"},
		}
		for _, p := range probes {
			sendCommand(t, pw, fmt.Sprintf("execute if entity %s run say %s", p.sel, p.mark))
			time.Sleep(120 * time.Millisecond)
		}
		time.Sleep(800 * time.Millisecond)
		hits := map[string]bool{}
		for _, l := range sink.take() {
			for _, p := range probes {
				if strings.Contains(l, p.mark) {
					hits[p.mark] = true
				}
			}
		}
		for _, p := range probes {
			if !hits[p.mark] {
				t.Fatalf("%s: selector probe %s missed (%s)", addr, p.mark, p.sel)
			}
		}
		c.Close()
	}
}

// TestHolderVariantParity pins the registry-holder variant sweep
// measured on vanilla 26.2: cat DATA_VARIANT_ID (20, cat_variant
// serializer 21) + DATA_SOUND_VARIANT_ID (21, serializer 22), frog
// DATA_VARIANT_ID (18, serializer 27 — no sound registry), wolf
// DATA_VARIANT_ID (23, serializer 25) + DATA_SOUND_VARIANT_ID (24,
// serializer 26), pig (19/28 + 20/29), cow (18/23 + 19/24), chicken
// (17/30 + 18/31), and salmon's plain-int DATA_TYPE (17, varint —
// small 0 / medium 1 / large 2). Defined defaults (black, pale,
// temperate, classic sounds, medium) stay unpacked on spawn; a
// non-default pack is a VarInt registry id.
func TestHolderVariantParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Var"),
	}, nil)
	defer targets.cleanup()

	for _, addr := range []string{targets.vanilla, targets.mcgo} {
		c, pr, pw := joinPlayBlackbox(t, addr, "Var")
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSink(pr, pw)
		time.Sleep(400 * time.Millisecond)
		sendCommand(t, pw, "gamemode spectator Var")
		sendCommand(t, pw, "tp @s 0 -60 0")
		time.Sleep(400 * time.Millisecond)
		sink.take()

		summons := []struct{ name, nbt string }{
			{"cat", `{variant:"minecraft:persian",NoAI:1b}`},
			{"cat", `{sound_variant:"minecraft:royal",NoAI:1b}`},
			{"frog", `{variant:"minecraft:warm",NoAI:1b}`},
			{"wolf", `{variant:"minecraft:ashen",NoAI:1b}`},
			{"wolf", `{sound_variant:"minecraft:puglin",NoAI:1b}`},
			{"pig", `{variant:"minecraft:warm",NoAI:1b}`},
			{"cow", `{sound_variant:"minecraft:moody",NoAI:1b}`},
			{"chicken", `{variant:"minecraft:warm",sound_variant:"minecraft:picky",NoAI:1b}`},
			{"salmon", `{type:"small",NoAI:1b}`},
			{"salmon", `{NoAI:1b}`},
			// CollarColor is a dye legacy-id byte (0-15) — cat packs
			// it at idx23 (after IS_LYING/RELAX_STATE_ONE), wolf at
			// 21; RED(14) is the defined default and stays unpacked.
			{"cat", `{variant:"minecraft:calico",CollarColor:5b,NoAI:1b}`},
			{"wolf", `{sound_variant:"minecraft:grumpy",CollarColor:9b,NoAI:1b}`},
			// NeutralMob's persistent anger: anger_end_time long +
			// angry_at single int-array uuid (unlike fox Trusted's
			// list). Wolf packs DATA_ANGER_END_TIME at 22 (long);
			// zombified_piglin writes the save keys but packs no
			// wire anger accessor.
			{"wolf", `{anger_end_time:600L,angry_at:[I;1,2,3,4],NoAI:1b}`},
			{"zombified_piglin", `{anger_end_time:600L,angry_at:[I;1,2,3,4],NoAI:1b}`},
			// Bee's DATA_FLAGS_ID byte at 18 (0x08 nectar | 0x04
			// stung | 0x02 rolling) and its own DATA_ANGER_END_TIME
			// long at 19.
			{"bee", `{HasNectar:1b,NoAI:1b}`},
			{"bee", `{HasStung:1b,NoAI:1b}`},
			{"bee", `{anger_end_time:600L,NoAI:1b}`},
			{"bee", `{CannotEnterHiveTicks:99,TicksSincePollination:5,CropsGrownSincePollination:7,NoAI:1b}`},
			{"bee", `{hive_pos:[I;1,2,3],flower_pos:[I;4,5,6],NoAI:1b}`},
			// AI-enabled bee: TicksSincePollination counts up only
			// via customServerAiStep — a NoAI bee freezes it.
			{"bee", `{TicksSincePollination:0,CropsGrownSincePollination:7}`},
		}
		wantMeta := []map[int]string{
			{9: "3:10", 15: "0:1", 20: "21:5"},            // persian
			{9: "3:10", 15: "0:1", 24: "22:1"},            // royal sound
			{9: "3:10", 15: "0:1", 18: "27:2"},            // frog warm
			{9: "3:8", 15: "0:1", 23: "25:0"},             // wolf ashen
			{9: "3:8", 15: "0:1", 24: "26:5"},             // puglin sound
			{9: "3:10", 15: "0:1", 19: "28:2"},            // pig warm
			{9: "3:10", 15: "0:1", 19: "24:1"},            // moody sound
			{9: "3:4", 15: "0:1", 18: "30:2", 19: "31:1"}, // warm + picky
			{9: "3:3", 15: "0:1", 17: "1:0"},              // salmon small
			{9: "3:3", 15: "0:1"},                         // salmon medium
			{9: "3:10", 15: "0:1", 20: "21:3", 23: "1:5"}, // calico + lime collar
			{9: "3:8", 15: "0:1", 21: "1:9", 24: "26:4"},  // cyan collar + grumpy
			{9: "3:8", 15: "0:1", 22: "2:600"},            // angry wolf
			{9: "3:20", 15: "0:1"},                        // angry zombified piglin
			{9: "3:10", 15: "0:1", 18: "0:8"},             // nectar flag
			{9: "3:10", 15: "0:1", 18: "0:4"},             // stung flag
			{9: "3:10", 15: "0:1", 19: "2:600"},           // angry bee
			{9: "3:10", 15: "0:1"},                        // hive-tick ints (no wire)
			{9: "3:10", 15: "0:1"},                        // hive/flower pos (no wire)
			{9: "3:10"},                                   // AI bee (flags byte 0 unpacks)
		}
		// optMeta lists metadata entries the spawn roll MAY pack
		// beside wantMeta: idx -> {serializer, max registry id}. Every
		// summon missing a holder key still rolls that key inside
		// EntityType.spawn (measured live), so the entry appears
		// whenever the roll lands off the defined default.
		optMeta := []map[int][2]int{
			{24: {22, 1}},  // persian: sound roll ∈ cat_sound
			{20: {21, 10}}, // royal: variant roll ∈ cat_variant
			{},             // frog warm: nothing left to roll
			{24: {26, 6}},  // ashen: sound roll ∈ wolf_sound
			{},             // puglin: variant roll = pale-only in plains
			{20: {29, 2}},  // pig warm: sound roll ∈ pig_sound
			{},             // moody: variant roll = temperate in plains
			{},             // warm+picky: nothing rolled
			{},             // salmon: no registry, never rolls
			{},
			{24: {22, 1}}, // calico+collar: sound roll ∈ cat_sound
			{},            // grumpy+collar: variant roll = pale in plains
			{24: {26, 6}}, // angry wolf: sound roll ∈ wolf_sound
			{},
			{}, {}, {}, {}, {}, {},
		}

		for i, s := range summons {
			// Two rows of 12 — the join sends no client_information
			// so vanilla's default view distance (2 → 32-block
			// entity tracking) must still reach the last summons.
			sendCommand(t, pw, fmt.Sprintf("summon minecraft:%s %d -60 %d %s", s.name, (i%12)*2, 3+(i/12)*2, s.nbt))
			time.Sleep(120 * time.Millisecond)
		}
		time.Sleep(1200 * time.Millisecond)

		gotMeta := map[int]map[int]metaEnt{}
		for _, l := range sink.take() {
			i := strings.IndexByte(l, ':')
			if i < 0 || l[:i] != "0x63" {
				continue
			}
			r := protocol.NewPacketReader([]byte(l[i+1:]))
			eid, _ := r.VarInt()
			slot := int(eid) - 2
			if slot < 0 || slot >= len(summons) {
				continue
			}
			rest, _ := r.Read(r.Remaining())
			ents := decodeMetaEntries(t, rest)
			if len(ents) > len(gotMeta[slot]) {
				gotMeta[slot] = ents
			}
		}
		for i, s := range summons {
			got := gotMeta[i]
			if len(got) == 0 {
				t.Fatalf("%s: %s emitted no 0x63 metadata packet", addr, s.name)
			}
			for idx, want := range wantMeta[i] {
				e, ok := got[idx]
				if !ok {
					t.Fatalf("%s: %s missing metadata idx %d (have %v)", addr, s.name, idx, got)
				}
				if gotv := metaEntryValue(e); gotv != want {
					t.Fatalf("%s: %s idx%d = %s, want %s (all %v)", addr, s.name, idx, gotv, want, got)
				}
			}
			for idx := range got {
				if _, ok := wantMeta[i][idx]; !ok {
					bound, opt := optMeta[i][idx]
					if !opt {
						t.Fatalf("%s: %s unexpected metadata idx %d typ%d val%x", addr, s.name, idx, got[idx].typ, got[idx].val)
					}
					e := got[idx]
					if int(e.typ) != bound[0] || len(e.val) == 0 || int(e.val[len(e.val)-1]) > bound[1] {
						t.Fatalf("%s: %s rolled idx%d = typ%d val%x, want typ%d id<=%d", addr, s.name, idx, e.typ, e.val, bound[0], bound[1])
					}
				}
			}
		}

		// Save-map keys: namespaced holder names for the registry
		// types, the plain enum name for salmon's `type`.
		probes := []struct{ sel, mark string }{
			{`@e[type=minecraft:cat,nbt={variant:"minecraft:persian"}]`, "ZZCATV"},
			{`@e[type=minecraft:cat,nbt={sound_variant:"minecraft:royal"}]`, "ZZCATS"},
			{`@e[type=minecraft:frog,nbt={variant:"minecraft:warm"}]`, "ZZFRGV"},
			{`@e[type=minecraft:wolf,nbt={variant:"minecraft:ashen"}]`, "ZZWOLF"},
			{`@e[type=minecraft:wolf,nbt={sound_variant:"minecraft:puglin"}]`, "ZZWLFS"},
			{`@e[type=minecraft:pig,nbt={variant:"minecraft:warm"}]`, "ZZPIGV"},
			{`@e[type=minecraft:cow,nbt={sound_variant:"minecraft:moody"}]`, "ZZCOWS"},
			{`@e[type=minecraft:chicken,nbt={variant:"minecraft:warm",sound_variant:"minecraft:picky"}]`, "ZZCHKN"},
			{`@e[type=minecraft:salmon,nbt={type:"small"}]`, "ZZSLMN"},
			{`@e[type=minecraft:salmon,nbt={type:"medium"}]`, "ZZSLMD"},
			{`@e[type=minecraft:cat,nbt={CollarColor:5b}]`, "ZZCATC"},
			{`@e[type=minecraft:wolf,nbt={CollarColor:9b}]`, "ZWOLFC"},
			{`@e[type=minecraft:cat,nbt={CollarColor:14b}]`, "ZZCATD"},
			{`@e[type=minecraft:wolf,nbt={CollarColor:14b}]`, "ZWOLFD"},
			{`@e[type=minecraft:wolf,nbt={anger_end_time:600L}]`, "ZWEND"},
			{`@e[type=minecraft:wolf,nbt={angry_at:[I;1,2,3,4]}]`, "ZWAT"},
			{`@e[type=minecraft:wolf,nbt={anger_end_time:-1L}]`, "ZWCALM"},
			{`@e[type=minecraft:zombified_piglin,nbt={anger_end_time:600L}]`, "ZZPEND"},
			{`@e[type=minecraft:zombified_piglin,nbt={angry_at:[I;1,2,3,4]}]`, "ZZPAT"},
			{`@e[type=minecraft:bee,nbt={HasNectar:1b}]`, "ZBEN"},
			{`@e[type=minecraft:bee,nbt={HasStung:1b}]`, "ZBES"},
			{`@e[type=minecraft:bee,nbt={anger_end_time:600L}]`, "ZBEA"},
			// The tick counters move post-summon: TicksSincePollina-
			// tion counts up while !HasNectar, CannotEnterHiveTicks
			// counts down — pin the movement with if/unless pairs
			// anchored on the stable CropsGrownSincePollination.
			{`@e[type=minecraft:bee,nbt={CropsGrownSincePollination:7}]`, "ZCGS"},
			// NoAI gate: the counter stays exactly 5 on the NoAI
			// bee (customServerAiStep never runs).
			{`@e[type=minecraft:bee,nbt={TicksSincePollination:5}]`, "ZTSP5"},
			// The AI bee's TicksSincePollination leaves 0.
			{`@e[type=minecraft:bee,nbt={CropsGrownSincePollination:7}] unless entity @e[type=minecraft:bee,nbt={CropsGrownSincePollination:7,TicksSincePollination:0}]`, "ZTSP0"},
			// stayOutOfHiveCountdown decrements even under NoAI.
			{`@e[type=minecraft:bee,nbt={TicksSincePollination:5}] unless entity @e[type=minecraft:bee,nbt={TicksSincePollination:5,CannotEnterHiveTicks:99}]`, "ZCEH0"},
			// aiStep's every-20th-tick drop: hive_pos clears within
			// 20 ticks when it doesn't name a hive block entity;
			// flower_pos has no check and survives.
			{`@e[type=minecraft:bee,nbt={flower_pos:[I;4,5,6]}]`, "ZFPI"},
			{`@e[type=minecraft:bee,nbt={flower_pos:[I;4,5,6]}] unless entity @e[type=minecraft:bee,nbt={hive_pos:[I;1,2,3]}]`, "ZHIVEDROP"},
		}
		for _, p := range probes {
			sendCommand(t, pw, fmt.Sprintf("execute if entity %s run say %s", p.sel, p.mark))
			time.Sleep(120 * time.Millisecond)
		}
		time.Sleep(800 * time.Millisecond)
		hits := map[string]bool{}
		for _, l := range sink.take() {
			for _, p := range probes {
				if strings.Contains(l, p.mark) {
					hits[p.mark] = true
				}
			}
		}
		for _, p := range probes {
			if !hits[p.mark] {
				t.Fatalf("%s: selector probe %s missed (%s)", addr, p.mark, p.sel)
			}
		}
		c.Close()
	}
}

// metaEntryValue renders a decoded entry as "type:value" — varints,
// bytes and bools read as ints, floats truncate (all pinned health
// values are whole).
func metaEntryValue(e metaEnt) string {
	if e.typ == 3 && len(e.val) == 4 {
		r := protocol.NewPacketReader(e.val)
		f, _ := r.Float()
		return fmt.Sprintf("3:%d", int(f))
	}
	if len(e.val) == 0 {
		return fmt.Sprintf("%d:", e.typ)
	}
	if e.typ == 1 {
		r := protocol.NewPacketReader(e.val)
		v, _ := r.VarInt()
		return fmt.Sprintf("1:%d", v)
	}
	if e.typ == 2 {
		r := protocol.NewPacketReader(e.val)
		v, _ := r.VarLong()
		return fmt.Sprintf("2:%d", v)
	}
	return fmt.Sprintf("%d:%d", e.typ, e.val[len(e.val)-1])
}

// TestAngerTimeLegacyParity pins the legacy AngerTime int read:
// NeutralMob.readPersistentAngerSaveData converts it to an ABSOLUTE
// end time via setTimeToRemainAngry → anger_end_time = gameTime(at
// load) + value. Asserting the delta against a gametime query pins
// the conversion without depending on the world age — both servers
// must land in the same small window below 600.
func TestAngerTimeLegacyParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("AngL"),
	}, nil)
	defer targets.cleanup()

	for _, addr := range []string{targets.vanilla, targets.mcgo} {
		c, pr, pw := joinPlayBlackbox(t, addr, "AngL")
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSink(pr, pw)
		time.Sleep(400 * time.Millisecond)
		sendCommand(t, pw, "gamemode spectator AngL")
		sendCommand(t, pw, "tp @s 0 -60 0")
		time.Sleep(400 * time.Millisecond)
		sink.take()

		sendCommand(t, pw, "summon minecraft:wolf 0 -60 3 {AngerTime:600,NoAI:1b}")
		time.Sleep(600 * time.Millisecond)

		st, end := dataGetSel(t, sink, pw, "@e[type=minecraft:wolf,limit=1]", "anger_end_time")
		if st != "query" {
			t.Fatalf("%s: anger_end_time query = %s — want query (leg had no converted end time)", addr, st)
		}
		gt := gameTimeOf(t, pw, sink)
		delta := end - float64(gt)
		// The conversion ran on the summon tick, a few ticks BEFORE
		// the gametime query — delta lands in (600-Δ, 600]; allow a
		// generous 60 ticks of command/query drift.
		if delta < 540 || delta > 600 {
			t.Fatalf("%s: anger_end_time - gametime = %v — want (540, 600] (AngerTime:600 → gameTime+600)", addr, delta)
		}
		c.Close()
	}
}
