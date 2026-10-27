import java.io.PrintWriter;
import java.lang.reflect.Method;
import java.util.List;
import com.mojang.serialization.Lifecycle;
import net.minecraft.SharedConstants;
import net.minecraft.commands.arguments.blocks.BlockStateParser;
import net.minecraft.core.BlockPos;
import net.minecraft.core.MappedRegistry;
import net.minecraft.core.RegistrationInfo;
import net.minecraft.core.RegistryAccess;
import net.minecraft.core.registries.Registries;
import net.minecraft.data.registries.VanillaRegistries;
import net.minecraft.world.level.ChunkPos;
import net.minecraft.world.level.LevelHeightAccessor;
import net.minecraft.world.level.biome.Biome;
import net.minecraft.world.level.biome.Biomes;
import net.minecraft.world.level.biome.FixedBiomeSource;
import net.minecraft.world.level.chunk.PalettedContainerFactory;
import net.minecraft.world.level.chunk.ProtoChunk;
import net.minecraft.world.level.chunk.UpgradeData;
import net.minecraft.world.level.levelgen.NoiseBasedChunkGenerator;
import net.minecraft.world.level.levelgen.NoiseChunk;
import net.minecraft.world.level.levelgen.NoiseGeneratorSettings;
import net.minecraft.world.level.levelgen.RandomState;
import net.minecraft.world.level.levelgen.Aquifer;
import net.minecraft.world.level.levelgen.blending.Blender;

// Has the official server's noise-based generator fill standalone proto
// chunks (the noise stage only: terrain density, aquifers, ore veins —
// no biomes, surface, carvers or features) and records each column as
// runs of block states. The chunk's NoiseChunk is created up front with
// the beardifier marker, as a chunk with no structures gets, so the
// generator's own fill needs no structure manager. Only public entry
// points are called, plus reflective calls of the generator's own fill
// and fluid-picker methods.
public class ChunkFillProbe {
    public static void main(String[] args) throws Exception {
        SharedConstants.tryDetectVersion();
        net.minecraft.server.Bootstrap.bootStrap();
        var lookup = VanillaRegistries.createLookup();
        var biomes = lookup.lookupOrThrow(Registries.BIOME);
        MappedRegistry<Biome> biomeRegistry = new MappedRegistry<>(Registries.BIOME, Lifecycle.stable());
        biomes.listElements().forEach(h -> biomeRegistry.register(h.key(), h.value(), RegistrationInfo.BUILT_IN));
        biomeRegistry.freeze();
        RegistryAccess access = new RegistryAccess.ImmutableRegistryAccess(List.of(biomeRegistry));
        PalettedContainerFactory containers = PalettedContainerFactory.create(access);

        var settingsHolder = lookup.lookupOrThrow(Registries.NOISE_SETTINGS).getOrThrow(NoiseGeneratorSettings.OVERWORLD);
        NoiseGeneratorSettings settings = settingsHolder.value();
        var generator = new NoiseBasedChunkGenerator(new FixedBiomeSource(biomeRegistry.getOrThrow(Biomes.PLAINS)), settingsHolder);
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
        LevelHeightAccessor height = LevelHeightAccessor.create(settings.noiseSettings().minY(), settings.noiseSettings().height());
        int cellHeight = settings.noiseSettings().getCellHeight();
        int cellMinY = Math.floorDiv(settings.noiseSettings().minY(), cellHeight);
        int cellCountY = Math.floorDiv(settings.noiseSettings().height(), cellHeight);

        long[] seeds = {0L, 12345L, -4172144997902289642L};
        int[][] chunks = {{0, 0}, {-1, 3}, {10, -7}, {-37, -52}, {125, 88}, {-300, 412}, {4, -1}, {61, 61}};
        try (PrintWriter out = new PrintWriter(args[0], java.nio.charset.StandardCharsets.UTF_8)) {
            for (long seed : seeds) {
                RandomState state = RandomState.create(settings, lookup.lookupOrThrow(Registries.NOISE), seed);
                for (int[] c : chunks) {
                    ChunkPos pos = new ChunkPos(c[0], c[1]);
                    ProtoChunk chunk = new ProtoChunk(pos, UpgradeData.EMPTY, height, containers, null);
                    chunk.getOrCreateNoiseChunk(ch -> {
                        try {
                            return (NoiseChunk) forChunk.invoke(null, ch, state, beardifierMarker, settings, picker, Blender.empty());
                        } catch (ReflectiveOperationException e) {
                            throw new RuntimeException(e);
                        }
                    });
                    doFill.invoke(generator, Blender.empty(), null, state, chunk, cellMinY, cellCountY);
                    BlockPos.MutableBlockPos p = new BlockPos.MutableBlockPos();
                    for (int x = 0; x < 16; x++) {
                        for (int z = 0; z < 16; z++) {
                            StringBuilder col = new StringBuilder();
                            String last = null;
                            int runStart = 0;
                            int minY = height.getMinY(), maxY = height.getMaxY();
                            for (int y = minY; y <= maxY + 1; y++) {
                                String s = y <= maxY ? BlockStateParser.serialize(chunk.getBlockState(p.set(pos.getMinBlockX() + x, y, pos.getMinBlockZ() + z))) : null;
                                if (last != null && !last.equals(s)) {
                                    col.append(runStart).append('-').append(y - 1).append('=').append(last).append(' ');
                                }
                                if (s != null && !s.equals(last)) runStart = y;
                                last = s;
                            }
                            out.printf("fill\t%d\t%d,%d\t%d,%d\t%s%n", seed, c[0], c[1], x, z, col.toString().trim());
                        }
                    }
                }
            }
        }
    }
}
