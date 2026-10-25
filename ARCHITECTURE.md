# Runtime foundations and evidence boundaries

mc-go targets Minecraft Java 26.2 (protocol 776) behavioral parity. The
current foundations cover ownership, lifecycle, canonical identities, detached
chunk encoding, measured Java 26.1/775 plus 26.2/776 wire adaptation, and neutral
avatar admission with player presence/movement replication, and finite canonical
block views with privileged typed editing. Full parity, parallel regional
simulation and complete neutral world/gameplay replication remain unfinished.
The [parity matrix](GOAL.md) records implemented behavior; tests and
benchmarks establish only the paths they exercise.

## Runtime and ownership

`Server.Start` initializes one runtime, including the game loop, periodic
time synchronization, RCON and Query. Repeated starts share that runtime.
`Attach` connects a protocol frontend to it; closing one listener detaches
that listener while the runtime and other listeners continue. `Stop` owns
terminal shutdown. A stopped server cannot restart its retired world
workers. Configure server fields before starting it.

One simulation executor serializes gameplay actions. Java play handlers,
player initialization and teardown, RCON commands, frontend actions,
persistence snapshots and ticks use that ownership boundary. Save operations
currently keep both snapshotting and disk I/O under ownership, so saving
can pause gameplay; a detached snapshot followed by asynchronous I/O is
future work. Internal helpers whose names end in
`Owned` require ownership and must not recursively acquire it. Sprints
yield between complete ticks so commands can interleave. Deferred plugin
enforcement uses a bounded queue of 256 actions, drained after an action;
acceptance is distinct from completion. Socket readers and writers remain
separate from simulation ownership. Initial Java chunk/light warmup still
runs synchronously under the owner and can delay other gameplay; join
readiness tests distinguish Play Login from completion of that warmup.
New view windows also warm section and sky data before encoder submission,
so a cold teleport can spend time on the owner before any worker is involved.
Chunk packet encoding can run on bounded workers after owner-side capture
of detached block states, block-entity NBT, light bytes and dimension scalars.
Encoder functions receive no live gameplay objects; completion updates
synchronized transport accounting.

The block world already partitions block overrides among shard workers.
Each worker owns its map and services read/write messages. This does not
make mutable entities, sessions or global gameplay systems independently
safe to tick in parallel. Internal snapshots containing entity pointers
remain owner-only views. The concrete server still exposes legacy world
and entity accessors for in-tree tests and embeddings; callers must not
treat those as an unrestricted concurrent gameplay API. The new public
extension APIs avoid those pointers.

## Protocol and plugin APIs

External frontends can import [`api/frontend`](api/frontend/frontend.go)
and construct a runtime through [`server`](server/server.go), without
importing internal packages. Frontends own transport, framing, codecs,
authentication and authorization. The restricted core returns detached
player/status snapshots and accepts typed movement, teleport, kick and
privileged command actions. `Core.Admit` creates a transient avatar from a
trusted frontend's authenticated profile. Its host-issued `player.ID` identifies
one admission; reconnecting never reuses the previous incarnation. A Java session
embeds the same authoritative player record that a neutral avatar uses without
allocating a fake Java connection. The action boundary
checks cancellation and shutdown under simulation ownership before
dispatch; cancellation does not roll back an admitted action. Movement
shares Java's validation, collision, hooks and canonical commit path.
Custom frontends must stop their own transport workers when their supplied
context ends; the runtime cannot forcibly join arbitrary adapter goroutines.

Avatar connections control only their own presence and movement. A detached
snapshot of active players comes first, followed by reliable revisioned added,
moved, removed and correction values. Revisions increase within each stream;
gaps are allowed. Pose, dimension, game mode and entity identity changes are
coalesced at complete ownership boundaries, including Java command and tick
paths. Dirty records are collected without a global player comparison after
every action; full-state equality suppresses unchanged updates. Pending changes
flush before a new snapshot is captured and registered. Queries by incarnation
read the last committed immutable state, including inside hooks.

Java configuring logins and avatars share capacity and identity reservations.
Neutral duplicate profiles reject. Exact-profile Java replacement retires its
predecessor only after policy and required initial FIFO admission succeed;
denied or failed setup preserves it. Old transport teardown cannot resolve a
name again and retire its successor. These are offline/trusted-frontend identity
policies, not a new account authentication mechanism.

Each avatar has a mailbox, defaulting to 128 queued updates and 1 MiB of detached
state/text storage. Initial snapshot capacity is reserved before copying its
payload. `Next` transfers value ownership and releases the queued charge, so
this is not an in-flight, adapter-copy or total heap limit. A failed queue
retires only that avatar. Iterative removal fanout preserves revision order
through cascading overflows. Canceling one `Next` leaves the connection intact.

The admission context and attachment own connection lifetime. `Close` signals
immediately and schedules owned retirement. One `Admit` per attachment and one
`Move` per connection may await the owner; concurrent requests return `ErrBusy`.
That mutex wait cannot itself be canceled, but lifetime and request cancellation
are rechecked before commit and after movement hooks. No goroutine is created
per waiting request. Migration note: attachment detachment now revokes retained
`Core.Snapshot` and `Core.Execute` capabilities, which previously remained usable
until runtime shutdown. Other attachments continue independently.

`frontend.Blocks` is an optional attachment capability, leaving existing Core
implementations unchanged. `WatchBlocks` captures one fixed half-open box of
canonical block handles and registers its interest under the same simulation
owner. Pending changes flush before that baseline. Snapshots use x-fastest
ordering and carry an immutable catalog; deltas contain detached final values.
Revisions order observed commits with gaps, rather than identifying every world
mutation or serving as a whole-world compare-and-set version.

The world records accepted writes only for interested cells. Direct owned
`SetBlock` and `Restore` deletions use that same recorder. Sparse final reads
batched by shard follow accepted writes and establish their completion barrier;
workers never call frontends or enter gameplay ownership. Action ends and
complete tick yields flush the journal, coalescing repeated positions and
suppressing unchanged values against each view's private baseline. Java retains
its separate tick-end block/light packet timing. This reliability contract covers
owner-committed gameplay, commands, typed edits and owned direct writes. Raw
concurrent storage calls remain outside transaction/snapshot guarantees.

Each view is limited to 65,536 cells, with at most four per attachment. Host
defaults reserve at most 64 views and 1,048,576 baseline/interest cells; payload
budgets cover 64 MiB of queued/reserved bytes and 4,096 queued/reserved messages. Each
mailbox defaults to 64 messages and 2 MiB. Initial snapshot reservations precede
payload, baseline and journal allocation. Cell/view reservations remain held
until owned cleanup removes the interest; `Next` and terminal closure release
queued payload charges. These limits do not cap checked-out values, capture
temporaries, map/slice backing capacity, adapter copies, GC or total heap.
Closing a view signals immediately and schedules owned cleanup once; already
retired handles do not schedule cleanup. Overflow closes only that view, and
attachment teardown removes its views without retiring another attachment.

`EditBlock` accepts an explicit dimension, position, complete canonical target
and optional expected-state key. It enters the existing command-style write
pipeline, including immediate neighbor, block-entity, lighting and entity side
effects. Policy runs before mutation; context, catalog, bounds and comparison
conditions are rechecked afterward. Only the target is modifiable. This policy
applies to the typed privileged entry point, not every Java dig/place or physics
write. One requested cell can cause effects elsewhere; it is not a rollback
transaction, NBT editor or player placement/inventory action.

Plugins can submit lifecycle-bound deferred edits through optional
`plugin.BlockEditor`. They use the existing 256-action pending queue and return
one buffered outcome before closing the receipt channel; callbacks must never
wait on a receipt while holding gameplay ownership. Submission means acceptance,
not application: inspect `EditOutcome.Err` and `Result.Applied`; immediate
rejection returns no receipt. Cancellation/unload/shutdown completes queued work
without mutation. Once dispatch begins, the owner checks
cancellation before writing and reports its actual result; late cancellation
does not rewrite committed success. `Applied` means the write pipeline ran,
and `After` is read after its immediate synchronous effects. Later deferred
edits or scheduled ticks can supersede it before stream publication, so receipts
do not promise a corresponding delta or revision. Legacy block observations
remain unchanged; `TopicBlockEdited` is additive and lossy, separate from the
reliable block stream. Frontends authorize privileged edits themselves.

The bundled Java frontend admits measured 26.1/775 and 26.2/776 profiles
against one authoritative world. Profiles select configuration, codecs,
dispatch and named registry projections. Entity and item gameplay handles
retain canonical 776 registration order; wire IDs never determine core type
classification. Incoming slots and recipe displays resolve to canonical values
before mutation. Public `server.JavaProfiles` distinguishes admission from
complete content representation.

The Java frontend still owns version-specific replication. Typed entity
metadata helpers use measured accessor schemas; legacy sound, particle, damage,
equipment and block-entity builders use a transitional presentation adapter
before FIFO admission. Already-profiled slots and blocks are not projected
twice. Projection is pure and per viewer: mandatory failure retires that
recipient, and partial packet buffers never enter its queue. Item ingress
rejects unknown or unmodeled semantic component data before mutation. Particle
commands support measured option forms and simple item templates; unmeasured
inline or component-bearing item options reject explicitly.

The public avatar capability covers player presence and movement only. It has
no inventory, combat, survival simulation, persistence or world/chunk replication
through the avatar connection. The separate finite block capability omits light,
block-entity/NBT data, entities, chunk protocols and moving view windows.
Gameplay selectors remain Java-session based; explicit plugin ID enforcement
can target an admitted avatar. Presence-only entities do not activate inside-
block/tripwire gameplay, environmental movement or dimension parking. A separate
external consumer module exercises admission and lifetime against actual 775
and 776 TCP observers without importing internal packages. This is not a
complete neutral game frontend.
Gameplay rules remain 26.2. Five newer attributes are omitted for 775, which
does not establish equivalent older-client prediction or full 26.1 parity.

[`api/plugin`](api/plugin/plugin.go) exposes copied query values, commands,
observations, movement hooks and queued enforcement. API version 1 and
optional dependency metadata are validated before plugin initialization.
A manager cancels plugin resources and closes initialized plugins in
reverse dependency order; partial initialization also rolls back. Legacy
internal import aliases preserve existing in-tree plugin compatibility.
Plugin-owned goroutines must cooperate with `Context` cancellation and
`Close`; the manager does not discover or forcibly stop them.
Admission hooks are deny-only decisions before reservation. Movement proposals
carry immutable incarnation IDs; additive typed join/leave observations carry
detached states. Existing join/leave topics retain their legacy map payloads.
Queued name-based enforcement
captures the live ID when accepted, then checks that exact incarnation and plugin
lifetime when executed. Acceptance is
not confirmation that a mutation ran. Narrow panic boundaries cover plugin
initialization, individual cleanup, commands, hooks and external frontend
`Serve`; panic failure replies are generic and private logs retain attribution.
Plugins remain trusted code, without arbitrary goroutine recovery, execution timeouts,
dynamic loading or a security sandbox.

[`api/registry`](api/registry/state.go) represents complete namespaced block
states with sorted properties. Immutable catalogs assign dense, catalog-local
32-bit handles; adapters translate them through immutable numeric projections.
Builtin handles retain historical order for existing generated behavior tables.
The 776 block projection is complete. The measured 775 projection represents
29,873 states, including 5,186 renumbered values, and lacks 2,493 newer states.
Missing states reject by default; explicit visual fallback changes only a
viewer's encoded presentation. A mapping from another catalog cannot be used
to interpret the runtime's handles.

JSON world override saves now persist canonical keys in format version 2.
Version 1 loads through the historical 776 projection before resolving keys
into the current catalog. Unknown states reject before replacing overrides.
The change covers this JSON format; native region files continue using their
existing names/properties codec. Immutable name catalogs now identify items and
components, and adapter-owned tables project builtin and configured numeric
domains. Legacy item JSON numeric saves still require a separate migration;
these wire boundaries do not make every saved numeric field catalog-portable.

The event bus supplies lossy, asynchronous observations of committed
actions. It cannot cancel or modify gameplay. Movement hooks are separate
synchronous decisions before commit: lower priorities run first, ties use
registration order, cancellation stops dispatch and a panic rejects the
move. Hook changes are validated again before session/entity state or
movement observations are committed. Hooks must return promptly and must
not retain proposal pointers or wait on another simulation action; plugin
enforcement requests are queued to avoid recursive ownership.

## Slow clients

Each Java session has a FIFO writer queue bounded by both packet count and
bytes. Defaults are 256 packets, 8 MiB and a five-second socket write
deadline, configurable before startup. The budgets include the active
write. Enqueue never waits for a socket or queue space; exceeding a budget
closes that client because omitting ordered game-state packets would make
its view inconsistent. Cleanup releases queued payloads and wakes readers.
Compression changes use the same FIFO. The protocol writer serializes
complete frames, including threshold changes, and rejects short writes.

The chunk encoder defaults to up to four workers, 128 jobs and 64 MiB of
reserved/queued/running asynchronous snapshot inputs. An unresolved result
occupies its packet's FIFO position; later deltas and dimension transitions
cannot overtake it. Ready results consume the session's output budget.
Queued cancellation releases its input reservation; running work retains its
reservation until it finishes. Shutdown wakes future waiters and joins workers.

Temporary encoder saturation defers ordinary batches without consuming pending
columns or pacing credit. Initial join preserves its packet ordering using a
synchronous fallback that holds one additional owner-side snapshot at a time.
That fallback, capture temporaries, encoded outputs and GC overhead are outside
the asynchronous input quota; the quota is not a total heap cap. Light capture
reservation includes both edge sections and copied block-entity NBT.

The deterministic default-budget test queues the combined configuration
and play join burst without a writer: 68 packets and 227,844 payload bytes
at view distance 10 in the tested flat-world fixture. This checks startup
headroom for that fixture, not every possible join, modded registry or
production backlog.

## Tracking and performance evidence

Movement reevaluation still visits all entities for one moving player. It
now builds a pass-local lookup from entity ID to live player session once,
instead of scanning every session for every entity. This reduces that
lookup portion from O(entities × players) to O(entities + players), keeps
live positions and the range/dimension/delivered-chunk predicate, and adds
no shared cache lifecycle. It is not a spatial index or parallel tracker.

The tracking benchmark compares the frozen previous scan, kept only in a
test file, with the new lookup under identical steady pairings. World read
and chunk encoding benchmarks establish separate costs. Reproduce with
the supported Go toolchain:

```sh
go test ./internal/server -run '^$' -bench 'Benchmark(RetrackSession|TrackingWorldRead|TrackingChunkEncoding)$' -benchmem -benchtime=300ms -count=3
```

A historical Linux/amd64 Go 1.27.3 sample at the `6e28441` foundation,
on an AMD EPYC 9V74 with
`GOMAXPROCS=5` and concurrent development load, produced these
three-sample medians:

| Entities / players | Previous scan | Pass-local lookup | Previous bytes / allocations | Lookup bytes / allocations |
| --- | ---: | ---: | ---: | ---: |
| 128 / 8 | 76.9 µs | 26.5 µs | 40,064 / 9 | 1,152 / 1 |
| 1,024 / 64 | 1.965 ms | 0.380 ms | 320,768 / 65 | 13,928 / 10 |
| 8,192 / 256 | 44.38 ms | 1.438 ms | 1,310,720 / 257 | 84,392 / 14 |

The world-read sample median was 984 ns, 128 bytes and one allocation.
The chunk-encoding sample median was 4.40 ms for an 11,380-byte packet;
allocation counts varied across samples. Scheduling contention produced
wide timing variation, so these measurements are illustrative and should
be repeated on an idle machine before comparing small changes. Synthetic
state, stable pairings and no sockets or concurrent tick load do not
establish player capacity, sustained TPS or production tail latency.

The canonical block handle increases state storage from two to four bytes.
A 16×384×16 destination grows from 196,608 to 393,216 bytes. With unchanged
block-column routing, the same FillBox fixture's allocated bytes grew from
about 215,600 to 423,200 bytes per call; sampled median time was 0.784 versus
0.849 ms. These measurements include worker-response allocation and exclude
the preallocated destination. A chunk-aligned routing experiment increased
the single-chunk median to 3.706 ms and was reverted; storage remains routed
by block-column coordinates.

Final four-byte snapshot benchmarks, using a mixed-block fixture with skylight
disabled, measured these three-sample medians at 200 ms per sample:

| Operation | Elapsed per operation | Allocated bytes | Allocations |
| --- | ---: | ---: | ---: |
| Capture on owner | 0.981 ms | 816,777 | 106 |
| Encode detached snapshot | 0.384 ms | 90,816 | 1,021 |
| Capture and submit on owner | 0.978 ms | measured without worker completion | measured without worker completion |
| Capture and encode synchronously | 1.491 ms | 907,566 | 1,127 |

Capture-and-submit owner elapsed time was about 34% below synchronous work
in this fixture. It excludes completion wait and does not establish lower
total CPU, client latency, sustained TPS or player capacity. Cold lighting
materialization remains owner-side. The single-valued palette fast path is
an independent encoding allocation/CPU improvement. Reproduce with:

```sh
go test ./internal/server -run '^$' -bench '^BenchmarkChunkSnapshot$' -benchmem -benchtime=200ms -count=3
go test ./internal/world -run '^$' -bench '^BenchmarkChunkRouting' -benchmem -benchtime=300ms -count=3
```

## Conformance and provenance

The public [`conformance`](conformance/README.md) runner and
`cmd/mc-conformance` use declarative endpoint/version/capability manifests,
explicit pass/fail/unsupported/infrastructure results and bounded framed
wire transcripts. Its 775 and 776 smoke profiles check status, ping, login,
configuration/join and rejection/liveness contracts. They account for the
measured session-ID and Join Game field differences. Deliberately broken
independent TCP controls test its ability to detect failures. It does not
provide a complete protocol grammar or certify all gameplay parity.

Differential tests compare observable behavior with the pinned vanilla
jar. Scripted client tests cover implementation-specific contracts. The
blackbox package is discoverable by ordinary `go test`; oracle comparisons
still require explicit environment opt-in and local prerequisites. No
test silently downloads an oracle. Missing prerequisites produce explicit
skips, so record which comparisons actually ran when reporting coverage.

The Go implementation is independently written and uses the standard
library. No copied Mojang source, decompiled source, disassembly or copied
implementation structure belongs in it. Existing repository provenance
is broader than a formal independent-team clean-room process:
[AGENTS.md](AGENTS.md) historically permits `javap` measurement of
constants, ordering and branch behavior, while generators including
`genfoods` and `genpotions` extract factual tables from that output. Other
generators consume official reports and bundled datapacks. The jar has
therefore not been used exclusively as a running black-box oracle or a
datapack source. Disclosing those inputs does not authorize copying or
translating implementation structure, or audit every preexisting artifact.

New measurement and generation slices record source class, tool/version,
input checksum when applicable, independently written implementation and
the corresponding differential or scripted test. A complete provenance
manifest for existing generated artifacts remains future work.

New 775 block projection data is generated by `tools/genblockmapping` from
the official 26.1 and 26.2 datagen block reports. Its header and measurement
manifest record report/JAR hashes and exact/missing/renumbered counts.
Named wire fixtures carry their own measurement provenance. Current item,
entity-accessor and recipe slices also record official runtime reflection and
public stream-codec observations with independently selected values, oracle
checksums and probe hashes. These are factual observations, not copied or
translated implementation structure.

Publication validation also builds a clean `git archive` of the committed
tree and runs a separate consumer module importing `server.New`,
`server.JavaProfiles` and `api/registry`. This catches source packages present
in a working tree but accidentally excluded by ignore rules. The `/server`
binary ignore has a directory-only exception for the public Go package.

## Remaining stages

1. Audit remaining concrete-server mutation entry points and pointer
   escapes. Retain one simulation owner until independent region ownership
   is demonstrated.
2. Introduce region-owned state, deterministic cross-region messages,
   entity migration handoffs and barriers for global systems. Benchmark
   realistic mixed loads before introducing parallel workers.
3. Migrate remaining persisted numeric domains, including legacy item JSON,
   and broaden measured coverage of unsupported cross-version content.
4. Replace more Java packet-shaped presentation paths with typed core events,
   extend plugin mutation APIs beyond current admission/movement/edit decisions
   and queued enforcement, and broaden finite block views to richer world/entity
   replication and gameplay capabilities beyond transient presence/movement.
5. Add sustained load scenarios with tick p50/p95/p99, queue bytes,
   disconnect reasons, CPU, heap, allocation rate and tracked entity count.
6. Expand differential gameplay coverage and the conformance corpus;
   retain per-slice evidence and explicit unsupported behavior.
