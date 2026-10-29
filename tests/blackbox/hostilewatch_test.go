package blackbox

import (
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// hostileLog is the ordered wire record the hostile-family pins
// (evoker, vex, ravager, spider, cube push) read: every add_entity,
// entity_event, metadata entry, positioned sound, damage_event,
// block_update and motion packet, stamped with the arrival time so a
// test can bound cadences. Positions are folded per entity from the
// spawn packet plus move/sync/teleport packets.
type hostileLog struct {
	events []hostileEv
	pos    map[int32][3]float64
	types  map[int32]string
	maxY   map[int32]float64
	// refEid is the entity whose folded position each add event
	// snapshots into ref (the caster a spawn is measured against).
	refEid int32
}

type hostileEv struct {
	at    time.Time
	kind  string // add, remove, event, meta, sound, damage, block, motion
	eid   int32
	typ   string     // add: entity type name
	xyz   [3]float64 // add/motion/sound position or vector
	idx   int        // meta index / event status / sound id / damage type
	ser   int32      // meta serializer
	val   int64      // meta value (byte/int/bool), damage cause
	fval  float64    // meta float value
	block int32      // block_update state id
	bpos  [3]int32   // block_update position
	ref   [3]float64 // add: lg.refEid's position at arrival
}

func newHostileLog() *hostileLog {
	return &hostileLog{pos: map[int32][3]float64{}, types: map[int32]string{}, maxY: map[int32]float64{}}
}

// hostileTap owns both pair sockets from start on: one goroutine per
// conn reads frames with no read deadline (a deadline expiring mid
// frame would desync the stream), answers keepalive / chunk-batch /
// teleport like a client, and queues conn A's packets with their
// arrival time for the log.
type hostileTap struct {
	mu   sync.Mutex
	pkts []tappedPacket
}

type tappedPacket struct {
	at time.Time
	p  protocol.Packet
}

func startHostileTap(v *vehPair) *hostileTap {
	tp := &hostileTap{}
	_ = v.ca.SetReadDeadline(time.Time{})
	_ = v.cb.SetReadDeadline(time.Time{})
	go tp.pump(v.pra, v.pwa, true)
	go tp.pump(v.prb, v.pwb, false)
	return tp
}

func (tp *hostileTap) pump(pr *protocol.Reader, pw *protocol.Writer, keep bool) {
	for {
		p, err := pr.ReadPacket()
		if err != nil {
			return
		}
		switch int32(p.ID) {
		case server.IDPlayKeepAliveCB:
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
			continue
		case server.IDPlayChunkBatchDone:
			_ = ackChunkBatch(pw)
			continue
		case server.IDPlaySyncPos:
			r := protocol.NewPacketReader(p.Data)
			id, _ := r.VarInt()
			var b protocol.Buffer
			b.VarInt(id)
			_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
			continue
		}
		if keep {
			tp.mu.Lock()
			tp.pkts = append(tp.pkts, tappedPacket{at: time.Now(), p: p})
			tp.mu.Unlock()
		}
	}
}

// drain moves the queued packets into lg (nil discards them).
func (tp *hostileTap) drain(lg *hostileLog) {
	tp.mu.Lock()
	q := tp.pkts
	tp.pkts = nil
	tp.mu.Unlock()
	if lg == nil {
		return
	}
	for _, tpk := range q {
		lg.record(tpk.at, tpk.p)
	}
}

// hostileWatch drains the tap into lg for d.
func hostileWatch(t *testing.T, tp *hostileTap, lg *hostileLog, d time.Duration) {
	t.Helper()
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		tp.drain(lg)
		time.Sleep(20 * time.Millisecond)
	}
	tp.drain(lg)
}

// hostileWatchUntil drains until done(lg) or the deadline.
func hostileWatchUntil(t *testing.T, tp *hostileTap, lg *hostileLog, d time.Duration, done func(*hostileLog) bool) bool {
	t.Helper()
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		tp.drain(lg)
		if done(lg) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	tp.drain(lg)
	return done(lg)
}

func (lg *hostileLog) record(now time.Time, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	switch int32(p.ID) {
	case server.IDPlaySpawnEntity:
		eid, _ := r.VarInt()
		r.UUID()
		ty, _ := r.VarInt()
		n, _ := server.EntityTypeName(ty)
		x, _ := r.Double()
		y, _ := r.Double()
		z, _ := r.Double()
		lg.types[eid] = n
		lg.pos[eid] = [3]float64{x, y, z}
		lg.maxY[eid] = y
		lg.events = append(lg.events, hostileEv{at: now, kind: "add", eid: eid, typ: n, xyz: [3]float64{x, y, z}, ref: lg.pos[lg.refEid]})
	case 0x4D: // remove_entities
		n, _ := r.VarInt()
		for i := int32(0); i < n; i++ {
			id, _ := r.VarInt()
			lg.events = append(lg.events, hostileEv{at: now, kind: "remove", eid: id})
		}
	case server.IDPlaySetHealth:
		hp, _ := r.Float()
		lg.events = append(lg.events, hostileEv{at: now, kind: "health", fval: float64(hp)})
	case server.IDPlayEntityEvent:
		eid, _ := r.Int()
		st, _ := r.Byte()
		lg.events = append(lg.events, hostileEv{at: now, kind: "event", eid: eid, idx: int(st)})
	case server.IDPlayDamageEvent:
		eid, _ := r.VarInt()
		ty, _ := r.VarInt()
		cause, _ := r.VarInt()
		lg.events = append(lg.events, hostileEv{at: now, kind: "damage", eid: eid, idx: int(ty), val: int64(cause)})
	case 0x75: // sound
		sid, err := r.VarInt()
		if err != nil {
			return
		}
		_, _ = r.VarInt()
		x, _ := r.Int()
		y, _ := r.Int()
		z, _ := r.Int()
		lg.events = append(lg.events, hostileEv{at: now, kind: "sound", idx: int(sid),
			xyz: [3]float64{float64(x) / 8, float64(y) / 8, float64(z) / 8}})
	case 0x08: // block_update
		x, y, z, err := r.Position()
		if err != nil {
			return
		}
		st, _ := r.VarInt()
		lg.events = append(lg.events, hostileEv{at: now, kind: "block", bpos: [3]int32{x, y, z}, block: st})
	case 0x54: // section_blocks_update — several writes in one section
		sp, err := r.Long()
		if err != nil {
			return
		}
		sx := int32(sp >> 42)
		sy := int32(sp << 44 >> 44)
		sz := int32(sp << 22 >> 42)
		n, _ := r.VarInt()
		for i := int32(0); i < n; i++ {
			v, err := r.VarLong()
			if err != nil {
				return
			}
			st := int32(v >> 12)
			lx, lz, ly := int32(v>>8&15), int32(v>>4&15), int32(v&15)
			lg.events = append(lg.events, hostileEv{at: now, kind: "block",
				bpos: [3]int32{sx*16 + lx, sy*16 + ly, sz*16 + lz}, block: st})
		}
	case 0x65: // set_entity_motion
		eid, err := r.VarInt()
		if err != nil {
			return
		}
		vx, vy, vz, _ := r.ReadLpVec3()
		lg.events = append(lg.events, hostileEv{at: now, kind: "motion", eid: eid, xyz: [3]float64{vx, vy, vz}})
	case 53, 54: // move_entity_pos(_rot)
		eid, _ := r.VarInt()
		dx, _ := r.Short()
		dy, _ := r.Short()
		dz, _ := r.Short()
		lp := lg.pos[eid]
		lp[0] += float64(dx) / 4096
		lp[1] += float64(dy) / 4096
		lp[2] += float64(dz) / 4096
		lg.pos[eid] = lp
		if lp[1] > lg.maxY[eid] {
			lg.maxY[eid] = lp[1]
		}
	case 35, 125: // entity_position_sync / teleport_entity
		eid, _ := r.VarInt()
		x, _ := r.Double()
		y, _ := r.Double()
		z, _ := r.Double()
		lg.pos[eid] = [3]float64{x, y, z}
		if y > lg.maxY[eid] {
			lg.maxY[eid] = y
		}
	case server.IDPlayEntityMetadata:
		eid, _ := r.VarInt()
		for {
			idx, err := r.Byte()
			if err != nil || idx == 0xFF {
				return
			}
			ser, err := r.VarInt()
			if err != nil {
				return
			}
			ev := hostileEv{at: now, kind: "meta", eid: eid, idx: int(idx), ser: ser}
			switch ser {
			case 0:
				b, _ := r.Byte()
				ev.val = int64(b)
			case 1, 12, 14, 15, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31:
				vv, _ := r.VarInt()
				ev.val = int64(vv)
			case 2:
				vv, _ := r.VarLong()
				ev.val = vv
			case 3:
				f, _ := r.Float()
				ev.fval = float64(f)
			case 8:
				b, _ := r.Bool()
				if b {
					ev.val = 1
				}
			case 13:
				present, _ := r.Bool()
				if present {
					_, _ = r.Read(16)
					ev.val = 1
				}
			default:
				// Unparsed serializer: the rest of the packet is opaque.
				return
			}
			lg.events = append(lg.events, ev)
		}
	}
}

// adds returns the add events of a type, in order.
func (lg *hostileLog) adds(typ string) []hostileEv {
	var out []hostileEv
	for _, e := range lg.events {
		if e.kind == "add" && e.typ == typ {
			out = append(out, e)
		}
	}
	return out
}

// metas returns the meta events for eid at idx, in order.
func (lg *hostileLog) metas(eid int32, idx int) []hostileEv {
	var out []hostileEv
	for _, e := range lg.events {
		if e.kind == "meta" && e.eid == eid && e.idx == idx {
			out = append(out, e)
		}
	}
	return out
}

// sounds returns the sound events carrying the named sound.
func (lg *hostileLog) sounds(name string) []hostileEv {
	id, ok := server.SoundEventID(name)
	if !ok {
		return nil
	}
	var out []hostileEv
	for _, e := range lg.events {
		if e.kind == "sound" && e.idx == int(id)+1 {
			out = append(out, e)
		}
	}
	return out
}

// entityEvents returns the entity_event statuses for eid.
func (lg *hostileLog) entityEvents(eid int32, status int) []hostileEv {
	var out []hostileEv
	for _, e := range lg.events {
		if e.kind == "event" && e.eid == eid && e.idx == status {
			out = append(out, e)
		}
	}
	return out
}

// damages returns damage events on victim.
func (lg *hostileLog) damages(victim int32) []hostileEv {
	var out []hostileEv
	for _, e := range lg.events {
		if e.kind == "damage" && e.eid == victim {
			out = append(out, e)
		}
	}
	return out
}

// firstEid is the eid of the first add of typ (0 when none).
func (lg *hostileLog) firstEid(typ string) int32 {
	for _, e := range lg.events {
		if e.kind == "add" && e.typ == typ {
			return e.eid
		}
	}
	return 0
}

// selfEID reads the joined player's entity id from a fresh
// /data get on BOATA's own record is not available on the wire, so
// tests match damage events on any non-mob eid instead; this helper
// returns the eids that are not adds of a known mob type.
func (lg *hostileLog) nonMobDamage(mobs ...string) []hostileEv {
	skip := map[int32]bool{}
	for eid, ty := range lg.types {
		for _, m := range mobs {
			if strings.EqualFold(ty, m) {
				skip[eid] = true
			}
		}
	}
	var out []hostileEv
	for _, e := range lg.events {
		if e.kind == "damage" && !skip[e.eid] {
			if _, known := lg.types[e.eid]; !known {
				out = append(out, e)
			}
		}
	}
	return out
}

// healthDrops lists each set_health that fell below the previous
// reading (starting from start), as the drop size.
func (lg *hostileLog) healthDrops(start float64) []float64 {
	var out []float64
	prev := start
	for _, e := range lg.events {
		if e.kind != "health" {
			continue
		}
		if e.fval < prev-1e-4 {
			out = append(out, prev-e.fval)
		}
		prev = e.fval
	}
	return out
}

func hostileDist2(a, b [3]float64) float64 {
	dx, dy, dz := a[0]-b[0], a[1]-b[1], a[2]-b[2]
	return dx*dx + dy*dy + dz*dz
}

func approxEq(a, b, tol float64) bool { return math.Abs(a-b) <= tol }
