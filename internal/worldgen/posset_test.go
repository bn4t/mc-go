package worldgen

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestPosSetMatchesJavaHashSet replays the JDK probe's adds, pops and
// removals and checks the iteration order at every recorded point.
func TestPosSetMatchesJavaHashSet(t *testing.T) {
	f, err := os.Open("testdata/hashset_order.tsv.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(z)
	sc.Buffer(nil, 1<<24)
	var s *posSet
	scenario, checks, trees := "", 0, 0
	for line := 1; sc.Scan(); line++ {
		text := sc.Text()
		if strings.HasPrefix(text, "#") {
			continue
		}
		op, rest, _ := strings.Cut(text, " ")
		var p Point
		switch op {
		case "scenario":
			scenario, s = rest, &posSet{}
		case "add", "remove":
			fmt.Sscanf(rest, "%d %d %d", &p.X, &p.Y, &p.Z)
			if op == "add" {
				s.add(p)
			} else {
				s.remove(p)
			}
		case "pop":
			s.pop()
		case "order":
			var got []string
			s.each(func(p Point) { got = append(got, fmt.Sprintf("%d,%d,%d", p.X, p.Y, p.Z)) })
			if g := strings.Join(got, " "); g != rest {
				t.Fatalf("scenario %s line %d: order differs\n  got  %.300s\n  want %.300s", scenario, line, g, rest)
			}
			if len(got) != s.size {
				t.Fatalf("scenario %s line %d: size %d, %d iterated", scenario, line, s.size, len(got))
			}
			for _, b := range s.table {
				if b != nil && b.tree {
					trees++
				}
			}
			checks++
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if checks == 0 || trees == 0 {
		t.Fatalf("%d order checks, %d tree buckets seen: the probe no longer exercises tree buckets", checks, trees)
	}
	t.Logf("%d order checks, %d tree buckets seen", checks, trees)
}
