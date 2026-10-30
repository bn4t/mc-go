# Default kinetic-weapon component facts

From the repository root, with JDK 25 and the extracted Java 26.2 bundle:

```sh
bash tools/genkineticmetadata/extract.sh
go run ./tools/genkineticmetadata
```

The Java extractor reads all registered default items through public component
getters and writes `internal/server/testdata/vanilla-26.2/kinetic_weapon_metadata.tsv`.
It uses the public vanilla lookup/component-initializer APIs to bind default
components: `Bootstrap.bootStrap()` alone leaves item component holders unbound.
No gameplay implementation or decompiled code is extracted.

The TSV has seven rows and 18 tab-separated columns:

1. item registry name
2. contact cooldown ticks
3. delay ticks
4. dismount maximum duration
5. dismount minimum speed
6. dismount minimum relative speed
7. knockback maximum duration
8. knockback minimum speed
9. knockback minimum relative speed
10. damage maximum duration
11. damage minimum speed
12. damage minimum relative speed
13. forward movement
14. damage multiplier
15. use-sound registry ID
16. hit-sound registry ID
17. use-sound name
18. hit-sound name

Component `float` getters are explicitly promoted to double before serialization,
retaining values such as `0.699999988079071` and `4.599999904632568` that short JSON
reports round for display. All seven defaults have all three conditions and both
sounds; extraction fails if that assumption changes. The independent Go generator
validates row count, duplicates, types and promoted float values, then emits
`internal/server/kineticinfo_gen.go`. Regeneration from the checked-in TSV needs
only Go; extraction additionally needs the native jar and libraries.

These are factual defaults, separate from condition evaluation, use timing,
kinetic damage/knockback, durability and metadata behavior. Merely adopting the
table does not establish those behaviors.

Provenance: Java 26.2 protocol 776, public registry/component getters under
Temurin 25.0.5+8; bundle SHA256
`cdacdfb25898de5e4b4b0e5ddcc2722f77067e46605709c2d886c000ebb63ec5`,
inner server jar SHA256
`183c0499c5f855570ee487dd38e141a53f0121f83a0b07a3bac2d8b6698823e8`.
