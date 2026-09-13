package blackbox

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// Update Time parity: the once-a-second clockless tick and the /time
// set broadcast, measured against a live 26.2 server. The periodic
// sync is the world age with no clock entries (9 bytes, advancing 20
// ticks a second); /time set fans out the age with the single day
// clock (id 0) at the new day time.

// TestPeriodicTimeParity requires two consecutive periodic syncs on
// each server to be clockless 9-byte packets about a second and 20
// ticks apart.
func TestPeriodicTimeParity(t *testing.T) {
	tg := requireBlackbox(t)
	defer tg.cleanup()
	for _, tc := range []struct{ tag, addr string }{
		{"vanilla", tg.vanilla},
		{"mc-go", tg.mcgo},
	} {
		c, pr, _ := joinPlayBlackbox(t, tc.addr, "TickWatcher")
		func() {
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(30 * time.Second))
			var ages []int64
			var dts []time.Duration
			start := time.Now()
			prev := start
			for i := 0; i < 400 && len(ages) < 2; i++ {
				p, err := pr.ReadPacket()
				if err != nil {
					t.Fatalf("%s: %v", tc.tag, err)
				}
				if p.ID != server.IDPlayUpdateTime || len(p.Data) != 9 {
					continue
				}
				r := protocol.NewPacketReader(p.Data)
				age, err := r.Long()
				if err != nil {
					t.Fatalf("%s: age: %v", tc.tag, err)
				}
				if n, err := r.VarInt(); err != nil || n != 0 {
					t.Fatalf("%s: periodic clocks %d, want none", tc.tag, n)
				}
				now := time.Now()
				ages = append(ages, age)
				dts = append(dts, now.Sub(prev))
				prev = now
			}
			if len(ages) < 2 {
				t.Fatalf("%s: only %d periodic syncs in %v", tc.tag, len(ages), time.Since(start))
			}
			if d := ages[1] - ages[0]; d < 18 || d > 22 {
				t.Fatalf("%s: age advanced %d, want ~20", tc.tag, d)
			}
			if dt := dts[1]; dt < 800*time.Millisecond || dt > 1500*time.Millisecond {
				t.Fatalf("%s: sync gap %v, want ~1s", tc.tag, dt)
			}
		}()
	}
}

// TestTimeSetBroadcastParity runs /time set on both servers and
// requires the broadcast to carry the single day clock at the new
// time.
func TestTimeSetBroadcastParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("ClockSetter"),
	}, nil)
	defer tg.cleanup()
	for _, tc := range []struct{ tag, addr string }{
		{"vanilla", tg.vanilla},
		{"mc-go", tg.mcgo},
	} {
		c, pr, pw := joinPlayBlackbox(t, tc.addr, "ClockSetter")
		func() {
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(30 * time.Second))
			time.Sleep(2 * time.Second)
			// Drain clocked join sync and periodic ticks so the next
			// Update Time is the set broadcast.
			_ = c.SetDeadline(time.Now().Add(3 * time.Second))
			for {
				p, err := pr.ReadPacket()
				if err != nil || p.ID != server.IDPlayUpdateTime {
					break
				}
			}
			_ = c.SetDeadline(time.Now().Add(30 * time.Second))
			sendCommand(t, pw, "time set 6000")
			for i := 0; i < 60; i++ {
				p, err := pr.ReadPacket()
				if err != nil {
					t.Fatalf("%s: %v", tc.tag, err)
				}
				if p.ID != server.IDPlayUpdateTime || len(p.Data) != 20 {
					continue
				}
				r := protocol.NewPacketReader(p.Data)
				r.Long()
				n, _ := r.VarInt()
				id, _ := r.VarInt()
				tm, _ := r.VarLong()
				f, _ := r.Float()
				q, _ := r.Float()
				if n != 1 || id != 0 || tm != 6000 || f != 0 || q != 1 {
					t.Fatalf("%s: set broadcast (%d,%d,%d,%v,%v)", tc.tag, n, id, tm, f, q)
				}
				if r.Remaining() != 0 {
					t.Fatalf("%s: set trailing bytes: %d", tc.tag, r.Remaining())
				}
				return
			}
			t.Fatalf("%s: no set broadcast", tc.tag)
		}()
	}
}
