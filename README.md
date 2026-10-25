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
[GOAL.md](GOAL.md).

## Clean-room rules

- Go standard library only — no Minecraft libraries, no generated Mojang
  code.
- No decompiled or otherwise derived Mojang source. The vanilla jar is
  used only as (a) a black-box oracle — run it, observe the bytes — and
  (b) a data source — version.json and the bundled data packs.
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
- `plugins/` — example plugins (`hello`, `anticheat`).
- `tests/blackbox/` — the differential parity suite (build tag
  `blackbox`).
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
- `-frontend` — protocol frontend; `java` is the 26.2 Java Edition one.
- `-rcon-password`, `-enable-query` — vanilla RCON/GameSpy4 surfaces.
- `-save-interval` — autosave period.

Join with a vanilla 26.2 client pointed at `localhost:25565`; the server
runs offline mode, so any username works.

## Testing

```sh
go test ./internal/...                 # core unit tests
MCGO_BLACKBOX=1 go test -tags blackbox -timeout 45m -count=1 ./tests/blackbox/
```

The blackbox suite boots **both** mc-go (in-process) and the vanilla jar
per test, drives them with scripted protocol clients, and compares the
wire-visible behavior. It skips silently unless `MCGO_BLACKBOX=1` is set
and `java` is on PATH. Environment knobs:

- `MCGO_VANILLA_JAR` — path to the oracle jar (default
  `vanilla/server-26.2.jar`).
- `MCGO_TARGET=host:port` — run the same suite against an already
  running external server instead of an in-process mc-go; this is how
  other implementations get parity-checked.

Single test:

```sh
MCGO_BLACKBOX=1 go test -tags blackbox -count=1 -run TestEnderStareParity ./tests/blackbox/
```

For playtesting mc-go with a real (unmodified, offline-mode) 26.2
client see `.agents/skills/testing-mc-go-real-client/SKILL.md`.
