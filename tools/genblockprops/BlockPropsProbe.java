import java.io.PrintWriter;
import java.util.TreeMap;
import net.minecraft.SharedConstants;
import net.minecraft.core.BlockPos;
import net.minecraft.core.Direction;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.world.level.EmptyBlockGetter;
import net.minecraft.world.level.block.Block;
import net.minecraft.world.level.block.LeavesBlock;
import net.minecraft.world.level.block.state.BlockState;
import net.minecraft.world.level.block.state.properties.Property;
import net.minecraft.world.level.material.Fluids;

// Prints, per block state id, the state's canonical form (properties
// sorted) and facts world generation consults, read from the running
// game against an empty world at the origin: air, blocksMotion, isSolid,
// canBeReplaced, its fluid (water or lava, source or not), whether the
// block is leaves, which faces are sturdy (full, center and rigid
// support), whether its collision shape is a full block, whether it
// renders solid, whether fire can burn it, whether its collision shape
// covers its top face, whether it is a liquid block, which faces' occlusion
// shapes are full blocks, whether it can occlude, and which faces its
// collision shape covers. Per block it prints the
// default state id, its class, the classes in its hierarchy declaring
// canSurvive and mayPlaceOn, the support tag it keeps, if any, and the
// class declaring updateShape.
public class BlockPropsProbe {
    public static void main(String[] args) throws Exception {
        SharedConstants.tryDetectVersion();
        net.minecraft.server.Bootstrap.bootStrap();
        try (var out = new PrintWriter(args[0], java.nio.charset.StandardCharsets.UTF_8)) {
            for (Block block : BuiltInRegistries.BLOCK) {
                out.printf("block\t%s\t%d\t%s\t%s\t%s\t%s\t%s%n", BuiltInRegistries.BLOCK.getKey(block), Block.getId(block.defaultBlockState()),
                    block.getClass().getSimpleName(), declaring(block.getClass(), "canSurvive"), declaring(block.getClass(), "mayPlaceOn"),
                    supportTag(block), declaring(block.getClass(), "updateShape"));
            }
            var fire = net.minecraft.world.level.block.Blocks.FIRE;
            var igniteOdds = net.minecraft.world.level.block.FireBlock.class.getDeclaredMethod("getIgniteOdds", BlockState.class);
            igniteOdds.setAccessible(true);
            for (BlockState state : Block.BLOCK_STATE_REGISTRY) {
                var empty = EmptyBlockGetter.INSTANCE;
                long flags = 0;
                if (state.isAir()) flags |= 1;
                if (state.blocksMotion()) flags |= 1 << 1;
                if (state.isSolid()) flags |= 1 << 2;
                if (state.canBeReplaced()) flags |= 1 << 3;
                var fluid = state.getFluidState();
                if (!fluid.isEmpty()) {
                    flags |= 1 << 4;
                    if (fluid.getType().isSame(Fluids.WATER)) flags |= 1 << 5;
                    if (fluid.isSource()) flags |= 1 << 6;
                }
                if (state.getBlock() instanceof LeavesBlock) flags |= 1 << 7;
                for (Direction d : Direction.values()) {
                    if (state.isFaceSturdy(empty, BlockPos.ZERO, d)) flags |= 1 << (8 + d.ordinal());
                }
                if (Block.isShapeFullBlock(state.getCollisionShape(empty, BlockPos.ZERO))) flags |= 1 << 14;
                if (state.isSolidRender()) flags |= 1 << 15;
                for (Direction d : Direction.values()) {
                    if (state.isFaceSturdy(empty, BlockPos.ZERO, d, net.minecraft.world.level.block.SupportType.CENTER)) flags |= 1 << (16 + d.ordinal());
                    if (state.isFaceSturdy(empty, BlockPos.ZERO, d, net.minecraft.world.level.block.SupportType.RIGID)) flags |= 1 << (22 + d.ordinal());
                }
                if ((int) igniteOdds.invoke(fire, state) > 0) flags |= 1 << 28;
                if (Block.isFaceFull(state.getCollisionShape(empty, BlockPos.ZERO), Direction.UP)) flags |= 1 << 29;
                if (state.liquid()) flags |= 1 << 30;
                for (Direction d : Direction.values()) {
                    if (Block.isShapeFullBlock(state.getFaceOcclusionShape(d))) flags |= 1L << (31 + d.ordinal());
                }
                if (state.canOcclude()) flags |= 1L << 37;
                for (Direction d : Direction.values()) {
                    if (Block.isFaceFull(state.getCollisionShape(empty, BlockPos.ZERO), d)) flags |= 1L << (38 + d.ordinal());
                }
                if (!state.getCollisionShape(empty, BlockPos.ZERO).getFaceShape(Direction.UP).isEmpty()) flags |= 1L << 44;
                if (state.getBlock().hasDynamicShape()) flags |= 1L << 45;
                out.printf("state\t%d\t%s\t%d%n", Block.getId(state), canonical(state), flags);
            }
        }
    }

    // declaring is the class in the block's hierarchy that declares a
    // method, "-" for none.
    static String declaring(Class<?> c, String name) {
        for (Class<?> k = c; k != null; k = k.getSuperclass()) {
            for (var m : k.getDeclaredMethods()) {
                if (m.getName().equals(name)) return k.getSimpleName();
            }
        }
        return "-";
    }

    // supportTag is the block tag a block keeps in a field (the tag its
    // support checks consult), "-" for none.
    static String supportTag(Block block) throws Exception {
        for (Class<?> k = block.getClass(); k != null && k != Block.class; k = k.getSuperclass()) {
            for (var f : k.getDeclaredFields()) {
                if (f.getType() == net.minecraft.tags.TagKey.class && !java.lang.reflect.Modifier.isStatic(f.getModifiers())) {
                    f.setAccessible(true);
                    return ((net.minecraft.tags.TagKey<?>) f.get(block)).location().getPath();
                }
            }
        }
        return "-";
    }

    static String canonical(BlockState st) {
        String name = BuiltInRegistries.BLOCK.getKey(st.getBlock()).toString();
        var props = new TreeMap<String, String>();
        for (var prop : st.getProperties()) props.put(prop.getName(), value(st, prop));
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

    static <T extends Comparable<T>> String value(BlockState st, Property<T> prop) {
        return prop.getName(st.getValue(prop));
    }
}
