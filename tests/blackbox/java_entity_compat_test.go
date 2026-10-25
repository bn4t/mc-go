package blackbox

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"testing"
	"time"

	"mc-go/conformance"
	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// Numeric assertions come from the official 26.1/26.2 reports and independent
// runtime accessor measurements (tools/genentitymetadata). The client validates
// complete login/configuration before these real mixed-viewer gameplay checks.
func TestMixedJavaEntityProfiles(t *testing.T) {
	admitted := false
	for _, version := range server.SupportedVersions() {
		if version.Protocol == 775 {
			admitted = true
		}
	}
	if !admitted {
		t.Fatal("advertised protocol 775 support is unavailable")
	}
	addr := requireMcGoOnlyCfg(t, func(s *server.Server) { s.ViewDistance = 2; s.TimeSyncEvery = -1 })
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	old, err := conformance.OpenPlay(ctx, conformance.Config{Endpoint: addr, Protocol: 775, Version: "26.1", Username: "EntityOld", Timeout: 60 * time.Second, TranscriptLimit: 8 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	current, err := conformance.OpenPlay(ctx, conformance.Config{Endpoint: addr, Protocol: 776, Version: "26.2", Username: "EntityNew", Timeout: 60 * time.Second, TranscriptLimit: 8 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	for _, client := range []*conformance.PlayClient{old, current} {
		if err := checkCompatInitialAttributes(client); err != nil {
			t.Fatal(err)
		}
	}
	if err := current.Command("tick freeze"); err != nil {
		t.Fatal(err)
	}
	if _, err := current.WaitPacket(ctx, func(packet conformance.Packet) bool {
		return packet.ID == 127 && len(packet.Data) == 5 && packet.Data[4] == 1
	}); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for _, client := range []*conformance.PlayClient{old, current} {
		go func(client *conformance.PlayClient) { results <- collectCompatEntityPackets(ctx, client) }(client)
	}
	for _, command := range []string{
		"kill @e[type=!minecraft:player]",
		"summon minecraft:slime 3.5 -60 3.5 {Size:3,NoAI:1b,NoGravity:1b}",
		"summon minecraft:zombie 4.5 -60 3.5 {NoAI:1b,NoGravity:1b,Health:17.0f}",
		"attribute @e[type=minecraft:zombie,limit=1] minecraft:movement_speed base set 0.125",
		"tick step 1",
	} {
		if err := current.Command(command); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
}

func checkCompatInitialAttributes(client *conformance.PlayClient) error {
	want := map[int32]bool{8: true, 13: true, 26: true}
	if client.Protocol() == 775 {
		want = map[int32]bool{6: true, 10: true, 22: true}
	}
	seen := map[int32]bool{}
	for _, packet := range client.InitialPackets() {
		if packet.ID != 131 {
			continue
		}
		eid, entries, err := readCompatAttributes(packet.Data)
		if err != nil {
			return err
		}
		if eid != client.EntityID {
			continue
		}
		for id := range entries {
			if !want[id] {
				return fmt.Errorf("protocol%d unexpected initial attribute%d", client.Protocol(), id)
			}
			seen[id] = true
		}
	}
	if len(seen) != 3 {
		return fmt.Errorf("protocol%d did not reach complete player attributes:%v", client.Protocol(), seen)
	}
	return nil
}

func collectCompatEntityPackets(ctx context.Context, client *conformance.PlayClient) error {
	slimeType, zombieType, speedAttribute, sizeIndex := int32(117), int32(151), int32(26), byte(18)
	if client.Protocol() == 775 {
		zombieType, speedAttribute, sizeIndex = 150, 22, 16
	}
	entityTypes := map[int32]int32{}
	var gotSlime, gotZombie, gotSpeed bool
	for !gotSlime || !gotZombie || !gotSpeed {
		packet, err := client.ReadPacket(ctx)
		if err != nil {
			return fmt.Errorf("protocol%d entity stream:%w", client.Protocol(), err)
		}
		switch packet.ID {
		case 1:
			r := bytes.NewReader(packet.Data)
			eid, err := protocol.ReadVarInt(r)
			if err != nil {
				return err
			}
			if r.Len() < 16 {
				return fmt.Errorf("truncated spawn UUID")
			}
			_, _ = r.Seek(16, 1)
			kind, err := protocol.ReadVarInt(r)
			if err != nil {
				return err
			}
			entityTypes[eid] = kind
		case 99:
			r := bytes.NewReader(packet.Data)
			eid, err := protocol.ReadVarInt(r)
			if err != nil {
				return err
			}
			kind := entityTypes[eid]
			if kind != slimeType && kind != zombieType {
				continue
			}
			values, err := readCompatMetadata(r)
			if err != nil {
				return fmt.Errorf("protocol%d metadata:%w", client.Protocol(), err)
			}
			if kind == slimeType {
				if size, ok := values[sizeIndex].(int32); ok && size == 4 {
					gotSlime = true
				}
				if other := byte(18); sizeIndex == 18 {
					other = 16
					if _, wrong := values[other].(int32); wrong {
						return fmt.Errorf("protocol776 slime size used obsolete accessor16")
					}
				}
			}
			if kind == zombieType {
				if health, ok := values[9].(float32); ok && health == 17 {
					gotZombie = true
				}
			}
		case 131:
			eid, entries, err := readCompatAttributes(packet.Data)
			if err != nil {
				return err
			}
			if entityTypes[eid] != zombieType {
				continue
			}
			if value, ok := entries[speedAttribute]; ok && value == .125 {
				gotSpeed = true
			}
		}
	}
	return nil
}

func readCompatAttributes(data []byte) (int32, map[int32]float64, error) {
	r := bytes.NewReader(data)
	eid, err := protocol.ReadVarInt(r)
	if err != nil {
		return 0, nil, err
	}
	count, err := protocol.ReadVarInt(r)
	if err != nil || count < 0 || count > 128 {
		return 0, nil, fmt.Errorf("invalid attribute count%d:%v", count, err)
	}
	entries := map[int32]float64{}
	for i := int32(0); i < count; i++ {
		id, err := protocol.ReadVarInt(r)
		if err != nil {
			return 0, nil, err
		}
		var bits uint64
		if err = binary.Read(r, binary.BigEndian, &bits); err != nil {
			return 0, nil, err
		}
		entries[id] = math.Float64frombits(bits)
		mods, err := protocol.ReadVarInt(r)
		if err != nil || mods < 0 || mods > 1024 {
			return 0, nil, fmt.Errorf("invalid modifiers")
		}
		for j := int32(0); j < mods; j++ {
			if _, err = protocol.ReadString(r); err != nil {
				return 0, nil, err
			}
			if _, err = r.Seek(8, 1); err != nil {
				return 0, nil, err
			}
			if _, err = protocol.ReadVarInt(r); err != nil {
				return 0, nil, err
			}
		}
	}
	if r.Len() != 0 {
		return 0, nil, fmt.Errorf("trailing attribute bytes")
	}
	return eid, entries, nil
}

// This targeted observer only needs the value families emitted by the selected
// ordinary mobs; unknown serializers fail instead of losing packet alignment.
func readCompatMetadata(r *bytes.Reader) (map[byte]any, error) {
	out := map[byte]any{}
	for {
		index, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		if index == 255 {
			if r.Len() != 0 {
				return nil, fmt.Errorf("trailing metadata bytes")
			}
			return out, nil
		}
		kind, err := protocol.ReadVarInt(r)
		if err != nil {
			return nil, err
		}
		switch kind {
		case 0, 8:
			value, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			out[index] = value
		case 1, 12, 14, 15, 19, 20:
			value, err := protocol.ReadVarInt(r)
			if err != nil {
				return nil, err
			}
			out[index] = value
		case 2:
			if _, err := protocol.ReadVarLong(r); err != nil {
				return nil, err
			}
		case 3:
			var bits uint32
			if err := binary.Read(r, binary.BigEndian, &bits); err != nil {
				return nil, err
			}
			out[index] = math.Float32frombits(bits)
		case 4:
			if _, err := protocol.ReadString(r); err != nil {
				return nil, err
			}
		case 5:
			if _, err := nbt.ReadUnnamed(r); err != nil {
				return nil, err
			}
		case 6:
			present, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			if present != 0 {
				if _, err = nbt.ReadUnnamed(r); err != nil {
					return nil, err
				}
			}
		case 10:
			if r.Len() < 8 {
				return nil, fmt.Errorf("truncated position")
			}
			_, _ = r.Seek(8, 1)
		case 11:
			present, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			if present != 0 {
				if r.Len() < 8 {
					return nil, fmt.Errorf("truncated optional position")
				}
				_, _ = r.Seek(8, 1)
			}
		case 17:
			count, err := protocol.ReadVarInt(r)
			if err != nil || count != 0 {
				return nil, fmt.Errorf("unexpected particle list in no-effect fixture")
			}
		default:
			return nil, fmt.Errorf("unexpected selected-mob serializer%d", kind)
		}
	}
}
