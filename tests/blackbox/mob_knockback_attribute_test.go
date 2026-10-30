package blackbox

import (
	"encoding/binary"
	"fmt"
	"math"
	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMobCanonicalKnockbackParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor("P")}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
		s.OpsRequired = true
		s.SetOpLevel("P", 4)
	})
	defer tg.cleanup()
	for _, leg := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		c, r, w := joinPlayBlackbox(t, leg.addr, "P")
		defer c.Close()
		sendClientInformation(t, w)
		sink := newChatSinkResolvedTP(r, w)
		play := int32(0)
		statsPackets := 0
		havePlay := false
		serial := 0
		owner := int32(-1)
		spawns := map[[16]byte]int32{}
		barrier := func() []string {
			serial++
			marker := fmt.Sprintf("MOB_KB_%d", serial)
			sendCommand(t, w, "say "+marker)
			lines := waitForExactSayBB(t, sink, marker, 15*time.Second)
			for _, line := range lines {
				prefix, body, ok := strings.Cut(line, ":")
				if !ok {
					continue
				}
				reader := protocol.NewPacketReader([]byte(body))
				if prefix == "0x3" {
					statsPackets++
					n, _ := reader.VarInt()
					for i := int32(0); i < n; i++ {
						cat, _ := reader.VarInt()
						id, _ := reader.VarInt()
						value, e := reader.VarInt()
						if e == nil && cat == 8 && id == server.CustomStatIDs["play_time"] {
							play = value
							havePlay = true
						}
					}
					continue
				}
				if prefix == fmt.Sprintf("%#x", server.IDPlaySpawnEntity) {
					id, e := reader.VarInt()
					uuid, e2 := reader.UUID()
					if e == nil && e2 == nil {
						spawns[uuid] = id
					}
				}
				if prefix == fmt.Sprintf("%#x", server.IDPlayJoinGame) {
					owner, _ = reader.Int()
				}
			}
			return lines
		}
		playerTime := func() int32 {
			before := statsPackets
			var request protocol.Buffer
			request.VarInt(1)
			w.WritePacket(server.IDPlayClientStatusSB, request.Bytes())
			deadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) {
				barrier()
				if statsPackets > before {
					if havePlay {
						return play
					}
					before = statsPackets
					w.WritePacket(server.IDPlayClientStatusSB, request.Bytes())
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatal("no actual player ticks")
			return 0
		}
		settle := func(n int32) {
			before := playerTime()
			deadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) {
				if playerTime()-before >= n {
					return
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatal("player cooldown clock stalled")
		}
		sendCommand(t, w, "tick freeze")
		barrier()
		for index, tc := range []struct {
			name, kind, base, mod string
			attack, sprint        bool
		}{
			{"zero", "pig", "0", "", false, false}, {"half", "pig", "0.5", "", false, false}, {"minimum", "pig", "-4", "", false, false}, {"maximum", "pig", "2", "", false, false}, {"modifier", "pig", "0", "-1", false, false},
			{"hoglin_default", "hoglin", "", "", false, false}, {"ravager_default", "ravager", "", "", false, false}, {"golem_default", "iron_golem", "", "", false, false},
			{"attack_half", "pig", "0.5", "", true, false}, {"sprint_half", "pig", "0.5", "", true, true},
		} {
			target := "@e[tag=KB_" + tc.name + ",limit=1]"
			sendCommand(t, w, "tp @e[tag=KB] 40 -60 40")
			sendCommand(t, w, "tp @s 8 -60 8 180 0")
			sendCommand(t, w, fmt.Sprintf("summon minecraft:%s 8 -60 6 {Tags:[\"KB\",\"KB_%s\"],NoAI:1b,NoGravity:1b,UUID:[I;1,2,3,%d]}", tc.kind, tc.name, index+100))
			if tc.base != "" {
				sendCommand(t, w, "attribute "+target+" knockback_resistance base set "+tc.base)
			}
			if tc.mod != "" {
				sendCommand(t, w, "attribute "+target+" knockback_resistance modifier add test:kb "+tc.mod+" add_value")
			}
			sendCommand(t, w, "execute if entity "+target+" run say MOB_READY")
			if !structureSaidBB(barrier(), "MOB_READY") {
				t.Fatal("mob preparation failed", leg.name, tc.name)
			}
			sendCommand(t, w, "attribute "+target+" knockback_resistance get")
			before := barrier()
			if tc.attack {
				settle(14)
				var uuid [16]byte
				binary.BigEndian.PutUint32(uuid[0:4], 1)
				binary.BigEndian.PutUint32(uuid[4:8], 2)
				binary.BigEndian.PutUint32(uuid[8:12], 3)
				binary.BigEndian.PutUint32(uuid[12:16], uint32(index+100))
				deadline := time.Now().Add(15 * time.Second)
				for spawns[uuid] == 0 && time.Now().Before(deadline) {
					barrier()
				}
				if spawns[uuid] == 0 {
					t.Fatal("target was not tracked", leg.name, tc.name)
				}
				var move protocol.Buffer
				move.Double(8.5)
				move.Double(-60)
				move.Double(8.5)
				move.Byte(1)
				if err := w.WritePacket(server.IDPlayMovePosSB, move.Bytes()); err != nil {
					t.Fatal(err)
				}
				sendCommand(t, w, "data get entity @s Pos")
				sendCommand(t, w, "execute positioned 8 -60 8 if entity @s[distance=..1] run say ACTOR_READY")
				actor := barrier()

				if !structureSaidBB(actor, "ACTOR_READY") {
					t.Fatal("actor position outside attack arena", leg.name, tc.name)
				}
				sendCommand(t, w, "execute if data entity "+target+" {Health:10f} run say HEALTH_READY")
				if !structureSaidBB(barrier(), "HEALTH_READY") {
					t.Fatal("attack target health baseline", leg.name, tc.name)
				}
				if tc.sprint {
					if owner < 0 {
						t.Fatal("no owner id")
					}
					var sprint protocol.Buffer
					sprint.VarInt(owner)
					sprint.VarInt(1)
					sprint.VarInt(0)
					w.WritePacket(server.IDPlayPlayerCommandSB, sprint.Bytes())
				}
				var attack protocol.Buffer
				attack.VarInt(spawns[uuid])
				if err := w.WritePacket(server.IDPlayAttackSB, attack.Bytes()); err != nil {
					t.Fatal(err)
				}
			} else {
				sendCommand(t, w, "damage "+target+" 1 minecraft:mob_attack by P")
			}
			if tc.attack {
				applied := false
				deadline := time.Now().Add(5 * time.Second)
				for !applied && time.Now().Before(deadline) {
					sendCommand(t, w, "execute unless data entity "+target+" {Health:10f} run say MOB_HIT")
					applied = structureSaidBB(barrier(), "MOB_HIT")
				}
				if !applied {
					t.Fatal("actual attack did not damage target", leg.name, tc.name)
				}
			}
			sendCommand(t, w, "data get entity "+target+" Motion")
			sendCommand(t, w, "data get entity "+target+" Health")
			after := barrier()
			var effective float64
			gotQuery := false
			for _, line := range before {
				key, args, ok := componentArgs(line)
				if !ok || key != "commands.attribute.value.get.success" || len(args) != 3 {
					continue
				}
				value, ok := args[2].(map[string]any)
				if !ok {
					t.Fatal("attribute value component", args[2])
				}
				var err error
				effective, err = strconv.ParseFloat(fmt.Sprint(value[""]), 64)
				if err != nil {
					t.Fatal(err)
				}
				gotQuery = true
			}
			wantEffective := []float64{0, .5, -2, 1, -1, .6000000238418579, .75, 1, .5, .5}[index]
			if !gotQuery || effective != wantEffective {
				t.Fatalf("%s %s effective=%v present=%v want=%v", leg.name, tc.name, effective, gotQuery, wantEffective)
			}
			foundMotion := false
			damageApplied := false
			foundHealth := false
			health := float64(0)
			var motion [3]float64
			for _, line := range after {
				key, args, ok := componentArgs(line)
				if !ok {
					continue
				}
				if key == "commands.damage.success" {
					damageApplied = true
				}
				if key != "commands.data.entity.query" || len(args) != 2 {
					continue
				}
				text := mobKBComponentTextBB(args[1])
				if !strings.HasPrefix(text, "[") {
					value, err := strconv.ParseFloat(strings.TrimSuffix(text, "f"), 64)
					if err != nil {
						t.Fatal("Health component", text, err)
					}
					health = value
					foundHealth = true
					continue
				}
				parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(text, "["), "]"), ",")
				if len(parts) != 3 {
					t.Fatal("invalid Motion list", text)
				}
				for i, part := range parts {
					value, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(part), "d"), 64)
					if err != nil {
						t.Fatal("Motion component", text, err)
					}
					motion[i] = value
				}
				foundMotion = true
			}
			if !foundMotion || !foundHealth || (!tc.attack && !damageApplied) {
				t.Fatal("missing applied damage/Motion/Health receipt", leg.name, tc.name)
			}
			if tc.attack && health != 9 {
				t.Fatal("attack health", leg.name, tc.name, health)
			}
			magnitude := math.Hypot(motion[0], motion[2])
			// Frozen simulation and NoAI leave the immediate push available to /data.
			// Both the teleport and explicit movement place the attacker at (8.5,-60,8.5).
			wantMagnitude := []float64{.4000000059604645, .20000000298023224, 1.2000000178813934, 0, .800000011920929, .15999999284744248, .10000000149011612, 0, .20000000298023224, .3500000014901161}[index]
			if math.Abs(magnitude-wantMagnitude) > 1e-12 {
				t.Fatalf("%s %s horizontal=%.17g want=%.17g motion=%v", leg.name, tc.name, magnitude, wantMagnitude, motion)
			}
			t.Logf("%s %s effective=%g horizontal=%.17g", leg.name, tc.name, effective, magnitude)
		}
	}
}

// Styled SNBT mixes text components with compact empty-key punctuation.
func mobKBComponentTextBB(value any) string {
	switch c := value.(type) {
	case string:
		return c
	case map[string]any:
		text, _ := c["text"].(string)
		if compact, ok := c[""].(string); ok {
			text = compact
		}
		return text + mobKBComponentTextBB(c["extra"])
	case nbt.List:
		var b strings.Builder
		for _, part := range c.Items {
			b.WriteString(mobKBComponentTextBB(part))
		}
		return b.String()
	case []any:
		var b strings.Builder
		for _, part := range c {
			b.WriteString(mobKBComponentTextBB(part))
		}
		return b.String()
	}
	return ""
}

func TestMobKnockbackComponentText(t *testing.T) {
	component := map[string]any{"text": "", "extra": nbt.List{Elem: nbt.TagCompound, Items: []any{
		map[string]any{"": "["}, map[string]any{"text": "0.0", "color": "gold"}, map[string]any{"text": "d"}, map[string]any{"": ", "}, map[string]any{"text": "-0.4d"}, map[string]any{"": "]"},
	}}}
	if got := mobKBComponentTextBB(component); got != "[0.0d, -0.4d]" {
		t.Fatalf("compact SNBT punctuation lost: %q", got)
	}
}
