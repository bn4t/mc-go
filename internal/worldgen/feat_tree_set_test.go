package worldgen

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// jsetJava replays an operation script on a java.util.HashSet whose
// keys hash like BlockPos (and, like BlockPos, do not implement
// Comparable themselves), printing every pop and listing.
const jsetJava = `
import java.io.*;
import java.util.*;
public class JSet {
  static class P { final int x, y, z; P(int x, int y, int z) { this.x = x; this.y = y; this.z = z; }
    public int hashCode() { return (y + z * 31) * 31 + x; }
    public boolean equals(Object o) { if (!(o instanceof P)) return false; P p = (P) o; return p.x == x && p.y == y && p.z == z; }
    public String toString() { return x + "," + y + "," + z; } }
  public static void main(String[] a) throws Exception {
    BufferedReader r = new BufferedReader(new FileReader(a[0]));
    HashSet<P> s = new HashSet<>();
    int nop = 0;
    StringBuilder out = new StringBuilder();
    try {
    for (String l; (l = r.readLine()) != null;) {
      String[] f = l.split(" ");
      switch (f[0]) {
        case "a": s.add(new P(Integer.parseInt(f[1]), Integer.parseInt(f[2]), Integer.parseInt(f[3]))); break;
        case "r": s.remove(new P(Integer.parseInt(f[1]), Integer.parseInt(f[2]), Integer.parseInt(f[3]))); break;
        case "f": { Iterator<P> it = s.iterator(); P p = it.next(); it.remove(); out.append("f ").append(p).append('\n'); break; }
        case "l": { out.append("l"); for (P p : s) out.append(' ').append(p); out.append('\n'); break; }
      }
      if (System.getenv("JSET_SIZES") != null) { int h = 0; for (P p : s) h = ((h * 31 + p.x) * 31 + p.y) * 31 + p.z; out.append("n ").append(nop++).append(' ').append(s.size()).append(' ').append(h).append('\n'); }
    }
    } finally { System.out.print(out); }
  }
}
`

// TestJSetMatchesJava pins jset's iteration order against the JDK's
// HashSet, including tree bins, resizes and iterator removal.
func TestJSetMatchesJava(t *testing.T) {
	java, err := exec.LookPath("java")
	if err != nil {
		home, _ := os.UserHomeDir()
		java = filepath.Join(home, "opt", "jdk25", "bin", "java")
		if _, err := os.Stat(java); err != nil {
			t.Skip("no java")
		}
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "JSet.java")
	os.WriteFile(src, []byte(jsetJava), 0o644)
	rnd := rand.New(rand.NewSource(7))
	var script strings.Builder
	s := newJSet()
	var live []BlockPos
	byHash := map[int32]BlockPos{}
	for round := 0; round < 40; round++ {
		base := BlockPos{int32(rnd.Intn(4000) - 2000), int32(rnd.Intn(300) - 60), int32(rnd.Intn(4000) - 2000)}
		n := 20 + rnd.Intn(600)
		for i := 0; i < n; i++ {
			var p BlockPos
			switch rnd.Intn(3) {
			case 0: // same low hash bits, distinct hashes: forces tree bins
				k := int32(rnd.Intn(40))
				p = BlockPos{base[0] + k*1024, base[1], base[2]}
			case 1: // equal-x-offset collisions across y
				p = BlockPos{base[0] + int32(rnd.Intn(8))*2048, base[1] + int32(rnd.Intn(3)), base[2]}
			default:
				p = offset(base, int32(rnd.Intn(16)-8), int32(rnd.Intn(16)), int32(rnd.Intn(16)-8))
			}
			// Distinct keys with equal hash codes order by identity
			// hash inside Java tree bins; keep them out.
			if q, ok := byHash[jhash(p)]; ok && q != p {
				continue
			}
			byHash[jhash(p)] = p
			fmt.Fprintf(&script, "a %d %d %d\n", p[0], p[1], p[2])
			if s.add(p) {
				live = append(live, p)
			}
			if rnd.Intn(5) == 0 && s.n > 0 {
				script.WriteString("f\n")
				s.remove(s.first())
			}
			if rnd.Intn(9) == 0 && len(live) > 0 {
				q := live[rnd.Intn(len(live))]
				fmt.Fprintf(&script, "r %d %d %d\n", q[0], q[1], q[2])
				s.removeKey(q)
			}
		}
		script.WriteString("l\n")
		if round%10 == 9 {
			for s.n > 0 {
				script.WriteString("f\n")
				s.remove(s.first())
			}
		}
	}
	// Rebuild expectations deterministically by replaying the script.
	exp := replayJSet(script.String())
	path := filepath.Join(dir, "ops.txt")
	os.WriteFile(path, []byte(script.String()), 0o644)
	if d := os.Getenv("JSET_DUMP"); d != "" {
		os.WriteFile(d, []byte(script.String()), 0o644)
	}
	cmd := exec.Command(java, "-Xmx256m", src, path)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Errorf("java: %v\n%s", err, stderr.String())
	}
	got := strings.Split(strings.TrimSpace(string(out)), "\n")
	wl := strings.Split(strings.TrimSpace(exp), "\n")
	if len(got) != len(wl) {
		t.Errorf("java printed %d lines, model %d", len(got), len(wl))
	}
	for i := range min(len(got), len(wl)) {
		if got[i] != wl[i] {
			t.Fatalf("line %d differs:\njava  %.300s\nmodel %.300s", i, got[i], wl[i])
		}
	}
	t.Logf("%d lines match; tie breaks %d", len(got), JSetTieBreaks.Load())
}

func replayJSet(script string) string {
	s := newJSet()
	nop := 0
	var out strings.Builder
	for _, l := range strings.Split(strings.TrimSpace(script), "\n") {
		var p BlockPos
		switch l[0] {
		case 'a':
			fmt.Sscanf(l, "a %d %d %d", &p[0], &p[1], &p[2])
			s.add(p)
		case 'r':
			fmt.Sscanf(l, "r %d %d %d", &p[0], &p[1], &p[2])
			s.removeKey(p)
		case 'f':
			q := s.first()
			s.remove(q)
			fmt.Fprintf(&out, "f %d,%d,%d\n", q[0], q[1], q[2])
		case 'l':
			out.WriteString("l")
			for _, q := range s.list() {
				fmt.Fprintf(&out, " %d,%d,%d", q[0], q[1], q[2])
			}
			out.WriteString("\n")
		}
		if os.Getenv("JSET_SIZES") != "" {
			h := int32(0)
			for _, q := range s.list() {
				h = ((h*31+q[0])*31+q[1])*31 + q[2]
			}
			fmt.Fprintf(&out, "n %d %d %d\n", nop, s.n, h)
			nop++
		}
	}
	return out.String()
}
