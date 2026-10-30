import net.minecraft.SharedConstants;
import net.minecraft.server.Bootstrap;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.world.entity.Entity;
import net.minecraft.world.entity.EntityDimensions;
import net.minecraft.world.entity.EntityAttachment;
import java.lang.reflect.Modifier;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.MessageDigest;
import java.util.ArrayList;
import java.util.Collections;
import java.util.HexFormat;
import java.util.jar.JarFile;

// Records numeric dimension objects through their public getters. No native
// implementation, instructions or method bodies are inspected or reproduced.
public class EyeDimensionsProbe {
 public static void main(String[] args) throws Exception {
  if(args.length!=2&&args.length!=3)throw new IllegalArgumentException("server-version.jar output.tsv [attachments.tsv]");
  var digest=MessageDigest.getInstance("SHA-256");
  try(var input=Files.newInputStream(Path.of(args[0]))){byte[] b=new byte[65536];for(int n;(n=input.read(b))!=-1;)digest.update(b,0,n);}
  String hash=HexFormat.of().formatHex(digest.digest());
  SharedConstants.tryDetectVersion();Bootstrap.bootStrap();
  var rows=new ArrayList<String>();var attachments=new ArrayList<String>();int types=0,entities=0,variants=0;
  for(var type:BuiltInRegistries.ENTITY_TYPE){var id=BuiltInRegistries.ENTITY_TYPE.getKey(type);if(!id.getNamespace().equals("minecraft"))throw new IllegalStateException("unexpected namespace");var d=type.getDimensions();rows.add("type\t"+id.getPath()+"\t"+d.width()+"\t"+d.height()+"\t"+d.eyeHeight());types++;
   if(args.length==3){var p=d.attachments().getClamped(EntityAttachment.PASSENGER,0,0);var v=d.attachments().getClamped(EntityAttachment.VEHICLE,0,0);attachments.add("type\t"+id.getPath()+"\t"+p.x+"\t"+p.y+"\t"+p.z+"\t"+v.x+"\t"+v.y+"\t"+v.z);}
  }
  try(var jar=new JarFile(args[0])){for(var entry:Collections.list(jar.entries())){
   String name=entry.getName();if(!name.startsWith("net/minecraft/world/entity/")||!name.endsWith(".class"))continue;
   var cls=Class.forName(name.substring(0,name.length()-6).replace('/','.'),false,Entity.class.getClassLoader());
   if(!Entity.class.isAssignableFrom(cls))continue;entities++;
   for(var field:cls.getDeclaredFields())if(Modifier.isStatic(field.getModifiers())&&field.getType()==EntityDimensions.class){field.setAccessible(true);var d=(EntityDimensions)field.get(null);if(d!=null){rows.add("variant\t"+cls.getName()+"\t"+field.getName()+"\t"+d.width()+"\t"+d.height()+"\t"+d.eyeHeight());variants++;}}
  }}
  if(types!=158||entities!=187||variants!=44)throw new IllegalStateException("unexpected census "+types+"/"+entities+"/"+variants);
  Collections.sort(rows);
  String text="# Minecraft Java26.2 public EntityDimensions getter facts\n# server-26.2.jar SHA256 "+hash+"\n"+String.join("\n",rows)+"\n";
  Files.writeString(Path.of(args[1]),text);
  if(args.length==3){if(attachments.size()!=158)throw new IllegalStateException("unexpected attachment census");Collections.sort(attachments);Files.writeString(Path.of(args[2]),"# Minecraft Java26.2 public EntityDimensions attachment getter facts\n# server-26.2.jar SHA256 "+hash+"\n# EntityType.getDimensions().attachments().getClamped(PASSENGER/VEHICLE,0,0f); type records only\n"+String.join("\n",attachments)+"\n");}
  System.err.println("types="+types+" entityClasses="+entities+" dimensionVariants="+variants);
 }
}
