package blackbox

import (
	"bytes"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestCloudMetaParity pins the area_effect_cloud pairing dirty-pack:
// the tracker-attach set_entity_data carries exactly the synced
// entries the summon NBT touched — DATA_RADIUS (idx 8, float) when it
// differs from the 3.0 default and DATA_PARTICLE (idx 10, single
// particle serializer 16) as entity_effect holding the mixed potion
// ARGB.
func TestCloudMetaParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	type result struct {
		cloudEID int32
		meta     map[int]metaEnt
	}

	run := func(addr string) result {
		var r result
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(30 * time.Second))

		sendCommand(t, pw, "gamemode creative P")
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		sendCommand(t, pw, "tp P 0.5 -60 0.5")
		fleePump(t, c, pr, pw, &fleeObs{}, 600*time.Millisecond)
		sendCommand(t, pw, "summon minecraft:area_effect_cloud 5.5 -60 0.5 {Radius:5.0f,Duration:400,potion_contents:{custom_effects:[{id:'minecraft:speed',duration:200,amplifier:1b}]}}")

		start := time.Now()
		for time.Since(start) < 5*time.Second {
			_ = c.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
			p, err := pr.ReadPacket()
			if err != nil {
				continue
			}
			switch p.ID {
			case server.IDPlayKeepAliveCB:
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
			case server.IDPlaySyncPos:
				rd := protocol.NewPacketReader(p.Data)
				if id, err := rd.VarInt(); err == nil {
					var b protocol.Buffer
					b.VarInt(id)
					_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
				}
			case server.IDPlaySpawnEntity:
				rd := protocol.NewPacketReader(p.Data)
				eid, _ := rd.VarInt()
				rd.UUID()
				if typ, _ := rd.VarInt(); typ == 3 {
					r.cloudEID = eid
				}
			case server.IDPlayEntityMetadata:
				rd := protocol.NewPacketReader(p.Data)
				eid, _ := rd.VarInt()
				rest, _ := rd.Read(rd.Remaining())
				if eid == r.cloudEID && r.cloudEID != 0 {
					got := decodeMetaEntries(t, rest)
					if r.meta == nil {
						r.meta = map[int]metaEnt{}
					}
					for k, v := range got {
						r.meta[k] = v
					}
				}
			}
		}
		return r
	}

	van, mc := run(targets.vanilla), run(targets.mcgo)
	for i, leg := range []result{van, mc} {
		if leg.cloudEID == 0 {
			t.Fatalf("leg %d: cloud spawn never tracked", i)
		}
		if leg.meta == nil {
			t.Fatalf("leg %d: no set_entity_data for the summoned cloud", i)
		}
	}
	// idx 8: float radius 5.0 on both.
	if !bytes.Equal(van.meta[8].val, mc.meta[8].val) || van.meta[8].typ != 3 || mc.meta[8].typ != 3 {
		t.Fatalf("radius entry diverged: vanilla idx8=%x(t%d) mc idx8=%x(t%d)",
			van.meta[8].val, van.meta[8].typ, mc.meta[8].val, mc.meta[8].typ)
	}
	if got := van.meta[8].val; len(got) != 4 || got[0] != 0x40 || got[1] != 0xa0 {
		t.Fatalf("vanilla radius entry not 5.0f: %x", got)
	}
	// idx 10: single particle entity_effect(28) + ARGB 0xFF33EBFF —
	// speed's effect color, one custom_effect mixes to itself.
	wantParticle := []byte{28, 0xFF, 0x33, 0xEB, 0xFF}
	if !bytes.Equal(van.meta[10].val, wantParticle) {
		t.Fatalf("vanilla particle entry = %x, want %x", van.meta[10].val, wantParticle)
	}
	if !bytes.Equal(mc.meta[10].val, van.meta[10].val) || mc.meta[10].typ != van.meta[10].typ {
		t.Fatalf("particle entry diverged: vanilla %x(t%d) mc %x(t%d)",
			van.meta[10].val, van.meta[10].typ, mc.meta[10].val, mc.meta[10].typ)
	}
	// idx 9: DATA_WAITING flips 1 during the wait then 0 — the merged
	// map holds the last value, which must land on both servers.
	vw, vok := van.meta[9]
	mw, mok := mc.meta[9]
	if !vok || !mok || vw.typ != mw.typ || !bytes.Equal(vw.val, mw.val) {
		t.Fatalf("waiting entry diverged: vanilla=%x(t%d,present=%v) mc=%x(t%d,present=%v)",
			vw.val, vw.typ, vok, mw.val, mw.typ, mok)
	}
	t.Logf("van entries=%d mc entries=%d radius=%x particle=%x",
		len(van.meta), len(mc.meta), van.meta[8].val, van.meta[10].val)
}
