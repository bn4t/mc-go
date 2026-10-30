import java.util.*;
import net.minecraft.SharedConstants;
import net.minecraft.server.Bootstrap;
import net.minecraft.core.registries.BuiltInRegistries;
public class EffectCadenceProbe {
 public static void main(String[] args) {
  SharedConstants.tryDetectVersion(); Bootstrap.bootStrap();
  var rows = new ArrayList<String>();
  for (var effect : BuiltInRegistries.MOB_EFFECT) {
   String name = BuiltInRegistries.MOB_EFFECT.getKey(effect).toString();
   if (!Set.of("minecraft:regeneration", "minecraft:poison", "minecraft:wither").contains(name)) continue;
   var amps = new ArrayList<Integer>();
   for (int amp = 0; amp <= 255; amp++) amps.add(amp);
   amps.addAll(List.of(Integer.MIN_VALUE,-33,-32,-1,256,257,Integer.MAX_VALUE));
   for (int amp : amps) {
    var mask = new StringBuilder();
    for (int duration = 0; duration <= 200; duration++) mask.append(effect.shouldApplyEffectTickThisTick(duration, amp) ? '1' : '0');
    rows.add(name + "\t" + amp + "\t" + mask);
   }
  }
  Collections.sort(rows); rows.forEach(row -> System.out.println("FACT\t" + row));
 }
}
