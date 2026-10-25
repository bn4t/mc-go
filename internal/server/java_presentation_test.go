package server

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"mc-go/api/registry"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

func wireDomainFacts(t *testing.T, version int) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(fmt.Sprintf("testdata/java-wire-domains/protocol-%d.json", version))
	if err != nil {
		t.Fatal(err)
	}
	var facts map[string]json.RawMessage
	if err := json.Unmarshal(data, &facts); err != nil {
		t.Fatal(err)
	}
	return facts
}

func factBytes(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	data, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func factPackets(raw json.RawMessage) []string {
	var packets []string
	if json.Unmarshal(raw, &packets) == nil {
		return packets
	}
	var probe struct {
		Packets []string `json:"packets"`
	}
	_ = json.Unmarshal(raw, &probe)
	return probe.Packets
}

// Fixtures are paired live vanilla observations; their manifests record the
// exact oracle and official-report checksums, source class and tool version.
func TestJavaPresentationMeasuredParticleOptions(t *testing.T) {
	old, current := wireDomainFacts(t, 775), wireDomainFacts(t, 776)
	p, _ := javaProfileFor(775)
	count := 0
	for key, raw := range current {
		if !strings.HasPrefix(key, "particle_") && !strings.HasPrefix(key, "option_probe_") {
			continue
		}
		sources, targets := factPackets(raw), factPackets(old[key])
		if len(sources) == 0 {
			continue
		}
		t.Run(key, func(t *testing.T) {
			source, err := hex.DecodeString(sources[0])
			if err != nil {
				t.Fatal(err)
			}
			before := bytes.Clone(source)
			got, err := p.projectLevelParticles776(source)
			if len(targets) == 0 {
				if !errors.Is(err, ErrJavaUnsupportedCosmetic) || got != nil {
					t.Fatalf("absent particle: %x, %v", got, err)
				}
				return
			}
			target, err2 := hex.DecodeString(targets[0])
			if err2 != nil {
				t.Fatal(err2)
			}
			// Position/count are event inputs, not registry data. Keep the
			// source values while independently pinning the complete options.
			want := append(bytes.Clone(source[:46]), target[46:]...)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("projected=%x, %v; want %x", got, err, want)
			}
			if !bytes.Equal(source, before) {
				t.Fatal("projection mutated the shared source body")
			}
		})
		count++
	}
	if count < 110 {
		t.Fatalf("only %d measured particle cases", count)
	}
}

func TestJavaPresentationMeasuredExplosionAndDirectSound(t *testing.T) {
	old, current := wireDomainFacts(t, 775), wireDomainFacts(t, 776)
	p, _ := javaProfileFor(775)
	source, target := factBytes(t, current["explosion"]), factBytes(t, old["explosion"])
	got, err := p.projectExplosion776(source)
	// Both observed events include knockback; preserve the entire physical
	// event, and compare the independent particle/sound/weighted-list tail.
	want := append(bytes.Clone(source[:57]), target[57:]...)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("explosion=%x, %v; want %x", got, err, want)
	}
	for _, raw := range factPackets(current["sound_direct"]) {
		data, _ := hex.DecodeString(raw)
		got, err := p.projectSound776(data, false)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("direct holder changed event/seed: %x, %v", got, err)
		}
		r := protocol.NewPacketReader(got)
		holder, _ := r.VarInt()
		name, _ := r.String()
		flag, _ := r.Byte()
		category, _ := r.VarInt()
		if holder != 0 || name != "minecraft:ui.button.click" || flag != 0 || category != 7 {
			t.Fatalf("direct holder ordering: %d %q %d %d", holder, name, flag, category)
		}
	}
}

func presentationSession(t *testing.T, proto int32) *Session {
	t.Helper()
	p, ok := javaProfileFor(proto)
	if !ok {
		t.Fatal("missing profile")
	}
	s := &Session{profile: p, ver: p.version, done: make(chan struct{})}
	s.initOutbound()
	s.state.Store(StatePlay)
	t.Cleanup(s.closeQueue)
	return s
}

func TestJavaPresentationMalformedDomainsRetireOnlyRecipient(t *testing.T) {
	ids := LatestVersion().IDs
	for _, id := range []int32{ids.PlaySound, ids.PlaySoundEntity, ids.PlayLevelParticles, ids.PlayExplode, ids.PlayDamageEvent, ids.PlayBlockEntityData, ids.PlaySetEquipment, ids.PlayBlockEvent} {
		t.Run(fmt.Sprint(id), func(t *testing.T) {
			old, healthy := presentationSession(t, 775), presentationSession(t, 776)
			if err := old.write(id, nil); err == nil || !old.gone() {
				t.Fatalf("malformed mandatory body: %v, gone=%v", err, old.gone())
			}
			if len(old.sendCh) != 0 {
				t.Fatal("partially projected packet was enqueued")
			}
			if err := healthy.write(ids.PlayUpdateTime, []byte{1}); err != nil || healthy.gone() {
				t.Fatalf("other recipient affected: %v", err)
			}
		})
	}
}

func TestJavaPresentationUnsupportedCosmeticsAndMandatoryCarriers(t *testing.T) {
	old := presentationSession(t, 775)
	base, _ := javaProfileFor(776)
	id, _ := base.builtinRegistryID("minecraft:particle_type", "minecraft:geyser")
	var particle protocol.Buffer
	particle.Raw(make([]byte, 46))
	particle.VarInt(id)
	if err := old.write(old.ids().PlayLevelParticles, particle.Bytes()); !errors.Is(err, ErrJavaUnsupportedCosmetic) || old.gone() || len(old.sendCh) != 0 {
		t.Fatalf("optional omission: %v, gone=%v", err, old.gone())
	}
	var explode protocol.Buffer
	explode.Raw(make([]byte, 32))
	explode.Bool(false)
	explode.VarInt(id)
	if err := old.write(old.ids().PlayExplode, explode.Bytes()); err == nil || errors.Is(err, ErrJavaUnsupportedCosmetic) || !old.gone() {
		t.Fatalf("mandatory explosion carrier: %v", err)
	}
}

func TestJavaPresentationRegistryDomains(t *testing.T) {
	p, _ := javaProfileFor(775)
	current, _ := javaProfileFor(776)
	for _, entity := range []bool{false, true} {
		var b protocol.Buffer
		b.VarInt(700)
		b.VarInt(7)
		if entity {
			b.VarInt(123)
		} else {
			b.Raw(make([]byte, 12))
		}
		b.Float(1)
		b.Float(.5)
		b.Long(0x12345678)
		got, err := p.projectSound776(b.Bytes(), entity)
		if err != nil {
			t.Fatal(err)
		}
		r := protocol.NewPacketReader(got)
		holder, _ := r.VarInt()
		if holder != 698 || !bytes.Equal(got[len(got)-8:], b.Bytes()[b.Len()-8:]) {
			t.Fatal("numeric sound or shared seed changed")
		}
	}
	var be protocol.Buffer
	be.Raw(make([]byte, 8))
	be.VarInt(26)
	be.Raw([]byte{10, 0})
	got, err := p.projectBlockEntity776(be.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	r := protocol.NewPacketReader(got)
	_, _ = r.Read(8)
	typ, _ := r.VarInt()
	if typ != 27 {
		t.Fatalf("barrel type=%d", typ)
	}
	var event protocol.Buffer
	event.Raw(make([]byte, 10))
	canonical, _ := current.builtinRegistryID("minecraft:block", "minecraft:piston")
	event.VarInt(canonical)
	got, err = p.projectBlockEvent776(event.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	r = protocol.NewPacketReader(got)
	_, _ = r.Read(10)
	typ, _ = r.VarInt()
	want, _ := p.builtinRegistryID("minecraft:block", "minecraft:piston")
	if typ != want {
		t.Fatalf("block event=%d want%d", typ, want)
	}
	var damage protocol.Buffer
	damage.VarInt(123)
	damage.VarInt(current.registryEntryID("minecraft:damage_type", "minecraft:generic"))
	damage.VarInt(0)
	damage.VarInt(0)
	damage.Bool(false)
	got, err = p.projectDamage776(damage.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	r = protocol.NewPacketReader(got)
	_, _ = r.VarInt()
	typ, _ = r.VarInt()
	if typ != p.registryEntryID("minecraft:damage_type", "minecraft:generic") {
		t.Fatalf("damage=%d", typ)
	}
}

func TestJavaPresentationSourceEventSamplesSeedOnceAcrossProfiles(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	old, _ := enforceSession(t, s, "OldSound")
	current, _ := enforceSession(t, s, "NewSound")
	p, _ := javaProfileFor(775)
	old.profile = p
	old.ver = p.version
	d := s.dims[dimOverworld]
	old.dim.Store(d)
	current.dim.Store(d)
	s.playSeededSoundAt(d, 8, -60, 8, "minecraft:ui.button.click", 7, 1, .5)
	if len(old.sendCh) != 1 || len(current.sendCh) != 1 {
		t.Fatal("sound did not reach both profiles")
	}
	a, b := <-old.sendCh, <-current.sendCh
	if !bytes.Equal(a.data, b.data) {
		t.Fatal("one source sound event resampled seed/pitch across profiles")
	}
}

func TestJavaPresentationChargesProjectedBytesAndPreservesSeed(t *testing.T) {
	s := presentationSession(t, 775)
	var sourceID, targetID int32 = -1, -1
	for i, target := range builtinDomain(775, "minecraft:sound_event").from776 {
		if target >= 0 && protocol.VarIntLen(int32(i)+1) > protocol.VarIntLen(target+1) {
			sourceID, targetID = int32(i), target
			break
		}
	}
	if sourceID < 0 {
		// Sound ID varints happen to retain their width across these
		// reports. A custom valid block projection exercises length change
		// using the same queue boundary with a block-particle body.
		catalog := world.BlockCatalog()
		air, _ := catalog.State(world.Air)
		state := world.Block(catalog.Len() - 1)
		key, _ := catalog.State(state)
		mapping, err := registry.NewBlockMapping(775, catalog, []registry.BlockWireEntry{{State: air, WireID: 0}, {State: key, WireID: 1}}, registry.MissingBlockPolicy{})
		if err != nil {
			t.Fatal(err)
		}
		profile := *s.profile
		profile.blocks = mapping
		s.profile = &profile
		base, _ := javaProfileFor(776)
		canonical, err := base.blocks.Encode(state)
		if err != nil {
			t.Fatal(err)
		}
		var b protocol.Buffer
		b.Raw(make([]byte, 46))
		b.VarInt(1)
		b.VarInt(canonical)
		data := bytes.Clone(b.Bytes())
		want, err := profile.projectLevelParticles776(data)
		if err != nil {
			t.Fatal(err)
		}
		if len(want) >= len(data) {
			t.Fatal("block projection did not reduce length")
		}
		s.outByteLimit = int64(len(want) + protocol.VarIntLen(s.ids().PlayLevelParticles))
		if err := s.write(s.ids().PlayLevelParticles, data); err != nil {
			t.Fatal(err)
		}
		pkt := <-s.sendCh
		if !bytes.Equal(pkt.data, want) || !bytes.Equal(data, b.Bytes()) {
			t.Fatal("projected body or source changed")
		}
		if _, size := outboundPending(s); size != s.outByteLimit {
			t.Fatalf("charged %d want%d", size, s.outByteLimit)
		}
		s.retireOutbound(pkt)
		return
	}
	var b protocol.Buffer
	b.VarInt(sourceID + 1)
	b.VarInt(7)
	b.Raw(make([]byte, 12))
	b.Float(1)
	b.Float(.5)
	b.Long(0x12345678)
	source := bytes.Clone(b.Bytes())
	want, err := s.profile.projectSound776(source, false)
	if err != nil {
		t.Fatal(err)
	}
	s.outByteLimit = int64(len(want) + protocol.VarIntLen(s.ids().PlaySound))
	if err := s.write(s.ids().PlaySound, source); err != nil {
		t.Fatal(err)
	}
	queued := <-s.sendCh
	r := protocol.NewPacketReader(queued.data)
	holder, _ := r.VarInt()
	if holder != targetID+1 || !bytes.Equal(queued.data[len(queued.data)-8:], source[len(source)-8:]) || !bytes.Equal(source, b.Bytes()) {
		t.Fatal("holder, sampled seed, or shared source changed")
	}
	if _, size := outboundPending(s); size != s.outByteLimit {
		t.Fatalf("charged %d, want %d", size, s.outByteLimit)
	}
	s.retireOutbound(queued)
}
