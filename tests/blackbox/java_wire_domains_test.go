package blackbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/conformance"
	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
)

// TestJavaWireDomainMeasurements records packet facts from the real server.
// These observations establish payload layouts independently of implementation
// codecs; official registry reports separately establish numeric ID mappings.
func TestJavaWireDomainMeasurements(t *testing.T) {
	_ = blackboxJar(t) // Explicit opt-in and installed Java requirement.
	for _, tc := range []struct {
		number  int32
		version string
	}{{775, "26.1"}, {776, "26.2"}} {
		t.Run(tc.version, func(t *testing.T) {
			jar, err := filepath.Abs(filepath.Join("..", "..", "vanilla", "server-"+tc.version+".jar"))
			if err != nil {
				t.Fatal(err)
			}
			jarBytes, err := os.ReadFile(jar)
			if err != nil {
				t.Fatal(err)
			}
			port, ok := freePort()
			if !ok {
				t.Skip("loopback unavailable")
			}
			addr, stop := startVanillaCfgFiles(t, jar, port, map[string]string{"view-distance": "2", "simulation-distance": "2"}, map[string]string{"ops.json": opsJSONFor("WireProbe")})
			defer stop()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			client, err := conformance.OpenPlay(ctx, conformance.Config{Endpoint: addr, Protocol: tc.number, Version: tc.version, Username: "WireProbe", Timeout: 60 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			facts := map[string]any{"protocol": tc.number, "version": tc.version, "source_class": "live official server wire observation", "tool": "conformance.OpenPlay/TestJavaWireDomainMeasurements " + runtime.Version(), "oracle_sha256": fmt.Sprintf("%x", sha256.Sum256(jarBytes))}
			t.Cleanup(func() {
				if dir := os.Getenv("MCGO_CAPTURE_WIRE_DOMAINS"); dir != "" {
					data, err := json.MarshalIndent(facts, "", "  ")
					if err != nil {
						t.Error(err)
						return
					}
					if err := os.MkdirAll(dir, 0755); err != nil {
						t.Error(err)
						return
					}
					if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("protocol-%d.json", tc.number)), append(data, '\n'), 0644); err != nil {
						t.Error(err)
					}
				}
			})
			commands := []struct {
				name, command string
				packet        int32
			}{
				{"sound_direct", "playsound minecraft:ui.button.click player @s ~ ~ ~ 1 1", 0x75},
				{"particle_simple", "particle minecraft:smoke ~ ~1 ~ 0 0 0 0 1 force @s", 0x2f},
				{"particle_block", "particle minecraft:block{block_state:{Name:\"minecraft:stone\"}} ~ ~1 ~ 0 0 0 0 1 force @s", 0x2f},
				{"particle_entity_effect", "particle minecraft:entity_effect{color:16711935,power:1.0f} ~ ~1 ~ 0 0 0 0 1 force @s", 0x2f},
				{"particle_dust", "particle minecraft:dust{color:[1.0f,0.0f,0.0f],scale:1.0f} ~ ~1 ~ 0 0 0 0 1 force @s", 0x2f},
				{"particle_transition", "particle minecraft:dust_color_transition{from_color:[1.0f,0.0f,0.0f],to_color:[0.0f,0.0f,1.0f],scale:1.0f} ~ ~1 ~ 0 0 0 0 1 force @s", 0x2f},
				{"particle_item", "particle minecraft:item{item:{id:\"minecraft:stone\",count:2}} ~ ~1 ~ 0 0 0 0 1 force @s", 0x2f},
				{"particle_vibration", "particle minecraft:vibration{destination:{type:\"minecraft:block\",pos:[8,-60,8]},arrival_in_ticks:7} ~ ~1 ~ 0 0 0 0 1 force @s", 0x2f},
				{"particle_charge", "particle minecraft:sculk_charge{roll:0.5f} ~ ~1 ~ 0 0 0 0 1 force @s", 0x2f},
				{"particle_shriek", "particle minecraft:shriek{delay:7} ~ ~1 ~ 0 0 0 0 1 force @s", 0x2f},
				{"particle_trail", "particle minecraft:trail{color:16711935,duration:7,target:[8.0d,-59.0d,8.0d]} ~ ~1 ~ 0 0 0 0 1 force @s", 0x2f},
				{"particle_tinted", "particle minecraft:tinted_leaves{color:16711935} ~ ~1 ~ 0 0 0 0 1 force @s", 0x2f},
				{"particle_flash", "particle minecraft:flash{color:16711935} ~ ~1 ~ 0 0 0 0 1 force @s", 0x2f},
				{"particle_dragon", "particle minecraft:dragon_breath{power:0.5f} ~ ~1 ~ 0 0 0 0 1 force @s", 0x2f},
			}
			for _, command := range commands {
				if err := client.Command(command.command); err != nil {
					t.Fatal(err)
				}
				packets, feedback := collectWireDomainCommand(t, ctx, client, command.packet)
				facts[command.name] = packets
				t.Logf("%s %s packets=%v feedback=%q", tc.version, command.name, packets, feedback)
			}
			optionNames := map[string]bool{}
			for _, c := range commands {
				if strings.HasPrefix(c.name, "particle_") {
					particle := strings.Fields(c.command)[1]
					particle = strings.SplitN(particle, "{", 2)[0]
					optionNames[particle] = true
				}
			}
			for _, name := range measuredReportRegistryNames(t, tc.version, "minecraft:particle_type") {
				if optionNames[name] {
					continue
				}
				// Blocks with alternate option names share the already measured
				// block-state codec; exercise them explicitly with stone.
				argument := name
				switch name {
				case "minecraft:block_marker", "minecraft:block_crumble", "minecraft:dust_pillar", "minecraft:falling_dust":
					argument += "{block_state:{Name:\"minecraft:stone\"}}"
				}
				if err := client.Command("particle " + argument + " ~ ~1 ~ 0 0 0 0 1 force @s"); err != nil {
					t.Fatal(err)
				}
				packets, feedback := collectWireDomainCommand(t, ctx, client, 0x2f)
				facts["option_probe_"+name] = map[string]any{"packets": packets, "feedback": feedback}
				if len(packets) == 0 {
					t.Logf("%s extra particle %s feedback=%q", tc.version, name, feedback)
				}
			}

			for _, command := range []string{"setblock 8 -60 8 minecraft:red_bed[facing=south,part=head]", "setblock 8 -60 7 minecraft:red_bed[facing=south,part=foot]"} {
				if err := client.Command(command); err != nil {
					t.Fatal(err)
				}
				packets, feedback := collectWireDomainCommand(t, ctx, client, 0x06)
				t.Logf("%s bed update packets=%v feedback=%q", tc.version, packets, feedback)
				facts[command] = packets
			}
			observer, err := conformance.OpenPlay(ctx, conformance.Config{Endpoint: addr, Protocol: tc.number, Version: tc.version, Username: "WireObserve", Timeout: 60 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			var bedEntries []string
			var bedChunkPackets []string
			for _, packet := range observer.InitialPackets() {
				if packet.ID == 0x2d && len(packet.Data) >= 8 && bytes.Equal(packet.Data[:8], make([]byte, 8)) {
					bedChunkPackets = append(bedChunkPackets, hex.EncodeToString(packet.Data))
					bedEntries = append(bedEntries, measuredChunkBlockEntities(t, packet.Data)...)
				}
			}
			facts["bed_chunk_packets"] = bedChunkPackets
			_ = observer.Close()
			facts["bed_chunk_entries"] = bedEntries
			t.Logf("%s bed chunk entries=%v", tc.version, bedEntries)
			if err := client.Command("tick freeze"); err != nil {
				t.Fatal(err)
			}
			_, freezeFeedback := collectWireDomainCommand(t, ctx, client, -1)
			t.Logf("%s freeze=%q", tc.version, freezeFeedback)
			if err := client.Command("summon minecraft:tnt ~6 ~1 ~ {fuse:1}"); err != nil {
				t.Fatal(err)
			}
			earlyExplosions, summonFeedback := collectWireDomainCommand(t, ctx, client, 0x24)
			t.Logf("%s summon=%q", tc.version, summonFeedback)
			if err := client.Command("tick step 100"); err != nil {
				t.Fatal(err)
			}
			var explosion conformance.Packet
			if len(earlyExplosions) > 0 {
				explosion.ID = 0x24
				explosion.Data, err = hex.DecodeString(earlyExplosions[0])
				if err != nil {
					t.Fatal(err)
				}
			} else {
				explodeCtx, cancelExplosion := context.WithTimeout(ctx, 10*time.Second)
				explosion, err = client.WaitPacket(explodeCtx, func(p conformance.Packet) bool { return p.ID == 0x24 })
				cancelExplosion()
				if err != nil {
					t.Fatal(err)
				}
			}
			facts["explosion"] = hex.EncodeToString(explosion.Data)
			if dir := os.Getenv("MCGO_CAPTURE_WIRE_DOMAINS"); dir != "" {
				data, err := json.MarshalIndent(facts, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("protocol-%d.json", tc.number)), append(data, '\n'), 0644); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func collectWireDomainCommand(t *testing.T, ctx context.Context, client *conformance.PlayClient, packetID int32) ([]string, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var packets []string
	for {
		packet, err := client.ReadPacket(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if packet.ID == packetID {
			packets = append(packets, hex.EncodeToString(packet.Data))
		}
		if packet.ID == 0x79 {
			text := chatTextBlackbox(t, packet.Data)
			if strings.Contains(text, "multiplayer.player.joined") || strings.Contains(text, "multiplayer.player.left") {
				continue
			}
			return packets, text
		}
	}
}

func measuredChunkBlockEntities(t *testing.T, data []byte) []string {
	t.Helper()
	r := protocol.NewPacketReader(data)
	cx, _ := r.Int()
	cz, _ := r.Int()
	maps, err := r.VarInt()
	if err != nil {
		t.Fatal(err)
	}
	for range maps {
		_, _ = r.VarInt()
		n, err := r.VarInt()
		if err != nil {
			t.Fatal(err)
		}
		if _, err = r.Read(int(n) * 8); err != nil {
			t.Fatal(err)
		}
	}
	length, err := r.VarInt()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Read(int(length)); err != nil {
		t.Fatal(err)
	}
	count, err := r.VarInt()
	if err != nil {
		t.Fatal(err)
	}
	rest, _ := r.Read(r.Remaining())
	b := bytes.NewReader(rest)
	var out []string
	for range count {
		xz, err := b.ReadByte()
		if err != nil {
			t.Fatal(err)
		}
		y, err := protocol.ReadUShort(b)
		if err != nil {
			t.Fatal(err)
		}
		id, err := protocol.ReadVarInt(b)
		if err != nil {
			t.Fatal(err)
		}
		start := b.Len()
		var value any
		tag, err := b.ReadByte()
		if err != nil {
			t.Fatal(err)
		}
		if tag != 0 {
			if err := b.UnreadByte(); err != nil {
				t.Fatal(err)
			}
			value, err = nbt.ReadUnnamed(b)
			if err != nil {
				t.Fatal(err)
			}
		}
		body := rest[len(rest)-start : len(rest)-b.Len()]
		if cx == 0 && cz == 0 {
			out = append(out, fmt.Sprintf("%d,%d,%d type=%d nbt=%x value=%s", int(cx)*16+int(xz>>4), int(int16(y)), int(cz)*16+int(xz&15), id, body, strings.TrimSpace(fmt.Sprint(value))))
		}
	}
	return out
}

func measuredReportRegistryID(t *testing.T, version, key, name string) int32 {
	t.Helper()
	path := filepath.Join("..", "..", "vanilla", "measure-"+version, "generated", "reports", "registries.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report map[string]struct {
		Entries map[string]struct {
			ID int32 `json:"protocol_id"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	entry, ok := report[key].Entries[name]
	if !ok {
		t.Fatalf("missing measured report entry %s/%s", key, name)
	}
	return entry.ID
}

func measuredReportRegistryNames(t *testing.T, version, key string) []string {
	t.Helper()
	path := filepath.Join("..", "..", "vanilla", "measure-"+version, "generated", "reports", "registries.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report map[string]struct {
		Entries map[string]struct {
			ID int32 `json:"protocol_id"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	entries := report[key].Entries
	names := make([]string, len(entries))
	for name, entry := range entries {
		names[entry.ID] = name
	}
	return names
}

// TestJavaCreativeEquipmentMeasurements is intentionally separate from world
// explosions and visual commands: ingress acceptance and non-creative rejection
// are measured directly through authoritative Inventory, then equipment bytes.
func TestJavaCreativeEquipmentMeasurements(t *testing.T) {
	_ = blackboxJar(t)
	for _, tc := range []struct {
		protocol int32
		version  string
	}{{775, "26.1"}, {776, "26.2"}} {
		t.Run(tc.version, func(t *testing.T) {
			jar, err := filepath.Abs(filepath.Join("..", "..", "vanilla", "server-"+tc.version+".jar"))
			if err != nil {
				t.Fatal(err)
			}
			jarBytes, err := os.ReadFile(jar)
			if err != nil {
				t.Fatal(err)
			}
			port, ok := freePort()
			if !ok {
				t.Skip("loopback unavailable")
			}
			addr, stop := startVanillaCfgFiles(t, jar, port, map[string]string{"view-distance": "2", "simulation-distance": "2"}, map[string]string{"ops.json": opsJSONFor("IngressProbe")})
			defer stop()
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			client, err := conformance.OpenPlay(ctx, conformance.Config{Endpoint: addr, Protocol: tc.protocol, Version: tc.version, Username: "IngressProbe", Timeout: 60 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			facts := map[string]any{"protocol": tc.protocol, "version": tc.version, "source_class": "live official server wire observation", "tool": "conformance.OpenPlay/TestJavaCreativeEquipmentMeasurements " + runtime.Version(), "oracle_sha256": fmt.Sprintf("%x", sha256.Sum256(jarBytes))}
			t.Cleanup(func() {
				if dir := os.Getenv("MCGO_CAPTURE_WIRE_DOMAINS"); dir != "" {
					facts["wire_transcript"] = client.Transcript()
					data, err := json.MarshalIndent(facts, "", "  ")
					if err != nil {
						t.Error(err)
						return
					}
					if err := os.MkdirAll(dir, 0755); err != nil {
						t.Error(err)
						return
					}
					if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("ingress-%d.json", tc.protocol)), append(data, '\n'), 0644); err != nil {
						t.Error(err)
					}
				}
			})
			if err := client.Command("gamemode creative @s"); err != nil {
				t.Fatal(err)
			}
			mode, attempts := pollWireFeedback(t, ctx, client, "data get entity @s playerGameType", "commands.data.entity.query", func(text string) bool { return strings.HasSuffix(text, "1") })
			facts["creative_mode_query"], facts["creative_mode_query_attempts"] = mode, attempts
			var held protocol.Buffer
			held.Short(0)
			if err := client.SendPacket(0x35, held.Bytes()); err != nil {
				t.Fatal(err)
			}
			var plain protocol.Buffer
			plain.Short(36)
			plain.VarInt(1)
			plain.VarInt(measuredReportRegistryID(t, tc.version, "minecraft:item", "minecraft:iron_pickaxe"))
			plain.VarInt(0)
			plain.VarInt(0)
			facts["plain_creative_packet"] = hex.EncodeToString(plain.Bytes())
			if err := client.SendPacket(0x38, plain.Bytes()); err != nil {
				t.Fatal(err)
			}
			plainInventory, plainAttempts := pollWireFeedback(t, ctx, client, "data get entity @s Inventory", "commands.data.entity.query", func(text string) bool { return strings.Contains(text, "iron_pickaxe") })
			facts["plain_creative_inventory"], facts["plain_attempts"] = plainInventory, plainAttempts
			creativePacket := func(damage byte) []byte {
				var b protocol.Buffer
				b.Short(36)
				b.VarInt(1)
				b.VarInt(measuredReportRegistryID(t, tc.version, "minecraft:item", "minecraft:iron_pickaxe"))
				b.VarInt(1)
				b.VarInt(0)
				b.VarInt(measuredReportRegistryID(t, tc.version, "minecraft:data_component_type", "minecraft:damage"))
				b.VarInt(1)
				b.Byte(damage)
				return b.Bytes()
			}
			for _, step := range []struct {
				mode   string
				damage byte
			}{{"creative", 7}, {"survival", 9}} {
				if step.mode == "survival" {
					if err := client.Command("gamemode survival"); err != nil {
						t.Fatal(err)
					}
					mode, attempts := pollWireFeedback(t, ctx, client, "data get entity @s playerGameType", "commands.data.entity.query", func(text string) bool { return strings.HasSuffix(text, "0") })
					facts["survival_mode_query"], facts["survival_mode_query_attempts"] = mode, attempts
				}
				body := creativePacket(step.damage)
				facts["creative_packet_"+step.mode] = hex.EncodeToString(body)
				if err := client.SendPacket(0x38, body); err != nil {
					t.Fatal(err)
				}
				if step.mode == "survival" {
					var x, y, z float64
					for _, packet := range client.InitialPackets() {
						if packet.ID == 0x48 {
							r := protocol.NewPacketReader(packet.Data)
							_, _ = r.VarInt()
							x, _ = r.Double()
							y, _ = r.Double()
							z, _ = r.Double()
							break
						}
					}
					x += 0.125
					if err := client.Move(x, y, z, 0, 0, true); err != nil {
						t.Fatal(err)
					}
					position, attempts := pollWireFeedback(t, ctx, client, "data get entity @s Pos[0]", "commands.data.entity.query", func(text string) bool { return strings.HasSuffix(text, strconv.FormatFloat(x, 'f', -1, 64)+"d") })
					facts["survival_later_movement_barrier"], facts["survival_barrier_attempts"] = position, attempts
				}
				feedback, attempts := pollWireFeedback(t, ctx, client, `data get entity @s Inventory[{Slot:0b}].components."minecraft:damage"`, "commands.data.entity.query", func(text string) bool {
					return text == "commands.data.entity.queryIngressProbe7"
				})
				facts["damage_query_"+step.mode], facts["inventory_attempts_"+step.mode] = feedback, attempts
				inventory, _ := pollWireFeedback(t, ctx, client, "data get entity @s Inventory", "commands.data.entity.query", func(text string) bool { return strings.Contains(text, "iron_pickaxe") })
				facts["inventory_"+step.mode] = inventory
				t.Logf("%s %s damage query=%q inventory=%q", tc.version, step.mode, feedback, inventory)

			}
			if err := client.Command(`summon minecraft:zombie 5 -60 5 {NoAI:1b,NoGravity:1b,Health:17f,equipment:{mainhand:{id:"minecraft:bow",count:1},head:{id:"minecraft:iron_helmet",count:1}}}`); err != nil {
				t.Fatal(err)
			}
			entityCtx, entityCancel := context.WithTimeout(ctx, 10*time.Second)
			defer entityCancel()
			zombieType := measuredReportRegistryID(t, tc.version, "minecraft:entity_type", "minecraft:zombie")
			spawn, err := client.WaitPacket(entityCtx, func(p conformance.Packet) bool {
				if p.ID != 0x01 {
					return false
				}
				r := protocol.NewPacketReader(p.Data)
				_, _ = r.VarInt()
				_, _ = r.UUID()
				kind, _ := r.VarInt()
				return kind == zombieType
			})
			if err != nil {
				t.Fatal(err)
			}
			eid, _ := protocol.NewPacketReader(spawn.Data).VarInt()
			facts["zombie_spawn"] = hex.EncodeToString(spawn.Data)
			equipment, err := client.WaitPacket(entityCtx, func(p conformance.Packet) bool {
				if p.ID != 0x66 {
					return false
				}
				id, _ := protocol.NewPacketReader(p.Data).VarInt()
				return id == eid
			})
			if err != nil {
				t.Fatal(err)
			}
			facts["zombie_equipment"] = hex.EncodeToString(equipment.Data)
			r := protocol.NewPacketReader(equipment.Data)
			_, _ = r.VarInt()
			for i, entry := range []struct {
				slot byte
				item string
			}{{0, "minecraft:bow"}, {5, "minecraft:iron_helmet"}} {
				wireSlot, err := r.Byte()
				if err != nil {
					t.Fatal(err)
				}
				if wireSlot&0x7f != entry.slot || (wireSlot&0x80 != 0) != (i == 0) {
					t.Fatalf("equipment slot/continuation=%#x", wireSlot)
				}
				count, _ := r.VarInt()
				item, _ := r.VarInt()
				added, _ := r.VarInt()
				removed, _ := r.VarInt()
				if count != 1 || item != measuredReportRegistryID(t, tc.version, "minecraft:item", entry.item) || added != 0 || removed != 0 {
					t.Fatalf("equipment entry%d: count=%d item=%d patch=%d/%d", i, count, item, added, removed)
				}
			}
			if r.Remaining() != 0 {
				t.Fatalf("trailing equipment bytes%d", r.Remaining())
			}
			facts["completed"] = true
			t.Logf("%s equipment=%x", tc.version, equipment.Data)
		})
	}
}

func waitWireFeedback(t *testing.T, ctx context.Context, client *conformance.PlayClient, contains string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		packet, err := client.ReadPacket(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if packet.ID == 0x79 {
			text := chatTextBlackbox(t, packet.Data)
			if strings.Contains(text, contains) {
				return text
			}
		}
	}
}

func pollWireFeedback(t *testing.T, ctx context.Context, client *conformance.PlayClient, command, contains string, ready func(string) bool) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for attempt := 1; ; attempt++ {
		if err := client.Command(command); err != nil {
			t.Fatal(err)
		}
		feedback := waitWireFeedback(t, ctx, client, "")
		if strings.Contains(feedback, contains) && ready(feedback) {
			return feedback, attempt
		}
		if attempt <= 3 {
			t.Logf("initial state query %s => %s", command, feedback)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wire state never committed after%d queries: %s", attempt, feedback)
		case <-time.After(50 * time.Millisecond):
		}
	}
}
