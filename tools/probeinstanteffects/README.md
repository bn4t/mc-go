# Instantaneous effect metadata

Official Minecraft Java26.2 public `MobEffect.isInstantaneous` and
`shouldApplyEffectTickThisTick(0,0)/(1,0)` are queried for all40 built-ins.
The only instantaneous effects are instant_health, instant_damage and saturation;
their tick predicates reject0 and accept1. This describes status instances,
not direct potion-dose application.

JDK25.0.5+8; Go1.27.3. BundleSHA1
`823e2250d24b3ddac457a60c92a6a941943fcd6a`; runtimeSHA1
`86765a5899bd9c96461036a628796b4245715058`.
ProbeSHA256 `5bd3f110b435943e1e00d2d1f5de82c7ee065fa32dd89638ad11040d18d13991`.
ExtractorSHA256 `a36a4524d935efae988bbbe4f5ef6f50986a831cf00e474a04b04bf637acb5a5`.
Normalized fixtureSHA256 `2a7b4ad63574ee50e264e39a9c753dfcf1bec15c7760f38060f54b5a508336fa`.

From repository root:

```sh
TASK_CP="vanilla/versions/26.2/server-26.2.jar:$(rg --files vanilla/libraries -g '*.jar' | paste -sd: -)"
mkdir -p work/effect-instant-probe
javac -d work/effect-instant-probe -cp "$TASK_CP" tools/probeinstanteffects/InstantEffectsProbe.java
flock /tmp/mcgo-oracle-secondary.lock java -cp "work/effect-instant-probe:$TASK_CP" InstantEffectsProbe > work/effect-instant-public-api.log 2>&1
go run ./tools/probeinstanteffects -in work/effect-instant-public-api.log
```

The stdlib Go extractor validates40 unique names, three instantaneous flags and
all Boolean fields, then removes only logger prefixes. Default output:
`internal/server/testdata/vanilla-26.2/effect_instantaneous.tsv`. Use `-out` for
an independent regeneration and `cmp`.
