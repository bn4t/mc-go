// Sound channel parity — vanilla splits sounds onto two packets by
// overload (javap of Entity.playSound, Player.playSound,
// LivingEntity.makeSound/die + a full-jar caller sweep):
//   - Entity.playSound → level.playSound(null, x,y,z,…) → positional
//     clientbound sound packet (0x75), broadcast to everyone in range.
//   - Player.playSound overrides it: level.playSound(this, x,y,z,…)
//     — the player is the EXCEPT arg, so victims never receive their
//     own hurt/fall voice over the wire (the client plays it locally
//     on the damage event); bystanders do receive it.
//   - LivingEntity.die → broadcastEntityEvent(this, 3): the death
//     sound is client-side — no sound packet is emitted at all.
//   - Only the explicit level.playSound(except, entity,…) overload
//     anchors a sound on an entity (clientbound sound_entity, 0x74):
//     BREEZE_DEFLECT, firework launch + LEAD_BREAK on the leashed mob,
//     TRIDENT_THROW on the thrower, instrument/map/dye/shear items and
//     a handful of animal behaviors — the whole rest is positional.
//   - Breeze.playAmbientSound uses playLocalSound, a server-side
//     no-op (the client schedules it locally): no wire emit.
package blackbox

import (
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestEntitySoundChannelParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Snd", "Snd2"),
	}, nil)
	defer targets.cleanup()

	// Wire sound ids are registry id + 1.
	const (
		sndZombieHurt = 1881 + 1
		sndPlayerHurt = 1312 + 1
	)
	type sndHit struct{ id, eid int32 }
	type obs struct {
		entSounds []sndHit // 0x74 sound_entity
		posSounds []sndHit // 0x75 positional
	}
	perLeg := map[string]*obs{}          // victim conn
	perLegBystander := map[string]*obs{} // second conn next to the victim

	for _, tg := range []struct{ tag, addr string }{
		{"vanilla", targets.vanilla},
		{"mc-go", targets.mcgo},
	} {
		conn, pr, pw, _ := joinPlayEid(t, tg.addr, "Snd")
		conn2, pr2, pw2, _ := joinPlayEid(t, tg.addr, "Snd2")
		sink := newChatSinkTP(pr, pw)
		sink2 := newChatSinkTP(pr2, pw2)
		sendClientInformationVD(t, pw, 24)
		sendClientInformationVD(t, pw2, 24)
		sendCommand(t, pw, "tp @s 0 -59 0 0 0")
		sendCommand(t, pw, "tp Snd2 1 -59 0 0 0")
		dataGetLine(t, sink, pw, "@s", "Pos")

		sendCommand(t, pw, "fill -4 -61 0 4 -56 8 minecraft:air")
		sendCommand(t, pw, "kill @e[type=!minecraft:player,x=-4,y=-64,z=0,dx=8,dy=10,dz=8]")
		sendCommand(t, pw, "gamemode survival @s")
		sendCommand(t, pw, "gamemode survival Snd2")
		dataGetLine(t, sink, pw, "@s", "Pos")

		zType, _ := server.EntityTypeID("zombie")
		var zEid int32
		o, o2 := &obs{}, &obs{}
		collect := func(l string, oo *obs) {
			pref, rest, ok := strings.Cut(l, ":")
			if !ok {
				return
			}
			r := protocol.NewPacketReader([]byte(rest))
			switch pref {
			case "0x1": // add_entity
				id, e1 := r.VarInt()
				_, e2 := r.Read(16)
				typ, e3 := r.VarInt()
				if e1 != nil || e2 != nil || e3 != nil {
					return
				}
				if typ == zType && zEid == 0 {
					zEid = id
				}
			case "0x74": // sound_entity: id(+1), source, entityID, vol, pitch, seed
				s, e1 := r.VarInt()
				_, e2 := r.VarInt()
				eid, e3 := r.VarInt()
				if e1 != nil || e2 != nil || e3 != nil {
					return
				}
				oo.entSounds = append(oo.entSounds, sndHit{s, eid})
			case "0x75": // positional sound: id(+1), source, xyz, vol, pitch, seed
				s, e1 := r.VarInt()
				if e1 != nil {
					return
				}
				oo.posSounds = append(oo.posSounds, sndHit{s, 0})
			}
		}

		sendCommand(t, pw, "summon minecraft:zombie 0 -59 4 {NoAI:1b}")
		deadline := time.Now().Add(5 * time.Second)
		for zEid == 0 && time.Now().Before(deadline) {
			for _, l := range sink.take() {
				collect(l, o)
			}
			if zEid == 0 {
				time.Sleep(50 * time.Millisecond)
			}
		}
		if zEid == 0 {
			conn.Close()
			conn2.Close()
			t.Fatalf("%s: zombie spawn missing", tg.tag)
		}
		// Past the ~3s join invulnerability so /damage lands.
		time.Sleep(3 * time.Second)

		// Lanes: a mob's hurt voice broadcasts positionally to all in
		// range; the victim's own hurt voice self-excludes but still
		// reaches the bystander.
		sendCommand(t, pw, "damage @e[type=minecraft:zombie,limit=1] 5 minecraft:mob_attack by @s")
		time.Sleep(300 * time.Millisecond)
		sendCommand(t, pw, "damage @s 3 minecraft:mob_attack")
		time.Sleep(600 * time.Millisecond)
		for _, l := range sink.take() {
			collect(l, o)
		}
		for _, l := range sink2.take() {
			collect(l, o2)
		}
		perLeg[tg.tag], perLegBystander[tg.tag] = o, o2
		t.Logf("%s victim ent=%v pos=%v | bystander ent=%v pos=%v",
			tg.tag, o.entSounds, o.posSounds, o2.entSounds, o2.posSounds)
		conn.Close()
		conn2.Close()
	}

	v, m := perLeg["vanilla"], perLeg["mc-go"]
	v2, m2 := perLegBystander["vanilla"], perLegBystander["mc-go"]
	hasEnt := func(o *obs, id int32) bool {
		for _, s := range o.entSounds {
			if s.id == id {
				return true
			}
		}
		return false
	}
	hasPos := func(o *obs, id int32) bool {
		for _, s := range o.posSounds {
			if s.id == id {
				return true
			}
		}
		return false
	}

	// zombie.hurt: positional on both legs, for both listeners.
	for _, tc := range []struct {
		name string
		o    *obs
	}{
		{"victim", v}, {"bystander", v2},
	} {
		if !hasPos(tc.o, sndZombieHurt) {
			t.Errorf("vanilla %s: zombie.hurt missing on the positional channel (pos=%v)",
				tc.name, tc.o.posSounds)
		}
		if hasEnt(tc.o, sndZombieHurt) {
			t.Errorf("vanilla %s: zombie.hurt leaked onto sound_entity", tc.name)
		}
	}
	for _, tc := range []struct {
		name string
		o    *obs
	}{
		{"victim", m}, {"bystander", m2},
	} {
		if !hasPos(tc.o, sndZombieHurt) {
			t.Errorf("mc-go %s: zombie.hurt missing on the positional channel (pos=%v)",
				tc.name, tc.o.posSounds)
		}
		if hasEnt(tc.o, sndZombieHurt) {
			t.Errorf("mc-go %s: zombie.hurt leaked onto sound_entity", tc.name)
		}
	}

	// player.hurt: self-excluded on the victim conn, positional on the
	// bystander conn — same split on both legs.
	if hasPos(v, sndPlayerHurt) || hasEnt(v, sndPlayerHurt) {
		t.Errorf("vanilla: victim received own hurt voice (ent=%v pos=%v)", v.entSounds, v.posSounds)
	}
	if hasPos(m, sndPlayerHurt) || hasEnt(m, sndPlayerHurt) {
		t.Errorf("mc-go: victim received own hurt voice (ent=%v pos=%v)", m.entSounds, m.posSounds)
	}
	if !hasPos(v2, sndPlayerHurt) {
		t.Errorf("vanilla: bystander did not hear victim's hurt voice (pos=%v)", v2.posSounds)
	}
	if !hasPos(m2, sndPlayerHurt) {
		t.Errorf("mc-go: bystander did not hear victim's hurt voice (pos=%v)", m2.posSounds)
	}
}
