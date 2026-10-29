package worldgen

import (
	"fmt"

	"mc-go/internal/world"
)

// TreeDebug, when set, receives tree placement traces (debug only).
var TreeDebug func(center [2]int32, msg string)

func stateName(b world.Block) string {
	n, props, _ := world.DescribeState(b)
	return fmt.Sprint(n, props)
}

func treeTrace(rg *Region, format string, args ...any) {
	if TreeDebug != nil {
		TreeDebug([2]int32{rg.center.CX, rg.center.CZ}, fmt.Sprintf(format, args...))
	}
}
