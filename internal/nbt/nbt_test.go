package nbt

import (
	"bytes"
	"reflect"
	"testing"
)

func roundTrip(t *testing.T, name string, v any) any {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteNamed(&buf, name, v); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	gotName, got, err := ReadNamed(&buf)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	if gotName != name {
		t.Fatalf("name = %q, want %q", gotName, name)
	}
	return got
}

func TestAllScalarTags(t *testing.T) {
	cases := []any{
		int8(-5), int16(-300), int32(-70000), int64(-1 << 40),
		float32(1.5), float64(-3.25),
		[]byte{0, 1, 2, 255}, "hello",
		[]int32{1, -2, 3}, []int64{-1, 2},
	}
	for _, v := range cases {
		if got := roundTrip(t, "v", v); !reflect.DeepEqual(got, v) {
			t.Fatalf("round trip %#v gave %#v", v, got)
		}
	}
}

func TestNestedCompoundAndList(t *testing.T) {
	v := map[string]any{
		"name":   "steve",
		"health": float32(20),
		"pos":    List{Elem: TagDouble, Items: []any{1.5, 65.0, -3.25}},
		"inner": map[string]any{
			"flag": int8(1),
			"ids":  List{Elem: TagInt, Items: []any{int32(1), int32(2)}},
		},
		"empty": List{Elem: TagEnd, Items: []any{}},
	}
	got, ok := roundTrip(t, "root", v).(map[string]any)
	if !ok {
		t.Fatalf("root type %T", got)
	}
	if got["name"] != "steve" || got["health"] != float32(20) {
		t.Fatalf("scalars = %+v", got)
	}
	pos, ok := got["pos"].(List)
	if !ok || pos.Elem != TagDouble || len(pos.Items) != 3 {
		t.Fatalf("pos = %+v", got["pos"])
	}
	inner, ok := got["inner"].(map[string]any)
	if !ok || inner["flag"] != int8(1) {
		t.Fatalf("inner = %+v", got["inner"])
	}
}

func TestKnownEncoding(t *testing.T) {
	// TAG_Byte named "b" with value 5 must encode as 01 00 01 'b' 05.
	var buf bytes.Buffer
	if err := WriteNamed(&buf, "b", int8(5)); err != nil {
		t.Fatal(err)
	}
	want := []byte{1, 0, 1, 'b', 5}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("encoding = %x, want %x", buf.Bytes(), want)
	}
}

func TestTruncatedFails(t *testing.T) {
	for _, raw := range [][]byte{{}, {10}, {10, 0}, {1, 0, 1, 'b'}} {
		if _, _, err := ReadNamed(bytes.NewReader(raw)); err == nil {
			t.Fatalf("expected error for %x", raw)
		}
	}
}

func TestBadTagFails(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteNamed(&buf, "ok", int32(1)); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	raw[0] = 99 // corrupt root tag
	if _, _, err := ReadNamed(bytes.NewReader(raw)); err == nil {
		t.Fatal("expected error for unknown tag")
	}
}

func TestUnknownGoTypeFails(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteNamed(&buf, "x", uint16(1)); err == nil {
		t.Fatal("expected error for unsupported Go type")
	}
}
