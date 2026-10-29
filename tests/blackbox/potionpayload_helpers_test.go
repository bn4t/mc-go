package blackbox

import (
	"fmt"
	"mc-go/internal/protocol"
)

func readPotionPayloadBB(r *protocol.PacketReader) (string, error) {
	out := ""
	present, e := r.Bool()
	if e != nil {
		return "", e
	}
	if present {
		v, e := r.Int()
		if e != nil {
			return "", e
		}
		out += fmt.Sprintf("color=%d;", v)
	}
	n, e := r.VarInt()
	if e != nil || n < 0 || n > 256 {
		return "", fmt.Errorf("potion effect count %d: %v", n, e)
	}
	for ; n > 0; n-- {
		id, e := r.VarInt()
		if e != nil {
			return "", e
		}
		d, e := readPotionDetailsBB(r, 0)
		if e != nil {
			return "", e
		}
		out += fmt.Sprintf("effect%d=%s;", id, d)
	}
	present, e = r.Bool()
	if e != nil {
		return "", e
	}
	if present {
		name, e := r.String()
		if e != nil {
			return "", e
		}
		out += "name=" + name + ";"
	}
	return out, nil
}
func readPotionDetailsBB(r *protocol.PacketReader, depth int) (string, error) {
	if depth > 16 {
		return "", fmt.Errorf("potion effect depth")
	}
	amp, e := r.VarInt()
	if e != nil {
		return "", e
	}
	duration, e := r.VarInt()
	if e != nil {
		return "", e
	}
	flags := ""
	for range 3 {
		v, e := r.Byte()
		if e != nil {
			return "", e
		}
		flags += fmt.Sprint(v)
	}
	hidden, e := r.Bool()
	if e != nil {
		return "", e
	}
	out := fmt.Sprintf("%d/%d/%s", amp, duration, flags)
	if hidden {
		s, e := readPotionDetailsBB(r, depth+1)
		if e != nil {
			return "", e
		}
		out += "(" + s + ")"
	}
	return out, nil
}
