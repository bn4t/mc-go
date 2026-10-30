import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.Collections;
import net.minecraft.SharedConstants;
import net.minecraft.server.Bootstrap;
import net.minecraft.data.registries.VanillaRegistries;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.core.component.DataComponents;
import net.minecraft.world.item.Item;
import net.minecraft.world.item.component.KineticWeapon;

public final class KineticMetadata {
    private static String condition(KineticWeapon.Condition condition) {
        return condition.maxDurationTicks() + "\t" + (double)condition.minSpeed()
            + "\t" + (double)condition.minRelativeSpeed();
    }
    public static void main(String[] args) throws Exception {
        SharedConstants.tryDetectVersion();
        Bootstrap.bootStrap();
        for (var pending : BuiltInRegistries.DATA_COMPONENT_INITIALIZERS.build(VanillaRegistries.createLookup())) pending.apply();
        var rows = new ArrayList<String>();
        for (Item item : BuiltInRegistries.ITEM) {
            var component = item.getDefaultInstance().get(DataComponents.KINETIC_WEAPON);
            if (component == null) continue;
            var sound = component.sound().orElseThrow().value();
            var hit = component.hitSound().orElseThrow().value();
            rows.add(BuiltInRegistries.ITEM.getKey(item) + "\t"
                + component.contactCooldownTicks() + "\t" + component.delayTicks() + "\t"
                + condition(component.dismountConditions().orElseThrow()) + "\t"
                + condition(component.knockbackConditions().orElseThrow()) + "\t"
                + condition(component.damageConditions().orElseThrow()) + "\t"
                + (double)component.forwardMovement() + "\t" + (double)component.damageMultiplier() + "\t"
                + BuiltInRegistries.SOUND_EVENT.getId(sound) + "\t" + BuiltInRegistries.SOUND_EVENT.getId(hit) + "\t"
                + sound.location() + "\t" + hit.location());
        }
        Collections.sort(rows);
        Files.writeString(Path.of(args[0]), String.join("\n", rows) + "\n");
    }
}
