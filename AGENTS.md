# AGENTS

Conventions for agents (human or AI) working in this repo.

## Project shape

mc-go is a clean-room Minecraft Java Edition 26.2 (protocol 776) server
in Go, stdlib only. See README.md for build/run and the clean-room
rules — they are hard constraints, not style.

## Workflow: parity slices

Work lands in small, independently verified **slices**. Each slice:

1. **Measure** vanilla behavior first. Sources of truth, in order:
   - the wire: run the vanilla jar and observe the actual packets
     (the blackbox harness does exactly this), and
   - `javap -c -p -cp vanilla/versions/26.2/server-26.2.jar <class>` for
     constants, order-of-operations, and branch structure. Reading
     bytecode for measurement is fine; copying structure/text into the
     Go code is not.
2. **Implement** in Go, matching measured semantics — not an
   approximation of them.
3. **Pin** it with a differential blackbox test (`tests/blackbox/`) that
   runs against both servers, or a scripted-client test when vanilla
   has no counterpart.
4. **Log** the slice at the end of `GOAL.md`'s progress log in the
   established `## Slice — <name> (<date>)` format with
   **Measured** / **Implemented** / **Pinned** bullets, and flip the
   parity-matrix checkbox notes when a matrix item's coverage changed.

GOAL.md is append-mostly: new slices go at the bottom of the progress
log; matrix checkboxes get edited in place with short notes. Do not
restructure the file.

## Testing

```sh
go build ./...                                     # must stay clean
go test ./internal/...                             # unit tests
MCGO_BLACKBOX=1 go test -tags blackbox -count=1 -run TestX ./tests/blackbox/   # one pin
MCGO_BLACKBOX=1 go test -tags blackbox -timeout 45m -count=1 ./tests/blackbox/ # full suite
```

- Blackbox needs `java` (JDK 25) on PATH and
  `vanilla/server-26.2.jar` — fetch + datagen per README.
- `MCGO_TARGET=host:port` diffs an external server against vanilla.
- Vanilla legs of a test boot the real jar per test — each is slow
  (~seconds), so run the tests you touched, not the whole suite, while
  iterating.

## Commits and pushing

- Push directly to `master`; no PRs, no force-pushes.
- Commit style: lowercase, terse, area-prefixed — e.g.
  `enderman: TakeBlockGoal/LeaveBlockGoal + CARRY_STATE wire/save parity`,
  `panic flee: PanicGoal 40t window, random ±5/±4 targets`.
- Commit code + test + the GOAL.md slice entry together, one commit per
  slice.
- Never commit `vanilla/` (the jar, unpacked versions, generated data —
  gitignored), `*.test`, or world output files.
- Never commit Mojang-derived code or data text; `*_gen.go` files are
  produced by `tools/gen*` reading the jar/datagen — regenerate them
  rather than hand-editing.

## Conventions in the code

- Mob/entity goals are modeled per vanilla class/goal names in
  `internal/server/mob.go` and family files (e.g. `enderstare.go`,
  `followowner.go`, `caravan.go`) — comments cite the measured vanilla
  behavior (class/method context), not just what the code does.
- Tick ordering matters: where a step sits relative to the
  `isEffectiveAi` (NoAI) gate and other goals is part of the parity —
  the comments around `mobTickStep` document the measured order.
- `Entity` carries the wire metadata fields; `mobsync.go` flushes
  `MetaDirty`/`PosDirty` via `mobSyncFlush`.
- Generated tables (`*_gen.go`) come from the vanilla datapack/jar via
  `tools/gen*` — never hand-edit them.

## Environment notes

- Go is at `/usr/local/go/bin/go` (may not be on PATH); JDK 25 at
  `~/opt/jdk25/bin` — the client/server both need class-file v25.
- Vanilla measurements for mob goals use the unpacked jar at
  `vanilla/versions/26.2/server-26.2.jar` (created by running the
  bundler jar once, e.g. the datagen command in README.md).
- The flat blackbox world runs `spawn-monsters=false` +
  `spawn-animals=false` — tests summon mobs explicitly.
- `/tick sprint` on vanilla is ASYNC: commands queued behind it
  interleave mid-sprint. Wait for the `commands.tick.sprint.report`
  chat before reading post-sprint state.
- Real-client playtesting: `.agents/skills/testing-mc-go-real-client/`
  has the launch recipe and the debugging checklist for kicks.
