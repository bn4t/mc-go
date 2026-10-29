package server

import "mc-go/internal/protocol"

// Entity's DATA_NO_GRAVITY is index5, Boolean serializer8. Pairing
// packs its non-default value without consuming the pending dirty set.
func entityNoGravityPack(b *protocol.Buffer, e *Entity) {
	if e.NoGravity {
		b.Byte(5)
		b.Byte(8)
		b.Bool(true)
	}
}

func (e *Entity) hasNoGravityMetadataChange() bool {
	return e.noGravityDirty || e.NoGravity != e.noGravitySent
}

func (e *Entity) setNoGravity(value bool) {
	if e.NoGravity != value {
		e.NoGravity = value
		e.noGravityDirty = true
	}
}

func entityNoGravityDirty(b *protocol.Buffer, e *Entity) {
	if !e.hasNoGravityMetadataChange() {
		return
	}
	b.Byte(5)
	b.Byte(8)
	b.Bool(e.NoGravity)
	e.noGravitySent = e.NoGravity
	e.noGravityDirty = false
}

// Family payloads already have their terminator; an otherwise empty
// family still needs one when the base flag is its only metadata.
func withEntityNoGravityMetadata(body []byte, e *Entity, dirty bool) []byte {
	var b protocol.Buffer
	if dirty {
		entityNoGravityDirty(&b, e)
	} else {
		entityNoGravityPack(&b, e)
	}
	if b.Len() == 0 {
		return body
	}
	if len(body) == 0 {
		b.Byte(0xff)
	} else {
		b.Raw(body)
	}
	return b.Bytes()
}
