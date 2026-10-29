package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// These conversions are for JSON codecs, not NBT numeric tags. JsonOps passes
// a lazily parsed decimal Number to Codec.INT; its intValue truncates toward
// zero and keeps the low signed32 bits, unlike a Java floating-point cast.
func advancementJSONInt(raw any) (int32, error) {
	var text string
	switch value := raw.(type) {
	case json.Number:
		text = value.String()
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return 0, fmt.Errorf("invalid JSON integer")
		}
		text = strconv.FormatFloat(value, 'g', -1, 64)
	case int:
		text = strconv.FormatInt(int64(value), 10)
	case int32:
		return value, nil
	case int64:
		return int32(value), nil
	default:
		return 0, fmt.Errorf("expected JSON number")
	}
	if text == "" || !(text[0] == '-' || text[0] >= '0' && text[0] <= '9') || !json.Valid([]byte(text)) {
		return 0, fmt.Errorf("invalid JSON integer")
	}
	negative := strings.HasPrefix(text, "-")
	text = strings.TrimPrefix(text, "-")
	exponent := int64(0)
	if i := strings.IndexAny(text, "eE"); i >= 0 {
		var err error
		exponent, err = strconv.ParseInt(text[i+1:], 10, 64)
		if err != nil || exponent < math.MinInt32 || exponent > math.MaxInt32 {
			return 0, fmt.Errorf("invalid JSON exponent")
		}
		text = text[:i]
	}
	fraction := 0
	if point := strings.IndexByte(text, '.'); point >= 0 {
		fraction = len(text) - point - 1
		text = text[:point] + text[point+1:]
	}
	if scale := int64(fraction) - exponent; scale < math.MinInt32 || scale > math.MaxInt32 {
		return 0, fmt.Errorf("invalid JSON decimal scale")
	}
	// Very large positive shifts contain at least32 factors of2; very large
	// negative shifts have no integer digits. Neither needs a huge allocation.
	if exponent >= int64(fraction)+32 || exponent <= -int64(len(text)-fraction) {
		return 0, nil
	}
	shift := int(exponent) - fraction
	end := len(text)
	if shift < 0 {
		end += shift
	}
	var bits uint32
	for _, digit := range text[:end] {
		bits = bits*10 + uint32(digit-'0')
	}
	for ; shift > 0; shift-- {
		bits *= 10
	}
	if negative {
		bits = 0 - bits
	}
	return int32(bits), nil
}

func advancementJSONFloat(raw any) (float64, bool) {
	switch value := raw.(type) {
	case float64:
		return value, !math.IsNaN(value)
	case json.Number:
		number, err := value.Float64()
		return number, err == nil || math.IsInf(number, 0)
	}
	return 0, false
}

// Preserve exact numeric tokens where integer codecs or newly compiled tick
// predicates consume them. Other existing gameplay evaluators retain their
// previous float64 input shape until their own codec slices migrate.
func decodeAdvancementJSON(body []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var original map[string]any
	if err := decoder.Decode(&original); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("trailing advancement JSON")
	}
	var legacy func(any) any
	legacy = func(raw any) any {
		switch value := raw.(type) {
		case json.Number:
			if number, err := value.Float64(); err == nil {
				return number
			}
			return value
		case map[string]any:
			out := map[string]any{}
			for key, item := range value {
				out[key] = legacy(item)
			}
			return out
		case []any:
			out := make([]any, len(value))
			for i, item := range value {
				out[i] = legacy(item)
			}
			return out
		}
		return raw
	}
	if original == nil {
		return nil, fmt.Errorf("advancement must be an object")
	}
	normalized := legacy(original).(map[string]any)
	if reward, ok := original["rewards"].(map[string]any); ok {
		if experience, exists := reward["experience"]; exists {
			normalized["rewards"].(map[string]any)["experience"] = experience
		}
	}
	if display, ok := original["display"].(map[string]any); ok {
		if icon, ok := display["icon"].(map[string]any); ok {
			if count, exists := icon["count"]; exists {
				normalized["display"].(map[string]any)["icon"].(map[string]any)["count"] = count
			}
		}
	}
	if criteria, ok := original["criteria"].(map[string]any); ok {
		for name, value := range criteria {
			criterion, ok := value.(map[string]any)
			if !ok {
				continue
			}
			trigger, _ := criterion["trigger"].(string)
			if normalizeID(trigger) == "minecraft:tick" {
				if conditions, exists := criterion["conditions"]; exists {
					normalized["criteria"].(map[string]any)[name].(map[string]any)["conditions"] = conditions
				}
			}
		}
	}
	return normalized, nil
}
