package server

import (
	"math"
	"strings"

	"mc-go/internal/world"
)

// Entity.checkInsideBlocks runs once per entity per tick: every cell
// the entity's box overlaps gets its block's inside effect. Tripwire
// is the modeled family — a wire's collision shape is a full-cell
// column 2.5px tall when attached (strung between hooks) and 8px when
// loose.
//
// Spectator-mode players are isIgnoringBlockTriggers and never press.

// entityDims is the (width, height) box for the entity types mc-go
// spawns where it differs from the 0.6 x 1.8 default.
var entityDims = map[string][2]float64{
	"item":             {0.25, 0.25},
	"experience_orb":   {0.5, 0.5},
	"arrow":            {0.5, 0.5},
	"trident":          {0.5, 0.5},
	"armor_stand":      {0.5, 1.975},
	"bat":              {0.5, 0.9},
	"bee":              {0.7, 0.6},
	"chicken":          {0.4, 0.7},
	"cod":              {0.5, 0.3},
	"salmon":           {0.7, 0.4},
	"pufferfish":       {0.7, 0.7},
	"tropical_fish":    {0.5, 0.4},
	"tadpole":          {0.4, 0.3},
	"silverfish":       {0.4, 0.3},
	"endermite":        {0.4, 0.3},
	"cave_spider":      {0.7, 0.5},
	"spider":           {1.4, 0.9},
	"pig":              {0.9, 0.9},
	"sheep":            {0.9, 1.3},
	"cow":              {0.9, 1.4},
	"mooshroom":        {0.9, 1.4},
	"rabbit":           {0.4, 0.5},
	"fox":              {0.6, 0.7},
	"wolf":             {0.6, 0.85},
	"cat":              {0.6, 0.7},
	"ocelot":           {0.6, 0.7},
	"parrot":           {0.5, 0.9},
	"horse":            {1.3965, 1.6},
	"donkey":           {1.3965, 1.5},
	"mule":             {1.3965, 1.52},
	"llama":            {0.9, 1.87},
	"camel":            {1.7, 2.375},
	"creeper":          {0.6, 1.7},
	"skeleton":         {0.6, 1.99},
	"stray":            {0.6, 1.99},
	"wither_skeleton":  {0.7, 2.4},
	"zombie":           {0.6, 1.95},
	"husk":             {0.6, 1.95},
	"drowned":          {0.6, 1.95},
	"zombie_villager":  {0.6, 1.95},
	"zombified_piglin": {0.6, 1.95},
	"skeleton_horse":   {1.3965, 1.6},
	"zombie_horse":     {1.3965, 1.6},
	"villager":         {0.6, 1.95},
	"wandering_trader": {0.6, 1.95},
	"pillager":         {0.6, 1.95},
	"vindicator":       {0.6, 1.95},
	"evoker":           {0.6, 1.95},
	"witch":            {0.6, 1.95},
	"ravager":          {1.95, 2.2},
	"blaze":            {0.6, 1.8},
	"magma_cube":       {0.52, 0.52},
	"slime":            {0.52, 0.52},
	"turtle":           {1.2, 0.4},
	"axolotl":          {0.75, 0.42},
	"frog":             {0.5, 0.5},
	"allay":            {0.35, 0.6},
	"enderman":         {0.6, 2.9},
	"iron_golem":       {1.4, 2.7},
	"snow_golem":       {0.7, 1.9},
	"ghast":            {4.0, 4.0},
	"wither":           {0.9, 3.5},
	"ender_dragon":     {16.0, 8.0},
	"warden":           {0.9, 2.9},
	"sniffer":          {1.9, 1.75},
	"shulker":          {1.0, 1.0},
	"phantom":          {0.9, 0.5},
	"boat":             {1.375, 0.5625},
	"oak_boat":         {1.375, 0.5625},
	"minecart":         {0.98, 0.7},
}

// entityBox returns an entity's (width, height); 0.6 x 1.8 (player
// size) when the type isn't in the table.
func entityBox(name string) (w, h float64) {
	if d, ok := entityDims[name]; ok {
		return d[0], d[1]
	}
	return 0.6, 1.8
}

// entityInsideStep is Entity.checkInsideBlocks over every loaded
// entity plus each session's player entity.
func (s *Server) entityInsideStep() {
	spectator := map[int32]bool{}
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.gameMode == 3 {
			if eid := sess.entityID.Load(); eid != 0 {
				spectator[eid] = true
			}
		}
		return true
	})
	for _, e := range s.entities.All() {
		if spectator[e.ID] {
			continue
		}
		d := s.dims[e.Dim]
		if d == nil {
			continue
		}
		name, _ := EntityTypeName(e.Type)
		w, h := entityBox(name)
		minX := int(math.Floor(e.X - float64(w)/2))
		maxX := int(math.Floor(e.X + float64(w)/2))
		minY := int(math.Floor(e.Y))
		maxY := int(math.Floor(e.Y + float64(h)))
		minZ := int(math.Floor(e.Z - float64(w)/2))
		maxZ := int(math.Floor(e.Z + float64(w)/2))
		for x := minX; x <= maxX; x++ {
			for y := minY; y <= maxY; y++ {
				for z := minZ; z <= maxZ; z++ {
					pos := world.Pos{X: x, Y: y, Z: z}
					b := d.w.GetBlock(pos)
					bn, ok := world.StateOwner(b)
					if !ok || strings.TrimPrefix(bn, "minecraft:") != "tripwire" {
						continue
					}
					// The inside shape only covers the column's low
					// band — 0.15625 attached, 0.5 loose.
					band := 0.5
					if at, _ := world.StateProperty(b, "attached"); at == "true" {
						band = 0.15625
					}
					if e.Y >= float64(y)+band || e.Y+float64(h) <= float64(y) {
						continue
					}
					s.tripwireInside(d, pos, b)
				}
			}
		}
	}
}

// tripwireInside is TripWireBlock.entityInside: an unpowered wire
// with no pending recheck presses.
func (s *Server) tripwireInside(d *dimension, pos world.Pos, b world.Block) {
	if p, _ := world.StateProperty(b, "powered"); p == "true" {
		return
	}
	if s.ticks != nil && s.ticks.isQueued(schedKey{dim: d.name, pos: pos, kind: tickBlock}) {
		return
	}
	s.tripwireSetPressed(d, pos, true)
}

// tripwireSetPressed is TripWireBlock.checkPressed: flip powered when
// it disagrees, walk the run's hooks, and arm the 10-tick recheck
// while pressed.
func (s *Server) tripwireSetPressed(d *dimension, pos world.Pos, pressed bool) {
	b := d.w.GetBlock(pos)
	name, ok := world.StateOwner(b)
	if !ok || strings.TrimPrefix(name, "minecraft:") != "tripwire" {
		return
	}
	was, _ := world.StateProperty(b, "powered")
	if (was == "true") != pressed {
		nb, ok := world.WithProperty(b, "powered", boolStr(pressed))
		if ok {
			s.setNaturalBlock(d, pos, nb)
			s.tripwireUpdateSource(d, pos, nb)
		}
	}
	if pressed {
		s.scheduleBlockTick(d, pos, 10)
	}
}

// tripwireUpdateSource is TripWireBlock.updateSource: from the pressed
// wire, walk south and west through wires; a hook facing back at the
// run recalculates.
func (s *Server) tripwireUpdateSource(d *dimension, pos world.Pos, _ world.Block) {
	for _, dir := range [2]string{"south", "west"} {
		for i := 1; i < 42; i++ {
			hp := pos
			for k := 0; k < i; k++ {
				hp = shiftPos(hp, dir)
			}
			hb := d.w.GetBlock(hp)
			hn, _ := world.StateOwner(hb)
			hn = strings.TrimPrefix(hn, "minecraft:")
			if hn == "tripwire_hook" {
				f, _ := world.StateProperty(hb, "facing")
				if f == dirOpposite(dir) {
					s.hookRecalc(d, hp, f)
				}
				break
			}
			if hn != "tripwire" {
				break
			}
		}
	}
}

// tripwireEntities returns whether any non-spectator entity still
// overlaps the wire's inside band — the tick's entity scan.
func (s *Server) tripwireEntities(d *dimension, pos world.Pos, b world.Block) bool {
	band := 0.5
	if at, _ := world.StateProperty(b, "attached"); at == "true" {
		band = 0.15625
	}
	spectator := map[int32]bool{}
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.gameMode == 3 {
			if eid := sess.entityID.Load(); eid != 0 {
				spectator[eid] = true
			}
		}
		return true
	})
	for _, e := range s.entities.All() {
		if spectator[e.ID] || e.Dim != d.name {
			continue
		}
		name, _ := EntityTypeName(e.Type)
		w, h := entityBox(name)
		if e.X+float64(w)/2 <= float64(pos.X) || e.X-float64(w)/2 >= float64(pos.X+1) {
			continue
		}
		if e.Z+float64(w)/2 <= float64(pos.Z) || e.Z-float64(w)/2 >= float64(pos.Z+1) {
			continue
		}
		if e.Y >= float64(pos.Y)+band || e.Y+float64(h) <= float64(pos.Y) {
			continue
		}
		return true
	}
	return false
}

// tripwireTick is TripWireBlock.tick: a powered wire re-scans; empty
// means release, pressed means reschedule.
func (s *Server) tripwireTick(d *dimension, pos world.Pos, b world.Block) {
	if p, _ := world.StateProperty(b, "powered"); p != "true" {
		return
	}
	s.tripwireSetPressed(d, pos, s.tripwireEntities(d, pos, b))
}
