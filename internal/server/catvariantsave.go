package server

import "strings"

// applyCatVariantSave restores the holder names serialized by the cat codec.
// Missing holders leave the existing value intact; a fresh cat projects the
// defined black/classic defaults through the ordinary pairing/save helpers.
func applyCatVariantSave(e *Entity, save map[string]any) {
	variant, sound := e.VariantName, e.SoundVariant
	if name, ok := save["variant"].(string); ok {
		name = strings.TrimPrefix(name, "minecraft:")
		if _, known := holderVariantIDs["cat"][name]; known {
			variant = name
		}
	}
	if name, ok := save["sound_variant"].(string); ok {
		name = strings.TrimPrefix(name, "minecraft:")
		for _, known := range spawnSoundVariantNames["cat"] {
			if name == known {
				sound = name
				break
			}
		}
	}
	if variant != e.VariantName || sound != e.SoundVariant {
		e.MetaDirty = true
	}
	e.VariantName, e.SoundVariant = variant, sound
}
