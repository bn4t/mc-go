# Local command-coordinate numerical oracle

`CommandMathPublicProbe.java` calls public Minecraft26.2 APIs: `Mth.sin/cos`, the angular constants, `CommandSourceStack.withRotation`, and `LocalCoordinates.getPosition`. It creates a source with zero position and no executor; no server/world is started. It does not inspect native method bodies, instructions, private fields or implementation structure.

The deterministic normalized fixture contains65536 sine-bin values,60 boundary sin/cos observations and789 local-basis vectors for263 fixed/seeded rotations. Float values use raw hexadecimal bits; input angles and vector components use exact hexadecimal floating-point text. The probe computes and records the version-jar SHA256. The runtime sine samples are generated independently with stdlib `math.Sin`; the Go census checks every float32 sample and boundary, then the complete TP local-coordinate callsite against every native double basis component. The Euler/tangent-basis model was derived from these observations and standard geometry.

Version jar SHA256: `183c0499c5f855570ee487dd38e141a53f0121f83a0b07a3bac2d8b6698823e8`.

Fixture SHA256: `050b0f6b91a582f6dc3a6c009239631cd95b168b9af5f83a147e497fa81c9208`.

From the repository root with the official26.2 version jar/libraries and JDK25:

```sh
source /workspace/mcgo-tools/activate.sh
command_math_classpath="vanilla/versions/26.2/server-26.2.jar:$(find vanilla/libraries -name '*.jar' -print | paste -sd:)"
mkdir -p /tmp/mcgo-command-math
javac -d /tmp/mcgo-command-math -cp "$command_math_classpath" tools/probecommandmath/CommandMathPublicProbe.java
java -cp "$command_math_classpath:/tmp/mcgo-command-math" CommandMathPublicProbe vanilla/versions/26.2/server-26.2.jar tools/probecommandmath/public-math.tsv
sha256sum tools/probecommandmath/public-math.tsv
go test ./internal/server -run '^TestCommand(MathPublicSamples|LocalBasisPublicSamples)$' -count=1
```

The unit tests are the exact verifier for the independently computed runtime table and basis construction; there is no copied/generated native implementation. Float32 angular operations occur before widening basis components, and the left direction is a double cross product. Public API samples include rotations beyond normal command argument ranges to verify the numerical helper itself. The wire pin uses24 bounded rotations with native-valid decimal tokens and independent corpus-based position expectations.

This fixture covers local command-coordinate math. It does not establish facing-angle interpolation, global entity/physics trigonometry, every numeric-token grammar, or every possible combination/sum of local offsets. Source eye anchors and dimension coordinate scaling have their own pins.
