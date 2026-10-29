import java.util.*;
import net.minecraft.SharedConstants;
import net.minecraft.server.Bootstrap;
import net.minecraft.core.registries.BuiltInRegistries;
public class PlayerEffectAttributesProbe {
 public static void main(String[] args) {
  SharedConstants.tryDetectVersion();Bootstrap.bootStrap();
  var rows=new ArrayList<String>();
  for(var effect:BuiltInRegistries.MOB_EFFECT) {
   final String name=BuiltInRegistries.MOB_EFFECT.getKey(effect).toString();
   effect.createModifiers(0,(attribute,modifier)->rows.add(name+"\t"+BuiltInRegistries.ATTRIBUTE.getKey(attribute.value())+"\t"+modifier.id()+"\t"+modifier.amount()+"\t"+modifier.operation().getSerializedName()));
  }
  Collections.sort(rows);rows.forEach(row->System.out.println("FACT\t"+row));
 }
}
