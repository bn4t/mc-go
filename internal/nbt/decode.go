package nbt

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
)

func readPayload(r io.Reader, tag byte) (any, error) {
	switch tag {
	case TagByte:
		b, err := readByte(r)
		return int8(b), err
	case TagShort:
		n, err := readUint(r, 2)
		return int16(n), err
	case TagInt:
		n, err := readUint(r, 4)
		return int32(n), err
	case TagLong:
		n, err := readUint(r, 8)
		return int64(n), err
	case TagFloat:
		n, err := readUint(r, 4)
		return math.Float32frombits(uint32(n)), err
	case TagDouble:
		n, err := readUint(r, 8)
		return math.Float64frombits(n), err
	case TagByteArray:
		n, err := readUint(r, 4)
		if err != nil {
			return nil, err
		}
		if n > maxString {
			return nil, ErrBadString
		}
		b := make([]byte, n)
		_, err = io.ReadFull(r, b)
		return b, err
	case TagString:
		return readString(r)
	case TagList:
		elem, err := readByte(r)
		if err != nil {
			return nil, err
		}
		if elem != TagEnd && !validTag(elem) {
			return nil, ErrUnknownTag
		}
		n, err := readUint(r, 4)
		if err != nil {
			return nil, err
		}
		if n > maxString {
			return nil, ErrBadString
		}
		items := make([]any, 0, n)
		for i := uint64(0); i < n; i++ {
			it, err := readPayload(r, elem)
			if err != nil {
				return nil, err
			}
			items = append(items, it)
		}
		return List{Elem: elem, Items: items}, nil
	case TagCompound:
		m := make(map[string]any)
		for {
			t, err := readByte(r)
			if err != nil {
				return nil, err
			}
			if t == TagEnd {
				return m, nil
			}
			if !validTag(t) {
				return nil, ErrUnknownTag
			}
			name, err := readString(r)
			if err != nil {
				return nil, err
			}
			v, err := readPayload(r, t)
			if err != nil {
				return nil, err
			}
			m[name] = v
		}
	case TagIntArray:
		n, err := readUint(r, 4)
		if err != nil {
			return nil, err
		}
		if n > maxString {
			return nil, ErrBadString
		}
		a := make([]int32, 0, n)
		for i := uint64(0); i < n; i++ {
			v, err := readUint(r, 4)
			if err != nil {
				return nil, err
			}
			a = append(a, int32(v))
		}
		return a, nil
	case TagLongArray:
		n, err := readUint(r, 4)
		if err != nil {
			return nil, err
		}
		if n > maxString {
			return nil, ErrBadString
		}
		a := make([]int64, 0, n)
		for i := uint64(0); i < n; i++ {
			v, err := readUint(r, 8)
			if err != nil {
				return nil, err
			}
			a = append(a, int64(v))
		}
		return a, nil
	}
	return nil, ErrUnknownTag
}

func validTag(t byte) bool { return t >= TagByte && t <= TagLongArray }

func writeByte(w io.Writer, b byte) error {
	_, err := w.Write([]byte{b})
	return err
}

func readByte(r io.Reader) (byte, error) {
	var b [1]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, errTrunc(err)
	}
	return b[0], nil
}

func errTrunc(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return ErrTruncated
	}
	return err
}

func writeUint(w io.Writer, size int, v uint64) error {
	var b [8]byte
	switch size {
	case 2:
		binary.BigEndian.PutUint16(b[:2], uint16(v))
		_, err := w.Write(b[:2])
		return err
	case 4:
		binary.BigEndian.PutUint32(b[:4], uint32(v))
		_, err := w.Write(b[:4])
		return err
	case 8:
		binary.BigEndian.PutUint64(b[:], v)
		_, err := w.Write(b[:])
		return err
	}
	return ErrUnknownTag
}

func readUint(r io.Reader, size int) (uint64, error) {
	var b [8]byte
	if _, err := io.ReadFull(r, b[:size]); err != nil {
		return 0, errTrunc(err)
	}
	switch size {
	case 2:
		return uint64(binary.BigEndian.Uint16(b[:2])), nil
	case 4:
		return uint64(binary.BigEndian.Uint32(b[:4])), nil
	case 8:
		return binary.BigEndian.Uint64(b[:]), nil
	}
	return 0, ErrUnknownTag
}

func writeString(w io.Writer, s string) error {
	encoded, err := encodeModifiedUTF8(s)
	if err != nil {
		return err
	}
	if err := writeUint(w, 2, uint64(len(encoded))); err != nil {
		return err
	}
	_, err = w.Write(encoded)
	return err
}

func readString(r io.Reader) (string, error) {
	n, err := readUint(r, 2)
	if err != nil {
		return "", err
	}
	if n > maxString {
		return "", ErrBadString
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return "", errTrunc(err)
	}
	return decodeModifiedUTF8(b)
}
