import net.minecraft.SharedConstants;
import net.minecraft.server.Bootstrap;
import net.minecraft.commands.CommandSource;
import net.minecraft.commands.CommandSourceStack;
import net.minecraft.commands.arguments.coordinates.LocalCoordinates;
import net.minecraft.network.chat.Component;
import net.minecraft.server.permissions.PermissionSet;
import net.minecraft.util.Mth;
import net.minecraft.world.phys.Vec2;
import net.minecraft.world.phys.Vec3;
import java.nio.file.*;
import java.util.*;
public class CommandMathPublicProbe {
 public static void main(String[] args)throws Exception{
  SharedConstants.tryDetectVersion();Bootstrap.bootStrap();
  if(args.length!=2)throw new IllegalArgumentException("version-jar output.tsv");
  var digest=java.security.MessageDigest.getInstance("SHA-256");
  try(var input=Files.newInputStream(Path.of(args[0]))){byte[] b=new byte[65536];for(int n;(n=input.read(b))!=-1;)digest.update(b,0,n);}
  String versionHash=HexFormat.of().formatHex(digest.digest());
  var source=new CommandSourceStack(CommandSource.NULL,Vec3.ZERO,Vec2.ZERO,null,PermissionSet.ALL_PERMISSIONS,"probe",Component.literal("probe"),null,null);
  try(var out=Files.newBufferedWriter(Path.of(args[1]))){
   out.write("# Public26.2 math/command-source API numeric observations\n# Version-jar SHA256 "+versionHash+"\n");
   out.write("constant\tDEG_TO_RAD\t"+Float.toHexString(Mth.DEG_TO_RAD)+"\nconstant\tRAD_TO_DEG\t"+Float.toHexString(Mth.RAD_TO_DEG)+"\n");
   for(int i=0;i<65536;i++){double angle=(i+.25)*Math.PI*2/65536;out.write("sin\t"+i+"\t"+Integer.toHexString(Float.floatToRawIntBits(Mth.sin(angle)))+"\n");}
   int[] edges={-131073,-65536,-32768,-16384,-1,0,1,16384,32768,65535,65536,131071};
   for(int i:edges)for(double f:new double[]{-1e-10,0,1e-10,.25,.75}){double angle=(i+f)*Math.PI*2/65536;out.write("trig\t"+Double.toHexString(angle)+"\t"+Integer.toHexString(Float.floatToRawIntBits(Mth.sin(angle)))+"\t"+Integer.toHexString(Float.floatToRawIntBits(Mth.cos(angle)))+"\n");}
   var random=new Random(262);
   float[][] rotations={{0,0},{90,0},{0,90},{90,30},{-0x1p-17f,0},{0,9.201994f},{-90,-7.406911f}};
   for(var r:rotations)basis(out,source,r[0],r[1]);
   for(int i=0;i<256;i++)basis(out,source,(random.nextFloat()-.5f)*1440,(random.nextFloat()-.5f)*360);
  }
 }
 static void basis(java.io.Writer out,CommandSourceStack source,float yaw,float pitch)throws Exception{
  var rotated=source.withRotation(new Vec2(pitch,yaw));
  for(int axis=0;axis<3;axis++){var v=new LocalCoordinates(axis==0?1:0,axis==1?1:0,axis==2?1:0).getPosition(rotated);out.write("basis\t"+Float.toHexString(yaw)+"\t"+Float.toHexString(pitch)+"\t"+axis+"\t"+Double.toHexString(v.x)+"\t"+Double.toHexString(v.y)+"\t"+Double.toHexString(v.z)+"\n");}
 }
}
