package server

// These measured LivingEntity overrides matter before the water-animal
// handleAirSupply override: NoAI suppresses that latter step for axolotls and
// nautiluses, while LivingEntity's ordinary refill still runs. Evidence:
// OpenJDK25.0.5 javap,26.2 Axolotl/Nautilus/Dolphin + water potion wire pin.
func mobMaxAirSupply(name string) int16 {
	switch name {
	case "axolotl":
		return 6000
	case "dolphin":
		return 4800
	default:
		return 300
	}
}
func mobIncreaseAirSupply(name string, current int16) int16 {
	if name == "dolphin" {
		return mobMaxAirSupply(name)
	}
	return int16(min(int32(current)+4, int32(mobMaxAirSupply(name))))
}
