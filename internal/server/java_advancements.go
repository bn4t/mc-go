package server

import (
	"bytes"
	"fmt"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
)

// Generated advancement definitions contain one ItemTemplate inside display
// information. Everything else is names, components and non-registry scalars;
// keep those bytes intact and project that template exactly once per viewer.
func (p *javaProfile) advancementDefinitionFrom776(body []byte) ([]byte, error) {
	if p.version.Protocol == 776 {
		return body, nil
	}
	r := bytes.NewReader(body)
	if _, err := protocol.ReadString(r); err != nil {
		return nil, err
	}
	parent, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	if parent > 1 {
		return nil, fmt.Errorf("invalid advancement parent flag")
	}
	if parent == 1 {
		if _, err = protocol.ReadString(r); err != nil {
			return nil, err
		}
	}
	display, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	if display > 1 {
		return nil, fmt.Errorf("invalid advancement display flag")
	}
	if display == 0 {
		return body, nil
	}
	for range 2 {
		if _, err = nbt.ReadUnnamed(r); err != nil {
			return nil, err
		}
	}
	offset := len(body) - r.Len()
	item := protocol.NewPacketReader(body[offset:])
	var b protocol.Buffer
	b.Raw(body[:offset])
	if err = p.projectItemTemplateFrom776(item, &b); err != nil {
		return nil, err
	}
	b.Raw(body[len(body)-item.Remaining():])
	return b.Bytes(), nil
}
