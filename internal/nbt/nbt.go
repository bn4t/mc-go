// Package nbt is a clean-room Named Binary Tag codec, implemented from
// the public spec (https://minecraft.wiki/w/NBT_format). NBT carries
// Registry Data, chunk sections, and item components, so this is the
// foundation of the registry-data milestone.
//
// Value model (all tags covered):
//
//	int8, int16, int32, int64, float32, float64,
//	[]byte (ByteArray), string, List{Elem, Items []any},
//	map[string]any (Compound), []int32 (IntArray), []int64 (LongArray)
package nbt

import (
	"errors"
	"io"
	"math"
)

// Tag type ids.
const (
	TagEnd       byte = 0
	TagByte      byte = 1
	TagShort     byte = 2
	TagInt       byte = 3
	TagLong      byte = 4
	TagFloat     byte = 5
	TagDouble    byte = 6
	TagByteArray byte = 7
	TagString    byte = 8
	TagList      byte = 9
	TagCompound  byte = 10
	TagIntArray  byte = 11
	TagLongArray byte = 12
)

// List is a homogeneous NBT list.
type List struct {
	Elem  byte
	Items []any
}

var (
	ErrUnknownTag = errors.New("nbt: unknown tag")
	ErrBadString  = errors.New("nbt: bad string")
	ErrTruncated  = errors.New("nbt: truncated")
)

const maxString = 1 << 20 // 1 MiB cap for a single string/array

// WriteNamed writes a named root tag: type + name + payload.
func WriteNamed(w io.Writer, name string, v any) error {
	tag, err := tagOf(v)
	if err != nil {
		return err
	}
	if err := writeByte(w, tag); err != nil {
		return err
	}
	if err := writeString(w, name); err != nil {
		return err
	}
	return writePayload(w, tag, v)
}

// WriteUnnamed writes a packet-style NBT value: type byte + payload,
// no name. This is the "NBT Tag" protocol type used by Registry Data.
func WriteUnnamed(w io.Writer, v any) error {
	tag, err := tagOf(v)
	if err != nil {
		return err
	}
	if err := writeByte(w, tag); err != nil {
		return err
	}
	return writePayload(w, tag, v)
}

// ReadUnnamed reads a packet-style NBT value.
func ReadUnnamed(r io.Reader) (any, error) {
	tag, err := readByte(r)
	if err != nil {
		return nil, err
	}
	if !validTag(tag) {
		return nil, ErrUnknownTag
	}
	return readPayload(r, tag)
}

// ReadNamed reads a named root tag.
func ReadNamed(r io.Reader) (string, any, error) {
	tag, err := readByte(r)
	if err != nil {
		return "", nil, err
	}
	name, err := readString(r)
	if err != nil {
		return "", nil, err
	}
	v, err := readPayload(r, tag)
	return name, v, err
}

func tagOf(v any) (byte, error) {
	switch v.(type) {
	case int8:
		return TagByte, nil
	case int16:
		return TagShort, nil
	case int32:
		return TagInt, nil
	case int64:
		return TagLong, nil
	case float32:
		return TagFloat, nil
	case float64:
		return TagDouble, nil
	case []byte:
		return TagByteArray, nil
	case string:
		return TagString, nil
	case List:
		return TagList, nil
	case map[string]any:
		return TagCompound, nil
	case []int32:
		return TagIntArray, nil
	case []int64:
		return TagLongArray, nil
	}
	return 0, ErrUnknownTag
}

func writePayload(w io.Writer, tag byte, v any) error {
	switch tag {
	case TagByte:
		return writeByte(w, byte(v.(int8)))
	case TagShort:
		return writeUint(w, 2, uint64(uint16(v.(int16))))
	case TagInt:
		return writeUint(w, 4, uint64(uint32(v.(int32))))
	case TagLong:
		return writeUint(w, 8, uint64(v.(int64)))
	case TagFloat:
		return writeUint(w, 4, uint64(math.Float32bits(v.(float32))))
	case TagDouble:
		return writeUint(w, 8, math.Float64bits(v.(float64)))
	case TagByteArray:
		b := v.([]byte)
		if err := writeUint(w, 4, uint64(len(b))); err != nil {
			return err
		}
		_, err := w.Write(b)
		return err
	case TagString:
		return writeString(w, v.(string))
	case TagList:
		l := v.(List)
		if err := writeByte(w, l.Elem); err != nil {
			return err
		}
		if err := writeUint(w, 4, uint64(len(l.Items))); err != nil {
			return err
		}
		for _, it := range l.Items {
			if err := writePayload(w, l.Elem, it); err != nil {
				return err
			}
		}
		return nil
	case TagCompound:
		for name, item := range v.(map[string]any) {
			t, err := tagOf(item)
			if err != nil {
				return err
			}
			if err := writeByte(w, t); err != nil {
				return err
			}
			if err := writeString(w, name); err != nil {
				return err
			}
			if err := writePayload(w, t, item); err != nil {
				return err
			}
		}
		return writeByte(w, TagEnd)
	case TagIntArray:
		a := v.([]int32)
		if err := writeUint(w, 4, uint64(len(a))); err != nil {
			return err
		}
		for _, n := range a {
			if err := writeUint(w, 4, uint64(uint32(n))); err != nil {
				return err
			}
		}
		return nil
	case TagLongArray:
		a := v.([]int64)
		if err := writeUint(w, 4, uint64(len(a))); err != nil {
			return err
		}
		for _, n := range a {
			if err := writeUint(w, 8, uint64(n)); err != nil {
				return err
			}
		}
		return nil
	}
	return ErrUnknownTag
}
