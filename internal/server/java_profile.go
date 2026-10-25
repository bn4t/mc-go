package server

import (
	"errors"
	"fmt"
	"sync"

	"mc-go/api/registry"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

var ErrIncompleteJavaProfile = errors.New("Java protocol profile is not ready for admission")

// JavaProfileSupport reports independently implemented codec surfaces and the
// remaining admission gates. Missing lists implementation gates, not content
// parity: an admitted profile can reject content absent from its target.
// MissingBlockStates counts strict block-state exclusions. A required missing
// state retires only the viewer that needs it.
type JavaProfileSupport struct {
	Name                    string
	Protocol                int32
	Admission               bool
	LoginSuccessCodec       bool
	JoinGameCodec           bool
	BlockProjection         bool
	BlockProjectionComplete bool
	MissingBlockStates      int
	ConfigurationData       bool
	ConfigurationRegistries int
	Missing                 []string
}

// javaProfile owns wire choices. Its encoders accept detached values, never a
// concrete Server or Session. The existing gameplay handlers remain in server;
// only a fully admitted profile may select their dispatcher.
type javaProfile struct {
	version        Version
	admitted       bool
	loginSessionID bool
	joinOnlineMode bool
	blocks         *registry.BlockMapping
	routes         map[int32]playHandler
	missing        []string
	config         *javaConfigData
}

var javaProfileSet struct {
	sync.Once
	profiles map[int32]*javaProfile
}

// newJavaProfile binds a codec to this runtime's canonical catalog. Equal
// numeric handles from a different catalog are not interchangeable. Candidate
// codecs never gain admission merely by being supplied a complete table.
func newJavaProfile(version Version, blocks *registry.BlockMapping) (*javaProfile, error) {
	if blocks == nil || blocks.Catalog() != world.BlockCatalog() {
		return nil, errors.New("Java profile block mapping belongs to a different catalog")
	}
	if blocks.Protocol() != int(version.Protocol) {
		return nil, errors.New("Java profile block mapping belongs to a different protocol")
	}
	data, err := javaConfigFor(version.Protocol)
	if err != nil {
		return nil, err
	}
	p := &javaProfile{version: version, blocks: blocks, config: data}
	switch version.Protocol {
	case 776:
		if err := blocks.RequireComplete(); err != nil {
			return nil, err
		}
		p.admitted, p.loginSessionID, p.joinOnlineMode = true, true, true
		p.routes = make(map[int32]playHandler, len(playServerbound))
		for id, handler := range playServerbound {
			p.routes[id] = handler
		}
	case 775:
		// Common-content codecs and the measured version-specific input
		// decoders are complete. Projection remains strict: newer canonical
		// content absent from 775 is not a numeric substitute or admission gate.
		p.admitted = true
		p.routes = make(map[int32]playHandler, len(playServerbound))
		for id, handler := range playServerbound {
			p.routes[id] = handler
		}
	default:
		return nil, errors.New("unmeasured Java codec profile")
	}
	return p, nil
}

func loadJavaProfiles() {
	javaProfileSet.Do(func() {
		current := LatestVersion()
		blocks776, _ := world.BlockMapping(int(current.Protocol))
		blocks775, _ := world.BlockMapping(775)
		profile776, err := newJavaProfile(current, blocks776)
		if err != nil {
			panic(err)
		}
		profile775, err := newJavaProfile(java775Version(), blocks775)
		if err != nil {
			panic(err)
		}
		javaProfileSet.profiles = map[int32]*javaProfile{current.Protocol: profile776, 775: profile775}
	})
}

// Official packet reports measured the same packet numbers in775/776. The
// renamed0x3e payload is decoded by the profile, not inferred from its number.
// EntityType fields remain canonical gameplay handles, never target wire IDs.
func java775Version() Version {
	v := LatestVersion()
	v.Name, v.Protocol = "26.1", 775
	return v
}

func javaProfileFor(protocolNumber int32) (*javaProfile, bool) {
	loadJavaProfiles()
	profile, ok := javaProfileSet.profiles[protocolNumber]
	return profile, ok
}

// JavaProfiles includes candidates as well as admitted profiles. SupportedVersions
// remains the authoritative list accepted by the Java login handshake.
func JavaProfiles() []JavaProfileSupport {
	loadJavaProfiles()
	profiles := make([]JavaProfileSupport, 0, 2)
	for _, number := range []int32{775, ProtocolVersion} {
		p, ok := javaProfileFor(number)
		if !ok {
			continue
		}
		missingBlocks := len(p.blocks.Missing())
		profiles = append(profiles, JavaProfileSupport{Name: p.version.Name, Protocol: number,
			Admission: p.admitted, LoginSuccessCodec: true, JoinGameCodec: true,
			BlockProjection: p.blocks != nil, BlockProjectionComplete: missingBlocks == 0,
			MissingBlockStates: missingBlocks, ConfigurationData: p.config != nil,
			ConfigurationRegistries: len(p.config.registries), Missing: append([]string(nil), p.missing...)})
	}
	return profiles
}

func (p *javaProfile) admissionError() error {
	if !p.admitted {
		return fmt.Errorf("%w: protocol %d: %v", ErrIncompleteJavaProfile, p.version.Protocol, p.missing)
	}
	return nil
}

func (sess *Session) javaProfile() (*javaProfile, error) {
	if sess.profile != nil {
		return sess.profile, nil
	}
	p, ok := javaProfileFor(sess.ver.Protocol)
	if !ok {
		return nil, fmt.Errorf("unknown Java protocol profile %d", sess.ver.Protocol)
	}
	return p, nil
}

func (p *javaProfile) playHandler(id int32) (playHandler, bool) {
	if !p.admitted {
		return nil, false
	}
	handler, ok := p.routes[id]
	return handler, ok
}

func (p *javaProfile) blockState(state world.Block) (int32, error) {
	if p.blocks == nil {
		return -1, errors.New("Java profile has no block projection")
	}
	return p.blocks.Encode(registry.BlockStateID(state))
}

// javaLoginSuccess is authenticated profile data plus the optional 776 session
// identifier. Properties and signatures are already detached from transport.
type javaLoginSuccess struct {
	UUID       [16]byte
	Name       string
	Properties []profileProperty
	SessionID  [16]byte
}

func (p *javaProfile) encodeLoginSuccess(login javaLoginSuccess) []byte {
	var b protocol.Buffer
	b.UUID(login.UUID)
	b.String(login.Name)
	b.VarInt(int32(len(login.Properties)))
	for _, property := range login.Properties {
		b.String(property.name)
		b.String(property.value)
		b.Bool(property.signature != "")
		if property.signature != "" {
			b.String(property.signature)
		}
	}
	if p.loginSessionID {
		b.UUID(login.SessionID)
	}
	return b.Bytes()
}

// javaJoinGame is detached input for the profile-owned Java packet layout.
// Registry indices are profile-owned wire values, not canonical runtime IDs.
type javaJoinGame struct {
	EntityID                                     int32
	Hardcore                                     bool
	Dimensions                                   []string
	MaxPlayers, ViewDistance, SimulationDistance int32
	ReducedDebug, RespawnScreen, LimitedCrafting bool
	DimensionType                                int32
	Dimension                                    string
	HashedSeed                                   int64
	GameMode, PreviousGameMode                   byte
	Debug, Flat                                  bool
	LastDeath                                    *javaDeathLocation
	PortalCooldown, SeaLevel                     int32
	OnlineMode, SecureChat                       bool
}

type javaDeathLocation struct {
	Dimension string
	X, Y, Z   int32
}

func (p *javaProfile) encodeJoinGame(join javaJoinGame) []byte {
	var b protocol.Buffer
	b.Int(join.EntityID)
	b.Bool(join.Hardcore)
	b.VarInt(int32(len(join.Dimensions)))
	for _, dimension := range join.Dimensions {
		b.String(dimension)
	}
	b.VarInt(join.MaxPlayers)
	b.VarInt(join.ViewDistance)
	b.VarInt(join.SimulationDistance)
	b.Bool(join.ReducedDebug)
	b.Bool(join.RespawnScreen)
	b.Bool(join.LimitedCrafting)
	p.writeSpawnInfo(&b, join)
	if p.joinOnlineMode {
		b.Bool(join.OnlineMode)
	}
	b.Bool(join.SecureChat)
	return b.Bytes()
}

// CommonPlayerSpawnInfo has the same measured layout in 775 and 776; Join
// and Respawn share it rather than assigning unrelated registry indices.
func (p *javaProfile) writeSpawnInfo(b *protocol.Buffer, join javaJoinGame) {
	b.VarInt(join.DimensionType)
	b.String(join.Dimension)
	b.Long(join.HashedSeed)
	b.Byte(join.GameMode)
	b.Byte(join.PreviousGameMode)
	b.Bool(join.Debug)
	b.Bool(join.Flat)
	b.Bool(join.LastDeath != nil)
	if join.LastDeath != nil {
		b.String(join.LastDeath.Dimension)
		b.Position(join.LastDeath.X, join.LastDeath.Y, join.LastDeath.Z)
	}
	b.VarInt(join.PortalCooldown)
	b.VarInt(join.SeaLevel)
}

func (p *javaProfile) encodeRespawn(join javaJoinGame, dataKept byte) []byte {
	var b protocol.Buffer
	p.writeSpawnInfo(&b, join)
	b.Byte(dataKept)
	return b.Bytes()
}

func (p *javaProfile) encodeBlockUpdate(x, y, z int32, state world.Block) ([]byte, error) {
	wire, err := p.blockState(state)
	if err != nil {
		return nil, err
	}
	return encodeBlockUpdate(x, y, z, wire), nil
}

// sendProjectedBlockUpdate is the single-viewer correction path. A failed
// exact projection retires this viewer instead of sending a different state's
// numeric ID. The authoritative block and other viewers remain unchanged.
func (sess *Session) sendProjectedBlockUpdate(pos world.Pos, state world.Block) error {
	profile, err := sess.javaProfile()
	var body []byte
	if err == nil {
		body, err = profile.encodeBlockUpdate(int32(pos.X), int32(pos.Y), int32(pos.Z), state)
	}
	if err != nil {
		sess.closeQueue()
		return err
	}
	return sess.write(sess.ids().PlayBlockUpdateCB, body)
}

type javaSectionBlock struct {
	Local uint16
	State world.Block
}

func (p *javaProfile) encodeSectionBlocks(x, y, z int, states []javaSectionBlock) ([]byte, error) {
	var b protocol.Buffer
	section := int64(int64(x)&0x3fffff)<<42 | int64(int64(z)&0x3fffff)<<20 | int64(y)&0xfffff
	b.Long(section)
	b.VarInt(int32(len(states)))
	for _, state := range states {
		wire, err := p.blockState(state.State)
		if err != nil {
			return nil, err
		}
		b.VarLong(int64(uint64(wire)<<12 | uint64(state.Local)))
	}
	return b.Bytes(), nil
}
