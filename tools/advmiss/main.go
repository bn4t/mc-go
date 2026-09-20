// Command advmiss lists advancement ids absent from the def-wire table.
package main

import (
	"fmt"

	"mc-go/internal/data"
)

func main() {
	w := data.AdvDefWire
	var miss []string
	for path := range data.Advancements() {
		id := "minecraft:" + path
		if _, ok := w[id]; !ok {
			miss = append(miss, id)
		}
	}
	fmt.Println("missing:", miss, "total wire:", len(w))
}
