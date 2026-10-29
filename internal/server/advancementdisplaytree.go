package server

import (
	"bytes"
	"fmt"

	"mc-go/internal/data"
	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
)

func finishAdvancementDisplayWires(registry *advancementRegistry) {
	roots := map[string]bool{}
	markRoot := func(id string) {
		if !registry.attached[id] {
			return
		}
		root := id
		for registry.entries[root].parent != "" {
			root = registry.entries[root].parent
		}
		roots[root] = true
	}
	for id := range registry.custom {
		markRoot(id)
		// Moving or hiding an existing node also changes the original tab.
		if original := gameAdvs[id]; original != nil {
			root := id
			for gameAdvs[root] != nil && gameAdvs[root].parent != "" {
				root = gameAdvs[root].parent
			}
			markRoot(root)
		}
	}
	for root := range roots {
		// A displayless root prevents layout altogether. Clear captured positions
		// as well as custom positions first, so descendants do not retain a stale
		// built-in drawing when their root is overridden without a display.
		points := map[string]advancementLayoutPoint{}
		var clearTree func(string)
		clearTree = func(id string) {
			if registry.entries[id].hasDisplay {
				points[id] = advancementLayoutPoint{}
			}
			for _, child := range registry.children[id] {
				clearTree(child)
			}
		}
		clearTree(root)
		for id, point := range layoutAdvancementTree(root, registry.entries, registry.children) {
			points[id] = point
		}
		for id, point := range points {
			meta := registry.entries[id]
			if meta.display != nil {
				meta.display.x, meta.display.y = point.x, point.y
				continue
			}
			// Existing built-in definitions retain their captured component/icon
			// codecs; only the affected tab's coordinates change.
			if captured := data.AdvDefWire[id]; captured != nil {
				if rewritten, err := advancementDefinitionCoordinates(captured, point); err == nil {
					registry.wire[id] = rewritten
				}
			}
		}
	}
	for id := range registry.custom {
		registry.wire[id] = encodePackAdvancement(registry.entries[id])
	}
}

func advancementDefinitionCoordinates(body []byte, point advancementLayoutPoint) ([]byte, error) {
	r := protocol.NewPacketReader(body)
	if _, err := r.String(); err != nil {
		return nil, err
	}
	parent, err := r.Bool()
	if err != nil {
		return nil, err
	}
	if parent {
		if _, err = r.String(); err != nil {
			return nil, err
		}
	}
	display, err := r.Bool()
	if err != nil || !display {
		return nil, fmt.Errorf("advancement definition has no display")
	}
	for range 2 {
		offset := len(body) - r.Remaining()
		source := bytes.NewReader(body[offset:])
		if _, err := nbt.ReadUnnamed(source); err != nil {
			return nil, err
		}
		if _, err = r.Read(len(body) - offset - source.Len()); err != nil {
			return nil, err
		}
	}
	profile, ok := javaProfileFor(776)
	if !ok {
		return nil, fmt.Errorf("missing canonical Java profile")
	}
	var item protocol.Buffer
	if err = profile.projectItemTemplateFrom776(r, &item); err != nil {
		return nil, err
	}
	if _, err = r.VarInt(); err != nil {
		return nil, err
	}
	flags, err := r.Int()
	if err != nil {
		return nil, err
	}
	if flags&1 != 0 {
		if _, err = r.String(); err != nil {
			return nil, err
		}
	}
	offset := len(body) - r.Remaining()
	if _, err = r.Read(8); err != nil {
		return nil, err
	}
	out := append([]byte(nil), body...)
	var coords protocol.Buffer
	coords.Float(point.x)
	coords.Float(point.y)
	copy(out[offset:offset+8], coords.Bytes())
	return out, nil
}
