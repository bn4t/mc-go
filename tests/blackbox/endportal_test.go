//go:build blackbox

package blackbox

import (
	"fmt"
	"net"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/worldgen"
)

// endTrip is what one server showed of an end portal journey.
type endTrip struct {
	events  []string // level events and game events, in order
	arrival portalArrival
	chunks  map[[2]int32][]byte
	home    string // dimension after the credits' perform-respawn
}

// readEndEvents reads packets (answering keep-alives, batches and
// teleports) until stop returns true for one, recording level and game
// events.
func readEndEvents(t *testing.T, tag string, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, events *[]string, stop func(p protocol.Packet) bool) protocol.Packet {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Minute))
	defer c.SetReadDeadline(time.Time{})
	for {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("%s: %v", tag, err)
		}
		switch p.ID {
		case server.IDPlayKeepAliveCB:
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
		case server.IDPlayChunkBatchDone:
			_ = ackChunkBatch(pw)
		case server.IDPlayLevelEvent:
			r := protocol.NewPacketReader(p.Data)
			id, _ := r.Int()
			pos, _ := r.Long()
			data, _ := r.Int()
			global, _ := r.Bool()
			*events = append(*events, fmt.Sprintf("level %d at %d,%d,%d data %d global %v", id, pos>>38, pos<<52>>52, pos<<26>>38, data, global))
		case server.IDPlayGameEvent:
			r := protocol.NewPacketReader(p.Data)
			ev, _ := r.Byte()
			v, _ := r.Float()
			if ev == 4 {
				*events = append(*events, fmt.Sprintf("game event %d %v", ev, v))
			}
		}
		if stop(p) {
			return p
		}
	}
}

// TestEndPortalParity builds a frame ring in a generated overworld with
// one eye missing, places the last eye (level event 1503, the portal
// fill and global level event 1038), walks into the portal (arrival on
// the End platform: position, yaw 90, kept pitch, Relative flags; the
// platform and the arrival window block for block), then steps into an
// end portal in the End: the credits (game event 4) and, after perform
// respawn, home in the overworld.
func TestEndPortalParity(t *testing.T) {
	const seed = 12345
	ov := worldgen.StageOverrides(worldgen.Vanilla(), func(string) bool { return false }, true, true)
	props := map[string]string{
		"level-type": "minecraft:normal", "generator-settings": "{}",
		"level-seed": fmt.Sprint(seed), "generate-structures": "false",
	}
	files := stageDatapack(ov)
	files["ops.json"] = opsJSONFor("EndProbe")
	tg := requireBlackboxFiles(t, props, files, func(s *server.Server) {
		s.LevelType = server.LevelTypeNormal
		s.GenerateStructures = false
		s.WorldSeed = seed
		s.WorldgenOverlay = ov
	})
	defer tg.cleanup()
	defer func(d time.Duration) { clientDeadline = d }(clientDeadline)
	clientDeadline = 3 * time.Minute

	const cx, cy, cz = 104, 150, -28
	run := func(tag, addr string) endTrip {
		var tr endTrip
		c, pr, pw := joinPlayBlackbox(t, addr, "EndProbe")
		defer c.Close()
		collectJoin(t, tag, c, pr, pw)
		frame := func(x, z int, facing string, eye bool) {
			sendCommand(t, pw, fmt.Sprintf("setblock %d %d %d minecraft:end_portal_frame[facing=%s,eye=%v]", x, cy, z, facing, eye))
		}
		for k := -1; k <= 1; k++ {
			frame(cx+k, cz-2, "south", true)
			frame(cx+k, cz+2, "north", true)
			frame(cx-2, cz+k, "east", true)
			frame(cx+2, cz+k, "west", k != 1)
		}
		sendCommand(t, pw, "tp @s 104.5 151 -24.5 0 30")
		sendCommand(t, pw, "give @s minecraft:ender_eye")
		idleClient(t, tag, c, pr, pw, 2*time.Second)
		sendUseItemOn(t, pw, cx+2, cy, cz+1, 1, 1)
		readEndEvents(t, tag+" eye", c, pr, pw, &tr.events, func(p protocol.Packet) bool {
			return len(tr.events) > 0 && tr.events[len(tr.events)-1][:10] == "level 1038"
		})
		sendCommand(t, pw, fmt.Sprintf("tp @s %d.5 %d %d.5", cx, cy, cz))
		tr.arrival = readArrival(t, tag+" to end", c, pr, pw)
		tr.chunks, _ = collectJoin(t, tag+" end", c, pr, pw)
		sendCommand(t, pw, "setblock 100 49 0 minecraft:end_portal")
		readEndEvents(t, tag+" credits", c, pr, pw, &tr.events, func(p protocol.Packet) bool {
			return p.ID == server.IDPlayGameEvent && len(p.Data) > 0 && p.Data[0] == 4
		})
		var b protocol.Buffer
		b.VarInt(0)
		_ = pw.WritePacket(server.IDPlayClientStatusSB, b.Bytes())
		r := readRespawn(t, tag+" home", pr, pw)
		tr.home = r.dim
		return tr
	}
	v := run("vanilla", tg.vanilla)
	m := run("mc-go", tg.mcgo)
	if fmt.Sprint(v.events) != fmt.Sprint(m.events) {
		t.Errorf("events:\nvanilla %v\nmc-go   %v", v.events, m.events)
	}
	a, b := v.arrival, m.arrival
	if a.dim != b.dim || a.x != b.x || a.y != b.y || a.z != b.z || a.yaw != b.yaw || a.pitch != b.pitch || a.flags != b.flags {
		t.Errorf("End arrival: vanilla %+v mc-go %+v", stripChunks(a), stripChunks(b))
	}
	if v.home != m.home {
		t.Errorf("home after the credits: vanilla %s mc-go %s", v.home, m.home)
	}
	t.Logf("vanilla events %v, arrival %+v, home %s", v.events, stripChunks(a), v.home)
	compareChunkStreams(t, v.chunks, m.chunks, 16, true)
}

func stripChunks(a portalArrival) portalArrival {
	a.chunks = nil
	return a
}
