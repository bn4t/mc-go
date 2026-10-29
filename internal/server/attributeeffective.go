package server

import "math"

// sanitizeAttributeValue applies the registered range after all modifier
// passes. Stored bases and modifier amounts remain raw; entity suppliers
// continue to choose their own defaults independently of registry defaults.
func sanitizeAttributeValue(name string, value float64) float64 {
	info, ok := attributeInfo[name]
	if !ok {
		return value
	}
	if math.IsNaN(value) {
		return info.minValue
	}
	return max(info.minValue, min(info.maxValue, value))
}
