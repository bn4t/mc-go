package main

import (
    "encoding/json"
    "flag"
    "fmt"
    "go/format"
    "os"
    "path/filepath"
    "sort"
    "strconv"
    "strings"
)

// Lightweight reader for minecraft-data protocol.json and emitter for proto.Protocol.

func readJSON(path string) (map[string]any, error) {
    b, err := os.ReadFile(path)
    if err != nil { return nil, err }
    var v map[string]any
    if err := json.Unmarshal(b, &v); err != nil { return nil, err }
    return v, nil
}

func isPrim(s string) bool {
    switch s {
    case "varint","varlong","i8","i16","i32","i64","u8","u16","bool","f32","f64","string": return true
    default: return false
    }
}

func parseHexOrDec(s string) (int32, bool) {
    s = strings.TrimSpace(s)
    if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
        n, err := strconv.ParseInt(s[2:], 16, 32)
        if err != nil { return 0, false }
        return int32(n), true
    }
    n, err := strconv.ParseInt(s, 10, 32)
    if err != nil { return 0, false }
    return int32(n), true
}

// idMap extracts packet name -> id mapping from protocol.json for a given state+direction.
func idMap(proto map[string]any, state, dir string) (map[string]int32, error) {
    sraw, ok := proto[state]
    if !ok { return map[string]int32{}, nil }
    s, _ := sraw.(map[string]any)
    draw, ok := s[dir]
    if !ok { return map[string]int32{}, nil }
    d, _ := draw.(map[string]any)
    traw, ok := d["types"]
    if !ok { return map[string]int32{}, nil }
    types, _ := traw.(map[string]any)
    praw, ok := types["packet"]
    if !ok { return map[string]int32{}, fmt.Errorf("missing packet types for %s.%s", state, dir) }
    parr, _ := praw.([]any)
    if len(parr) < 2 || fmt.Sprint(parr[0]) != "container" { return nil, fmt.Errorf("unexpected packet container for %s.%s", state, dir) }
    fields, _ := parr[1].([]any)
    for _, fres := range fields {
        f, _ := fres.(map[string]any)
        if f == nil { continue }
        if f["name"] == "name" {
            tarr, _ := f["type"].([]any)
            if len(tarr) != 2 || fmt.Sprint(tarr[0]) != "mapper" { continue }
            conf, _ := tarr[1].(map[string]any)
            mm, _ := conf["mappings"].(map[string]any)
            out := make(map[string]int32, len(mm))
            for k, v := range mm {
                name := fmt.Sprint(v)
                if id, ok := parseHexOrDec(k); ok { out[name] = id }
            }
            return out, nil
        }
    }
    return nil, fmt.Errorf("name mapper not found for %s.%s", state, dir)
}

// --- schema translation ---

// Emit Go code that constructs proto.Spec/Protocol structs.
type Emitter struct {
    sb strings.Builder
    andThen []string // post-declarations for references
    // cache of emitted inline specs to stable variables (avoid duplication)
    counter int
}

func (e *Emitter) newVar(prefix string) string {
    e.counter++
    return fmt.Sprintf("s_%s_%d", prefix, e.counter)
}

func (e *Emitter) emitSpecInline(v any) string {
    // Returns an expression string that constructs *proto.Spec for value v.
    switch x := v.(type) {
    case string:
        if x == "UUID" {
            return "&Spec{Tag:\"uuid\"}"
        }
        if x == "restBuffer" {
            return "&Spec{Tag:\"restBuffer\"}"
        }
        if x == "void" {
            return "&Spec{Tag:\"void\"}"
        }
        if x == "optvarint" {
            return "&Spec{Tag:\"native\", Native:\"optvarint\"}"
        }
        if isPrim(x) { return fmt.Sprintf("&Spec{Tag:\"prim\", Name:\"%s\"}", x) }
        // named reference
        return fmt.Sprintf("&Spec{Tag:\"ref\", Ref:\"%s\"}", x)
    case []any:
        if len(x) == 0 { return "&Spec{Tag:\"void\"}" }
        tag := fmt.Sprint(x[0])
        switch tag {
        case "container":
            flist, _ := x[1].([]any)
            varName := e.newVar("cont")
            e.sb.WriteString(fmt.Sprintf("%s := &Spec{Tag:\"container\"}\n", varName))
            e.sb.WriteString(fmt.Sprintf("%s.Fields = make([]Field, 0, %d)\n", varName, len(flist)))
            for _, it := range flist {
                fm, _ := it.(map[string]any)
                if fm == nil { continue }
                fname := fmt.Sprint(fm["name"])
                fsp := e.emitSpecInline(fm["type"]) // expression
                e.sb.WriteString(fmt.Sprintf("%s.Fields = append(%s.Fields, Field{Name:\"%s\", Type:%s})\n", varName, varName, escape(fname), fsp))
            }
            return varName
        case "array":
            opt, _ := x[1].(map[string]any)
            ct := fmt.Sprint(opt["countType"]) // expect string
            et := e.emitSpecInline(opt["type"]) 
            return fmt.Sprintf("&Spec{Tag:\"array\", CountType:\"%s\", Elem:%s}", ct, et)
        case "option":
            inner := e.emitSpecInline(x[1])
            return fmt.Sprintf("&Spec{Tag:\"option\", Inner:%s}", inner)
        case "mapper":
            conf, _ := x[1].(map[string]any)
            ut := e.emitSpecInline(conf["type"]) 
            mm, _ := conf["mappings"].(map[string]any)
            // stable deterministic literal: sort keys
            keys := make([]string, 0, len(mm))
            for k := range mm { keys = append(keys, k) }
            sort.Strings(keys)
            var b strings.Builder
            b.WriteString("map[string]string{")
            for _, k := range keys {
                b.WriteString(fmt.Sprintf("\"%s\":\"%s\",", escape(k), escape(fmt.Sprint(mm[k]))))
            }
            b.WriteString("}")
            return fmt.Sprintf("&Spec{Tag:\"mapper\", Underlying:%s, Mappings:%s}", ut, b.String())
        case "switch":
            conf, _ := x[1].(map[string]any)
            cmp := fmt.Sprint(conf["compareTo"])
            fields, _ := conf["fields"].(map[string]any)
            keys := make([]string, 0, len(fields))
            for k := range fields { keys = append(keys, k) }
            sort.Strings(keys)
            varName := e.newVar("sw")
            e.sb.WriteString(fmt.Sprintf("%s := &Spec{Tag:\"switch\", CompareTo:\"%s\", Cases: map[string]*Spec{}}\n", varName, escape(cmp)))
            for _, k := range keys {
                e.sb.WriteString(fmt.Sprintf("%s.Cases[\"%s\"] = %s\n", varName, escape(k), e.emitSpecInline(fields[k])))
            }
            if def, ok := conf["default"]; ok {
                e.sb.WriteString(fmt.Sprintf("%s.Default = %s\n", varName, e.emitSpecInline(def)))
            }
            return varName
        case "bitfield":
            arr, _ := x[1].([]any)
            varName := e.newVar("bf")
            e.sb.WriteString(fmt.Sprintf("%s := &Spec{Tag:\"bitfield\"}\n", varName))
            e.sb.WriteString(fmt.Sprintf("%s.Bits = make([]BitField, 0, %d)\n", varName, len(arr)))
            for _, it := range arr {
                fm, _ := it.(map[string]any)
                if fm == nil { continue }
                fname := fmt.Sprint(fm["name"]) 
                size := int(numFrom(fm["size"]))
                signed := boolFrom(fm["signed"]) 
                e.sb.WriteString(fmt.Sprintf("%s.Bits = append(%s.Bits, BitField{Name:\"%s\", Size:%d, Signed:%v})\n", varName, varName, escape(fname), size, signed))
            }
            return varName
        case "bitflags":
            conf, _ := x[1].(map[string]any)
            ft := e.emitSpecInline(conf["type"])
            flagsIfc, _ := conf["flags"]
            var flags []any
            switch t := flagsIfc.(type){
            case []any: flags = t
            default: flags = nil
            }
            var b strings.Builder
            b.WriteString("[]string{")
            for _, f := range flags { b.WriteString(fmt.Sprintf("\"%s\",", escape(fmt.Sprint(f)))) }
            b.WriteString("}")
            return fmt.Sprintf("&Spec{Tag:\"bitflags\", FlagsType:%s, Flags:%s}", ft, b.String())
        case "buffer":
            conf, _ := x[1].(map[string]any)
            ct := fmt.Sprint(conf["countType"]) 
            if ct == "" { ct = "varint" }
            return fmt.Sprintf("&Spec{Tag:\"buffer\", CountType:\"%s\"}", ct)
        case "registryEntryHolder", "registryEntryHolderSet":
            // Params: baseName (string) when provided; otherwise -> nested spec
            conf, _ := x[1].(map[string]any)
            baseName := fmt.Sprint(conf["baseName"]) // optional
            // otherwise may be an object {name,type}
            var otherExpr string
            if om, ok := conf["otherwise"].(map[string]any); ok {
                if om["name"] != nil && om["type"] != nil {
                    // We ignore the name but store for encode/decode heuristic
                    otherExpr = e.emitSpecInline(om["type"]) // expression for type
                }
            }
            vname := e.newVar("reh")
            e.sb.WriteString(fmt.Sprintf("%s := &Spec{Tag:\"native\", Native:\"%s\", Params: map[string]any{}}\n", vname, tag))
            if baseName != "" {
                e.sb.WriteString(fmt.Sprintf("%s.Params[\"baseName\"] = \"%s\"\n", vname, escape(baseName)))
            }
            if otherExpr != "" {
                e.sb.WriteString(fmt.Sprintf("%s.Params[\"otherwise\"] = %s\n", vname, otherExpr))
            }
            return vname
        case "entityMetadataLoop":
            conf, _ := x[1].(map[string]any)
            endVal := numFrom(conf["endVal"]) 
            entry := e.emitSpecInline(conf["type"]) 
            vname := e.newVar("eml")
            e.sb.WriteString(fmt.Sprintf("%s := &Spec{Tag:\"native\", Native:\"entityMetadataLoop\", Params: map[string]any{}}\n", vname))
            e.sb.WriteString(fmt.Sprintf("%s.Params[\"endVal\"] = %d\n", vname, int(endVal)))
            e.sb.WriteString(fmt.Sprintf("%s.Params[\"entry\"] = %s\n", vname, entry))
            return vname
        default:
            // Built-in natives mapped directly
            if tag == "anonymousNbt" || tag == "anonOptionalNbt" {
                return fmt.Sprintf("&Spec{Tag:\"native\", Native:\"%s\"}", tag)
            }
            // Fallback: assume named reference
            return fmt.Sprintf("&Spec{Tag:\"ref\", Ref:\"%s\"}", tag)
        }
    default:
        return "&Spec{Tag:\"void\"}"
    }
}

func escape(s string) string {
    s = strings.ReplaceAll(s, "\\", "\\\\")
    s = strings.ReplaceAll(s, "\"", "\\\"")
    return s
}

func numFrom(v any) float64 {
    switch x := v.(type) {
    case float64: return x
    case float32: return float64(x)
    case int: return float64(x)
    case int64: return float64(x)
    case json.Number:
        f, _ := x.Float64(); return f
    default: return 0
    }
}
func boolFrom(v any) bool { b, _ := v.(bool); return b }

func main() {
    dataDir := flag.String("data", "tools/.cache/minecraft-data", "path to minecraft-data repo root")
    version := flag.String("version", "1.21.8", "protocol version folder (e.g., 1.21.8)")
    outDir := flag.String("out", "proto", "output package directory for compiled schema")
    packetsOut := flag.String("packets-out", "packets", "output package directory for typed packet structs")
    entitiesOut := flag.String("entities-out", "entities", "output package directory for entities enums/data")
    flag.Parse()

    protoPath := filepath.Join(*dataDir, "data", "pc", *version, "protocol.json")
    m, err := readJSON(protoPath)
    if err != nil { fmt.Fprintf(os.Stderr, "read %s: %v\n", protoPath, err); os.Exit(1) }

    var e Emitter
    // header
    e.sb.WriteString("package proto\n\n")
    e.sb.WriteString("// Code generated by tools/protogen; DO NOT EDIT.\n")
    e.sb.WriteString("// Source: minecraft-data "+*version+"\n\n")
    e.sb.WriteString("// nolint: revive,stylecheck\n")
    e.sb.WriteString("// NOTE: This file constructs the full Protocol schema for version "+*version+".\n\n")

    // Emit global named types (declare now, populate in init())
    e.sb.WriteString("// --- Global types (protocol.json .types) ---\n")
    e.sb.WriteString("var types_"+strings.ReplaceAll(*version, ".", "_")+" = map[string]*Spec{}\n")

    // For each state+dir: packet ids and types
    states := []string{"handshaking","status","login","configuration","play"}
    for _, st := range states {
        for _, dir := range []string{"toClient","toServer"} {
            ids, _ := idMap(m, st, dir)
            // Maps
            e.sb.WriteString(fmt.Sprintf("var ids_%s_%s_%s = map[int32]string{\n", strings.ReplaceAll(*version, ".", "_"), st, dir))
            // stable order by id
            sortids := make([]int, 0, len(ids))
            for _, id := range ids { sortids = append(sortids, int(id)) }
            sort.Ints(sortids)
            for _, id := range sortids {
                // find name by id
                var name string
                for n, v := range ids { if int(v) == id { name = n; break } }
                e.sb.WriteString(fmt.Sprintf("\t%d: \"%s\",\n", id, escape("packet_"+name)))
            }
            e.sb.WriteString("}\n")
            e.sb.WriteString(fmt.Sprintf("var name2id_%s_%s_%s = map[string]int32{\n", strings.ReplaceAll(*version, ".", "_"), st, dir))
            names := make([]string, 0, len(ids))
            for n := range ids { names = append(names, n) }
            sort.Strings(names)
            for _, n := range names {
                e.sb.WriteString(fmt.Sprintf("\t\"%s\": %d,\n", escape("packet_"+n), ids[n]))
            }
            e.sb.WriteString("}\n")

            // types for this state+dir (declare now, populate in init())
            e.sb.WriteString(fmt.Sprintf("var types_%s_%s_%s = map[string]*Spec{}\n", strings.ReplaceAll(*version, ".", "_"), st, dir))
        }
    }

    // init() populates the maps declared above
    e.sb.WriteString("\nfunc init() {\n")
    // Fill global types
    if typesRaw, ok := m["types"].(map[string]any); ok {
        keys := make([]string, 0, len(typesRaw))
        for k := range typesRaw { keys = append(keys, k) }
        sort.Strings(keys)
        for _, k := range keys {
            v := typesRaw[k]
            if s, ok := v.(string); ok && s == "native" { continue }
            expr := e.emitSpecInline(v)
            e.sb.WriteString(fmt.Sprintf("types_%s[\"%s\"] = %s\n", strings.ReplaceAll(*version, ".", "_"), escape(k), expr))
        }
    }
    // Fill per-state types
    for _, st := range states {
        for _, dir := range []string{"toClient","toServer"} {
            sraw, _ := m[st].(map[string]any)
            draw, _ := sraw[dir].(map[string]any)
            traw, _ := draw["types"].(map[string]any)
            tkeys := make([]string, 0, len(traw))
            for k := range traw { tkeys = append(tkeys, k) }
            sort.Strings(tkeys)
            for _, k := range tkeys {
                v := traw[k]
                if s, ok := v.(string); ok && s == "native" { continue }
                expr := e.emitSpecInline(v)
                e.sb.WriteString(fmt.Sprintf("types_%s_%s_%s[\"%s\"] = %s\n", strings.ReplaceAll(*version, ".", "_"), st, dir, escape(k), expr))
            }
        }
    }

    // Build Protocol instance variable in init assignment
    e.sb.WriteString("\n")
    vname := strings.ReplaceAll(*version, ".", "_")
    e.sb.WriteString("PC_"+vname+" = Protocol{\n")
    e.sb.WriteString("\tVersion: \""+escape(*version)+"\",\n")
    e.sb.WriteString("\tTypes: types_"+vname+",\n")
    e.sb.WriteString("\tStates: map[string]State{\n")
    for _, st := range states {
        e.sb.WriteString(fmt.Sprintf("\t\t\"%s\": {\n", st))
        for _, dir := range []string{"toClient","toServer"} {
            e.sb.WriteString(fmt.Sprintf("\t\t\t%s: Direction{\n", dirToField(dir)))
            e.sb.WriteString(fmt.Sprintf("\t\t\t\tIDToName: ids_%s_%s_%s,\n", vname, st, dir))
            e.sb.WriteString(fmt.Sprintf("\t\t\t\tNameToID: name2id_%s_%s_%s,\n", vname, st, dir))
            e.sb.WriteString(fmt.Sprintf("\t\t\t\tTypes: types_%s_%s_%s,\n", vname, st, dir))
            e.sb.WriteString("\t\t\t},\n")
        }
        e.sb.WriteString("\t\t},\n")
    }
    e.sb.WriteString("\t},\n}\n")
    e.sb.WriteString("}\n")

    // Top-level var declaration for the compiled schema
    e.sb.WriteString("\n// PC_"+vname+" is the full compiled schema for PC "+*version+".\n")
    e.sb.WriteString("var PC_"+vname+" Protocol\n")

    // Format and write file
    code := e.sb.String()
    formatted, err := format.Source([]byte(code))
    if err != nil {
        // write unformatted for debugging
        _ = os.MkdirAll(*outDir, 0o755)
        _ = os.WriteFile(filepath.Join(*outDir, "pc_"+vname+"_schema_unformatted.go"), []byte(code), 0o644)
        fmt.Fprintf(os.Stderr, "format: %v\n", err)
        os.Exit(1)
    }
    if err := os.MkdirAll(*outDir, 0o755); err != nil { fmt.Fprintf(os.Stderr, "mkdir: %v\n", err); os.Exit(1) }
    outFile := filepath.Join(*outDir, "pc_"+vname+"_schema.go")
    if err := os.WriteFile(outFile, formatted, 0o644); err != nil { fmt.Fprintf(os.Stderr, "write: %v\n", err); os.Exit(1) }

    // Also emit typed packet structs and entities
    if err := emitTypedPackets(*packetsOut, *version, m); err != nil {
        fmt.Fprintf(os.Stderr, "emit packets: %v\n", err)
        os.Exit(1)
    }
    if err := emitEntities(*entitiesOut, *version, *dataDir); err != nil {
        fmt.Fprintf(os.Stderr, "emit entities: %v\n", err)
        os.Exit(1)
    }
}

func dirToField(dir string) string {
    if dir == "toClient" { return "ToClient" }
    return "ToServer"
}

// -------------------- Typed packet structs emission --------------------

func exportIdent(s string) string {
    s = strings.TrimSpace(s)
    if s == "" { return "X" }
    // separators
    repl := strings.NewReplacer("-", "_", ".", "_", " ", "_", "/", "_")
    s = repl.Replace(s)
    parts := strings.Split(s, "_")
    out := ""
    for _, p := range parts {
        if p == "" { continue }
        out += strings.ToUpper(p[:1]) + strings.ToLower(p[1:])
    }
    // reserved words minimal handling
    if out == "Type" { out = "Type_" }
    if out == "Default" { out = "Default_" }
    if out == "Data" { out = "Data_" }
    return out
}

func typedPacketTypeName(ver, state, dir, pkt string) string {
    name := strings.ReplaceAll(ver, ".", "_")
    d := map[string]string{"toClient":"CB","toServer":"SB"}[dir]
    return "PC"+name+"_"+exportIdent(state)+"_"+d+"_"+exportIdent(strings.TrimPrefix(pkt, "packet_"))
}

// supported primitives
func primGoType(x string) (string, bool) {
    switch x {
    case "varint", "i32": return "int32", true
    case "varlong", "i64": return "int64", true
    case "i8": return "int8", true
    case "i16": return "int16", true
    case "u8": return "uint8", true
    case "u16": return "uint16", true
    case "bool": return "bool", true
    case "f32": return "float32", true
    case "f64": return "float64", true
    case "string": return "string", true
    }
    return "", false
}

type tdecl struct{ name, code string }

// goType returns a Go type string and any nested type declarations needed.
var genTypes map[string]any

func goType(spec any, parent, field string) (string, []tdecl, error) {
    switch x := spec.(type) {
    case string:
        if x == "UUID" { return "[16]byte", nil, nil }
        if x == "restBuffer" { return "[]byte", nil, nil }
        if x == "void" { return "struct{}", nil, nil }
        if x == "optvarint" { return "*int32", nil, nil }
        if t, ok := primGoType(x); ok { return t, nil, nil }
        // named reference becomes its own generated type
        return exportIdent(x), nil, nil
    case []any:
        if len(x) == 0 { return "struct{}", nil, nil }
        tag := fmt.Sprint(x[0])
        switch tag {
        case "buffer":
            return "[]byte", nil, nil
        case "array":
            opt, _ := x[1].(map[string]any)
            et, decls, err := goType(opt["type"], parent+"_"+exportIdent(field), field+"Item")
            if err != nil { return "", nil, err }
            return "[]"+et, decls, nil
        case "option":
            inner := x[1]
            et, decls, err := goType(inner, parent+"_"+exportIdent(field), field+"Opt")
            if err != nil { return "", nil, err }
            return "*"+et, decls, nil
        case "container":
            // define nested struct type
            typeName := parent+"_"+exportIdent(field)
            fl, _ := x[1].([]any)
            var b strings.Builder
            b.WriteString("type "+typeName+" struct{\n")
            var decls []tdecl
            for _, it := range fl {
                fm, _ := it.(map[string]any)
                if fm == nil { continue }
                fName := exportIdent(fmt.Sprint(fm["name"]))
                gt, more, err := goType(fm["type"], typeName, fName)
                if err != nil { return "", nil, err }
                decls = append(decls, more...)
                b.WriteString("\t"+fName+" "+gt+"\n")
            }
            b.WriteString("}\n\n")
            decls = append(decls, tdecl{typeName, b.String()})
            return typeName, decls, nil
        case "mapper":
            // expose as string (label)
            return "string", nil, nil
        case "switch":
            // union wrapper with pointer fields per case
            conf, _ := x[1].(map[string]any)
            fields, _ := conf["fields"].(map[string]any)
            keys := make([]string, 0, len(fields))
            for k := range fields { keys = append(keys, k) }
            sort.Strings(keys)
            typeName := parent+"_"+exportIdent(field)
            var b strings.Builder
            b.WriteString("type "+typeName+" struct{\n")
            var decls []tdecl
            for _, k := range keys {
                fname := exportIdent(k)
                gt, more, err := goType(fields[k], typeName, fname)
                if err != nil { return "", nil, err }
                decls = append(decls, more...)
                b.WriteString("\t"+fname+" *"+gt+"\n")
            }
            if def, ok := conf["default"]; ok {
                gt, more, err := goType(def, typeName, "Default")
                if err != nil { return "", nil, err }
                decls = append(decls, more...)
                b.WriteString("\tDefault *"+gt+"\n")
            }
            b.WriteString("}\n\n")
            decls = append(decls, tdecl{typeName, b.String()})
            return typeName, decls, nil
        case "bitfield":
            arr, _ := x[1].([]any)
            typeName := parent+"_"+exportIdent(field)
            var b strings.Builder
            b.WriteString("type "+typeName+" struct{\n")
            for _, it := range arr {
                fm, _ := it.(map[string]any)
                if fm == nil { continue }
                b.WriteString("\t"+exportIdent(fmt.Sprint(fm["name"]))+" int64\n")
            }
            b.WriteString("}\n\n")
            return typeName, []tdecl{{typeName, b.String()}}, nil
        case "bitflags":
            conf, _ := x[1].(map[string]any)
            flags, _ := conf["flags"].([]any)
            typeName := parent+"_"+exportIdent(field)
            var b strings.Builder
            b.WriteString("type "+typeName+" struct{\n")
            for _, it := range flags { b.WriteString("\t"+exportIdent(fmt.Sprint(it))+" bool\n") }
            b.WriteString("}\n\n")
            return typeName, []tdecl{{typeName, b.String()}}, nil
        case "registryEntryHolder", "registryEntryHolderSet":
            typeName := parent+"_"+exportIdent(field)
            conf, _ := x[1].(map[string]any)
            var decls []tdecl
            var other string
            if om, ok := conf["otherwise"].(map[string]any); ok {
                gt, more, err := goType(om["type"], typeName, "Data")
                if err != nil { return "", nil, err }
                decls = append(decls, more...)
                other = gt
            } else {
                other = "struct{}"
            }
            var b strings.Builder
            b.WriteString("type "+typeName+" struct{ Name *string; Data *"+other+" }\n\n")
            decls = append(decls, tdecl{typeName, b.String()})
            return typeName, decls, nil
        case "entityMetadataLoop":
            conf, _ := x[1].(map[string]any)
            gt, decls, err := goType(conf["type"], parent+"_"+exportIdent(field), "Entry")
            if err != nil { return "", nil, err }
            return "[]"+gt, decls, nil
        default:
            // treat as reference
            return exportIdent(tag), nil, nil
        }
    }
    return "", nil, fmt.Errorf("unsupported spec kind")
}

// emitRead and emitWrite generate (un)marshal snippets. They assume in-scope:
//  - br: *bytes.Reader for reading; buf bytes.Buffer and bw byteWriter for writing
func emitRead(expr string, spec any, br string, indent string, parentTypeName, fieldName string, cvar string) (string, error) {
    pad := func(s string) string { return indent + s + "\n" }
    switch x := spec.(type) {
    case string:
        switch x {
        case "varint": return pad("if tv, _, err := types.ReadVarInt("+br+"); err != nil { return err } else { "+expr+" = tv }"), nil
        case "varlong": return pad("if tv, _, err := types.ReadVarLong("+br+"); err != nil { return err } else { "+expr+" = tv }"), nil
        case "i8": return pad("if tv, _, err := types.ReadI8("+br+"); err != nil { return err } else { "+expr+" = tv }"), nil
        case "i16": return pad("if tv, _, err := types.ReadI16("+br+"); err != nil { return err } else { "+expr+" = tv }"), nil
        case "i32": return pad("if tv, _, err := types.ReadI32("+br+"); err != nil { return err } else { "+expr+" = tv }"), nil
        case "i64": return pad("if tv, _, err := types.ReadI64("+br+"); err != nil { return err } else { "+expr+" = tv }"), nil
        case "u8": return pad("if tv, _, err := types.ReadU8("+br+"); err != nil { return err } else { "+expr+" = tv }"), nil
        case "u16": return pad("if tv, _, err := types.ReadU16("+br+"); err != nil { return err } else { "+expr+" = tv }"), nil
        case "bool": return pad("if tv, _, err := types.ReadBool("+br+"); err != nil { return err } else { "+expr+" = tv }"), nil
        case "f32": return pad("if tv, _, err := types.ReadF32("+br+"); err != nil { return err } else { "+expr+" = tv }"), nil
        case "f64": return pad("if tv, _, err := types.ReadF64("+br+"); err != nil { return err } else { "+expr+" = tv }"), nil
        case "string": return pad("if tv, _, err := types.ReadString("+br+", "+br+", 1<<20); err != nil { return err } else { "+expr+" = tv }"), nil
        case "UUID": return pad("if tv, _, err := types.ReadUUID("+br+"); err != nil { return err } else { "+expr+" = tv }"), nil
        case "restBuffer": return pad("if rem, err := io.ReadAll("+br+"); err != nil { return err } else { "+expr+" = rem }"), nil
        case "optvarint":
            return pad("{ v, _, err := types.ReadVarInt("+br+"); if err != nil { return err }; if v != -1 { tmp := v; "+expr+" = &tmp } }"), nil
        default:
            // named ref: call its method
            tn := exportIdent(x)
            return pad("{ var tv "+tn+"; if err := (&tv).UnmarshalFrom("+br+"); err != nil { return err }; "+expr+" = tv }"), nil
        }
    case []any:
        tag := fmt.Sprint(x[0])
        switch tag {
        case "buffer":
            return pad("if v, _, err := types.ReadBytesVar("+br+", "+br+", 1<<20); err != nil { return err } else { "+expr+" = v }"), nil
        case "array":
            opt, _ := x[1].(map[string]any)
            et := opt["type"]
            gt, _, err := goType(et, parentTypeName+"_"+exportIdent(fieldName), fieldName+"Item")
            if err != nil { return "", err }
            var sb strings.Builder
            sb.WriteString(pad("{ ln, _, err := types.ReadVarInt("+br+"); if err != nil { return err }; if ln < 0 { return fmt.Errorf(\"negative array length\") }; "+expr+" = make([]"+gt+", int(ln)); for i:=0;i<int(ln); i++ {"))
            inner, err := emitRead(expr+"[i]", et, br, indent+"\t", parentTypeName+"_"+exportIdent(fieldName), fieldName+"Item", expr+"[i]")
            if err != nil { return "", err }
            sb.WriteString(inner)
            sb.WriteString(pad("}"))
            sb.WriteString(pad("}"))
            return sb.String(), nil
        case "option":
            inner := x[1]
            var sb strings.Builder
            sb.WriteString(pad("{ present, _, err := types.ReadBool("+br+"); if err != nil { return err }; if present {"))
            gt, _, err := goType(inner, parentTypeName+"_"+exportIdent(fieldName), fieldName+"Opt")
            if err != nil { return "", err }
            sb.WriteString(pad("var v "+gt))
            innerRead, err := emitRead("v", inner, br, indent+"\t", parentTypeName+"_"+exportIdent(fieldName), fieldName+"Opt", "v")
            if err != nil { return "", err }
            sb.WriteString(innerRead)
            sb.WriteString(pad(expr+" = &v"))
            sb.WriteString(pad("} }"))
            return sb.String(), nil
        case "container":
            fl, _ := x[1].([]any)
            var sb strings.Builder
            for _, it := range fl {
                fm, _ := it.(map[string]any)
                if fm == nil { continue }
                r, err := emitRead(expr+"."+exportIdent(fmt.Sprint(fm["name"])), fm["type"], br, indent, parentTypeName+"_"+exportIdent(fieldName), fmt.Sprint(fm["name"]), expr)
                if err != nil { return "", err }
                sb.WriteString(r)
            }
            return sb.String(), nil
        case "mapper":
            conf, _ := x[1].(map[string]any)
            ut := fmt.Sprint(conf["type"]) // assume varint
            mm, _ := conf["mappings"].(map[string]any)
            var sb strings.Builder
            // read underlying int then map to string
            if ut == "varint" {
                sb.WriteString(pad("{ mv, _, err := types.ReadVarInt("+br+"); if err != nil { return err }; switch mv {"))
                // stable order by numeric key
                keys := make([]string, 0, len(mm))
                for k := range mm { keys = append(keys, k) }
                sort.Slice(keys, func(i,j int) bool { a,_:=parseHexOrDec(keys[i]); b,_:=parseHexOrDec(keys[j]); return a<b })
                for _, k := range keys {
                    id, _ := parseHexOrDec(k)
                    sb.WriteString(pad(fmt.Sprintf("case %d: %s = \"%s\"", id, expr, escape(fmt.Sprint(mm[k])))))
                }
                sb.WriteString(pad(fmt.Sprintf("default: %s = fmt.Sprint(mv) } }", expr)))
            } else {
                // fallback: just decode underlying
                inner, err := emitRead(expr, ut, br, indent, parentTypeName, fieldName, cvar)
                if err != nil { return "", err }
                sb.WriteString(inner)
            }
            return sb.String(), nil
        case "switch":
            conf, _ := x[1].(map[string]any)
            cmp := exportIdent(fmt.Sprint(conf["compareTo"]))
            fields, _ := conf["fields"].(map[string]any)
            keys := make([]string, 0, len(fields))
            for k := range fields { keys = append(keys, k) }
            sort.Strings(keys)
            var sb strings.Builder
            for i, k := range keys {
                cond := "if"
                if i > 0 { cond = "} else if" }
                sb.WriteString(pad(fmt.Sprintf("%s %s.%s == \"%s\" {", cond, cvar, cmp, escape(k))))
                inner, err := emitRead(expr+"."+exportIdent(k), fields[k], br, indent+"\t", parentTypeName+"_"+exportIdent(fieldName), k, expr+"."+exportIdent(k))
                if err != nil { return "", err }
                sb.WriteString(inner)
            }
            if def, ok := conf["default"]; ok {
                sb.WriteString(pad("} else {"))
                inner, err := emitRead(expr+".Default", def, br, indent+"\t", parentTypeName+"_"+exportIdent(fieldName), "Default", expr+".Default")
                if err != nil { return "", err }
                sb.WriteString(inner)
            }
            sb.WriteString(pad("}"))
            return sb.String(), nil
        case "bitfield":
            arr, _ := x[1].([]any)
            var sb strings.Builder
            sb.WriteString(pad("{ v, _, err := types.ReadI64("+br+"); if err != nil { return err }; u := uint64(v)"))
            // compute total bits and offsets
            total := 0
            for _, it := range arr { fm,_ := it.(map[string]any); total += int(numFrom(fm["size"])) }
            off := total
            for _, it := range arr {
                fm,_ := it.(map[string]any)
                name := exportIdent(fmt.Sprint(fm["name"]))
                size := int(numFrom(fm["size"]))
                signed := boolFrom(fm["signed"]) 
                off -= size
                sb.WriteString(pad(fmt.Sprintf("{ raw := int64((u >> %d) & ((1<<%d)-1));", off, size)))
                if signed {
                    sb.WriteString(pad(fmt.Sprintf("sign := int64(1 << (%d-1)); if raw & sign != 0 { raw = raw - (1<<%d) }", size, size)))
                }
                sb.WriteString(pad(fmt.Sprintf("%s.%s = raw }", expr, name)))
            }
            sb.WriteString(pad("}"))
            return sb.String(), nil
        case "bitflags":
            conf, _ := x[1].(map[string]any)
            ft := fmt.Sprint(conf["type"]) // u8 or varint
            flags, _ := conf["flags"].([]any)
            var sb strings.Builder
            // read base value
            if ft == "u8" {
                sb.WriteString(pad("{ v, _, err := types.ReadU8("+br+"); if err != nil { return err }; tmp := uint64(v)"))
            } else {
                sb.WriteString(pad("{ v, _, err := types.ReadVarInt("+br+"); if err != nil { return err }; tmp := uint64(uint32(v))"))
            }
            for i, it := range flags {
                sb.WriteString(pad(fmt.Sprintf("%s.%s = (tmp & (1<<%d)) != 0", expr, exportIdent(fmt.Sprint(it)), i)))
            }
            sb.WriteString(pad("}"))
            return sb.String(), nil
        case "registryEntryHolder", "registryEntryHolderSet":
            // bool then either base string or otherwise
            conf, _ := x[1].(map[string]any)
            var sb strings.Builder
            sb.WriteString(pad("{ present, _, err := types.ReadBool("+br+"); if err != nil { return err }; if present {"))
            // base type is string by default
            sb.WriteString(pad("v, _, err := types.ReadString("+br+", "+br+", 1<<20); if err != nil { return err }; "+expr+".Name = &v"))
            sb.WriteString(pad("} else {"))
            if om, ok := conf["otherwise"].(map[string]any); ok {
                gt, _, _ := goType(om["type"], parentTypeName+"_"+exportIdent(fieldName), "Data")
                sb.WriteString(pad("var dv "+gt))
                inner, err := emitRead("dv", om["type"], br, indent+"\t", parentTypeName+"_"+exportIdent(fieldName), "Data", "dv")
                if err != nil { return "", err }
                sb.WriteString(inner)
                sb.WriteString(pad(expr+".Data = &dv"))
            }
            sb.WriteString(pad("} }"))
            return sb.String(), nil
        case "entityMetadataLoop":
            conf, _ := x[1].(map[string]any)
            entry := conf["type"]
            gt, _, _ := goType(entry, parentTypeName+"_"+exportIdent(fieldName), "Entry")
            var sb strings.Builder
            sb.WriteString(pad("{ for { b, err := "+br+".ReadByte(); if err != nil { return err }; if b == 0xff { break }; if err := "+br+".UnreadByte(); err != nil { return err }"))
            sb.WriteString(pad("var v "+gt))
            inner, err := emitRead("v", entry, br, indent+"\t", parentTypeName+"_"+exportIdent(fieldName), "Entry", "v")
            if err != nil { return "", err }
            sb.WriteString(inner)
            sb.WriteString(pad(expr+" = append("+expr+", v)"))
            sb.WriteString(pad("} }"))
            return sb.String(), nil
        default:
            return pad("// unsupported tag: "+tag), nil
        }
    }
    return "", fmt.Errorf("unsupported spec kind")
}

func emitWrite(expr string, spec any, bw string, bufVar string, indent string) (string, error) {
    pad := func(s string) string { return indent + s + "\n" }
    switch x := spec.(type) {
    case string:
        switch x {
        case "varint": return pad("if _, err := types.WriteVarInt("+bw+", "+expr+"); err != nil { return nil, err }"), nil
        case "varlong": return pad("if _, err := types.WriteVarLong("+bw+", "+expr+"); err != nil { return nil, err }"), nil
        case "i8": return pad("if _, err := types.WriteI8(&"+bufVar+", "+expr+"); err != nil { return nil, err }"), nil
        case "i16": return pad("if _, err := types.WriteI16(&"+bufVar+", "+expr+"); err != nil { return nil, err }"), nil
        case "i32": return pad("if _, err := types.WriteI32(&"+bufVar+", "+expr+"); err != nil { return nil, err }"), nil
        case "i64": return pad("if _, err := types.WriteI64(&"+bufVar+", "+expr+"); err != nil { return nil, err }"), nil
        case "u8": return pad("if _, err := types.WriteU8(&"+bufVar+", "+expr+"); err != nil { return nil, err }"), nil
        case "u16": return pad("if _, err := types.WriteU16(&"+bufVar+", "+expr+"); err != nil { return nil, err }"), nil
        case "bool": return pad("if _, err := types.WriteBool(&"+bufVar+", "+expr+"); err != nil { return nil, err }"), nil
        case "f32": return pad("if _, err := types.WriteF32(&"+bufVar+", "+expr+"); err != nil { return nil, err }"), nil
        case "f64": return pad("if _, err := types.WriteF64(&"+bufVar+", "+expr+"); err != nil { return nil, err }"), nil
        case "string": return pad("if _, err := types.WriteString("+bw+", &"+bufVar+", "+expr+", 1<<20); err != nil { return nil, err }"), nil
        case "UUID": return pad("if _, err := types.WriteUUID(&"+bufVar+", "+expr+"); err != nil { return nil, err }"), nil
        case "restBuffer": return pad("if _, err := "+bufVar+".Write("+expr+"); err != nil { return nil, err }"), nil
        case "optvarint":
            return pad("if "+expr+" == nil { if _, err := types.WriteVarInt("+bw+", -1); err != nil { return nil, err } } else { if _, err := types.WriteVarInt("+bw+", *"+expr+"); err != nil { return nil, err } }"), nil
        default:
            // named ref: call its method
            return pad("{ v := "+expr+"; if err := v.MarshalTo("+bw+", &"+bufVar+"); err != nil { return nil, err } }"), nil
        }
    case []any:
        tag := fmt.Sprint(x[0])
        switch tag {
        case "buffer":
            return pad("if _, err := types.WriteBytesVar("+bw+", &"+bufVar+", "+expr+", 1<<20); err != nil { return nil, err }"), nil
        case "array":
            opt, _ := x[1].(map[string]any)
            et := opt["type"]
            var sb strings.Builder
            sb.WriteString(pad("if _, err := types.WriteVarInt("+bw+", int32(len("+expr+"))); err != nil { return nil, err }"))
            sb.WriteString(pad("for i := range "+expr+" {"))
            inner, err := emitWrite(expr+"[i]", et, bw, bufVar, indent+"\t")
            if err != nil { return "", err }
            sb.WriteString(inner)
            sb.WriteString(pad("}"))
            return sb.String(), nil
        case "option":
            inner := x[1]
            var sb strings.Builder
            sb.WriteString(pad("if "+expr+" != nil {"))
            sb.WriteString(pad("if _, err := types.WriteBool(&"+bufVar+", true); err != nil { return nil, err }"))
            sb.WriteString(pad("v := *"+expr))
            innerW, err := emitWrite("v", inner, bw, bufVar, indent+"\t")
            if err != nil { return "", err }
            sb.WriteString(innerW)
            sb.WriteString(pad("} else { if _, err := types.WriteBool(&"+bufVar+", false); err != nil { return nil, err } }"))
            return sb.String(), nil
        case "container":
            fl, _ := x[1].([]any)
            var sb strings.Builder
            for _, it := range fl {
                fm, _ := it.(map[string]any)
                if fm == nil { continue }
                w, err := emitWrite(expr+"."+exportIdent(fmt.Sprint(fm["name"])) , fm["type"], bw, bufVar, indent)
                if err != nil { return "", err }
                sb.WriteString(w)
            }
            return sb.String(), nil
        case "mapper":
            // expect string label; reverse map to varint
            conf, _ := x[1].(map[string]any)
            mm, _ := conf["mappings"].(map[string]any)
            // generate a switch on string label
            var sb strings.Builder
            sb.WriteString(pad("switch "+expr+" {"))
            for k, v := range mm {
                id, _ := parseHexOrDec(k)
                sb.WriteString(pad(fmt.Sprintf("case \"%s\": if _, err := types.WriteVarInt("+bw+", %d); err != nil { return nil, err }", escape(fmt.Sprint(v)), id)))
            }
            sb.WriteString(pad("default: // unknown label: encode as 0\n if _, err := types.WriteVarInt("+bw+", 0); err != nil { return nil, err } }"))
            return sb.String(), nil
        case "switch":
            conf, _ := x[1].(map[string]any)
            fields, _ := conf["fields"].(map[string]any)
            keys := make([]string, 0, len(fields))
            for k := range fields { keys = append(keys, k) }
            sort.Strings(keys)
            var sb strings.Builder
            for i, k := range keys {
                cond := "if"; if i>0 { cond = "} else if" }
                sb.WriteString(pad(cond+" "+expr+"."+exportIdent(k)+" != nil {"))
                sb.WriteString(pad("v := *"+expr+"."+exportIdent(k)))
                inner, err := emitWrite("v", fields[k], bw, bufVar, indent+"\t")
                if err != nil { return "", err }
                sb.WriteString(inner)
            }
            if def, ok := conf["default"]; ok {
                sb.WriteString(pad("} else if "+expr+".Default != nil { v := *"+expr+".Default "))
                inner, err := emitWrite("v", def, bw, bufVar, indent)
                if err != nil { return "", err }
                sb.WriteString(inner)
            }
            sb.WriteString(pad("}"))
            return sb.String(), nil
        case "bitfield":
            arr, _ := x[1].([]any)
            var sb strings.Builder
            sb.WriteString(pad("{ var out uint64; off := 0"))
            // compute total size first
            total := 0
            for _, it := range arr { fm,_ := it.(map[string]any); total += int(numFrom(fm["size"])) }
            sb.Reset()
            sb.WriteString(pad(fmt.Sprintf("{ var out uint64; off := %d", total)))
            for _, it := range arr {
                fm,_ := it.(map[string]any)
                name := exportIdent(fmt.Sprint(fm["name"]))
                size := int(numFrom(fm["size"]))
                signed := boolFrom(fm["signed"]) 
                sb.WriteString(pad(fmt.Sprintf("off -= %d", size)))
                if signed {
                    sb.WriteString(pad(fmt.Sprintf("val := %s.%s; if val < 0 { val = (1<<%d)+val }; out |= (uint64(val) & ((1<<%d)-1)) << off", expr, name, size, size)))
                } else {
                    sb.WriteString(pad(fmt.Sprintf("out |= (uint64(%s.%s) & ((1<<%d)-1)) << off", expr, name, size)))
                }
            }
            sb.WriteString(pad("if _, err := types.WriteI64(&"+bufVar+", int64(out)); err != nil { return nil, err } }"))
            return sb.String(), nil
        case "bitflags":
            conf, _ := x[1].(map[string]any)
            ft := fmt.Sprint(conf["type"]) 
            flags, _ := conf["flags"].([]any)
            var sb strings.Builder
            sb.WriteString(pad("{ var bits uint64"))
            for i, it := range flags {
                sb.WriteString(pad(fmt.Sprintf("if %s.%s { bits |= 1<<%d }", expr, exportIdent(fmt.Sprint(it)), i)))
            }
            if ft == "u8" { sb.WriteString(pad("if _, err := types.WriteU8(&"+bufVar+", uint8(bits)); err != nil { return nil, err }")) } else { sb.WriteString(pad("if _, err := types.WriteVarInt("+bw+", int32(bits)); err != nil { return nil, err }")) }
            sb.WriteString(pad("}"))
            return sb.String(), nil
        case "registryEntryHolder", "registryEntryHolderSet":
            conf, _ := x[1].(map[string]any)
            var sb strings.Builder
            sb.WriteString(pad("if "+expr+".Name != nil { if _, err := types.WriteBool(&"+bufVar+", true); err != nil { return nil, err }; if _, err := types.WriteString("+bw+", &"+bufVar+", *"+expr+".Name, 1<<20); err != nil { return nil, err } } else { if _, err := types.WriteBool(&"+bufVar+", false); err != nil { return nil, err }"))
            if om, ok := conf["otherwise"].(map[string]any); ok {
                sb.WriteString(pad("if "+expr+".Data != nil { v := *"+expr+".Data"))
                inner, err := emitWrite("v", om["type"], bw, bufVar, indent+"\t")
                if err != nil { return "", err }
                sb.WriteString(inner)
                sb.WriteString(pad("}"))
            }
            sb.WriteString(pad("}"))
            return sb.String(), nil
        case "entityMetadataLoop":
            // write entries then end marker 0xFF
            conf, _ := x[1].(map[string]any)
            entry := conf["type"]
            var sb strings.Builder
            sb.WriteString(pad("for i := range "+expr+" { v := "+expr+"[i]"))
            inner, err := emitWrite("v", entry, bw, bufVar, indent+"\t")
            if err != nil { return "", err }
            sb.WriteString(inner)
            sb.WriteString(pad("}"))
            sb.WriteString(pad("if _, err := types.WriteU8(&"+bufVar+", 0xff); err != nil { return nil, err }"))
            return sb.String(), nil
        default:
            return pad("// unsupported tag: "+tag), nil
        }
    }
    return "", fmt.Errorf("unsupported spec kind")
}

func isPrimOrSpecial(s string) bool {
    if isPrim(s) { return true }
    switch s { case "UUID","restBuffer","void","optvarint": return true }
    return false
}

func collectRefs(spec any, gm map[string]any, seen map[string]bool) {
    switch x := spec.(type) {
    case string:
        if !isPrimOrSpecial(x) {
            if !seen[x] { seen[x] = true; if gm != nil { collectRefs(gm[x], gm, seen) } }
        }
    case []any:
        if len(x) == 0 { return }
        tag := fmt.Sprint(x[0])
        switch tag {
        case "array":
            opt, _ := x[1].(map[string]any)
            collectRefs(opt["type"], gm, seen)
        case "option":
            collectRefs(x[1], gm, seen)
        case "container":
            fl, _ := x[1].([]any)
            for _, it := range fl { fm,_ := it.(map[string]any); if fm!=nil { collectRefs(fm["type"], gm, seen) } }
        case "mapper":
            conf,_ := x[1].(map[string]any)
            collectRefs(conf["type"], gm, seen)
        case "switch":
            conf,_ := x[1].(map[string]any)
            fields,_ := conf["fields"].(map[string]any)
            for _, v := range fields { collectRefs(v, gm, seen) }
            if def, ok := conf["default"]; ok { collectRefs(def, gm, seen) }
        case "buffer","bitfield","bitflags":
            // no named refs
        case "registryEntryHolder","registryEntryHolderSet":
            conf,_ := x[1].(map[string]any)
            if om, ok := conf["otherwise"].(map[string]any); ok { collectRefs(om["type"], gm, seen) }
        case "entityMetadataLoop":
            conf,_ := x[1].(map[string]any)
            collectRefs(conf["type"], gm, seen)
        default:
            // named ref alias
            if !isPrimOrSpecial(tag) { if !seen[tag] { seen[tag]=true; if gm!=nil { collectRefs(gm[tag], gm, seen) } } }
        }
    }
}

func emitTypedPackets(outDir, version string, m map[string]any) error {
    // make global types visible for ref resolution
    if gm, ok := m["types"].(map[string]any); ok { genTypes = gm } else { genTypes = nil }
    var sb strings.Builder
    sb.WriteString("package packets\n\n")
    sb.WriteString("// Code generated by tools/protogen; DO NOT EDIT.\n")
    sb.WriteString("// Source: minecraft-data "+version+"\n\n")
    sb.WriteString("import (\n\t\"bytes\"\n\t\"fmt\"\n\t\"io\"\n\t\"github.com/benj/mc-proto-go/types\"\n)\n\n")
    sb.WriteString("type byteWriter struct{ io.ByteWriter }\n\n")

    // Determine named type subset used by selected packet states
    used := map[string]bool{}
    gm, _ := m["types"].(map[string]any)

    // Limit typed packet emission to stable, smaller states first.
    states := []string{"handshaking","status","login","configuration"}
    // Collect refs
    for _, st := range states {
        sraw, ok := m[st].(map[string]any); if !ok { continue }
        for _, dir := range []string{"toClient","toServer"} {
            draw, ok := sraw[dir].(map[string]any); if !ok { continue }
            traw, ok := draw["types"].(map[string]any); if !ok { continue }
            for k, spec := range traw { if strings.HasPrefix(k, "packet_") && k != "packet" { collectRefs(spec, gm, used) } }
        }
    }
    // Emit only used named types (deterministic order)
    uk := make([]string, 0, len(used))
    for k := range used { uk = append(uk, k) }
    sort.Strings(uk)
    for _, k := range uk {
        v := gm[k]
        if s, ok := v.(string); ok && s == "native" {
            // synthesize thin wrappers for common natives we reference
            kn := exportIdent(k)
            switch k {
            case "anonymousNbt":
                sb.WriteString("type "+kn+" []byte\n")
                sb.WriteString("func (p *"+kn+") UnmarshalFrom(br *bytes.Reader) error { b, _, err := types.ReadBytesVar(br, br, 1<<22); if err != nil { return err }; *p = "+kn+"(b); return nil }\n")
                sb.WriteString("func (p "+kn+") MarshalTo(bw byteWriter, buf *bytes.Buffer) error { _, err := types.WriteBytesVar(bw, buf, []byte(p), 1<<22); return err }\n\n")
                continue
            case "anonOptionalNbt":
                sb.WriteString("type "+kn+" []byte\n")
                sb.WriteString("func (p *"+kn+") UnmarshalFrom(br *bytes.Reader) error { present, _, err := types.ReadBool(br); if err != nil { return err }; if !present { *p = nil; return nil }; b, _, err := types.ReadBytesVar(br, br, 1<<22); if err != nil { return err }; *p = "+kn+"(b); return nil }\n")
                sb.WriteString("func (p "+kn+") MarshalTo(bw byteWriter, buf *bytes.Buffer) error { if p == nil { _, err := types.WriteBool(buf, false); return err }; if _, err := types.WriteBool(buf, true); err != nil { return err }; _, err := types.WriteBytesVar(bw, buf, []byte(p), 1<<22); return err }\n\n")
                continue
            default:
                // skip other natives for now
                continue
            }
        }
        gt, decls, err := goType(v, exportIdent(k), "")
        if err != nil { continue }
        sb.WriteString("type "+exportIdent(k)+" "+gt+"\n\n")
        for _, d := range decls { sb.WriteString(d.code) }
        sb.WriteString("func (p *"+exportIdent(k)+") UnmarshalFrom(br *bytes.Reader) error {\n")
        sb.WriteString("\tvar tmp "+gt+"\n")
        rcode, err := emitRead("tmp", v, "br", "\t", exportIdent(k), "", "tmp")
        if err == nil { sb.WriteString(rcode) }
        sb.WriteString("\t_ = br\n\t*p = "+exportIdent(k)+"(tmp)\n\treturn nil\n}\n\n")
        sb.WriteString("func (p "+exportIdent(k)+") MarshalTo(bw byteWriter, buf *bytes.Buffer) error {\n")
        sb.WriteString("\tv := "+gt+"(p)\n")
        wcode, err := emitWrite("v", v, "bw", "*buf", "\t")
        if err == nil { sb.WriteString(strings.ReplaceAll(wcode, "return nil, err", "return err")) }
        sb.WriteString("\t_ = bw\n\treturn nil\n}\n\n")
    }
    for _, st := range states {
        sraw, ok := m[st].(map[string]any); if !ok { continue }
        for _, dir := range []string{"toClient","toServer"} {
            draw, ok := sraw[dir].(map[string]any); if !ok { continue }
            traw, ok := draw["types"].(map[string]any); if !ok { continue }
            // enumerate packet_* containers (excluding key "packet")
            keys := make([]string, 0, 64)
            for k := range traw { if strings.HasPrefix(k, "packet_") && k != "packet" { keys = append(keys, k) } }
            sort.Strings(keys)
            for _, k := range keys {
                spec := traw[k]
                arr, _ := spec.([]any)
                if len(arr) < 2 || fmt.Sprint(arr[0]) != "container" { continue }
                // Build struct type
                typeName := typedPacketTypeName(version, st, dir, k)
                // declare struct fields
                fl, _ := arr[1].([]any)
                var decls []tdecl
                sb.WriteString("type "+typeName+" struct{\n")
                for _, it := range fl {
                    fm, _ := it.(map[string]any)
                    if fm == nil { continue }
                    fName := exportIdent(fmt.Sprint(fm["name"]))
                    gt, more, err := goType(fm["type"], typeName, fName)
                    if err != nil { continue }
                    decls = append(decls, more...)
                    sb.WriteString("\t"+fName+" "+gt+"\n")
                }
                sb.WriteString("}\n\n")
                // write nested decls after struct
                for _, d := range decls { sb.WriteString(d.code) }
                // Unmarshal
                sb.WriteString("func (p *"+typeName+") UnmarshalBinary(data []byte) error {\n\tbr := bytes.NewReader(data)\n")
                for _, it := range fl {
                    fm, _ := it.(map[string]any)
                    if fm == nil { continue }
                rcode, err := emitRead("p."+exportIdent(fmt.Sprint(fm["name"])) , fm["type"], "br", "\t", typeName, fmt.Sprint(fm["name"]), "p")
                if err != nil { continue }
                sb.WriteString(rcode)
                }
                sb.WriteString("\t_ = br\n\treturn nil\n}\n\n")
                // Marshal
                sb.WriteString("func (p *"+typeName+") MarshalBinary() ([]byte, error) {\n\tvar buf bytes.Buffer\n\tbw := byteWriter{&buf}\n")
                for _, it := range fl {
                    fm, _ := it.(map[string]any)
                    if fm == nil { continue }
                    wcode, err := emitWrite("p."+exportIdent(fmt.Sprint(fm["name"])) , fm["type"], "bw", "buf", "\t")
                    if err != nil { continue }
                    sb.WriteString(wcode)
                }
                sb.WriteString("\t_ = bw\n\treturn buf.Bytes(), nil\n}\n\n")
            }
        }
    }

    // Write file
    if err := os.MkdirAll(outDir, 0o755); err != nil { return err }
    name := strings.ReplaceAll(version, ".", "_")
    formatted, err := format.Source([]byte(sb.String()))
    if err != nil {
        _ = os.MkdirAll(outDir, 0o755)
        _ = os.WriteFile(filepath.Join(outDir, "pc_"+name+"_gen_unformatted.go"), []byte(sb.String()), 0o644)
        return err
    }
    return os.WriteFile(filepath.Join(outDir, "pc_"+name+"_gen.go"), formatted, 0o644)
}

// -------------------- Entities emission --------------------

func emitEntities(outDir, version, dataDir string) error {
    path := filepath.Join(dataDir, "data", "pc", version, "entities.json")
    b, err := os.ReadFile(path)
    if err != nil { return err }
    var arr []map[string]any
    if err := json.Unmarshal(b, &arr); err != nil { return err }
    var sb strings.Builder
    sb.WriteString("package entities\n\n")
    sb.WriteString("// Code generated by tools/protogen; DO NOT EDIT.\n")
    sb.WriteString("// Source: minecraft-data "+version+"\n\n")
    sb.WriteString("type ID int\n\n")
    sb.WriteString("type Info struct{ ID ID; Name string; DisplayName string; Width float64; Height float64; Type string; Category string }\n\n")
    sb.WriteString("const (\n")
    // stable order by id
    sort.Slice(arr, func(i,j int) bool { return int(numFrom(arr[i]["id"])) < int(numFrom(arr[j]["id"])) })
    for _, e := range arr {
        name := exportIdent(fmt.Sprint(e["name"]))
        id := int(numFrom(e["id"]))
        sb.WriteString(fmt.Sprintf("\t%s ID = %d\n", name, id))
    }
    sb.WriteString(")\n\n")
    sb.WriteString("var All = map[ID]Info{\n")
    for _, e := range arr {
        name := exportIdent(fmt.Sprint(e["name"]))
        sb.WriteString(fmt.Sprintf("\t%s: {ID:%s, Name:\"%s\", DisplayName:\"%s\", Width:%v, Height:%v, Type:\"%s\", Category:\"%s\"},\n", name, name, escape(fmt.Sprint(e["name"])), escape(fmt.Sprint(e["displayName"])), e["width"], e["height"], escape(fmt.Sprint(e["type"])), escape(fmt.Sprint(e["category"])) ))
    }
    sb.WriteString("}\n")
    if err := os.MkdirAll(outDir, 0o755); err != nil { return err }
    name := strings.ReplaceAll(version, ".", "_")
    formatted, err := format.Source([]byte(sb.String()))
    if err != nil { return err }
    return os.WriteFile(filepath.Join(outDir, "pc_"+name+".go"), formatted, 0o644)
}
