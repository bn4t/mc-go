// Command nbtdump prints an NBT file (gzipped or raw, named or not) as
// indented text. It exists so save-format work can read what the real
// server wrote without a second implementation of the format; the
// blackbox suite uses the same internal/nbt reader the server does.
//
//	go run ./tools/nbtdump world/level.dat
package main

import (
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"mc-go/internal/nbt"
)

// arrays prints the contents of short numeric arrays, which carry the
// spawn position and the section palettes.
var arrays *bool

func main() {
	root := flag.Bool("root", false, "print the whole root compound instead of the Data payload")
	depth := flag.Int("depth", 12, "maximum depth to print")
	arrays = flag.Bool("arrays", false, "print the contents of numeric arrays of eight entries or fewer")
	flag.Parse()
	for _, path := range flag.Args() {
		if err := dumpFile(path, *root, *depth); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			os.Exit(1)
		}
	}
}

func dumpFile(path string, root bool, depth int) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var r io.Reader = f
	head := make([]byte, 2)
	if _, err := io.ReadFull(f, head); err != nil {
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if head[0] == 0x1f && head[1] == 0x8b {
		zr, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer zr.Close()
		r = zr
	}
	_, v, err := nbt.ReadNamed(r)
	if err != nil {
		return err
	}
	fmt.Printf("== %s\n", path)
	if !root {
		if m, ok := v.(map[string]any); ok {
			if data, ok := m["Data"]; ok {
				v = data
			}
		}
	}
	printValue(v, 0, depth)
	return nil
}

func printValue(v any, indent, depth int) {
	pad := strings.Repeat("  ", indent)
	if indent > depth {
		fmt.Printf("%s...\n", pad)
		return
	}
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			switch child := t[k].(type) {
			case map[string]any, nbt.List, []int64, []int32, []byte:
				fmt.Printf("%s%s:\n", pad, k)
				printValue(child, indent+1, depth)
			default:
				fmt.Printf("%s%s: %v\n", pad, k, child)
			}
		}
	case nbt.List:
		fmt.Printf("%s(list of %d)\n", pad, len(t.Items))
		for i, it := range t.Items {
			if i > 24 {
				fmt.Printf("%s... %d more\n", pad, len(t.Items)-i)
				break
			}
			printValue(it, indent+1, depth)
		}
	case []int64:
		if *arrays && len(t) <= 8 {
			fmt.Printf("%s(int64 array, %d entries) %v\n", pad, len(t), t)
			return
		}
		fmt.Printf("%s(int64 array, %d entries)\n", pad, len(t))
	case []int32:
		if *arrays && len(t) <= 8 {
			fmt.Printf("%s(int32 array, %d entries) %v\n", pad, len(t), t)
			return
		}
		fmt.Printf("%s(int32 array, %d entries)\n", pad, len(t))
	case []byte:
		if *arrays && len(t) <= 8 {
			fmt.Printf("%s(byte array, %d entries) %v\n", pad, len(t), t)
			return
		}
		fmt.Printf("%s(byte array, %d entries)\n", pad, len(t))
	default:
		fmt.Printf("%s%v\n", pad, v)
	}
}
