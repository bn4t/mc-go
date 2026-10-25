package protocol

import (
	"bytes"
	"testing"
)

func TestVarIntRoundTrip(t *testing.T) {
	vals := []int32{0, 1, 2, 127, 128, 255, 2147483647, -1, -2147483648}
	for _, v := range vals {
		var buf bytes.Buffer
		if err := WriteVarInt(&buf, v); err != nil {
			t.Fatalf("write %d: %v", v, err)
		}
		got, err := ReadVarInt(&buf)
		if err != nil {
			t.Fatalf("read %d: %v", v, err)
		}
		if got != v {
			t.Fatalf("roundtrip %d got %d", v, got)
		}
	}
	// Known encoding: 300 -> AC 02
	var buf bytes.Buffer
	_ = WriteVarInt(&buf, 300)
	if !bytes.Equal(buf.Bytes(), []byte{0xAC, 0x02}) {
		t.Fatalf("300 encoding = %x, want ac02", buf.Bytes())
	}
}

func TestPacketFraming(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.WritePacket(0x00, []byte{0x01, 0x02}); err != nil {
		t.Fatal(err)
	}
	r := NewReader(&buf)
	p, err := r.ReadPacket()
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != 0x00 || len(p.Data) != 2 || p.Data[0] != 0x01 {
		t.Fatalf("bad packet %+v", p)
	}
}
