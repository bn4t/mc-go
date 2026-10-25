import java.io.PrintWriter;
import java.util.UUID;
import java.util.List;
import java.util.Optional;
import java.lang.reflect.Field;
import java.lang.reflect.Modifier;
import io.netty.buffer.ByteBuf;
import io.netty.buffer.Unpooled;
import net.minecraft.SharedConstants;
import net.minecraft.core.RegistryAccess;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.network.RegistryFriendlyByteBuf;
import net.minecraft.network.codec.StreamCodec;
import net.minecraft.network.protocol.game.ClientboundAddEntityPacket;
import net.minecraft.network.protocol.game.ClientboundUpdateAttributesPacket;
import net.minecraft.network.protocol.game.ClientboundUpdateMobEffectPacket;
import net.minecraft.network.protocol.game.ClientboundRemoveMobEffectPacket;
import net.minecraft.network.protocol.game.ClientboundSetEntityDataPacket;
import net.minecraft.network.syncher.EntityDataAccessor;
import net.minecraft.network.syncher.SynchedEntityData;
import net.minecraft.world.entity.Entity;
import net.minecraft.world.entity.EntityType;
import net.minecraft.world.entity.ai.attributes.AttributeInstance;
import net.minecraft.world.entity.ai.attributes.AttributeModifier;
import net.minecraft.world.effect.MobEffectInstance;
import net.minecraft.world.item.component.ResolvableProfile;
import net.minecraft.world.phys.Vec3;
import net.minecraft.world.entity.player.PlayerSkin;
import net.minecraft.world.entity.player.PlayerModelType;
import net.minecraft.core.ClientAsset;
import com.mojang.datafixers.util.Either;
import net.minecraft.resources.Identifier;
import com.mojang.authlib.GameProfile;
import com.mojang.authlib.properties.Property;
import com.mojang.authlib.properties.PropertyMap;
import com.google.common.collect.ArrayListMultimap;

// Independently chosen detached values are passed to official public codecs.
// No server methods are disassembled or translated by this measurement tool.
public class CodecProbe {
    public static void main(String[] args) throws Exception {
        SharedConstants.tryDetectVersion();
        net.minecraft.server.Bootstrap.bootStrap();
        try (var out = new PrintWriter(args[0], java.nio.charset.StandardCharsets.UTF_8)) {
            var uuid = UUID.fromString("00000000-0000-0000-0000-000000000001");
            var zombie = BuiltInRegistries.ENTITY_TYPE.getValue(Identifier.parse("minecraft:zombie"));
            var packet = new ClientboundAddEntityPacket(321, uuid, 1.25, 70, -3.5, 22.5f, 90f, zombie, 7, new Vec3(.25, -.5, 1.125), 45);
            registry(out,"spawn_zombie_nonzero_motion",ClientboundAddEntityPacket.STREAM_CODEC,packet);
            var speed = BuiltInRegistries.ATTRIBUTE.get(Identifier.parse("minecraft:movement_speed")).orElseThrow();
            var instance = new AttributeInstance(speed, ignored -> {});
            instance.setBaseValue(.125);
            instance.addTransientModifier(new AttributeModifier(Identifier.parse("example:boost"),.25,AttributeModifier.Operation.ADD_MULTIPLIED_TOTAL));
            registry(out,"attributes_speed_modifier",ClientboundUpdateAttributesPacket.STREAM_CODEC,new ClientboundUpdateAttributesPacket(321,List.of(instance)));
            var effect = BuiltInRegistries.MOB_EFFECT.get(Identifier.parse("minecraft:speed")).orElseThrow();
            registry(out,"effect_speed",ClientboundUpdateMobEffectPacket.STREAM_CODEC,new ClientboundUpdateMobEffectPacket(321,new MobEffectInstance(effect,200,2,false,true,true),true));
            registry(out,"remove_speed",ClientboundRemoveMobEffectPacket.STREAM_CODEC,new ClientboundRemoveMobEffectPacket(321,effect));
            Class<?> slime;
            try { slime = Class.forName("net.minecraft.world.entity.monster.Slime"); }
            catch (ClassNotFoundException moved) { slime = Class.forName("net.minecraft.world.entity.monster.cubemob.Slime"); }
            EntityDataAccessor<?> size = accessor(slime,"ID_SIZE");
            @SuppressWarnings("unchecked") var integer = (EntityDataAccessor<Integer>) size;
            registry(out,"slime_size_4",ClientboundSetEntityDataPacket.STREAM_CODEC,new ClientboundSetEntityDataPacket(321,List.of(SynchedEntityData.DataValue.create(integer,4))));
            plain(out,"profile_unresolved_name",ResolvableProfile.STREAM_CODEC,ResolvableProfile.createUnresolved("Alice"));
            plain(out,"profile_unresolved_uuid",ResolvableProfile.STREAM_CODEC,ResolvableProfile.createUnresolved(uuid));
            plain(out,"profile_resolved",ResolvableProfile.STREAM_CODEC,ResolvableProfile.createResolved(new GameProfile(uuid,"Alice")));
            var properties = ArrayListMultimap.<String,Property>create();
            properties.put("textures",new Property("textures","skin-value","signature"));
            plain(out,"profile_resolved_signed",ResolvableProfile.STREAM_CODEC,ResolvableProfile.createResolved(new GameProfile(uuid,"Alice",new PropertyMap(properties))));
            var patch = new PlayerSkin.Patch(Optional.of(new ClientAsset.ResourceTexture(Identifier.parse("example:body"))),Optional.of(new ClientAsset.ResourceTexture(Identifier.parse("example:cape"))),Optional.of(new ClientAsset.ResourceTexture(Identifier.parse("example:elytra"))),Optional.of(PlayerModelType.SLIM));
            var resolved=ResolvableProfile.createResolved(new GameProfile(uuid,"Alice"));
            var constructor=resolved.getClass().getDeclaredConstructor(Either.class,PlayerSkin.Patch.class);
            constructor.setAccessible(true);
            var patched=(ResolvableProfile)constructor.newInstance(Either.left(new GameProfile(uuid,"Alice")),patch);
            plain(out,"profile_skin_patch",ResolvableProfile.STREAM_CODEC,patched);
        }
    }
    private static EntityDataAccessor<?> accessor(Class<?> type,String name) throws Exception {
        for (Class<?> owner=type;owner!=null&&Entity.class.isAssignableFrom(owner);owner=owner.getSuperclass()) {
            try { Field field=owner.getDeclaredField(name);field.setAccessible(true);return (EntityDataAccessor<?>)field.get(null); }
            catch (NoSuchFieldException absent) { }
        }
        throw new IllegalStateException("No accessor "+name);
    }
    private static <T> void registry(PrintWriter out,String name,StreamCodec<RegistryFriendlyByteBuf,T> codec,T value) {
        var buffer=new RegistryFriendlyByteBuf(Unpooled.buffer(),RegistryAccess.fromRegistryOfRegistries(BuiltInRegistries.REGISTRY));
        codec.encode(buffer,value);emit(out,name,buffer);
    }
    private static <T> void plain(PrintWriter out,String name,StreamCodec<ByteBuf,T> codec,T value) {
        var buffer=Unpooled.buffer();codec.encode(buffer,value);emit(out,name,buffer);
    }
    private static void emit(PrintWriter out,String name,ByteBuf buffer) {
        byte[] bytes=new byte[buffer.readableBytes()];buffer.readBytes(bytes);buffer.release();
        out.printf("packet\t%s\t%s%n",name,java.util.HexFormat.of().formatHex(bytes));
    }
}
