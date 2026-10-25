package proto

import (
    "bytes"
    "errors"
    "fmt"
    "io"
    "math"
    "sort"
    "strings"
    "github.com/benj/mc-proto-go/types"
)

// Value is the decoded representation for a Spec.
// We keep it ergonomic for inspection:
// - primitives map to Go primitives (int32,int64,float32,float64,bool,string,[16]byte)
// - buffer/restBuffer -> []byte
// - container -> map[string]any
// - array -> []any
// - option -> nil or underlying value
// - mapper -> decoded to its mapped string label if mapping exists; otherwise raw numeric
// - switch -> value of the selected branch
// - bitfield -> map[string]int64
// - bitflags -> map[string]bool
// - native -> implementation-defined (we return []byte or structured maps where possible)

var (
    ErrUnknownPacket     = errors.New("unknown packet id/name for state/direction")
    ErrUnsupportedNative = errors.New("unsupported native type in schema")
    ErrInvalidSwitchKey  = errors.New("switch compare key not found in context")
)

// DecodePacket decodes a single packet payload given state+direction and packet id.
// Returns the packet name and the decoded value (container map).
func (p *Protocol) DecodePacket(state, dir string, id int32, data []byte) (string, map[string]any, error) {
    st, ok := p.States[state]
    if !ok { return "", nil, ErrUnknownPacket }
    var d Direction
    switch dir {
    case "toClient": d = st.ToClient
    case "toServer": d = st.ToServer
    default: return "", nil, ErrUnknownPacket
    }
    name, ok := d.IDToName[id]
    if !ok { return "", nil, ErrUnknownPacket }
    spec, ok := d.Types[name]
    if !ok { return "", nil, fmt.Errorf("packet spec missing: %s", name) }
    br := bytes.NewReader(data)
    v, err := p.decode(spec, newCtx(), br)
    if err != nil { return name, nil, err }
    m, _ := v.(map[string]any)
    return name, m, nil
}

// EncodePacket encodes a container map value according to the schema.
func (p *Protocol) EncodePacket(state, dir string, name string, value map[string]any) ([]byte, error) {
    st, ok := p.States[state]
    if !ok { return nil, ErrUnknownPacket }
    var d Direction
    switch dir {
    case "toClient": d = st.ToClient
    case "toServer": d = st.ToServer
    default: return nil, ErrUnknownPacket
    }
    spec, ok := d.Types[name]
    if !ok { return nil, fmt.Errorf("packet spec missing: %s", name) }
    var buf bytes.Buffer
    bw := byteWriter{&buf}
    if err := p.encode(spec, newCtxWithMap(value), bw, &buf, value); err != nil { return nil, err }
    return buf.Bytes(), nil
}

// --- internal ---

type ctx struct{
    // last container frame for resolving CompareTo keys
    stack []map[string]any
}

func newCtx() *ctx { return &ctx{stack: make([]map[string]any, 0, 8)} }
func newCtxWithMap(m map[string]any) *ctx { return &ctx{stack: []map[string]any{m}} }

func (c *ctx) push(m map[string]any) { c.stack = append(c.stack, m) }
func (c *ctx) pop() { c.stack = c.stack[:len(c.stack)-1] }
func (c *ctx) get(name string) (any, bool) {
    for i := len(c.stack)-1; i >= 0; i-- {
        if v, ok := c.stack[i][name]; ok { return v, true }
    }
    return nil, false
}

func (p *Protocol) decode(s *Spec, c *ctx, br *bytes.Reader) (any, error) {
    switch s.Tag {
    case "void":
        return nil, nil
    case "prim":
        switch s.Name {
        case "varint":  v, _, err := types.ReadVarInt(br); return v, err
        case "varlong": v, _, err := types.ReadVarLong(br); return v, err
        case "i8":      v, _, err := types.ReadI8(br); return v, err
        case "i16":     v, _, err := types.ReadI16(br); return v, err
        case "i32":     v, _, err := types.ReadI32(br); return v, err
        case "i64":     v, _, err := types.ReadI64(br); return v, err
        case "u8":      v, _, err := types.ReadU8(br); return v, err
        case "u16":     v, _, err := types.ReadU16(br); return v, err
        case "bool":    v, _, err := types.ReadBool(br); return v, err
        case "f32":     v, _, err := types.ReadF32(br); return v, err
        case "f64":     v, _, err := types.ReadF64(br); return v, err
        case "string":  v, _, err := types.ReadString(br, br, 1<<20); return v, err
        default:
            return nil, fmt.Errorf("unknown primitive %q", s.Name)
        }
    case "uuid":
        v, _, err := types.ReadUUID(br); return v, err
    case "buffer":
        // Only countType varint supported here (per minecraft-data usage).
        if s.CountType != "varint" { return nil, fmt.Errorf("buffer: unsupported countType %s", s.CountType) }
        v, _, err := types.ReadBytesVar(br, br, 1<<20)
        return v, err
    case "restBuffer":
        v, err := io.ReadAll(br); return v, err
    case "ref":
        // Resolve named type
        if t, ok := p.Types[s.Ref]; ok {
            return p.decode(t, c, br)
        }
        return nil, fmt.Errorf("unknown ref type %q", s.Ref)
    case "container":
        m := map[string]any{}
        c.push(m)
        for _, f := range s.Fields {
            v, err := p.decode(f.Type, c, br)
            if err != nil { c.pop(); return nil, fmt.Errorf("field %s: %w", f.Name, err) }
            m[f.Name] = v
        }
        c.pop()
        return m, nil
    case "array":
        // Currently only VarInt length arrays are emitted by minecraft-data here.
        ln, _, err := types.ReadVarInt(br)
        if err != nil { return nil, err }
        if ln < 0 { return nil, fmt.Errorf("negative array length") }
        out := make([]any, int(ln))
        for i := 0; i < int(ln); i++ {
            v, err := p.decode(s.Elem, c, br)
            if err != nil { return nil, err }
            out[i] = v
        }
        return out, nil
    case "option":
        present, _, err := types.ReadBool(br)
        if err != nil { return nil, err }
        if !present { return nil, nil }
        return p.decode(s.Inner, c, br)
    case "mapper":
        v, err := p.decode(s.Underlying, c, br)
        if err != nil { return nil, err }
        // Convert numeric to mapped label when possible.
        if len(s.Mappings) > 0 {
            key := fmt.Sprint(v)
            if lab, ok := s.Mappings[key]; ok { return lab, nil }
        }
        return v, nil
    case "switch":
        tagVal, ok := c.get(s.CompareTo)
        if !ok { return nil, ErrInvalidSwitchKey }
        key := fmt.Sprint(tagVal)
        // Some schemas expect lowercase mapping keys; normalize to exact first, then lower.
        if branch, ok := s.Cases[key]; ok {
            return p.decode(branch, c, br)
        }
        if branch, ok := s.Cases[strings.ToLower(key)]; ok {
            return p.decode(branch, c, br)
        }
        if s.Default != nil { return p.decode(s.Default, c, br) }
        return nil, fmt.Errorf("switch: no case for %q", key)
    case "bitfield":
        // Pack stored as i64 on the wire.
        val, _, err := types.ReadI64(br)
        if err != nil { return nil, err }
        u := uint64(val)
        // The schema defines fields MSB -> LSB in order.
        total := 0
        for _, b := range s.Bits { total += b.Size }
        if total > 64 { return nil, fmt.Errorf("bitfield size %d exceeds 64", total) }
        // start from MSB
        off := total
        out := make(map[string]any, len(s.Bits))
        for _, bf := range s.Bits {
            off -= bf.Size
            mask := uint64((1<<bf.Size) - 1)
            raw := int64((u >> off) & mask)
            if bf.Signed {
                // two's complement for bf.Size bits
                signBit := int64(1 << (bf.Size - 1))
                if raw&signBit != 0 { raw = raw - (1 << bf.Size) }
            }
            out[bf.Name] = raw
        }
        return out, nil
    case "bitflags":
        // Underlying numeric, then map bits -> labels
        v, err := p.decode(s.FlagsType, c, br)
        if err != nil { return nil, err }
        var bits uint64
        switch x := v.(type) {
        case int32: bits = uint64(uint32(x))
        case int64: bits = uint64(x)
        default: return nil, fmt.Errorf("bitflags expects integer, got %T", v)
        }
        m := map[string]bool{}
        for i, lab := range s.Flags {
            if lab == "" { continue }
            m[lab] = (bits & (1<<i)) != 0
        }
        return m, nil
    case "native":
        switch s.Native {
        case "optvarint":
            v, _, err := types.ReadVarInt(br)
            if err != nil { return nil, err }
            if v == -1 { return nil, nil }
            return v, nil
        case "anonymousNbt":
            // Read VarInt length + raw bytes
            b, _, err := types.ReadBytesVar(br, br, 1<<22)
            return b, err
        case "anonOptionalNbt":
            present, _, err := types.ReadBool(br)
            if err != nil { return nil, err }
            if !present { return nil, nil }
            b, _, err := types.ReadBytesVar(br, br, 1<<22)
            return b, err
        case "entityMetadataLoop":
            // Params: endVal (int), type (*Spec) provided by generator via Params["typeName"] resolved to ref
            endAny, ok := s.Params["endVal"]
            if !ok { return nil, fmt.Errorf("entityMetadataLoop missing endVal") }
            end := uint8(intFromAny(endAny))
            // The entry spec should be in Params["entry"] as *Spec (resolved by generator)
            entry, _ := s.Params["entry"].(*Spec)
            if entry == nil { return nil, fmt.Errorf("entityMetadataLoop missing entry spec") }
            var out []any
            for {
                // Peek next byte for key to detect endVal
                b, err := br.ReadByte()
                if err != nil { return nil, err }
                if b == end { break }
                // Put it back and decode full entry
                _ = br.UnreadByte()
                v, err := p.decode(entry, c, br)
                if err != nil { return nil, err }
                out = append(out, v)
            }
            return out, nil
        case "registryEntryHolder", "registryEntryHolderSet":
            // Heuristic minimal support: a boolean followed by either a string base or the provided otherwise spec.
            // Params may include: baseName (string), baseType (optional, default string), otherwise (Field{name,Type}).
            present, _, err := types.ReadBool(br)
            if err != nil { return nil, err }
            res := map[string]any{}
            if present {
                // Read base identifier (string by default)
                baseType := "string"
                if bt, ok := s.Params["baseType"].(string); ok && bt != "" { baseType = bt }
                v, err := p.decode(&Spec{Tag: "prim", Name: baseType}, c, br)
                if err != nil { return nil, err }
                name := "name"
                if bn, ok := s.Params["baseName"].(string); ok && bn != "" { name = bn }
                res[name] = v
            } else {
                // Otherwise branch
                if other, ok := s.Params["otherwise"].(*Spec); ok && other != nil {
                    v, err := p.decode(other, c, br)
                    if err != nil { return nil, err }
                    // Name optional
                    resName := "data"
                    if rn, ok := s.Params["otherwiseName"].(string); ok && rn != "" { resName = rn }
                    res[resName] = v
                }
            }
            return res, nil
        default:
            return nil, ErrUnsupportedNative
        }
    default:
        return nil, fmt.Errorf("unknown spec tag: %s", s.Tag)
    }
}

func (p *Protocol) encode(s *Spec, c *ctx, bw io.ByteWriter, buf *bytes.Buffer, v any) error {
    switch s.Tag {
    case "void":
        return nil
    case "prim":
        switch s.Name {
        case "varint":  _, err := types.WriteVarInt(bw, getI32(v)); return err
        case "varlong": _, err := types.WriteVarLong(bw, getI64(v)); return err
        case "i8":      _, err := types.WriteI8(buf, int8(getI64(v))); return err
        case "i16":     _, err := types.WriteI16(buf, int16(getI64(v))); return err
        case "i32":     _, err := types.WriteI32(buf, int32(getI64(v))); return err
        case "i64":     _, err := types.WriteI64(buf, getI64(v)); return err
        case "u8":      _, err := types.WriteU8(buf, uint8(getI64(v))); return err
        case "u16":     _, err := types.WriteU16(buf, uint16(getI64(v))); return err
        case "bool":    _, err := types.WriteBool(buf, getBool(v)); return err
        case "f32":     _, err := types.WriteF32(buf, float32(getF64(v))); return err
        case "f64":     _, err := types.WriteF64(buf, getF64(v)); return err
        case "string":  _, err := types.WriteString(bw, buf, getString(v), 1<<20); return err
        default: return fmt.Errorf("unknown primitive %q", s.Name)
        }
    case "uuid":
        arr := [16]byte{}
        switch x := v.(type) {
        case [16]byte: arr = x
        case []byte: if len(x) == 16 { copy(arr[:], x) } else { return fmt.Errorf("uuid: want 16 bytes") }
        case string:
            b, err := types.UUIDBytesFromString(x); if err != nil { return err }; arr = b
        default: return fmt.Errorf("uuid: unsupported value %T", v)
        }
        _, err := types.WriteUUID(buf, arr); return err
    case "buffer":
        b, _ := v.([]byte)
        _, err := types.WriteBytesVar(bw, buf, b, 1<<20)
        return err
    case "restBuffer":
        if b, ok := v.([]byte); ok { _, err := buf.Write(b); return err }
        return fmt.Errorf("restBuffer expects []byte")
    case "ref":
        t, ok := p.Types[s.Ref]
        if !ok { return fmt.Errorf("unknown ref type %q", s.Ref) }
        return p.encode(t, c, bw, buf, v)
    case "container":
        m, _ := v.(map[string]any)
        if m == nil { m = map[string]any{} }
        c.push(m)
        for _, f := range s.Fields {
            if err := p.encode(f.Type, c, bw, buf, m[f.Name]); err != nil { c.pop(); return fmt.Errorf("field %s: %w", f.Name, err) }
        }
        c.pop(); return nil
    case "array":
        arr, _ := v.([]any)
        // Accept slices of concrete types too by reflecting to []any
        if arr == nil {
            arr = toAnySlice(v)
        }
        _, err := types.WriteVarInt(bw, int32(len(arr)))
        if err != nil { return err }
        for _, it := range arr {
            if err := p.encode(s.Elem, c, bw, buf, it); err != nil { return err }
        }
        return nil
    case "option":
        if v == nil { _, err := types.WriteBool(buf, false); return err }
        if _, err := types.WriteBool(buf, true); err != nil { return err }
        return p.encode(s.Inner, c, bw, buf, v)
    case "mapper":
        // Accept either mapped label or underlying value
        if lab, ok := v.(string); ok && len(s.Mappings) > 0 {
            // inverse lookup by value
            var key string
            for k, val := range s.Mappings {
                if val == lab { key = k; break }
            }
            if key == "" { return fmt.Errorf("mapper: unknown label %q", lab) }
            // key may be hex or dec; write through underlying type encoder
            // prefer numeric parse
            if strings.HasPrefix(key, "0x") || strings.HasPrefix(key, "0X") {
                // hex -> int64
                var num int64
                fmt.Sscanf(key, "%x", &num)
                return p.encode(s.Underlying, c, bw, buf, num)
            }
            // dec
            var num int64
            fmt.Sscanf(key, "%d", &num)
            return p.encode(s.Underlying, c, bw, buf, num)
        }
        return p.encode(s.Underlying, c, bw, buf, v)
    case "switch":
        // Encoding requires the CompareTo already emitted by previous field(s) in same container.
        // We select branch by inspecting current top frame.
        top := map[string]any{}
        if len(c.stack) > 0 { top = c.stack[len(c.stack)-1] }
        key := fmt.Sprint(top[s.CompareTo])
        if branch, ok := s.Cases[key]; ok { return p.encode(branch, c, bw, buf, v) }
        if branch, ok := s.Cases[strings.ToLower(key)]; ok { return p.encode(branch, c, bw, buf, v) }
        if s.Default != nil { return p.encode(s.Default, c, bw, buf, v) }
        return fmt.Errorf("switch: no case for %q", key)
    case "bitfield":
        // Value is map[string]int64; pack MSB->LSB
        m, _ := v.(map[string]any)
        if m == nil { m = map[string]any{} }
        total := 0
        for _, b := range s.Bits { total += b.Size }
        if total > 64 { return fmt.Errorf("bitfield size %d exceeds 64", total) }
        var out uint64
        off := total
        for _, bf := range s.Bits {
            off -= bf.Size
            var val int64
            if vv, ok := m[bf.Name]; ok { val = int64(intFromAny(vv)) } else { val = 0 }
            if bf.Signed {
                // normalize signed into bf.Size bits two's complement
                max := int64(1<<(bf.Size-1))
                if val < -max || val >= max { return fmt.Errorf("bitfield %s out of range", bf.Name) }
                if val < 0 { val = (1<<bf.Size) + val }
            } else {
                max := int64(1<<bf.Size)
                if val < 0 || val >= max { return fmt.Errorf("bitfield %s out of range", bf.Name) }
            }
            out |= (uint64(val) & ((1<<bf.Size)-1)) << off
        }
        _, err := types.WriteI64(buf, int64(out))
        return err
    case "bitflags":
        // Accept map[string]bool and pack in LSB-first order
        m, _ := v.(map[string]bool)
        // fallback: map[string]any
        if m == nil {
            if ma, ok := v.(map[string]any); ok {
                m = map[string]bool{}
                for k, vv := range ma { m[k] = getBool(vv) }
            }
        }
        var bits uint64
        for i, lab := range s.Flags {
            if m != nil && m[lab] { bits |= 1<<i }
        }
        switch s.FlagsType.Name {
        case "u8":   _, err := types.WriteU8(buf, uint8(bits)); return err
        case "varint": _, err := types.WriteVarInt(bw, int32(bits)); return err
        default: return fmt.Errorf("bitflags unsupported base %s", s.FlagsType.Name)
        }
    case "native":
        switch s.Native {
        case "optvarint":
            if v == nil { _, err := types.WriteVarInt(bw, -1); return err }
            return p.encode(&Spec{Tag: "prim", Name: "varint"}, c, bw, buf, v)
        case "anonymousNbt":
            b, _ := v.([]byte); _, err := types.WriteBytesVar(bw, buf, b, 1<<22); return err
        case "anonOptionalNbt":
            if v == nil { _, err := types.WriteBool(buf, false); return err }
            if _, err := types.WriteBool(buf, true); err != nil { return err }
            b, _ := v.([]byte); _, err := types.WriteBytesVar(bw, buf, b, 1<<22); return err
        case "entityMetadataLoop":
            // Value must be []any entries followed by end marker.
            arr := toAnySlice(v)
            entry, _ := s.Params["entry"].(*Spec)
            if entry == nil { return fmt.Errorf("entityMetadataLoop missing entry spec") }
            for _, it := range arr {
                if err := p.encode(entry, c, bw, buf, it); err != nil { return err }
            }
            // write terminator key only (0xFF)
            _, err := types.WriteU8(buf, 0xFF)
            return err
        case "registryEntryHolder", "registryEntryHolderSet":
            // Follow decode heuristic: value is map with either baseName or otherwiseName
            m, _ := v.(map[string]any)
            if m == nil { m = map[string]any{} }
            // Determine which branch
            // prefer baseName then otherwiseName
            bn := "name"; if s.Params["baseName"] != nil { bn = s.Params["baseName"].(string) }
            on := "data"; if s.Params["otherwiseName"] != nil { on = s.Params["otherwiseName"].(string) }
            if base, ok := m[bn]; ok {
                if _, err := types.WriteBool(buf, true); err != nil { return err }
                baseType := "string"; if t, ok := s.Params["baseType"].(string); ok && t != "" { baseType = t }
                return p.encode(&Spec{Tag: "prim", Name: baseType}, c, bw, buf, base)
            }
            if _, err := types.WriteBool(buf, false); err != nil { return err }
            if other, ok := s.Params["otherwise"].(*Spec); ok && other != nil {
                return p.encode(other, c, bw, buf, m[on])
            }
            return nil
        default:
            return ErrUnsupportedNative
        }
    default:
        return fmt.Errorf("unknown spec tag: %s", s.Tag)
    }
}

// Utilities

type byteWriter struct{ io.ByteWriter }

func intFromAny(v any) int {
    switch x := v.(type) {
    case int: return x
    case int8: return int(x)
    case int16: return int(x)
    case int32: return int(x)
    case int64: return int(x)
    case uint: return int(x)
    case uint8: return int(x)
    case uint16: return int(x)
    case uint32: return int(x)
    case uint64: return int(x)
    case float32: return int(math.Round(float64(x)))
    case float64: return int(math.Round(x))
    case string:
        var n int
        fmt.Sscanf(x, "%d", &n)
        return n
    default: return 0
    }
}

func getI32(v any) int32 { return int32(intFromAny(v)) }
func getI64(v any) int64 { return int64(intFromAny(v)) }
func getF64(v any) float64 {
    switch x := v.(type) {
    case float32: return float64(x)
    case float64: return x
    default: return float64(intFromAny(v))
    }
}
func getBool(v any) bool {
    switch x := v.(type) {
    case bool: return x
    case int, int32, int64: return intFromAny(v) != 0
    default: return false
    }
}
func getString(v any) string {
    switch x := v.(type) {
    case string: return x
    default: return fmt.Sprint(v)
    }
}

func toAnySlice(v any) []any {
    switch x := v.(type) {
    case []any: return x
    case []string:
        out := make([]any, len(x)); for i := range x { out[i] = x[i] }; return out
    case []int:
        out := make([]any, len(x)); for i := range x { out[i] = x[i] }; return out
    case []int32:
        out := make([]any, len(x)); for i := range x { out[i] = x[i] }; return out
    case []int64:
        out := make([]any, len(x)); for i := range x { out[i] = x[i] }; return out
    case [][]byte:
        out := make([]any, len(x)); for i := range x { out[i] = x[i] }; return out
    default:
        return nil
    }
}

// SortedKeys returns sorted keys of a string->T map for stable iteration (debugging).
func SortedKeys[M ~map[string]V, V any](m M) []string {
    keys := make([]string, 0, len(m))
    for k := range m { keys = append(keys, k) }
    sort.Strings(keys)
    return keys
}
