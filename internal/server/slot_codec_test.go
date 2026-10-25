package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

type measuredSlot struct {
	Protocol            int32 `json:"protocol"`
	Label, File, SHA256 string
	Length              int
}

func measuredSlots(t *testing.T) (map[int32]map[string][]byte, map[int32]map[string]ItemStack) {
	t.Helper()
	raw, err := os.ReadFile("testdata/slot-profiles/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct{ Cases []measuredSlot }
	if err = json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	bodies := map[int32]map[string][]byte{}
	stacks := map[int32]map[string]ItemStack{}
	for _, fixture := range manifest.Cases {
		raw, err := os.ReadFile(filepath.Join("testdata/slot-profiles", fixture.File))
		if err != nil {
			t.Fatal(err)
		}
		if got := sha256.Sum256(raw); hex.EncodeToString(got[:]) != fixture.SHA256 || len(raw) != fixture.Length {
			t.Fatalf("fixture provenance mismatch %s", fixture.File)
		}
		r := protocol.NewPacketReader(raw)
		_, _ = r.VarInt()
		_, _ = r.VarInt()
		_, _ = r.Short()
		body, err := r.Read(r.Remaining())
		if err != nil {
			t.Fatal(err)
		}
		profile, _ := javaProfileFor(fixture.Protocol)
		reader := protocol.NewPacketReader(body)
		stack, err := profile.readSlot(reader)
		if err != nil || reader.Remaining() != 0 {
			t.Fatalf("actual%d %s slot decode failed remaining=%d: %v", fixture.Protocol, fixture.Label, reader.Remaining(), err)
		}
		if bodies[fixture.Protocol] == nil {
			bodies[fixture.Protocol] = map[string][]byte{}
			stacks[fixture.Protocol] = map[string]ItemStack{}
		}
		bodies[fixture.Protocol][fixture.Label] = body
		stacks[fixture.Protocol][fixture.Label] = stack
	}
	return bodies, stacks
}

func TestActual775776SlotFixturesProjectAndDecode(t *testing.T) {
	bodies, stacks := measuredSlots(t)
	profile775, _ := javaProfileFor(775)
	if len(stacks[775]) != 16 || len(stacks[776]) != 16 {
		t.Fatal("missing measured component fixtures")
	}
	for label, canonical := range stacks[776] {
		t.Run(label, func(t *testing.T) {
			if !reflect.DeepEqual(canonical, stacks[775][label]) {
				t.Fatalf("versions produced different canonical stacks: %+v / %+v", canonical, stacks[775][label])
			}
			var projected protocol.Buffer
			r := protocol.NewPacketReader(bodies[776][label])
			if err := profile775.projectSlotFrom776(r, &projected); err != nil || r.Remaining() != 0 {
				t.Fatalf("raw projection failed: %v", err)
			}
			if !bytes.Equal(projected.Bytes(), bodies[775][label]) {
				t.Fatalf("projection differs from independently captured775 body\n got %x\nwant %x", projected.Bytes(), bodies[775][label])
			}
			for _, number := range []int32{775, 776} {
				p, _ := javaProfileFor(number)
				var encoded protocol.Buffer
				if err := p.writeSlot(&encoded, canonical); err != nil {
					t.Fatalf("modeled%d encoding: %v", number, err)
				}
				rr := protocol.NewPacketReader(encoded.Bytes())
				decoded, err := p.readSlot(rr)
				if err != nil || rr.Remaining() != 0 || !reflect.DeepEqual(decoded, canonical) {
					t.Fatalf("modeled%d roundtrip failed %+v err=%v remaining=%d", number, decoded, err, rr.Remaining())
				}
			}
		})
	}
	if stacks[775]["instrument_sing"].Instrument != "sing_goat_horn" || stacks[775]["bees"].Bees != 1 || stacks[775]["container"].Contents == nil || stacks[775]["container"].Contents[0].Name != "Nested" {
		t.Fatal("fixture fields were silently dropped")
	}
}

func TestCanonicalItemAndComponentCatalogsMatchBuiltinFacts(t *testing.T) {
	s := New("unused")
	defer s.Stop()
	if s.ItemCatalog().Len() != 1537 || s.ComponentCatalog().Len() != 111 {
		t.Fatal("canonical catalog size mismatch")
	}
	for name, id := range world.ItemIDs {
		canonical, ok := s.ItemCatalog().Lookup("minecraft:" + name)
		if !ok || int32(canonical) != id {
			t.Fatalf("item handle drifted %s:%d", name, id)
		}
	}
	if id, ok := s.ComponentCatalog().Lookup("minecraft:instrument"); !ok || id != compInstrument {
		t.Fatal("component handle drifted")
	}
}

func TestSlotProjectionPermutedNumericDomainsAndNoPartialEquipment(t *testing.T) {
	p776, _ := javaProfileFor(776)
	source, err := slotDialectFor(p776)
	if err != nil {
		t.Fatal(err)
	}
	target := *source
	for _, index := range []int{slotItemDomain, slotComponentDomain} {
		domain := *source.builtin[index]
		domain.from776 = append([]int32(nil), domain.from776...)
		if index == slotItemDomain {
			domain.from776[world.ItemIDs["stone"]], domain.from776[world.ItemIDs["dirt"]] = domain.from776[world.ItemIDs["dirt"]], domain.from776[world.ItemIDs["stone"]]
		} else {
			domain.from776[compDamage], domain.from776[compRepairCost] = domain.from776[compRepairCost], domain.from776[compDamage]
		}
		target.builtin[index] = &domain
	}
	var input protocol.Buffer
	writeSlot(&input, ItemStack{ID: world.ItemIDs["stone"], Count: 2, Damage: 7})
	var output protocol.Buffer
	projector := slotProjector{source: source, target: &target}
	if err = projector.slot(protocol.NewPacketReader(input.Bytes()), &output, 0, false); err != nil {
		t.Fatal(err)
	}
	r := protocol.NewPacketReader(output.Bytes())
	count, _ := r.VarInt()
	id, _ := r.VarInt()
	_, _ = r.VarInt()
	_, _ = r.VarInt()
	component, _ := r.VarInt()
	damage, _ := r.VarInt()
	if count != 2 || id != world.ItemIDs["dirt"] || component != compRepairCost || damage != 7 || r.Remaining() != 0 {
		t.Fatalf("numeric permutation missed: %x", output.Bytes())
	}
	p775, _ := javaProfileFor(775)
	var equipment protocol.Buffer
	equipment.VarInt(42)
	equipment.Byte(128)
	writeSlot(&equipment, ItemStack{ID: world.ItemIDs["stone"], Count: 2})
	equipment.Byte(7) // saddle is the eighth vanilla equipment slot
	writeSlot(&equipment, ItemStack{ID: world.ItemIDs["goat_horn"], Count: 1, Instrument: "sing_goat_horn"})
	body, err := p775.projectEquipment776(equipment.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	er := protocol.NewPacketReader(body)
	entity, _ := er.VarInt()
	first, _ := er.Byte()
	a, err := p775.readSlot(er)
	if err != nil {
		t.Fatal(err)
	}
	last, _ := er.Byte()
	b, err := p775.readSlot(er)
	if err != nil || entity != 42 || first != 128 || last != 7 || a.ID != world.ItemIDs["stone"] || b.Instrument != "sing_goat_horn" || er.Remaining() != 0 {
		t.Fatal("equipment continuation/order lost")
	}
	equipment.Byte(0)
	if body, err = p775.projectEquipment776(equipment.Bytes()); err == nil || body != nil {
		t.Fatal("equipment trailing data produced a partial packet")
	}
}
