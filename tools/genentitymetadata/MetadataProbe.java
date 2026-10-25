import java.io.PrintWriter;
import java.lang.reflect.Field;
import java.lang.reflect.Modifier;
import java.lang.reflect.ParameterizedType;
import java.util.IdentityHashMap;
import java.util.ArrayList;
import java.util.Comparator;
import net.minecraft.SharedConstants;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.network.syncher.EntityDataAccessor;
import net.minecraft.network.syncher.EntityDataSerializer;
import net.minecraft.network.syncher.EntityDataSerializers;
import net.minecraft.world.entity.EntityType;
import net.minecraft.world.entity.Entity;
import net.minecraft.world.entity.Pose;

public class MetadataProbe {
    public static void main(String[] args) throws Exception {
        SharedConstants.tryDetectVersion();
        net.minecraft.server.Bootstrap.bootStrap();
        var serializers = new IdentityHashMap<EntityDataSerializer<?>, String>();
        var classes = new IdentityHashMap<EntityType<?>, Class<?>>();
        Class<?> constants;
        try { constants = Class.forName("net.minecraft.world.entity.EntityTypes"); }
        catch (ClassNotFoundException absent) { constants = EntityType.class; }
        for (Field field : constants.getFields()) {
            if (Modifier.isStatic(field.getModifiers()) && field.getType() == EntityType.class && field.getGenericType() instanceof ParameterizedType generic && generic.getActualTypeArguments()[0] instanceof Class<?> concrete) {
                classes.put((EntityType<?>) field.get(null), concrete);
            }
        }
        try (var out = new PrintWriter(args[0], java.nio.charset.StandardCharsets.UTF_8)) {
            for (Field field : EntityDataSerializers.class.getDeclaredFields()) {
                if (Modifier.isStatic(field.getModifiers()) && EntityDataSerializer.class.isAssignableFrom(field.getType())) {
                    field.setAccessible(true);
                    var serializer = (EntityDataSerializer<?>) field.get(null);
                    serializers.put(serializer, field.getName());
                    out.printf("serializer\t%s\t%d%n", field.getName(), EntityDataSerializers.getSerializedId(serializer));
                    if (field.getGenericType() instanceof ParameterizedType generic && generic.getActualTypeArguments()[0] instanceof Class<?> valueType && valueType.isEnum()) {
                        for (Object value : valueType.getEnumConstants()) {
                            var item = (Enum<?>) value;
                            out.printf("enum\t%d\t%s\t%d%n", EntityDataSerializers.getSerializedId(serializer), item.name(), item.ordinal());
                        }
                    }
                }
            }
            for (Pose pose : Pose.values()) out.printf("pose\t%s\t%d%n", pose.name(), pose.ordinal());
            for (EntityType<?> type : BuiltInRegistries.ENTITY_TYPE) {
                var key = BuiltInRegistries.ENTITY_TYPE.getKey(type).toString();
                Class<?> base = classes.get(type);
                if (base == null) throw new IllegalStateException("No declared generic entity class for " + key);
                Class.forName(base.getName(), true, base.getClassLoader());
                out.printf("entity\t%s\t%d\t%s%n", key, BuiltInRegistries.ENTITY_TYPE.getId(type), base.getName());
                var fields = new ArrayList<String>();
                for (Class<?> owner = base; owner != null && Entity.class.isAssignableFrom(owner); owner = owner.getSuperclass()) {
                    for (Field field : owner.getDeclaredFields()) {
                        if (Modifier.isStatic(field.getModifiers()) && EntityDataAccessor.class.isAssignableFrom(field.getType())) {
                            field.setAccessible(true);
                            var accessor = (EntityDataAccessor<?>) field.get(null);
                            var serializer = accessor.serializer();
                            fields.add(String.format("field\t%s\t%s#%s\t%d\t%s\t%d", key, owner.getSimpleName(), field.getName(), accessor.id(), serializers.get(serializer), EntityDataSerializers.getSerializedId(serializer)));
                        }
                    }
                }
                fields.sort(Comparator.naturalOrder());
                for (String field : fields) out.println(field);
            }
        }
    }
}
