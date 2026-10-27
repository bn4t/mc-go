import java.io.PrintWriter;
import java.lang.reflect.Method;
import java.util.List;
import com.mojang.serialization.Lifecycle;
import net.minecraft.SharedConstants;
import net.minecraft.core.BlockPos;
import net.minecraft.core.MappedRegistry;
import net.minecraft.core.RegistrationInfo;
import net.minecraft.core.RegistryAccess;
import net.minecraft.core.registries.Registries;
import net.minecraft.data.registries.VanillaRegistries;
import net.minecraft.world.level.ChunkPos;
import net.minecraft.world.level.LevelHeightAccessor;
import net.minecraft.world.level.biome.Biome;
import net.minecraft.world.level.chunk.PalettedContainerFactory;
import net.minecraft.world.level.chunk.ProtoChunk;
import net.minecraft.world.level.chunk.UpgradeData;
import net.minecraft.world.level.levelgen.NoiseBasedChunkGenerator;
import net.minecraft.world.level.levelgen.NoiseChunk;
import net.minecraft.world.level.levelgen.NoiseGeneratorSettings;
import net.minecraft.world.level.levelgen.RandomState;
import net.minecraft.world.level.levelgen.Aquifer;
import net.minecraft.world.level.levelgen.blending.Blender;

// Has the official server's noise-based generator run its first stages
// on standalone proto chunks — biomes, the noise fill (terrain density,
// aquifers, ore veins), the surface (surface rules and the badlands
// and iceberg column extensions) and the carvers, but no features — and
// records each column after the fill, the surface and the carvers as
// runs of block states (properties sorted). The chunk's NoiseChunk is created up front with
// the beardifier marker, as a chunk with no structures gets, so the
// generator's own fill needs no structure manager. Only public entry
// points are called, plus reflective calls of the generator's own fill
// and fluid-picker methods. Block tags are bound from the vanilla data
// pack before the registries are looked up.
public class ChunkFillProbe {
    public static void main(String[] args) throws Exception {
        SharedConstants.tryDetectVersion();
        net.minecraft.server.Bootstrap.bootStrap();
        // Bind the built-in registries' tags from the vanilla data pack,
        // as a starting server does: carvers replace by block tag.
        var resources = new net.minecraft.server.packs.resources.MultiPackResourceManager(
            net.minecraft.server.packs.PackType.SERVER_DATA,
            List.of(net.minecraft.server.packs.repository.ServerPacksSource.createVanillaPackSource()));
        net.minecraft.tags.TagLoader.loadTagsForExistingRegistries(resources,
            RegistryAccess.fromRegistryOfRegistries(net.minecraft.core.registries.BuiltInRegistries.REGISTRY))
            .forEach(net.minecraft.core.Registry.PendingTags::apply);
        var lookup = VanillaRegistries.createLookup();
        var biomes = lookup.lookupOrThrow(Registries.BIOME);
        MappedRegistry<Biome> biomeRegistry = new MappedRegistry<>(Registries.BIOME, Lifecycle.stable());
        biomes.listElements().forEach(h -> biomeRegistry.register(h.key(), h.value(), RegistrationInfo.BUILT_IN));
        biomeRegistry.freeze();
        RegistryAccess access = new RegistryAccess.ImmutableRegistryAccess(List.of(biomeRegistry));
        PalettedContainerFactory containers = PalettedContainerFactory.create(access);

        var paramLists = lookup.lookupOrThrow(Registries.MULTI_NOISE_BIOME_SOURCE_PARAMETER_LIST);
        try (PrintWriter out = new PrintWriter(args[0], java.nio.charset.StandardCharsets.UTF_8)) {
            run(out, "", lookup, containers, NoiseGeneratorSettings.OVERWORLD,
                net.minecraft.world.level.biome.MultiNoiseBiomeSource.createFromPreset(
                    paramLists.getOrThrow(net.minecraft.world.level.biome.MultiNoiseBiomeSourceParameterLists.OVERWORLD)),
                -64, 384, new long[]{0L, 12345L, -4172144997902289642L},
                new int[][]{{0, 0}, {-1, 3}, {10, -7}, {-37, -52}, {125, 88}, {-300, 412}, {4, -1}, {61, 61}}, access, true);
            run(out, ".nether", lookup, containers, NoiseGeneratorSettings.NETHER,
                net.minecraft.world.level.biome.MultiNoiseBiomeSource.createFromPreset(
                    paramLists.getOrThrow(net.minecraft.world.level.biome.MultiNoiseBiomeSourceParameterLists.NETHER)),
                0, 256, new long[]{0L, 12345L},
                new int[][]{{0, 0}, {-3, 5}, {20, -11}, {-40, 33}, {7, 7}, {100, -60}}, access, true);
            run(out, ".end", lookup, containers, NoiseGeneratorSettings.END,
                net.minecraft.world.level.biome.TheEndBiomeSource.create(lookup.lookupOrThrow(Registries.BIOME)),
                0, 256, new long[]{0L, 12345L},
                new int[][]{{0, 0}, {5, -3}, {70, 10}, {-90, 40}, {120, -130}, {-200, -200}}, access, false);
        }
    }

    // run generates one dimension's probe chunks: for each chunk, biomes
    // for its 3x3 neighbourhood, then the noise fill and the surface.
    static void run(PrintWriter out, String tag, net.minecraft.core.HolderLookup.Provider lookup, PalettedContainerFactory containers,
            net.minecraft.resources.ResourceKey<NoiseGeneratorSettings> settingsKey, net.minecraft.world.level.biome.BiomeSource biomeSource,
            int heightMinY, int heightTotal, long[] seeds, int[][] chunks, RegistryAccess access, boolean carve) throws Exception {
        var settingsHolder = lookup.lookupOrThrow(Registries.NOISE_SETTINGS).getOrThrow(settingsKey);
        NoiseGeneratorSettings settings = settingsHolder.value();
        var generator = new NoiseBasedChunkGenerator(biomeSource, settingsHolder);
        Method doCreateBiomes = java.util.Arrays.stream(NoiseBasedChunkGenerator.class.getDeclaredMethods())
            .filter(m -> m.getName().equals("doCreateBiomes")).findFirst().orElseThrow();
        doCreateBiomes.setAccessible(true);
        Method fluidPicker = NoiseBasedChunkGenerator.class.getDeclaredMethod("createFluidPicker", NoiseGeneratorSettings.class);
        fluidPicker.setAccessible(true);
        Aquifer.FluidPicker picker = (Aquifer.FluidPicker) fluidPicker.invoke(null, settings);
        Method doFill = null;
        for (Method m : NoiseBasedChunkGenerator.class.getDeclaredMethods()) {
            if (m.getName().equals("doFill")) doFill = m;
        }
        doFill.setAccessible(true);
        // The beardifier marker (the no-structure beardifier) is package
        // private, so it and forChunk are reached reflectively.
        Class<?> markerClass = Class.forName("net.minecraft.world.level.levelgen.DensityFunctions$BeardifierMarker");
        Object beardifierMarker = markerClass.getEnumConstants()[0];
        Method forChunk = java.util.Arrays.stream(NoiseChunk.class.getDeclaredMethods())
            .filter(m -> m.getName().equals("forChunk")).findFirst().orElseThrow();
        forChunk.setAccessible(true);
        LevelHeightAccessor height = LevelHeightAccessor.create(heightMinY, heightTotal);
        int cellHeight = settings.noiseSettings().getCellHeight();
        int cellMinY = Math.floorDiv(settings.noiseSettings().minY(), cellHeight);
        int cellCountY = Math.floorDiv(settings.noiseSettings().height(), cellHeight);

        {
            for (long seed : seeds) {
                RandomState state = RandomState.create(settings, lookup.lookupOrThrow(Registries.NOISE), seed);
                for (int[] c : chunks) {
                    // Biomes for the chunk and its eight neighbours, in
                    // a fixed order (x outer, z inner): the surface reads
                    // them from these chunks' storage, as a generation
                    // region does, and vanilla's climate search carries
                    // its previous answer from one lookup to the next.
                    ProtoChunk[][] around = new ProtoChunk[3][3];
                    for (int dx = -1; dx <= 1; dx++) {
                        for (int dz = -1; dz <= 1; dz++) {
                            ProtoChunk ch = new ProtoChunk(new ChunkPos(c[0] + dx, c[1] + dz), UpgradeData.EMPTY, height, containers, null);
                            ch.getOrCreateNoiseChunk(cc -> {
                                try {
                                    return (NoiseChunk) forChunk.invoke(null, cc, state, beardifierMarker, settings, picker, Blender.empty());
                                } catch (ReflectiveOperationException e) {
                                    throw new RuntimeException(e);
                                }
                            });
                            doCreateBiomes.invoke(generator, Blender.empty(), state, null, ch);
                            ch.setPersistedStatus(net.minecraft.world.level.chunk.status.ChunkStatus.BIOMES);
                            around[dx + 1][dz + 1] = ch;
                        }
                    }
                    ProtoChunk chunk = around[1][1];
                    doFill.invoke(generator, Blender.empty(), null, state, chunk, cellMinY, cellCountY);
                    chunk.setPersistedStatus(net.minecraft.world.level.chunk.status.ChunkStatus.NOISE);
                    dump(out, "fill" + tag, seed, c, chunk, height);
                    final int cx = c[0], cz = c[1];
                    var manager = new net.minecraft.world.level.biome.BiomeManager(
                        (qx, qy, qz) -> around[(qx >> 2) - cx + 1][(qz >> 2) - cz + 1].getNoiseBiome(qx, qy, qz),
                        net.minecraft.world.level.biome.BiomeManager.obfuscateSeed(seed));
                    generator.buildSurface(chunk, new net.minecraft.world.level.levelgen.WorldGenerationContext(generator, height),
                        state, null, manager, Blender.empty(), biomeSource.possibleBiomes());
                    dump(out, "surface" + tag, seed, c, chunk, height);
                    if (!carve) continue;
                    // The carvers stage as the generator's applyCarvers
                    // runs it: every chunk within 8 may start its
                    // biome's carvers (biome sampled from the source at
                    // the chunk's corner quart, y 0), each seeded from
                    // the world seed plus its index; carving reads block
                    // biomes zoomed straight from the biome source.
                    var noiseChunk = chunk.getOrCreateNoiseChunk(cc -> { throw new IllegalStateException(); });
                    var carving = new net.minecraft.world.level.levelgen.carver.CarvingContext(generator, access,
                        chunk.getHeightAccessorForGeneration(), noiseChunk, state, settings.surfaceRule());
                    var sourceManager = manager.withDifferentSource((qx, qy, qz) -> biomeSource.getNoiseBiome(qx, qy, qz, state.sampler()));
                    var aquifer = noiseChunk.aquifer();
                    var mask = chunk.getOrCreateCarvingMask();
                    var random = new net.minecraft.world.level.levelgen.WorldgenRandom(
                        new net.minecraft.world.level.levelgen.LegacyRandomSource(0L));
                    for (int dx = -8; dx <= 8; dx++) {
                        for (int dz = -8; dz <= 8; dz++) {
                            ChunkPos start = new ChunkPos(c[0] + dx, c[1] + dz);
                            var gen = generator.getBiomeGenerationSettings(biomeSource.getNoiseBiome(
                                net.minecraft.core.QuartPos.fromBlock(start.getMinBlockX()), 0,
                                net.minecraft.core.QuartPos.fromBlock(start.getMinBlockZ()), state.sampler()));
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
                    dump(out, "carve" + tag, seed, c, chunk, height);
                }
            }
        }
    }

    // canonical renders a state as name[k=v,...] with sorted properties.
    static String canonical(net.minecraft.world.level.block.state.BlockState st) {
        String name = net.minecraft.core.registries.BuiltInRegistries.BLOCK.getKey(st.getBlock()).toString();
        var props = new java.util.TreeMap<String, String>();
        for (var prop : st.getProperties()) {
            props.put(prop.getName(), propValue(st, prop));
        }
        if (props.isEmpty()) return name;
        StringBuilder b = new StringBuilder(name).append('[');
        boolean first = true;
        for (var e : props.entrySet()) {
            if (!first) b.append(',');
            b.append(e.getKey()).append('=').append(e.getValue());
            first = false;
        }
        return b.append(']').toString();
    }

    static <T extends Comparable<T>> String propValue(net.minecraft.world.level.block.state.BlockState st,
            net.minecraft.world.level.block.state.properties.Property<T> prop) {
        return prop.getName(st.getValue(prop));
    }

    static void dump(PrintWriter out, String kind, long seed, int[] c, ProtoChunk chunk, LevelHeightAccessor height) {
        ChunkPos pos = chunk.getPos();
        BlockPos.MutableBlockPos p = new BlockPos.MutableBlockPos();
        for (int x = 0; x < 16; x++) {
            for (int z = 0; z < 16; z++) {
                StringBuilder col = new StringBuilder();
                String last = null;
                int runStart = 0;
                int minY = height.getMinY(), maxY = height.getMaxY();
                for (int y = minY; y <= maxY + 1; y++) {
                    String s = y <= maxY ? canonical(chunk.getBlockState(p.set(pos.getMinBlockX() + x, y, pos.getMinBlockZ() + z))) : null;
                    if (last != null && !last.equals(s)) {
                        col.append(runStart).append('-').append(y - 1).append('=').append(last).append(' ');
                    }
                    if (s != null && !s.equals(last)) runStart = y;
                    last = s;
                }
                out.printf("%s\t%d\t%d,%d\t%d,%d\t%s%n", kind, seed, c[0], c[1], x, z, col.toString().trim());
            }
        }
    }
}
