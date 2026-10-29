package protocol

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

type injectedFrameTimeout struct{}

func (injectedFrameTimeout) Error() string   { return "injected frame read deadline" }
func (injectedFrameTimeout) Timeout() bool   { return true }
func (injectedFrameTimeout) Temporary() bool { return true }

// oneFrameTimeout delivers exactly split bytes, one deadline error, and then
// the remainder. It covers partial length prefixes as well as partial bodies.
type oneFrameTimeout struct {
	wire          []byte
	split, offset int
	injected      bool
}

func (r *oneFrameTimeout) Read(p []byte) (int, error) {
	if r.offset == r.split && !r.injected {
		r.injected = true
		return 0, injectedFrameTimeout{}
	}
	if r.offset == len(r.wire) {
		return 0, io.EOF
	}
	n := len(r.wire) - r.offset
	if n > len(p) {
		n = len(p)
	}
	if !r.injected && n > r.split-r.offset {
		n = r.split - r.offset
	}
	copy(p, r.wire[r.offset:r.offset+n])
	r.offset += n
	return n, nil
}

func TestReaderResumesEveryFrameDeadline(t *testing.T) {
	payload := make([]byte, 300)
	x := uint32(1)
	for i := range payload {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		payload[i] = byte(x)
	}
	for _, threshold := range []int32{-1, 64} {
		var buf bytes.Buffer
		w := NewWriter(&buf)
		w.SetThreshold(threshold)
		if err := w.WritePacket(0x1234, payload); err != nil {
			t.Fatal(err)
		}
		firstLen := buf.Len()
		if err := w.WritePacket(0x33, []byte("next")); err != nil {
			t.Fatal(err)
		}
		wire := buf.Bytes()
		for split := 0; split < firstLen; split++ {
			r := NewReader(&oneFrameTimeout{wire: wire, split: split})
			r.SetThreshold(threshold)
			_, err := r.ReadPacket()
			var ne net.Error
			if !errors.As(err, &ne) || !ne.Timeout() {
				t.Fatalf("threshold%d split%d first read=%v", threshold, split, err)
			}
			got, err := r.ReadPacket()
			if err != nil || got.ID != 0x1234 || !bytes.Equal(got.Data, payload) {
				t.Fatalf("threshold%d split%d resumed id=%x bytes=%d err=%v", threshold, split, got.ID, len(got.Data), err)
			}
			next, err := r.ReadPacket()
			if err != nil || next.ID != 0x33 || string(next.Data) != "next" {
				t.Fatalf("threshold%d split%d next frame=%+v err=%v", threshold, split, next, err)
			}
		}
		t.Logf("threshold%d resumed all%d frame boundaries", threshold, firstLen)
	}
}

func TestReaderTerminalFrameErrorsReset(t *testing.T) {
	cases := []struct {
		name string
		wire []byte
		want error
	}{
		{"prefix_eof", []byte{0x80}, io.EOF},
		{"body_eof", []byte{3, 1}, io.ErrUnexpectedEOF},
		{"varint_limit", []byte{0x80, 0x80, 0x80, 0x80, 0x80}, ErrVarIntTooBig},
		{"negative_length", AppendVarInt(nil, -1), ErrPacketTooLarge},
		{"frame_limit", AppendVarInt(nil, MaxPacketLen+1), ErrPacketTooLarge},
		{"empty_frame", []byte{0}, io.ErrUnexpectedEOF},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf := bytes.NewBuffer(append([]byte(nil), tc.wire...))
			r := NewReader(buf)
			if _, err := r.ReadPacket(); !errors.Is(err, tc.want) {
				t.Fatalf("got%v want%v", err, tc.want)
			}
			if err := NewWriter(buf).WritePacket(9, []byte("fresh")); err != nil {
				t.Fatal(err)
			}
			got, err := r.ReadPacket()
			if err != nil || got.ID != 9 || string(got.Data) != "fresh" {
				t.Fatalf("terminal error retained stale bytes: %+v %v", got, err)
			}
		})
	}
}

func TestReaderDeadlineAcrossCompressionNegotiation(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.WritePacket(3, []byte("enable compression")); err != nil {
		t.Fatal(err)
	}
	firstLen := buf.Len()
	w.SetThreshold(16)
	payload := bytes.Repeat([]byte("payload"), 50)
	if err := w.WritePacket(0x4321, payload); err != nil {
		t.Fatal(err)
	}
	r := NewReader(&oneFrameTimeout{wire: buf.Bytes(), split: firstLen + 4})
	if p, err := r.ReadPacket(); err != nil || p.ID != 3 {
		t.Fatalf("negotiation packet: %+v %v", p, err)
	}
	r.SetThreshold(16)
	if _, err := r.ReadPacket(); err == nil {
		t.Fatal("missing injected deadline")
	}
	p, err := r.ReadPacket()
	if err != nil || p.ID != 0x4321 || !bytes.Equal(p.Data, payload) {
		t.Fatalf("negotiated resumed packet: %+v %v", p, err)
	}
}

func TestReaderDeadlineWithFragmentedPipe(t *testing.T) {
	for _, threshold := range []int32{-1, 64} {
		for _, prefixOnly := range []bool{true, false} {
			t.Run(fmt.Sprintf("threshold%d/prefix%t", threshold, prefixOnly), func(t *testing.T) {
				payload := bytes.Repeat([]byte("fragmented packet"), 40)
				var buf bytes.Buffer
				w := NewWriter(&buf)
				w.SetThreshold(threshold)
				if err := w.WritePacket(0x2345, payload); err != nil {
					t.Fatal(err)
				}
				firstLen := buf.Len()
				if err := w.WritePacket(7, []byte("after")); err != nil {
					t.Fatal(err)
				}
				wire := buf.Bytes()
				split := firstLen / 2
				if prefixOnly {
					split = 1
				}
				readerConn, writerConn := net.Pipe()
				defer readerConn.Close()
				defer writerConn.Close()
				release := make(chan struct{})
				written := make(chan error, 1)
				go func() {
					if _, err := writerConn.Write(wire[:split]); err != nil {
						written <- err
						return
					}
					<-release
					for off := split; off < len(wire); {
						end := off + 3
						if end > len(wire) {
							end = len(wire)
						}
						n, err := writerConn.Write(wire[off:end])
						off += n
						if err != nil {
							written <- err
							return
						}
					}
					written <- nil
				}()
				r := NewReader(readerConn)
				r.SetThreshold(threshold)
				_ = readerConn.SetReadDeadline(time.Now().Add(25 * time.Millisecond))
				_, err := r.ReadPacket()
				var ne net.Error
				if !errors.As(err, &ne) || !ne.Timeout() {
					close(release)
					t.Fatalf("partial pipe read: %v", err)
				}
				_ = readerConn.SetReadDeadline(time.Now().Add(time.Second))
				close(release)
				got, err := r.ReadPacket()
				if err != nil || got.ID != 0x2345 || !bytes.Equal(got.Data, payload) {
					t.Fatalf("resumed pipe frame: id%x bytes%d err%v", got.ID, len(got.Data), err)
				}
				next, err := r.ReadPacket()
				if err != nil || next.ID != 7 || string(next.Data) != "after" {
					t.Fatalf("following pipe frame: %+v %v", next, err)
				}
				if err := <-written; err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestReaderResumesThreeByteFrameLength(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 16384)
	var buf bytes.Buffer
	if err := NewWriter(&buf).WritePacket(0x1234, payload); err != nil {
		t.Fatal(err)
	}
	for _, split := range []int{1, 2, 3} {
		r := NewReader(&oneFrameTimeout{wire: buf.Bytes(), split: split})
		if _, err := r.ReadPacket(); err == nil {
			t.Fatal("missing injected deadline")
		}
		got, err := r.ReadPacket()
		if err != nil || got.ID != 0x1234 || !bytes.Equal(got.Data, payload) {
			t.Fatalf("three-byte length split%d: id%x bytes%d err%v", split, got.ID, len(got.Data), err)
		}
	}
}
