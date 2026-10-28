import java.io.PrintWriter;
import java.util.HashSet;
import java.util.Iterator;
import java.util.Random;

// Records the iteration order of the JDK's HashSet over block positions
// (hashed as (y + z*31)*31 + x) through scripted adds, first-element
// pops and removals. Positions cluster in layers so buckets grow past
// eight entries and become trees, and sets grow and shrink across table
// sizes. Half the positions lie on diagonals (x+t, y, z-t+64m), whose
// hashes agree modulo 64, the way a wide flat canopy's do. Widths stay
// under 31 and layer spans under 30 so no two positions share a hash.
// Only the JDK is involved.
public class HashOrderProbe {
    record P(int x, int y, int z) {
        @Override public int hashCode() { return (y + z * 31) * 31 + x; }
    }

    public static void main(String[] args) throws Exception {
        try (PrintWriter out = new PrintWriter(args[0], java.nio.charset.StandardCharsets.UTF_8)) {
            int[][] boxes = {
                // x0, y0, z0, width, layers, depth, steps
                {0, 64, 0, 16, 3, 16, 600},
                {-1000, 70, 500, 24, 4, 24, 1500},
                {123456, -40, -98765, 30, 6, 30, 3000},
                {-7, 100, -7, 9, 2, 9, 400},
                {40, 0, 40, 28, 2, 28, 2500},
            };
            for (int s = 0; s < boxes.length; s++) {
                int[] b = boxes[s];
                Random r = new Random(s * 7919L + 17);
                HashSet<P> set = new HashSet<>();
                out.printf("scenario %d%n", s);
                for (int step = 0; step < b[6]; step++) {
                    int k = r.nextInt(10);
                    // Grow for the first half, then mostly shrink.
                    boolean grow = step < b[6] / 2 ? k < 7 : k < 3;
                    if (grow || set.isEmpty()) {
                        P p;
                        if (r.nextBoolean()) {
                            int t = r.nextInt(b[3]);
                            p = new P(b[0] + t, b[1] + r.nextInt(b[4]) * 2, b[2] - t + 64 * r.nextInt(4));
                        } else {
                            p = new P(b[0] + r.nextInt(b[3]), b[1] + r.nextInt(b[4]) * 2, b[2] + r.nextInt(b[5]));
                        }
                        set.add(p);
                        out.printf("add %d %d %d%n", p.x, p.y, p.z);
                    } else if (k < 8) {
                        Iterator<P> it = set.iterator();
                        it.next();
                        it.remove();
                        out.println("pop");
                    } else {
                        P p = new P(b[0] + r.nextInt(b[3]), b[1] + r.nextInt(b[4]) * 2, b[2] + r.nextInt(b[5]));
                        set.remove(p);
                        out.printf("remove %d %d %d%n", p.x, p.y, p.z);
                    }
                    if (step % 25 == 24 || step == b[6] - 1) {
                        StringBuilder sb = new StringBuilder("order");
                        for (P p : set) sb.append(' ').append(p.x).append(',').append(p.y).append(',').append(p.z);
                        out.println(sb);
                    }
                }
            }
        }
    }
}
