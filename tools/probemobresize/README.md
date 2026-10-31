# Native resize guard experiment

This optional Java 26.2 experiment calls the public `Entity.refreshDimensions`
method and counts a controlled `fudgePositionAfterSizeChange` override. It records
branch facts only. It does not construct or admit a real entity, and it does not
observe collision search or mounting. The differential blackbox fixture
`TestMobScaleResizeLifecycleParity` provides real server measurements separately.

Input: official server version jar SHA256
`183c0499c5f855570ee487dd38e141a53f0121f83a0b07a3bac2d8b6698823e8`.
Measured with Temurin25.0.5+8. Put the unpacked version jar and all bundler
library jars on the Java classpath, then compile/run `ResizeGuardProbe.java`.
The managed workspace uses the existing build slot A for javac, releases it,
and runs the JVM under an available oracle lock (maximum two JVM invocations).

The source lists every injected field. An uninitialized ServerLevel is used
only for its server-side flag; no world methods or collision operations run.
The subclass supplies fixed pose/dimension observations, suppresses position
reapplication and counts the adjustment callback. Native implementation bodies
are neither copied nor emitted. Unsafe warnings are expected for this deliberately
limited method experiment.

Expected callbacks: admitted growth1, firstTick growth0, noPhysics growth0,
new width5 growth0, shrink0, width-only growth1. The native wire control uses a
real admitted NoAI vex: its noPhysics living-frame callback scales its box while
preserving feet. A real admitted player separately retains its feet after scale2.
