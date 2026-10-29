package externalavatar

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"testing"
	"time"

	"mc-go/api/player"
	"mc-go/conformance"
)

// Independent Java wire facts are pinned by the existing official775/776
// captures/reports in conformance/testdata/mixed-profile-facts.json.
// No production encoder/constants are
// imported. A stationary player spawn has a zero LpVec3 and zero spawn data.
type javaPeer struct {
	client  *conformance.PlayClient
	initial []conformance.Packet
	next    int
	kind    int32
}

func openJava(t *testing.T, ctx context.Context, addr string, n int32) *javaPeer {
	t.Helper()
	version := "26.1"
	kind := int32(155)
	if n == 776 {
		version = "26.2"
		kind = 156
	}
	c, e := conformance.OpenPlay(ctx, conformance.Config{Endpoint: addr, Protocol: n, Version: version, Username: fmt.Sprintf("Java%d", n), Timeout: 60 * time.Second})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { c.Close() })
	return &javaPeer{client: c, initial: c.InitialPackets(), kind: kind}
}
func vi(r *bytes.Reader) (int32, error) {
	var n int32
	for i := 0; i < 5; i++ {
		b, e := r.ReadByte()
		if e != nil {
			return 0, e
		}
		n |= int32(b&127) << (7 * i)
		if b < 128 {
			return n, nil
		}
	}
	return 0, fmt.Errorf("overlong VarInt")
}
func text(r *bytes.Reader) (string, error) {
	n, e := vi(r)
	if e != nil || n < 0 || int64(n) > int64(r.Len()) {
		return "", fmt.Errorf("invalid string length")
	}
	b := make([]byte, n)
	_, e = io.ReadFull(r, b)
	return string(b), e
}
func flag(r *bytes.Reader) (bool, error) {
	b, e := r.ReadByte()
	if e != nil || b > 1 {
		return false, fmt.Errorf("invalid bool")
	}
	return b == 1, nil
}
func (j *javaPeer) until(t *testing.T, match func(conformance.Packet) bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		var p conformance.Packet
		if j.next < len(j.initial) {
			p = j.initial[j.next]
			j.next++
		} else {
			var e error
			p, e = j.client.ReadPacket(ctx)
			if e != nil {
				t.Fatalf("Java%d observation: %v", j.client.Protocol(), e)
			}
		}
		if match(p) {
			return
		}
	}
}
func infoNames(body []byte) (map[[16]byte]string, error) {
	r := bytes.NewReader(body)
	mask, e := r.ReadByte()
	if e != nil {
		return nil, e
	}
	n, e := vi(r)
	if e != nil || n < 0 || n > 10000 {
		return nil, fmt.Errorf("bad info count")
	}
	out := map[[16]byte]string{}
	for range n {
		var id [16]byte
		if _, e = io.ReadFull(r, id[:]); e != nil {
			return nil, e
		}
		if mask&1 != 0 {
			name, e := text(r)
			if e != nil {
				return nil, e
			}
			out[id] = name
			props, e := vi(r)
			if e != nil || props < 0 || props > 1024 {
				return nil, fmt.Errorf("bad properties")
			}
			for range props {
				if _, e = text(r); e != nil {
					return nil, e
				}
				if _, e = text(r); e != nil {
					return nil, e
				}
				signed, e := flag(r)
				if e != nil {
					return nil, e
				}
				if signed {
					if _, e = text(r); e != nil {
						return nil, e
					}
				}
			}
		}
		if mask&2 != 0 {
			present, e := flag(r)
			if e != nil {
				return nil, e
			}
			if present {
				return nil, fmt.Errorf("unexpected signed-chat data in offline fixture")
			}
		}
		if mask&4 != 0 {
			if _, e = vi(r); e != nil {
				return nil, e
			}
		}
		if mask&8 != 0 {
			if _, e = flag(r); e != nil {
				return nil, e
			}
		}
		if mask&16 != 0 {
			if _, e = vi(r); e != nil {
				return nil, e
			}
		}
		if mask&32 != 0 {
			present, e := flag(r)
			if e != nil {
				return nil, e
			}
			if present {
				return nil, fmt.Errorf("unexpected display override in fixture")
			}
		}
		if mask&64 != 0 {
			if _, e = vi(r); e != nil {
				return nil, e
			}
		}
		if mask&128 != 0 {
			if _, e = flag(r); e != nil {
				return nil, e
			}
		}
	}
	if r.Len() != 0 {
		return nil, fmt.Errorf("trailing player-info data")
	}
	return out, nil
}
func (j *javaPeer) presence(t *testing.T, want player.State) {
	t.Helper()
	info, spawn := false, false
	j.until(t, func(p conformance.Packet) bool {
		switch p.ID {
		case 0x46:
			names, e := infoNames(p.Data)
			if e != nil {
				t.Fatal(e)
			}
			if name, ok := names[want.Profile.UUID]; ok {
				if name != want.Profile.Name {
					t.Fatalf("wire name%q want%q", name, want.Profile.Name)
				}
				info = true
			}
		case 1:
			if j.spawned(t, p, want) {
				spawn = true
			}
		}
		return info && spawn
	})
}

// spawned validates one target avatar's complete add-entity packet. It is
// also the readiness marker after a respawn clears a Java viewer's tracker.
func (j *javaPeer) spawned(t *testing.T, p conformance.Packet, want player.State) bool {
	t.Helper()
	if p.ID != 1 {
		return false
	}
	r := bytes.NewReader(p.Data)
	eid, e := vi(r)
	if e != nil {
		t.Fatal(e)
	}
	var id [16]byte
	if _, e = io.ReadFull(r, id[:]); e != nil {
		t.Fatal(e)
	}
	kind, e := vi(r)
	if e != nil {
		t.Fatal(e)
	}
	if id != want.Profile.UUID {
		return false
	}
	if eid != want.EntityID || kind != j.kind {
		t.Fatalf("Java%d avatar entity/type%d/%d want%d/%d", j.client.Protocol(), eid, kind, want.EntityID, j.kind)
	}
	var xyz [3]float64
	for i := range xyz {
		if e = binary.Read(r, binary.BigEndian, &xyz[i]); e != nil {
			t.Fatal(e)
		}
	}
	if xyz != [3]float64{want.Pose.X, want.Pose.Y, want.Pose.Z} {
		t.Fatalf("spawn pose%v want%+v", xyz, want.Pose)
	}
	velocity, e := r.ReadByte()
	if e != nil || velocity != 0 {
		t.Fatal("stationary spawn velocity")
	}
	for range 3 {
		if _, e = r.ReadByte(); e != nil {
			t.Fatal(e)
		}
	}
	data, e := vi(r)
	if e != nil || data != 0 || r.Len() != 0 {
		t.Fatal("spawn body boundary")
	}
	return true
}

func (j *javaPeer) repaired(t *testing.T, states ...player.State) {
	t.Helper()
	pending := make(map[player.ID]player.State, len(states))
	for _, state := range states {
		pending[state.ID] = state
	}
	j.until(t, func(p conformance.Packet) bool {
		if p.ID != 1 {
			return false
		}
		for id, state := range pending {
			if j.spawned(t, p, state) {
				delete(pending, id)
			}
		}
		return len(pending) == 0
	})
}

// repairedMovement observes a fresh pairing and the committed movement. The
// tracker may have captured the spawn before that movement, in which case a
// subsequent native delta must deliver it before this barrier completes.
func (j *javaPeer) repairedMovement(t *testing.T, before, after player.State) {
	t.Helper()
	oldSpawn := false
	j.until(t, func(p conformance.Packet) bool {
		if p.ID != 1 {
			return false
		}
		r := bytes.NewReader(p.Data)
		id, err := vi(r)
		if err != nil {
			t.Fatal(err)
		}
		if id != after.EntityID {
			return false
		}
		if _, err = r.Seek(16, io.SeekCurrent); err != nil {
			t.Fatal(err)
		}
		if _, err = vi(r); err != nil {
			t.Fatal(err)
		}
		var xyz [3]float64
		for i := range xyz {
			if err = binary.Read(r, binary.BigEndian, &xyz[i]); err != nil {
				t.Fatal(err)
			}
		}
		want := after
		if xyz == [3]float64{before.Pose.X, before.Pose.Y, before.Pose.Z} {
			want = before
			oldSpawn = true
		}
		return j.spawned(t, p, want)
	})
	if oldSpawn {
		j.moved(t, after)
	}
}

func (j *javaPeer) marker(t *testing.T, token string) {
	t.Helper()
	j.until(t, func(p conformance.Packet) bool { return p.ID == 0x79 && bytes.Contains(p.Data, []byte(token)) })
}

func nativeMove(body []byte) (int32, player.Pose, error) {
	r := bytes.NewReader(body)
	id, e := vi(r)
	if e != nil {
		return 0, player.Pose{}, e
	}
	var pos, velocity [3]float64
	for i := range pos {
		if e = binary.Read(r, binary.BigEndian, &pos[i]); e != nil {
			return 0, player.Pose{}, e
		}
	}
	for i := range velocity {
		if e = binary.Read(r, binary.BigEndian, &velocity[i]); e != nil {
			return 0, player.Pose{}, e
		}
	}
	var yaw, pitch float32
	if e = binary.Read(r, binary.BigEndian, &yaw); e != nil {
		return 0, player.Pose{}, e
	}
	if e = binary.Read(r, binary.BigEndian, &pitch); e != nil {
		return 0, player.Pose{}, e
	}
	ground, e := flag(r)
	if e != nil || r.Len() != 0 {
		return 0, player.Pose{}, fmt.Errorf("move body boundary")
	}
	return id, player.Pose{X: pos[0], Y: pos[1], Z: pos[2], Yaw: yaw, Pitch: pitch, OnGround: ground}, nil
}
func (j *javaPeer) moved(t *testing.T, state player.State) {
	t.Helper()
	j.until(t, func(p conformance.Packet) bool {
		if p.ID != 0x23 {
			return false
		}
		id, pose, e := nativeMove(p.Data)
		if e != nil {
			t.Fatal(e)
		}
		return id == state.EntityID && pose == state.Pose
	})
}
func (j *javaPeer) removed(t *testing.T, state player.State) {
	t.Helper()
	entity, profile := false, false
	j.until(t, func(p conformance.Packet) bool {
		if p.ID != 0x4d && p.ID != 0x45 {
			return false
		}
		r := bytes.NewReader(p.Data)
		n, e := vi(r)
		if e != nil || n < 0 || n > 10000 {
			t.Fatal("invalid remove count")
		}
		for range n {
			if p.ID == 0x4d {
				id, e := vi(r)
				if e != nil {
					t.Fatal(e)
				}
				entity = entity || id == state.EntityID
			} else {
				var id [16]byte
				if _, e := io.ReadFull(r, id[:]); e != nil {
					t.Fatal(e)
				}
				profile = profile || id == state.Profile.UUID
			}
		}
		if r.Len() != 0 {
			t.Fatal("remove boundary")
		}
		return entity && profile
	})
}
func (j *javaPeer) fence(t *testing.T, token string, entity int32, forbidden player.Pose) {
	t.Helper()
	j.until(t, func(p conformance.Packet) bool {
		if p.ID == 0x23 {
			id, pose, e := nativeMove(p.Data)
			if e != nil {
				t.Fatal(e)
			}
			if id == entity && pose == forbidden {
				t.Fatal("rejected movement leaked to Java viewer")
			}
		}
		return p.ID == 0x79 && bytes.Contains(p.Data, []byte(token))
	})
}
func finite(p player.Pose) bool { return !math.IsNaN(p.X) && !math.IsNaN(p.Y) && !math.IsNaN(p.Z) }

func (j *javaPeer) synchronized(t *testing.T, want player.Pose) {
	t.Helper()
	j.until(t, func(p conformance.Packet) bool {
		if p.ID != 0x48 {
			return false
		}
		r := bytes.NewReader(p.Data)
		if _, e := vi(r); e != nil {
			t.Fatal(e)
		}
		var xyz [3]float64
		for i := range xyz {
			if e := binary.Read(r, binary.BigEndian, &xyz[i]); e != nil {
				t.Fatal(e)
			}
		}
		if xyz != [3]float64{want.X, want.Y, want.Z} {
			return false
		}
		if r.Len() != 36 {
			t.Fatal("position synchronization boundary")
		}
		return true
	})
}
func (j *javaPeer) respawn(t *testing.T, want string) {
	t.Helper()
	j.until(t, func(p conformance.Packet) bool {
		if p.ID != 0x52 {
			return false
		}
		r := bytes.NewReader(p.Data)
		typ, e := vi(r)
		if e != nil {
			t.Fatal(e)
		}
		dim, e := text(r)
		if e != nil {
			t.Fatal(e)
		}
		if dim != want {
			return false
		}
		names := j.client.RegistryEntries("minecraft:dimension_type")
		if typ < 0 || int(typ) >= len(names) || names[typ] != want {
			t.Fatal("respawn dimension registry mismatch")
		}
		if _, e = r.Seek(10, io.SeekCurrent); e != nil {
			t.Fatal(e)
		}
		for range 2 {
			if _, e = flag(r); e != nil {
				t.Fatal(e)
			}
		}
		death, e := flag(r)
		if e != nil {
			t.Fatal(e)
		}
		if death {
			if _, e = text(r); e != nil {
				t.Fatal(e)
			}
			if _, e = r.Seek(8, io.SeekCurrent); e != nil {
				t.Fatal(e)
			}
		}
		if _, e = vi(r); e != nil {
			t.Fatal(e)
		}
		if _, e = vi(r); e != nil {
			t.Fatal(e)
		}
		kept, e := r.ReadByte()
		if e != nil || kept&^byte(3) != 0 || r.Len() != 0 {
			t.Fatal("respawn body boundary")
		}
		return true
	})
}

func (j *javaPeer) mode(t *testing.T, want int32) {
	t.Helper()
	j.until(t, func(p conformance.Packet) bool {
		if p.ID != 0x26 {
			return false
		}
		if len(p.Data) != 5 {
			t.Fatal("game event body boundary")
		}
		return p.Data[0] == 3 && math.Float32frombits(binary.BigEndian.Uint32(p.Data[1:])) == float32(want)
	})
}
