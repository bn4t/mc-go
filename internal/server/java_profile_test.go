package server

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"testing"

	"mc-go/api/registry"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// These bodies are actual independent TCP observations of the official 26.1
// and 26.2 JVM servers, not expectations produced by the codec under test.
// The shared corpus includes input checksums and measurement provenance.
func TestJavaProfileCodecsMatchOfficialWire(t *testing.T) {
	raw, err := os.ReadFile("../../conformance/testdata/java-profile-observations.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Profiles []struct {
			Protocol int32  `json:"protocol"`
			Username string `json:"username"`
			Login    string `json:"login_success_hex"`
			Join     string `json:"play_login_hex"`
			Fields   struct {
				Entity           int32    `json:"entity"`
				Hardcore         int      `json:"hardcore"`
				Dimensions       []string `json:"dimensions"`
				MaxPlayers       int32    `json:"max_players"`
				View             int32    `json:"view"`
				Simulation       int32    `json:"simulation"`
				ReducedDebug     int      `json:"reduced_debug"`
				RespawnScreen    int      `json:"respawn_screen"`
				LimitedCrafting  int      `json:"limited_crafting"`
				DimensionType    int32    `json:"dimension_type"`
				Dimension        string   `json:"dimension_name"`
				Seed             string   `json:"seed"`
				Gamemode         byte     `json:"gamemode"`
				PreviousGamemode byte     `json:"previous_gamemode"`
				Debug            int      `json:"debug"`
				Flat             int      `json:"flat"`
				Death            int      `json:"death"`
				PortalCooldown   int32    `json:"portal_cooldown"`
				SeaLevel         int64    `json:"sea_level"`
				OnlineMode       int      `json:"online_mode"`
				SecureChat       int      `json:"secure_chat"`
			} `json:"decoded_join"`
		} `json:"profiles"`
	}
	if err = json.Unmarshal(raw, &corpus); err != nil {
		t.Fatal(err)
	}
	if len(corpus.Profiles) != 2 {
		t.Fatalf("oracle profiles=%d", len(corpus.Profiles))
	}
	for _, oracle := range corpus.Profiles {
		t.Run(fmt.Sprint(oracle.Protocol), func(t *testing.T) {
			profile, ok := javaProfileFor(oracle.Protocol)
			if !ok {
				t.Fatal("missing measured profile")
			}
			login, err := hex.DecodeString(oracle.Login)
			if err != nil {
				t.Fatal(err)
			}
			r := protocol.NewPacketReader(login)
			uuid, err := r.UUID()
			if err != nil {
				t.Fatal(err)
			}
			name, err := r.String()
			if err != nil {
				t.Fatal(err)
			}
			properties, err := r.VarInt()
			if err != nil || properties != 0 {
				t.Fatalf("offline properties=%d,%v", properties, err)
			}
			if name != oracle.Username {
				t.Fatalf("wire name=%q", name)
			}
			input := javaLoginSuccess{UUID: uuid, Name: name}
			if oracle.Protocol == 776 {
				input.SessionID, err = r.UUID()
				if err != nil {
					t.Fatal(err)
				}
			}
			if r.Remaining() != 0 {
				t.Fatalf("unexpected login tail=%d", r.Remaining())
			}
			if got := profile.encodeLoginSuccess(input); !bytes.Equal(got, login) {
				t.Fatalf("login codec differs\ngot %x\nwant%x", got, login)
			}
			f := oracle.Fields
			seed, err := strconv.ParseUint(f.Seed, 16, 64)
			if err != nil {
				t.Fatal(err)
			}
			if f.Death != 0 {
				t.Fatal("fixture unexpectedly has optional death location")
			}
			inputJoin := javaJoinGame{EntityID: f.Entity, Hardcore: f.Hardcore != 0, Dimensions: f.Dimensions,
				MaxPlayers: f.MaxPlayers, ViewDistance: f.View, SimulationDistance: f.Simulation,
				ReducedDebug: f.ReducedDebug != 0, RespawnScreen: f.RespawnScreen != 0, LimitedCrafting: f.LimitedCrafting != 0,
				DimensionType: f.DimensionType, Dimension: f.Dimension, HashedSeed: int64(seed), GameMode: f.Gamemode,
				PreviousGameMode: f.PreviousGamemode, Debug: f.Debug != 0, Flat: f.Flat != 0,
				PortalCooldown: f.PortalCooldown, SeaLevel: int32(f.SeaLevel), OnlineMode: f.OnlineMode != 0, SecureChat: f.SecureChat != 0,
			}
			join, err := hex.DecodeString(oracle.Join)
			if err != nil {
				t.Fatal(err)
			}
			if got := profile.encodeJoinGame(inputJoin); !bytes.Equal(got, join) {
				t.Fatalf("join codec differs\ngot %x\nwant%x", got, join)
			}
		})
	}
}

func TestJavaProfileCandidateCannotSelectAdmissionOrDispatch(t *testing.T) {
	candidate, ok := javaProfileFor(775)
	if !ok {
		t.Fatal("missing candidate")
	}
	if !errors.Is(candidate.admissionError(), ErrIncompleteJavaProfile) {
		t.Fatal("candidate accepted")
	}
	if _, ok := NegotiateVersion(775); ok {
		t.Fatal("candidate negotiated")
	}
	if _, ok := candidate.playHandler(IDPlayMovePosSB); ok {
		t.Fatal("unverified candidate gameplay dispatch enabled")
	}
	current, _ := javaProfileFor(ProtocolVersion)
	if _, ok := current.playHandler(IDPlayMovePosSB); !ok {
		t.Fatal("current profile lost movement route")
	}
	statuses := JavaProfiles()
	if len(statuses) != 2 || statuses[0].Admission || !statuses[1].Admission {
		t.Fatalf("profile gates=%+v", statuses)
	}
	statuses[0].Missing[0] = "mutated"
	if JavaProfiles()[0].Missing[0] == "mutated" {
		t.Fatal("status aliases profile state")
	}
}

func TestJavaCandidate775RejectedOverWire(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	c, reader, writer := pipePair(t, s)
	defer c.Close()
	if err := sendHandshake(writer, 775, "localhost", 25565, NextLogin); err != nil {
		t.Fatal(err)
	}
	var b protocol.Buffer
	b.String("Candidate")
	b.Raw(make([]byte, 16))
	if err := writer.WritePacket(IDLoginStart, b.Bytes()); err != nil {
		t.Fatal(err)
	}
	packet, err := reader.ReadPacket()
	if err != nil || packet.ID != IDLoginDisconnect {
		t.Fatalf("candidate response=%#x,%v", packet.ID, err)
	}
}

func TestJavaBlockBroadcastProjectsEachViewerWithoutChangingWorld(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	catalog := world.BlockCatalog()
	stone, _ := catalog.State(registry.BlockStateID(world.Stone))
	dirt, _ := catalog.State(registry.BlockStateID(world.Dirt))
	mapping, err := registry.NewBlockMapping(999, catalog, []registry.BlockWireEntry{{State: stone, WireID: 701}, {State: dirt, WireID: 91}}, registry.MissingBlockPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	normal, _ := enforceSession(t, s, "Normal")
	projected, _ := enforceSession(t, s, "Projected")
	normal.queued = map[[2]int]bool{{0, 0}: true}
	projected.queued = map[[2]int]bool{{0, 0}: true}
	projected.profile = &javaProfile{version: LatestVersion(), blocks: mapping}
	pos := world.Pos{X: 2, Y: 70, Z: 3}
	s.world.SetBlock(pos, world.Stone)
	s.broadcastBlockState(s.dims[dimOverworld], pos, world.Stone)
	for _, tc := range []struct {
		session *Session
		want    int32
	}{{normal, int32(world.Stone)}, {projected, 701}} {
		packet := <-tc.session.sendCh
		packet = materializeOutboundPacket(t, tc.session, packet)
		r := protocol.NewPacketReader(packet.data)
		_, _, _, _ = r.Position()
		state, err := r.VarInt()
		if err != nil || state != tc.want {
			t.Fatalf("projected single=%d,%v want%d", state, err, tc.want)
		}
	}
	s.ticking.Store(true)
	defer s.ticking.Store(false)
	other := world.Pos{X: 3, Y: 70, Z: 3}
	s.world.SetBlock(other, world.Dirt)
	s.broadcastBlockState(s.dims[dimOverworld], pos, world.Stone)
	s.broadcastBlockState(s.dims[dimOverworld], other, world.Dirt)
	s.blockChangesFlushStep()
	for _, tc := range []struct {
		session *Session
		want    [2]int32
	}{{normal, [2]int32{int32(world.Stone), int32(world.Dirt)}}, {projected, [2]int32{701, 91}}} {
		packet := <-tc.session.sendCh
		packet = materializeOutboundPacket(t, tc.session, packet)
		if packet.id != LatestVersion().IDs.PlaySectionBlocksUpdate {
			t.Fatalf("section packet=%#x", packet.id)
		}
		r := protocol.NewPacketReader(packet.data)
		_, _ = r.Long()
		count, err := r.VarInt()
		if err != nil || count != 2 {
			t.Fatalf("section count=%d,%v", count, err)
		}
		for _, want := range tc.want {
			value, err := r.VarLong()
			if err != nil || int32(uint64(value)>>12) != want {
				t.Fatalf("section projection=%d,%v want%d", uint64(value)>>12, err, want)
			}
		}
	}
	if got := s.world.GetBlock(pos); got != world.Stone {
		t.Fatal("projection changed canonical stone")
	}
	if got := s.world.GetBlock(other); got != world.Dirt {
		t.Fatal("projection changed canonical dirt")
	}
	// A profile without an exact mapping retires only its own viewer. It
	// must not send an arbitrary numeric state or affect another profile.
	third := world.Pos{X: 4, Y: 70, Z: 3}
	calcite := world.BlockStateMust("calcite")
	s.world.SetBlock(third, calcite)
	s.broadcastBlockState(s.dims[dimOverworld], third, calcite)
	s.blockChangesFlushStep()
	select {
	case <-projected.done:
	default:
		t.Fatal("missing projection did not retire viewer")
	}
	select {
	case <-normal.done:
		t.Fatal("missing projection retired compatible viewer")
	default:
	}
	packet := <-normal.sendCh
	packet = materializeOutboundPacket(t, normal, packet)
	r := protocol.NewPacketReader(packet.data)
	_, _, _, _ = r.Position()
	state, err := r.VarInt()
	if err != nil || state != int32(calcite) {
		t.Fatalf("compatible viewer lost state=%d,%v", state, err)
	}
	if s.world.GetBlock(third) != calcite {
		t.Fatal("rejected projection changed authoritative block")
	}
}

func TestJava775StrictBlockProjectionRejectsNewStates(t *testing.T) {
	p, _ := javaProfileFor(775)
	missing := p.blocks.Missing()
	if len(missing) == 0 {
		t.Fatal("candidate mapping unexpectedly complete")
	}
	if _, err := p.encodeBlockUpdate(0, 70, 0, world.Block(missing[0])); !errors.Is(err, registry.ErrMissingBlockState) {
		t.Fatalf("missing mapping error=%v", err)
	}
	if _, err := p.encodeSectionBlocks(0, 4, 0, []javaSectionBlock{{State: world.Block(missing[0])}}); !errors.Is(err, registry.ErrMissingBlockState) {
		t.Fatalf("missing section mapping error=%v", err)
	}
}

func TestJavaProfileRejectsForeignCatalogAndProtocol(t *testing.T) {
	air, _ := registry.NewStateKey("minecraft:air", nil)
	catalog, err := registry.NewBlockCatalog([]registry.BlockDefinition{{State: air, Default: true}})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := registry.NewBlockMapping(776, catalog, []registry.BlockWireEntry{{State: air, WireID: 0}}, registry.MissingBlockPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = newJavaProfile(LatestVersion(), foreign); err == nil {
		t.Fatal("profile accepted same numeric IDs from a foreign catalog")
	}
	wrongProtocol, _ := world.BlockMapping(775)
	if _, err = newJavaProfile(LatestVersion(), wrongProtocol); err == nil {
		t.Fatal("profile accepted another protocol's mapping")
	}
}

func TestJava775BlockCodecUsesMeasuredRenumberedState(t *testing.T) {
	// Official --reports blocks.json: calcite's default state is 27160 in
	// 26.2 and 24687 in 26.1. The generator records both input checksums.
	state := world.BlockStateMust("calcite")
	if state != 27160 {
		t.Fatalf("canonical fixture state=%d", state)
	}
	profile, _ := javaProfileFor(775)
	body, err := profile.encodeBlockUpdate(2, 70, 3, state)
	if err != nil {
		t.Fatal(err)
	}
	r := protocol.NewPacketReader(body)
	_, _, _, _ = r.Position()
	wire, err := r.VarInt()
	if err != nil || wire != 24687 {
		t.Fatalf("775 single-block wire state=%d,%v", wire, err)
	}
	body, err = profile.encodeSectionBlocks(0, 4, 0, []javaSectionBlock{{Local: 0x236, State: state}})
	if err != nil {
		t.Fatal(err)
	}
	r = protocol.NewPacketReader(body)
	_, _ = r.Long()
	_, _ = r.VarInt()
	entry, err := r.VarLong()
	if err != nil || uint64(entry)>>12 != 24687 || uint64(entry)&4095 != 0x236 {
		t.Fatalf("775 section entry=%x,%v", entry, err)
	}
}

func TestJavaPredictionCorrectionsUseViewerProjection(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	catalog := world.BlockCatalog()
	stone, _ := catalog.State(registry.BlockStateID(world.Stone))
	dirt, _ := catalog.State(registry.BlockStateID(world.Dirt))
	mapping, err := registry.NewBlockMapping(999, catalog, []registry.BlockWireEntry{{State: stone, WireID: 701}, {State: dirt, WireID: 91}}, registry.MissingBlockPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	normal, _ := enforceSession(t, s, "NormalCorrection")
	projected, _ := enforceSession(t, s, "ProjectedCorrection")
	projected.profile = &javaProfile{version: LatestVersion(), blocks: mapping}
	projected.loaded.Store(true)
	projected.setPos(2.5, 71, 5.5, 0, 0, true)
	clicked, face := world.Pos{X: 2, Y: 70, Z: 3}, world.Pos{X: 2, Y: 71, Z: 3}
	s.world.SetBlock(clicked, world.Stone)
	s.world.SetBlock(face, world.Dirt)
	sequence := int32(42)
	// Empty-handed use on plain stone still corrects both the clicked block
	// and its face neighbor. Exercise the actual prediction-revert closure.
	s.handleUseItem(projected, useItemPkt(0, 2, 70, 3, 1, &sequence))
	updates, acks := drainAcks(t, projected)
	if len(updates) != 2 || len(acks) != 1 || acks[0] != sequence {
		t.Fatalf("revert packets: updates=%d acks=%v", len(updates), acks)
	}
	check := func(packet protocol.Packet, wantPos world.Pos, wantState int32) {
		t.Helper()
		r := protocol.NewPacketReader(packet.Data)
		x, y, z, err := r.Position()
		if err != nil || int(x) != wantPos.X || int(y) != wantPos.Y || int(z) != wantPos.Z {
			t.Fatalf("correction position=%d,%d,%d,%v", x, y, z, err)
		}
		state, err := r.VarInt()
		if err != nil || state != wantState {
			t.Fatalf("correction state=%d,%v want%d", state, err, wantState)
		}
	}
	check(updates[0], clicked, 701)
	check(updates[1], face, 91)
	// Rejected survival digs use the same projection for their resync.
	s.sendBlockResync(projected, face)
	updates, _ = drainAcks(t, projected)
	if len(updates) != 1 {
		t.Fatalf("dig resync updates=%d", len(updates))
	}
	check(updates[0], face, 91)

	missing, err := registry.NewBlockMapping(999, catalog, []registry.BlockWireEntry{{State: stone, WireID: 701}}, registry.MissingBlockPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	projected.profile = &javaProfile{version: LatestVersion(), blocks: missing}
	s.sendBlockResync(projected, face)
	select {
	case <-projected.done:
	default:
		t.Fatal("unmapped correction did not retire viewer")
	}
	select {
	case <-normal.done:
		t.Fatal("unmapped correction retired another viewer")
	default:
	}
	updates, _ = drainAcks(t, projected)
	if len(updates) != 0 {
		t.Fatal("unmapped correction emitted a numeric block ID")
	}
	s.sendBlockResync(normal, face)
	updates, _ = drainAcks(t, normal)
	if len(updates) != 1 {
		t.Fatal("compatible viewer lost correction")
	}
	check(updates[0], face, int32(world.Dirt))
	if s.world.GetBlock(clicked) != world.Stone || s.world.GetBlock(face) != world.Dirt {
		t.Fatal("prediction corrections changed authoritative blocks")
	}
}
