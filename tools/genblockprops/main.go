// Command genblockprops measures per-state block facts the terrain
// generator branches on — air, blocksMotion, legacy solidity,
// replaceability, leaves, block entities, fluid state, sturdy faces
// (full/center/rigid support per direction), full collision cube,
// random ticking, skylight propagation, redstone conduction,
// post-processing, light emission and dampening, full collision faces
// per direction, empty collision — by running the 26.2
// server jar as an oracle: a small Java harness bootstraps the game's
// registries and asks every block state through its public accessors,
// with an empty block getter at the origin.
//
// Source class: jar oracle run (public BlockStateBase accessors). The
// harness copies no game code; the output is one row of facts per
// state in registry (protocol) order.
//
// Usage: go run ./tools/genblockprops [vanilla-dir]
// Needs java (JDK 25) and the unpacked vanilla/versions + libraries.
// Writes internal/worldgen/blockprops_gen.go.
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
import net.minecraft.world.level.block.LeavesBlock;
import net.minecraft.world.level.block.SupportType;
import net.minecraft.world.level.block.state.BlockState;
import net.minecraft.world.level.EmptyBlockGetter;
import net.minecraft.world.level.material.Fluids;
import net.minecraft.world.level.material.FluidState;
import net.minecraft.core.BlockPos;
import net.minecraft.core.Direction;
public class Probe {
  public static void main(String[] a) {
    SharedConstants.tryDetectVersion();
    Bootstrap.bootStrap();
    EmptyBlockGetter g = EmptyBlockGetter.INSTANCE;
    BlockPos p = BlockPos.ZERO;
    Direction[] dirs = {Direction.DOWN, Direction.UP, Direction.NORTH, Direction.SOUTH, Direction.WEST, Direction.EAST};
    StringBuilder sb = new StringBuilder();
    int id = 0;
    for (BlockState s : Block.BLOCK_STATE_REGISTRY) {
      long b = 0;
      if (s.isAir()) b |= 1L;
      if (s.blocksMotion()) b |= 1L << 1;
      if (s.isSolid()) b |= 1L << 2;
      if (s.canBeReplaced()) b |= 1L << 3;
      if (s.getBlock() instanceof LeavesBlock) b |= 1L << 4;
      if (s.hasBlockEntity()) b |= 1L << 5;
      FluidState f = s.getFluidState();
      if (!f.isEmpty()) b |= 1L << 6;
      if (f.is(Fluids.WATER) || f.is(Fluids.FLOWING_WATER)) b |= 1L << 7;
      if (f.is(Fluids.LAVA) || f.is(Fluids.FLOWING_LAVA)) b |= 1L << 8;
      if (f.isSource()) b |= 1L << 9;
      b |= ((long) f.getAmount() & 15) << 10;
      for (int i = 0; i < 6; i++) {
        if (s.isFaceSturdy(g, p, dirs[i], SupportType.FULL)) b |= 1L << (14 + i);
        if (s.isFaceSturdy(g, p, dirs[i], SupportType.CENTER)) b |= 1L << (20 + i);
        if (s.isFaceSturdy(g, p, dirs[i], SupportType.RIGID)) b |= 1L << (26 + i);
      }
      if (s.isCollisionShapeFullBlock(g, p)) b |= 1L << 32;
      if (s.isRandomlyTicking()) b |= 1L << 33;
      if (s.propagatesSkylightDown()) b |= 1L << 34;
      if (s.isRedstoneConductor(g, p)) b |= 1L << 35;
      if (s.getPostProcessPos(g, p) != null) b |= 1L << 36;
      if (s.isSolidRender()) b |= 1L << 37;
      if (s.canOcclude()) b |= 1L << 38;
      if (s.liquid()) b |= 1L << 39;
      b |= ((long) s.getLightEmission() & 15) << 40;
      b |= ((long) s.getLightDampening() & 15) << 44;
      for (int i = 0; i < 6; i++) {
        if (Block.isFaceFull(s.getCollisionShape(g, p), dirs[i])) b |= 1L << (48 + i);
      }
      if (s.getCollisionShape(g, p).isEmpty()) b |= 1L << 54;
      sb.append("ROW ").append(id).append(' ').append(b).append(' ').append(s.toString()).append('\n');
      if (s == s.getBlock().defaultBlockState()) {
        Class<?> c = s.getBlock().getClass();
        StringBuilder chain = new StringBuilder();
        while (c != null && c != Object.class) { if (chain.length() > 0) chain.append(','); chain.append(c.getSimpleName()); c = c.getSuperclass(); }
        sb.append("CLS ").append(net.minecraft.core.registries.BuiltInRegistries.BLOCK.getKey(s.getBlock()).getPath()).append(' ').append(chain).append('\n');
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
	tmp, err := os.MkdirTemp("", "genblockprops")
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
	var bits []uint64
	var names []string
	classes := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if j := strings.Index(line, "CLS "); j >= 0 {
			f := strings.SplitN(line[j+4:], " ", 2)
			classes[f[0]] = f[1]
			continue
		}
		i := strings.Index(line, "ROW ")
		if i < 0 {
			continue
		}
		f := strings.SplitN(line[i+4:], " ", 3)
		id, _ := strconv.Atoi(f[0])
		if id != len(bits) {
			log.Fatalf("row %d out of order", id)
		}
		v, _ := strconv.ParseUint(f[1], 10, 64)
		bits = append(bits, v)
		names = append(names, f[2])
	}
	if len(bits) == 0 {
		log.Fatal("probe produced no rows")
	}
	h := sha256.New()
	for i := range bits {
		fmt.Fprintf(h, "%d %s\n", bits[i], names[i])
	}
	var w bytes.Buffer
	fmt.Fprintf(&w, "// Code generated by tools/genblockprops; DO NOT EDIT.\n")
	fmt.Fprintf(&w, "// Source: 26.2 jar oracle run (public BlockStateBase accessors).\n")
	fmt.Fprintf(&w, "// States: %d; rows SHA-256: %x\n\n", len(bits), h.Sum(nil))
	fmt.Fprintf(&w, "package worldgen\n\n")
	fmt.Fprintf(&w, "// vanillaStateProps is indexed by protocol 776 state id; bit layout in blockprops.go.\n")
	fmt.Fprintf(&w, "var vanillaStateProps = [...]uint64{")
	for i, b := range bits {
		if i%8 == 0 {
			w.WriteString("\n\t")
		} else {
			w.WriteString(" ")
		}
		fmt.Fprintf(&w, "0x%x,", b)
	}
	w.WriteString("\n}\n\n")
	// The first state of each block, to cross-check the id order.
	fmt.Fprintf(&w, "// vanillaStateNames spot-checks the id order (every 512th state).\n")
	fmt.Fprintf(&w, "var vanillaStateNames = map[int]string{\n")
	for i := 0; i < len(names); i += 512 {
		fmt.Fprintf(&w, "\t%d: %q,\n", i, names[i])
	}
	w.WriteString("}\n\n")
	fmt.Fprintf(&w, "// vanillaBlockClasses is each block's behaviour class chain, most specific first.\n")
	fmt.Fprintf(&w, "var vanillaBlockClasses = map[string]string{\n")
	keys := make([]string, 0, len(classes))
	for k := range classes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&w, "\t%q: %q,\n", k, classes[k])
	}
	w.WriteString("}\n")
	if err := os.WriteFile("internal/worldgen/blockprops_gen.go", w.Bytes(), 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("wrote %d states\n", len(bits))
}
