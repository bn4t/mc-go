# Command-facing public API observations

`FacingPublicProbe.java` calls public Minecraft 26.2 numerical getters and
`CommandSourceStack.facing`. It reads no method bodies or private fields.
The version jar SHA-256 is recorded in the fixture header. The fixture SHA-256 is
`3e1c5089e7cef7af93817e3cb2898f15b59fad8f715e737a03383cbde9aac81b`. The fixture holds
12 reciprocal-root observations, 10,257 angular observations, and 283 actual
command-facing rotations, including signed zero and near-vertical targets.

With JDK 25 and the repository vanilla dependencies installed, reproduce from
repository root (write compiled classes into an owned scratch directory):

```sh
mkdir -p work/facing-probe
CP="vanilla/versions/26.2/server-26.2.jar:$(find vanilla/libraries -name '*.jar' -printf '%p:' | sed 's/:$//')"
javac -cp "$CP" -d work/facing-probe tools/probefacingmath/FacingPublicProbe.java
java -cp "work/facing-probe:$CP" FacingPublicProbe vanilla/versions/26.2/server-26.2.jar work/facing-probe/public-facing.tsv
cmp tools/probefacingmath/public-facing.tsv work/facing-probe/public-facing.tsv
python3 tools/probefacingmath/derive_seed.py tools/probefacingmath/public-facing.tsv
go test ./internal/server -run '^TestFacingPublicNumericModel$' -count=1
```

The independent Go model uses standard complex phase subtraction: normalize a
vector, choose a nearby reference angle on a 1/256 sine grid, and recover the
remaining angle with the cubic inverse-sine expansion. Symmetries extend the
first octant. The reciprocal-root seed was independently synthesized by
inverting one standard Newton refinement with Decimal arithmetic and searching
the nearby binary64 seeds against all twelve public outputs; the committed
script reproduces that derivation. The float degrees-per-radian constant was
observed through the public numerical API. This is an independently constructed
consumer model, not a transcription of a native atan implementation.

Tests assert float degree consumer bits and actual facing yaw/pitch bits.
They do not assert identical intermediate double atan bits. The finite corpus
covers ordinary finite command offsets, cardinals, signed zeros and specified
near-vertical inputs; it establishes no arbitrary extreme/NaN/infinite-domain
or general-purpose atan equivalence. Only command-facing consumers use this
model. Physics trigonometry is unchanged.
