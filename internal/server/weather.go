package server

import (
	"strconv"
	"strings"
	"sync/atomic"
)

// /weather and the server's weather state. Vanilla keeps a single
// server-wide WeatherData (MinecraftServer.setWeatherParameters) —
// every level's isRaining/isThundering reads it — and the command
// writes the three fields in one call: clear → (0,0,false,false),
// rain → (dur,dur,true,false), thunder → (dur,dur,true,true). mc-go
// models the same pair of flags plus a countdown; the random
// provider vanilla rolls for an omitted duration is pinned to a
// fixed value, and natural weather cycling (advance_weather) is
// unmodeled — the flags only move by command or expiry.

// weatherDefaultTicks is the fixed duration used when /weather omits
// one; vanilla rolls a uniform provider per mode instead.
const weatherDefaultTicks = 5 * 60 * 20

// isThundering answers the channeling weather_check the way vanilla's
// Level.isThundering does: the lerped thunder level must exceed 0.9 —
// the flags flip instantly but the level ramps 0.01/tick, so a bolt
// summoned right after /weather thunder still sees clear skies.
func (s *Server) isThundering() bool { return float64(s.effectiveThunderLevel()) > 0.9 }

// weatherTickStep counts the weather duration down once per tick and
// lerps both intensity levels toward their flags at vanilla's
// 0.01/tick rate; expiry returns the world to clear.
func (s *Server) weatherTickStep() {
	beforeRain, beforeThunder := s.rainLevel(), s.thunderLevel()
	wasRaining := s.isRaining()
	if s.weatherTicks.Load() > 0 && s.weatherTicks.Add(-1) <= 0 {
		s.weatherRaining.Store(false)
		s.weatherThunder.Store(false)
	}
	s.lerpWeather(&s.weatherRain, s.weatherRaining.Load())
	s.lerpWeather(&s.weatherThundLv, s.weatherThunder.Load())
	rain, thunder := s.rainLevel(), s.thunderLevel()
	if rain != beforeRain {
		s.broadcastWeatherLevel(7, rain)
	}
	if thunder != beforeThunder {
		s.broadcastWeatherLevel(8, thunder)
	}
	if raining := s.isRaining(); raining != wasRaining {
		event := byte(2)
		if raining {
			event = 1
		}
		s.broadcastGameEvent(event, 0)
		s.broadcastGameEvent(7, rain)
		s.broadcastGameEvent(8, thunder)
	}
}

// lerpWeather advances one independently rounded float32 step.
func (s *Server) lerpWeather(level *atomic.Int32, on bool) {
	value := weatherLevel(level)
	if on {
		value = min(value+float32(0.01), float32(1))
	} else {
		value = max(value-float32(0.01), float32(0))
	}
	setWeatherLevel(level, value)
}

// runWeather implements /weather <clear|rain|thunder> [duration]. The
// duration is vanilla's TimeArgument — bare seconds, or a t/s/d
// suffix — and replies are the commands.weather.set.* keys, sent to
// the admin channel as well as the issuer like every state-changing
// vanilla command.
func (s *Server) runWeather(player string, args []string) reply {
	sess := s.findSessionByName(player)
	if sess == nil && s.commandFrame(player) == nil || s.permissionLevel(player) < 2 {
		return reply{comp: unknownCommand(), styled: true, fail: &failMark{}}
	}
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 1)
	}
	var rain, thunder bool
	switch args[0] {
	case "clear":
	case "rain":
		rain = true
	case "thunder":
		rain, thunder = true, true
	default:
		// The modes are brigadier literals — a bad word fails the
		// dispatch itself.
		return errTokenEnd("command.unknown.command", 1)
	}
	ticks := int64(weatherDefaultTicks)
	if len(args) > 1 {
		v, ok := parseWeatherDuration(args[1])
		if !ok {
			return errTokenEnd("command.unknown.command", 1)
		}
		ticks = v
	}
	if len(args) > 2 {
		return errToken("command.unknown.argument", 3)
	}
	s.weatherRaining.Store(rain)
	s.weatherThunder.Store(thunder)
	s.weatherTicks.Store(ticks)
	msg := translatable("commands.weather.set." + args[0])
	return opsEchoReply(msg)
}

// parseWeatherDuration reads a vanilla time argument: a bare number
// is seconds, "t" is ticks, "s" seconds, "d" days (24000 ticks each).
// Vanilla bounds the value to [1, 1000000] before unit conversion.
func parseWeatherDuration(s string) (int64, bool) {
	unit := int64(20)
	num := s
	if strings.HasSuffix(s, "t") {
		unit = 1
		num = strings.TrimSuffix(s, "t")
	} else if strings.HasSuffix(s, "s") {
		num = strings.TrimSuffix(s, "s")
	} else if strings.HasSuffix(s, "d") {
		unit = 24000 * 20
		num = strings.TrimSuffix(s, "d")
	}
	v, err := strconv.ParseInt(num, 10, 64)
	if err != nil || v < 1 || v > 1000000 {
		return 0, false
	}
	return v * unit, true
}
