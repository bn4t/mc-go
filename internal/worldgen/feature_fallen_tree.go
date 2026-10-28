package worldgen

// Fallen trees, measured from FallenTreeFeature (26.2): a one-block
// stump at the origin, then, if a whole log fits, a sideways log of the
// sampled length less two lying two or three blocks away on solid
// ground. Each part runs its decorators over its own logs.

func init() {
	featureKinds["minecraft:fallen_tree"] = decodeFallenTree
	treeDecorators["minecraft:attached_to_logs"] = func(cfg any) treeDecorator {
		a := attachedToLogs{probability: ffloat(ffield(cfg, "probability"), 0),
			provider: decodeStateProvider(ffield(cfg, "block_provider"))}
		for _, d := range flist(ffield(cfg, "directions")) {
			a.directions = append(a.directions, directionNames[fstr(d)])
		}
		return a
	}
}

// attachedToLogs sets a block on a random side of each log, in a
// shuffled log order, where the side is air.
type attachedToLogs struct {
	probability float32
	provider    stateProvider
	directions  []int
}

func (a attachedToLogs) place(t *tree) {
	rand := t.rand
	logs := sortedByY(t.logs)
	shufflePoints(rand, logs)
	for _, l := range logs {
		q := l.offset(a.directions[rand.NextIntn(int32(len(a.directions)))])
		if rand.NextFloat() <= a.probability && t.isAir(q) {
			t.setDecoration(q, a.provider.get(t.r, rand, q))
		}
	}
}

type fallenTree struct {
	trunk           stateProvider
	length          intProvider
	stumpDecorators []treeDecorator
	logDecorators   []treeDecorator
}

func decodeFallenTree(cfg any) func(*featureCtx) bool {
	f := &fallenTree{trunk: decodeStateProvider(ffield(cfg, "trunk_provider")),
		length: decodeIntProvider(ffield(cfg, "log_length"))}
	decorators := func(key string) (l []treeDecorator) {
		for _, d := range flist(ffield(cfg, key)) {
			md, ok := treeDecorators[ftype(d)]
			if !ok {
				unsupported("tree decorator %s", ftype(d))
			}
			l = append(l, md(d))
		}
		return
	}
	f.stumpDecorators, f.logDecorators = decorators("stump_decorators"), decorators("log_decorators")
	return f.place
}

func (f *fallenTree) decorate(fc *featureCtx, logs *posSet, decorators []treeDecorator) {
	if len(decorators) == 0 {
		return
	}
	t := &tree{r: fc.r, rand: fc.rand, roots: &posSet{}, logs: logs, leaves: &posSet{}, decorations: &posSet{}}
	for _, d := range decorators {
		d.place(t)
	}
}

// overSolidGround is whether the block below has a sturdy top face.
func overSolidGround(r *decoRegion, p Point) bool { return r.get(p.offset(dirDown)).sturdy(dirUp) }

func (f *fallenTree) place(fc *featureCtx) bool {
	r, random, o := fc.r, fc.rand, fc.origin
	r.set(o, f.trunk.get(r, random, o))
	stump := &posSet{}
	stump.add(o)
	f.decorate(fc, stump, f.stumpDecorators)

	dir := horizontalDirs[random.NextIntn(4)]
	length := f.length.sample(random) - 2
	p := o.add(scaleDir(dir, 2+random.NextIntn(2)))
	// Settle the start on ground: from one above, at most six steps down.
	p = p.offset(dirUp)
	for range 6 {
		if validTreePos(r, p) && overSolidGround(r, p) {
			break
		}
		p = p.offset(dirDown)
	}
	gap := 0
	for i := int32(0); i < length; i++ {
		q := p.add(scaleDir(dir, i))
		if !validTreePos(r, q) {
			return true
		}
		if overSolidGround(r, q) {
			gap = 0
		} else if gap++; gap > 2 {
			return true
		}
	}
	logs := &posSet{}
	axis := [3]string{"y", "z", "x"}[dirAxis(dir)]
	for i := int32(0); i < length; i++ {
		q := p.add(scaleDir(dir, i))
		s := f.trunk.get(r, random, q)
		if s.hasProp("axis") {
			s = s.with("axis", axis)
		}
		r.set(q, s)
		logs.add(q)
	}
	f.decorate(fc, logs, f.logDecorators)
	return true
}
