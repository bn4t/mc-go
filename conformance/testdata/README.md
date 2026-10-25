# Java profile observations

`java-profile-observations.json` contains small factual observations from
independent TCP sessions against official Java Edition 26.1/775 and 26.2/776
server jars. It is wire data, not implementation source. Each row pins the
original official download URL and SHA1/SHA256, measured decompressed login
bodies and their SHA256, decoded Join Game fields, and configuration registry
counts. Session IDs, entity IDs and dimension ordering are representative
captured values, not deterministic expectations for a new server run.

The 775 jar URL/hash was resolved from a pinned GitHub mirror of the official
launcher metadata because the managed environment allowed the official
`piston-data.mojang.com` downloads but not the metadata hostname. Its actual
60,417,588 bytes were then downloaded from Mojang and verified against the
manifest SHA1. The jar's own `version.json` independently reported 26.1/775.
The 776 jar was already pinned by the repository README. Both ran with
OpenJDK Temurin 25.0.5+8. The independently written measurement probe used only
Python's standard library; it performed real handshakes, compression
negotiation, empty known-pack selection, registry receipt, complete joins,
teleport/chunk acknowledgements and movement followed by a player-position
query. Supporting checks compared selected class-file declarations with
`javap -p` and consulted a pinned public protocol schema as a measurement guide.
Those source classes and checksums are disclosed in the record. No schema,
decompiled implementation, disassembly or implementation structure was copied
into Go.

Official `--reports` datagen outputs were generated separately for both jars
and the three relevant report hashes are retained. They establish packet-ID
and numeric-registry facts. Raw jar/datagen output stays in ignored `vanilla/`.
The 775 report has 29,873 states and the776 report 32,366; all 775 state keys
including complete properties occur in 776, but 5,186 common numeric IDs change.
These facts require real adapter mappings; they do not enable 775 admission.

`TestCapturedVanillaProfileBodies` exercises these small bodies through the
public runner. The two `TestPortableConformanceVanilla*` oracle tests reproduce
the seven complete profile scenarios against fresh official endpoints. They
skip explicitly when the opted-in Java/jar prerequisites are absent.

`chunk-26.1.bin` and `chunk-26.2.bin` are complete initial chunk/light
packet bodies from the same official oracle captures. `chunks.json` records
hashes, oracle hashes and registry/dimension bounds used by the independent
public decoder tests. The negative controls truncate bodies, append bytes and
supply an impossible block-registry bound.

`recipe-crafting-table-26.1.bin` and `recipe-crafting-table-26.2.bin` are the
166-byte RecipeBookAdd bodies for the same named recipe. Their manifest pins
both oracle hashes. Mixed-client tests compare native per-version output to
these records before sending the measured display IDs back as recipe clicks.
`mixed-profile-facts.json` holds only the small official report facts exercised
by those tests, including shifted player, item, attribute and block identities;
its input report hashes are recorded alongside the values.
