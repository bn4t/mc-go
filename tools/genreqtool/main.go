// Command genreqtool extracts the set of blocks registered with
// requiresCorrectToolForDrops() from the vanilla server jar's
// net/minecraft/world/level/block/Blocks <clinit> bytecode and emits
// internal/server/blockstool_gen.go.
//
// Usage: go run ./tools/genreqtool <path-to-real-server-jar> <blocks.json>
// where blocks.json is vanilla/generated/reports/blocks.json (to filter
// collection members against the real block id set).
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

func javap(jarDir, class string) []string {
	out, err := exec.Command("javap", "-c", "-p", "-classpath", jarDir, class).Output()
	if err != nil {
		panic(err)
	}
	return strings.Split(string(out), "\n")
}

func javapV(jarDir, class string) []string {
	out, err := exec.Command("javap", "-v", "-p", "-classpath", jarDir, class).Output()
	if err != nil {
		panic(err)
	}
	return strings.Split(string(out), "\n")
}

var (
	methodDecl = regexp.MustCompile(`^  (?:private|public|protected|static|final|synchronized)[^=]*?\b([\w$]+)\(`)
	cpDyn      = regexp.MustCompile(`^\s*#(\d+) = InvokeDynamic\s+#(\d+):#`)
	bsmHead    = regexp.MustCompile(`^\s*(\d+): #\d+ `)
	bsmImpl    = regexp.MustCompile(`REF_\w+ net/minecraft/world/level/block/Blocks\.([\w$]+):`)
	instr      = regexp.MustCompile(`^\s+\d+: `)
	idSingle   = regexp.MustCompile(`Field net/minecraft/references/BlockItemIds\.(\w+):Lnet/minecraft/references/BlockItemId;`)
	idColl     = regexp.MustCompile(`Field net/minecraft/references/BlockItemIds\.(\w+):Lnet/minecraft/world/level/block/(\w+);`)
	idKey      = regexp.MustCompile(`Field net/minecraft/references/BlockIds\.(\w+):`)
	blockField = regexp.MustCompile(`// Field (\w+):Lnet/minecraft/world/level/block/Block;`)
	dynIdx     = regexp.MustCompile(`invokedynamic\s+#(\d+)`)
)

var colors = []string{"white", "orange", "magenta", "light_blue", "yellow", "lime", "pink", "gray", "light_gray", "cyan", "purple", "blue", "brown", "green", "red", "black"}

var colorBase = map[string]string{
	"BED": "bed", "WOOL": "wool", "STAINED_GLASS": "stained_glass",
	"DYED_TERRACOTTA": "terracotta", "STAINED_GLASS_PANE": "stained_glass_pane",
	"CARPET": "carpet", "BANNER": "banner", "WALL_BANNER": "wall_banner",
	"DYED_SHULKER_BOX": "shulker_box", "GLAZED_TERRACOTTA": "glazed_terracotta",
	"CONCRETE": "concrete", "CONCRETE_POWDER": "concrete_powder",
	"DYED_CANDLE": "candle", "DYED_CANDLE_CAKE": "candle_cake",
}

// expandColl turns a BlockItemIds collection constant into its member block ids.
func expandColl(field string) []string {
	if base, ok := colorBase[field]; ok {
		out := make([]string, 0, len(colors))
		for _, c := range colors {
			out = append(out, c+"_"+base)
		}
		return out
	}
	b := strings.ToLower(field)
	out := []string{b, "exposed_" + b, "weathered_" + b, "oxidized_" + b,
		"waxed_" + b, "waxed_exposed_" + b, "waxed_weathered_" + b, "waxed_oxidized_" + b}
	if b == "copper_block" {
		out = append(out, "exposed_copper", "weathered_copper", "oxidized_copper",
			"waxed_exposed_copper", "waxed_weathered_copper", "waxed_oxidized_copper")
	}
	return out
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: genreqtool <jar-classes-dir> <blocks.json>")
		os.Exit(2)
	}
	jarDir, blocksJSON := os.Args[1], os.Args[2]

	clines := javap(jarDir, "net.minecraft.world.level.block.Blocks")
	vlines := javapV(jarDir, "net.minecraft.world.level.block.Blocks")

	// 1. Methods whose body calls Properties.requiresCorrectToolForDrops().
	reqMethods := map[string]bool{}
	cur := ""
	for _, l := range clines {
		if strings.Contains(l, "static {}") {
			cur = "CLINIT"
			continue
		}
		if m := methodDecl.FindStringSubmatch(l); m != nil && !strings.Contains(l, "Code:") {
			cur = m[1]
		}
		if strings.Contains(l, "requiresCorrectToolForDrops") && cur != "" && cur != "CLINIT" {
			reqMethods[cur] = true
		}
	}

	// 2. Constant pool: invokedynamic index -> bootstrap ordinal.
	cp2bsm := map[int]int{}
	inCP := false
	for _, l := range vlines {
		if strings.HasPrefix(l, "Constant pool:") {
			inCP = true
			continue
		}
		if inCP && strings.TrimSpace(l) == "" {
			inCP = false
		}
		if !inCP {
			continue
		}
		if m := cpDyn.FindStringSubmatch(l); m != nil {
			var cp, bsm int
			fmt.Sscanf(m[1], "%d", &cp)
			fmt.Sscanf(m[2], "%d", &bsm)
			cp2bsm[cp] = bsm
		}
	}

	// 3. BootstrapMethods: ordinal -> Blocks.<impl> method name.
	bsm2impl := map[int]string{}
	seen := false
	curOrd := -1
	for _, l := range vlines {
		if !seen {
			if strings.HasPrefix(l, "BootstrapMethods:") {
				seen = true
			}
			continue
		}
		if strings.TrimSpace(l) != "" && l[0] != ' ' && l[0] != '\t' {
			break
		}
		if m := bsmHead.FindStringSubmatch(l); m != nil {
			fmt.Sscanf(m[1], "%d", &curOrd)
		}
		if m := bsmImpl.FindStringSubmatch(l); m != nil && curOrd >= 0 {
			bsm2impl[curOrd] = m[1]
		}
	}
	reqBsm := map[int]bool{}
	for ord, name := range bsm2impl {
		if reqMethods[name] {
			reqBsm[ord] = true
		}
	}

	// 4. Walk <clinit>: BlockItemIds field opens a segment; a
	//    requiresCorrectToolForDrops call or invokedynamic to a req-lambda sets
	//    the flag; a register* invokestatic closes it; getstatic Blocks.<BASE>
	//    records flag inheritance.
	type seg struct {
		name  string
		req   bool
		deps  []string
		colls bool
	}
	var segs []seg
	var name string
	var coll bool
	var req bool
	var deps []string
	reset := func() { name, coll, req, deps = "", false, false, nil }
	start := -1
	for i, l := range clines {
		if strings.Contains(l, "static {}") {
			start = i
			break
		}
	}
	for _, l := range clines[start:] {
		if !instr.MatchString(l) {
			continue
		}
		if strings.Contains(l, "getstatic") {
			if m := idSingle.FindStringSubmatch(l); m != nil {
				name, coll, req, deps = m[1], false, false, nil
			} else if m := idColl.FindStringSubmatch(l); m != nil {
				name, coll, req, deps = m[1], true, false, nil
			} else if m := idKey.FindStringSubmatch(l); m != nil {
				name, coll, req, deps = m[1], false, false, nil
			} else if m := blockField.FindStringSubmatch(l); m != nil &&
				!strings.Contains(l, "Field net/") {
				deps = append(deps, m[1])
			}
		}
		if strings.Contains(l, "requiresCorrectToolForDrops") {
			req = true
		}
		if strings.Contains(l, "invokedynamic") {
			if m := dynIdx.FindStringSubmatch(l); m != nil {
				var cp int
				fmt.Sscanf(m[1], "%d", &cp)
				if reqBsm[cp2bsm[cp]] {
					req = true
				}
			}
		}
		if strings.Contains(l, "invokestatic") &&
			strings.Contains(strings.ToLower(l), "register") {
			if name != "" {
				segs = append(segs, seg{name, req, deps, coll})
			}
			reset()
		}
	}

	// 5. Load real block ids, expand, propagate deps to a fixpoint.
	raw, err := os.ReadFile(blocksJSON)
	if err != nil {
		panic(err)
	}
	var rep map[string]any
	if err := json.Unmarshal(raw, &rep); err != nil {
		panic(err)
	}
	bset := map[string]bool{}
	for k := range rep {
		if i := strings.Index(k, ":"); i >= 0 {
			bset[k[i+1:]] = true
		}
	}
	flag := map[string]bool{}
	depMap := map[string][]string{}
	for _, s := range segs {
		var ids []string
		if s.colls {
			ids = expandColl(s.name)
		} else {
			ids = []string{strings.ToLower(s.name)}
		}
		for _, id := range ids {
			if s.req {
				flag[id] = true
			}
			for _, d := range s.deps {
				depMap[id] = append(depMap[id], strings.ToLower(d))
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for id, ds := range depMap {
			for _, d := range ds {
				if flag[d] && !flag[id] {
					flag[id] = true
					changed = true
				}
			}
		}
	}
	var out []string
	for id := range flag {
		if flag[id] && bset[id] {
			out = append(out, id)
		}
	}
	sort.Strings(out)

	w, _ := os.Create("internal/server/blockstool_gen.go")
	defer w.Close()
	bw := bufio.NewWriter(w)
	fmt.Fprintln(bw, "// Code generated by tools/genreqtool from Blocks.<clinit>; DO NOT EDIT.")
	fmt.Fprintln(bw)
	fmt.Fprintln(bw, "package server")
	fmt.Fprintln(bw)
	fmt.Fprintln(bw, "// blockRequiresTool mirrors vanilla BlockBehaviour.Properties.requiresCorrectToolForDrops().")
	fmt.Fprintln(bw, "var blockRequiresTool = map[string]bool{")
	for _, b := range out {
		fmt.Fprintf(bw, "\t%q: true,\n", b)
	}
	fmt.Fprintln(bw, "}")
	bw.Flush()
	fmt.Fprintf(os.Stderr, "%d blocks with requiresCorrectToolForDrops\n", len(out))
}
