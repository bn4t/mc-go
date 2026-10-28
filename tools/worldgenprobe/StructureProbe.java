import java.io.PrintWriter;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.TreeMap;
import com.mojang.serialization.Lifecycle;
import net.minecraft.SharedConstants;
import net.minecraft.core.Holder;
import net.minecraft.core.HolderLookup;
import net.minecraft.core.MappedRegistry;
import net.minecraft.core.RegistrationInfo;
import net.minecraft.core.RegistryAccess;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.core.registries.Registries;
import net.minecraft.nbt.CompoundTag;
import net.minecraft.nbt.ListTag;
import net.minecraft.nbt.Tag;
import net.minecraft.resources.ResourceKey;
import net.minecraft.world.level.ChunkPos;
import net.minecraft.world.level.Level;
import net.minecraft.world.level.LevelHeightAccessor;
import net.minecraft.world.level.biome.Biome;
import net.minecraft.world.level.biome.BiomeSource;
import net.minecraft.world.level.chunk.ChunkGeneratorStructureState;
import net.minecraft.world.level.chunk.PalettedContainerFactory;
import net.minecraft.world.level.chunk.ProtoChunk;
import net.minecraft.world.level.chunk.UpgradeData;
import net.minecraft.world.level.levelgen.Heightmap;
import net.minecraft.world.level.levelgen.NoiseBasedChunkGenerator;
import net.minecraft.world.level.levelgen.NoiseGeneratorSettings;
import net.minecraft.world.level.levelgen.RandomState;
import net.minecraft.world.level.levelgen.WorldOptions;
import net.minecraft.world.level.levelgen.structure.Structure;
import net.minecraft.world.level.levelgen.structure.StructureSet;
import net.minecraft.world.level.levelgen.structure.StructureStart;
import net.minecraft.world.level.levelgen.structure.pieces.StructurePieceSerializationContext;
import net.minecraft.world.level.levelgen.structure.placement.ConcentricRingsStructurePlacement;
import net.minecraft.world.level.levelgen.structure.templatesystem.StructureTemplateManager;

// Has the official server's generator place structures for one seed in
// each dimension: which structure sets the biome source can hold, which
// chunks of a square each set's placement picks, the stronghold rings,
// the noise-only base heights and columns the structures measure the
// ground with, and the structure starts the structure-starts stage builds
// on bare proto chunks, each as the tag a chunk saves it under (keys
// sorted). Only public entry points are called.
public class StructureProbe {
    public static void main(String[] args) throws Exception {
        SharedConstants.tryDetectVersion();
        net.minecraft.server.Bootstrap.bootStrap();
        var resources = new net.minecraft.server.packs.resources.MultiPackResourceManager(
            net.minecraft.server.packs.PackType.SERVER_DATA,
            List.of(net.minecraft.server.packs.repository.ServerPacksSource.createVanillaPackSource()));
        var layers = net.minecraft.server.RegistryLayer.createRegistryAccess();
        var pendingTags = net.minecraft.tags.TagLoader.loadTagsForExistingRegistries(resources,
            layers.getLayer(net.minecraft.server.RegistryLayer.STATIC));
        var context = net.minecraft.tags.TagLoader.buildUpdatedLookups(
            layers.getAccessForLoading(net.minecraft.server.RegistryLayer.WORLDGEN), pendingTags);
        RegistryAccess.Frozen lookup = net.minecraft.resources.RegistryDataLoader.load(resources, context,
            net.minecraft.resources.RegistryDataLoader.WORLDGEN_REGISTRIES, Runnable::run).join();
        pendingTags.forEach(net.minecraft.core.Registry.PendingTags::apply);
        // Structure pieces look up blocks and other static registries too.
        lookup = layers.replaceFrom(net.minecraft.server.RegistryLayer.WORLDGEN, lookup).compositeAccess();
        var biomes = lookup.lookupOrThrow(Registries.BIOME);
        MappedRegistry<Biome> biomeRegistry = new MappedRegistry<>(Registries.BIOME, Lifecycle.stable());
        biomes.listElements().forEach(h -> biomeRegistry.register(h.key(), h.value(), RegistrationInfo.BUILT_IN));
        biomeRegistry.freeze();
        PalettedContainerFactory containers = PalettedContainerFactory.create(
            new RegistryAccess.ImmutableRegistryAccess(List.of(biomeRegistry)));
        var temp = java.nio.file.Files.createTempDirectory("mcgo-structure-probe-");
        var storage = net.minecraft.world.level.storage.LevelStorageSource.createDefault(temp).createAccess("probe");
        var templates = new StructureTemplateManager(resources, storage, net.minecraft.util.datafix.DataFixers.getDataFixer(),
            BuiltInRegistries.BLOCK);
        var serialization = new StructurePieceSerializationContext(resources, lookup, templates);
        var paramLists = lookup.lookupOrThrow(Registries.MULTI_NOISE_BIOME_SOURCE_PARAMETER_LIST);
        try (PrintWriter out = new PrintWriter(args[0], java.nio.charset.StandardCharsets.UTF_8)) {
            StructureProbe overworld = new StructureProbe(out, "overworld", lookup, containers, templates, serialization,
                NoiseGeneratorSettings.OVERWORLD, Level.OVERWORLD,
                net.minecraft.world.level.biome.MultiNoiseBiomeSource.createFromPreset(
                    paramLists.getOrThrow(net.minecraft.world.level.biome.MultiNoiseBiomeSourceParameterLists.OVERWORLD)),
                -64, 384, 12345L);
            overworld.run(160);
            StructureProbe nether = new StructureProbe(out, "the_nether", lookup, containers, templates, serialization,
                NoiseGeneratorSettings.NETHER, Level.NETHER,
                net.minecraft.world.level.biome.MultiNoiseBiomeSource.createFromPreset(
                    paramLists.getOrThrow(net.minecraft.world.level.biome.MultiNoiseBiomeSourceParameterLists.NETHER)),
                0, 256, 12345L);
            nether.run(64);
            StructureProbe end = new StructureProbe(out, "the_end", lookup, containers, templates, serialization,
                NoiseGeneratorSettings.END, Level.END,
                net.minecraft.world.level.biome.TheEndBiomeSource.create(lookup.lookupOrThrow(Registries.BIOME)),
                0, 256, 12345L);
            end.run(160);
        }
    }

    final PrintWriter out;
    final String dim;
    final RegistryAccess.Frozen lookup;
    final PalettedContainerFactory containers;
    final StructureTemplateManager templates;
    final StructurePieceSerializationContext serialization;
    final ResourceKey<Level> level;
    final BiomeSource biomeSource;
    final NoiseBasedChunkGenerator generator;
    final LevelHeightAccessor height;
    final long seed;
    final RandomState state;
    final ChunkGeneratorStructureState structures;
    final net.minecraft.world.level.StructureManager manager;

    StructureProbe(PrintWriter out, String dim, RegistryAccess.Frozen lookup, PalettedContainerFactory containers,
            StructureTemplateManager templates, StructurePieceSerializationContext serialization,
            ResourceKey<NoiseGeneratorSettings> settingsKey, ResourceKey<Level> level, BiomeSource biomeSource,
            int minY, int totalHeight, long seed) {
        this.out = out;
        this.dim = dim;
        this.lookup = lookup;
        this.containers = containers;
        this.templates = templates;
        this.serialization = serialization;
        this.level = level;
        this.biomeSource = biomeSource;
        var settingsHolder = lookup.lookupOrThrow(Registries.NOISE_SETTINGS).getOrThrow(settingsKey);
        this.generator = new NoiseBasedChunkGenerator(biomeSource, settingsHolder);
        this.height = LevelHeightAccessor.create(minY, totalHeight);
        this.seed = seed;
        this.state = RandomState.create(settingsHolder.value(), lookup.lookupOrThrow(Registries.NOISE), seed);
        this.structures = ChunkGeneratorStructureState.createForNormal(state, seed, biomeSource,
            lookup.lookupOrThrow(Registries.STRUCTURE_SET));
        // The structure manager only reads and writes the chunk it is
        // handed while structure starts are made.
        this.manager = new net.minecraft.world.level.StructureManager(null, new WorldOptions(seed, true, false), null);
    }

    String setName(Holder<StructureSet> set) {
        return set.unwrapKey().orElseThrow().identifier().toString();
    }

    // run records the dimension's structure sets, the chunks each picks in
    // the square of the given chunk radius, the stronghold rings, base
    // heights, and structure starts.
    void run(int radius) throws Exception {
        structures.ensureStructuresGenerated();
        for (var set : structures.possibleStructureSets()) {
            out.printf("set\t%s\t%s%n", dim, setName(set));
            if (set.value().placement() instanceof ConcentricRingsStructurePlacement rings) {
                var positions = structures.getRingPositionsFor(rings);
                for (int i = 0; i < positions.size(); i++) {
                    out.printf("ring\t%s\t%s\t%d\t%d\t%d%n", dim, setName(set), i, positions.get(i).x(), positions.get(i).z());
                }
            }
        }
        for (var set : structures.possibleStructureSets()) {
            var placement = set.value().placement();
            for (int cx = -radius; cx < radius; cx++) {
                for (int cz = -radius; cz < radius; cz++) {
                    if (placement.isStructureChunk(structures, cx, cz)) {
                        out.printf("chunk\t%s\t%s\t%d\t%d%n", dim, setName(set), cx, cz);
                    }
                }
            }
        }
        var random = new java.util.Random(seed ^ dim.hashCode());
        for (int i = 0; i < 400; i++) {
            int x = random.nextInt(4096) - 2048, z = random.nextInt(4096) - 2048;
            out.printf("height\t%s\t%d\t%d\t%d\t%d%n", dim, x, z,
                generator.getBaseHeight(x, z, Heightmap.Types.WORLD_SURFACE_WG, height, state),
                generator.getBaseHeight(x, z, Heightmap.Types.OCEAN_FLOOR_WG, height, state));
        }
        for (int i = 0; i < 40; i++) {
            int x = random.nextInt(4096) - 2048, z = random.nextInt(4096) - 2048;
            var column = generator.getBaseColumn(x, z, height, state);
            var runs = new StringBuilder();
            String last = null;
            int count = 0;
            for (int y = height.getMinY(); y < height.getMaxY() + 1; y++) {
                String name = BuiltInRegistries.BLOCK.getKey(column.getBlock(y).getBlock()).getPath();
                if (name.equals(last)) {
                    count++;
                    continue;
                }
                if (last != null) runs.append(last).append('*').append(count).append(' ');
                last = name;
                count = 1;
            }
            runs.append(last).append('*').append(count);
            out.printf("column\t%s\t%d\t%d\t%s%n", dim, x, z, runs);
        }
        // Each set's structure chunks are run nearest first, out to twice
        // the square, until each of its structures has started `cap`
        // times (the huge trial chambers fewer), the stronghold rings'
        // first positions included.
        var probed = new java.util.HashSet<Long>();
        var counts = new java.util.HashMap<String, Integer>();
        for (var set : structures.possibleStructureSets()) {
            var placement = set.value().placement();
            List<ChunkPos> candidates = new ArrayList<>();
            if (placement instanceof ConcentricRingsStructurePlacement rings) {
                var positions = structures.getRingPositionsFor(rings);
                candidates.addAll(positions.subList(0, Math.min(12, positions.size())));
            } else {
                // The sparsest sets (woodland mansions) are searched further.
                int reach = ((net.minecraft.world.level.levelgen.structure.placement.RandomSpreadStructurePlacement) placement).spacing() >= 64
                    ? 6 * radius : 2 * radius;
                for (int r = 0; r <= reach; r++) {
                    for (int cx = -r; cx <= r; cx++) {
                        for (int cz = -r; cz <= r; cz++) {
                            if (Math.max(Math.abs(cx), Math.abs(cz)) == r && placement.isStructureChunk(structures, cx, cz)) {
                                candidates.add(new ChunkPos(cx, cz));
                            }
                        }
                    }
                }
            }
            int setStarts = 0;
            for (ChunkPos pos : candidates) {
                boolean need = false;
                for (var entry : set.value().structures()) {
                    String name = entry.structure().unwrapKey().orElseThrow().identifier().toString();
                    int cap = name.equals("minecraft:trial_chambers") ? 8 : name.startsWith("minecraft:mineshaft") ? 12 : 24;
                    if (counts.getOrDefault(name, 0) < cap && setStarts < 4 * cap) need = true;
                }
                if (!need) break;
                if (!probed.add(ChunkPos.pack(pos.x(), pos.z()))) continue;
                out.printf("probed\t%s\t%d\t%d%n", dim, pos.x(), pos.z());
                ProtoChunk chunk = new ProtoChunk(pos, UpgradeData.EMPTY, height, containers, null);
                generator.createStructures(lookup, structures, manager, chunk, templates, level);
                var starts = new TreeMap<String, StructureStart>();
                for (Map.Entry<Structure, StructureStart> e : chunk.getAllStarts().entrySet()) {
                    starts.put(lookup.lookupOrThrow(Registries.STRUCTURE).getKey(e.getKey()).toString(), e.getValue());
                }
                for (var e : starts.entrySet()) {
                    counts.merge(e.getKey(), 1, Integer::sum);
                    setStarts++;
                    out.printf("start\t%s\t%d\t%d\t%s\t%s%n", dim, pos.x(), pos.z(), e.getKey(),
                        canon(e.getValue().createTag(serialization, pos)));
                }
            }
        }
    }

    // canon writes a tag as SNBT with compound keys sorted, so the same
    // tag always reads the same.
    static String canon(Tag tag) {
        if (tag instanceof CompoundTag c) {
            var keys = new ArrayList<>(c.keySet());
            java.util.Collections.sort(keys);
            var b = new StringBuilder("{");
            for (int i = 0; i < keys.size(); i++) {
                if (i > 0) b.append(',');
                b.append(keys.get(i)).append(':').append(canon(c.get(keys.get(i))));
            }
            return b.append('}').toString();
        }
        if (tag instanceof ListTag l) {
            var b = new StringBuilder("[");
            for (int i = 0; i < l.size(); i++) {
                if (i > 0) b.append(',');
                b.append(canon(l.get(i)));
            }
            return b.append(']').toString();
        }
        return tag.toString();
    }
}
