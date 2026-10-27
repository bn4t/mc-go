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
// block is leaves, which faces are sturdy, and whether its collision
// shape is a full block. Per block it prints the default state id.
public class BlockPropsProbe {
    public static void main(String[] args) throws Exception {
        SharedConstants.tryDetectVersion();
        net.minecraft.server.Bootstrap.bootStrap();
        try (var out = new PrintWriter(args[0], java.nio.charset.StandardCharsets.UTF_8)) {
            for (Block block : BuiltInRegistries.BLOCK) {
                out.printf("block\t%s\t%d%n", BuiltInRegistries.BLOCK.getKey(block), Block.getId(block.defaultBlockState()));
            }
            for (BlockState state : Block.BLOCK_STATE_REGISTRY) {
                var empty = EmptyBlockGetter.INSTANCE;
                int flags = 0;
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
                out.printf("state\t%d\t%s\t%d%n", Block.getId(state), canonical(state), flags);
            }
        }
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
