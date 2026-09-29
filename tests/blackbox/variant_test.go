//go:build blackbox

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
	return fmt.Sprintf("%d:%d", e.typ, e.val[len(e.val)-1])
}
