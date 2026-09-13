package protocol

import (
	"bytes"
	"testing"
)

func TestScalarRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteByte(&buf, 0xAB); err != nil {
		t.Fatal(err)
	}
	if err := WriteBool(&buf, true); err != nil {
		t.Fatal(err)
	}
	if err := WriteInt(&buf, -123456); err != nil {
		t.Fatal(err)
	}
	if err := WriteFloat(&buf, 1.5); err != nil {
		t.Fatal(err)
	}
	if err := WriteDouble(&buf, -3.25); err != nil {
		t.Fatal(err)
	}
	r := bytes.NewReader(buf.Bytes())
	if b, err := ReadByte(r); err != nil || b != 0xAB {
		t.Fatalf("byte = %#x, err %v", b, err)
	}
	if v, err := ReadBool(r); err != nil || !v {
		t.Fatalf("bool = %v, err %v", v, err)
	}
	if v, err := ReadInt(r); err != nil || v != -123456 {
		t.Fatalf("int = %d, err %v", v, err)
	}
	if v, err := ReadFloat(r); err != nil || v != 1.5 {
		t.Fatalf("float = %v, err %v", v, err)
	}
	if v, err := ReadDouble(r); err != nil || v != -3.25 {
		t.Fatalf("double = %v, err %v", v, err)
	}
}

func TestBoolRejectsInvalid(t *testing.T) {
	if _, err := ReadBool(bytes.NewReader([]byte{7})); err == nil {
		t.Fatal("expected error for byte 7")
	}
}

func TestBufferPacketReaderScalars(t *testing.T) {
	var b Buffer
	b.Byte(9)
	b.Bool(true)
	b.Int(42)
	b.Float(2.5)
	b.Double(7.75)
	var id [16]byte
	id[0] = 1
	b.UUID(id)
	r := NewPacketReader(b.Bytes())
	if v, _ := r.Byte(); v != 9 {
		t.Fatalf("byte = %d", v)
	}
	if v, _ := r.Bool(); !v {
		t.Fatal("bool = false")
	}
	if v, _ := r.Int(); v != 42 {
		t.Fatalf("int = %d", v)
	}
	if v, _ := r.Float(); v != 2.5 {
		t.Fatalf("float = %v", v)
	}
	if v, _ := r.Double(); v != 7.75 {
		t.Fatalf("double = %v", v)
	}
	if v, err := r.UUID(); err != nil || v != id {
		t.Fatalf("uuid = %x, err %v", v, err)
	}
	if r.Remaining() != 0 {
		t.Fatalf("remaining = %d", r.Remaining())
	}
}
