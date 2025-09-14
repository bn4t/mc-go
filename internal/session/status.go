package session

import (
	"context"
	"fmt"

	"github.com/mc-go/server/internal/packets/v1211"
)

// runStatus handles the Status query+ping cycle.
func (s *Session) runStatus(ctx context.Context) error {
	for {
		rf, ok, err := s.readRaw(ctx)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		pid, body := s.peekPacketID(rf.Body)

		switch pid {
		case v1211.PIDCStatusRequest:
			var req v1211.CStatusRequest
			if err := req.ReadFrom(body); err != nil {
				return err
			}
			online := 0
			if s.world != nil {
				online = s.world.Online()
			}
			json := fmt.Sprintf(`{"version":{"name":"1.21.1","protocol":767},"players":{"max":%d,"online":%d},"description":{"text":"%s"}}`, s.maxP, online, s.motd)
			resp := v1211.SStatusResponse{JSON: json}
			if err := s.writePacketNow(resp.ID(), resp.EncodeBody()); err != nil {
				return err
			}
		case v1211.PIDCPing:
			var ping v1211.CPing
			if err := ping.ReadFrom(body); err != nil {
				return err
			}
			pong := v1211.SPong{Payload: ping.Payload}
			if err := s.writePacketNow(pong.ID(), pong.EncodeBody()); err != nil {
				return err
			}
			return nil
		default:
			return fmt.Errorf("unexpected pid in status: 0x%x", pid)
		}
	}
}
