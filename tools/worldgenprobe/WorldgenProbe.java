import java.io.PrintWriter;
import net.minecraft.SharedConstants;
import net.minecraft.core.HolderGetter;
import net.minecraft.core.registries.Registries;
import net.minecraft.data.registries.VanillaRegistries;
import net.minecraft.util.RandomSource;
import net.minecraft.world.level.levelgen.LegacyRandomSource;
import net.minecraft.world.level.levelgen.NoiseGeneratorSettings;
import net.minecraft.world.level.levelgen.RandomState;
import net.minecraft.world.level.levelgen.XoroshiroRandomSource;
import net.minecraft.world.level.levelgen.synth.ImprovedNoise;
import net.minecraft.world.level.levelgen.synth.NormalNoise;
import net.minecraft.world.level.levelgen.DensityFunction;

// Asks the official server's own world-generation classes for sample
// values: random draws, positional sources, noise samples and noise
// router outputs. Only public entry points are called; nothing is
// decompiled. Output is tab-separated "kind key... value" lines.
public class WorldgenProbe {
    static PrintWriter out;
    static final long[] SEEDS = {0L, 1L, -1L, 12345L, 8675309L, Long.MAX_VALUE, Long.MIN_VALUE};
    static final double[][] POINTS = {
        {0, 0, 0}, {0.5, 1.25, -2.75}, {123.456, -64.5, 987.25}, {-1000.125, 32, 4096.5},
        {1e5, 300, -1e5}, {-33.3, 0.01, 17.77}, {2.5e6, -40, 3.1e6}, {7, 64, 7},
    };

    static void draws(String kind, RandomSource r, long seed) {
        for (int i = 0; i < 4; i++) out.printf("%s\t%d\tlong\t%d\t%d%n", kind, seed, i, r.nextLong());
        for (int i = 0; i < 4; i++) out.printf("%s\t%d\tint\t%d\t%d%n", kind, seed, i, r.nextInt());
        for (int i = 0; i < 6; i++) out.printf("%s\t%d\tintn\t%d\t%d%n", kind, seed, i, r.nextInt(new int[]{1, 2, 7, 100, 256, 1_000_000_007}[i]));
        for (int i = 0; i < 4; i++) out.printf("%s\t%d\tfloat\t%d\t%d%n", kind, seed, i, Float.floatToRawIntBits(r.nextFloat()));
        for (int i = 0; i < 4; i++) out.printf("%s\t%d\tdouble\t%d\t%d%n", kind, seed, i, Double.doubleToRawLongBits(r.nextDouble()));
        for (int i = 0; i < 4; i++) out.printf("%s\t%d\tbool\t%d\t%b%n", kind, seed, i, r.nextBoolean());
        for (int i = 0; i < 4; i++) out.printf("%s\t%d\tgauss\t%d\t%d%n", kind, seed, i, Double.doubleToRawLongBits(r.nextGaussian()));
        var pos = r.forkPositional();
        out.printf("%s\t%d\tat\t1,-2,3\t%d%n", kind, seed, pos.at(1, -2, 3).nextLong());
        out.printf("%s\t%d\tat\t-30000000,320,29999999\t%d%n", kind, seed, pos.at(-30000000, 320, 29999999).nextLong());
        out.printf("%s\t%d\thash\tminecraft:octave_-7\t%d%n", kind, seed, pos.fromHashOf("minecraft:octave_-7").nextLong());
        out.printf("%s\t%d\tfork\t0\t%d%n", kind, seed, r.fork().nextLong());
    }

    public static void main(String[] args) throws Exception {
        SharedConstants.tryDetectVersion();
        net.minecraft.server.Bootstrap.bootStrap();
        out = new PrintWriter(args[0], java.nio.charset.StandardCharsets.UTF_8);
        for (long seed : SEEDS) {
            draws("xoro", new XoroshiroRandomSource(seed), seed);
            draws("legacy", new LegacyRandomSource(seed), seed);
            var improved = new ImprovedNoise(new XoroshiroRandomSource(seed));
            for (double[] p : POINTS) {
                out.printf("improved\t%d\t%s,%s,%s\t%d%n", seed, p[0], p[1], p[2], Double.doubleToRawLongBits(improved.noise(p[0], p[1], p[2])));
            }
        }
        var lookup = VanillaRegistries.createLookup();
        HolderGetter<NormalNoise.NoiseParameters> noises = lookup.lookupOrThrow(Registries.NOISE);
        var settings = lookup.lookupOrThrow(Registries.NOISE_SETTINGS).getOrThrow(NoiseGeneratorSettings.OVERWORLD).value();
        for (long seed : new long[]{0L, 12345L, -4172144997902289642L}) {
            var state = RandomState.create(settings, noises, seed);
            for (String name : new String[]{"continentalness", "erosion", "ridge", "temperature", "vegetation", "offset", "jagged", "surface", "aquifer_barrier", "ore_veininess", "cave_entrance", "spaghetti_3d_1"}) {
                var key = net.minecraft.resources.ResourceKey.create(Registries.NOISE, net.minecraft.resources.Identifier.withDefaultNamespace(name));
                var noise = state.getOrCreateNoise(key);
                for (double[] p : POINTS) {
                    out.printf("normal\t%d\t%s\t%s,%s,%s\t%d%n", seed, name, p[0], p[1], p[2], Double.doubleToRawLongBits(noise.getValue(p[0], p[1], p[2])));
                }
            }
            var router = state.router();
            DensityFunction[] fns = {router.barrierNoise(), router.fluidLevelFloodednessNoise(), router.fluidLevelSpreadNoise(), router.lavaNoise(),
                router.temperature(), router.vegetation(), router.continents(), router.erosion(), router.depth(), router.ridges(),
                router.preliminarySurfaceLevel(), router.finalDensity(), router.veinToggle(), router.veinRidged(), router.veinGap()};
            String[] names = {"barrier", "fluid_level_floodedness", "fluid_level_spread", "lava", "temperature", "vegetation",
                "continents", "erosion", "depth", "ridges", "preliminary_surface_level", "final_density", "vein_toggle", "vein_ridged", "vein_gap"};
            int[][] blocks = new int[40][];
            int[][] fixed = {{0, 0, 0}, {0, 64, 0}, {100, 70, -200}, {-1234, -30, 567}, {4000, 120, 4000}, {-8, 40, 8}, {31, 63, -17}, {250000, 10, -250000}};
            System.arraycopy(fixed, 0, blocks, 0, fixed.length);
            // The rest spread over a 6000-block square and the full build
            // height, from a fixed LCG so the set is reproducible.
            long lcg = 42;
            for (int i = fixed.length; i < blocks.length; i++) {
                lcg = lcg * 6364136223846793005L + 1442695040888963407L;
                int x = (int) ((lcg >>> 33) % 6000) - 3000;
                lcg = lcg * 6364136223846793005L + 1442695040888963407L;
                int y = (int) ((lcg >>> 33) % 384) - 64;
                lcg = lcg * 6364136223846793005L + 1442695040888963407L;
                int z = (int) ((lcg >>> 33) % 6000) - 3000;
                blocks[i] = new int[]{x, y, z};
            }
            for (int i = 0; i < fns.length; i++) {
                for (int[] b : blocks) {
                    double v = fns[i].compute(new DensityFunction.SinglePointContext(b[0], b[1], b[2]));
                    out.printf("router\t%d\t%s\t%d,%d,%d\t%d%n", seed, names[i], b[0], b[1], b[2], Double.doubleToRawLongBits(v));
                }
            }
        }
        // Biomes: the overworld multi-noise source at quart positions, the
        // block-level zoom over it, and the zoom seed.
        var paramLists = lookup.lookupOrThrow(Registries.MULTI_NOISE_BIOME_SOURCE_PARAMETER_LIST);
        var overworldList = paramLists.getOrThrow(net.minecraft.world.level.biome.MultiNoiseBiomeSourceParameterLists.OVERWORLD);
        var source = net.minecraft.world.level.biome.MultiNoiseBiomeSource.createFromPreset(overworldList);
        for (long seed : new long[]{0L, 12345L, -4172144997902289642L}) {
            var state = RandomState.create(settings, noises, seed);
            var sampler = state.sampler();
            long lcg = seed ^ 0x5DEECE66DL;
            for (int i = 0; i < 600; i++) {
                lcg = lcg * 6364136223846793005L + 1442695040888963407L;
                int qx = (int) ((lcg >>> 33) % 2500) - 1250;
                lcg = lcg * 6364136223846793005L + 1442695040888963407L;
                int qy = (int) ((lcg >>> 33) % 96) - 16;
                lcg = lcg * 6364136223846793005L + 1442695040888963407L;
                int qz = (int) ((lcg >>> 33) % 2500) - 1250;
                var biome = source.getNoiseBiome(qx, qy, qz, sampler);
                out.printf("biome\t%d\t%d,%d,%d\t%s%n", seed, qx, qy, qz, biome.unwrapKey().orElseThrow().identifier());
            }
            long zoomSeed = net.minecraft.world.level.biome.BiomeManager.obfuscateSeed(seed);
            out.printf("zoomseed\t%d\t%d%n", seed, zoomSeed);
            var manager = new net.minecraft.world.level.biome.BiomeManager((x, y, z) -> source.getNoiseBiome(x, y, z, sampler), zoomSeed);
            for (int i = 0; i < 400; i++) {
                lcg = lcg * 6364136223846793005L + 1442695040888963407L;
                int x = (int) ((lcg >>> 33) % 512) - 256;
                lcg = lcg * 6364136223846793005L + 1442695040888963407L;
                int y = (int) ((lcg >>> 33) % 200) - 40;
                lcg = lcg * 6364136223846793005L + 1442695040888963407L;
                int z = (int) ((lcg >>> 33) % 512) - 256;
                var biome = manager.getBiome(new net.minecraft.core.BlockPos(x, y, z));
                out.printf("zoom\t%d\t%d,%d,%d\t%s%n", seed, x, y, z, biome.unwrapKey().orElseThrow().identifier());
            }
        }
        // The game's sine table, as a polynomial hash of its float bits.
        var sinField = net.minecraft.util.Mth.class.getDeclaredField("SIN");
        sinField.setAccessible(true);
        long sinHash = 0;
        for (float f : (float[]) sinField.get(null)) sinHash = sinHash * 31 + Float.floatToRawIntBits(f);
        out.printf("mthsin\t%d%n", sinHash);
        // The climate spawn search the initial world spawn starts from.
        for (long seed : new long[]{0L, 12345L, -4172144997902289642L, 1L, 42L, 8675309L, 2026L, -1L,
                3141592653589793L, -987654321L, 7777777L, 123456789012345L}) {
            var sp = RandomState.create(settings, noises, seed).sampler().findSpawnPosition();
            out.printf("spawn\t%d\t%d,%d%n", seed, sp.getX(), sp.getZ());
        }
        out.close();
    }
}
