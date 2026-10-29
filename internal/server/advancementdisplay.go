package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
)

type advancementDisplay struct {
	title, description any
	icon               ItemStack
	frame, flags       int32
	background         string
	x, y               float32
}

func advancementJSONValue(value any) (any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	tag, end, parseErr := parseSNBTTag(string(encoded), 0)
	if parseErr != nil || end != len(encoded) {
		return nil, fmt.Errorf("invalid advancement JSON value")
	}
	return snbtToValue(tag), nil
}

// Components are parsed through the existing native value reader, then
// canonicalized as component values rather than preserving JSON syntax.
func advancementComponent(value any) (any, error) {
	native, err := advancementJSONValue(value)
	if err != nil {
		return nil, err
	}
	if !validAdvancementComponent(native) {
		return nil, fmt.Errorf("invalid advancement component")
	}

	return canonicalAdvancementComponent(native), nil
}
func validAdvancementComponent(value any) bool {
	switch value := value.(type) {
	case string:
		return true
	case nbt.List:
		if len(value.Items) == 0 {
			return false
		}
		for _, item := range value.Items {
			if !validAdvancementComponent(item) {
				return false
			}
		}
		return true
	case map[string]any:
		content := false
		for _, field := range []string{"text", "translate", "keybind", "selector", "nbt"} {
			if item, exists := value[field]; exists {
				if _, ok := item.(string); !ok {
					return false
				}
				content = true
			}
		}
		if score, exists := value["score"]; exists {
			if _, ok := score.(map[string]any); !ok {
				return false
			}
			content = true
		}
		if !content {
			return false
		}
		for _, field := range []string{"bold", "italic", "underlined", "strikethrough", "obfuscated"} {
			if item, exists := value[field]; exists {
				byteValue, ok := item.(int8)
				if !ok || byteValue < 0 || byteValue > 1 {
					return false
				}
			}
		}
		for _, field := range []string{"color", "font", "insertion"} {
			if item, exists := value[field]; exists {
				if _, ok := item.(string); !ok {
					return false
				}
			}
		}
		if extra, exists := value["extra"]; exists {
			list, ok := extra.(nbt.List)
			if !ok {
				return false
			}
			for _, item := range list.Items {
				if !validAdvancementComponent(item) {
					return false
				}
			}
		}
		if args, exists := value["with"]; exists {
			list, ok := args.(nbt.List)
			if !ok {
				return false
			}
			for _, item := range list.Items {
				switch item.(type) {
				case map[string]any, nbt.List:
					if !validAdvancementComponent(item) {
						return false
					}
				case string, int32, int64, float32, float64:
				default:
					return false
				}
			}
		}
		if separator, exists := value["separator"]; exists && !validAdvancementComponent(separator) {
			return false
		}
		if hover, exists := value["hover_event"]; exists {
			object, ok := hover.(map[string]any)
			if !ok {
				return false
			}
			if object["action"] == "show_text" && !validAdvancementComponent(object["value"]) {
				return false
			}
		}
		return true
	}
	return false
}

func canonicalAdvancementComponent(value any) any {
	if list, ok := value.(nbt.List); ok {
		if len(list.Items) == 0 {
			return ""
		}
		base := advancementComponentMap(canonicalAdvancementComponent(list.Items[0]))
		extras, _ := base["extra"].(nbt.List)
		for _, item := range list.Items[1:] {
			extras.Items = append(extras.Items, canonicalAdvancementComponent(item))
		}
		base["extra"] = advancementComponentList(extras.Items)
		return base
	}
	object, ok := value.(map[string]any)
	if !ok {
		return value
	}
	if extras, ok := object["extra"].(nbt.List); ok {
		var items []any
		for _, item := range extras.Items {
			items = append(items, canonicalAdvancementComponent(item))
		}
		if len(items) == 0 {
			delete(object, "extra")
		} else {
			object["extra"] = advancementComponentList(items)
		}
	}
	if args, ok := object["with"].(nbt.List); ok {
		items := make([]any, 0, len(args.Items))
		for _, arg := range args.Items {
			switch arg.(type) {
			case map[string]any, nbt.List:
				arg = canonicalAdvancementComponent(arg)
			}
			items = append(items, arg)
		}
		if len(items) == 0 {
			delete(object, "with")
		} else if list, err := argumentList(items); err == nil {
			object["with"] = list
		}
	}
	if hover, ok := object["hover_event"].(map[string]any); ok && hover["action"] == "show_text" {
		if contents, exists := hover["value"]; exists {
			hover["value"] = canonicalAdvancementComponent(contents)
		}
	}
	if len(object) == 1 {
		if text, ok := object["text"].(string); ok {
			return text
		}
	}
	return object
}
func advancementComponentMap(value any) map[string]any {
	if object, ok := cloneNBTValue(value).(map[string]any); ok {
		return object
	}
	return map[string]any{"text": value}
}
func advancementComponentList(items []any) nbt.List {
	plain := true
	for _, item := range items {
		if _, ok := item.(string); !ok {
			plain = false
			break
		}
	}
	if plain {
		return nbt.List{Elem: nbt.TagString, Items: items}
	}
	out := make([]any, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			out = append(out, map[string]any{"": text})
		} else {
			out = append(out, item)
		}
	}
	return nbt.List{Elem: nbt.TagCompound, Items: out}
}

func parseAdvancementDisplay(raw any) (*advancementDisplay, error) {
	object, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid advancement display")
	}
	title, exists := object["title"]
	if !exists {
		return nil, fmt.Errorf("missing advancement title")
	}
	description, exists := object["description"]
	if !exists {
		return nil, fmt.Errorf("missing advancement description")
	}
	display := &advancementDisplay{flags: 2}
	var err error
	display.title, err = advancementComponent(title)
	if err != nil {
		return nil, err
	}
	display.description, err = advancementComponent(description)
	if err != nil {
		return nil, err
	}
	rawIcon, ok := object["icon"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("missing advancement icon")
	}
	if count, exists := rawIcon["count"]; exists {
		n, ok := count.(float64)
		if !ok || n != math.Trunc(n) || n < 1 || n > 99 {
			return nil, fmt.Errorf("invalid advancement icon count")
		}
	}
	if components, exists := rawIcon["components"]; exists {
		if _, ok := components.(map[string]any); !ok {
			return nil, fmt.Errorf("invalid advancement icon components")
		}
	}
	icon, err := advancementJSONValue(rawIcon)
	if err != nil {
		return nil, err
	}
	iconMap, ok := icon.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("missing advancement icon")
	}
	display.icon = itemFromNBT(iconMap)
	if components, ok := rawIcon["components"].(map[string]any); ok {
		keys := make([]string, 0, len(components))
		for key := range components {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		var parts []string
		for _, key := range keys {
			value, err := json.Marshal(components[key])
			if err != nil {
				return nil, err
			}
			parts = append(parts, key+"="+string(value))
		}
		if len(parts) > 0 {
			patch, _, ok := parseItemPatch(strings.Join(parts, ","), 0)
			if !ok {
				return nil, fmt.Errorf("unsupported advancement icon component")
			}
			patch.ID, patch.Count = display.icon.ID, display.icon.Count
			display.icon = patch
		}
	}
	if display.icon.Empty() || display.icon.Count > 99 {
		return nil, fmt.Errorf("invalid advancement icon")
	}
	if value, exists := object["frame"]; exists {
		frame, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("invalid advancement frame")
		}
		switch frame {
		case "task":
		case "challenge":
			display.frame = 1
		case "goal":
			display.frame = 2
		default:
			return nil, fmt.Errorf("unknown advancement frame")
		}
	}
	if value, exists := object["background"]; exists {
		id, ok := value.(string)
		if !ok || id == "" {
			return nil, fmt.Errorf("invalid advancement background")
		}
		display.background = normalizeID(id)
		display.flags |= 1
	}
	for _, field := range []string{"show_toast", "hidden", "announce_to_chat"} {
		if value, exists := object[field]; exists {
			enabled, ok := value.(bool)
			if !ok {
				return nil, fmt.Errorf("invalid advancement display flag")
			}
			if field == "show_toast" && !enabled {
				display.flags &^= 2
			}
			if field == "hidden" && enabled {
				display.flags |= 4
			}
		}
	}
	return display, nil
}

func encodePackAdvancement(meta *advMeta) []byte {
	var b protocol.Buffer
	b.String(meta.id)
	b.Bool(meta.parent != "")
	if meta.parent != "" {
		b.String(meta.parent)
	}
	display := meta.display
	b.Bool(display != nil)
	if display != nil {
		b.Raw(advancementDisplayComponentContent(display.title))
		b.Raw(advancementDisplayComponentContent(display.description))
		b.VarInt(display.icon.ID)
		b.VarInt(display.icon.Count)
		writeSlotPatch(&b, display.icon)
		b.VarInt(display.frame)
		b.Int(display.flags)
		if display.flags&1 != 0 {
			b.String(display.background)
		}
		b.Float(display.x)
		b.Float(display.y)
	}
	b.VarInt(int32(len(meta.reqs)))
	for _, group := range meta.reqs {
		b.VarInt(int32(len(group)))
		for _, criterion := range group {
			b.String(criterion)
		}
	}
	telemetry, _ := meta.raw["sends_telemetry_event"].(bool)
	b.Bool(telemetry)
	return b.Bytes()
}

func packAdvancementChatName(meta *advMeta) component {
	display := meta.display
	color := advFrameColor(meta.frame)
	title := advancementComponentMap(display.title)
	hover := advancementComponentMap(display.title)
	if _, styled := hover["color"]; !styled {
		hover["color"] = color
	}
	extra, _ := hover["extra"].(nbt.List)
	extra.Items = append(extra.Items, "\n", cloneNBTValue(display.description))
	hover["extra"] = advancementComponentList(extra.Items)
	title["hover_event"] = map[string]any{"action": "show_text", "value": hover}
	return component{translate: "chat.square_brackets", color: color, with: []any{title}}
}

// Component compound fields retain codec insertion order for same-bucket NBT
// keys; the payload remains an ordinary unordered compound to clients.
func advancementDisplayComponentContent(value any) []byte {
	object, ok := value.(map[string]any)
	if !ok {
		return componentAnyContent(value)
	}
	var b bytes.Buffer
	b.WriteByte(nbt.TagCompound)
	for _, key := range nbt.OrderedKeysSeq(object, []string{"text", "translate", "with", "keybind", "score", "selector", "nbt", "extra", "color", "bold", "italic", "underlined", "strikethrough", "obfuscated", "insertion", "font", "click_event", "hover_event"}) {
		if err := nbt.WriteNamed(&b, key, object[key]); err != nil {
			return textComponentContent("")
		}
	}
	b.WriteByte(nbt.TagEnd)
	return b.Bytes()
}
