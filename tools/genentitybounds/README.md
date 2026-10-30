# Static entity dimension and attachment facts

The runtime tables contain numeric public getter results, not native implementation. They reuse the existing `gencommandeyes/public-dimensions.tsv` width/height columns and add `public-attachments.tsv`, produced by the same probe's optional third output. The original two-argument probe contract and eye output remain unchanged.

The source is Minecraft Java Edition 26.2, protocol 776, with JDK 25. The version-jar SHA256 is `183c0499c5f855570ee487dd38e141a53f0121f83a0b07a3bac2d8b6698823e8`. The dimensions fixture covers all 158 entity type records plus 44 static dimension objects across 187 entity classes. The attachment fixture covers all 158 type records through `EntityType.getDimensions().attachments().getClamped(kind, 0, 0f)` for PASSENGER and VEHICLE. It records the first point before rotation.

Dimension fixture SHA256: `c6fb64e26a7b82e9adf7b75090032d738e411e94e07170efd3aba82a7d5ec63c`.
Attachment fixture SHA256: `6e60f954187d584d977a2e88744f18a9c616884e1e79423a8bb56fdf0dd6567c`.

From the repository root:

```sh
source /workspace/mcgo-tools/activate.sh
entity_fact_libraries=$(rg --files vanilla/libraries -g '*.jar' | paste -sd:)
entity_fact_classpath="vanilla/versions/26.2/server-26.2.jar:$entity_fact_libraries"
mkdir -p /tmp/mcgo-entity-facts-probe
javac -d /tmp/mcgo-entity-facts-probe -cp "$entity_fact_classpath" tools/gencommandeyes/EyeDimensionsProbe.java
java -cp "$entity_fact_classpath:/tmp/mcgo-entity-facts-probe" EyeDimensionsProbe vanilla/versions/26.2/server-26.2.jar /tmp/mcgo-entity-dimensions.tsv tools/gencommandeyes/public-attachments.tsv
cmp /tmp/mcgo-entity-dimensions.tsv tools/gencommandeyes/public-dimensions.tsv
go run ./tools/genentitybounds
go run ./tools/genentitybounds -check
go run ./tools/gencommandeyes -check
```

The stdlib Go generator checks finite numbers, duplicate keys, equal version-jar checksums, matching registry membership, and exact coverage counts. Width/height tables use float32 so native float values widen correctly. Attachment coordinates use float64; decimal getter output round-trips the original double, including preserved negative zero if present. Generated headers record both fixture hashes and the version-jar checksum.

These are static type records. They do not establish runtime dimensions for poses, babies, attribute scale, interaction/display entities, size-dependent mobs or live vehicles. A boat's type-record passenger fallback is its full height; AbstractBoat's runtime seat overrides it. Preserve such consumer overrides and pin each runtime adoption independently. Complete factual coverage is not complete collision, reach or seating parity.
