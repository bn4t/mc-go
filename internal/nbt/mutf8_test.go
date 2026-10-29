package nbt

import (
	"bytes"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

// Fixtures measured by running JDK 25 DataOutputStream.writeUTF and
// DataInputStream.readUTF, rather than generated through this codec.
func TestModifiedUTF8JavaFixtures(t *testing.T) {
	for _, tc := range []struct{ text, wire string }{
		{"A\x00B", "000441c08042"},
		{"😀", "0006eda0bdedb880"},
		{"é", "0002c3a9"},
		{"\xed\xa0\x80", "0003eda080"},
		{"\xed\xb0\x80", "0003edb080"},
	} {
		want, _ := hex.DecodeString(tc.wire)
		var b bytes.Buffer
		if err := writeString(&b, tc.text); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(b.Bytes(), want) {
			t.Errorf("write %q: got %x want %x", tc.text, b.Bytes(), want)
		}
		got, err := readString(bytes.NewReader(want))
		if err != nil || got != tc.text {
			t.Errorf("read %x: got %q,%v want %q", want, got, err, tc.text)
		}
	}
}

func TestModifiedUTF8JavaDecodeCompatibility(t *testing.T) {
	for _, tc := range []struct{ wire, text string }{
		{"00", "\x00"}, {"c080", "\x00"}, {"c181", "A"}, {"e08080", "\x00"},
	} {
		b, _ := hex.DecodeString(tc.wire)
		got, err := decodeModifiedUTF8(b)
		if err != nil || got != tc.text {
			t.Errorf("decode %s: %q,%v", tc.wire, got, err)
		}
	}
	for _, wire := range []string{"f09f9880", "80", "c0", "c041", "e080", "e04180", "ff"} {
		b, _ := hex.DecodeString(wire)
		if _, err := decodeModifiedUTF8(b); !errors.Is(err, ErrBadString) {
			t.Errorf("decode %s error=%v", wire, err)
		}
	}
}

func TestModifiedUTF8EncodedLength(t *testing.T) {
	for _, tc := range []struct {
		text  string
		size  int
		valid bool
	}{
		{strings.Repeat("a", 65535), 65535, true},
		{strings.Repeat("a", 65536), 0, false},
		{strings.Repeat("\x00", 32767), 65534, true},
		{strings.Repeat("\x00", 32768), 0, false},
		{strings.Repeat("😀", 10922), 65532, true},
		{strings.Repeat("😀", 10923), 0, false},
	} {
		var b bytes.Buffer
		err := writeString(&b, tc.text)
		if tc.valid {
			if err != nil || b.Len() != tc.size+2 {
				t.Errorf("encoded length %d: bytes=%d err=%v", len(tc.text), b.Len(), err)
			}
		} else if !errors.Is(err, ErrBadString) || b.Len() != 0 {
			t.Errorf("overflow wrote=%d err=%v", b.Len(), err)
		}
	}
	var b bytes.Buffer
	if err := writeString(&b, "\xff"); !errors.Is(err, ErrBadString) {
		t.Fatalf("invalid UTF8 error=%v", err)
	}
}

func TestNBTUnicodeCompoundRoundTrip(t *testing.T) {
	original := map[string]any{"nul\x00😀": "é\x00😀"}
	var b bytes.Buffer
	if err := WriteNamed(&b, "😀", original); err != nil {
		t.Fatal(err)
	}
	name, got, err := ReadNamed(&b)
	if err != nil || name != "😀" || got.(map[string]any)["nul\x00😀"] != "é\x00😀" {
		t.Fatalf("roundtrip name=%q data=%v err=%v", name, got, err)
	}
}
