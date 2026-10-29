package server

import (
	"strings"

	"mc-go/internal/world"
)

func chestHingeSound(block world.Block, opening bool) string {
	name, _ := world.StateOwner(block)
	base := "block.chest"
	if name == "copper_chest" || strings.HasSuffix(name, "_copper_chest") {
		base = "block.copper_chest"
		if strings.Contains(name, "weathered_") {
			base += "_weathered"
		} else if strings.Contains(name, "oxidized_") {
			base += "_oxidized"
		}
	}
	if opening {
		return base + ".open"
	}
	return base + ".close"
}
