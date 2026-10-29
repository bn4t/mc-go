//go:build blackbox

package blackbox

import (
	"archive/zip"
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/world"
	"mc-go/internal/worldgen"
)

// nethOrderAgent is a java agent logging, per dimension, the order in
// which the real server enters ChunkGenerator.applyBiomeDecoration. It
// adds one log call at the method's start (level and chunk arguments)
// through the JDK's java.lang.classfile API and changes nothing else.
// Run with one worldgen thread the log is the exact decoration order.
const nethOrderAgent = `
import java.lang.classfile.*;
import java.lang.constant.*;
import java.lang.instrument.*;
import java.security.ProtectionDomain;

public class NethOrderAgent {
  public static void premain(String args, Instrumentation inst) throws Exception {
    String[] a = args.split(";", 2);
    System.setProperty("nethorder.log", a[1]);
    inst.appendToBootstrapClassLoaderSearch(new java.util.jar.JarFile(a[0]));
    ClassDesc hook = ClassDesc.of("NethOrderHook");
    MethodTypeDesc sig = MethodTypeDesc.of(ConstantDescs.CD_void, ConstantDescs.CD_Object, ConstantDescs.CD_Object);
    inst.addTransformer(new ClassFileTransformer() {
      public byte[] transform(ClassLoader l, String name, Class<?> c, ProtectionDomain pd, byte[] b) {
        if (!"net/minecraft/world/level/chunk/ChunkGenerator".equals(name)) return null;
        ClassFile cf = ClassFile.of();
        CodeTransform ct = new CodeTransform() {
          public void atStart(CodeBuilder cb) { cb.aload(1); cb.aload(2); cb.invokestatic(hook, "enter", sig); }
          public void accept(CodeBuilder cb, CodeElement e) { cb.with(e); }
        };
        return cf.transformClass(cf.parse(b), ClassTransform.transformingMethodBodies(
            m -> m.methodName().equalsString("applyBiomeDecoration") && m.methodTypeSymbol().parameterCount() == 3, ct));
      }
    });
  }
}
`

const nethOrderHook = `
import java.io.*;

public class NethOrderHook {
  static PrintWriter out;
  public static synchronized void enter(Object level, Object chunk) {
    try {
      if (out == null) out = new PrintWriter(new FileWriter(System.getProperty("nethorder.log"), true));
      Object sl = level.getClass().getMethod("getLevel").invoke(level);
      Object dim = sl.getClass().getMethod("dimension").invoke(sl);
      Object pos = chunk.getClass().getMethod("getPos").invoke(chunk);
      int x = (int) pos.getClass().getMethod("x").invoke(pos);
      int z = (int) pos.getClass().getMethod("z").invoke(pos);
      out.println("enter " + x + " " + z + " " + dim);
      out.flush();
    } catch (Throwable t) { if (out != null) { out.println("err " + t); out.flush(); } }
  }
}
`

// nethBuildAgent compiles the agent, returning the agent jar and the
// hook jar (the hook joins the bootstrap path on its own).
func nethBuildAgent(t *testing.T) (string, string) {
	t.Helper()
	javac := "javac"
	if j, err := exec.LookPath("java"); err == nil {
		javac = filepath.Join(filepath.Dir(j), "javac")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "NethOrderAgent.java"), []byte(nethOrderAgent), 0o644)
	os.WriteFile(filepath.Join(dir, "NethOrderHook.java"), []byte(nethOrderHook), 0o644)
	cmd := exec.Command(javac, "-d", dir, filepath.Join(dir, "NethOrderAgent.java"), filepath.Join(dir, "NethOrderHook.java"))
	cmd.Env = append(os.Environ(), "JAVA_TOOL_OPTIONS=")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("javac agent: %v\n%s", err, out)
	}
	mk := func(name, manifest string, keep func(string) bool) string {
		p := filepath.Join(dir, name)
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		zw := zip.NewWriter(f)
		w, _ := zw.Create("META-INF/MANIFEST.MF")
		fmt.Fprint(w, "Manifest-Version: 1.0\r\n"+manifest+"\r\n")
		ents, _ := os.ReadDir(dir)
		for _, e := range ents {
			if strings.HasSuffix(e.Name(), ".class") && keep(e.Name()) {
				b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
				w, _ := zw.Create(e.Name())
				w.Write(b)
			}
		}
		zw.Close()
		f.Close()
		return p
	}
	hook := func(n string) bool { return strings.HasPrefix(n, "NethOrderHook") }
	return mk("nethorder.jar", "Premain-Class: NethOrderAgent\r\n", func(n string) bool { return !hook(n) }),
		mk("nethhook.jar", "", hook)
}

// nethReadOrder returns the logged decoration order of one dimension.
func nethReadOrder(t *testing.T, path, dim string) [][2]int32 {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("decoration order log: %v", err)
	}
	defer f.Close()
	var out [][2]int32
	seen := map[[2]int32]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "err ") {
			t.Fatalf("order hook: %s", line)
		}
		var x, z int32
		if n, _ := fmt.Sscanf(line, "enter %d %d", &x, &z); n != 2 || !strings.Contains(line, "minecraft:"+dim+"]") {
			continue
		}
		k := [2]int32{x, z}
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

// nethOracle has the real server generate the areas of one dimension
// with the stage data pack, one worldgen thread and the order agent,
// and returns the region directory and the decoration order log.
// MCGO_NETH_CACHE keeps both per test name for re-running mc-go alone.
func nethOracle(t *testing.T, seed int64, files map[string]string, dim string, areas []terrainArea) (string, string) {
	t.Helper()
	run := func(dir string) (string, string) {
		jar := blackboxJar(t)
		port, ok := freePort()
		if !ok {
			t.Skip("loopback binds blocked in this environment")
		}
		props := map[string]string{
			"level-name": "world", "level-type": "minecraft:normal", "generator-settings": "{}",
			"level-seed": fmt.Sprint(seed), "generate-structures": "false",
			"view-distance": "3", "simulation-distance": "3", "max-tick-time": "-1",
		}
		agent, hook := nethBuildAgent(t)
		logPath := filepath.Join(dir, "order.log")
		opts := strings.TrimSpace(os.Getenv("JAVA_TOOL_OPTIONS"))
		if !strings.Contains(opts, "-Xmx") {
			opts += " -Xmx1536m"
		}
		t.Setenv("JAVA_TOOL_OPTIONS", strings.TrimSpace(opts+" -Dmax.bg.threads=1 -javaagent:"+agent+"="+hook+";"+logPath))
		_, say, wait := vanillaConsole(t, jar, port, props, files, dir)
		vlog := filepath.Join(dir, "vanilla.log")
		logHas := func(s string) bool {
			b, _ := os.ReadFile(vlog)
			return strings.Contains(string(b), s)
		}
		say("tick freeze")
		in := "execute in minecraft:" + dim + " run "
		for _, a := range areas {
			say(fmt.Sprintf(in+"forceload add %d %d %d %d", a.x0*16, a.z0*16, a.x1*16+15, a.z1*16+15))
		}
		for i, a := range areas {
			for cx := a.x0; cx <= a.x1; cx++ {
				for cz := a.z0; cz <= a.z1; cz++ {
					tag := fmt.Sprintf("loaded-%d-%d-%d", i, cx, cz)
					deadline := time.Now().Add(300 * time.Second)
					for !logHas(tag) {
						if time.Now().After(deadline) {
							t.Fatalf("chunk %d,%d never loaded (log %s)", cx, cz, vlog)
						}
						say(fmt.Sprintf("execute in minecraft:%s if loaded %d 0 %d run say %s", dim, cx*16, cz*16, tag))
						time.Sleep(200 * time.Millisecond)
					}
				}
			}
		}
		say("save-all flush")
		say("stop")
		if code := wait(); code != 0 {
			t.Fatalf("vanilla exited %d (log %s)", code, vlog)
		}
		return filepath.Join(dir, "world", "dimensions", "minecraft", dim, "region"), logPath
	}
	cache := os.Getenv("MCGO_NETH_CACHE")
	if cache == "" {
		return run(t.TempDir())
	}
	dir := filepath.Join(cache, strings.ReplaceAll(t.Name(), "/", "_"))
	if _, err := os.Stat(filepath.Join(dir, "done")); err == nil {
		return filepath.Join(dir, "region"), filepath.Join(dir, "order.log")
	}
	os.RemoveAll(dir)
	tmp := dir + ".run"
	os.RemoveAll(tmp)
	os.MkdirAll(tmp, 0o755)
	region, lp := run(tmp)
	if err := os.CopyFS(filepath.Join(dir, "region"), os.DirFS(region)); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(lp)
	os.WriteFile(filepath.Join(dir, "order.log"), b, 0o644)
	os.WriteFile(filepath.Join(dir, "done"), nil, 0o644)
	return region, lp
}

// nethCase is one dimension parity run.
type nethCase struct {
	seed               int64
	dim                string // vanilla dimension id
	settings, biomes   string // mc-go generator settings and biome list
	areas              []terrainArea
	extra              map[string]string // worldgen overrides on both sides
	allowed            []string          // feature types a kept placed feature may reach
	require            map[string]int    // block -> minimum feature writes in the compared chunks
	exempt             func(c [2]int32) bool
	checkBlockEntities []string // block names whose block entity must exist in the saved chunk
}

// nethParity has the real server decorate the forced areas of one
// dimension with only the placed features whose reachable feature
// types are all allowed (as vegParity keys them), logging the exact
// decoration order (one worldgen thread, nethOrderAgent), replays that
// order in mc-go for every chunk within three rings of an area and
// compares the forced areas block for block. A cell several chunks
// wrote still passes when vanilla's block is one of those writes (a
// safety net; with the replayed order there should be none), and
// flowing fluid where mc-go has air is post-processing's fluid tick.
func nethParity(t *testing.T, c nethCase) {
	t.Helper()
	ok := map[string]bool{}
	for _, a := range c.allowed {
		ok[a] = true
	}
	extraRaw := map[string]json.RawMessage{}
	for k, v := range c.extra {
		extraRaw[k] = json.RawMessage(v)
	}
	base := worldgen.Overlay(worldgen.Vanilla(), extraRaw)
	kept := map[string]bool{}
	keep := func(p string) bool {
		ts := vegFeatureTypes(base, p)
		if len(ts) == 0 {
			return false
		}
		for ty := range ts {
			if !ok[ty] {
				return false
			}
		}
		kept[p] = true
		return true
	}
	ov := worldgen.StageOverrides(base, keep, true, true)
	for k, v := range extraRaw {
		// Biome overrides go through the filter like the vanilla ones.
		if !strings.HasPrefix(k, "biome/") {
			ov[k] = v
		}
	}
	var names []string
	for k := range kept {
		names = append(names, k)
	}
	sort.Strings(names)
	t.Logf("kept placed features: %v", names)
	region, orderLog := nethOracle(t, c.seed, stageDatapack(ov), c.dim, c.areas)

	g, err := worldgen.NewGenerator(worldgen.Overlay(worldgen.Vanilla(), ov), c.settings, c.biomes, c.seed)
	if err != nil {
		t.Fatal(err)
	}
	if c.dim != "overworld" {
		g.RS.SetLevelBounds(0, 256)
	}
	minY, height := g.RS.LevelMinY(), g.RS.LevelHeight()
	near := func(k [2]int32) bool {
		for _, a := range c.areas {
			if k[0] >= a.x0-3 && k[0] <= a.x1+3 && k[1] >= a.z0-3 && k[1] <= a.z1+3 {
				return true
			}
		}
		return false
	}
	var order [][2]int32
	logged := nethReadOrder(t, orderLog, c.dim)
	for _, k := range logged {
		if near(k) {
			order = append(order, k)
		}
	}
	t.Logf("decoration order: %d %s chunks logged, %d replayed", len(logged), c.dim, len(order))

	writes := map[worldgen.BlockPos]map[[2]int32]world.Block{}
	inArea := func(k [2]int32) bool {
		for _, a := range c.areas {
			if k[0] >= a.x0 && k[0] <= a.x1 && k[1] >= a.z0 && k[1] <= a.z1 {
				return true
			}
		}
		return false
	}
	counts := map[string]int{}
	g.WriteLog = func(p worldgen.BlockPos, ch [2]int32, b world.Block) {
		m := writes[p]
		if m == nil {
			m = map[[2]int32]world.Block{}
			writes[p] = m
		}
		m[ch] = b
		if inArea([2]int32{p[0] >> 4, p[2] >> 4}) {
			n, _ := world.StateOwner(b)
			counts[n]++
		}
	}
	chunks := map[[2]int32]*worldgen.ProtoChunk{}
	for _, k := range order {
		for dz := int32(-1); dz <= 1; dz++ {
			for dx := int32(-1); dx <= 1; dx++ {
				q := [2]int32{k[0] + dx, k[1] + dz}
				if chunks[q] == nil {
					chunks[q] = g.Terrain(q[0], q[1])
				}
			}
		}
	}
	for _, k := range order {
		g.Decorate(g.NewRegion(chunks[k], chunks))
	}

	total, orderDep, fluid, badChunks, entities, lightSched, shapePost := 0, 0, 0, 0, 0, 0, 0
	for ai, a := range c.areas {
		for cx := a.x0; cx <= a.x1; cx++ {
			for cz := a.z0; cz <= a.z1; cz++ {
				k := [2]int32{cx, cz}
				if c.exempt != nil && c.exempt(k) {
					continue
				}
				saved := readSavedChunk(t, region, cx, cz, minY, height)
				p := chunks[k]
				if p == nil {
					t.Fatalf("chunk %d,%d was not decorated by the oracle", cx, cz)
				}
				bad := 0
				for i, w := range saved.blocks {
					gs := protoStateString(p.Blocks[i])
					pos := worldgen.BlockPos{cx*16 + int32(i&15), int32(i>>8) + minY, cz*16 + int32((i>>4)&15)}
					if gs == w {
						continue
					}
					if m := writes[pos]; len(m) > 1 {
						hit := false
						for _, b := range m {
							hit = hit || protoStateString(b) == w
						}
						if hit {
							orderDep++
							continue
						}
					}
					if (gs == "air" || writes[pos] != nil) && strings.Contains(w, "[level=") {
						fluid++
						continue
					}
					// MushroomBlock.canSurvive needs raw brightness < 13. A
					// chunk's light turns on (INITIALIZE_LIGHT, no neighbour
					// requirement) right after its own decoration, so later
					// neighbour decorations write into lit-enabled sections
					// whose block light the async light engine may or may not
					// have propagated by the time a mushroom checks it. mc-go
					// models unlit chunks (0); a mushroom vanilla refused within
					// reach of an emitter (emission - distance >= 13) is that
					// light schedule.
					if strings.HasSuffix(gs, "_mushroom") && w == "air" && nethEmittedLight(chunks, pos, minY) >= 13 {
						lightSched++
						continue
					}
					// LevelChunk.postProcessGeneration also runs
					// updateFromNeighbourShapes on every marked cell, which
					// mc-go does not model: a snow layer a later disk
					// (markAboveForPostProcessing) slid packed ice under
					// drops to air there.
					if strings.HasPrefix(gs, "snow[") && w == "air" && slices.Contains(p.PostProcess, pos) &&
						saved.blocks[i-256] == "packed_ice" {
						shapePost++
						continue
					}
					if bad < 8 || os.Getenv("MCGO_NETH_VERBOSE") != "" {
						var wr []string
						for ch, b := range writes[pos] {
							wr = append(wr, fmt.Sprintf("%v=%s", ch, protoStateString(b)))
						}
						var below string
						if i >= 256 {
							below = fmt.Sprintf("; below mc-go %s vanilla %s (writes %v)", protoStateString(p.Blocks[i-256]), saved.blocks[i-256], writes[worldgen.BlockPos{pos[0], pos[1] - 1, pos[2]}])
						}
						if os.Getenv("MCGO_NETH_DIAG") != "" {
							below += fmt.Sprintf("; emitted light %d", nethEmittedLight(chunks, pos, minY))
						}
						t.Errorf("area %d (%d,%d,%d): mc-go %s, vanilla %s (writes %v%s)", ai, pos[0], pos[1], pos[2], gs, w, wr, below)
					}
					bad++
				}
				if bad > 0 {
					t.Errorf("chunk %d,%d: %d mismatched blocks", cx, cz, bad)
					badChunks++
				}
				if len(c.checkBlockEntities) > 0 {
					entities += nethCheckBlockEntities(t, saved, p, c.checkBlockEntities, minY)
				}
				total++
			}
		}
	}
	if len(worldgen.UnsupportedFeatures) > 0 {
		t.Errorf("unsupported feature types placed nothing: %v", worldgen.UnsupportedFeatures)
	}
	if len(worldgen.UnsupportedSurvive) > 0 {
		t.Errorf("canSurvive not modeled for: %v", worldgen.UnsupportedSurvive)
	}
	var got []string
	for n, v := range counts {
		got = append(got, fmt.Sprintf("%s=%d", n, v))
	}
	sort.Strings(got)
	t.Logf("feature writes in the compared areas: %s", strings.Join(got, " "))
	for n, min := range c.require {
		if counts[n] < min {
			t.Errorf("coverage: %d %s writes in the compared areas, want >= %d", counts[n], n, min)
		}
	}
	t.Logf("%d chunks compared (%d mismatched), %d block entities checked; %d order-dependent cells, %d fluid post-processing cells, %d light-schedule mushrooms, %d shape post-processing cells accepted",
		total, badChunks, entities, orderDep, fluid, lightSched, shapePost)
}

// nethEmittedLight is the brightest block light any emitter within 15
// cells could put on pos with no obstruction (emission minus Manhattan
// distance), over mc-go's decorated blocks.
func nethEmittedLight(chunks map[[2]int32]*worldgen.ProtoChunk, pos worldgen.BlockPos, minY int32) int {
	best := 0
	for dx := int32(-14); dx <= 14; dx++ {
		for dy := int32(-14); dy <= 14; dy++ {
			for dz := int32(-14); dz <= 14; dz++ {
				d := int(absInt(dx) + absInt(dy) + absInt(dz))
				if d > 14 {
					continue
				}
				x, y, z := pos[0]+dx, pos[1]+dy, pos[2]+dz
				p := chunks[[2]int32{x >> 4, z >> 4}]
				if p == nil || y < minY || y >= minY+p.Height {
					continue
				}
				if l := worldgen.LightEmission(p.Get(x&15, y, z&15)) - d; l > best {
					best = l
				}
			}
		}
	}
	return best
}

// nethCheckBlockEntities checks that every block of the named kinds in
// mc-go's chunk has a block entity of the same id in the saved chunk.
func nethCheckBlockEntities(t *testing.T, saved savedChunk, p *worldgen.ProtoChunk, kinds []string, minY int32) int {
	t.Helper()
	have := map[[3]int32]string{}
	if l, ok := saved.raw["block_entities"].(nbt.List); ok {
		for _, e := range l.Items {
			m := e.(map[string]any)
			id, _ := m["id"].(string)
			have[[3]int32{m["x"].(int32), m["y"].(int32), m["z"].(int32)}] = strings.TrimPrefix(id, "minecraft:")
		}
	}
	want := map[string]bool{}
	for _, k := range kinds {
		want[k] = true
	}
	n := 0
	for i, b := range p.Blocks {
		name, _ := world.StateOwner(b)
		if !want[name] {
			continue
		}
		pos := [3]int32{p.CX*16 + int32(i&15), int32(i>>8) + minY, p.CZ*16 + int32((i>>4)&15)}
		if have[pos] != name {
			t.Errorf("%s at %v: saved block entity %q", name, pos, have[pos])
		}
		n++
	}
	return n
}

// nethAreas (seed 12345, 4x4 chunks each): soul sand valley, basalt
// deltas, crimson forest, nether wastes, warped forest.
var nethAreas = []terrainArea{{-104, -120, -101, -117}, {-92, -120, -89, -117}, {-64, -120, -61, -117},
	{-20, -120, -17, -117}, {-44, -108, -41, -105}}

// nethFeatureTypes are the types the nether biomes' decoration reaches.
var nethFeatureTypes = []string{"glowstone_blob", "basalt_columns", "basalt_pillar", "delta_feature", "huge_fungus",
	"nether_forest_vegetation", "netherrack_replace_blobs", "twisting_vines", "weeping_vines",
	"ore", "scattered_ore", "spring_feature", "simple_block", "random_selector", "simple_random_selector",
	"random_boolean_selector", "sequence", "weighted_random_selector"}

// TestFeatureNetherParity pins the whole nether decoration — glowstone
// blobs, fire patches, mushrooms, springs and ores everywhere; crimson
// and warped forests (huge fungi with shroomlight and hanging vines,
// ground cover, weeping and twisting vines); soul sand valley basalt
// pillars and roots; basalt delta lava deltas with magma rims, basalt
// columns and basalt/blackstone blobs — in nether wastes, crimson
// forest, warped forest, soul sand valley and basalt deltas, with
// vanilla's exact decoration order replayed.
func TestFeatureNetherParity(t *testing.T) {
	nethParity(t, nethCase{
		seed: 12345, dim: "the_nether", settings: "nether", biomes: "nether",
		areas:   nethAreas,
		allowed: nethFeatureTypes,
		require: map[string]int{"glowstone": 50, "fire": 10, "crimson_stem": 20, "warped_stem": 20, "nether_wart_block": 50,
			"warped_wart_block": 50, "shroomlight": 2, "weeping_vines": 5, "twisting_vines": 5, "crimson_roots": 20,
			"warped_roots": 20, "nether_sprouts": 20, "basalt": 200, "blackstone": 50, "magma_block": 20, "lava": 20},
	})
}
