import java.util.Map;
import net.minecraft.SharedConstants;
import net.minecraft.server.Bootstrap;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.world.item.AxeItem;
import net.minecraft.world.item.HoneycombItem;
import net.minecraft.world.level.block.Block;
import net.minecraft.world.level.block.WeatheringCopper;

public class ToolFactsProbe {
    private static void report(Class<?> owner, String field) throws Exception {
        var member = owner.getDeclaredField(field);
        member.setAccessible(true);
        Object value = member.get(null);
        if (value instanceof java.util.function.Supplier<?> supplier) value = supplier.get();
        for (var entry : ((Map<?, ?>) value).entrySet()) {
            Block from = (Block) entry.getKey(), to = (Block) entry.getValue();
            System.out.println("MCGO_TOOL_FACT\t" + field + "\t"
                + BuiltInRegistries.BLOCK.getKey(from) + "\t" + BuiltInRegistries.BLOCK.getKey(to));
        }
    }
    public static void main(String[] args) throws Exception {
        SharedConstants.tryDetectVersion();
        Bootstrap.bootStrap();
        report(AxeItem.class, "STRIPPABLES");
        report(WeatheringCopper.class, "PREVIOUS_BY_BLOCK");
        report(HoneycombItem.class, "WAX_OFF_BY_BLOCK");
        report(HoneycombItem.class, "WAXABLES");
    }
}
