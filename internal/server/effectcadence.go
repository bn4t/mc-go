package server

// Periodic effect amplifiers use Java's masked int shift. Once the period
// becomes zero the effect runs every tick, including large amplifiers.
func effectPeriodicTickDue(period int32, amplifier int32, duration int64) bool {
	interval := period >> (uint32(amplifier) & 31)
	return interval <= 0 || duration%int64(interval) == 0
}
