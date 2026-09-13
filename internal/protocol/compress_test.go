package protocol

import (
	"bytes"
	"testing"
)

func framed(t *testing.T, threshold int32, id int32, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := NewWriter(&buf)
	w.SetThreshold(threshold)
	if err := w.WritePacket(id, data); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestUncompressedPassthrough(t *testing.T) {
	raw := framed(t, -1, 0x02, []byte{1, 2, 3})
	r := NewReader(bytes.NewReader(raw))
	p, err := r.ReadPacket()
	if err != nil || p.ID != 0x02 || len(p.Data) != 3 {
		t.Fatalf("got %+v, err %v", p, err)
	}
}

func TestSmallPacketStaysRaw(t *testing.T) {
	raw := framed(t, 256, 0x02, []byte{9})
	r := NewReader(bytes.NewReader(raw))
	r.SetThreshold(256)
	p, err := r.ReadPacket()
	if err != nil || p.ID != 0x02 || len(p.Data) != 1 || p.Data[0] != 9 {
		t.Fatalf("got %+v, err %v", p, err)
	}
}

func TestLargePacketRoundTrip(t *testing.T) {
	data := bytes.Repeat([]byte("abcdefgh"), 100) // 800 bytes, compressible
	raw := framed(t, 256, 0x07, data)
	if len(raw) >= 800 {
		t.Fatalf("expected compression to shrink 800 bytes, frame = %d", len(raw))
	}
	r := NewReader(bytes.NewReader(raw))
	r.SetThreshold(256)
	p, err := r.ReadPacket()
	if err != nil || p.ID != 0x07 || !bytes.Equal(p.Data, data) {
		t.Fatalf("id=%#x len=%d err=%v", p.ID, len(p.Data), err)
	}
}

func TestThresholdBoundary(t *testing.T) {
	// Body = id varint + data. id 0x00 -> 1 byte, so with
	// threshold 5 a 4-byte data payload (body 5) must compress
	// while a 3-byte payload (body 4) stays raw.
	small := framed(t, 5, 0x00, []byte{1, 2, 3})    // body 4
	large := framed(t, 5, 0x00, []byte{1, 2, 3, 4}) // body 5
	rs := NewReader(bytes.NewReader(small))
	rs.SetThreshold(5)
	if _, err := rs.ReadPacket(); err != nil {
		t.Fatalf("small: %v", err)
	}
	rl := NewReader(bytes.NewReader(large))
	rl.SetThreshold(5)
	p, err := rl.ReadPacket()
	if err != nil || len(p.Data) != 4 {
		t.Fatalf("large: %+v err %v", p, err)
	}
	// Wire check: compressed frames carry nonzero data-length.
	r := bytes.NewReader(large)
	if _, err := ReadVarInt(r); err != nil { // length
		t.Fatal(err)
	}
	if dl, err := ReadVarInt(r); err != nil || dl == 0 {
		t.Fatalf("data length = %d, err %v (want nonzero)", dl, err)
	}
}

func TestCorruptDeflateFails(t *testing.T) {
	raw := framed(t, 1, 0x01, bytes.Repeat([]byte{7}, 64))
	raw[len(raw)-1] ^= 0xFF // corrupt trailer
	r := NewReader(bytes.NewReader(raw))
	r.SetThreshold(1)
	if _, err := r.ReadPacket(); err == nil {
		t.Fatal("expected error for corrupt stream")
	}
}
