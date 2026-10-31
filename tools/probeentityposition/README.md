# Entity position quantizer facts

`PositionQuantizerProbe.java` calls public `VecDeltaCodec.encodeX/Y/Z` on a
zero base and checks all three axes agree. It then sets an explicit base and
observes the exact ceiling-resize delta. `facts.tsv` stores input double bits
and observed integer output, plus the named delta row. No implementation body
is copied into this probe or the independent Go quantizer.

Measured with Temurin25.0.5+8 against the unpacked Minecraft26.2 server jar,
SHA256 `183c0499c5f855570ee487dd38e141a53f0121f83a0b07a3bac2d8b6698823e8`.
Compile with the version jar plus bundler library jars on the classpath, then
run `PositionQuantizerProbe` using that classpath. The managed workspace uses
build slot A only for javac, releases it, and runs under an available oracle
lock (at most two JVM invocations).

The finite corpus covers zero, signed half-step ties, their immediate double
neighbors, measured Y coordinates160.79999995231628/161.19999998807907/162.000001,
and saturation limits. NaN/infinities are codec-only guards; they do not imply
that nonfinite entity positions are admitted. This is a bounded numeric corpus,
not an exhaustive audit of every finite double or entity movement consumer.

`TestEntityPositionQuantizerParity` independently drives actual first-frame armor
stand movement in empty space and checks authoritative source positions with
exact one-nonzero-displacement count and relative-packet integer comparisons.
Raw zero-delta spawn resends remain logged; frozen network-frame versus world-
frame tracking resends are a separate existing limit. `TestMobPositionQuantizerFacts`
pins the API corpus and the ceiling delta without changing other wire codecs.
