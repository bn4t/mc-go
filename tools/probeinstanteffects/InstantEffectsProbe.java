import java.util.*;
import net.minecraft.SharedConstants;
import net.minecraft.server.Bootstrap;
import net.minecraft.core.registries.BuiltInRegistries;
public class InstantEffectsProbe {
 public static void main(String[] args) {
  SharedConstants.tryDetectVersion(); Bootstrap.bootStrap();
  var rows=new ArrayList<String>();
  for(var effect:BuiltInRegistries.MOB_EFFECT) {
   String name=BuiltInRegistries.MOB_EFFECT.getKey(effect).toString();
   rows.add(name+"\t"+effect.isInstantaneous()+"\t"+effect.shouldApplyEffectTickThisTick(0,0)+"\t"+effect.shouldApplyEffectTickThisTick(1,0));
  }
  Collections.sort(rows); rows.forEach(row->System.out.println("FACT\t"+row));
 }
}
