package server

import (
	"bytes"
	"math"
	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"testing"
)

func TestEntityTagQueryPermissionAndLevelLookup(t *testing.T) {
	for _, row := range []struct {
		name           string
		level          int
		dim            string
		missing, reply bool
	}{
		{name: "nonoperator", level: 0, dim: dimOverworld},
		{name: "level_one", level: 1, dim: dimOverworld},
		{name: "gamemaster", level: 2, dim: dimOverworld, reply: true},
		{name: "other_level", level: 4, dim: dimNether},
		{name: "missing", level: 4, dim: dimOverworld, missing: true},
	} {
		t.Run(row.name, func(t *testing.T) {
			s := New("unused")
			t.Cleanup(s.Stop)
			s.OpsRequired = true
			s.SetOpLevel("QueryUnit", row.level)
			sess := &Session{server: s, ver: LatestVersion(), sendCh: make(chan sendPkt, 8), done: make(chan struct{})}
			sess.setIdentity("QueryUnit", offlineUUID("QueryUnit"))
			e := s.entities.Spawn(&Entity{Type: mustEntityType("pig"), Dim: row.dim, Living: true, Health: 7.5, ExtraNBT: map[string]any{"id": "minecraft:pig"}})
			id := e.ID
			if row.missing {
				id = math.MaxInt32
			}
			var query protocol.Buffer
			query.VarInt(math.MinInt32)
			query.VarInt(id)
			s.handleEntityTagQuery(sess, protocol.Packet{Data: query.Bytes()})
			if !row.reply {
				if len(sess.sendCh) != 0 {
					t.Fatal("unexpected reply")
				}
				return
			}
			if len(sess.sendCh) != 1 {
				t.Fatal("missing canonical reply")
			}
			packet := materializeOutboundPacket(t, sess, <-sess.sendCh)
			r := bytes.NewReader(packet.data)
			tx, err := protocol.ReadVarInt(r)
			if err != nil || tx != math.MinInt32 {
				t.Fatalf("transaction%d error%v", tx, err)
			}
			value, err := nbt.ReadUnnamed(r)
			if err != nil {
				t.Fatal(err)
			}
			m, ok := value.(map[string]any)
			if !ok {
				t.Fatalf("root%T", value)
			}
			if _, ok := m["id"]; ok {
				t.Fatal("query included outer type identifier")
			}
			if m["Health"] != float32(7.5) {
				t.Fatalf("Health%#v", m["Health"])
			}
			if u, ok := m["UUID"].([]int32); !ok || len(u) != 4 {
				t.Fatalf("UUID%#v", m["UUID"])
			}
			if e.ExtraNBT["id"] != "minecraft:pig" {
				t.Fatal("query mutated native entity extras")
			}
		})
	}
}
