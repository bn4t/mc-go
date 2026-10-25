import net.minecraft.SharedConstants;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.util.datafix.DataFixers;
import net.minecraft.world.entity.EntityType;
public class Probe {
    public static void main(String[] a) {
        SharedConstants.tryDetectVersion();
        net.minecraft.server.Bootstrap.bootStrap();
        DataFixers.getDataFixer(); // force DFU schema init for EntityType.build
        for (EntityType<?> t : BuiltInRegistries.ENTITY_TYPE) {
            var k = BuiltInRegistries.ENTITY_TYPE.getKey(t);
            System.out.println(k + " " + t.clientTrackingRange() + " " + t.updateInterval());
        }
    }
}
