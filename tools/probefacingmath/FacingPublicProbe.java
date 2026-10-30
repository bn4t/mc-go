import net.minecraft.SharedConstants;
import net.minecraft.server.Bootstrap;
import net.minecraft.commands.CommandSource;
import net.minecraft.commands.CommandSourceStack;
import net.minecraft.network.chat.Component;
import net.minecraft.server.permissions.PermissionSet;
import net.minecraft.util.Mth;
import net.minecraft.world.phys.Vec2;
import net.minecraft.world.phys.Vec3;
import java.nio.file.*;
import java.util.*;
public class FacingPublicProbe {
 public static void main(String[] args)throws Exception{
  SharedConstants.tryDetectVersion();Bootstrap.bootStrap();
  if(args.length!=2)throw new IllegalArgumentException("version-jar output.tsv");
  var digest=java.security.MessageDigest.getInstance("SHA-256");
  try(var input=Files.newInputStream(Path.of(args[0]))){byte[] bytes=new byte[65536];for(int n;(n=input.read(bytes))!=-1;)digest.update(bytes,0,n);}
  String hash=HexFormat.of().formatHex(digest.digest());
  var source=new CommandSourceStack(CommandSource.NULL,Vec3.ZERO,Vec2.ZERO,null,PermissionSet.ALL_PERMISSIONS,"probe",Component.literal("probe"),null,null);
  try(var out=Files.newBufferedWriter(Path.of(args[1]))){
   out.write("# Public26.2 facing numerical API observations\n# Version-jar SHA256 "+hash+"\n");
   for(double x:new double[]{.1,.25,.5,1,2,3,4,10,100,10000,1e-20,1e20})out.write("inv\t"+Double.toHexString(x)+"\t"+Double.toHexString(Mth.fastInvSqrt(x))+"\n");
   double[] lengths={.125,.5,1,2,3,10,32,100,10000};
   for(double length:lengths)for(int i=0;i<=1024;i++){double y=i/1024.0,x=Math.sqrt(1-y*y);angle(out,y*length,x*length);}
   var random=new Random(262);for(int i=0;i<1024;i++)angle(out,(random.nextDouble()-.5)*4096,(random.nextDouble()-.5)*4096);
   for(double y:new double[]{0.0,-0.0})for(double x:new double[]{0.0,-0.0,1,-1})angle(out,y,x);
   double[][] fixed={{0,0,10},{-10,0,0},{10,0,0},{0,10,0},{0,-10,0},{0,0,0},{0,-(double)1.62f,10},{10,(double)1.3f,0},{10,(double)1.3f-(double)1.62f,0},{-0.0,0,-1},{-1,0,-0.0}};
   for(var v:fixed)face(out,source,v[0],v[1],v[2]);
   for(double sign:new double[]{-1,1})for(double epsilon:new double[]{0,1e-12,1e-6,.001})for(double horizontal:new double[]{-1,1})face(out,source,epsilon*horizontal,sign*10,epsilon);
   random=new Random(262);for(int i=0;i<256;i++)face(out,source,(random.nextDouble()-.5)*2048,(random.nextDouble()-.5)*2048,(random.nextDouble()-.5)*2048);
  }
 }
 static void angle(java.io.Writer out,double y,double x)throws Exception{out.write("atan\t"+Double.toHexString(y)+"\t"+Double.toHexString(x)+"\t"+Double.toHexString(Mth.atan2(y,x))+"\n");}
 static void face(java.io.Writer out,CommandSourceStack source,double x,double y,double z)throws Exception{var rotation=source.facing(new Vec3(x,y,z)).getRotation();out.write("face\t"+Double.toHexString(x)+"\t"+Double.toHexString(y)+"\t"+Double.toHexString(z)+"\t"+Integer.toHexString(Float.floatToRawIntBits(rotation.y))+"\t"+Integer.toHexString(Float.floatToRawIntBits(rotation.x))+"\n");}
}
