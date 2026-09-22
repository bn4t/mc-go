package server

import (
	"bytes"
	"testing"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

func TestSignBlockEntityLifecycle(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "Signer", 0.5, 0.5, 0.5)
	pos := world.Pos{X: 4, Y: 4, Z: 4}
	sign, _ := world.BlockState("oak_sign")

	s.setBlockAndBroadcast("Signer", pos, sign)
	if _, ok := s.signAt(s.dims[dimOverworld], pos); !ok {
		t.Fatal("sign block entity not created")
	}

	// A sign editor belongs to the player who opened it - the real server
	// answers an update_sign from anyone else with nothing at all - so
	// the test opens the editor the way a player does, standing in front
	// of the sign within reach.
	sess.setPos(4.5, 4.5, 3.5, 0, 0, true)
	s.handleUseItem(sess, useItemPkt(0, 4, 4, 4, 2, nil))
	if s.signEditorHolder(s.dims[dimOverworld], pos) != sess {
		t.Fatal("sign editor not opened by the use")
	}

	// Edit the front text.
	var b protocol.Buffer
	b.Position(int32(pos.X), int32(pos.Y), int32(pos.Z))
	b.Bool(true)
	for _, line := range []string{"hello", "sign", "", "world"} {
		b.String(line)
	}
	s.handleUpdateSign(sess, protocol.Packet{ID: IDPlayUpdateSignSB, Data: b.Bytes()})
	st, ok := s.signAt(s.dims[dimOverworld], pos)
	if !ok || st.Front != [4]string{"hello", "sign", "", "world"} {
		t.Fatalf("sign text = %+v", st)
	}

	// Replace with stone: the block entity is retired.
	stone, _ := world.BlockState("stone")
	s.setBlockAndBroadcast("Signer", pos, stone)
	if _, ok := s.signAt(s.dims[dimOverworld], pos); ok {
		t.Fatal("sign block entity survived replacement")
	}
}

func TestSignNBTShape(t *testing.T) {
	raw := signNBT(signText{Front: [4]string{"a", "b", "c", "d"}})
	root, err := nbt.ReadUnnamed(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("sign nbt: %v", err)
	}
	m, ok := root.(map[string]any)
	if !ok {
		t.Fatalf("sign nbt root = %T", root)
	}
	front, ok := m["front_text"].(map[string]any)
	if !ok {
		t.Fatalf("front_text = %T", m["front_text"])
	}
	msgs, ok := front["messages"].(nbt.List)
	if !ok || len(msgs.Items) != 4 {
		t.Fatalf("messages = %+v", front["messages"])
	}
	if msgs.Items[0] != "a" || msgs.Items[3] != "d" {
		t.Fatalf("messages = %v", msgs.Items)
	}
}

func TestUseItemOnSignOpensEditor(t *testing.T) {
	s := New("unused")
	c, pr, _ := joinPlay(t, s, "Signer")
	defer c.Close()
	drainJoinBurst(t, pr)
	drainToQuiet(t, pr)

	sess := s.findSessionByName("Signer")
	// The packet-level use gate checks eye->block-AABB reach — stand
	// next to the sign.
	sess.setPos(8.5, -60, 0.5, 0, 0, true)
	pos := world.Pos{X: 8, Y: -61, Z: 0}
	sign, _ := world.BlockState("oak_sign")
	s.setBlockAndBroadcast("Signer", pos, sign)
	drainToQuiet(t, pr)

	seq := int32(4)
	s.handleUseItem(sess, useItemPkt(0, 8, -61, 0, 1, &seq))
	p, err := waitPacket(t, pr, IDPlayOpenSignEditor)
	if err != nil {
		t.Fatal(err)
	}
	r := protocol.NewPacketReader(p.Data)
	x, y, z, err := r.Position()
	if err != nil || x != 8 || y != -61 || z != 0 {
		t.Fatalf("editor pos = %d,%d,%d err %v", x, y, z, err)
	}
	if front, err := r.Bool(); err != nil || !front {
		t.Fatalf("editor front = %v, err %v", front, err)
	}
}
