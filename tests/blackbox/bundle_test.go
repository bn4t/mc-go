package blackbox

// TestPairingBundleParity pins vanilla's pairing-burst framing:
// ServerEntity.addPairing collects sendPairingData's packets — the
// add_entity, set_entity_data, update_attributes, set_equipment,
// set_passengers, set_entity_link stream — into ONE
// ClientboundBundlePacket, which hits the wire as bundle_delimiter,
// the inner packets, bundle_delimiter. A zombie summoned with
// equipment gives a deterministic four-packet bundle on vanilla;
// mc-go must wrap the same stream the same way.

import (
	"net"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// bundleAround returns the packet ids strictly inside the bundle that
// contains the add_entity for the wanted entity type — the ids
// between the bundle_delimiter that precedes add_entity and the one
// that closes the bundle.
func bundleAround(t *testing.T, tag string, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, wantType string) []int32 {
	t.Helper()
	var all []struct {
		id   int32
		data []byte
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(time.Now().Add(80 * time.Millisecond))
		for {
			p, err := pr.ReadPacket()
			if err != nil {
				break
			}
			switch p.ID {
			case server.IDPlayKeepAliveCB:
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
			case server.IDPlayChunkBatchDone:
				_ = ackChunkBatch(pw)
			}
			all = append(all, struct {
				id   int32
				data []byte
			}{int32(p.ID), p.Data})
		}
		// Scan what arrived so far for the entity's add_entity.
		for i, pk := range all {
			if pk.id != int32(server.IDPlaySpawnEntity) {
				continue
			}
			r := protocol.NewPacketReader(pk.data)
			if _, err := r.VarInt(); err != nil {
				continue
			}
			if _, err := r.UUID(); err != nil {
				continue
			}
			ty, err := r.VarInt()
			if err != nil {
				continue
			}
			if name, ok := server.EntityTypeName(ty); !ok || name != wantType {
				continue
			}
			// Delimiter must open immediately before add_entity.
			if i == 0 || all[i-1].id != int32(0x00) {
				t.Fatalf("%s: add_entity not preceded by bundle_delimiter (ids %v)", tag, all)
			}
			var inner []int32
			closed := false
			for _, q := range all[i:] {
				if q.id == int32(0x00) {
					closed = true
					break
				}
				inner = append(inner, q.id)
			}
			if !closed {
				t.Fatalf("%s: bundle never closed (ids %v)", tag, all)
			}
			return inner
		}
	}
	t.Fatalf("%s: no bundled add_entity for %s", tag, wantType)
	return nil
}

func TestPairingBundleParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Trk"),
	}, nil)
	defer targets.cleanup()

	run := func(tag, addr string) []int32 {
		c, pr, pw := joinPlayBlackbox(t, addr, "Trk")
		defer c.Close()
		drainChunkWindow(t, tag, c, pr, pw, 300*time.Millisecond)
		time.Sleep(600 * time.Millisecond)
		done := make(chan []int32, 1)
		go func() {
			done <- bundleAround(t, tag, c, pr, pw, "zombie")
		}()
		time.Sleep(150 * time.Millisecond)
		sendCommand(t, pw, "summon minecraft:zombie 8 -60 8 {equipment:{mainhand:{id:\"minecraft:iron_sword\",count:1}}}")
		return <-done
	}

	want := run("vanilla", targets.vanilla)
	got := run("mc-go", targets.mcgo)
	t.Logf("vanilla bundle=%v mc-go bundle=%v", want, got)
	if len(got) != len(want) {
		t.Fatalf("bundle inner count: vanilla=%v mc-go=%v", want, got)
	}
	for i := range want {
		if want[i] != got[i] {
			t.Fatalf("bundle[%d]: vanilla=%v mc-go=%v", i, want, got)
		}
	}
}
