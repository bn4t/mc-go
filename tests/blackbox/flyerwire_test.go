package blackbox

// flyerWire is the shared packet recorder for the Blaze/Ghast/Phantom
// pins: a reader goroutine keeps the session alive (keep-alive, teleport
// confirm, chunk-batch ack) and timestamps every non-chunk packet, and
// the helpers below decode the handful of clientbound shapes the pins
// read — add_entity, the move family, set_entity_data's index entries,
// level_event and set_health.

import (
	"math"
	"net"
	"sync"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

type fwPkt struct {
	at   time.Time
	id   int32
	data []byte
}

type flyerWire struct {
	mu   sync.Mutex
	pkts []fwPkt
	err  error
}

func startFlyerWire(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer) *flyerWire {
	t.Helper()
	fw := &flyerWire{}
	_ = c.SetReadDeadline(time.Time{})
	go func() {
		for {
			p, err := pr.ReadPacket()
			if err != nil {
				fw.mu.Lock()
				fw.err = err
				fw.mu.Unlock()
				return
			}
			switch p.ID {
			case server.IDPlayKeepAliveCB:
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
			case server.IDPlayChunkBatchDone:
				_ = ackChunkBatch(pw)
			case server.IDPlaySyncPos:
				r := protocol.NewPacketReader(p.Data)
				if id, err := r.VarInt(); err == nil {
					var b protocol.Buffer
					b.VarInt(id)
					_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
				}
			case server.IDPlayChunkData, server.IDPlayUpdateLight:
				continue
			}
			d := make([]byte, len(p.Data))
			copy(d, p.Data)
			fw.mu.Lock()
			fw.pkts = append(fw.pkts, fwPkt{at: time.Now(), id: p.ID, data: d})
			fw.mu.Unlock()
		}
	}()
	return fw
}

// mark returns the current packet count — a cursor for since().
func (fw *flyerWire) mark() int {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	return len(fw.pkts)
}

func (fw *flyerWire) since(i int) []fwPkt {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	out := make([]fwPkt, len(fw.pkts)-i)
	copy(out, fw.pkts[i:])
	return out
}

// fwSpawn is one decoded add_entity.
type fwSpawn struct {
	at         time.Time
	eid, typ   int32
	x, y, z    float64
	vx, vy, vz float64
	data       int32
}

func fwDecodeSpawn(p fwPkt) (fwSpawn, bool) {
	r := protocol.NewPacketReader(p.data)
	var s fwSpawn
	var err error
	s.at = p.at
	if s.eid, err = r.VarInt(); err != nil {
		return s, false
	}
	r.UUID()
	if s.typ, err = r.VarInt(); err != nil {
		return s, false
	}
	s.x, _ = r.Double()
	s.y, _ = r.Double()
	s.z, _ = r.Double()
	vx, vy, vz, ok := decodeLpVec3(r)
	if !ok {
		return s, false
	}
	s.vx, s.vy, s.vz = vx, vy, vz
	_, _ = r.Byte()
	_, _ = r.Byte()
	_, _ = r.Byte()
	s.data, _ = r.VarInt()
	return s, true
}

// fwTracker follows client-side entity positions through the spawn,
// move and position-sync packets, the way the real client does.
type fwTracker struct {
	pos map[int32][3]float64
	typ map[int32]int32
}

func newFwTracker() *fwTracker {
	return &fwTracker{pos: map[int32][3]float64{}, typ: map[int32]int32{}}
}

func (tr *fwTracker) feed(p fwPkt) {
	switch p.id {
	case server.IDPlaySpawnEntity:
		if s, ok := fwDecodeSpawn(p); ok {
			tr.pos[s.eid] = [3]float64{s.x, s.y, s.z}
			tr.typ[s.eid] = s.typ
		}
	case server.IDPlayEntityUpdatePos, 0x36:
		r := protocol.NewPacketReader(p.data)
		eid, err := r.VarInt()
		if err != nil {
			return
		}
		dx, _ := r.Short()
		dy, _ := r.Short()
		dz, _ := r.Short()
		if q, ok := tr.pos[eid]; ok {
			tr.pos[eid] = [3]float64{q[0] + float64(dx)/4096, q[1] + float64(dy)/4096, q[2] + float64(dz)/4096}
		}
	case server.IDPlayEntityTeleportCB:
		r := protocol.NewPacketReader(p.data)
		eid, err := r.VarInt()
		if err != nil {
			return
		}
		x, _ := r.Double()
		y, _ := r.Double()
		z, _ := r.Double()
		tr.pos[eid] = [3]float64{x, y, z}
	}
}

// fwMetaEntry is one decoded set_entity_data entry; only the simple
// serializers the pins meet are decoded (byte, varint, float, bool,
// pose) — anything else ends the walk.
type fwMetaEntry struct {
	idx, typ int
	i        int64
	f        float32
	b        bool
}

func fwDecodeMeta(data []byte) (eid int32, out []fwMetaEntry) {
	r := protocol.NewPacketReader(data)
	eid, err := r.VarInt()
	if err != nil {
		return -1, nil
	}
	for {
		idx, err := r.Byte()
		if err != nil || idx == 0xFF {
			return
		}
		typ, err := r.VarInt()
		if err != nil {
			return
		}
		e := fwMetaEntry{idx: int(idx), typ: int(typ)}
		switch typ {
		case 0:
			v, _ := r.Byte()
			e.i = int64(v)
		case 1, 20:
			v, _ := r.VarInt()
			e.i = int64(v)
		case 3:
			e.f, _ = r.Float()
		case 8:
			v, _ := r.Byte()
			e.b = v != 0
		default:
			return
		}
		out = append(out, e)
	}
}

// fwLevelEvent decodes level_event: int event, block pos, int data.
func fwLevelEvent(p fwPkt) (ev int32, x, y, z int, ok bool) {
	r := protocol.NewPacketReader(p.data)
	e, err := r.Int()
	if err != nil {
		return 0, 0, 0, 0, false
	}
	v, err := r.Long()
	if err != nil {
		return 0, 0, 0, 0, false
	}
	x = int(v >> 38)
	y = int(v << 52 >> 52)
	z = int(v << 26 >> 38)
	return e, x, y, z, true
}

// fwHealth decodes set_health's leading float.
func fwHealth(p fwPkt) (float32, bool) {
	r := protocol.NewPacketReader(p.data)
	h, err := r.Float()
	return h, err == nil
}

func fwLen(x, y, z float64) float64 { return math.Sqrt(x*x + y*y + z*z) }
