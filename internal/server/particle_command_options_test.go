package server

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strconv"
	"testing"

	"mc-go/internal/protocol"
)

// These are the command arguments used for the independently captured official
// 775/776 packets in java-wire-domains. Exercise command construction and the
// viewer adapter together: copying a valid captured packet through an adapter
// alone would not detect a command that omitted its particle option value.
func TestParticleCommandOptionsMatchOfficialPackets(t *testing.T) {
	commands := []struct{ fact, token string }{
		{"particle_simple", "minecraft:smoke"},
		{"particle_block", `minecraft:block{block_state:{Name:"minecraft:stone"}}`},
		{"particle_dust", `minecraft:dust{color:[1.0f,0.0f,0.0f],scale:1.0f}`},
		{"particle_transition", `minecraft:dust_color_transition{from_color:[1.0f,0.0f,0.0f],to_color:[0.0f,0.0f,1.0f],scale:1.0f}`},
		{"particle_item", `minecraft:item{item:{id:"minecraft:stone",count:2}}`},
		{"particle_entity_effect", `minecraft:entity_effect{color:16711935,power:1.0f}`},
		{"particle_vibration", `minecraft:vibration{destination:{type:"minecraft:block",pos:[8,-60,8]},arrival_in_ticks:7}`},
		{"particle_charge", `minecraft:sculk_charge{roll:0.5f}`},
		{"particle_shriek", `minecraft:shriek{delay:7}`},
		{"particle_trail", `minecraft:trail{color:16711935,duration:7,target:[8.0d,-59.0d,8.0d]}`},
		{"particle_tinted", `minecraft:tinted_leaves{color:16711935}`},
		{"particle_flash", `minecraft:flash{color:16711935}`},
		{"particle_dragon", `minecraft:dragon_breath{power:0.5f}`},
	}
	for _, number := range []int32{775, 776} {
		facts := wireDomainFacts(t, int(number))
		for _, command := range commands {
			t.Run(fmt.Sprintf("%d/%s", number, command.fact), func(t *testing.T) {
				packets := factPackets(facts[command.fact])
				if len(packets) != 1 {
					t.Fatalf("official observation has %d packets", len(packets))
				}
				want, err := hex.DecodeString(packets[0])
				if err != nil {
					t.Fatal(err)
				}
				// Feed the observed event's physical inputs into the command.
				// The complete target body, including its registry ID and option
				// bytes after offset46, remains an independent wire expectation.
				r := protocol.NewPacketReader(want)
				_, _ = r.Byte()
				_, _ = r.Byte()
				args := []string{command.token}
				for range 3 {
					v, err := r.Double()
					if err != nil {
						t.Fatal(err)
					}
					// A decimal point disables Minecraft's integer X/Z centering.
					args = append(args, strconv.FormatFloat(v, 'f', 1, 64))
				}
				for range 4 {
					v, err := r.Float()
					if err != nil {
						t.Fatal(err)
					}
					args = append(args, strconv.FormatFloat(float64(v), 'f', -1, 32))
				}
				count, err := r.Int()
				if err != nil {
					t.Fatal(err)
				}
				args = append(args, strconv.Itoa(int(count)), "force", "@s")
				s := New("unused")
				t.Cleanup(s.Stop)
				sess := menuProjectionSession(t, s, number, "ParticleCommand")
				feedback := s.runParticle("ParticleCommand", args)
				if len(sess.sendCh) != 1 {
					t.Fatalf("command enqueued %d packets: %+v", len(sess.sendCh), feedback)
				}
				packet := materializeOutboundPacket(t, sess, <-sess.sendCh)
				if packet.id != sess.ids().PlayLevelParticles || !bytes.Equal(packet.data, want) {
					t.Fatalf("command packet=%x\nwant=%x", packet.data, want)
				}
			})
		}
	}
}

func TestParticleCommandInvalidOptionsDoNotEmitPartialPacket(t *testing.T) {
	for _, token := range []string{
		`minecraft:block{block_state:{Name:"minecraft:not_a_block"}}`,
		`minecraft:dust{color:[1.0f,0.0f],scale:1.0f}`,
		`minecraft:item{item:{id:"minecraft:not_an_item",count:2}}`,
		`minecraft:item{item:{id:"minecraft:stone",count:2,components:{"minecraft:damage":7}}}`,
		`minecraft:smoke{unmeasured:1}`,
		`minecraft:vibration{destination:{type:"minecraft:entity",pos:[8,-60,8]},arrival_in_ticks:7}`,
	} {
		t.Run(token, func(t *testing.T) {
			s := New("unused")
			t.Cleanup(s.Stop)
			sess := menuProjectionSession(t, s, 776, "InvalidParticle")
			s.runParticle("InvalidParticle", []string{token, "0.0", "0.0", "0.0", "0", "0", "0", "0", "1", "force", "@s"})
			if len(sess.sendCh) != 0 {
				t.Fatal("invalid particle options emitted a partial packet")
			}
		})
	}
}
