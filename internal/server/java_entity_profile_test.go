package server

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

func entityCodecFixture(t *testing.T, number int32, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/java-entity-wire-codecs.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err = json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	var fixtures map[string]string
	if err = json.Unmarshal(document[fmt.Sprint(number)], &fixtures); err != nil {
		t.Fatal(err)
	}
	text, ok := fixtures[name]
	if !ok {
		t.Fatalf("no measured %d/%s fixture", number, name)
	}
	body, err := hex.DecodeString(text)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func entityProfile(t *testing.T, number int32) *javaProfile {
	t.Helper()
	p, ok := javaProfileFor(number)
	if !ok {
		t.Fatalf("missing profile%d", number)
	}
	return p
}

func entityViewer(t *testing.T, s *Server, number int32, name string) *Session {
	t.Helper()
	profile := entityProfile(t, number)
	sess := &Session{id: s.nextID.Add(1), server: s, ver: profile.version, sendCh: make(chan sendPkt, 512), done: make(chan struct{})}
	sess.setIdentity(name, offlineUUID(name))
	sess.state.Store(StatePlay)
	sess.spawned.Store(true)
	sess.loaded.Store(true)
	sess.dim.Store(s.dims[dimOverworld])
	sess.initSurvival()
	sess.setPos(.5, -60, .5, 0, 0, true)
	entity := s.entities.Spawn(&Entity{Type: mustEntityType("player"), UUID: sess.getUUID(), Dim: dimOverworld, X: .5, Y: -60, Z: .5, AirSupply: 300, Living: true, Health: 20, MaxHealth: 20})
	sess.entityID.Store(entity.ID)
	s.sessions.Store(sess.id, sess)
	t.Cleanup(func() {
		s.sessions.Delete(sess.id)
		sess.closeQueue()
		for len(sess.sendCh) > 0 {
			sess.retireOutbound(<-sess.sendCh)
		}
	})
	return sess
}
func entityPackets(t *testing.T, sess *Session) []sendPkt {
	t.Helper()
	var out []sendPkt
	for len(sess.sendCh) > 0 {
		packet := <-sess.sendCh
		out = append(out, materializeOutboundPacket(t, sess, packet))
	}
	return out
}
func entityPacketOf(t *testing.T, packets []sendPkt, id int32) []byte {
	t.Helper()
	for _, packet := range packets {
		if packet.id == id {
			return packet.data
		}
	}
	t.Fatalf("packet%#x absent from%v", id, packets)
	return nil
}
func entityAttributeIDs(t *testing.T, body []byte) []int32 {
	t.Helper()
	r := protocol.NewPacketReader(body)
	_, err := r.VarInt()
	if err != nil {
		t.Fatal(err)
	}
	n, err := r.VarInt()
	if err != nil {
		t.Fatal(err)
	}
	var ids []int32
	for i := int32(0); i < n; i++ {
		id, err := r.VarInt()
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		if _, err = r.Double(); err != nil {
			t.Fatal(err)
		}
		mods, err := r.VarInt()
		if err != nil {
			t.Fatal(err)
		}
		for j := int32(0); j < mods; j++ {
			if _, err = r.String(); err != nil {
				t.Fatal(err)
			}
			if _, err = r.Double(); err != nil {
				t.Fatal(err)
			}
			if _, err = r.VarInt(); err != nil {
				t.Fatal(err)
			}
		}
	}
	if r.Remaining() != 0 {
		t.Fatal("attribute trailing bytes")
	}
	return ids
}

func TestJavaEntityMeasuredPacketCodecs(t *testing.T) {
	var uuid [16]byte
	uuid[15] = 1
	entity := &Entity{ID: 321, UUID: uuid, Type: mustEntityType("zombie"), X: 1.25, Y: 70, Z: -3.5, Pitch: 22.5, Yaw: 90, HeadYaw: 45, VX: 2000, VY: -4000, VZ: 9000, Data: 7}
	original := *entity
	for _, number := range []int32{775, 776} {
		t.Run(fmt.Sprint(number), func(t *testing.T) {
			p := entityProfile(t, number)
			body, err := p.encodeSpawn(entity)
			if err != nil {
				t.Fatal(err)
			}
			if want := entityCodecFixture(t, number, "spawn_zombie_nonzero_motion"); !bytes.Equal(body, want) {
				t.Fatalf("spawn\n got%x\nwant%x", body, want)
			}
			var attributes protocol.Buffer
			attributes.VarInt(321)
			attributes.VarInt(1)
			writeAttrEntry(&attributes, attrMovementSpeed, .125, attrModifier{"example:boost", .25, 2})
			body, err = p.projectAttributes776(attributes.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			if want := entityCodecFixture(t, number, "attributes_speed_modifier"); !bytes.Equal(body, want) {
				t.Fatalf("attributes got%x want%x", body, want)
			}
			for _, remove := range []bool{false, true} {
				key := "effect_speed"
				if remove {
					key = "remove_speed"
				}
				body, err = p.projectMobEffect776(entityCodecFixture(t, 776, key), remove)
				if err != nil {
					t.Fatal(err)
				}
				if want := entityCodecFixture(t, number, key); !bytes.Equal(body, want) {
					t.Fatalf("effect got%x want%x", body, want)
				}
			}
			body, err = p.projectMetadata776(mustEntityType("slime"), entityCodecFixture(t, 776, "slime_size_4"))
			if err != nil {
				t.Fatal(err)
			}
			if want := entityCodecFixture(t, number, "slime_size_4"); !bytes.Equal(body, want) {
				t.Fatalf("slime got%x want%x", body, want)
			}
		})
	}
	if !reflect.DeepEqual(*entity, original) {
		t.Fatal("projection mutated authoritative entity")
	}
}

func TestJavaEntityProfileMetadataValues(t *testing.T) {
	p := entityProfile(t, 775)
	for _, name := range []string{"profile_unresolved_name", "profile_unresolved_uuid", "profile_resolved", "profile_resolved_signed", "profile_skin_patch"} {
		t.Run(name, func(t *testing.T) {
			raw := entityCodecFixture(t, 776, name)
			r := protocol.NewPacketReader(raw)
			var out protocol.Buffer
			if err := p.projectMetadataValue776(r, &out, 41); err != nil {
				t.Fatal(err)
			}
			if r.Remaining() != 0 || !bytes.Equal(out.Bytes(), entityCodecFixture(t, 775, name)) {
				t.Fatalf("profile shape changed: remaining%d body%x", r.Remaining(), out.Bytes())
			}
			var full protocol.Buffer
			full.VarInt(321)
			full.Byte(17)
			full.VarInt(41)
			full.Raw(raw)
			full.Byte(255)
			if _, err := p.projectMetadata776(mustEntityType("mannequin"), full.Bytes()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestJavaEntitySlimeAccessorProjectionAndCanonicalWriter(t *testing.T) {
	var body protocol.Buffer
	body.VarInt(321)
	body.Byte(16)
	body.Byte(8)
	body.Bool(true)
	body.Byte(17)
	body.Byte(8)
	body.Bool(true)
	body.Byte(18)
	body.Byte(1)
	body.VarInt(4)
	body.Byte(255)
	for _, name := range []string{"slime", "magma_cube"} {
		projected, err := entityProfile(t, 775).projectMetadata776(mustEntityType(name), body.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(projected, entityCodecFixture(t, 775, "slime_size_4")) {
			t.Fatalf("%s lost size while omitting newer age accessors: %x", name, projected)
		}
		entity := &Entity{Type: mustEntityType(name), Living: true, AirSupply: 300, Health: 16, CubeSize: 4}
		if metadata := encodeLivingMetadata(entity, nil); !bytes.HasSuffix(metadata, []byte{18, 1, 4, 255}) {
			t.Fatalf("canonical776 %s size accessor wrong:%x", name, metadata)
		}
	}
	var onlyNew protocol.Buffer
	onlyNew.VarInt(321)
	onlyNew.Byte(16)
	onlyNew.Byte(8)
	onlyNew.Bool(true)
	onlyNew.Byte(255)
	if got, err := entityProfile(t, 775).projectMetadata776(mustEntityType("slime"), onlyNew.Bytes()); err != nil || len(got) != 0 {
		t.Fatalf("empty projected update=%x error%v", got, err)
	}
}

func TestJavaEntityAllAttributesAndNewerAttributeOmission(t *testing.T) {
	p := entityProfile(t, 775)
	domain := builtinDomain(776, "minecraft:attribute")
	var body protocol.Buffer
	body.VarInt(321)
	body.VarInt(int32(len(domain.names)))
	for id := range domain.names {
		writeAttrEntry(&body, int32(id), float64(id)+.25)
	}
	before := append([]byte(nil), body.Bytes()...)
	projected, err := p.projectAttributes776(body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	ids := entityAttributeIDs(t, projected)
	if len(ids) != 35 {
		t.Fatalf("775 received%d attribute entries,want35", len(ids))
	}
	seen := map[int32]bool{}
	for _, id := range ids {
		if id < 0 || id >= 35 || seen[id] {
			t.Fatalf("invalid mapped attribute%d", id)
		}
		seen[id] = true
	}
	if !bytes.Equal(before, body.Bytes()) {
		t.Fatal("attribute projection mutated canonical packet")
	}
	var corrupt protocol.Buffer
	corrupt.VarInt(321)
	corrupt.VarInt(1)
	writeAttrEntry(&corrupt, int32(len(domain.names)), 1)
	if _, err = p.projectAttributes776(corrupt.Bytes()); err == nil {
		t.Fatal("unknown canonical attribute was silently omitted")
	}
}

func TestJavaEntityMixedViewerPairingAndBroadcasts(t *testing.T) {
	s := simulationTestServer(t)
	old := entityViewer(t, s, 775, "Old")
	current := entityViewer(t, s, 776, "Current")
	entity := s.entities.Spawn(&Entity{Type: mustEntityType("zombie"), Dim: dimOverworld, X: .5, Y: -60, Z: .5, Living: true, AirSupply: 300, Health: 17, NoAI: true, AttrTouched: map[string]bool{"movement_speed": true}, AttrBase: map[string]float64{"movement_speed": .125}})
	old.setTracked(entity.ID, true)
	current.setTracked(entity.ID, true)
	s.simulationAction(func() {
		for _, viewer := range []*Session{old, current} {
			if err := s.sendEntityTo(viewer, entity); err != nil {
				t.Fatal(err)
			}
		}
	})
	for _, viewer := range []*Session{old, current} {
		packets := entityPackets(t, viewer)
		spawn := protocol.NewPacketReader(entityPacketOf(t, packets, viewer.ids().PlaySpawnEntity))
		_, _ = spawn.VarInt()
		_, _ = spawn.UUID()
		wire, err := spawn.VarInt()
		if err != nil {
			t.Fatal(err)
		}
		profile := entityProfile(t, viewer.ver.Protocol)
		want, err := profile.builtinRegistryID("minecraft:entity_type", "minecraft:zombie")
		if err != nil || wire != want {
			t.Fatalf("profile%d spawn type%d want%d error%v", viewer.ver.Protocol, wire, want, err)
		}
		attributes := entityAttributeIDs(t, entityPacketOf(t, packets, viewer.ids().PlayUpdateAttributes))
		speed, _ := profile.builtinRegistryID("minecraft:attribute", "minecraft:movement_speed")
		if !reflect.DeepEqual(attributes, []int32{speed}) {
			t.Fatalf("profile%d attrs%v", viewer.ver.Protocol, attributes)
		}
	}
	entity.AttrsDirty = map[string]bool{"movement_speed": true}
	s.simulationAction(func() { s.broadcastMobAttrs(entity); s.broadcastEntityMetadata(entity.ID, encodeLivingHealth(12)) })
	for _, viewer := range []*Session{old, current} {
		packets := entityPackets(t, viewer)
		_ = entityPacketOf(t, packets, viewer.ids().PlayEntityMetadata)
		attributes := entityAttributeIDs(t, entityPacketOf(t, packets, viewer.ids().PlayUpdateAttributes))
		speed, _ := entityProfile(t, viewer.ver.Protocol).builtinRegistryID("minecraft:attribute", "minecraft:movement_speed")
		if !reflect.DeepEqual(attributes, []int32{speed}) {
			t.Fatal("mob sync bypassed target attribute projection")
		}
	}
	if entity.Type != mustEntityType("zombie") || entity.AttrBase["movement_speed"] != .125 {
		t.Fatal("mixed viewers changed core state")
	}
}

func TestJavaEntityCanonicalPlayerClassificationIgnoresWireType(t *testing.T) {
	s := simulationTestServer(t)
	viewer := entityViewer(t, s, 775, "OldPlayer")
	// Deliberately put the old wire ID in the obsolete mixed-domain field.
	// Core classification and metadata must remain based on canonical156.
	viewer.ver.IDs.EntityTypePlayer = 155
	player := s.entities.Spawn(&Entity{Type: mustEntityType("player"), Dim: dimOverworld, AirSupply: 300, Health: 20})
	if err := s.sendEntityTo(viewer, player); err != nil {
		t.Fatal(err)
	}
	packets := entityPackets(t, viewer)
	metadata := entityPacketOf(t, packets, viewer.ids().PlayEntityMetadata)
	if !bytes.Contains(metadata, []byte{16, 0, 127, 255}) {
		t.Fatalf("player misclassified using viewer wire type:%x", metadata)
	}
}

func TestJavaEntityPlayerAttributeRefreshPaths(t *testing.T) {
	s := simulationTestServer(t)
	viewer := entityViewer(t, s, 775, "OldAttributes")
	profile := entityProfile(t, 775)
	for _, name := range []string{"fresh", "returning"} {
		t.Run(name, func(t *testing.T) {
			if err := s.sendUpdateAttributes(viewer); err != nil {
				t.Fatal(err)
			}
			packets := entityPackets(t, viewer)
			for _, packet := range packets {
				ids := entityAttributeIDs(t, packet.data)
				for _, id := range ids {
					if profile.builtinRegistryName("minecraft:attribute", id) == "" {
						t.Fatalf("unmapped initial attribute%d", id)
					}
				}
			}
		})
	}
	viewer.setMode(GameModeSurvival)
	s.simulationAction(func() { _ = s.runGamemode(viewer.getName(), []string{"creative"}) })
	_ = entityAttributeIDs(t, entityPacketOf(t, entityPackets(t, viewer), viewer.ids().PlayUpdateAttributes))
	entity, ok := s.entities.GetPtr(viewer.entityID.Load())
	if !ok {
		t.Fatal("player record absent")
	}
	entity.TicksFrozen = 140
	s.simulationAction(s.playerFluidTickStep)
	ids := entityAttributeIDs(t, entityPacketOf(t, entityPackets(t, viewer), viewer.ids().PlayUpdateAttributes))
	speed, _ := profile.builtinRegistryID("minecraft:attribute", "minecraft:movement_speed")
	if !reflect.DeepEqual(ids, []int32{speed}) {
		t.Fatal("frost refresh bypassed profile projection")
	}
}

func TestJavaEntityInvalidMetadataIsolatesViewerBeforeEnqueue(t *testing.T) {
	s := simulationTestServer(t)
	old := entityViewer(t, s, 775, "Reject")
	current := entityViewer(t, s, 776, "Keep")
	entity := s.entities.Spawn(&Entity{Type: mustEntityType("zombie"), Dim: dimOverworld, Living: true, AirSupply: 300, Health: 20})
	old.setTracked(entity.ID, true)
	current.setTracked(entity.ID, true)
	// A valid health entry precedes an invalid late accessor. Nothing from
	// the rejected packet may be admitted to the old viewer's FIFO.
	payload := []byte{9, 3, 0x41, 0x20, 0, 0, 200, 1, 7, 255}
	before := append([]byte(nil), payload...)
	s.simulationAction(func() { s.broadcastEntityMetadata(entity.ID, payload) })
	if !old.gone() || len(old.sendCh) != 0 {
		t.Fatal("invalid late entry partially reached old viewer")
	}
	if current.gone() {
		t.Fatal("old viewer projection failure poisoned current viewer")
	}
	body := entityPacketOf(t, entityPackets(t, current), current.ids().PlayEntityMetadata)
	r := protocol.NewPacketReader(body)
	_, _ = r.VarInt()
	rest, _ := r.Read(r.Remaining())
	if !bytes.Equal(rest, before) || !bytes.Equal(payload, before) {
		t.Fatal("projection mutated original/current-viewer packet")
	}
}

func TestJavaEntityNewTypeIsolatesOnlyMissingViewer(t *testing.T) {
	s := simulationTestServer(t)
	old := entityViewer(t, s, 775, "OldCube")
	current := entityViewer(t, s, 776, "NewCube")
	entity := s.entities.Spawn(&Entity{Type: mustEntityType("sulfur_cube"), Dim: dimOverworld})
	if err := s.sendEntityTo(old, entity); err == nil || !old.gone() || len(old.sendCh) != 0 {
		t.Fatal("new-only entity was silently remapped/partially admitted")
	}
	if err := s.sendEntityTo(current, entity); err != nil || current.gone() {
		t.Fatalf("missing old mapping affected current viewer:%v", err)
	}
	_ = entityPacketOf(t, entityPackets(t, current), current.ids().PlaySpawnEntity)
	if entity.Type != mustEntityType("sulfur_cube") {
		t.Fatal("projection rewrote core type")
	}
}

func TestJavaEntityBlockStatesProjectForFallingAndMetadata(t *testing.T) {
	p := entityProfile(t, 775)
	mapping, _ := world.BlockMapping(775)
	var state world.Block
	var want int32
	for id := world.Block(1); int(id) < world.BlockCatalog().Len(); id++ {
		if wire, ok := mapping.Lookup(id); ok && wire != int32(id) {
			state, want = id, wire
			break
		}
	}
	if state == 0 {
		t.Fatal("no measured renumbered state")
	}
	falling := &Entity{ID: 321, Type: mustEntityType("falling_block"), Data: int32(state)}
	body, err := p.encodeSpawn(falling)
	if err != nil {
		t.Fatal(err)
	}
	r := protocol.NewPacketReader(body)
	_, _ = r.VarInt()
	_, _ = r.UUID()
	_, _ = r.VarInt()
	_, _ = r.Read(24)
	_, _, _, _ = r.ReadLpVec3()
	_, _ = r.Read(3)
	data, err := r.VarInt()
	if err != nil || data != want {
		t.Fatalf("falling data%d want%d error%v", data, want, err)
	}
	var metadata protocol.Buffer
	metadata.VarInt(321)
	metadata.Byte(16)
	metadata.Byte(15)
	metadata.VarInt(int32(state))
	metadata.Byte(255)
	body, err = p.projectMetadata776(mustEntityType("enderman"), metadata.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	r = protocol.NewPacketReader(body)
	_, _ = r.VarInt()
	_, _ = r.Byte()
	_, _ = r.VarInt()
	data, err = r.VarInt()
	if err != nil || data != want {
		t.Fatalf("carried block%d want%d error%v", data, want, err)
	}
	if falling.Data != int32(state) {
		t.Fatal("falling state mutated")
	}
}

func TestJavaEntityNestedMetadataCodecs(t *testing.T) {
	p := entityProfile(t, 775)
	var name protocol.Buffer
	name.VarInt(321)
	name.Byte(2)
	name.Byte(6)
	name.Bool(true)
	if err := nbt.WriteUnnamed(&name, "Compat"); err != nil {
		t.Fatal(err)
	}
	name.Byte(9)
	name.Byte(3)
	name.Float(17)
	name.Byte(255)
	if got, err := p.projectMetadata776(mustEntityType("zombie"), name.Bytes()); err != nil || !bytes.Equal(got, name.Bytes()) {
		t.Fatalf("component boundary: %x %v", got, err)
	}
	items := builtinDomain(776, "minecraft:item")
	var item, itemWire int32
	for id := range items.names {
		if wire, err := p.projectBuiltinRegistryID("minecraft:item", int32(id)); err == nil && wire != int32(id) {
			item, itemWire = int32(id), wire
			break
		}
	}
	if item == 0 {
		t.Fatal("no renumbered common item")
	}
	var itemMetadata protocol.Buffer
	itemMetadata.VarInt(321)
	itemMetadata.Raw(encodeItemMetadata(ItemStack{ID: item, Count: 1}))
	body, err := p.projectMetadata776(mustEntityType("item"), itemMetadata.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	r := protocol.NewPacketReader(body)
	_, _ = r.VarInt()
	_, _ = r.Byte()
	_, _ = r.VarInt()
	count, _ := r.VarInt()
	wire, _ := r.VarInt()
	if count != 1 || wire != itemWire {
		t.Fatalf("item metadata used canonical%d: count%d wire%d want%d", item, count, wire, itemWire)
	}
	canonicalParticle, _ := entityProfile(t, 776).builtinRegistryID("minecraft:particle_type", "minecraft:entity_effect")
	particleWire, _ := p.builtinRegistryID("minecraft:particle_type", "minecraft:entity_effect")
	var particles protocol.Buffer
	particles.VarInt(321)
	particles.Byte(10)
	particles.Byte(17)
	particles.VarInt(1)
	particles.VarInt(canonicalParticle)
	particles.Int(-16711936)
	particles.Byte(255)
	body, err = p.projectMetadata776(mustEntityType("pig"), particles.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	r = protocol.NewPacketReader(body)
	_, _ = r.VarInt()
	_, _ = r.Byte()
	_, _ = r.VarInt()
	_, _ = r.VarInt()
	wire, _ = r.VarInt()
	color, _ := r.Int()
	end, _ := r.Byte()
	if wire != particleWire || color != -16711936 || end != 255 || r.Remaining() != 0 {
		t.Fatal("nested particle ID or measured color-only shape changed")
	}
	found := false
	for id, wire := range p.config.toWire["minecraft:painting_variant"] {
		if wire < 0 {
			continue
		}
		var painting protocol.Buffer
		painting.VarInt(321)
		painting.Byte(9)
		painting.Byte(34)
		painting.VarInt(int32(id) + 1)
		painting.Byte(255)
		body, err = p.projectMetadata776(mustEntityType("painting"), painting.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		r = protocol.NewPacketReader(body)
		_, _ = r.VarInt()
		_, _ = r.Byte()
		_, _ = r.VarInt()
		projected, _ := r.VarInt()
		if projected != wire+1 {
			t.Fatal("synced metadata holder bypassed its target registry")
		}
		found = true
		break
	}
	if !found {
		t.Fatal("fixture found no common measured painting holder")
	}
}

func TestJavaEntityEffectFanoutUsesViewerProfiles(t *testing.T) {
	s := simulationTestServer(t)
	old := entityViewer(t, s, 775, "OldEffect")
	current := entityViewer(t, s, 776, "NewEffect")
	// Establish an active effect: an update packet alone has no state to remove.
	s.simulationAction(func() { s.applyMobEffectWire(old, effectSpeed, 2, 200, false, 14) })
	for _, viewer := range []*Session{old, current} {
		body := entityPacketOf(t, entityPackets(t, viewer), viewer.ids().PlayUpdateMobEffect)
		r := protocol.NewPacketReader(body)
		eid, _ := r.VarInt()
		effect, _ := r.VarInt()
		amp, _ := r.VarInt()
		duration, _ := r.VarInt()
		flags, _ := r.Byte()
		want, _ := entityProfile(t, viewer.ver.Protocol).builtinRegistryID("minecraft:mob_effect", "minecraft:speed")
		if eid != old.entityID.Load() || effect != want || amp != 2 || duration != 200 || flags != 14 || r.Remaining() != 0 {
			t.Fatal("effect fanout changed source parameters or target identity")
		}
	}
	s.simulationAction(func() { s.removeMobEffect(old, effectSpeed) })
	for _, viewer := range []*Session{old, current} {
		_ = entityPacketOf(t, entityPackets(t, viewer), viewer.ids().PlayRemoveMobEffect)
		if viewer.gone() {
			t.Fatal("normal effect/removal closed a viewer")
		}
	}
}

func TestJavaEntityMalformedMetadataValuesReject(t *testing.T) {
	p := entityProfile(t, 775)
	for _, data := range [][]byte{{2}, {0, 2}, {1}, {1, 0, 0, 0}} {
		var out protocol.Buffer
		if err := p.projectMetadataValue776(protocol.NewPacketReader(data), &out, 41); err == nil {
			t.Fatalf("malformed profile accepted: %x", data)
		}
	}
	var bad protocol.Buffer
	bad.VarInt(321)
	bad.Byte(10)
	bad.Byte(17)
	bad.VarInt(1025)
	bad.Byte(255)
	if _, err := p.projectMetadata776(mustEntityType("pig"), bad.Bytes()); err == nil {
		t.Fatal("unbounded metadata particle list accepted")
	}
}

// Creeper DATA_SWELL_DIR is accessor 16 (int); 18 is the DATA_IS_IGNITED
// boolean, and a real client disconnects on an int there.
func TestJavaEntityCreeperMetadataMatchesSchema(t *testing.T) {
	p := entityProfile(t, 775)
	creeper := mustEntityType("creeper")
	check := func(label string, items []byte) {
		t.Helper()
		var body protocol.Buffer
		body.VarInt(321)
		body.Raw(items)
		if _, err := p.projectMetadata776(creeper, body.Bytes()); err != nil {
			t.Fatalf("%s: %v (%x)", label, err, items)
		}
		if !bytes.Contains(items, []byte{16, 1}) {
			t.Fatalf("%s: swell_dir not at accessor 16: %x", label, items)
		}
	}
	e := &Entity{Type: creeper, Living: true, AirSupply: 300, Health: 20, SwellDir: 1, Powered: true}
	check("spawn", encodeLivingMetadata(e, nil))
	// Establish the first post-pair dirty replay before measuring a reset.
	check("first dirty", encodeLivingMetadataDirty(e, nil))
	e.SwellDir = -1
	check("dirty", encodeLivingMetadataDirty(e, nil))
}
