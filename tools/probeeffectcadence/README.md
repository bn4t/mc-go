# Periodic effect cadence facts

Official Minecraft Java26.2, public `MobEffect.shouldApplyEffectTickThisTick(int,int)`
on built-in regeneration, poison, and wither. The probe records predicate truth
for duration0..200, every command amplifier0..255, and seven signed shift-boundary
integers. It calls the public API; it does not access private fields or extract
implementation structure. The normalized789-row fixture pins scalar scheduling,
not all effect mechanics or infinite-instance clock selection.

JDK25.0.5+8; Go1.27.3.
Official bundle SHA1 `823e2250d24b3ddac457a60c92a6a941943fcd6a`.
Runtime jar SHA1 `86765a5899bd9c96461036a628796b4245715058`.
Probe SHA256 `e72835ee69fcd17356c0a3182675b21cd130edafdfa6d50d101676f393671741`.
Extractor SHA256 `b5a5086a71dcd255d7b30c984b6b0fb27c30b9b920505da9969458e6ddb20b4a`.
Normalized input SHA256 `c2ae80f16559e553df3913c398f4591c705456bb16398f710b034e832dccd667`.

From repository root with JDK25 and bundled libraries:

```sh
TASK_CP="vanilla/versions/26.2/server-26.2.jar:$(rg --files vanilla/libraries -g '*.jar' | paste -sd: -)"
mkdir -p work/effect-cadence-probe
javac -d work/effect-cadence-probe -cp "$TASK_CP" tools/probeeffectcadence/EffectCadenceProbe.java
flock /tmp/mcgo-oracle-secondary.lock java -cp "work/effect-cadence-probe:$TASK_CP" EffectCadenceProbe > work/effect-cadence-public-api.log 2>&1
go run ./tools/probeeffectcadence -in work/effect-cadence-public-api.log
```

The extractor validates all names, counts, int32 amplifiers, and201-bit rows;
it removes only logging prefixes and writes the stable factual TSV. Its
`-out` option supports temporary regeneration followed by `cmp`. The default
output is `internal/server/testdata/vanilla-26.2/effect_cadence.tsv`.
