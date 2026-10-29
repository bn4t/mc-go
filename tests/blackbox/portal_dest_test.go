//go:build blackbox

package blackbox

import (
	"fmt"
	"math"
	"net"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/worldgen"
)

// portalArrival is where a portal trip landed: the Synchronize Player
// Position after the Respawn, and the destination window.
type portalArrival struct {
	dim        string
	x, y, z    float64
	yaw, pitch float32
	flags      int32
	chunks     map[[2]int32][]byte
}

// readArrival reads past the Respawn to the first position sync.
func readArrival(t *testing.T, tag string, c net.Conn, pr *protocol.Reader, pw *protocol.Writer) portalArrival {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Minute))
	out := readRespawn(t, tag, pr, pw)
	var a portalArrival
	a.dim = out.dim
	for {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("%s arrival: %v", tag, err)
		}
		switch p.ID {
		case server.IDPlayKeepAliveCB:
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
		case server.IDPlaySyncPos:
			r := protocol.NewPacketReader(p.Data)
			id, _ := r.VarInt()
			a.x, _ = r.Double()
			a.y, _ = r.Double()
			a.z, _ = r.Double()
			r.Double()
			r.Double()
			r.Double()
			a.yaw, _ = r.Float()
			a.pitch, _ = r.Float()
			a.flags, _ = r.Int()
			var b protocol.Buffer
			b.VarInt(id)
			_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
			_ = c.SetReadDeadline(time.Time{})
			return a
		}
	}
}

// TestPortalDestinationParity walks through a nether portal in a
// generated world on both servers: the arrival point (scaled exit,
// relative place in the portal, yaw turn), the portal the destination
// builds (PortalForcer.createPortal's site search over the real nether
// terrain) and the arrival window, then walks back through the built
// portal and requires the trip home to find the original portal.
func TestPortalDestinationParity(t *testing.T) {
	const seed = 12345
	ov := worldgen.StageOverrides(worldgen.Vanilla(), func(string) bool { return false }, true, true)
	props := map[string]string{
		"level-type":          "minecraft:normal",
		"generator-settings":  "{}",
		"level-seed":          fmt.Sprint(seed),
		"generate-structures": "false",
	}
	files := stageDatapack(ov)
	files["ops.json"] = opsJSONFor("PortalProbe")
	tg := requireBlackboxFiles(t, props, files, func(s *server.Server) {
		s.LevelType = server.LevelTypeNormal
		s.WorldSeed = seed
		s.WorldgenOverlay = ov
	})
	defer tg.cleanup()
	defer func(d time.Duration) { clientDeadline = d }(clientDeadline)
	clientDeadline = 3 * time.Minute

	trip := func(tag, addr string) (out, back portalArrival) {
		c, pr, pw := joinPlayBlackbox(t, addr, "PortalProbe")
		defer c.Close()
		collectJoin(t, tag, c, pr, pw)
		// A 2x3 x-axis portal near spawn, entered off-centre facing 45.
		sendCommand(t, pw, "fill 100 140 -30 103 144 -30 obsidian")
		sendCommand(t, pw, "fill 101 141 -30 102 143 -30 nether_portal[axis=x]")
		sendCommand(t, pw, "tp @s 101.7 141 -29.5 45 10")
		out = readArrival(t, tag+" out", c, pr, pw)
		out.chunks, _ = collectJoin(t, tag+" nether", c, pr, pw)
		// Step back into the portal the nether built (the arrival
		// stands in it) once the re-entry cooldown has run: hop out
		// above it, wait, return.
		sendCommand(t, pw, fmt.Sprintf("tp @s %.6f %.6f %.6f", out.x, out.y+6, out.z))
		idleClient(t, tag, c, pr, pw, 3*time.Second)
		sendCommand(t, pw, fmt.Sprintf("tp @s %.6f %.6f %.6f", out.x, out.y, out.z))
		back = readArrival(t, tag+" back", c, pr, pw)
		return out, back
	}
	vo, vb := trip("vanilla", tg.vanilla)
	mo, mb := trip("mc-go", tg.mcgo)
	t.Logf("vanilla out %s %.4f %.4f %.4f yaw %.1f, back %s %.4f %.4f %.4f yaw %.1f", vo.dim, vo.x, vo.y, vo.z, vo.yaw, vb.dim, vb.x, vb.y, vb.z, vb.yaw)
	t.Logf("mc-go   out %s %.4f %.4f %.4f yaw %.1f, back %s %.4f %.4f %.4f yaw %.1f", mo.dim, mo.x, mo.y, mo.z, mo.yaw, mb.dim, mb.x, mb.y, mb.z, mb.yaw)
	same := func(what string, a, b portalArrival) {
		if a.dim != b.dim || math.Abs(a.x-b.x) > 1e-6 || math.Abs(a.y-b.y) > 1e-6 || math.Abs(a.z-b.z) > 1e-6 || a.yaw != b.yaw || a.pitch != b.pitch || a.flags != b.flags {
			t.Errorf("%s arrival: vanilla %s (%v %v %v) yaw %v pitch %v flags %#x; mc-go %s (%v %v %v) yaw %v pitch %v flags %#x",
				what, a.dim, a.x, a.y, a.z, a.yaw, a.pitch, a.flags, b.dim, b.x, b.y, b.z, b.yaw, b.pitch, b.flags)
		}
	}
	same("outbound", vo, mo)
	same("return", vb, mb)
	compareChunkStreams(t, vo.chunks, mo.chunks, 16, true)
}
