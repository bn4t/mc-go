package server

import (
	"errors"
	"net"
	"time"

	"mc-go/internal/protocol"
)

// ErrOutboundLimit means the peer was disconnected because retaining another
// ordered packet would exceed its outbound budget. Callers never need to wait
// for that peer to read from its socket.
var ErrOutboundLimit = errors.New("client outbound budget exceeded")

const (
	defaultOutboundPackets = 256
	defaultOutboundBytes   = 8 << 20
	defaultWriteTimeout    = 5 * time.Second
)

// initOutbound runs before publishing the session to broadcasters.
func (sess *Session) initOutbound() {
	sess.outPacketLimit = defaultOutboundPackets
	sess.outByteLimit = defaultOutboundBytes
	sess.writeTimeout = defaultWriteTimeout
	if s := sess.server; s != nil {
		if s.OutboundPacketLimit > 0 {
			sess.outPacketLimit = s.OutboundPacketLimit
		}
		if s.OutboundByteLimit > 0 {
			sess.outByteLimit = s.OutboundByteLimit
		}
		if s.WriteTimeout > 0 {
			sess.writeTimeout = s.WriteTimeout
		}
	}
	sess.sendCh = make(chan sendPkt, sess.outPacketLimit)
}

// closeQueue aborts outbound work and unblocks both network goroutines. The
// channel itself stays open: concurrent producers cannot send to a closed
// channel. Only disconnected sessions discard queued packets.
func (sess *Session) closeQueue() {
	sess.outMu.Lock()
	sess.closeQueueLocked()
	sess.outMu.Unlock()
	if sess.conn != nil {
		_ = sess.conn.Close()
	}
}

func (sess *Session) closeQueueLocked() {
	sess.closing.Store(true)
	sess.closeOnce.Do(func() { close(sess.done) })
}

// write admits one packet without waiting for either queue space or network
// progress. Once admission fails the connection closes, since continuing after
// an omitted game-state packet would leave the client's state inconsistent.
func (sess *Session) write(id int32, data []byte) error {
	return sess.enqueue(sendPkt{id: id, data: data, charge: int64(len(data)) + int64(protocol.VarIntLen(id))})
}

func (sess *Session) enqueue(pkt sendPkt) error {
	sess.outMu.Lock()
	select {
	case <-sess.done:
		sess.outMu.Unlock()
		return net.ErrClosed
	default:
	}
	packetLimit, byteLimit := sess.outPacketLimit, sess.outByteLimit
	if packetLimit <= 0 {
		packetLimit = defaultOutboundPackets
	}
	if byteLimit <= 0 {
		byteLimit = defaultOutboundBytes
	}
	if sess.outPackets < packetLimit && pkt.charge <= byteLimit-sess.outBytes {
		select {
		case sess.sendCh <- pkt:
			sess.outPackets++
			sess.outBytes += pkt.charge
			sess.outMu.Unlock()
			return nil
		default:
		}
	}
	// Seal admission before releasing the lock so no producer can queue a
	// later packet after an earlier packet was rejected.
	sess.closeQueueLocked()
	sess.outMu.Unlock()
	if sess.conn != nil {
		_ = sess.conn.Close()
	}
	return ErrOutboundLimit
}

func (sess *Session) retireOutbound(pkt sendPkt) {
	charge := pkt.charge
	if f := pkt.future; f != nil {
		f.mu.Lock()
		charge = f.charge
		f.retired = true
		f.data = nil
	}
	sess.outMu.Lock()
	sess.outPackets--
	sess.outBytes -= charge
	sess.outMu.Unlock()
	if f := pkt.future; f != nil {
		f.mu.Unlock()
		f.Cancel()
	}
}

// writeLoop is the sole FIFO consumer. On every exit it releases queued
// payloads; a write error also closes the connection to wake its read loop.
func (sess *Session) writeLoop() {
	defer sess.wg.Done()
	defer func() {
		sess.closeQueue()
		for {
			select {
			case pkt := <-sess.sendCh:
				sess.retireOutbound(pkt)
			default:
				return
			}
		}
	}()
	for {
		select {
		case <-sess.done:
			return
		case pkt := <-sess.sendCh:
			select {
			case <-sess.done:
				sess.retireOutbound(pkt)
				return
			default:
			}
			if !sess.applyOne(pkt) {
				return
			}
		}
	}
}

func (sess *Session) applyOne(pkt sendPkt) bool {
	defer sess.retireOutbound(pkt)
	if pkt.future != nil {
		data, err := pkt.future.wait(sess.done)
		if err != nil {
			return false
		}
		pkt.data = data
	}
	sess.wmu.Lock()
	defer sess.wmu.Unlock()
	if pkt.setThreshold != nil {
		sess.pw.SetThreshold(*pkt.setThreshold)
		return true
	}
	timeout := sess.writeTimeout
	if timeout <= 0 {
		timeout = defaultWriteTimeout
	}
	if err := sess.conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		return false
	}
	return sess.pw.WritePacket(pkt.id, pkt.data) == nil
}

// Compression changes use the same bounded FIFO as packets, preserving the
// Set Compression / framed-packet boundary even under concurrent broadcasts.
func (sess *Session) setWriteThreshold(n int32) {
	v := n
	_ = sess.enqueue(sendPkt{setThreshold: &v})
}

// flush waits for admitted work to finish, including the active write. Its
// accounting is updated after serialization, so dequeueing is never mistaken
// for successful delivery. Short-lived status/login replies use this before
// closing their connection.
func (sess *Session) flush(timeout time.Duration) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		sess.outMu.Lock()
		drained := sess.outPackets == 0
		sess.outMu.Unlock()
		if drained {
			return
		}
		select {
		case <-sess.done:
			return
		case <-timer.C:
			return
		case <-tick.C:
		}
	}
}
