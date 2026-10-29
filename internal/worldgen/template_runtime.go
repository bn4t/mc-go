package worldgen

import "mc-go/internal/world"

// BuiltinTemplateNBT returns a private copy of the bundled native template.
// Runtime structure blocks share the same measured assets as world generation.
func BuiltinTemplateNBT(id string) ([]byte, bool) {
	raw, ok := templateNBT(id)
	if !ok {
		return nil, false
	}
	return append([]byte(nil), raw...), true
}

// TransformStructureState applies the measured block-state transformations
// already shared by template-based world generation (mirror before rotation).
func TransformStructureState(state world.Block, mirror, rotation int) world.Block {
	return legacyRotate(legacyMirror(state, mirror), rotation)
}

// TemplateRandomizesLoot reports the existing measured RandomizableContainer
// classification used by native template placement.
func TemplateRandomizesLoot(state world.Block) bool { return isRandomizable(state) }
