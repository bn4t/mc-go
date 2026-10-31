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
  packets that establish a later assertion's preconditions. Decode spawn LpVec3
  before angles; distinguish fresh body and head tracking. A pitch-only silence
  control needs an authoritative pitch receipt and actual player ticks. Complete the client
  terrain-loaded notification before entity interactions. For RUN or block-use
  timing, observe the applied controller/block state after the single action;
  command feedback alone can precede its application. For cursor return,
  observe the actual player-inventory update before querying the result.
- Silent query probes need a following packet-owner receipt to distinguish
  permission rejection or missing targets from an unread or stalled connection.
  Separate operator permission from game mode and query another player too.
- Invalid-packet probes must await the typed disconnect and actual connection
  closure. A subsequent chat reply can overtake a queued movement kick.
- An automatic movement echo can normalize yaw after teleport. Immediate command
  angle probes should exclude movement ingress or capture inside an atomic
  function; retain actual teleport/chunk acknowledgements for physical actions.
- Source-level mutation probes must query intended and unintended dimensions
  and check typed dimension/position arguments. Matching broken add/query
  handlers can otherwise agree on the wrong world. Cover primitive and batch
  leaves, including positioned-before-in coordinate-scale order.
- Executor-binding probes should retain distinct caller/other-player inventory
  sentinels and a nonplayer executor; cover scheduled neutral-source execution.
- For exact tick windows, freeze, use `/tick step N`, and observe completion
  through game time. `/tick sprint N` is asynchronous and measured 26.2
  advances N+1 ticks; wall-clock sleeps are not an exact tick counter.
- A frozen command poll is not a player frame. Compare independent base and
  living counters against actual play_time with world age fixed. Establish
  matching mode, health and admission before attributing physiology differences.
- Separate a periodic predicate's phase from evidence that its entity tick ran.
  Test a due-age tick and a second call with no age advance; distinguish fresh
  respawn body clocks from live dimension-transfer continuity.
- Check effect pulse counts with uncapped health, since a health cap can hide
  a lost final callback. Pair one-tick effects with zero-duration negatives.
- Distinguish instantaneous status tick semantics from direct potion doses.
  Use a frozen mob and unchanged game time to test add/re-give causality before
  any entity tick; preserve separate uncapped multi-tick and scaled-dose controls.
  For direct Saturation, assert food pools, consumed item and absence of active
  status together; keep arrow stored-effect origins separate.
- Player cooldowns, dimension refresh and advancement scans use player ticks. Observe `play_time`
  after the relevant item/update receipt; world tick steps and wall-clock sleeps
  do not prove that a player cooldown has elapsed. Before releasing a one-shot
  held-use probe after world stepping, observe the player pass that processes it.
  Preserve exact outcome checks. Held-use age and remembered-contact cooldown
  can use different clocks; pin player-only waits and explicit simulation deltas
  independently instead of treating one successful hit as proof of both.
- Conditional command witnesses and their barrier waits must match the exact
  decoded `/say` message body. Substrings can match syntax-error echoes,
  sender names or unconditional suffix markers. Establish block-entity creation
  before sending an editor packet, then observe its applied update receipt.
- Cap/category fixtures must observe the whole eligible domain. Capture global
  counts and new entity IDs/positions before changing retry budgets or blaming
  production; a valid spawn outside a counted arena can consume the cap.
- Pickup probes should establish source pairing before allowing collection,
  then assert actual source/recipient IDs, XP or inventory changes, count and
  retirement together. Keep world age separate from admitted player frames.
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
- Damage-source bypass checks must use the responsible cause/direct identity,
  not kill-credit attribution. Pin health, feedback and command success/result
  stores independently; rejected damage need not reject physical contact.
  For mount transitions, require actual passenger unlink and position receipts.
  Separate source invulnerability from hurt cooldown rejection, and bind vehicle
  client IDs to observed saved identity when requested summon UUID is unsupported.
- Pair raw/effective attribute queries with the physical consumer outcome. For
  healing, clear prior instant effects and observe actual player ticks before
  asserting Health; raising a maximum alone does not heal.
- Snapshot player-owned state before common retirement removes its entity.
  Native permanent effect modifiers must retain their active identity/duration;
  identifier prefixes alone do not establish ownership. Distinguish raw saved
  bases from post-load ability overrides and float32 storage from double queries.
- For exact scheduled callbacks, observe applied completion without advancing
  beyond the original deadline; game time alone can precede callback completion.
  Loaded predicates are not queue-activation witnesses and must not generate
  their own readiness. Preserve dormant relative versus blocked absolute delays.
  Distinguish FULL/admission loss, resident detachment, serialization and physical
  discard. Reticketing must invalidate stale save completions; already-collected
  callbacks are independent of future container membership.
- Reconcile completed IO independently of current controller presence or shutdown.
  A new-submission guard must not discard completion accounting. Pin last-player
  loss followed by full save, stale reticket completions and write failure/retry.
  Negative future-row probes must assert zero provider calls: retirement must not
  materialize chunks merely to save them. Automatic disk completion requires new
  saved state, not file existence or a full-save shortcut.
- Cross-dimension persistence predicates need actual destination readiness.
  A dedicated loader can establish it without moving the editor or changing
  later unqualified command contexts. Preserve strict saved-state assertions.
- Persistence probes must copy the completed save before shutdown can replace it.
  A preexisting file is not a completion receipt: verify new identifiers, types
  and deadlines. Save workers serialize detached snapshots captured by the
  simulation owner, never mutable runtime queues.
- Selective-save probes must change an unselected live neighbor and verify its
  prior disk bytes/timestamp survive. Read selected terrain and queued work with
  native before claiming writer compatibility. Serialize all same-region writers;
  selective storage support does not establish runtime unloading or IO fencing.
- Distinguish writer compatibility from loader support. Native readback of a
  Go save proves the written format and native behavior; Go restart preservation
  needs its own loading/expiry controls. Assert original deadlines before changing
  a copied fixture to allow startup time.
- Decode unordered registry-keyed state by ID while retaining strict counts,
  duplicate/missing-ID checks and typed value assertions. Positional map order
  is not a clock synchronization guarantee.
- Reject nonfinite values explicitly in numeric tolerance/range assertions;
  comparisons against NaN can otherwise pass silently.
- Pin actual raw summon hydration as well as direct load helpers: some entity
  branches return before generic applyEntitySave. Preserve one-dose retirement
  guards and split food/status/lifetime receipts when diagnosing cloud effects.
  For splash no-status assertions, require a positive companion effect on the
  same recipient so a missed throw cannot masquerade as correct dispatch.
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

When replacing legacy command-context swaps with explicit source frames, audit
every spatial consumer for position, dimension and rotation. Pin direct,
function and scheduled neutral-source dimension changes, plus public frontend
lifecycle/window regressions. Preserve exact pose assertions; change an old
expected value only after an independent native measurement establishes it.


Command eye constants are reproduced with tools/gencommandeyes/README.md:
public dimension getters and static dimension objects produce a normalized
fixture, then an independent stdlib generator emits float32 tables. Registry
dimensions alone do not prove runtime pose, baby inheritance or scale behavior.


Local command-coordinate numerics are reproduced with
`tools/probecommandmath/README.md`: public API observations form the factual
corpus; independent Go geometry must match declared float widths and phase
boundaries. Keep complete sample membership checks and a command-consumer wire
pin. Basis equality alone does not prove arbitrary weighted-sum ordering.


Facing-command numerical facts are reproduced with
`tools/probefacingmath/README.md`, including an independent constant derivation.
Keep source position/executor distinct from selected facing targets. Record the
measured finite command domain; matching float-degree consumers does not imply
bit-identical intermediate atan values or arbitrary nonfinite behavior.


When sampling relative saved timers, a clean native save may leave old chunk bytes. Require an independently new persisted marker in the same compound as the timer before declaring automatic unload complete; equal game time and delay can describe an earlier save. Keep activation, FULL loss, serialization, physical discard and cold timer behavior distinct.


For NPC environmental fixtures, freeze setup and reads, validate actual blocks and bodies, and prove exact world-step completion through game time. Player frames require separate receipts; preserve original health, fire, freeze, melt and retirement outcomes when repairing synchronization.


A capped freeze counter does not establish its increment: use independent uncapped player-frame windows in normal and frozen worlds. Native zero TicksFrozen is omitted, so assert canonical membership rather than an explicit zero tag. Keep stationary, re-entry and movement-path exposure claims separate.


Base entity dimensions and first unrotated attachment facts are reproduced by
`tools/genentitybounds/README.md`. Preserve existing runtime class/pose overrides;
type-record attachment defaults are not interchangeable with live vehicle seats.

Presence and a zero component value are separate facts. Pin absent, explicit
zero and nonzero through ingress, persistence and a physical consumer. Creative
slot probes need an actual packet-owner receipt and a retained-state sentinel;
a following chat command does not prove the slot edit was applied.


Resource overrides must follow toolchain activation. This workspace activation
sets GOMAXPROCS=4, and work/run-oracle.sh sources it again; use
`work/run-oracle.sh env GOMAXPROCS=2 ...` when requesting a two-thread oracle.
Record the effective environment rather than assuming an outer export survived.

On this shared four-core workspace, limit new compilations to two build slots
(`/tmp/mcgo-build-a.lock` and `/tmp/mcgo-build-b.lock`). Hold a slot for build,
vet or test-binary compilation, then release it before executing tests or waiting
for an oracle slot. Compile blackbox binaries before taking the JVM lock; CPU
contention during startup can consume the harness readiness budget.


Entity persistence changes must pin supported deletion/movement alongside opaque
roots and passenger composites. Check serializer capability before hydration;
preserve failed submission footprints and reject stale load receipts. Verify
unselected compressed payloads/timestamps and physical source-slot authority.
Run socket-dependent regression tests with local networking enabled. Execute
precompiled test binaries from their package directory so relative fixtures resolve.

Entity-region tests must distinguish native zero-byte unused files from nonzero
corruption, and unregistered passenger IDs from registered entities lacking
serializers. Pin valid siblings, standalone opaque roots and the first selected
write into a native empty file.

Distinguish original generator inputs from edited live blocks/biomes in structure
commands. Establish positive original and edited-biome witnesses before choosing
a source callback; preserve natural-generation and unsupported-context behavior.

For saved numeric limits, separate absent/wrong-type defaults from valid zero,
negative and wrapped values. Pin actual deadlines and elapsed-state continuity
through live edits; a runtime positive-only fallback can hide an ingress bug.

A full verification can compile uniquely named package test binaries with
`go test -c` under a build slot, then release the slot and execute every binary
from its own package directory. Preserve complete `go list ./...` test-package
membership, default/tagged build modes, executable hashes, logs and exit codes;
no-test compilation is a build check, not behavioral proof. Bound runtime workers
separately from native JVMs. When reclaiming compiler cache, hold both build slots
and check that no compiler is active; preserve sources, patches, logs and explicit
verification binaries. Cache eviction alone is not a source change.

Distinguish dying state from physical removal in persistence checks. Preserve
root/passenger topology and saved Health/DeathTime through actual damage, native
save/read and cold restart; pin remaining countdown and no replayed callbacks.
Startup game time can advance before corpse admission, so inspect independent
corpse counters. Identify deliberately injected credit/session fields in guards.

Separate an isolated clear/reset method from its later world-contact settlement.
Retain positive and increasing-inactive counter controls; normalization must not
erase progress toward ignition. Pace eventual-outcome probes by actual admitted
frames, not by the number of read-only polls.
