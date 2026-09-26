package blackbox

// TestItemSyncCadenceParity pins vanilla's per-entity-type
// ServerEntity.updateInterval on the wire: an item entity (interval
// 20) falls ~30 blocks, moving every tick, but its position only
// reaches trackers at the cadence — roughly every 20 ticks, never once
// per moved tick. The probe records the timestamp of every pos-family
// packet (entity_position_sync + the three move packets) naming the
// summoned item and compares the inter-emit interval, which pins the
// cadence without depending on which boundary packets the window
// happened to catch.

import (
	"net"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// fallCollect streams the window from before the summon so no fall
// time is lost: it remembers the first add_entity of the wanted type,
// then records the timestamp of every pos-family packet naming that
// id until the deadline.
func fallCollect(t *testing.T, tag string, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, wantType string, d time.Duration) []time.Duration {
	t.Helper()
	start := time.Now()
	var entID int32 = -1
	var times []time.Duration
	deadline := start.Add(d)
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
			case server.IDPlaySyncPos:
				if id, err := protocol.NewPacketReader(p.Data).VarInt(); err == nil {
					var b protocol.Buffer
					b.VarInt(id)
					_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
				}
			case server.IDPlaySpawnEntity:
				r := protocol.NewPacketReader(p.Data)
				id, _ := r.VarInt()
				if _, err := r.UUID(); err != nil {
					break
				}
				ty, _ := r.VarInt()
				if name, ok := server.EntityTypeName(ty); ok && name == wantType && entID < 0 {
					entID = id
				}
			default:
				if entID < 0 {
					break
				}
				id, err := protocol.NewPacketReader(p.Data).VarInt()
				if err != nil || id != entID {
					break
				}
				switch byte(p.ID) {
				case 0x23, 0x35, 0x36, 0x37: // position_sync, move_pos, pos_rot, rot
					times = append(times, time.Since(start))
				}
			}
		}
	}
	t.Logf("%s: entID=%d pos_times=%v", tag, entID, times)
	return times
}

// medianGap returns the median spacing between consecutive timestamps;
// a steady 20-tick emit cadence measures ~1000ms regardless of which
// window edges the packets fell on.
func medianGap(ts []time.Duration) (time.Duration, bool) {
	if len(ts) < 2 {
		return 0, false
	}
	gaps := make([]time.Duration, 0, len(ts)-1)
	for i := 1; i < len(ts); i++ {
		gaps = append(gaps, ts[i]-ts[i-1])
	}
	for i := 1; i < len(gaps); i++ {
		for j := i; j > 0 && gaps[j] < gaps[j-1]; j-- {
			gaps[j], gaps[j-1] = gaps[j-1], gaps[j]
		}
	}
	return gaps[len(gaps)/2], true
}

func TestItemSyncCadenceParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Trk"),
	}, nil)
	defer targets.cleanup()

	run := func(tag, addr string) []time.Duration {
		c, pr, pw := joinPlayBlackbox(t, addr, "Trk")
		defer c.Close()
		drainChunkWindow(t, tag, c, pr, pw, 300*time.Millisecond)
		time.Sleep(600 * time.Millisecond)
		done := make(chan []time.Duration, 1)
		go func() {
			done <- fallCollect(t, tag, c, pr, pw, "item", 2600*time.Millisecond)
		}()
		time.Sleep(150 * time.Millisecond)
		sendCommand(t, pw, "summon minecraft:item 8.5 -30 8.5 {Item:{id:\"minecraft:stone\",count:1}}")
		return <-done
	}

	want := run("vanilla", targets.vanilla)
	got := run("mc-go", targets.mcgo)
	wantGap, ok := medianGap(want)
	if !ok {
		t.Fatalf("vanilla: fewer than 2 item position packets during fall — probe broken")
	}
	gotGap, ok := medianGap(got)
	if !ok {
		t.Fatalf("mc-go: fewer than 2 item position packets during fall")
	}
	// Vanilla's gap is 20 game ticks (~1000ms); allow half an interval
	// of tick-scheduling jitter.
	if d := gotGap - wantGap; d < -300*time.Millisecond || d > 300*time.Millisecond {
		t.Fatalf("item sync interval: vanilla=%v mc-go=%v (want ~equal)", wantGap, gotGap)
	}
}
