# mc-go

A clean-room, multithreaded Minecraft Java Edition server written in Go
using only the standard library. The target is **full behavioral parity
with the vanilla 26.2 server (protocol 776)**, verified by blackbox
tests that run scripted protocol clients against both mc-go and the real
Mojang server and diff the observable behavior.

This is the consolidated repository at [bn4t/mc-go](https://github.com/bn4t/mc-go).
The active implementation comes from `mc-go-parity`. Earlier experiments
are preserved as independent modules in [`legacy/`](legacy/README.md),
along with their original Git history. See [MIGRATION.md](MIGRATION.md)
for source commits, preserved branches, and validation notes.

The living parity matrix and the slice-by-slice progress log are in
[GOAL.md](GOAL.md). [ARCHITECTURE.md](ARCHITECTURE.md) documents runtime
ownership, public extension boundaries, canonical block identities, detached
chunk encoding and measured costs. Gameplay still uses one simulation owner.
Measured Java 26.1/775 and 26.2/776 clients now share that world. Parallel
regions, remaining numeric-save migrations and neutral frontend admission/
replication remain future stages.

## Clean-room rules

- Go standard library only — no Minecraft libraries, no generated Mojang
  code.
- No copied Mojang source, decompiled source, disassembly, or copied
  implementation structure. The Go implementation is independently written.
- Current evidence sources include the running vanilla jar as a black-box
  oracle, public protocol documentation, official reports and bundled data
  packs. Existing workflows also inspect bytecode with `javap` to measure
  constants, operation ordering and branch behavior; some generators extract
  factual tables from that output. This is the repository's existing
  provenance, not a claim of a formal independent-team clean-room process.
  Bytecode measurement does not authorize translating or copying Mojang's
  implementation. See [AGENTS.md](AGENTS.md) for contribution rules and
  [ARCHITECTURE.md](ARCHITECTURE.md) for evidence boundaries and remaining work.
- Public protocol documentation (minecraft.wiki, protocol 776) is the
  reference for packet IDs, shapes, and semantics.
- Every behavior change is proven by a blackbox test comparing mc-go and
  vanilla, or by a scripted-client test where vanilla has no counterpart.

## Layout

- `cmd/mc-go/` — the server binary.
- `internal/protocol/` — wire framing, VarInt, compression, encryption.
- `internal/server/` — sessions, game tick, commands (Brigadier-style
  tree), entities/mobs and their goals, survival mechanics, world save
  and admin files.
- `internal/world/` — sharded block world, block-state registry, Anvil
  region files, level.dat.
- `internal/data/` — registries, recipes, tags, advancements, loot
  tables generated from the vanilla data packs (`*_gen.go`).
- `internal/nbt/`, `internal/loot/`, `internal/event/`,
  `internal/plugin/` — NBT codec, loot engine, event bus, plugin API.
- `api/event/`, `api/plugin/`, `api/frontend/`, `api/registry/`, `server/` — public
  extension contracts and runtime constructor, usable by external modules.
  Frontend actions currently operate on existing players; the bundled Java
  frontend accepts measured 26.1/775 and 26.2/776 wire profiles.
- `plugins/` — example plugins (`hello`, `anticheat`).
- `conformance/`, `cmd/mc-conformance/` — portable TCP smoke profile,
  endpoint manifests, JSON results and bounded wire transcripts for measured
  Java 775 and 776 profiles. Smoke coverage is separate from gameplay coverage.
- `tests/blackbox/` — the differential parity suite (build tag
  compatibility retained; oracle execution is enabled at runtime).
- `tools/gen*` — generators that read the vanilla jar/datagen output and
  emit the `*_gen.go` tables. `vanilla/` holds the oracle jar and the
  generated data packs; it is gitignored and must be recreated locally.

## Requirements

- Go 1.27.1+
- JDK 25 — only needed to run the vanilla oracle and the datagen, not
  to build mc-go.
- `vanilla/server-26.2.jar` — the Mojang server jar, pinned by sha1:

  ```sh
  mkdir -p vanilla
  curl -LsSf https://piston-data.mojang.com/v1/objects/823e2250d24b3ddac457a60c92a6a941943fcd6a/server.jar -o vanilla/server-26.2.jar
  echo "823e2250d24b3ddac457a60c92a6a941943fcd6a  vanilla/server-26.2.jar" | sha1sum -c -
  ```

- Regenerate the bundled data packs/registries after fetching the jar:

  ```sh
  (cd vanilla && java -DbundlerMainClass=net.minecraft.data.Main -jar server-26.2.jar --all)
  ```

## Build and run

```sh
go build -o mc-go ./cmd/mc-go
./mc-go -addr :25565 -gamemode creative -world world.json
```

Useful flags (see `cmd/mc-go/main.go` for all of them):

- `-world-dir` — read/write a vanilla-format world directory
  (level.dat + region files) instead of the flat `world.json`
  block-override file.
- `-server-dir` — where vanilla-format admin files live (ops.json,
  whitelist.json, banned-*.json).
- `-gamemode` — default game mode (survival, creative, adventure,
  spectator).
- `-frontend` — protocol frontend; `java` accepts Java Edition 26.1 and 26.2.
- `-rcon-password`, `-enable-query` — vanilla RCON/GameSpy4 surfaces.
- `-save-interval` — autosave period.

Join with a vanilla 26.1 or 26.2 client at `localhost:25565`; the server
runs offline mode, so any username works.

Java profile metadata is available through `server.JavaProfiles`. Both profiles
share one authoritative world and 26.2 gameplay rules. The 775 block mapping
represents 29,873 states; 2,493 newer states have no exact representation. Strict
mandatory-state failures retire the affected viewer; projection never
substitutes or rewrites authoritative state, and compatible viewers continue.
Some newer optional counters, attributes and cosmetics are omitted. The server
rejects unknown or unmodeled component input before mutation. This does not
establish complete 26.1 gameplay or client-physics parity. The neutral frontend
API still provides existing-player actions rather than player admission or
world replication.

JSON block override saves write complete canonical state keys in version 2 and
read historical version 1 IDs through their 776 mapping. Native region-format
persistence keeps its existing names/properties encoding. Legacy item JSON
numeric persistence has not been migrated to catalog-independent keys.

## Testing

```sh
go build ./...
go test ./...                          # core, public APIs and Java-free TCP checks
MCGO_BLACKBOX=1 go test -tags blackbox -timeout 45m -count=1 ./tests/blackbox/
```

The blackbox suite boots **both** mc-go (in-process) and the vanilla jar
per test, drives them with scripted protocol clients, and compares the
wire-visible behavior. Oracle comparisons skip with an explicit reason unless
`MCGO_BLACKBOX=1` is set and their Java/jar prerequisites exist. Environment knobs:

- `MCGO_VANILLA_JAR` — path to the oracle jar (default
  `vanilla/server-26.2.jar`).
- `MCGO_VANILLA_775_JAR` — optional 26.1 oracle for the measured
  775 profile (default `vanilla/server-26.1.jar`).
- `MCGO_TARGET=host:port` — run the same suite against an already
  running external server instead of an in-process mc-go; this is how
  other implementations get parity-checked.

The test package is also discoverable without `-tags blackbox`. Oracle
comparisons explicitly skip when their prerequisites are absent; tests never
download the jar. A default test run therefore does not establish vanilla
parity for skipped scenarios. For a Java-free check of another TCP endpoint:

```sh
go run ./cmd/mc-conformance -endpoint 127.0.0.1:25565
```

The default profile checks status and ping. Opt into offline login,
configuration and rejection checks with an endpoint manifest; see
[conformance/README.md](conformance/README.md) for capabilities, report
statuses and limitations.

Before publication, validate a clean export of the actual committed tree with
`git archive HEAD`, build it, and run the public `server` package tests. Their
separate consumer module imports the runtime constructor, profile metadata and
canonical registry API; working-tree success alone can miss ignored sources.

Single test:

```sh
MCGO_BLACKBOX=1 go test -tags blackbox -count=1 -run TestEnderStareParity ./tests/blackbox/
```

For playtesting mc-go with a real (unmodified, offline-mode) 26.2
client see `.agents/skills/testing-mc-go-real-client/SKILL.md`.
