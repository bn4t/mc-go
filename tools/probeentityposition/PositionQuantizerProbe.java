import net.minecraft.network.protocol.game.VecDeltaCodec;
import net.minecraft.world.phys.Vec3;

// Public codec input/output facts; no native bodies are reproduced.
public class PositionQuantizerProbe {
 public static void main(String[] args) {
  double half=0.5/4096.0;
  double[] samples={0,half,-half,Math.nextDown(half),Math.nextUp(half),Math.nextDown(-half),Math.nextUp(-half),162,160.79999995231628,161.19999998807907,162.000001,Double.NaN,Double.POSITIVE_INFINITY,Double.NEGATIVE_INFINITY,0x1p51,-0x1p51};
  var codec=new VecDeltaCodec();
  for(double value:samples){
   var point=new Vec3(value,value,value);
   long x=codec.encodeX(point),y=codec.encodeY(point),z=codec.encodeZ(point);
   if(x!=y||x!=z)throw new IllegalStateException("axis mismatch");
   System.out.println(Long.toHexString(Double.doubleToRawLongBits(value))+"\t"+x);
  }
  codec.setBase(new Vec3(8.5,162,.5));
  System.out.println("ceiling_delta\t"+codec.encodeY(new Vec3(8.5,160.79999995231628,.5)));
 }
}
