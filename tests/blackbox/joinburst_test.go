package blackbox

import (
	"encoding/hex"
	"testing"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestJoinBurstDiagnostic logs the play-stage packets both servers send
// through and beyond Join Game. It is a development aid for ordering
// and shape parity: run with -v when implementing the next join slice.
func TestJoinBurstDiagnostic(t *testing.T) {
	tg := requireBlackbox(t)
	defer tg.cleanup()
	for _, tc := range []struct{ tag, addr string }{
		{"vanilla", tg.vanilla},
		{"mc-go", tg.mcgo},
	} {
		c, pr, _ := joinPlayBlackbox(t, tc.addr, "BurstProbe")
		func() {
			defer c.Close()
			for i := 0; i < 25; i++ {
				p, err := pr.ReadPacket()
				if err != nil {
					t.Logf("%s: read %d: %v", tc.tag, i, err)
					return
				}
				body := ""
				if len(p.Data) <= 64 {
					body = hex.EncodeToString(p.Data)
				} else {
					body = hex.EncodeToString(p.Data[:24]) + "..."
				}
				t.Logf("%s play #%d id=%#x len=%d body=%s", tc.tag, i, p.ID, len(p.Data), body)
			}
			// Decode the join game head: entity id, hardcore, dimension count.
			// (First packet is Join Game on both servers.)
		}()
	}
}

// TestJoinGameFields compares the decoded Join Game header fields of
// both servers, ignoring the fields that legitimately differ per world
// (entity id, hashed seed) and logging the rest for reconciliation.
func TestJoinGameFields(t *testing.T) {
	tg := requireBlackbox(t)
	defer tg.cleanup()

	type fields struct {
		entityID   int32
		hardcore   bool
		dims       []string
		maxPlayers int32
		viewDist   int32
		simDist    int32
		reduced    bool
		respawn    bool
		limited    bool
		dimType    int32
		dimName    string
		gamemode   byte
		prevMode   byte
		debug      bool
		flat       bool
		death      bool
		cooldown   int32
		seaLevel   int32
		online     bool
		secure     bool
	}
	read := func(tag, addr string) fields {
		c, pr, _ := joinPlayBlackbox(t, addr, "JoinProbe")
		defer c.Close()
		for i := 0; i < 10; i++ {
			p, err := pr.ReadPacket()
			if err != nil {
				t.Fatalf("%s: %v", tag, err)
			}
			if p.ID != server.IDPlayJoinGame {
				continue
			}
			r := protocol.NewPacketReader(p.Data)
			var f fields
			if f.entityID, err = r.Int(); err != nil {
				t.Fatalf("%s eid: %v", tag, err)
			}
			if f.hardcore, err = r.Bool(); err != nil {
				t.Fatalf("%s hardcore: %v", tag, err)
			}
			n, err := r.VarInt()
			if err != nil {
				t.Fatalf("%s dim count: %v", tag, err)
			}
			for d := int32(0); d < n; d++ {
				s, err := r.String()
				if err != nil {
					t.Fatalf("%s dim name: %v", tag, err)
				}
				f.dims = append(f.dims, s)
			}
			f.maxPlayers, _ = r.VarInt()
			f.viewDist, _ = r.VarInt()
			f.simDist, _ = r.VarInt()
			f.reduced, _ = r.Bool()
			f.respawn, _ = r.Bool()
			f.limited, _ = r.Bool()
			f.dimType, _ = r.VarInt()
			f.dimName, _ = r.String()
			_, _ = r.Long() // hashed seed
			f.gamemode, _ = r.Byte()
			f.prevMode, _ = r.Byte()
			f.debug, _ = r.Bool()
			f.flat, _ = r.Bool()
			f.death, _ = r.Bool()
			f.cooldown, _ = r.VarInt()
			f.seaLevel, _ = r.VarInt()
			f.online, _ = r.Bool()
			f.secure, _ = r.Bool()
			_ = i
			return f
		}
		t.Fatalf("%s: no join game", tag)
		return fields{}
	}

	v := read("vanilla", tg.vanilla)
	m := read("mc-go", tg.mcgo)
	t.Logf("vanilla join: dims=%v max=%d view=%d sim=%d respawn=%v dim=%s(%d) mode=%d flat=%v sea=%d online=%v secure=%v",
		v.dims, v.maxPlayers, v.viewDist, v.simDist, v.respawn, v.dimName, v.dimType, v.gamemode, v.flat, v.seaLevel, v.online, v.secure)
	t.Logf("mc-go   join: dims=%v max=%d view=%d sim=%d respawn=%v dim=%s(%d) mode=%d flat=%v sea=%d online=%v secure=%v",
		m.dims, m.maxPlayers, m.viewDist, m.simDist, m.respawn, m.dimName, m.dimType, m.gamemode, m.flat, m.seaLevel, m.online, m.secure)

	// Vanilla's dimension name order varies between runs (map-backed);
	// compare as sets.
	if len(v.dims) != len(m.dims) {
		t.Errorf("dimension count: vanilla %v, mc-go %v", v.dims, m.dims)
	}
	for _, d := range v.dims {
		found := false
		for _, md := range m.dims {
			if md == d {
				found = true
			}
		}
		if !found {
			t.Errorf("mc-go dimensions lack %s: %v", d, m.dims)
		}
	}
	if v.maxPlayers != m.maxPlayers {
		t.Errorf("max players: vanilla %d, mc-go %d", v.maxPlayers, m.maxPlayers)
	}
	if v.respawn != m.respawn {
		t.Errorf("respawn screen: vanilla %v, mc-go %v", v.respawn, m.respawn)
	}
	if v.gamemode != m.gamemode {
		t.Errorf("gamemode: vanilla %d, mc-go %d", v.gamemode, m.gamemode)
	}
	if v.dimType != m.dimType || v.dimName != m.dimName {
		t.Errorf("dimension: vanilla %s(%d), mc-go %s(%d)", v.dimName, v.dimType, m.dimName, m.dimType)
	}
	if v.seaLevel != m.seaLevel {
		t.Errorf("sea level: vanilla %d, mc-go %d", v.seaLevel, m.seaLevel)
	}
	if v.online != m.online || v.secure != m.secure {
		t.Errorf("online/secure: vanilla %v/%v, mc-go %v/%v", v.online, v.secure, m.online, m.secure)
	}
}
