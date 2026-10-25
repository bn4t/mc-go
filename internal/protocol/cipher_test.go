package protocol

import (
	"bytes"
	"encoding/hex"
	"io"
	"testing"
)

// cfb8JDK is AES/CFB8/NoPadding output produced by the JDK's own
// Cipher (javax.crypto, encrypt mode) over cfb8Plain with cfb8Key —
// the same transform Minecraft's Crypt.getCipher installs on the
// connection. The test proves the hand-rolled stream matches the JDK
// byte-for-byte, since Go's crypto/cipher CFB is full-width feedback
// and cannot express the 8-bit variant.
const (
	cfb8KeyHex   = "359d41baf78afe0de1bbe7ae28c0450c"
	cfb8PlainHex = "01080f161d242b323940474e555c636a71787f868d949ba2a9b0b7bec5ccd3dae1e8eff6fd040b121920272e353c434a51585f666d747b828990979ea5acb3bac1c8cfd6dde4ebf2f900070e151c232a31383f464d545b626970777e858c939aa1a8afb6"
	cfb8JDKHex   = "85c42f35bb735a3e18674058283e320a8cd89a0a106e56c9f4b5756c7c79b18b1c33a348af32151d215e8223ab23b7b71f22c3de2c0974df61ee2bc8bbcc170f15a61a54fe39709e67e79df9e5ee039f55e4610482ca4d9e3618ac3174a602181c3569e6"
)

func unhex(t *testing.T, h string) []byte {
	t.Helper()
	b, err := hex.DecodeString(h)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCFB8MatchesJDK(t *testing.T) {
	key := unhex(t, cfb8KeyHex)
	plain := unhex(t, cfb8PlainHex)
	want := unhex(t, cfb8JDKHex)

	var buf bytes.Buffer
	w, err := cipherWriter(&buf, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(plain); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("encrypt mismatch\n got %x\nwant %x", buf.Bytes(), want)
	}

	r, err := cipherReader(bytes.NewReader(want), key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("decrypt mismatch\n got %x\nwant %x", got, plain)
	}
}

func TestCFB8RoundTrip(t *testing.T) {
	key := make([]byte, 16)
	for i := range key {
		key[i] = byte(i * 3)
	}
	plain := []byte("The quick brown fox jumps over the lazy dog. 0123456789!")

	var buf bytes.Buffer
	w, err := cipherWriter(&buf, key)
	if err != nil {
		t.Fatal(err)
	}
	// Write in odd-sized chunks — CFB8 is a byte stream, chunk
	// boundaries must not matter.
	for _, chunk := range [][]byte{plain[:1], plain[1:17], plain[17:]} {
		if _, err := w.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	r, err := cipherReader(bytes.NewReader(buf.Bytes()), key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("roundtrip mismatch\n got %q\nwant %q", got, plain)
	}
}
