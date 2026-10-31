# Scaled attachment getter facts

`SeatAttachmentProbe.java` observes public EntityType dimensions after
`EntityDimensions.scale(float)`, then reads unrotated PASSENGER and VEHICLE
points for chicken, horse, adult zombie and player type records at.5/1/2.
`facts.tsv` is the normalized12-row result. Width/height and scale are
float32 observations; the six attachment coordinates are doubles. Preserve
that distinction when parsing the rows. These numeric records alone do not
prove instance pose, linked seating or callback timing.

Input: official26.2 version jar SHA256
`183c0499c5f855570ee487dd38e141a53f0121f83a0b07a3bac2d8b6698823e8`;
Temurin25.0.5+8. Compile/run with the unpacked version jar and its bundler
libraries on the classpath. In this workspace use build slotA only for javac,
release it, and run under an available oracle lock (maximum two JVMs).
Bootstrap prefixes stdout with logging text; retain only lines containing
`[STDOUT]: ` followed by one of the four requested names, preserving row fields.
No native implementation bodies are copied or emitted.

`TestScaledMountAttachmentParity` separately witnesses ordered linked IDs and
saved UUID identity, standing player pose, effective scales, direct-vehicle
saved X/Z and actual executor positions using unmounted markers. Exact storage
copies and markers inside one function preserve pre-callback snapshots. The
post-change checks require a completed entity step and admitted player frames;
they do not claim complete frozen-world ticking parity for mounted entities.
Adult zombie VEHICLE precision is backed by both the getters and actual live
stack positions. Other legacy class/baby attachment base policies are preserved.
