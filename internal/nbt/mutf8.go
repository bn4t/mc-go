package nbt

import "unicode/utf8"

// NBT strings use Java modified UTF-8 in files and on the wire. Measured
// with JDK 25 DataInputStream/DataOutputStream and a 26.2 anvil name:
// NUL uses two bytes; supplementary scalars use two encoded UTF-16 units.
// Unpaired surrogate units are retained as their three-byte encodings in
// Go strings, allowing Java strings without a Unicode scalar equivalent
// to survive decoding and re-encoding without replacement.
func encodeModifiedUTF8(s string) ([]byte, error) {
	b := make([]byte, 0, min(len(s), 65535))
	unit := func(v uint16) {
		switch {
		case v > 0 && v < 0x80:
			b = append(b, byte(v))
		case v < 0x800:
			b = append(b, 0xc0|byte(v>>6), 0x80|byte(v&63))
		default:
			b = append(b, 0xe0|byte(v>>12), 0x80|byte((v>>6)&63), 0x80|byte(v&63))
		}
	}
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && n == 1 {
			// This is the lossless representation returned by our decoder for
			// an unpaired Java UTF-16 surrogate, rather than arbitrary bad UTF-8.
			if i+2 >= len(s) || s[i] != 0xed || s[i+1] < 0xa0 || s[i+1] > 0xbf || s[i+2]&0xc0 != 0x80 {
				return nil, ErrBadString
			}
			b = append(b, s[i:i+3]...)
			i += 3
		} else {
			i += n
			if r > 0xffff {
				r -= 0x10000
				unit(uint16(0xd800 + (r >> 10)))
				unit(uint16(0xdc00 + (r & 1023)))
			} else {
				unit(uint16(r))
			}
		}
		if len(b) > 65535 {
			return nil, ErrBadString
		}
	}
	return b, nil
}

func decodeModifiedUTF8(b []byte) (string, error) {
	units := make([]uint16, 0, len(b))
	for i := 0; i < len(b); {
		first := b[i]
		var v uint16
		var n int
		switch {
		case first < 0x80:
			v = uint16(first)
			n = 1
		case first&0xe0 == 0xc0:
			v = uint16(first & 31)
			n = 2
		case first&0xf0 == 0xe0:
			v = uint16(first & 15)
			n = 3
		default:
			return "", ErrBadString
		}
		if i+n > len(b) {
			return "", ErrBadString
		}
		for j := 1; j < n; j++ {
			if b[i+j]&0xc0 != 0x80 {
				return "", ErrBadString
			}
			v = v<<6 | uint16(b[i+j]&63)
		}
		units = append(units, v)
		i += n
	}
	out := make([]byte, 0, len(b))
	for i := 0; i < len(units); i++ {
		v := units[i]
		if v >= 0xd800 && v <= 0xdbff && i+1 < len(units) && units[i+1] >= 0xdc00 && units[i+1] <= 0xdfff {
			r := 0x10000 + (rune(v)-0xd800)*1024 + rune(units[i+1]) - 0xdc00
			out = utf8.AppendRune(out, r)
			i++
		} else if v >= 0xd800 && v <= 0xdfff {
			out = append(out, 0xe0|byte(v>>12), 0x80|byte((v>>6)&63), 0x80|byte(v&63))
		} else {
			out = utf8.AppendRune(out, rune(v))
		}
	}
	return string(out), nil
}
