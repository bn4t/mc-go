import java.io.PrintWriter;
import java.lang.reflect.InvocationHandler;
import java.lang.reflect.Method;
import java.lang.reflect.Proxy;
import java.util.ArrayList;
import java.util.EnumSet;
import java.util.IdentityHashMap;
import java.util.List;
import java.util.Map;
import com.mojang.serialization.Lifecycle;
import net.minecraft.SharedConstants;
import net.minecraft.core.BlockPos;
import net.minecraft.core.Holder;
import net.minecraft.core.HolderLookup;
import net.minecraft.core.MappedRegistry;
import net.minecraft.core.QuartPos;
import net.minecraft.core.RegistrationInfo;
import net.minecraft.core.RegistryAccess;
import net.minecraft.core.registries.Registries;
import net.minecraft.resources.ResourceKey;
import net.minecraft.world.level.ChunkPos;
import net.minecraft.world.level.LevelHeightAccessor;
import net.minecraft.world.level.WorldGenLevel;
import net.minecraft.world.level.biome.Biome;
import net.minecraft.world.level.biome.BiomeManager;
import net.minecraft.world.level.biome.BiomeSource;
import net.minecraft.world.level.block.Blocks;
import net.minecraft.world.level.block.state.BlockState;
import net.minecraft.world.level.chunk.ChunkAccess;
import net.minecraft.world.level.chunk.PalettedContainerFactory;
import net.minecraft.world.level.chunk.ProtoChunk;
import net.minecraft.world.level.chunk.UpgradeData;
import net.minecraft.world.level.chunk.status.ChunkStatus;
import net.minecraft.world.level.levelgen.Aquifer;
import net.minecraft.world.level.levelgen.Heightmap;
import net.minecraft.world.level.levelgen.NoiseBasedChunkGenerator;
import net.minecraft.world.level.levelgen.NoiseChunk;
import net.minecraft.world.level.levelgen.NoiseGeneratorSettings;
import net.minecraft.world.level.levelgen.RandomState;
import net.minecraft.world.level.levelgen.WorldgenRandom;
import net.minecraft.world.level.levelgen.blending.Blender;
import net.minecraft.world.level.levelgen.placement.PlacedFeature;

// Has the official server's generator decorate standalone proto chunks:
// a 5×5 square of chunks gets biomes, the middle 3×3 the noise fill, the
// surface and the carvers, and the middle chunk is then decorated feature
// by feature in the generator's own step and index order, through a
// WorldGenLevel proxy that behaves as a generation region (writes only
// within one chunk of the middle). For every placed feature the probe
// records which blocks of the 3×3 it changed, so features can be checked
// one at a time. It also records the generator's per-step feature order.
// Only public entry points are called, plus reflective calls of the
// generator's own stage methods and its feature order.
public class FeatureProbe {
    public static void main(String[] args) throws Exception {
        SharedConstants.tryDetectVersion();
        net.minecraft.server.Bootstrap.bootStrap();
        var resources = new net.minecraft.server.packs.resources.MultiPackResourceManager(
            net.minecraft.server.packs.PackType.SERVER_DATA,
            List.of(net.minecraft.server.packs.repository.ServerPacksSource.createVanillaPackSource()));
        // The worldgen registries come from the vanilla datapack's JSON, as
        // the server's world loader builds them, not from the in-code
        // bootstrap: a codec whose field getter reads another field
        // (cherry foliage's corner_hole_chance) makes the two differ.
        var layers = net.minecraft.server.RegistryLayer.createRegistryAccess();
        var pendingTags = net.minecraft.tags.TagLoader.loadTagsForExistingRegistries(resources,
            layers.getLayer(net.minecraft.server.RegistryLayer.STATIC));
        var context = net.minecraft.tags.TagLoader.buildUpdatedLookups(
            layers.getAccessForLoading(net.minecraft.server.RegistryLayer.WORLDGEN), pendingTags);
        HolderLookup.Provider lookup = net.minecraft.resources.RegistryDataLoader.load(resources, context,
            net.minecraft.resources.RegistryDataLoader.WORLDGEN_REGISTRIES, Runnable::run).join();
        pendingTags.forEach(net.minecraft.core.Registry.PendingTags::apply);
        var biomes = lookup.lookupOrThrow(Registries.BIOME);
        MappedRegistry<Biome> biomeRegistry = new MappedRegistry<>(Registries.BIOME, Lifecycle.stable());
        biomes.listElements().forEach(h -> biomeRegistry.register(h.key(), h.value(), RegistrationInfo.BUILT_IN));
        biomeRegistry.freeze();
        RegistryAccess access = new RegistryAccess.ImmutableRegistryAccess(List.of(biomeRegistry));
        PalettedContainerFactory containers = PalettedContainerFactory.create(access);
        var paramLists = lookup.lookupOrThrow(Registries.MULTI_NOISE_BIOME_SOURCE_PARAMETER_LIST);
        var dimTypes = lookup.lookupOrThrow(Registries.DIMENSION_TYPE);
        try (PrintWriter out = new PrintWriter(args[0], java.nio.charset.StandardCharsets.UTF_8)) {
            new FeatureProbe(out, "overworld", lookup, access, containers, NoiseGeneratorSettings.OVERWORLD,
                net.minecraft.world.level.biome.MultiNoiseBiomeSource.createFromPreset(
                    paramLists.getOrThrow(net.minecraft.world.level.biome.MultiNoiseBiomeSourceParameterLists.OVERWORLD)),
                dimTypes.getOrThrow(net.minecraft.world.level.dimension.BuiltinDimensionTypes.OVERWORLD).value(), -64, 384)
                .run(new long[]{12345L}, new int[][]{
                    {-24, 24}, {-24, -24}, {0, -24}, {-96, -24}, {-96, 24}, {-144, 24}, {-240, -264}, {-72, -120},
                    {-72, 96}, {312, 48}, {48, -120}, {0, -144}, {-48, 48}, {48, -48}, {0, 0}, {-168, 24},
                    {24, 48}, {264, 288}, {-96, -408}, {-72, -72}, {-72, -96}, {72, -72}, {48, 96}, {0, 144},
                    {48, 24}, {-264, -240}, {-24, 144}, {-24, 48}, {-48, 0}, {-240, -144},
                    // One chunk per overworld biome: the first, by growing
                    // ring, whose 3×3 surface (or cave-depth) quarts are
                    // mostly that biome under seed 12345.
                    {-172, 112}, {-68, 56}, {48, -104}, {-28, -28}, {36, -56}, {20, 12}, {-4, -24}, {28, -116},
                    {8, -8}, {20, 100}, {-108, 108}, {4, 24}, {-140, 32}, {-36, 24}, {-308, -284}, {8, -132},
                    {-20, 16}, {52, 52}, {-52, 44}, {48, -44}, {-36, 0}, {44, -48}, {-36, -12}, {-40, -36},
                    {-104, 100}, {-108, -88}, {28, 36}, {260, 276}, {4, -44}, {20, -28}, {-232, -140}, {8, -32},
                    {-20, 20}, {-4, -16}, {-52, -12}, {-68, -92}, {56, 44}, {44, -40}, {-20, 48}, {36, 40},
                    {-44, -44}, {-52, 20}, {-12, 16}, {56, 68}, {-28, 24}, {-108, -104}, {24, -36}, {-264, -244},
                    {-100, -96}, {104, 76}, {0, 112}, {80, -240}, {-264, -264},
                    // More chunks deep in lush caves, deep dark and
                    // dripstone caves, whose features are rarer.
                    {1, -1}, {-2, 2}, {6, -6}, {6, -3}, {48, -39}, {48, -36},
                    // More frozen and warm ocean chunks for icebergs and
                    // coral, and forest, taiga and jungle chunks for fallen
                    // trees and bamboo.
                    {44, 22}, {44, 25}, {45, 15}, {47, 97}, {49, 95}, {19, 98}, {53, 51},
                    {-263, -240}, {-265, -241}, {-262, -243}, {-266, -246},
                    {-66, 54}, {-68, 53}, {-17, 17}, {-18, -18}, {-27, -27}, {-9, -21}, {18, 33}, {21, 33}, {-36, -36}});
            new FeatureProbe(out, "the_nether", lookup, access, containers, NoiseGeneratorSettings.NETHER,
                net.minecraft.world.level.biome.MultiNoiseBiomeSource.createFromPreset(
                    paramLists.getOrThrow(net.minecraft.world.level.biome.MultiNoiseBiomeSourceParameterLists.NETHER)),
                dimTypes.getOrThrow(net.minecraft.world.level.dimension.BuiltinDimensionTypes.NETHER).value(), 0, 256)
                .run(new long[]{12345L}, new int[][]{{0, 0}, {-3, 5}, {20, -11}, {-40, 33}, {7, 7}, {100, -60},
                    // One chunk per nether biome, chosen as for the overworld.
                    {-16, 16}, {-4, 2}, {-22, 10}, {6, -6},
                    // More warped forest and basalt delta chunks.
                    {7, -7}, {7, -4}, {-17, 17}, {-18, 15}});
            new FeatureProbe(out, "the_end", lookup, access, containers, NoiseGeneratorSettings.END,
                net.minecraft.world.level.biome.TheEndBiomeSource.create(lookup.lookupOrThrow(Registries.BIOME)),
                dimTypes.getOrThrow(net.minecraft.world.level.dimension.BuiltinDimensionTypes.END).value(), 0, 256)
                .run(new long[]{12345L}, new int[][]{{0, 0}, {3, -2}, {70, 10}, {-90, 40}, {120, -130},
                    // The chunks holding the ten obsidian pillars' centres.
                    {2, 0}, {2, 1}, {0, 2}, {-1, 2}, {-3, 1}, {-3, -1}, {-3, -2}, {-1, -3}, {0, -3}, {2, -2}});
        }
    }

    final PrintWriter out;
    final String dim;
    final HolderLookup.Provider lookup;
    final RegistryAccess access;
    final PalettedContainerFactory containers;
    final NoiseGeneratorSettings settings;
    final BiomeSource biomeSource;
    final NoiseBasedChunkGenerator generator;
    final net.minecraft.world.level.dimension.DimensionType dimensionType;
    final LevelHeightAccessor height;
    final Method doCreateBiomes, doFill, forChunk;
    final Aquifer.FluidPicker picker;
    final Object beardifierMarker;
    final List<?> featuresPerStep;
    final Map<PlacedFeature, String> featureNames = new IdentityHashMap<>();
    // spawned lists the entities the running feature added, each as its
    // type and raw position, yaw, invulnerability and beam target.
    final List<String> spawned = new ArrayList<>();

    FeatureProbe(PrintWriter out, String dim, HolderLookup.Provider lookup, RegistryAccess access, PalettedContainerFactory containers,
            ResourceKey<NoiseGeneratorSettings> settingsKey, BiomeSource biomeSource,
            net.minecraft.world.level.dimension.DimensionType dimensionType, int minY, int totalHeight) throws Exception {
        this.out = out;
        this.dim = dim;
        this.lookup = lookup;
        this.access = access;
        this.containers = containers;
        this.biomeSource = biomeSource;
        this.dimensionType = dimensionType;
        var settingsHolder = lookup.lookupOrThrow(Registries.NOISE_SETTINGS).getOrThrow(settingsKey);
        this.settings = settingsHolder.value();
        this.generator = new NoiseBasedChunkGenerator(biomeSource, settingsHolder);
        this.height = LevelHeightAccessor.create(minY, totalHeight);
        doCreateBiomes = method(NoiseBasedChunkGenerator.class, "doCreateBiomes");
        doFill = method(NoiseBasedChunkGenerator.class, "doFill");
        forChunk = method(NoiseChunk.class, "forChunk");
        Method fluidPicker = NoiseBasedChunkGenerator.class.getDeclaredMethod("createFluidPicker", NoiseGeneratorSettings.class);
        fluidPicker.setAccessible(true);
        picker = (Aquifer.FluidPicker) fluidPicker.invoke(null, settings);
        beardifierMarker = Class.forName("net.minecraft.world.level.levelgen.DensityFunctions$BeardifierMarker").getEnumConstants()[0];
        var field = net.minecraft.world.level.chunk.ChunkGenerator.class.getDeclaredField("featuresPerStep");
        field.setAccessible(true);
        featuresPerStep = (List<?>) ((java.util.function.Supplier<?>) field.get(generator)).get();
        lookup.lookupOrThrow(Registries.PLACED_FEATURE).listElements()
            .forEach(h -> featureNames.put(h.value(), h.key().identifier().toString()));
        for (int step = 0; step < featuresPerStep.size(); step++) {
            List<PlacedFeature> features = stepFeatures(step);
            for (int i = 0; i < features.size(); i++) {
                out.printf("steps\t%s\t%d\t%d\t%s%n", dim, step, i, featureNames.get(features.get(i)));
            }
        }
    }

    static Method method(Class<?> c, String name) {
        Method m = java.util.Arrays.stream(c.getDeclaredMethods()).filter(x -> x.getName().equals(name)).findFirst().orElseThrow();
        m.setAccessible(true);
        return m;
    }

    @SuppressWarnings("unchecked")
    List<PlacedFeature> stepFeatures(int step) {
        return ((net.minecraft.world.level.biome.FeatureSorter.StepFeatureData) featuresPerStep.get(step)).features();
    }

    void run(long[] seeds, int[][] chunks) throws Exception {
        for (long seed : seeds) {
            RandomState state = RandomState.create(settings, lookup.lookupOrThrow(Registries.NOISE), seed);
            for (int[] c : chunks) {
                decorate(seed, state, c[0], c[1]);
            }
        }
    }

    // build makes the chunks around (cx, cz): biomes for the 5×5, then
    // the noise fill, surface and carvers for each of the middle 3×3
    // (x outer, z inner).
    ProtoChunk[][] build(long seed, RandomState state, int cx, int cz) throws Exception {
        ProtoChunk[][] grid = new ProtoChunk[5][5];
        for (int dx = -2; dx <= 2; dx++) {
            for (int dz = -2; dz <= 2; dz++) {
                ProtoChunk ch = new ProtoChunk(new ChunkPos(cx + dx, cz + dz), UpgradeData.EMPTY, height, containers, null);
                ch.getOrCreateNoiseChunk(cc -> {
                    try {
                        return (NoiseChunk) forChunk.invoke(null, cc, state, beardifierMarker, settings, picker, Blender.empty());
                    } catch (ReflectiveOperationException e) {
                        throw new RuntimeException(e);
                    }
                });
                doCreateBiomes.invoke(generator, Blender.empty(), state, null, ch);
                ch.setPersistedStatus(ChunkStatus.BIOMES);
                grid[dx + 2][dz + 2] = ch;
            }
        }
        var manager = new BiomeManager((qx, qy, qz) -> grid[(qx >> 2) - cx + 2][(qz >> 2) - cz + 2].getNoiseBiome(qx, qy, qz),
            BiomeManager.obfuscateSeed(seed));
        var sourceManager = manager.withDifferentSource((qx, qy, qz) -> biomeSource.getNoiseBiome(qx, qy, qz, state.sampler()));
        int cellHeight = settings.noiseSettings().getCellHeight();
        int cellMinY = Math.floorDiv(settings.noiseSettings().minY(), cellHeight);
        int cellCountY = Math.floorDiv(settings.noiseSettings().height(), cellHeight);
        for (int dx = -1; dx <= 1; dx++) {
            for (int dz = -1; dz <= 1; dz++) {
                ProtoChunk chunk = grid[dx + 2][dz + 2];
                doFill.invoke(generator, Blender.empty(), null, state, chunk, cellMinY, cellCountY);
                chunk.setPersistedStatus(ChunkStatus.NOISE);
                generator.buildSurface(chunk, new net.minecraft.world.level.levelgen.WorldGenerationContext(generator, height),
                    state, null, manager, Blender.empty(), biomeSource.possibleBiomes());
                chunk.setPersistedStatus(ChunkStatus.SURFACE);
                carve(seed, state, chunk, sourceManager);
                chunk.setPersistedStatus(ChunkStatus.CARVERS);
            }
        }
        return grid;
    }

    void carve(long seed, RandomState state, ProtoChunk chunk, BiomeManager sourceManager) {
        var noiseChunk = chunk.getOrCreateNoiseChunk(cc -> { throw new IllegalStateException(); });
        var carving = new net.minecraft.world.level.levelgen.carver.CarvingContext(generator, access,
            chunk.getHeightAccessorForGeneration(), noiseChunk, state, settings.surfaceRule());
        var aquifer = noiseChunk.aquifer();
        var mask = chunk.getOrCreateCarvingMask();
        var random = new WorldgenRandom(new net.minecraft.world.level.levelgen.LegacyRandomSource(0L));
        ChunkPos pos = chunk.getPos();
        for (int dx = -8; dx <= 8; dx++) {
            for (int dz = -8; dz <= 8; dz++) {
                ChunkPos start = new ChunkPos(pos.x() + dx, pos.z() + dz);
                var gen = generator.getBiomeGenerationSettings(biomeSource.getNoiseBiome(
                    QuartPos.fromBlock(start.getMinBlockX()), 0, QuartPos.fromBlock(start.getMinBlockZ()), state.sampler()));
                int index = 0;
                for (var holder : gen.getCarvers()) {
                    var carver = holder.value();
                    random.setLargeFeatureSeed(seed + index, start.x(), start.z());
                    if (carver.isStartChunk(random)) {
                        carver.carve(carving, chunk, sourceManager::getBiome, random, aquifer, start, mask);
                    }
                    index++;
                }
            }
        }
    }

    // decorate runs the middle chunk's features as the generator's
    // applyBiomeDecoration does and prints each placed feature's changes.
    void decorate(long seed, RandomState state, int cx, int cz) throws Exception {
        ProtoChunk[][] grid = build(seed, state, cx, cz);
        ProtoChunk center = grid[2][2];
        Heightmap.primeHeightmaps(center, EnumSet.of(Heightmap.Types.MOTION_BLOCKING, Heightmap.Types.MOTION_BLOCKING_NO_LEAVES,
            Heightmap.Types.OCEAN_FLOOR, Heightmap.Types.WORLD_SURFACE));
        WorldGenLevel level = region(seed, state, grid, cx, cz);
        BlockPos origin = net.minecraft.core.SectionPos.of(center.getPos(), level.getMinSectionY()).origin();
        var random = new WorldgenRandom(new net.minecraft.world.level.levelgen.XoroshiroRandomSource(0L));
        long decoration = random.setDecorationSeed(seed, origin.getX(), origin.getZ());
        var present = new java.util.LinkedHashSet<Holder<Biome>>();
        for (int dx = -1; dx <= 1; dx++) {
            for (int dz = -1; dz <= 1; dz++) {
                for (var section : grid[dx + 2][dz + 2].getSections()) {
                    section.getBiomes().getAll(present::add);
                }
            }
        }
        present.retainAll(biomeSource.possibleBiomes());
        for (int step = 0; step < featuresPerStep.size(); step++) {
            var data = (net.minecraft.world.level.biome.FeatureSorter.StepFeatureData) featuresPerStep.get(step);
            var indexes = new java.util.TreeSet<Integer>();
            for (var biome : present) {
                var lists = generator.getBiomeGenerationSettings(biome).features();
                if (step >= lists.size()) continue;
                final var d = data;
                lists.get(step).stream().map(Holder::value).forEach(f -> indexes.add(d.indexMapping().applyAsInt(f)));
            }
            for (int index : indexes) {
                PlacedFeature feature = data.features().get(index);
                BlockState[][] before = snapshot(grid);
                random.setFeatureSeed(decoration, index, step);
                // Reseeding keeps a cached second gaussian, so a feature's
                // draws depend on how many every earlier one took.
                String gaussian = cachedGaussian(random);
                String regionState = randomState(level.getRandom());
                String error = "";
                spawned.clear();
                try {
                    feature.placeWithBiomeCheck(level, generator, random, origin);
                } catch (RuntimeException e) {
                    error = e.getClass().getSimpleName() + (e.getMessage() == null ? "" : ":" + e.getMessage().replace('\t', ' ').replace(' ', '_'));
                }
                out.printf("feature\t%s\t%d\t%d,%d\t%d\t%d\t%s\t%s\t%s\t%s\t%s\t%s%n", dim, seed, cx, cz, step, index,
                    featureNames.get(feature), error.isEmpty() ? "ok" : error, delta(grid, before, cx, cz), gaussian, regionState,
                    spawned.isEmpty() ? "-" : String.join(" ", spawned));
            }
        }
    }

    // cachedGaussian is the random's pending second gaussian as raw
    // double bits in hex, or "-" when there is none.
    static String cachedGaussian(WorldgenRandom random) throws Exception {
        var source = net.minecraft.world.level.levelgen.LegacyRandomSource.class.getDeclaredField("gaussianSource");
        source.setAccessible(true);
        Object g = source.get(random);
        var have = g.getClass().getDeclaredField("haveNextNextGaussian");
        var next = g.getClass().getDeclaredField("nextNextGaussian");
        have.setAccessible(true);
        next.setAccessible(true);
        if (!have.getBoolean(g)) return "-";
        return Long.toHexString(Double.doubleToRawLongBits(next.getDouble(g)));
    }

    // randomState is the region random's state as a feature starts, so
    // a feature's draws from it do not depend on the ones before: the
    // Xoroshiro words or the legacy seed, then its cached gaussian.
    static String randomState(net.minecraft.util.RandomSource random) throws Exception {
        Object gaussianOwner = random;
        String words;
        if (random instanceof net.minecraft.world.level.levelgen.XoroshiroRandomSource) {
            var gen = net.minecraft.world.level.levelgen.XoroshiroRandomSource.class.getDeclaredField("randomNumberGenerator");
            gen.setAccessible(true);
            Object x = gen.get(random);
            var lo = x.getClass().getDeclaredField("seedLo");
            var hi = x.getClass().getDeclaredField("seedHi");
            lo.setAccessible(true);
            hi.setAccessible(true);
            words = "x:" + Long.toHexString(lo.getLong(x)) + ":" + Long.toHexString(hi.getLong(x));
        } else {
            var seed = net.minecraft.world.level.levelgen.LegacyRandomSource.class.getDeclaredField("seed");
            seed.setAccessible(true);
            words = "l:" + Long.toHexString(((java.util.concurrent.atomic.AtomicLong) seed.get(random)).get());
        }
        var source = gaussianOwner.getClass().getDeclaredField("gaussianSource");
        source.setAccessible(true);
        Object g = source.get(random);
        var have = g.getClass().getDeclaredField("haveNextNextGaussian");
        var next = g.getClass().getDeclaredField("nextNextGaussian");
        have.setAccessible(true);
        next.setAccessible(true);
        return words + ":" + (have.getBoolean(g) ? Long.toHexString(Double.doubleToRawLongBits(next.getDouble(g))) : "-");
    }

    BlockState[][] snapshot(ProtoChunk[][] grid) {
        BlockState[][] s = new BlockState[9][];
        BlockPos.MutableBlockPos p = new BlockPos.MutableBlockPos();
        for (int i = 0; i < 9; i++) {
            ProtoChunk ch = grid[i / 3 + 1][i % 3 + 1];
            BlockState[] states = new BlockState[16 * 16 * height.getHeight()];
            int minX = ch.getPos().getMinBlockX(), minZ = ch.getPos().getMinBlockZ();
            for (int y = 0; y < height.getHeight(); y++) {
                for (int z = 0; z < 16; z++) {
                    for (int x = 0; x < 16; x++) {
                        states[(y * 16 + z) * 16 + x] = ch.getBlockState(p.set(minX + x, height.getMinY() + y, minZ + z));
                    }
                }
            }
            s[i] = states;
        }
        return s;
    }

    // delta lists the blocks a feature changed, as x,y,z=state in world
    // coordinates, chunks x outer and z inner, blocks y, z, x.
    String delta(ProtoChunk[][] grid, BlockState[][] before, int cx, int cz) {
        BlockState[][] after = snapshot(grid);
        StringBuilder b = new StringBuilder();
        for (int i = 0; i < 9; i++) {
            ProtoChunk ch = grid[i / 3 + 1][i % 3 + 1];
            int minX = ch.getPos().getMinBlockX(), minZ = ch.getPos().getMinBlockZ();
            for (int j = 0; j < after[i].length; j++) {
                if (after[i][j] != before[i][j]) {
                    if (b.length() > 0) b.append(' ');
                    b.append(minX + (j & 15)).append(',').append(height.getMinY() + j / 256).append(',').append(minZ + (j >> 4 & 15))
                        .append('=').append(ChunkFillProbe.canonical(after[i][j]));
                }
            }
        }
        return b.length() == 0 ? "-" : b.toString();
    }

    Object serverLevel;

    // serverLevelStandIn is an uninitialised server level whose chunk
    // source answers getGenerator with this probe's generator, which is
    // all a feature reaches through WorldGenLevel.getLevel here.
    Object serverLevelStandIn() throws Exception {
        if (serverLevel != null) return serverLevel;
        var theUnsafe = sun.misc.Unsafe.class.getDeclaredField("theUnsafe");
        theUnsafe.setAccessible(true);
        var unsafe = (sun.misc.Unsafe) theUnsafe.get(null);
        Object level = unsafe.allocateInstance(net.minecraft.server.level.ServerLevel.class);
        Object cache = unsafe.allocateInstance(net.minecraft.server.level.ServerChunkCache.class);
        Object map = unsafe.allocateInstance(net.minecraft.server.level.ChunkMap.class);
        var ctor = net.minecraft.world.level.chunk.status.WorldGenContext.class.getDeclaredConstructors()[0];
        Object[] args = new Object[ctor.getParameterCount()];
        for (int i = 0; i < args.length; i++) {
            if (ctor.getParameterTypes()[i] == net.minecraft.world.level.chunk.ChunkGenerator.class) args[i] = generator;
        }
        ctor.setAccessible(true);
        setField(net.minecraft.server.level.ChunkMap.class, map, "worldGenContext", ctor.newInstance(args));
        setField(net.minecraft.server.level.ServerChunkCache.class, cache, "chunkMap", map);
        // A new entity takes the next id the chunk map does not track.
        setField(net.minecraft.server.level.ChunkMap.class, map, "entityMap", new it.unimi.dsi.fastutil.ints.Int2ObjectOpenHashMap<>());
        setField(net.minecraft.server.level.ServerLevel.class, level, "chunkSource", cache);
        // Entity types check the server's enabled feature flags before
        // creating an entity, as the End pillars' crystals do.
        Object server = unsafe.allocateInstance(net.minecraft.server.dedicated.DedicatedServer.class);
        Object worldData = java.lang.reflect.Proxy.newProxyInstance(FeatureProbe.class.getClassLoader(),
            new Class<?>[]{net.minecraft.world.level.storage.WorldData.class}, (proxy, m, a) -> switch (m.getName()) {
                case "enabledFeatures" -> net.minecraft.world.flag.FeatureFlags.DEFAULT_FLAGS;
                case "getDataConfiguration" -> net.minecraft.world.level.WorldDataConfiguration.DEFAULT;
                default -> m.isDefault() ? InvocationHandler.invokeDefault(proxy, m, a) : null;
            });
        setField(net.minecraft.server.MinecraftServer.class, server, "worldData", worldData);
        setField(net.minecraft.server.level.ServerLevel.class, level, "server", server);
        serverLevel = level;
        return level;
    }

    static void setField(Class<?> c, Object target, String name, Object value) throws Exception {
        var f = c.getDeclaredField(name);
        f.setAccessible(true);
        f.set(target, value);
    }

    // region is a WorldGenLevel over the grid that behaves as the
    // generation region of the middle chunk.
    WorldGenLevel region(long seed, RandomState state, ProtoChunk[][] grid, int cx, int cz) {
        var regionRandom = state.getOrCreateRandomFactory(net.minecraft.resources.Identifier.withDefaultNamespace("worldgen_region_random"))
            .at(grid[2][2].getPos().getWorldPosition());
        var ticks = (net.minecraft.world.ticks.LevelTickAccess<?>) Proxy.newProxyInstance(FeatureProbe.class.getClassLoader(),
            new Class<?>[]{net.minecraft.world.ticks.LevelTickAccess.class}, (p, m, a) -> {
                Class<?> r = m.getReturnType();
                if (r == boolean.class) return false;
                if (r == int.class) return 0;
                return null;
            });
        var border = new net.minecraft.world.level.border.WorldBorder();
        WorldGenLevel[] self = new WorldGenLevel[1];
        java.util.function.Function<BlockPos, ChunkAccess> chunkAt = pos -> {
            int gx = (pos.getX() >> 4) - cx + 2, gz = (pos.getZ() >> 4) - cz + 2;
            if (gx < 0 || gx > 4 || gz < 0 || gz > 4) throw new IllegalStateException("outside region: " + pos);
            return grid[gx][gz];
        };
        InvocationHandler h = (proxy, m, a) -> {
            switch (m.getName()) {
                case "getBlockState": return chunkAt.apply((BlockPos) a[0]).getBlockState((BlockPos) a[0]);
                case "getFluidState": return chunkAt.apply((BlockPos) a[0]).getFluidState((BlockPos) a[0]);
                case "isStateAtPosition":
                    return ((java.util.function.Predicate<BlockState>) a[1]).test(chunkAt.apply((BlockPos) a[0]).getBlockState((BlockPos) a[0]));
                case "isFluidAtPosition":
                    return ((java.util.function.Predicate<net.minecraft.world.level.material.FluidState>) a[1])
                        .test(chunkAt.apply((BlockPos) a[0]).getFluidState((BlockPos) a[0]));
                case "ensureCanWrite":
                case "isWithinWriteZone": {
                    BlockPos pos = (BlockPos) a[0];
                    return Math.abs((pos.getX() >> 4) - cx) <= 1 && Math.abs((pos.getZ() >> 4) - cz) <= 1;
                }
                case "setBlock": {
                    if (a.length == 3) return self[0].setBlock((BlockPos) a[0], (BlockState) a[1], (int) a[2], 512);
                    BlockPos pos = (BlockPos) a[0];
                    if (Math.abs((pos.getX() >> 4) - cx) > 1 || Math.abs((pos.getZ() >> 4) - cz) > 1) return false;
                    ChunkAccess ch = chunkAt.apply(pos);
                    BlockState st = (BlockState) a[1];
                    ch.setBlockState(pos, st, (int) a[2]);
                    if (st.hasBlockEntity()) {
                        var tag = new net.minecraft.nbt.CompoundTag();
                        tag.putInt("x", pos.getX());
                        tag.putInt("y", pos.getY());
                        tag.putInt("z", pos.getZ());
                        tag.putString("id", "DUMMY");
                        ch.setBlockEntityNbt(tag);
                    }
                    return true;
                }
                case "removeBlock":
                    return self[0].setBlock((BlockPos) a[0], Blocks.AIR.defaultBlockState(), 3, 512);
                case "destroyBlock": {
                    BlockPos pos = (BlockPos) a[0];
                    if (self[0].getBlockState(pos).isAir()) return false;
                    return self[0].setBlock(pos, Blocks.AIR.defaultBlockState(), 3, (int) a[3]);
                }
                case "getBlockEntity": {
                    if (a.length != 1) return InvocationHandler.invokeDefault(proxy, m, a);
                    BlockPos pos = (BlockPos) a[0];
                    ChunkAccess ch = chunkAt.apply(pos);
                    var be = ch.getBlockEntity(pos);
                    if (be != null) return be;
                    var tag = ch.getBlockEntityNbt(pos);
                    BlockState st = ch.getBlockState(pos);
                    if (tag != null && st.hasBlockEntity()) {
                        be = ((net.minecraft.world.level.block.EntityBlock) st.getBlock()).newBlockEntity(pos, st);
                        if (be != null) ch.setBlockEntity(be);
                        return be;
                    }
                    return null;
                }
                case "getHeight":
                    if (a == null || a.length == 0) return height.getHeight();
                    return chunkAt.apply(new BlockPos((int) a[1], 0, (int) a[2]))
                        .getHeight((Heightmap.Types) a[0], (int) a[1] & 15, (int) a[2] & 15) + 1;
                case "getHeightmapPos": {
                    BlockPos pos = (BlockPos) a[1];
                    return new BlockPos(pos.getX(), self[0].getHeight((Heightmap.Types) a[0], pos.getX(), pos.getZ()), pos.getZ());
                }
                // Nothing is lit while features generate: a column with
                // no light data reads sky light 15 and block light 0.
                case "getBrightness":
                    return a[0] == net.minecraft.world.level.LightLayer.SKY ? 15 : 0;
                case "getRawBrightness": return Math.max(0, 15 - (int) a[1]);
                case "getMinY": return height.getMinY();
                case "getSeed": return seed;
                case "getSeaLevel": return generator.getSeaLevel();
                case "getRandom": return regionRandom;
                case "getChunk":
                    if (a.length == 4) {
                        int gx = (int) a[0] - cx + 2, gz = (int) a[1] - cz + 2;
                        if (gx < 0 || gx > 4 || gz < 0 || gz > 4) {
                            if ((boolean) a[3]) throw new IllegalStateException("chunk outside region");
                            return null;
                        }
                        return grid[gx][gz];
                    }
                    return InvocationHandler.invokeDefault(proxy, m, a);
                case "hasChunk": {
                    int gx = (int) a[0] - cx + 2, gz = (int) a[1] - cz + 2;
                    return gx >= 0 && gx <= 4 && gz >= 0 && gz <= 4;
                }
                case "getChunkForCollisions": return self[0].getChunk((int) a[0], (int) a[1]);
                case "getUncachedNoiseBiome": return biomeSource.getNoiseBiome((int) a[0], (int) a[1], (int) a[2], state.sampler());
                case "getBiomeManager": return new BiomeManager(self[0], BiomeManager.obfuscateSeed(seed));
                // Features that place other configured features look them
                // up in the server's registries and take the generator
                // from the server level, as on a server.
                case "registryAccess": return lookup instanceof RegistryAccess full ? full : access;
                case "getLevel": return serverLevelStandIn();
                case "dimensionType": return dimensionType;
                case "enabledFeatures": return net.minecraft.world.flag.FeatureFlags.DEFAULT_FLAGS;
                case "getWorldBorder": return border;
                case "getBlockTicks":
                case "getFluidTicks": return ticks;
                case "createTick": return null;
                case "nextSubTickCount": return 0L;
                case "isClientSide": return false;
                case "getSkyDarken": return 0;
                case "players":
                case "getEntities":
                case "getEntityCollisions": return List.of();
                case "addParticle":
                case "gameEvent":
                case "levelEvent":
                case "playSound":
                case "setCurrentlyGenerating": return null;
                case "addFreshEntity": {
                    var e = (net.minecraft.world.entity.Entity) a[0];
                    String beam = "-";
                    if (e instanceof net.minecraft.world.entity.boss.enderdragon.EndCrystal c && c.getBeamTarget() != null) {
                        beam = c.getBeamTarget().getX() + "," + c.getBeamTarget().getY() + "," + c.getBeamTarget().getZ();
                    }
                    spawned.add(String.format("%s@%016x,%016x,%016x,%08x,%b,%s",
                        net.minecraft.core.registries.BuiltInRegistries.ENTITY_TYPE.getKey(e.getType()),
                        Double.doubleToRawLongBits(e.getX()), Double.doubleToRawLongBits(e.getY()), Double.doubleToRawLongBits(e.getZ()),
                        Float.floatToRawIntBits(e.getYRot()), e.isInvulnerable(), beam));
                    return true;
                }
                case "getServer":
                case "getCurrentDifficultyAt":
                case "getChunkSource":
                case "getLevelData":
                case "environmentAttributes":
                    throw new UnsupportedOperationException(m.getName());
                case "hashCode": return System.identityHashCode(proxy);
                case "equals": return proxy == a[0];
                case "toString": return "FeatureProbeRegion";
            }
            if (m.isDefault()) return InvocationHandler.invokeDefault(proxy, m, a);
            throw new UnsupportedOperationException(m.getName());
        };
        self[0] = (WorldGenLevel) Proxy.newProxyInstance(FeatureProbe.class.getClassLoader(), new Class<?>[]{WorldGenLevel.class}, h);
        return self[0];
    }
}
