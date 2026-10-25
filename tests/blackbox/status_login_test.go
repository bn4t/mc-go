package blackbox

import (
	"encoding/json"
	"net"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

type statusSampleJSON struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

type statusJSON struct {
	Version struct {
		Name     string `json:"name"`
		Protocol int    `json:"protocol"`
	} `json:"version"`
	Players struct {
		Max    int                `json:"max"`
		Online int                `json:"online"`
		Sample []statusSampleJSON `json:"sample"`
	} `json:"players"`
	Description any `json:"description"`
}

func fetchStatus(t *testing.T, addr string) statusJSON {
	t.Helper()
	c, pr, pw := client(t, addr)
	defer c.Close()
	if err := handshake(pw, server.NextStatus); err != nil {
		t.Fatal(err)
	}
	if err := pw.WritePacket(server.IDStatusRequest, nil); err != nil {
		t.Fatal(err)
	}
	resp, err := pr.ReadPacket()
	if err != nil || resp.ID != server.IDStatusResponse {
		t.Fatalf("%s: status resp = %#x, err %v", addr, resp.ID, err)
	}
	js, err := protocol.NewPacketReader(resp.Data).String()
	if err != nil {
		t.Fatalf("%s: status string: %v", addr, err)
	}
	var v statusJSON
	if err := json.Unmarshal([]byte(js), &v); err != nil {
		t.Fatalf("%s: bad status json %q: %v", addr, js, err)
	}
	return v
}

func TestStatusParity(t *testing.T) {
	tg := requireBlackbox(t)
	defer tg.cleanup()
	v := fetchStatus(t, tg.vanilla)
	m := fetchStatus(t, tg.mcgo)
	if v.Version.Protocol != m.Version.Protocol {
		t.Fatalf("protocol: vanilla=%d mc-go=%d", v.Version.Protocol, m.Version.Protocol)
	}
	if v.Version.Name != m.Version.Name {
		t.Fatalf("version name: vanilla=%q mc-go=%q", v.Version.Name, m.Version.Name)
	}
	if v.Players.Max != m.Players.Max {
		t.Fatalf("max players: vanilla=%d mc-go=%d", v.Players.Max, m.Players.Max)
	}
	if m.Description == nil {
		t.Fatal("mc-go status has no description")
	}
}

func TestPingEchoParity(t *testing.T) {
	tg := requireBlackbox(t)
	defer tg.cleanup()
	const payload int64 = 0x123456789ABCDEF
	for _, addr := range []string{tg.vanilla, tg.mcgo} {
		c, pr, pw := client(t, addr)
		func() {
			defer c.Close()
			if err := handshake(pw, server.NextStatus); err != nil {
				t.Fatal(err)
			}
			var b protocol.Buffer
			b.Long(payload)
			if err := pw.WritePacket(server.IDPingRequest, b.Bytes()); err != nil {
				t.Fatal(err)
			}
			pong, err := pr.ReadPacket()
			if err != nil || pong.ID != server.IDPongResponse {
				t.Fatalf("%s: pong = %#x, err %v", addr, pong.ID, err)
			}
			got, err := protocol.NewPacketReader(pong.Data).Long()
			if err != nil || got != payload {
				t.Fatalf("%s: pong payload = %d, err %v", addr, got, err)
			}
		}()
	}
}

// readLoginSuccess skips an optional Set Compression (0x03) before the
// Login Success (0x02) vanilla sends when compression is enabled.
func readLoginSuccess(t *testing.T, pr *protocol.Reader, pw *protocol.Writer, addr string) (uuid [16]byte, name string) {
	t.Helper()
	for {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("%s: login read: %v", addr, err)
		}
		if p.ID == 0x03 { // Set Compression: enable framing both ways
			th, err := protocol.NewPacketReader(p.Data).VarInt()
			if err != nil {
				t.Fatalf("%s: compression threshold: %v", addr, err)
			}
			pr.SetThreshold(th)
			pw.SetThreshold(th)
			continue
		}
		if p.ID != server.IDLoginSuccess {
			t.Fatalf("%s: login resp = %#x, want success", addr, p.ID)
		}
		r := protocol.NewPacketReader(p.Data)
		uuid, err = r.UUID()
		if err != nil {
			t.Fatalf("%s: success uuid: %v", addr, err)
		}
		name, err = r.String()
		if err != nil {
			t.Fatalf("%s: success name: %v", addr, err)
		}
		props, err := r.VarInt()
		if err != nil {
			t.Fatalf("%s: success props: %v", addr, err)
		}
		for i := int32(0); i < props; i++ {
			if _, err = r.String(); err != nil {
				t.Fatalf("%s: prop name: %v", addr, err)
			}
			if _, err = r.String(); err != nil {
				t.Fatalf("%s: prop value: %v", addr, err)
			}
			signed, err := r.Bool()
			if err != nil {
				t.Fatalf("%s: prop signed: %v", addr, err)
			}
			if signed {
				if _, err = r.String(); err != nil {
					t.Fatalf("%s: prop sig: %v", addr, err)
				}
			}
		}
		// 26.2 trails the properties with a session-id UUID; without
		// it the real client kicks on decode.
		if _, err := r.UUID(); err != nil {
			t.Fatalf("%s: missing trailing session id: %v", addr, err)
		}
		if r.Remaining() != 0 {
			t.Fatalf("%s: success trailing bytes: %d", addr, r.Remaining())
		}
		return uuid, name
	}
}

func loginStart(t *testing.T, addr, name string) (c net.Conn, pr *protocol.Reader, pw *protocol.Writer) {
	t.Helper()
	cc, prr, pww := client(t, addr)
	if err := handshake(pww, server.NextLogin); err != nil {
		t.Fatal(err)
	}
	var b protocol.Buffer
	b.String(name)
	var zero [16]byte
	b.Raw(zero[:])
	if err := pww.WritePacket(server.IDLoginStart, b.Bytes()); err != nil {
		t.Fatal(err)
	}
	return cc, prr, pww
}

func TestLoginSuccessUUIDParity(t *testing.T) {
	tg := requireBlackbox(t)
	defer tg.cleanup()
	const name = "BlackBox"
	c1, pr1, pw1 := loginStart(t, tg.vanilla, name)
	defer c1.Close()
	vUUID, vName := readLoginSuccess(t, pr1, pw1, "vanilla")

	c2, pr2, pw2 := loginStart(t, tg.mcgo, name)
	defer c2.Close()
	mUUID, mName := readLoginSuccess(t, pr2, pw2, "mc-go")

	if vName != name || mName != name {
		t.Fatalf("names: vanilla=%q mc-go=%q", vName, mName)
	}
	if vUUID != mUUID {
		t.Fatalf("offline UUID mismatch:\nvanilla=%x\nmc-go  =%x", vUUID, mUUID)
	}
}

// TestStatusSampleParity proves the status player sample lists online
// players with dashed UUIDs on both servers. The player is fully
// loaded (teleport confirmed, terrain reported) before probing.
func TestStatusSampleParity(t *testing.T) {
	tg := requireBlackbox(t)
	defer tg.cleanup()

	probe := func(tag, addr string) []statusSampleJSON {
		// A real client sends Client Information during configuration;
		// vanilla keeps players anonymous in the sample without it.
		c, pr, pw := loginStart(t, addr, "Sampled")
		defer c.Close()
		readLoginSuccess(t, pr, pw, tag)
		if err := pw.WritePacket(server.IDLoginAcknowledged, nil); err != nil {
			t.Fatal(err)
		}
		var info protocol.Buffer
		info.String("en_US")
		info.Byte(8)
		info.VarInt(0) // chat mode: enabled
		info.Bool(true)
		info.Byte(0x7F)
		info.VarInt(1)
		info.Bool(false) // text filtering
		info.Bool(true)  // allow server listings
		info.VarInt(0)   // particle status: all
		if err := pw.WritePacket(server.IDConfigClientInformation, info.Bytes()); err != nil {
			t.Fatal(err)
		}
		sendKnownPacks(t, pw, "99.9-test")
		for i := 0; i < 64; i++ {
			p, err := pr.ReadPacket()
			if err != nil {
				t.Fatalf("%s config: %v", tag, err)
			}
			if p.ID == server.IDConfigFinish {
				break
			}
		}
		if err := pw.WritePacket(server.IDConfigFinishAck, nil); err != nil {
			t.Fatal(err)
		}
		drainToJoinBlackbox(t, tag, pr)
		var tp protocol.Buffer
		tp.VarInt(1)
		_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, tp.Bytes())
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		// Let the join burst settle so the server counts the player as
		// online for the status sample.
		drainToQuietBlackbox(t, c, pr, 500*time.Millisecond)
		v := fetchStatus(t, addr)
		return v.Players.Sample
	}

	vSample := probe("vanilla", tg.vanilla)
	mSample := probe("mc-go", tg.mcgo)
	t.Logf("vanilla sample = %+v", vSample)
	t.Logf("mc-go sample = %+v", mSample)

	find := func(sample []statusSampleJSON) (string, bool) {
		for _, e := range sample {
			if e.Name == "Sampled" {
				return e.ID, true
			}
		}
		return "", false
	}
	vID, vOK := find(vSample)
	mID, mOK := find(mSample)
	if !vOK || !mOK {
		t.Fatalf("sample missing player: vanilla=%v mc-go=%v", vSample, mSample)
	}
	// Offline-mode UUID is derived from the name, so both agree, and it
	// must be the dashed rendering (36 chars, dashes at 8/13/18/23).
	if vID != mID {
		t.Fatalf("uuid mismatch: vanilla %s mc-go %s", vID, mID)
	}
	if len(mID) != 36 || mID[8] != '-' || mID[13] != '-' || mID[18] != '-' || mID[23] != '-' {
		t.Fatalf("uuid not dashed: %q", mID)
	}
}
