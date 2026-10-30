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
     constants, order-of-operations, and branch behavior. Existing
     workflows use bytecode measurement and factual table extraction;
     this is not a formal independent-team clean-room process. Do not
     copy source, decompiled source, disassembly, or implementation
     structure into Go code. Write the implementation independently
     and pin its observable behavior with tests.
2. **Implement** in Go, matching measured semantics — not an
   approximation of them.
3. **Pin** it with a differential blackbox test (`tests/blackbox/`) that
   runs against both servers, or a scripted-client test when vanilla
   has no counterpart.
4. **Log** the slice at the end of `GOAL.md`'s progress log in the
   established `## Slice — <name> (<date>)` format with
   **Measured** / **Implemented** / **Pinned** bullets, and flip the
   parity-matrix checkbox notes when a matrix item's coverage changed.

5. **Document** changed setup, user-visible capabilities and remaining limits
   in README.md or ARCHITECTURE.md as applicable. Update this file and the
   relevant `.agents/skills/*/SKILL.md` when a verified workflow changes.
   Record evidence and limitations; a passing subset is not full parity.

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
- Verify the committed tree from an immutable `git archive` export before
  pushing. With concurrent agents, test committed HEAD plus only the slice's
  owned patch; unrelated in-progress files can invalidate a test result.
  Stage exact owned patches and new files, preserving other agents' work.
  Verify overlay file hashes in the export and require actual named tests to run.
  git apply in an untracked nested export can discover the outer repository and
  skip paths; an exit-zero no-tests result is not a successful behavioral check.
- Socket deadlines are absolute across login/configuration/join unless reset.
  Set the readiness budget before those reads; a streaming deadline set after
  Join cannot prevent expiry inside Join readiness. Preserve stream assertions.
- Resolve relative SyncPos coordinates against a known client position before
  echoing MovePos. Seed the state with an absolute teleport and keep it current
  with any scripted client movement; raw offsets are not absolute coordinates.
- Validate both test build modes when adding shared blackbox helpers: default
  `go vet ./...` and `go test -tags blackbox -run '^$' ./tests/blackbox`.
  A vanilla-only test using tagged helpers must carry the same build tag;
  passing its tagged oracle does not prove default test compilation.
- Synchronize protocol fixtures with observed packets or queried game ticks.
  A command reply does not prove an earlier editor packet has been applied.
  Keep capture active across barriers; do not drain away the spawn/update
  packets that establish a later assertion's preconditions. Complete the client
  terrain-loaded notification before entity interactions. For RUN or block-use
  timing, observe the applied controller/block state after the single action;
  command feedback alone can precede its application. For cursor return,
  observe the actual player-inventory update before querying the result.
- Invalid-packet probes must await the typed disconnect and actual connection
  closure. A subsequent chat reply can overtake a queued movement kick.
- Executor-binding probes should retain distinct caller/other-player inventory
  sentinels and a nonplayer executor; cover scheduled neutral-source execution.
- For exact tick windows, freeze, use `/tick step N`, and observe completion
  through game time. `/tick sprint N` is asynchronous and measured 26.2
  advances N+1 ticks; wall-clock sleeps are not an exact tick counter.
- Separate a periodic predicate's phase from evidence that its entity tick ran.
  Test a due-age tick and a second call with no age advance; distinguish fresh
  respawn body clocks from live dimension-transfer continuity.
- Check effect pulse counts with uncapped health, since a health cap can hide
  a lost final callback. Pair one-tick effects with zero-duration negatives.
- Distinguish instantaneous status tick semantics from direct potion doses.
  Use a frozen mob and unchanged game time to test add/re-give causality before
  any entity tick; preserve separate uncapped multi-tick and scaled-dose controls.
- Player cooldowns, dimension refresh and advancement scans use player ticks. Observe `play_time`
  after the relevant item/update receipt; world tick steps and wall-clock sleeps
  do not prove that a player cooldown has elapsed. Preserve exact outcome checks.
- Conditional command witnesses and their barrier waits must match the exact
  decoded `/say` message body. Substrings can match syntax-error echoes,
  sender names or unconditional suffix markers. Establish block-entity creation
  before sending an editor packet, then observe its applied update receipt.
- Cap/category fixtures must observe the whole eligible domain. Capture global
  counts and new entity IDs/positions before changing retry budgets or blaming
  production; a valid spawn outside a counted arena can consume the cap.
- Initial play readiness does not prove destination visibility after a teleport.
  Before transient-entity probes, observe the arena chunk and authoritative
  actor position; retain the original spawn/survivor/outcome assertions.
- Physics fixtures must witness the actual block state around the measured tick.
  A requested farmland state can become dirt under a solid cover. Inspect full
  aggregate results and both native/Go failure rows before claiming a baseline.
- For randomized physics, use geometry to select a direction and pin impulse
  bounds plus displacement/velocity relations. Observe the exact first tick;
  unrelated random samples and accumulated impulses are not exact parity pins.
- Capture filters must explicitly retain large packets relevant to a probe.
  Advancement tab updates can exceed 4 KiB; decode runtime definitions and
  assert meaningful counts before comparing geometry or progress.
- For item-on-block interactions, verify default block/menu priority as well
  as selected-hand state and cost. Check effect recipients separately: the
  acting client can predict sounds/particles that only observers receive.
  Open-viewer continuity requires a usable menu backed by the same live object.
- Keep persistent component codec defaults distinct from item prototype defaults.
  An explicit empty component can differ from absence. For effect/spawn ordering,
  establish positive tracking first; batched block packets do not expose internal
  state-mutation order by themselves.
- Restore low command-sequence/fork limits before execute-based witnesses.
  Distinguish unknown-objective errors from missing-score false conditions,
  and decode nested typed error components rather than matching raw text.
- Function probes must distinguish empty/missing groups from nonempty functions
  without return: their execute-store callbacks differ. Confirm loader acceptance
  separately from a codec's partial result, including function permission level.
- Mixed avatar/session tests must await actual owned avatar retirement before
  direct legacy session teardown; Close can return before queued retirement.
- For native method experiments with injected state, list every injected field
  and distinguish method behavior from real construction/world admission. Test
  reset followed by movement before sampling; do not infer reset semantics.
- Pair raw/effective attribute queries with the physical consumer outcome. For
  healing, clear prior instant effects and observe actual player ticks before
  asserting Health; raising a maximum alone does not heal.
- Snapshot player-owned state before common retirement removes its entity.
  Native permanent effect modifiers must retain their active identity/duration;
  identifier prefixes alone do not establish ownership. Distinguish raw saved
  bases from post-load ability overrides and float32 storage from double queries.
- Persistence probes must copy the completed save before shutdown can replace it.
  A preexisting file is not a completion receipt: verify new identifiers, types
  and deadlines. Save workers serialize detached snapshots captured by the
  simulation owner, never mutable runtime queues.
- Distinguish writer compatibility from loader support. Native readback of a
  Go save proves the written format and native behavior; Go restart preservation
  needs its own loading/expiry controls. Assert original deadlines before changing
  a copied fixture to allow startup time.
- Decode unordered registry-keyed state by ID while retaining strict counts,
  duplicate/missing-ID checks and typed value assertions. Positional map order
  is not a clock synchronization guarantee.
- Each server leg must independently satisfy meaningful expected values.
  Equal empty captures, stale menu caches or a decoder that truncates unknown
  definitions do not establish parity. Diagnose fixture failures on an
  unchanged baseline before changing production code or expectations.
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
- Never commit copied Mojang source, decompiled source, disassembly,
  or translated implementation structure. Existing `*_gen.go` factual
  tables are produced by `tools/gen*` reading jar/datagen/report inputs,
  including bytecode inspection by some generators; regenerate them
  rather than hand-editing. Do not commit raw jar or datagen dumps.
- Attribute registry metadata is reproduced through `tools/genattrmetadata/README.md`: public Java getters → factual TSV → independent Go generator.
- Default kinetic component facts use `tools/genkineticmetadata/README.md`.
  Bind public item component initializers through the vanilla lookup; bootstrap
  alone leaves holders unbound. Preserve promoted floats and named condition
  roles independently from runtime evaluation.
- Default armor modifier facts are reproduced with `tools/genarmorattrs/README.md`;
  preserve zero modifiers, slot restrictions and promoted float values.
- New measurement and generation slices must record their source class
  (wire observation, public documentation, report/datapack, or bytecode
  inspection), tool/version and input checksum when applicable, plus
  the corresponding differential or scripted test. Existing artifacts
  do not yet have a complete provenance manifest; do not imply that
  documenting the workflow audits their history.

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

- Discover installed Go/JDK paths rather than assuming a workstation path.
  In the current managed workspace, `source /workspace/mcgo-tools/activate.sh`
  selects Go 1.27.3 and Temurin 25.0.5+8. Minecraft 26.2 needs JDK 25
  (class-file major version 69).
- On this four-core managed host, limit real-oracle runs to two JVMs using
  `/tmp/mcgo-oracle.lock` and `/tmp/mcgo-oracle-secondary.lock`, one lock per
  top-level test invocation. These paths are local coordination conventions,
  not prerequisites for other environments.
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
