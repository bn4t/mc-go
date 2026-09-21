package blackbox

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// explodePkt is the decoded explode packet: center, blast radius,
// blockCount, the optional per-recipient knockback, the burst
// particle id and the sound holder id.
type explodePkt struct {
	x, y, z     float64
	radius      float32
	blockCount  int32
	hasKB       bool
	kb          [3]float64
	particleID  int32
	soundHolder int32
}

func decodeExplode(t *testing.T, data []byte) explodePkt {
	t.Helper()
	r := protocol.NewPacketReader(data)
	var out explodePkt
	out.x, _ = r.Double()
	out.y, _ = r.Double()
	out.z, _ = r.Double()
	out.radius, _ = r.Float()
	out.blockCount, _ = r.Int()
	out.hasKB, _ = r.Bool()
	if out.hasKB {
		out.kb[0], _ = r.Double()
		out.kb[1], _ = r.Double()
		out.kb[2], _ = r.Double()
	}
	out.particleID, _ = r.VarInt()
	out.soundHolder, _ = r.VarInt()
	if n, err := r.VarInt(); err != nil || n != 0 {
		t.Fatalf("block-particles weighted list = %d, err %v (want empty)", n, err)
	}
	if r.Remaining() != 0 {
		t.Fatalf("explode trailing bytes: %x", data[len(data)-r.Remaining():])
	}
	return out
}

// TestWindChargeExplodeParity pins the explode packet a wind_charge
// burst emits on vanilla 26.2: it goes to every player within 64
// blocks carrying center + radius + the blast sampler's blockCount +
// gust_emitter_small + the wind_charge.wind_burst sound holder +
// empty block-particles list; the victim's copy carries the knockback
// vector inline (no separate set_entity_motion reaches the victim)
// while the unhit observer's omits it. Ordering: explode precedes
// remove_entities for the charge.
func TestWindChargeExplodeParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BoomV", "BoomW"),
	}, func(s *server.Server) {})
	defer targets.cleanup()

	type capture struct {
		victim, watcher explodePkt
		burstOrder      []int32 // explode=0x24 / remove=0x4d on the victim conn
		victimMotion    bool    // any set_entity_motion on the victim conn
		watcherMotion   bool    // set_entity_motion for the victim on the watcher conn
	}
	run := func(addr string) capture {
		cv, prv, pwv := joinPlayBlackbox(t, addr, "BoomV")
		defer cv.Close()
		_ = cv.SetDeadline(time.Time{})
		_ = pwv.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sendCommand(t, pwv, "gamemode survival @s")
		sendCommand(t, pwv, "tp @s 8.5 -60 8.5 0 0")
		cw, prw, pww := joinPlayBlackbox(t, addr, "BoomW")
		defer cw.Close()
		_ = cw.SetDeadline(time.Time{})
		_ = pww.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sendCommand(t, pww, "gamemode creative @s")
		sendCommand(t, pww, "tp @s 4.5 -60 8.5 90 0")
		_ = sendMovePosRot(pww, 4.5, -60, 8.5)
		drainToQuietBlackbox(t, cv, prv, 400*time.Millisecond)
		drainToQuietBlackbox(t, cw, prw, 400*time.Millisecond)

		sendCommand(t, pww, "summon minecraft:wind_charge 8.5 -60.2 8.5 {Motion:[0d,-0.2d,0d]}")

		var got capture
		var victimEID int32
		playerType, _ := server.EntityTypeID("player")
		end := time.Now().Add(8 * time.Second)
		for time.Now().Before(end) && (got.victim.radius == 0 || got.watcher.radius == 0) {
			_ = cv.SetReadDeadline(time.Now().Add(800 * time.Millisecond))
			for {
				p, err := prv.ReadPacket()
				if err != nil {
					break
				}
				switch p.ID {
				case server.IDPlayKeepAliveCB:
					_ = pwv.WritePacket(server.IDPlayKeepAliveSB, p.Data)
				case server.IDPlayChunkBatchDone:
					_ = ackChunkBatch(pwv)
				case 0x24:
					got.victim = decodeExplode(t, p.Data)
					got.burstOrder = append(got.burstOrder, 0x24)
				case server.IDPlayRemoveEntity:
					got.burstOrder = append(got.burstOrder, 0x4d)
				case 0x65:
					got.victimMotion = true
				}
			}
			_ = cw.SetReadDeadline(time.Now().Add(800 * time.Millisecond))
			for {
				p, err := prw.ReadPacket()
				if err != nil {
					break
				}
				switch p.ID {
				case server.IDPlayKeepAliveCB:
					_ = pww.WritePacket(server.IDPlayKeepAliveSB, p.Data)
				case server.IDPlayChunkBatchDone:
					_ = ackChunkBatch(pww)
				case server.IDPlaySpawnEntity:
					r := protocol.NewPacketReader(p.Data)
					id, _ := r.VarInt()
					_, _ = r.Read(16)
					typ, _ := r.VarInt()
					if typ == playerType {
						victimEID = id
					}
				case 0x24:
					got.watcher = decodeExplode(t, p.Data)
				case 0x65:
					r := protocol.NewPacketReader(p.Data)
					if eid, _ := r.VarInt(); eid == victimEID {
						got.watcherMotion = true
					}
				}
			}
		}
		return got
	}

	van := run(targets.vanilla)
	got := run(targets.mcgo)

	if van.victim.radius == 0 || van.watcher.radius == 0 {
		if b, err := os.ReadFile(filepath.Join(vanillaDirForDebug, "vanilla.log")); err == nil {
			t.Logf("vanilla log:\n%s", b)
		}
		t.Fatalf("vanilla explode not captured: victim=%+v watcher=%+v", van.victim, van.watcher)
	}
	if got.victim.radius == 0 || got.watcher.radius == 0 {
		t.Fatalf("mc-go explode missing: victim=%+v watcher=%+v", got.victim, got.watcher)
	}
	if got.victim.radius != van.victim.radius {
		t.Fatalf("radius %v, want %v", got.victim.radius, van.victim.radius)
	}
	if got.victim.particleID != van.victim.particleID || got.victim.soundHolder != van.victim.soundHolder {
		t.Fatalf("particle/sound = %d/%d, want %d/%d", got.victim.particleID, got.victim.soundHolder, van.victim.particleID, van.victim.soundHolder)
	}
	// The blast sampler is randomized per explosion — both counts just
	// have to land in the flat-ground band.
	if got.victim.blockCount <= 0 || van.victim.blockCount <= 0 {
		t.Fatalf("blockCount mc=%d van=%d, want >0", got.victim.blockCount, van.victim.blockCount)
	}
	// The victim carries the knockback inline; an unhit observer gets
	// no field.
	if !van.victim.hasKB || !got.victim.hasKB {
		t.Fatalf("victim knockback field missing (van=%v mc=%v)", van.victim.hasKB, got.victim.hasKB)
	}
	if van.watcher.hasKB || got.watcher.hasKB {
		t.Fatalf("observer should carry no knockback (van=%v mc=%v)", van.watcher.hasKB, got.watcher.hasKB)
	}
	// Player directly under the burst: knockback is mostly up, of the
	// (1-dist)*1.22 order — compare loosely across the differing
	// contact points.
	if got.victim.kb[1] <= 0.5 || math.Abs(got.victim.kb[1]-van.victim.kb[1]) > 0.5 {
		t.Fatalf("victim kb.y mc=%v van=%v", got.victim.kb[1], van.victim.kb[1])
	}
	if got.victimMotion != van.victimMotion || got.watcherMotion != van.watcherMotion {
		t.Fatalf("motion packets differ (mc victim=%v watcher=%v, van victim=%v watcher=%v)",
			got.victimMotion, got.watcherMotion, van.victimMotion, van.watcherMotion)
	}
	// Explode precedes the charge's remove_entities on the wire.
	if len(got.burstOrder) < 2 || got.burstOrder[0] != 0x24 {
		t.Fatalf("burst order %v, want explode before remove", got.burstOrder)
	}
}
