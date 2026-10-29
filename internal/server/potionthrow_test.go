package server

import "testing"

// PotionContents.getColorOptional weights each visible RGB by amplifier+1.
// The 26.2 turtle-master registry carries slowness IV and resistance III:
// colors 9154528 and 9520880, yielding opaque 0xff8d82e6.
func TestPotionMixedColor(t *testing.T) {
	want := int32(-7503130)
	if got := splashPotionColors["turtle_master"]; got != want {
		t.Fatalf("turtle master splash color %08x, want %08x", uint32(got), uint32(want))
	}
	slow, _ := MobEffectID("slowness")
	resist, _ := MobEffectID("resistance")
	effects := map[int32]mobEffectInst{slow: {amplifier: 3}, resist: {amplifier: 2}}
	if got := cloudParticleARGB(effects); got != want {
		t.Fatalf("turtle master cloud color %08x, want %08x", uint32(got), uint32(want))
	}
}
