package server

import (
	"encoding/json"
	"time"

	"mc-go/internal/protocol"
)

type statusSample struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

type statusResponse struct {
	Version struct {
		Name     string `json:"name"`
		Protocol int    `json:"protocol"`
	} `json:"version"`
	Players struct {
		Max    int            `json:"max"`
		Online int            `json:"online"`
		Sample []statusSample `json:"sample"`
	} `json:"players"`
	Description map[string]string `json:"description"`
}

// sampleLimit matches vanilla's cap on the status player sample.
const sampleLimit = 12

// onlineSample lists up to 12 logged-in players (name + dashed UUID),
// like vanilla's status response.
func (s *Server) onlineSample() []statusSample {
	var out []statusSample
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if len(out) >= sampleLimit || sess.state.Load() != StatePlay {
			return len(out) < sampleLimit
		}
		name := sess.getName()
		if name == "" {
			return true
		}
		out = append(out, statusSample{Name: name, ID: dashUUID(sess.getUUID())})
		return true
	})
	return out
}

// dashUUID renders a UUID in dashed form.
// DashUUID renders a UUID in vanilla's dashed string form. Exported so
// blackbox tests can build ops.json entries.
func DashUUID(u [16]byte) string { return dashUUID(u) }

func dashUUID(u [16]byte) string {
	const hexDigits = "0123456789abcdef"
	var out [36]byte
	j := 0
	for i, b := range u {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			out[j] = '-'
			j++
		}
		out[j] = hexDigits[b>>4]
		out[j+1] = hexDigits[b&0x0F]
		j += 2
	}
	return string(out[:])
}

func (s *Server) buildStatusJSON() string {
	var resp statusResponse
	resp.Version.Name = VersionName
	resp.Version.Protocol = ProtocolVersion
	resp.Players.Max = s.MaxPlayers
	resp.Players.Online = s.OnlineCount()
	resp.Players.Sample = s.onlineSample()
	resp.Description = map[string]string{"text": s.MOTD}
	b, _ := json.Marshal(resp)
	return string(b)
}

func (s *Server) handleStatus(sess *Session) {
	for {
		p, err := sess.pr.ReadPacket()
		if err != nil {
			return
		}
		switch p.ID {
		case IDStatusRequest:
			var b protocol.Buffer
			b.String(s.buildStatusJSON())
			if err := sess.write(IDStatusResponse, b.Bytes()); err != nil {
				return
			}
		case IDPingRequest:
			r := protocol.NewPacketReader(p.Data)
			payload, err := r.Long()
			if err != nil {
				return
			}
			var b protocol.Buffer
			b.Long(payload)
			_ = sess.write(IDPongResponse, b.Bytes())
			sess.flush(2 * time.Second)
			return // vanilla closes after pong
		default:
			return
		}
	}
}
