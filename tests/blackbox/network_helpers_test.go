package blackbox

import (
	"net"
	"sync"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// streamPlayPacketsBlackbox owns the framed reader until cleanup. Timed
// observations can stop waiting on packets without interrupting frame decoding.
func streamPlayPacketsBlackbox(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer) (<-chan protocol.Packet, <-chan error) {
	t.Helper()
	packets := make(chan protocol.Packet, 256)
	errors := make(chan error, 1)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	t.Cleanup(func() {
		close(stop)
		_ = c.Close()
		wg.Wait()
	})
	go func() {
		defer wg.Done()
		for {
			p, err := pr.ReadPacket()
			if err == nil {
				switch p.ID {
				case server.IDPlayKeepAliveCB:
					err = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
				case server.IDPlayChunkBatchDone:
					err = ackChunkBatch(pw)
				case server.IDPlaySyncPos:
					var id int32
					id, err = protocol.NewPacketReader(p.Data).VarInt()
					if err == nil {
						var ack protocol.Buffer
						ack.VarInt(id)
						err = pw.WritePacket(server.IDPlayTeleportConfirmSB, ack.Bytes())
					}
				}
			}
			if err != nil {
				errors <- err
				return
			}
			select {
			case packets <- p:
			case <-stop:
				return
			}
		}
	}()
	return packets, errors
}

// waitTeleportBlackbox confirms synchronization packets through want. Reads
// have a single outer deadline: a timeout aborts the framed stream rather than
// retrying after an unknown amount of a frame was consumed.
func waitTeleportBlackbox(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, want int32) (x, y, z float64) {
	t.Helper()
	_ = c.SetDeadline(time.Now().Add(30 * time.Second))
	defer c.SetDeadline(time.Time{})
	for {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting for teleport %d: %v", want, err)
		}
		switch p.ID {
		case server.IDPlayKeepAliveCB:
			if err := pw.WritePacket(server.IDPlayKeepAliveSB, p.Data); err != nil {
				t.Fatal(err)
			}
		case server.IDPlayChunkBatchDone:
			if err := ackChunkBatch(pw); err != nil {
				t.Fatal(err)
			}
		case server.IDPlayDisconnect:
			t.Fatalf("disconnected while waiting for teleport %d: %x", want, p.Data)
		case server.IDPlaySyncPos:
			r := protocol.NewPacketReader(p.Data)
			id, err := r.VarInt()
			if err != nil {
				t.Fatal(err)
			}
			var ack protocol.Buffer
			ack.VarInt(id)
			if err := pw.WritePacket(server.IDPlayTeleportConfirmSB, ack.Bytes()); err != nil {
				t.Fatal(err)
			}
			if id != want {
				continue
			}
			if x, err = r.Double(); err != nil {
				t.Fatal(err)
			}
			if y, err = r.Double(); err != nil {
				t.Fatal(err)
			}
			if z, err = r.Double(); err != nil {
				t.Fatal(err)
			}
			return x, y, z
		}
	}
}
