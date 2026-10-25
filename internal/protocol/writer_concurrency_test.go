package protocol

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"runtime"
	"sync"
	"testing"
)

// yieldingWriter allows a competing sender to run between header and body.
// Its buffer deliberately relies on Writer to serialize complete frames.
type yieldingWriter struct{ bytes.Buffer }

func (w *yieldingWriter) Write(p []byte) (int, error) {
	n, err := w.Buffer.Write(p)
	runtime.Gosched()
	return n, err
}

func TestWriterConcurrentFrames(t *testing.T) {
	for _, threshold := range []int32{-1, 1, 1024} {
		t.Run(fmt.Sprint(threshold), func(t *testing.T) {
			var sink yieldingWriter
			w := NewWriter(&sink)
			w.SetThreshold(threshold)
			const senders, packets = 8, 30
			var wg sync.WaitGroup
			errs := make(chan error, senders)
			for sender := 0; sender < senders; sender++ {
				wg.Add(1)
				go func(sender int) {
					defer wg.Done()
					for seq := 0; seq < packets; seq++ {
						if err := w.WritePacket(int32(sender), []byte{byte(seq), byte(sender)}); err != nil {
							errs <- err
							return
						}
					}
				}(sender)
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				t.Fatal(err)
			}
			r := NewReader(bytes.NewReader(sink.Bytes()))
			r.SetThreshold(threshold)
			next := make([]int, senders)
			for i := 0; i < senders*packets; i++ {
				p, err := r.ReadPacket()
				if err != nil {
					t.Fatalf("packet %d: %v", i, err)
				}
				if p.ID < 0 || p.ID >= senders || len(p.Data) != 2 || int(p.Data[1]) != int(p.ID) || int(p.Data[0]) != next[p.ID] {
					t.Fatalf("interleaved or reordered packet: %+v, next=%v", p, next)
				}
				next[p.ID]++
			}
			if _, err := r.ReadPacket(); !errors.Is(err, io.EOF) {
				t.Fatalf("trailing frame data: %v", err)
			}
		})
	}
}

type shortPacketWriter struct{ calls, shortAt int }

func (w *shortPacketWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.calls == w.shortAt {
		return len(p) - 1, nil
	}
	return len(p), nil
}

func TestWriterRejectsShortFrames(t *testing.T) {
	for _, threshold := range []int32{-1, 1, 1024} {
		for _, shortAt := range []int{1, 2} {
			t.Run(fmt.Sprintf("threshold=%d/part=%d", threshold, shortAt), func(t *testing.T) {
				w := NewWriter(&shortPacketWriter{shortAt: shortAt})
				w.SetThreshold(threshold)
				if err := w.WritePacket(5, []byte("payload")); !errors.Is(err, io.ErrShortWrite) {
					t.Fatalf("write error = %v, want short write", err)
				}
			})
		}
	}
}
