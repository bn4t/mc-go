package server

import (
	"testing"
	"time"
)

// materializeOutboundPacket lets queue-inspecting fixtures perform the same
// future wait and quota retirement as the socket writer, without needing a
// socket or assuming an encoder result is already present in sendPkt.data.
func materializeOutboundPacket(t *testing.T, sess *Session, pkt sendPkt) sendPkt {
	t.Helper()
	defer sess.retireOutbound(pkt)
	data := pkt.data
	if pkt.future != nil {
		select {
		case <-pkt.future.done:
		case <-time.After(10 * time.Second):
			t.Fatal("outbound fixture timed out awaiting chunk encoding")
		}
		var err error
		data, err = pkt.future.wait(nil)
		if err != nil {
			t.Fatalf("outbound fixture chunk encoding: %v", err)
		}
	}
	pkt.data = append([]byte(nil), data...)
	pkt.future = nil
	pkt.charge = 0
	return pkt
}
