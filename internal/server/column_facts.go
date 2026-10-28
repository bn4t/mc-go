package server

import "mc-go/internal/world"

// analyzedColumnFacts contains independent summaries of one detached canonical
// column. Counts use exact Air identity, section flags use block-name rules,
// and sky tops use blocksSky; none of these predicates substitutes for another.
type analyzedColumnFacts struct {
	sectionFlags []uint8
	nonEmpty     []int
	skyTops      [256]int32
}

// analyzeColumnFacts reads a whole number of sections in FillBox order (x, z,
// then y). It retains no input references and touches no live world or caches.
func analyzeColumnFacts(states []world.Block, minY int) analyzedColumnFacts {
	facts := analyzedColumnFacts{
		sectionFlags: make([]uint8, len(states)/4096),
		nonEmpty:     make([]int, len(states)/4096),
	}
	emptyTop := int32(minY - 1)
	for i := range facts.skyTops {
		facts.skyTops[i] = emptyTop
	}
	// Repeated states share immutable classification during this analysis.
	// The extra bit is private to the scan, outside the section flag bits.
	const skyBlocking uint8 = 1 << 2
	classified := make(map[world.Block]uint8)
	rules := randRules()
	for i := len(states) - 1; i >= 0; i-- {
		b := states[i]
		if b == world.Air {
			continue
		}
		sec := i / 4096
		facts.nonEmpty[sec]++
		bits, known := classified[b]
		if !known {
			if name, ok := world.StateOwner(b); ok {
				if _, ok := rules[name]; ok {
					bits |= secTickable
				}
				if !lightPassable[name] {
					bits |= secOpaque
				}
			}
			if blocksSky(b) {
				bits |= skyBlocking
			}
			classified[b] = bits
		}
		facts.sectionFlags[sec] |= bits & (secTickable | secOpaque)
		column := i % 256
		if bits&skyBlocking != 0 && facts.skyTops[column] == emptyTop {
			facts.skyTops[column] = int32(minY + i/256)
		}
	}
	return facts
}

// prepareColumnFactsOwned fuses cold skylight-column preparation under the
// simulation owner. Existing light entries remain the authoritative running
// values: writes can have updated them independently, so install only absent
// entries. Publication takes lightMu before randMu; section-only lookups never
// retain randMu while acquiring light state.
func (s *Server) prepareColumnFactsOwned(d *dimension, cx, cz int) {
	if !d.skylight {
		return
	}
	d.lightMu.Lock()
	defer d.lightMu.Unlock()
	ck := [2]int{cx, cz}
	_, countsWarm := d.nonEmptyCnt[ck]
	_, topsWarm := d.skyTops[ck]
	if countsWarm && topsWarm {
		return
	}
	facts := analyzeColumnFacts(d.chunkBlocks(cx, cz), d.minY)
	if !countsWarm {
		if d.nonEmptyCnt == nil {
			d.nonEmptyCnt = make(map[[2]int]map[int]int)
		}
		counts := make(map[int]int, len(facts.nonEmpty))
		for sec, count := range facts.nonEmpty {
			counts[sec] = count
		}
		d.nonEmptyCnt[ck] = counts
	}
	if !topsWarm {
		if d.skyTops == nil {
			d.skyTops = make(map[[2]int]*[256]int32)
		}
		tops := facts.skyTops
		d.skyTops[ck] = &tops
	}
	s.randMu.Lock()
	defer s.randMu.Unlock()
	if s.randSections == nil {
		return
	}
	for sec, flags := range facts.sectionFlags {
		key := randSectionKey{dim: d.name, x: cx, y: sec, z: cz}
		if _, warm := s.randSections[key]; !warm {
			s.randSections[key] = flags
		}
	}
}
