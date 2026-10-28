import java.io.PrintWriter;
import net.minecraft.SharedConstants;
import net.minecraft.core.BlockPos;
import net.minecraft.core.Direction;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.world.level.EmptyBlockGetter;
import net.minecraft.world.level.block.Block;
import net.minecraft.world.level.block.state.BlockState;

// Prints, per block state id, whether the state's collision shape (read
// against an empty world at the origin) covers its whole top face —
// the Block.isFaceFull(collision, UP) fact support checks consult.
public class FaceProbe {
    public static void main(String[] args) throws Exception {
        SharedConstants.tryDetectVersion();
        net.minecraft.server.Bootstrap.bootStrap();
        try (var out = new PrintWriter(args[0], java.nio.charset.StandardCharsets.UTF_8)) {
            int count = 0;
            for (BlockState state : Block.BLOCK_STATE_REGISTRY) {
                var shape = state.getCollisionShape(EmptyBlockGetter.INSTANCE, BlockPos.ZERO);
                boolean full = Block.isFaceFull(shape, Direction.UP);
                out.printf("%d\t%s\t%d%n", Block.getId(state), BuiltInRegistries.BLOCK.getKey(state.getBlock()), full ? 1 : 0);
                count++;
            }
            out.printf("count\t%d%n", count);
        }
    }
}
