package server

import (
	"math/rand"
	"sync"
)

// /weather and the server's weather. Vanilla keeps one server-wide
// WeatherData — clear, rain and thunder timers plus the raining and
// thundering flags — and every level able to have weather (sky light,
// no ceiling, not the End: the overworld here) advances it once per
// tick in ServerLevel.advanceWeatherCycle, then eases its own rain and
// thunder levels 0.01 per tick toward the flags. Gameplay reads the
// levels, not the flags: Level.isRaining is rain level > 0.2 and
// isThundering is thunder level × rain level > 0.9, so rain "starts"
// about twenty ticks after the flag flips.

// The UniformInt providers ServerLevel samples for the natural cycle
// and /weather samples for an omitted duration (inclusive bounds).
var (
	weatherRainDelay       = [2]int32{12000, 180000}
	weatherRainDuration    = [2]int32{12000, 24000}
	weatherThunderDelay    = [2]int32{12000, 180000}
	weatherThunderDuration = [2]int32{3600, 15600}
)

func weatherSample(p [2]int32) int32 { return p[0] + rand.Int31n(p[1]-p[0]+1) }

// Game event ids ClientboundGameEventPacket carries for weather.
const (
	gameEventStartRaining   = 1
	gameEventStopRaining    = 2
	gameEventRainLevel      = 7
	gameEventThunderLevel   = 8
	weatherLevelStep        = float32(0.01)
	weatherRainThreshold    = 0.2
	weatherThunderThreshold = 0.9
)

// weatherState is WeatherData plus the overworld's eased levels and
// their previous-tick values (vanilla's oRainLevel/oThunderLevel).
type weatherState struct {
	mu                   sync.Mutex
	clear, rain, thunder int32
	raining, thundering  bool
	rainLevel, oRain     float32
	thunderLvl, oThunder float32
}

// levels are getRainLevel(1) and getThunderLevel(1): Mth.lerp in
// float32 from the previous value, the thunder one scaled by rain.
func (w *weatherState) levels() (rain, thunder float32) {
	rain = w.oRain + (w.rainLevel - w.oRain)
	thunder = (w.oThunder + (w.thunderLvl - w.oThunder)) * rain
	return rain, thunder
}

// publish refreshes the isRaining/isThundering predicates every other
// system reads.
func (s *Server) publishWeather(w *weatherState) {
	rain, thunder := w.levels()
	s.weatherRaining.Store(float64(rain) > weatherRainThreshold)
	s.weatherThunder.Store(float64(thunder) > weatherThunderThreshold)
}

// isThundering answers Level.isThundering for the overworld.
func (s *Server) isThundering() bool { return s.weatherThunder.Load() }

// weatherTickStep is advanceWeatherCycle for the overworld: with the
// advance_weather rule on, a running clear timer holds both flags off
// (and arms their timers so both flip the tick after it ends), else each
// timer counts down and toggles its flag at zero, or resamples — a
// duration while its weather is on, a delay while off. The levels then
// ease toward the flags; a changed level goes to the overworld's
// players, and crossing the 0.2 rain threshold sends start/stop raining
// with both levels to every player.
func (s *Server) weatherTickStep() {
	w := &s.weather
	w.mu.Lock()
	rainBefore, _ := w.levels()
	wasRaining := float64(rainBefore) > weatherRainThreshold
	if s.gameRules["minecraft:advance_weather"] == "true" {
		switch {
		case w.clear > 0:
			w.clear--
			w.thunder = boolTimer(!w.thundering)
			w.rain = boolTimer(!w.raining)
			w.thundering, w.raining = false, false
		default:
			if w.thunder > 0 {
				if w.thunder--; w.thunder == 0 {
					w.thundering = !w.thundering
				}
			} else if w.thundering {
				w.thunder = weatherSample(weatherThunderDuration)
			} else {
				w.thunder = weatherSample(weatherThunderDelay)
			}
			if w.rain > 0 {
				if w.rain--; w.rain == 0 {
					w.raining = !w.raining
				}
			} else if w.raining {
				w.rain = weatherSample(weatherRainDuration)
			} else {
				w.rain = weatherSample(weatherRainDelay)
			}
		}
	}
	w.oThunder = w.thunderLvl
	w.thunderLvl = easeWeather(w.thunderLvl, w.thundering)
	w.oRain = w.rainLevel
	w.rainLevel = easeWeather(w.rainLevel, w.raining)
	rainChanged, thunderChanged := w.oRain != w.rainLevel, w.oThunder != w.thunderLvl
	rain, thunder := w.rainLevel, w.thunderLvl
	rainNow, _ := w.levels()
	isRaining := float64(rainNow) > weatherRainThreshold
	s.publishWeather(w)
	w.mu.Unlock()

	if rainChanged {
		s.broadcastGameEventIn(dimOverworld, gameEventRainLevel, rain)
	}
	if thunderChanged {
		s.broadcastGameEventIn(dimOverworld, gameEventThunderLevel, thunder)
	}
	if wasRaining != isRaining {
		ev := byte(gameEventStartRaining)
		if wasRaining {
			ev = gameEventStopRaining
		}
		s.broadcastGameEventIn("", ev, 0)
		s.broadcastGameEventIn("", gameEventRainLevel, rain)
		s.broadcastGameEventIn("", gameEventThunderLevel, thunder)
	}
}

// boolTimer is the clear branch's timer arming: 1 for weather that is
// off (so it flips on next), 0 for weather that is on.
func boolTimer(off bool) int32 {
	if off {
		return 1
	}
	return 0
}

// easeWeather moves a level 0.01 toward on ? 1 : 0 in float32 and
// clamps it to [0, 1], the way vanilla accumulates it.
func easeWeather(v float32, on bool) float32 {
	if on {
		v += weatherLevelStep
	} else {
		v -= weatherLevelStep
	}
	return min(max(v, 0), 1)
}

// resetWeatherCycle is ServerLevel.resetWeatherCycle — a night skipped
// by sleeping ends the rain: both timers and both flags go to zero; the
// clear timer is left alone.
func (s *Server) resetWeatherCycle() {
	w := &s.weather
	w.mu.Lock()
	w.rain, w.raining = 0, false
	w.thunder, w.thundering = 0, false
	w.mu.Unlock()
}

// broadcastGameEventIn sends one game event to every spawned player,
// or only to those in dim when it is set (PlayerList.broadcastAll with
// and without a dimension).
func (s *Server) broadcastGameEventIn(dim string, event byte, value float32) {
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || !sess.spawned.Load() {
			return true
		}
		if dim != "" && sess.dimName() != dim {
			return true
		}
		_ = s.sendGameEvent(sess, event, value)
		return true
	})
}

// sendLevelWeather is sendLevelInfo's weather part: a player entering a
// raining level hears start raining with that level's rain and thunder
// levels. Only the overworld has weather.
func (s *Server) sendLevelWeather(sess *Session, dim string) error {
	if dim != dimOverworld {
		return nil
	}
	w := &s.weather
	w.mu.Lock()
	rain, thunder := w.levels()
	w.mu.Unlock()
	if float64(rain) <= weatherRainThreshold {
		return nil
	}
	if err := s.sendGameEvent(sess, gameEventStartRaining, 0); err != nil {
		return err
	}
	if err := s.sendGameEvent(sess, gameEventRainLevel, rain); err != nil {
		return err
	}
	return s.sendGameEvent(sess, gameEventThunderLevel, thunder)
}

// runWeather implements /weather <clear|rain|thunder> [duration] —
// MinecraftServer.setWeatherParameters(clear, weather, raining,
// thundering): clear → (d, 0, off, off), rain → (0, d, on, off), thunder
// → (0, d, on, on), the rain and thunder timers both taking the weather
// time. The duration is a TimeArgument (bare ticks, or an s/t/d suffix,
// at least 1); an omitted one samples the mode's provider — the rain
// delay for clear, the rain or thunder duration otherwise. Replies are
// the commands.weather.set.* keys, sent to the admin channel as well as
// the issuer like every state-changing vanilla command.
func (s *Server) runWeather(player string, args []string) reply {
	sess := s.findSessionByName(player)
	if sess == nil || sess.opLevel() < 2 {
		return reply{comp: unknownCommand(), styled: true, fail: &failMark{}}
	}
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 1)
	}
	var provider [2]int32
	switch args[0] {
	case "clear":
		provider = weatherRainDelay
	case "rain":
		provider = weatherRainDuration
	case "thunder":
		provider = weatherThunderDuration
	default:
		// The modes are brigadier literals — a bad word fails the
		// dispatch itself.
		return errTokenEnd("command.unknown.command", 1)
	}
	ticks := int32(-1)
	if len(args) > 1 {
		v, bad, ok := parseTimeArg(args[1])
		if !ok {
			return errTokenEnd("command.unknown.command", 1)
		}
		if v < 1 {
			return errTokenEnd("argument.time.tick_count_too_low", 2, int32(1), bad)
		}
		ticks = v
	}
	if len(args) > 2 {
		return errToken("command.unknown.argument", 3)
	}
	if ticks < 0 {
		ticks = weatherSample(provider)
	}
	w := &s.weather
	w.mu.Lock()
	switch args[0] {
	case "clear":
		w.clear, w.rain, w.thunder, w.raining, w.thundering = ticks, 0, 0, false, false
	case "rain":
		w.clear, w.rain, w.thunder, w.raining, w.thundering = 0, ticks, ticks, true, false
	default:
		w.clear, w.rain, w.thunder, w.raining, w.thundering = 0, ticks, ticks, true, true
	}
	w.mu.Unlock()
	msg := translatable("commands.weather.set." + args[0])
	return opsEchoReply(msg)
}
