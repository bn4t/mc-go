import net.minecraft.SharedConstants;
import net.minecraft.server.Bootstrap;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.world.entity.EntityAttachment;

// Selected public dimension/attachment getter facts only. Actual linked-rider
// positions and callback clocks are established separately on the wire.
public class SeatAttachmentProbe {
 public static void main(String[] args) {
  SharedConstants.tryDetectVersion();Bootstrap.bootStrap();
  String[] names={"chicken","horse","zombie","player"};
  for(String name:names){
   boolean found=false;
   for(var type:BuiltInRegistries.ENTITY_TYPE){
    if(!BuiltInRegistries.ENTITY_TYPE.getKey(type).toString().equals("minecraft:"+name))continue;
    found=true;
    for(float scale:new float[]{.5f,1f,2f}){
     var d=type.getDimensions().scale(scale);
     var p=d.attachments().getClamped(EntityAttachment.PASSENGER,0,0);
     var v=d.attachments().getClamped(EntityAttachment.VEHICLE,0,0);
     System.out.println(name+"\t"+scale+"\t"+d.width()+"\t"+d.height()+"\t"+p.x+"\t"+p.y+"\t"+p.z+"\t"+v.x+"\t"+v.y+"\t"+v.z);
    }
   }
   if(!found)throw new IllegalStateException("missing type "+name);
  }
  System.exit(0);
 }
}
