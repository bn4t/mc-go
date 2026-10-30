# Command eye constants

This independent probe records numeric `EntityDimensions` objects through their public `width()`, `height()` and `eyeHeight()` getters. It reads the type registry plus static dimension objects on entity classes. It does not inspect native method bodies. The checked-in TSV contains factual numbers and identifiers, not native implementation.

The fixture is from Minecraft Java Edition 26.2, protocol 776, with JDK25. Its header identifies the exact version-jar SHA256. It contains all158 entity types and44 static dimension objects found across187 entity classes; the probe fails if that census changes. The generated file records the normalized fixture's SHA256.

Version-jar SHA256: `183c0499c5f855570ee487dd38e141a53f0121f83a0b07a3bac2d8b6698823e8`.

Normalized fixture SHA256: `c6fb64e26a7b82e9adf7b75090032d738e411e94e07170efd3aba82a7d5ec63c`.

From the repository root after obtaining the official jar/libraries using the documented vanilla setup:

```sh
source /workspace/mcgo-tools/activate.sh
spatial_eye_classpath="vanilla/versions/26.2/server-26.2.jar:$(find vanilla/libraries -name '*.jar' -print | paste -sd:)"
mkdir -p /tmp/mcgo-eye-probe
javac -d /tmp/mcgo-eye-probe -cp "$spatial_eye_classpath" tools/gencommandeyes/EyeDimensionsProbe.java
java -cp "$spatial_eye_classpath:/tmp/mcgo-eye-probe" EyeDimensionsProbe vanilla/versions/26.2/server-26.2.jar tools/gencommandeyes/public-dimensions.tsv
go run ./tools/gencommandeyes
go run ./tools/gencommandeyes -check
```

The stdlib Go generator validates dimensions and duplicate keys, checks coverage counts, maps class names with `BABY_DIMENSIONS` constants to their type names, and emits sorted, formatted float32 maps. `MushroomCow` maps to `mooshroom`. `AbstractCow` is superseded by concrete Cow/MushroomCow constants; ArmorStand's marker/small branches are handled explicitly by the runtime helper. Camel uses its named BABY_STANDING_DIMENSIONS eye constant. Native controls establish that glow_squid inherits squid eyes and trader_llama inherits llama eyes; the generator projects those aliases. Other named pose constants remain in the fixture for provenance but are not projected as generic baby dimensions.

These are baseline dimension facts, not proof of every runtime eye-height variant. Native wire pins cover normal/small/marker armor stands, cow/baby cow, zombie/baby zombie, items, bats, slime size4, and standing-player anchor source behavior. Dynamic camel/sniffer poses, some size overrides, interaction-entity runtime dimensions (default and custom) and nonplayer attribute-scale lifecycle remain outside this slice. Player eye offsets use the already measured pose constants and cached effective scale. Native baby bee and donkey controls independently establish the basic half-eye fallback for those modeled types. The same control set pins inherited glow_squid/trader_llama eyes and the separate camel baby constant. This is not a claim of exhaustive baby/pose coverage.
