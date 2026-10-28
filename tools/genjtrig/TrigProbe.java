import java.io.PrintWriter;
import java.math.BigDecimal;
import java.math.MathContext;

// TrigProbe records java.lang.Math.sin and cos, as the running JVM
// computes them, next to the correctly rounded values from a 90-digit
// series, for arguments in the ranges world generation uses. Uses only
// the JDK.
public class TrigProbe {
    static final MathContext MC = new MathContext(90);
    static final BigDecimal PI = new BigDecimal(
        "3.14159265358979323846264338327950288419716939937510582097494459230781640628620899862803482534211706798214808651");

    static BigDecimal sin(BigDecimal x) {
        BigDecimal twoPi = PI.multiply(BigDecimal.valueOf(2), MC);
        BigDecimal k = new BigDecimal(x.divide(twoPi, MC).toBigInteger());
        x = x.subtract(twoPi.multiply(k, MC), MC);
        BigDecimal term = x, sum = x, x2 = x.multiply(x, MC);
        for (int n = 1; n < 90; n++) {
            term = term.multiply(x2, MC).divide(BigDecimal.valueOf((2L * n) * (2L * n + 1)), MC).negate();
            sum = sum.add(term, MC);
        }
        return sum;
    }

    static BigDecimal cos(BigDecimal x) {
        return sin(PI.divide(BigDecimal.valueOf(2), MC).subtract(x, MC));
    }

    static void row(PrintWriter out, double x) {
        BigDecimal b = new BigDecimal(x);
        out.printf("%016x\t%016x\t%016x\t%016x\t%016x%n", Double.doubleToRawLongBits(x),
            Double.doubleToRawLongBits(Math.sin(x)), Double.doubleToRawLongBits(Math.cos(x)),
            Double.doubleToRawLongBits(sin(b).doubleValue()), Double.doubleToRawLongBits(cos(b).doubleValue()));
    }

    public static void main(String[] args) throws Exception {
        try (PrintWriter out = new PrintWriter(args[0])) {
            out.println("# x\tMath.sin\tMath.cos\tsin rounded\tcos rounded (os.arch " + System.getProperty("os.arch") + ")");
            // The End pillar angles.
            for (int i = 0; i < 10; i++) {
                row(out, 2.0 * (-Math.PI + 0.3141592653589793 * i));
            }
            java.util.Random r = new java.util.Random(26_2L);
            for (int i = 0; i < 1500; i++) {
                // Iceberg and trunk angles: a double turn, a quarter on.
                double a = r.nextDouble() * 2.0 * Math.PI;
                row(out, i % 3 == 0 ? a + Math.PI / 2 : a);
            }
            for (int i = 0; i < 500; i++) {
                // Ore and spawn-search angles: floats widened.
                row(out, (double) (r.nextFloat() * (float) Math.PI * (1 + r.nextInt(2))));
            }
        }
    }
}
