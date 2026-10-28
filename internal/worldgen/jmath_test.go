package worldgen

import (
	"bufio"
	"compress/gzip"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestJavaTrig checks jsin and jcos against java.lang.Math as the JVM
// computed it (testdata/java_trig.tsv.gz, from tools/genjtrig): they
// must give the correctly rounded value everywhere, which is the JVM's
// wherever the JVM's is correctly rounded.
func TestJavaTrig(t *testing.T) {
	f, err := os.Open("testdata/java_trig.tsv.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	rows, jvmOff := 0, 0
	s := bufio.NewScanner(z)
	for s.Scan() {
		if strings.HasPrefix(s.Text(), "#") {
			continue
		}
		var v [5]float64
		for i, field := range strings.Split(s.Text(), "\t") {
			b, err := strconv.ParseUint(field, 16, 64)
			if err != nil {
				t.Fatal(err)
			}
			v[i] = math.Float64frombits(b)
		}
		x, jvmSin, jvmCos, crSin, crCos := v[0], v[1], v[2], v[3], v[4]
		rows++
		if got := jsin(x); got != crSin {
			t.Errorf("jsin(%v) = %v, correctly rounded %v", x, got, crSin)
		}
		if got := jcos(x); got != crCos {
			t.Errorf("jcos(%v) = %v, correctly rounded %v", x, got, crCos)
		}
		if jvmSin != crSin {
			jvmOff++
		}
		if jvmCos != crCos {
			jvmOff++
		}
	}
	if rows < 2000 {
		t.Fatalf("only %d rows", rows)
	}
	t.Logf("%d arguments; the JVM's value is not the rounded one %d times", rows, jvmOff)
}
