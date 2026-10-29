package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"sort"
	"sync"
)

type testInstancePack struct {
	tests, environments                 map[string]map[string]any
	testOverrides, environmentOverrides map[string]map[string]any
}

var capturedTestEnvironments = sync.OnceValue(func() map[string]map[string]any {
	config, err := javaConfigFor(776)
	if err != nil {
		panic(err)
	}
	for _, reg := range config.registries {
		if reg.key == "minecraft:test_environment" {
			rows, err := gameTestRegistryRows(reg.body)
			if err != nil {
				panic(err)
			}
			values := map[string]map[string]any{}
			for _, row := range rows {
				if len(row.data) > 0 {
					value, err := nbt.ReadUnnamed(bytes.NewReader(row.data))
					if err != nil {
						panic(err)
					}
					values[row.name], _ = value.(map[string]any)
				}
			}
			return values
		}
	}
	return map[string]map[string]any{}
})

type gameTestRegistryRow struct {
	name    string
	present bool
	data    []byte
}

func gameTestRegistryRows(body []byte) ([]gameTestRegistryRow, error) {
	r := bytes.NewReader(body)
	if _, err := protocol.ReadString(r); err != nil {
		return nil, err
	}
	count, err := protocol.ReadVarInt(r)
	if err != nil {
		return nil, err
	}
	if count < 0 || count > 1<<20 {
		return nil, fmt.Errorf("invalid test registry count%d", count)
	}
	rows := make([]gameTestRegistryRow, 0, count)
	for i := int32(0); i < count; i++ {
		name, err := protocol.ReadString(r)
		if err != nil {
			return nil, err
		}
		present, err := protocol.ReadBool(r)
		if err != nil {
			return nil, err
		}
		var payload []byte
		if present {
			start := len(body) - r.Len()
			if _, err := nbt.ReadUnnamed(r); err != nil {
				return nil, err
			}
			payload = body[start : len(body)-r.Len()]
		}
		rows = append(rows, gameTestRegistryRow{name, present, payload})
	}
	if r.Len() != 0 {
		return nil, fmt.Errorf("trailing test registry bytes")
	}
	return rows, nil
}

// Existing row bytes/indices stay intact; server-owned resources replace a
// row or append their validated native codec values. No global catalog mutates.
func gameTestRegistryOverlay(reg configRegistry, overrides map[string]map[string]any) ([]byte, error) {
	if len(overrides) == 0 {
		return reg.body, nil
	}
	rows, err := gameTestRegistryRows(reg.body)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, row := range rows {
		seen[row.name] = true
	}
	var added []string
	for name := range overrides {
		if !seen[name] {
			added = append(added, name)
		}
	}
	sort.Strings(added)
	for _, name := range added {
		rows = append(rows, gameTestRegistryRow{name: name})
	}
	var out protocol.Buffer
	out.String(reg.key)
	out.VarInt(int32(len(rows)))
	for _, row := range rows {
		if value, ok := overrides[row.name]; ok {
			var raw bytes.Buffer
			if err := nbt.WriteUnnamed(&raw, value); err != nil {
				return nil, err
			}
			row.present = true
			row.data = raw.Bytes()
		}
		out.String(row.name)
		out.Bool(row.present)
		out.Raw(row.data)
	}
	return out.Bytes(), nil
}

func decodeGameTestJSON(body []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("trailing GameTest JSON")
	}
	if value == nil {
		return nil, fmt.Errorf("expected GameTest object")
	}
	return value, nil
}
func gameTestResource(value any) (string, error) {
	raw, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("expected GameTest resource key")
	}
	name, ok := jigsawIdentifier(raw)
	if !ok {
		return "", fmt.Errorf("invalid GameTest resource key%q", raw)
	}
	return name, nil
}
func gameTestIntField(in map[string]any, key string, fallback, low, high int32, required bool) (int32, error) {
	value, present := in[key]
	if !present {
		if required {
			return 0, fmt.Errorf("missing%s", key)
		}
		return fallback, nil
	}
	number, err := advancementJSONInt(value)
	if err != nil || number < low || number > high {
		return 0, fmt.Errorf("invalid%s", key)
	}
	return number, nil
}
func gameTestBoolField(in map[string]any, key string, fallback bool) (bool, error) {
	value, present := in[key]
	if !present {
		return fallback, nil
	}
	result, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("invalid%s", key)
	}
	return result, nil
}

func parseGameTestDefinition(in map[string]any) (map[string]any, error) {
	kind, err := gameTestResource(in["type"])
	if err != nil {
		return nil, err
	}
	if kind != "minecraft:function" && kind != "minecraft:block_based" {
		return nil, fmt.Errorf("unsupported GameTest type%s", kind)
	}
	environment, err := gameTestResource(in["environment"])
	if err != nil {
		return nil, err
	}
	structure, err := gameTestResource(in["structure"])
	if err != nil {
		return nil, err
	}
	out := map[string]any{"type": kind, "environment": environment, "structure": structure}
	if kind == "minecraft:function" {
		function, err := gameTestResource(in["function"])
		if err != nil {
			return nil, err
		}
		out["function"] = function
	}
	for _, field := range []struct {
		key                 string
		fallback, low, high int32
		required            bool
	}{
		{"max_ticks", 0, 1, 2147483647, true}, {"setup_ticks", 0, 0, 2147483647, false}, {"max_attempts", 1, 1, 2147483647, false}, {"required_successes", 1, 1, 2147483647, false}, {"padding", 0, 0, 128, false},
	} {
		value, err := gameTestIntField(in, field.key, field.fallback, field.low, field.high, field.required)
		if err != nil {
			return nil, err
		}
		if field.required || value != field.fallback {
			out[field.key] = value
		}
	}
	for _, field := range []struct {
		key      string
		fallback bool
	}{{"required", true}, {"manual_only", false}, {"sky_access", false}} {
		value, err := gameTestBoolField(in, field.key, field.fallback)
		if err != nil {
			return nil, err
		}
		if value != field.fallback {
			out[field.key] = nbtBool(value)
		}
	}
	if value, present := in["rotation"]; present {
		rotation, ok := value.(string)
		valid := false
		for _, name := range testInstanceRotations {
			valid = valid || rotation == name
		}
		if !ok || !valid {
			return nil, fmt.Errorf("invalid test rotation")
		}
		if rotation != "none" {
			out["rotation"] = rotation
		}
	}
	return out, nil
}

// Empty/nested all_of environments have no setup/teardown side effects.
// Other environment types require their own measured activation lifecycle.
func parseGameTestEnvironment(in map[string]any) (map[string]any, error) {
	kind, err := gameTestResource(in["type"])
	if err != nil {
		return nil, err
	}
	if kind != "minecraft:all_of" {
		return nil, fmt.Errorf("unsupported test environment%s", kind)
	}
	values, ok := in["definitions"].([]any)
	if !ok {
		return nil, fmt.Errorf("missing environment definitions")
	}
	children := nbt.List{Elem: nbt.TagEnd}
	for _, value := range values {
		child, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid child environment")
		}
		parsed, err := parseGameTestEnvironment(child)
		if err != nil {
			return nil, err
		}
		children.Elem = nbt.TagCompound
		children.Items = append(children.Items, parsed)
	}
	return map[string]any{"type": kind, "definitions": children}, nil
}
