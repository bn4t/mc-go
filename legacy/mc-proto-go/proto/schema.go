package proto

// A compact schema model sufficient to represent prismarine "protocol.json".
// Generated code will construct these structs for a specific version (e.g., 1.21.8).

// Spec is a discriminated union of schema node types.
// Each node has a Tag identifying its kind, and fields specific to that kind.
// Tags: prim, buffer, restBuffer, uuid, container, array, option, mapper, switch,
//       bitfield, bitflags, ref, native, void

type Spec struct {
    Tag string // see list above

    // prim: Name in {varint,varlong,i8,i16,i32,i64,u8,u16,bool,f32,f64,string}
    Name string

    // buffer: CountType is primitive type name, typically "varint".
    CountType string

    // container: ordered field list
    Fields []Field

    // array: element spec
    Elem *Spec

    // option: inner spec
    Inner *Spec

    // mapper: underlying type and value mapping number->label (and reverse filled at runtime)
    Underlying *Spec
    Mappings   map[string]string // key: number as string (e.g., "0", "0x1"), value: label

    // switch: compare to previously-decoded field name; case label -> spec; default optional
    CompareTo string
    Cases     map[string]*Spec
    Default   *Spec

    // bitfield: MSB-first multi-field packing of a 64-bit signed value
    Bits []BitField

    // bitflags: underlying integer type + flag labels (LSB-first)
    FlagsType *Spec
    Flags     []string

    // ref: reference to a named type in Protocol.Types
    Ref string

    // native: name and parameter bag
    Native string
    Params map[string]any
}

type Field struct {
    Name string
    Type *Spec
}

type BitField struct {
    Name   string
    Size   int  // number of bits
    Signed bool // two's complement decode/encode
}

// Direction is either "toClient" or "toServer".
type Direction struct {
    // IDToName maps packet ID to packet name (e.g., "packet_keep_alive").
    IDToName map[int32]string
    // NameToID maps name back to ID.
    NameToID map[string]int32
    // Types holds container specs for each packet_*, plus shared type helpers under this state.
    Types map[string]*Spec
}

// State schema: "handshaking", "status", "login", "configuration", "play".
type State struct{
    ToClient Direction
    ToServer Direction
}

// Protocol is a compiled schema for a minecraft-data version (e.g., 1.21.8).
type Protocol struct {
    Version string
    // Global named types (from protocol.json .types)
    Types map[string]*Spec
    // Per-state schemas
    States map[string]State
}

