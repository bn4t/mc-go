package server

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"strconv"
	"testing"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

func TestLiveCreativeAndEquipmentFixturesMatchProfiles(t *testing.T) {
	server := New("unused")
	defer server.Stop()
	fixtures := map[int32]map[string]any{}
	for _, number := range []int32{775, 776} {
		raw, err := os.ReadFile("testdata/slot-profiles/ingress-" + strconv.Itoa(int(number)) + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var facts map[string]any
		if err = json.Unmarshal(raw, &facts); err != nil {
			t.Fatal(err)
		}
		fixtures[number] = facts
		if facts["completed"] != true || facts["creative_mode_query"] != "commands.data.entity.queryIngressProbe1" || facts["survival_mode_query"] != "commands.data.entity.queryIngressProbe0" || facts["damage_query_creative"] != "commands.data.entity.queryIngressProbe7" || facts["damage_query_survival"] != "commands.data.entity.queryIngressProbe7" {
			t.Fatal("fixture lacks controlled live permission/acceptance proof")
		}
		p, _ := javaProfileFor(number)
		sess := slotTestSession(p)
		sess.server = server
		for _, step := range []struct {
			field  string
			mode   byte
			damage int32
		}{{"plain_creative_packet", GameModeCreative, 0}, {"creative_packet_creative", GameModeCreative, 7}, {"creative_packet_survival", GameModeSurvival, 7}} {
			body, err := hex.DecodeString(facts[step.field].(string))
			if err != nil {
				t.Fatal(err)
			}
			sess.setMode(step.mode)
			server.handleCreativeSlot(sess, protocol.Packet{ID: IDPlayCreativeSlotSB, Data: body})
			stack := sess.stackAt(36)
			if stack.ID != world.ItemIDs["iron_pickaxe"] || stack.Count != 1 || stack.Damage != step.damage {
				t.Fatalf("protocol%d controlled %s -> %+v", number, step.field, stack)
			}
		}
		equipment, err := hex.DecodeString(facts["zombie_equipment"].(string))
		if err != nil {
			t.Fatal(err)
		}
		r := protocol.NewPacketReader(equipment)
		_, _ = r.VarInt()
		for i, item := range []string{"bow", "iron_helmet"} {
			slot, err := r.Byte()
			want := byte(128)
			if i == 1 {
				want = 5
			}
			if err != nil || slot != want {
				t.Fatal("actual equipment continuation/order drift")
			}
			stack, err := p.readSlot(r)
			if err != nil || stack.ID != world.ItemIDs[item] || stack.Count != 1 {
				t.Fatalf("actual equipment %d/%s: %+v %v", number, item, stack, err)
			}
		}
		if r.Remaining() != 0 {
			t.Fatal("actual equipment fixture has trailing bytes")
		}
	}
	source, _ := hex.DecodeString(fixtures[776]["zombie_equipment"].(string))
	expected, _ := hex.DecodeString(fixtures[775]["zombie_equipment"].(string))
	p, _ := javaProfileFor(775)
	projected, err := p.projectEquipment776(source)
	if err != nil || !bytes.Equal(projected, expected) {
		t.Fatalf("actual equipment projection differs: %x want%x err%v", projected, expected, err)
	}
}
