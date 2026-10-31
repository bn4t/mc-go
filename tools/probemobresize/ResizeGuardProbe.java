import net.minecraft.SharedConstants;
import net.minecraft.server.Bootstrap;
import net.minecraft.server.level.ServerLevel;
import net.minecraft.world.entity.*;
import net.minecraft.world.damagesource.DamageSource;
import net.minecraft.network.syncher.SynchedEntityData;
import net.minecraft.world.level.storage.ValueInput;
import net.minecraft.world.level.storage.ValueOutput;
import sun.misc.Unsafe;

// Method-guard experiment only. No constructed/admitted native entity is claimed.
// Injected fields: Entity.level (uninitialized server-side ServerLevel used only
// for isClientSide), Entity.dimensions, Entity.firstTick, Entity.noPhysics,
// Probe.next and Probe.calls. Pose and reapplyPosition are controlled overrides;
// resize collision work is replaced with a counted callback, never native code.
public class ResizeGuardProbe {
 static class Probe extends Entity {
  EntityDimensions next; int calls;
  private Probe(){super(null,null);}
  protected void defineSynchedData(SynchedEntityData.Builder b){}
  public boolean hurtServer(ServerLevel l,DamageSource s,float a){return false;}
  protected void readAdditionalSaveData(ValueInput i){}
  protected void addAdditionalSaveData(ValueOutput o){}
  public Pose getPose(){return Pose.STANDING;}
  public EntityDimensions getDimensions(Pose p){return next;}
  protected void reapplyPosition(){}
  public boolean fudgePositionAfterSizeChange(EntityDimensions old){calls++;return true;}
 }
 static void field(Object o,String name,Object value)throws Exception{var f=Entity.class.getDeclaredField(name);f.setAccessible(true);f.set(o,value);}
 public static void main(String[] args)throws Exception{
  SharedConstants.tryDetectVersion();Bootstrap.bootStrap();
  var uf=Unsafe.class.getDeclaredField("theUnsafe");uf.setAccessible(true);var u=(Unsafe)uf.get(null);
  var level=(ServerLevel)u.allocateInstance(ServerLevel.class);
  String[] names={"admittedGrowth","firstTickGrowth","noPhysicsGrowth","tooLargeGrowth","shrink","widthOnly"};
  for(int i=0;i<names.length;i++){
   var p=(Probe)u.allocateInstance(Probe.class);p.next=EntityDimensions.scalable(i==3?5f:(i==4?.3f:1.2f),i==5?1.8f:(i==4?.9f:3.6f));
   field(p,"level",level);field(p,"dimensions",EntityDimensions.scalable(.6f,1.8f));field(p,"firstTick",i==1);p.noPhysics=i==2;
   p.refreshDimensions();System.out.println(names[i]+" callbacks="+p.calls+" dims="+p.next.width()+","+p.next.height());
  }
  System.exit(0);
 }
}
