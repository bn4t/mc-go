// Command genshapes measures per-state block shapes the ray clip reads
// — the outline shape (BlockStateBase.getShape with an empty collision
// context, the block's random offset taken back out), the interaction
// shape, canBeReplaced(Fluid) for water and lava, and each block's
// offset kind and maximum offsets (read back from getOffset at the
// origin, whose seed is 0: x = clamp(-0.25, ±maxH), y = -maxV; a
// horizontal maximum at or past 0.25 never clamps, so 0.25 stands for
// it) — by running the 26.2 server jar as
// an oracle: a small Java harness bootstraps the registries and asks
// every block state through its public accessors, with an empty block
// getter at the origin.
//
// Source class: jar oracle run (public BlockStateBase / Block / VoxelShape
// accessors). The harness copies no game code; the output is one row of
// facts per state in registry (protocol) order plus the distinct shapes
// as AABB lists.
//
// Usage: go run ./tools/genshapes [vanilla-dir]
// Needs java (JDK 25) and the unpacked vanilla/versions + libraries.
// Writes internal/server/shapes_gen.go.
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const probe = `import net.minecraft.SharedConstants;
import net.minecraft.server.Bootstrap;
import net.minecraft.world.level.block.Block;
import net.minecraft.world.level.block.BucketPickup;
import net.minecraft.world.level.block.LiquidBlockContainer;
import net.minecraft.world.level.LevelAccessor;
import net.minecraft.world.level.material.FluidState;
import net.minecraft.world.entity.LivingEntity;
import net.minecraft.world.level.block.state.BlockState;
import net.minecraft.world.level.EmptyBlockGetter;
import net.minecraft.world.level.material.Fluids;
import net.minecraft.world.phys.AABB;
import net.minecraft.world.phys.Vec3;
import net.minecraft.world.phys.shapes.CollisionContext;
import net.minecraft.world.phys.shapes.VoxelShape;
import net.minecraft.core.BlockPos;
import net.minecraft.core.registries.BuiltInRegistries;
public class Probe {
  static String boxes(VoxelShape v) {
    StringBuilder sb = new StringBuilder();
    for (AABB b : v.toAabbs()) {
      if (sb.length() > 0) sb.append(';');
      sb.append(b.minX).append(',').append(b.minY).append(',').append(b.minZ).append(',')
        .append(b.maxX).append(',').append(b.maxY).append(',').append(b.maxZ);
    }
    return sb.length() == 0 ? "-" : sb.toString();
  }
  public static void main(String[] a) {
    SharedConstants.tryDetectVersion();
    Bootstrap.bootStrap();
    EmptyBlockGetter g = EmptyBlockGetter.INSTANCE;
    BlockPos p = BlockPos.ZERO;
    StringBuilder sb = new StringBuilder();
    int id = 0;
    for (BlockState s : Block.BLOCK_STATE_REGISTRY) {
      VoxelShape outline = s.getShape(g, p, CollisionContext.empty());
      Vec3 off = s.getOffset(p);
      outline = outline.move(-off.x, -off.y, -off.z);
      int f = 0;
      if (s.canBeReplaced(Fluids.WATER)) f |= 1;
      if (s.canBeReplaced(Fluids.LAVA)) f |= 2;
      if (s.getBlock() instanceof LiquidBlockContainer c) {
        if (c.canPlaceLiquid(null, g, p, s, Fluids.WATER)) f |= 4;
        if (c.canPlaceLiquid(null, g, p, s, Fluids.LAVA)) f |= 8;
      }
      sb.append("ROW ").append(id).append(' ').append(f).append(' ')
        .append(boxes(outline)).append(' ').append(boxes(s.getInteractionShape(g, p))).append('\n');
      if (s == s.getBlock().defaultBlockState()) {
        Block b = s.getBlock();
        int kind = 0;
        if (s.hasOffsetFunction()) kind = off.y != 0 ? 2 : 1;
        String pick = "-", place = "-", sound = "-";
        try {
          if (b instanceof BucketPickup bp) {
            pick = b.getClass().getMethod("pickupBlock", LivingEntity.class, LevelAccessor.class, BlockPos.class, BlockState.class).getDeclaringClass().getSimpleName();
            sound = bp.getPickupSound().map(e -> BuiltInRegistries.SOUND_EVENT.getKey(e).getPath()).orElse("-");
          }
          if (b instanceof LiquidBlockContainer) {
            place = b.getClass().getMethod("placeLiquid", LevelAccessor.class, BlockPos.class, BlockState.class, FluidState.class).getDeclaringClass().getSimpleName();
          }
        } catch (NoSuchMethodException e) { throw new RuntimeException(e); }
        sb.append("BKT ").append(BuiltInRegistries.BLOCK.getKey(b).getPath()).append(' ').append(pick).append(' ').append(place).append(' ').append(sound).append('\n');
        sb.append("BLK ").append(BuiltInRegistries.BLOCK.getKey(b).getPath()).append(' ').append(kind)
          .append(' ').append((float) -off.x + 0.0f).append(' ').append((float) -off.y + 0.0f).append('\n');
      }
      id++;
    }
    System.out.print(sb);
  }
}
`

func main() {
	vdir := "vanilla"
	if len(os.Args) > 1 {
		vdir = os.Args[1]
	}
	vdir, err := filepath.Abs(vdir)
	if err != nil {
		log.Fatal(err)
	}
	jar := filepath.Join(vdir, "versions/26.2/server-26.2.jar")
	cp := []string{jar}
	filepath.Walk(filepath.Join(vdir, "libraries"), func(p string, info os.FileInfo, err error) error {
		if err == nil && strings.HasSuffix(p, ".jar") {
			cp = append(cp, p)
		}
		return nil
	})
	tmp, err := os.MkdirTemp("", "genshapes")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	if err := os.WriteFile(filepath.Join(tmp, "Probe.java"), []byte(probe), 0o644); err != nil {
		log.Fatal(err)
	}
	classpath := strings.Join(cp, string(os.PathListSeparator))
	if out, err := exec.Command("javac", "-cp", classpath, "-d", tmp, filepath.Join(tmp, "Probe.java")).CombinedOutput(); err != nil {
		log.Fatalf("javac: %v\n%s", err, out)
	}
	cmd := exec.Command("java", "-cp", classpath+string(os.PathListSeparator)+tmp, "Probe")
	cmd.Dir = tmp
	out, err := cmd.Output()
	if err != nil {
		log.Fatalf("java: %v", err)
	}
	shapeIdx := map[string]int{"-": 0}
	shapes := []string{"-"}
	intern := func(s string) int {
		if i, ok := shapeIdx[s]; ok {
			return i
		}
		shapeIdx[s] = len(shapes)
		shapes = append(shapes, s)
		return len(shapes) - 1
	}
	type row struct{ flags, outline, interact int }
	var rows []row
	type blk struct {
		kind       int
		maxH, maxV string
	}
	blocks := map[string]blk{}
	buckets := map[string][3]string{}
	h := sha256.New()
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if j := strings.Index(line, "BKT "); j >= 0 {
			fmt.Fprintln(h, line[j:])
			f := strings.Fields(line[j+4:])
			if f[1] != "-" || f[2] != "-" {
				buckets[f[0]] = [3]string{f[1], f[2], f[3]}
			}
			continue
		}
		if j := strings.Index(line, "BLK "); j >= 0 {
			fmt.Fprintln(h, line[j:])
			f := strings.Fields(line[j+4:])
			k, _ := strconv.Atoi(f[1])
			if k != 0 {
				blocks[f[0]] = blk{k, f[2], f[3]}
			}
			continue
		}
		i := strings.Index(line, "ROW ")
		if i < 0 {
			continue
		}
		fmt.Fprintln(h, line[i:])
		f := strings.Fields(line[i+4:])
		id, _ := strconv.Atoi(f[0])
		if id != len(rows) {
			log.Fatalf("row %d out of order", id)
		}
		fl, _ := strconv.Atoi(f[1])
		rows = append(rows, row{fl, intern(f[2]), intern(f[3])})
	}
	if len(rows) == 0 {
		log.Fatal("probe produced no rows")
	}
	var w bytes.Buffer
	fmt.Fprintf(&w, "// Code generated by tools/genshapes; DO NOT EDIT.\n")
	fmt.Fprintf(&w, "// Source: 26.2 jar oracle run (public BlockStateBase / Block / VoxelShape accessors).\n")
	fmt.Fprintf(&w, "// States: %d; shapes: %d; output SHA-256: %x\n\n", len(rows), len(shapes), h.Sum(nil))
	fmt.Fprintf(&w, "package server\n\n")
	fmt.Fprintf(&w, "// vanillaShapes are the distinct shapes as AABB lists in cell coordinates;\n// entry 0 is the empty shape.\n")
	fmt.Fprintf(&w, "var vanillaShapes = [...][]cellBox{\n")
	for _, s := range shapes {
		if s == "-" {
			w.WriteString("\t{},\n")
			continue
		}
		w.WriteString("\t{")
		for k, b := range strings.Split(s, ";") {
			if k > 0 {
				w.WriteString(", ")
			}
			fmt.Fprintf(&w, "{%s}", strings.ReplaceAll(b, ",", ", "))
		}
		w.WriteString("},\n")
	}
	w.WriteString("}\n\n")
	fmt.Fprintf(&w, "// vanillaStateShapes is indexed by protocol 776 state id: outline shape,\n// interaction shape, and flags (1 canBeReplaced(water), 2 canBeReplaced(lava),\n// 4 canPlaceLiquid(water), 8 canPlaceLiquid(lava) with an empty getter).\n")
	fmt.Fprintf(&w, "var vanillaStateShapes = [...]stateShapes{")
	for i, r := range rows {
		if i%6 == 0 {
			w.WriteString("\n\t")
		} else {
			w.WriteString(" ")
		}
		fmt.Fprintf(&w, "{%d, %d, %d},", r.outline, r.interact, r.flags)
	}
	w.WriteString("\n}\n\n")
	fmt.Fprintf(&w, "// vanillaBlockOffsets is each offset block's kind (1 XZ, 2 XYZ) and\n// maximum horizontal / vertical offset.\n")
	fmt.Fprintf(&w, "var vanillaBlockOffsets = map[string]blockOffset{\n")
	keys := make([]string, 0, len(blocks))
	for k := range blocks {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b := blocks[k]
		fmt.Fprintf(&w, "\t%q: {%d, %s, %s},\n", k, b.kind, b.maxH, b.maxV)
	}
	w.WriteString("}\n")
	fmt.Fprintf(&w, "\n// vanillaBucketBlocks names, per BucketPickup / LiquidBlockContainer block,\n// the class declaring its pickupBlock and placeLiquid and the pickup sound.\n")
	fmt.Fprintf(&w, "var vanillaBucketBlocks = map[string]bucketBlock{\n")
	keys = keys[:0]
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b := buckets[k]
		for i := range b {
			if b[i] == "-" {
				b[i] = ""
			}
		}
		fmt.Fprintf(&w, "\t%q: {%q, %q, %q},\n", k, b[0], b[1], b[2])
	}
	w.WriteString("}\n")
	if err := os.WriteFile("internal/server/shapes_gen.go", w.Bytes(), 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("wrote %d states, %d shapes, %d offset blocks\n", len(rows), len(shapes), len(blocks))
}
