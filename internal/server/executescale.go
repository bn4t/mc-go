package server

// A source crossing dimensions retains its Y, rotation, anchor and executor.
// X/Z use the level coordinate-scale ratio, independently of player travel.
func setExecuteDimension(ctx *executeContext, destination *dimension) {
	if ctx.dim != nil && destination != nil && ctx.dim != destination {
		factor := ctx.dim.coordScale / destination.coordScale
		ctx.pos[0] *= factor
		ctx.pos[2] *= factor
	}
	ctx.dim = destination
}
