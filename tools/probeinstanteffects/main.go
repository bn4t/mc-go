// Normalize the public built-in instantaneous-effect metadata probe.
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
	input := flag.String("in", "", "probe stdout log")
	output := flag.String("out", "internal/server/testdata/vanilla-26.2/effect_instantaneous.tsv", "normalized facts")
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
	instant := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		i := strings.Index(line, "FACT\t")
		if i < 0 {
			continue
		}
		line = line[i+5:]
		fields := strings.Split(line, "\t")
		if len(fields) != 4 || !strings.HasPrefix(fields[0], "minecraft:") {
			return fmt.Errorf("invalid metadata row")
		}
		for _, v := range fields[1:] {
			if _, err := strconv.ParseBool(v); err != nil {
				return err
			}
		}
		if _, duplicate := rows[fields[0]]; duplicate {
			return fmt.Errorf("duplicate effect %s", fields[0])
		}
		rows[fields[0]] = line
		if fields[1] == "true" {
			instant++
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if len(rows) != 40 || instant != 3 {
		return fmt.Errorf("rows%d instant%d, want40/3", len(rows), instant)
	}
	keys := make([]string, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out strings.Builder
	out.WriteString("# Official Minecraft26.2 public isInstantaneous and tick predicates at duration0/1, amplifier0.\n")
	for _, k := range keys {
		out.WriteString(rows[k])
		out.WriteByte('\n')
	}
	return os.WriteFile(output, []byte(out.String()), 0644)
}
