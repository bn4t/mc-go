// Command worldgendiff compares mc-go's terrain generator with a world
// the official server generated and saved: for every chunk saved at
// full status in the given region directory, it generates the same
// chunk and counts the blocks that differ, by (vanilla, mc-go) state
// pair. It measures how far generation is from vanilla while stages
// (features, structures) are still missing.
//
// Usage: go run ./tools/worldgendiff -region <world>/dimensions/minecraft/overworld/region -seed 12345 [-dim minecraft:overworld]
package main

import (
	"flag"
	"fmt"
	"math/bits"
	"os"
	"path/filepath"
	"sort"
	"strings"

	nbtpkg "mc-go/internal/nbt"
	"mc-go/internal/world"
	"mc-go/internal/worldgen"
)

func main() {
	region := flag.String("region", "", "region directory of a vanilla-generated dimension")
	seed := flag.Int64("seed", 0, "world seed")
	dim := flag.String("dim", "minecraft:overworld", "dimension")
	top := flag.Int("top", 40, "state pairs to list")
	chunk := flag.String("chunk", "", "only this chunk, cx,cz")
	flag.Parse()
	gen, err := worldgen.NewDimensionGenerator(*dim, *seed)
	if err != nil {
		fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(*region, "r.*.mca"))
	pairs := map[[2]string]int{}
	total, differ, chunks := 0, 0, 0
	for _, f := range files {
		cs, err := world.ReadRegionFile(f)
		if err != nil {
			fatal(err)
		}
		for _, nbt := range cs {
			if status, _ := nbt["Status"].(string); status != "minecraft:full" {
				continue
			}
			pos := [2]int32{int32(toInt(nbt["xPos"])), int32(toInt(nbt["zPos"]))}
			if *chunk != "" && *chunk != fmt.Sprintf("%d,%d", pos[0], pos[1]) {
				continue
			}
			chunks++
			got := gen.Chunk(pos[0], pos[1])
			sections, _ := nbt["sections"].(nbtpkg.List)
			for _, s := range sections.Items {
				sec := s.(map[string]any)
				sy := int32(toInt(sec["Y"]))
				states, ok := sec["block_states"].(map[string]any)
				if !ok {
					continue
				}
				ids := decodeStates(states)
				for i, want := range ids {
					x, z, y := int32(i&15), int32(i>>4&15), sy*16+int32(i>>8)
					if y < got.MinY {
						continue
					}
					have := worldgen.StateName(got.Block(x, y, z))
					total++
					if have != want {
						differ++
						pairs[[2]string{want, have}]++
					}
				}
			}
		}
	}
	fmt.Printf("%d chunks, %d blocks, %d differ (%.3f%%)\n", chunks, total, differ, 100*float64(differ)/float64(max(total, 1)))
	type kv struct {
		k [2]string
		n int
	}
	var list []kv
	for k, n := range pairs {
		list = append(list, kv{k, n})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].n > list[j].n })
	for i, e := range list {
		if i >= *top {
			break
		}
		fmt.Printf("%9d  vanilla %-50s mc-go %s\n", e.n, e.k[0], e.k[1])
	}
}

// decodeStates unpacks a section's block_states into canonical state
// strings, index y<<8 | z<<4 | x.
func decodeStates(m map[string]any) []string {
	palette := m["palette"].(nbtpkg.List).Items
	names := make([]string, len(palette))
	for i, p := range palette {
		names[i] = canonical(p.(map[string]any))
	}
	out := make([]string, 4096)
	if len(palette) == 1 {
		for i := range out {
			out[i] = names[0]
		}
		return out
	}
	data := m["data"].([]int64)
	b := max(4, bits.Len(uint(len(palette)-1)))
	per := 64 / b
	mask := uint64(1)<<b - 1
	for i := range out {
		v := uint64(data[i/per]) >> (uint(i%per) * uint(b)) & mask
		out[i] = names[v]
	}
	return out
}

func canonical(p map[string]any) string {
	name := p["Name"].(string)
	props, _ := p["Properties"].(map[string]any)
	if len(props) == 0 {
		return name
	}
	var kv []string
	for k, v := range props {
		kv = append(kv, k+"="+fmt.Sprint(v))
	}
	sort.Strings(kv)
	return name + "[" + strings.Join(kv, ",") + "]"
}

func toInt(v any) int {
	switch n := v.(type) {
	case int8:
		return int(n)
	case int16:
		return int(n)
	case int32:
		return int(n)
	case int64:
		return int(n)
	case byte:
		return int(int8(n))
	}
	fatal(fmt.Errorf("not an integer: %T", v))
	return 0
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
