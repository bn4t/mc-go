package server

import (
	"bytes"
	"math"
	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

const testInstanceBlockEntityID = "minecraft:test_instance_block"

var testInstanceRotations = [...]string{"none", "clockwise_90", "180", "counterclockwise_90"}
var testInstanceStatuses = [...]string{"cleared", "running", "finished"}

type testInstanceData struct {
	test             string
	size             [3]int32
	rotation, status int32
	ignoreEntities   bool
	errorMessage     any
}

func (d testInstanceData) native() map[string]any {
	out := map[string]any{"size": []int32{d.size[0], d.size[1], d.size[2]}, "rotation": testInstanceRotations[d.rotation], "ignore_entities": nbtBool(d.ignoreEntities), "status": testInstanceStatuses[d.status]}
	if d.test != "" {
		out["test"] = d.test
	}
	if d.errorMessage != nil {
		out["error_message"] = cloneNBTValue(d.errorMessage)
	}
	return out
}
func testInstanceComponent(value any) (any, bool) {
	switch v := value.(type) {
	case string:
		return v, true
	case map[string]any:
		valid := false
		for _, key := range []string{"text", "translate", "selector", "keybind", "nbt"} {
			if _, ok := v[key].(string); ok {
				valid = true
			}
		}
		if _, ok := v["score"].(map[string]any); ok {
			valid = true
		}
		if !valid {
			return nil, false
		}
		if len(v) == 1 {
			if text, ok := v["text"].(string); ok {
				return text, true
			}
		}
		return cloneNBTValue(v), true
	case nbt.List:
		if len(v.Items) == 0 {
			return nil, false
		}
		for _, child := range v.Items {
			if _, ok := testInstanceComponent(child); !ok {
				return nil, false
			}
		}
		tag, _, err := parseSNBTTag(componentSNBT(v), 0)
		if err != nil {
			return nil, false
		}
		return compWire(tag), true
	}
	return nil, false
}

// These fields use DFU's numeric codecs, not ValueInput.getIntOr: floating
// numbers truncate toward zero before Java's saturated int conversion.
func testInstanceCodecInt(value any) (int32, bool) {
	switch n := value.(type) {
	case float32:
		return testInstanceFloatInt(float64(n)), true
	case float64:
		return testInstanceFloatInt(n), true
	default:
		return nbtInt32(value)
	}
}
func testInstanceFloatInt(value float64) int32 {
	if math.IsNaN(value) {
		return 0
	}
	if value >= math.MaxInt32 {
		return math.MaxInt32
	}
	if value <= math.MinInt32 {
		return math.MinInt32
	}
	return int32(value)
}
func testInstanceCodecBool(value any) (bool, bool) {
	switch n := value.(type) {
	case bool:
		return n, true
	case int8:
		return n != 0, true
	case int16:
		return n != 0, true
	case int32:
		return n != 0, true
	case int64:
		return n != 0, true
	case float32:
		return n != 0, true
	case float64:
		return n != 0, true
	}
	return false, false
}
func testInstanceVector(value any) ([3]int32, bool) {
	var out [3]int32
	var values []any
	switch v := value.(type) {
	case []int32:
		for _, n := range v {
			values = append(values, n)
		}
	case []int64:
		for _, n := range v {
			values = append(values, n)
		}
	case []int8:
		for _, n := range v {
			values = append(values, n)
		}
	case nbt.List:
		values = v.Items
	default:
		return out, false
	}
	if len(values) != 3 {
		return out, false
	}
	for i, n := range values {
		var ok bool
		out[i], ok = testInstanceCodecInt(n)
		if !ok {
			return out, false
		}
	}
	return out, true
}
func testInstanceDataFromNative(value any) (out testInstanceData, ok bool) {
	fields, valid := value.(map[string]any)
	if !valid {
		return out, false
	}
	out.size, valid = testInstanceVector(fields["size"])
	if !valid {
		return out, false
	}
	rotation, valid := fields["rotation"].(string)
	if !valid {
		return out, false
	}
	found := false
	for i, name := range testInstanceRotations {
		if rotation == name {
			out.rotation = int32(i)
			found = true
			break
		}
	}
	if !found {
		return out, false
	}
	status, valid := fields["status"].(string)
	if !valid {
		return out, false
	}
	found = false
	for i, name := range testInstanceStatuses {
		if status == name {
			out.status = int32(i)
			found = true
			break
		}
	}
	if !found {
		return out, false
	}
	out.ignoreEntities, valid = testInstanceCodecBool(fields["ignore_entities"])
	if !valid {
		return out, false
	}
	if test, present := fields["test"]; present {
		name, isString := test.(string)
		if !isString {
			return out, false
		}
		out.test, valid = jigsawIdentifier(name)
		if !valid {
			return out, false
		}
	}
	if message, present := fields["error_message"]; present {
		out.errorMessage, valid = testInstanceComponent(message)
		if !valid {
			return out, false
		}
	}
	return out, true
}
func testInstanceSavedFields(in map[string]any, current *genericBlockEntity) map[string]any {
	data := testInstanceData{}
	if current != nil && current.id == testInstanceBlockEntityID {
		if prior, ok := testInstanceDataFromNative(current.fields["data"]); ok {
			data = prior
		}
	}
	if loaded, ok := testInstanceDataFromNative(in["data"]); ok {
		data = loaded
	}
	out := map[string]any{"data": data.native()}
	if errors, ok := in["errors"].(nbt.List); ok && errors.Elem == nbt.TagCompound {
		markers := nbt.List{Elem: nbt.TagCompound}
		for _, entry := range errors.Items {
			m, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			pos, ok := testInstanceVector(m["pos"])
			if !ok {
				continue
			}
			message, ok := testInstanceComponent(m["text"])
			if !ok {
				continue
			}
			markers.Items = append(markers.Items, map[string]any{"pos": []int32{pos[0], pos[1], pos[2]}, "text": message})
		}
		if len(markers.Items) > 0 {
			out["errors"] = markers
		}
	}
	if components, ok := in["components"]; ok {
		out["components"] = cloneNBTValue(components)
	}
	return out
}

type testInstanceEdit struct {
	pos    world.Pos
	action int32
	data   testInstanceData
}

func readTestInstanceEdit(raw []byte) (out testInstanceEdit, ok bool) {
	r := protocol.NewPacketReader(raw)
	x, y, z, err := r.Position()
	if err != nil {
		return
	}
	out.pos = world.Pos{X: int(x), Y: int(y), Z: int(z)}
	if out.action, err = r.VarInt(); err != nil {
		return
	}
	if out.action < 0 || out.action > 6 {
		out.action = 0
	}
	hasTest, err := r.Bool()
	if err != nil {
		return
	}
	if hasTest {
		name, e := r.String()
		if e != nil {
			return
		}
		var valid bool
		out.data.test, valid = jigsawIdentifier(name)
		if !valid {
			return
		}
	}
	for i := range out.data.size {
		if out.data.size[i], err = r.VarInt(); err != nil {
			return
		}
	}
	if out.data.rotation, err = r.VarInt(); err != nil {
		return
	}
	out.data.rotation = (out.data.rotation%4 + 4) % 4
	if out.data.ignoreEntities, err = r.Bool(); err != nil {
		return
	}
	if out.data.status, err = r.VarInt(); err != nil {
		return
	}
	if out.data.status < 0 || out.data.status >= 3 {
		out.data.status = 0
	}
	hasError, err := r.Bool()
	if err != nil {
		return
	}
	if hasError {
		remaining, e := r.Read(r.Remaining())
		if e != nil {
			return
		}
		value, e := nbt.ReadUnnamed(bytes.NewReader(remaining))
		if e != nil {
			return
		}
		var valid bool
		out.data.errorMessage, valid = testInstanceComponent(value)
		if !valid {
			return
		}
	}
	return out, true
}
