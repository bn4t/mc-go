// Command genplacement measures the block-item facts BlockItem.place
// branches on - each block item's item class (StandingAndWallBlockItem,
// DoubleHighBlockItem, BedItem, ...), the block it places and, for a
// standing/wall item, its wall block and attachment direction - and
// each block's SoundType place sound, volume and pitch and note block
// instrument, by running the
// 26.2 server jar as an oracle: a small Java harness bootstraps the
// registries and reads the item and block objects (public accessors,
// plus reflection for the standing/wall item's two fields).
//
// Source class: jar oracle run. The harness copies no game code; the
// output is one row per item and per block, in registry order.
//
// Usage: go run ./tools/genplacement [vanilla-dir]
// Needs java (JDK 25) and the unpacked vanilla/versions + libraries.
// Writes internal/server/placement_gen.go.
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
	"strconv"
	"strings"
)

const probe = `import net.minecraft.SharedConstants;
import net.minecraft.server.Bootstrap;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.world.item.Item;
import net.minecraft.world.item.BlockItem;
import net.minecraft.world.item.StandingAndWallBlockItem;
import net.minecraft.world.level.block.Block;
import net.minecraft.world.level.block.SoundType;
import net.minecraft.core.Direction;
public class Probe {
  public static void main(String[] a) throws Exception {
    SharedConstants.tryDetectVersion();
    Bootstrap.bootStrap();
    StringBuilder sb = new StringBuilder();
    for (Item it : BuiltInRegistries.ITEM) {
      if (!(it instanceof BlockItem bi)) continue;
      StringBuilder chain = new StringBuilder();
      for (Class<?> c = it.getClass(); c != null && c != Item.class; c = c.getSuperclass()) {
        if (chain.length() > 0) chain.append(',');
        chain.append(c.getSimpleName());
      }
      String wall = "", dir = "";
      if (it instanceof StandingAndWallBlockItem sw) {
        java.lang.reflect.Field f = StandingAndWallBlockItem.class.getDeclaredField("wallBlock");
        f.setAccessible(true);
        wall = BuiltInRegistries.BLOCK.getKey((Block) f.get(sw)).getPath();
        java.lang.reflect.Field g = StandingAndWallBlockItem.class.getDeclaredField("attachmentDirection");
        g.setAccessible(true);
        dir = ((Direction) g.get(sw)).getName();
      }
      sb.append("I\t").append(BuiltInRegistries.ITEM.getKey(it).getPath()).append('\t').append(chain)
        .append('\t').append(BuiltInRegistries.BLOCK.getKey(bi.getBlock()).getPath())
        .append('\t').append(wall).append('\t').append(dir).append('\n');
    }
    for (Block b : BuiltInRegistries.BLOCK) {
      SoundType st = b.defaultBlockState().getSoundType();
      sb.append("B\t").append(BuiltInRegistries.BLOCK.getKey(b).getPath()).append('\t')
        .append(st.getPlaceSound().location().getPath()).append('\t')
        .append(st.getVolume()).append('\t').append(st.getPitch()).append('\t')
        .append(b.defaultBlockState().instrument().getSerializedName()).append('\t')
        .append(b.defaultBlockState().instrument().worksAboveNoteBlock()).append('\n');
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
	tmp, err := os.MkdirTemp("", "genplacement")
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
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		log.Fatal(err)
	}
	jarSum, err := fileSHA256(jar)
	if err != nil {
		log.Fatal(err)
	}

	var items, sounds, instr bytes.Buffer
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		f := strings.Split(sc.Text(), "\t")
		switch f[0] {
		case "I":
			fmt.Fprintf(&items, "\t%q: {class: %q, block: %q, wall: %q, attach: %q},\n", f[1], f[2], f[3], f[4], f[5])
		case "B":
			vol, _ := strconv.ParseFloat(f[3], 32)
			pitch, _ := strconv.ParseFloat(f[4], 32)
			fmt.Fprintf(&sounds, "\t%q: {%q, %v, %v},\n", f[1], f[2], float32(vol), float32(pitch))
			fmt.Fprintf(&instr, "\t%q: {%q, %v},\n", f[1], f[5], f[6] == "true")
		}
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "// Code generated by tools/genplacement from the 26.2 server jar\n// (sha256 %s); DO NOT EDIT.\n\npackage server\n\n", jarSum)
	b.WriteString("// placeItems describes each block item: its item class chain (most\n// specific first, up to BlockItem), the block it places and, for a\n// StandingAndWallBlockItem, the wall block and attachment direction.\nvar placeItems = map[string]placeItem{\n")
	b.Write(items.Bytes())
	b.WriteString("}\n\n// blockPlaceSounds is each block's SoundType: place sound, volume,\n// pitch.\nvar blockPlaceSounds = map[string]placeSound{\n")
	b.Write(sounds.Bytes())
	b.WriteString("}\n\n// blockInstruments is each block's BlockState.instrument (default\n// state) and whether it works above a note block.\nvar blockInstruments = map[string]noteInstrument{\n")
	b.Write(instr.Bytes())
	b.WriteString("}\n")
	if err := os.WriteFile("internal/server/placement_gen.go", b.Bytes(), 0o644); err != nil {
		log.Fatal(err)
	}
}

func fileSHA256(p string) (string, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(b)), nil
}
