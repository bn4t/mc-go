import net.minecraft.SharedConstants;
import net.minecraft.server.Bootstrap;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.world.level.block.Block;
import net.minecraft.world.level.EmptyBlockGetter;
import net.minecraft.core.BlockPos;
import net.minecraft.world.phys.shapes.Shapes;
import java.util.BitSet;
import java.util.HexFormat;
public class FullCubeProbe {
 public static void main(String[] args) {
  SharedConstants.tryDetectVersion();Bootstrap.bootStrap();
  for(double d:new double[]{0,-5e-8,5e-8,-2e-7,2e-7}) {
   var shape=Shapes.box(0,0,0,1+d,1,1);
   System.out.println("BOX delta="+d+" full="+Block.isShapeFullBlock(shape)+" boxes="+shape.toAabbs());
   var gap=Shapes.or(Shapes.box(0,0,0,.5,1,1),Shapes.box(.5+Math.abs(d),0,0,1,1,1));
   System.out.println("GAP width="+Math.abs(d)+" full="+Block.isShapeFullBlock(gap)+" boxes="+gap.toAabbs());
  }
  BitSet bits=new BitSet();int count=0,max=-1;
  for(var block:BuiltInRegistries.BLOCK)for(var state:block.getStateDefinition().getPossibleStates()) {
   int id=Block.getId(state);max=Math.max(id,max);count++;
   if(state.isCollisionShapeFullBlock(EmptyBlockGetter.INSTANCE,BlockPos.ZERO))bits.set(id);
  }
  byte[] fixed=new byte[(max+8)/8],raw=bits.toByteArray();System.arraycopy(raw,0,fixed,0,raw.length);
  System.out.println("FULL_STATES "+count+" MAX_ID "+max);
  System.out.println("FULL_BITS "+HexFormat.of().formatHex(fixed));
 }
}
