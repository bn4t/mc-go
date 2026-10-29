# Attribute registry facts

Run from the repository root with JDK 25 and the extracted Java 26.2 bundle:

```sh
bash tools/genattrmetadata/extract.sh
go run ./tools/genattrmetadata
```

The Java harness reads only public registry getters: identifier, registered
default, ranged minimum/maximum and client-sync flag. It writes a sorted,
40-row TSV. The independent Go generator validates the TSV and emits the
metadata table. Neither tool evaluates attributes or inspects implementation
bytecode. Per-entity supplier defaults remain in `entityattr_gen.go`.

Source inner jar SHA256:
`183c0499c5f855570ee487dd38e141a53f0121f83a0b07a3bac2d8b6698823e8`.
Bundle SHA256:
`cdacdfb25898de5e4b4b0e5ddcc2722f77067e46605709c2d886c000ebb63ec5`.
TSV SHA256:
`5c867198b99129eb80daef01e280c59ad388593a9a08d2638bf42efb9c2c6a06`.

The table is factual input. This slice does not change query evaluation,
client updates, equipment, or persistence.
