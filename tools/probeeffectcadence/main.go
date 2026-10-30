// Extract the probe's public-API truth rows from its game-logger output.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

func main() {
	input := flag.String("in", "", "Java probe stdout log")
	output := flag.String("out", "internal/server/testdata/vanilla-26.2/effect_cadence.tsv", "normalized facts")
	flag.Parse()
	if err := extract(*input, *output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func extract(input, output string) error {
	f, err := os.Open(input)
	if err != nil {
		return err
	}
	defer f.Close()
	rows := map[string]string{}
	counts := map[string]int{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		i := strings.Index(line, "FACT\t")
		if i < 0 {
			continue
		}
		line = line[i+5:]
		fields := strings.Split(line, "\t")
		if len(fields) != 3 {
			return fmt.Errorf("invalid fact fields")
		}
		switch fields[0] {
		case "minecraft:regeneration", "minecraft:poison", "minecraft:wither":
		default:
			return fmt.Errorf("unexpected effect %q", fields[0])
		}
		if _, err := strconv.ParseInt(fields[1], 10, 32); err != nil {
			return err
		}
		if len(fields[2]) != 201 || strings.Trim(fields[2], "01") != "" {
			return fmt.Errorf("invalid cadence mask")
		}
		key := fields[0] + "\t" + fields[1]
		if _, exists := rows[key]; exists {
			return fmt.Errorf("duplicate %s", key)
		}
		rows[key] = line
		counts[fields[0]]++
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if len(rows) != 789 {
		return fmt.Errorf("got %d rows, want789", len(rows))
	}
	for effect, n := range counts {
		if n != 263 {
			return fmt.Errorf("%s rows%d want263", effect, n)
		}
	}
	keys := make([]string, 0, len(rows))
	for key := range rows {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var out strings.Builder
	out.WriteString("# Official Minecraft26.2 public MobEffect.shouldApplyEffectTickThisTick; duration columns0..200.\n")
	for _, key := range keys {
		out.WriteString(rows[key])
		out.WriteByte('\n')
	}
	return os.WriteFile(output, []byte(out.String()), 0644)
}
