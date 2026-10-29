import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.Collections;
import net.minecraft.SharedConstants;
import net.minecraft.server.Bootstrap;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.world.entity.ai.attributes.Attribute;
import net.minecraft.world.entity.ai.attributes.RangedAttribute;

public final class AttributeMetadata {
    public static void main(String[] args) throws Exception {
        SharedConstants.tryDetectVersion();
        Bootstrap.bootStrap();
        var rows = new ArrayList<String>();
        for (Attribute attribute : BuiltInRegistries.ATTRIBUTE) {
            String bounds = attribute instanceof RangedAttribute ranged
                ? ranged.getMinValue() + "\t" + ranged.getMaxValue()
                : "raw\traw";
            rows.add(BuiltInRegistries.ATTRIBUTE.getKey(attribute) + "\t"
                + attribute.getDefaultValue() + "\t" + bounds + "\t"
                + attribute.isClientSyncable());
        }
        Collections.sort(rows);
        Files.writeString(Path.of(args[0]), String.join("\n", rows) + "\n");
    }
}
