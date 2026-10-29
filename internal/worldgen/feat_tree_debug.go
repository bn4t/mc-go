package worldgen

import "fmt"

// TreeDebug, when set, receives tree placement traces (debug only).
var TreeDebug func(center [2]int32, msg string)

func treeTrace(rg *Region, format string, args ...any) {
	if TreeDebug != nil {
		TreeDebug([2]int32{rg.center.CX, rg.center.CZ}, fmt.Sprintf(format, args...))
	}
}
