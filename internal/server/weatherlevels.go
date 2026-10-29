package server

import (
	"math"
	"sync/atomic"
)

// Intensities retain each float32 addition/subtraction, including accumulated
// rounding. Integer millisteps cannot reproduce the levels sent on the wire.
func weatherLevel(level *atomic.Int32) float32           { return math.Float32frombits(uint32(level.Load())) }
func setWeatherLevel(level *atomic.Int32, value float32) { level.Store(int32(math.Float32bits(value))) }
func (s *Server) rainLevel() float32                     { return weatherLevel(&s.weatherRain) }
func (s *Server) thunderLevel() float32                  { return weatherLevel(&s.weatherThundLv) }
func (s *Server) effectiveThunderLevel() float32         { return s.thunderLevel() * s.rainLevel() }
func (s *Server) isRaining() bool                        { return float64(s.rainLevel()) > 0.2 }

func (s *Server) broadcastWeatherLevel(event byte, value float32) {
	s.sessions.Range(func(_, v any) bool {
		sess, ok := v.(*Session)
		if ok && sess.state.Load() == StatePlay && sess.dimension().name == dimOverworld {
			_ = s.sendGameEvent(sess, event, value)
		}
		return true
	})
}

// A new player gets effective thunder intensity, while ongoing weather
// broadcasts carry the independently changing raw thunder intensity.
func (s *Server) sendWeatherState(sess *Session) error {
	if sess.dimension().name != dimOverworld || !s.isRaining() {
		return nil
	}
	if err := s.sendGameEvent(sess, 1, 0); err != nil {
		return err
	}
	if err := s.sendGameEvent(sess, 7, s.rainLevel()); err != nil {
		return err
	}
	return s.sendGameEvent(sess, 8, s.effectiveThunderLevel())
}
