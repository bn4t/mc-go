package server

import (
	"bytes"
	"fmt"
	"io"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
)

// Network NBT is validated before the general tree decoder sees it. Counts,
// nesting and raw byte consumption stay bounded by the enclosing packet.
func (t *slotProjector) nbt(r *protocol.PacketReader, b *protocol.Buffer, named bool, depth int) error {
	tag, err := r.Byte()
	if err != nil {
		return err
	}
	b.Byte(tag)
	if tag == 0 {
		return nil
	}
	if named {
		if err = t.nbtString(r, b); err != nil {
			return err
		}
	}
	return t.nbtValue(r, b, tag, depth)
}
func (t *slotProjector) nbtString(r *protocol.PacketReader, b *protocol.Buffer) error {
	n, err := r.UShort()
	if err != nil {
		return err
	}
	raw, err := r.Read(int(n))
	if err != nil {
		return err
	}
	b.UShort(n)
	b.Raw(raw)
	return nil
}
func (t *slotProjector) nbtValue(r *protocol.PacketReader, b *protocol.Buffer, tag byte, depth int) error {
	if err := t.enter(depth); err != nil {
		return err
	}
	fixed := func(n int) error {
		if n < 0 || n > r.Remaining() {
			return fmt.Errorf("slot: truncated NBT field")
		}
		raw, err := r.Read(n)
		if err != nil {
			return err
		}
		b.Raw(raw)
		return nil
	}
	switch tag {
	case 1:
		return fixed(1)
	case 2:
		return fixed(2)
	case 3, 5:
		return fixed(4)
	case 4, 6:
		return fixed(8)
	case 7, 11, 12:
		n, err := r.Int()
		if err != nil {
			return err
		}
		width := int64(1)
		if tag == 11 {
			width = 4
		} else if tag == 12 {
			width = 8
		}
		if n < 0 || int64(n)*width > int64(r.Remaining()) {
			return fmt.Errorf("slot: invalid NBT array count %d", n)
		}
		b.Int(n)
		return fixed(int(int64(n) * width))
	case 8:
		return t.nbtString(r, b)
	case 9:
		kind, err := r.Byte()
		if err != nil {
			return err
		}
		n, err := r.Int()
		if err != nil {
			return err
		}
		if n < 0 || n > 4096 || int64(n) > int64(r.Remaining()) || kind > 12 || kind == 0 && n != 0 {
			return fmt.Errorf("slot: invalid NBT list")
		}
		b.Byte(kind)
		b.Int(n)
		for i := int32(0); i < n; i++ {
			if err = t.nbtValue(r, b, kind, depth+1); err != nil {
				return err
			}
		}
		return nil
	case 10:
		for {
			kind, err := r.Byte()
			if err != nil {
				return err
			}
			b.Byte(kind)
			if kind == 0 {
				return nil
			}
			if err = t.nbtString(r, b); err != nil {
				return err
			}
			if err = t.nbtValue(r, b, kind, depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("slot: invalid NBT tag %d", tag)
	}
}
func (t *slotProjector) readNBT(r *protocol.PacketReader, named bool, depth int) (any, error) {
	var encoded protocol.Buffer
	if err := t.nbt(r, &encoded, named, depth); err != nil {
		return nil, err
	}
	if named {
		_, value, err := nbt.ReadNamed(bytes.NewReader(encoded.Bytes()))
		return value, err
	}
	return nbt.ReadUnnamed(bytes.NewReader(encoded.Bytes()))
}

type slotNBTReader struct{ r *protocol.PacketReader }

func (s slotNBTReader) Read(dst []byte) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	n := len(dst)
	if n > s.r.Remaining() {
		n = s.r.Remaining()
	}
	if n == 0 {
		return 0, io.EOF
	}
	raw, err := s.r.Read(n)
	copy(dst, raw)
	return n, err
}
