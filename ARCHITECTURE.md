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
separate from simulation ownership. Initial Java joins now wait outside the
owner while a bounded runtime scheduler prepares their destination caches in
separate owner actions. Each turn completes one cold facts column or one whole
light initialization/flood, or checks at most 64 already warm entries. Sessions
remain in Configuration until preparation completes; the existing atomic Join,
opening chunk batch, attributes, entities and admission transaction then runs.
Join readiness tests distinguish this preparation and the complete streamed
window from merely receiving Play Login. Movement and teleport windows still
warm section and sky data synchronously before encoder submission, so a cold
teleport can spend time on the owner before any encoder worker is involved.
Chunk packet encoding can run on bounded workers after owner-side capture
of detached block states, block-entity NBT, light bytes and dimension scalars.
Encoder functions receive no live gameplay objects; completion updates
synchronized transport accounting.

Java wall-gameplay ticks require both Play phase and committed active admission.
Configuring, unadmitted and retired sessions do not accrue attack charge or
advance digging, hunger, combat, eating or portal state. Transport keepalive
and connection lifetime work retain their separate wall-clock behavior.

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

Fresh skylight columns now share one detached full-column read when preparing
random-tick flags, non-air counts and sky tops. The three predicates remain
independent, and repeated canonical states share classification only within
that analysis. Publication fills absent entries while preserving existing
incremental light values. Warm columns and section-local flag invalidation
retain their existing paths; skylight-disabled dimensions still prepare flags
alone. This adds no halo preparation, light-flood concurrency or persistent
cache, and does not add invalidation for raw `World.Restore` calls that bypass
the existing light-write pipeline. Preparation and light materialization still
run synchronously under simulation ownership.

The matching before/after harness is
`internal/server/column_preparation_bench_test.go`, SHA-256
`d4787091a42c78a82d6dbacb3abf5d384d30336c2e30b81d438251f4e60d5ff3`.
The baseline is a clean archive of `ab791d032a4e6b1ca5864c44b5405fde676f96e4`
with only that benchmark added. Sequential five-sample runs used Linux/amd64,
Go 1.27.3, AMD EPYC 9V74 and `GOMAXPROCS=5`, at 200 ms per sample. Both use the
same flat world with a fixed 64-cell stone/grass/glass/water canopy and eight
world shards. Cold resets dimension and section caches over that immutable
world, excluding worker construction and package codec initialization. The
materialization control shares precomputed immutable neighboring count/top
facts so its excluded setup does not dominate benchmark calibration.

| Operation | Baseline median (range) | Fused median (range) | Baseline bytes / allocations | Fused bytes / allocations |
| --- | ---: | ---: | ---: | ---: |
| Cold column facts | 9.355 ms (9.099–10.793) | 0.951 ms (0.888–1.076) | 2,371,012 / 2,438 | 822,029 / 111 |
| Warm column facts | 834 ns (789–883) | 815 ns (789–837) | 0 / 0 | 0 / 0 |
| One invalidated flag section | 142 µs (121–175) | 103 µs (101–123) | 30,512 / 93 | 30,512 / 93 |
| Cold sky tops alone | 6.138 ms (5.700–7.832) | 5.225 ms (4.970–5.325) | 817,717 / 97 | 817,714 / 97 |
| Cold non-air counts alone | 1.531 ms (1.284–3.414) | 0.952 ms (0.908–1.061) | 817,817 / 103 | 817,816 / 103 |
| Sky materialization, facts warm | 1.592 ms (1.523–1.680) | 1.550 ms (1.533–1.750) | 270,320 / 1,063 | 270,320 / 1,063 |
| Cold skylight window and opening batch | 1.333 s (1.215–2.891) | 0.834 s (0.814–0.846) | 331,396,912 / 183,821 | 253,638,888 / 68,969 |
| Warm skylight window and opening batch | 12.613 ms (10.786–46.487) | 11.831 ms (10.898–14.654) | 8,190,824 / 5,851 | 8,190,826 / 5,851 |
| Cold skyless window and opening batch | 133.175 ms (127.135–145.938) | 130.254 ms (123.543–146.407) | 45,151,088 / 115,918 | 45,150,752 / 115,915 |
| Warm skyless window and opening batch | 11.909 ms (11.504–12.510) | 11.606 ms (10.955–13.024) | 7,835,287 / 5,734 | 7,835,282 / 5,734 |

The window fixture uses radius 2: enumeration and synchronous preparation of
the 49 fresh view columns and existing light margin, followed by capture,
completed synchronous encoding and outbound admission of the nine-column
opening batch. It excludes socket delivery, encoder workers, concurrent ticks
and the remaining paced batches. Both versions emit exactly 86,024 opening
chunk-body bytes with skylight and 30,602 without it. The cold skylight median
fell about 37.5%, with about 23.5% fewer allocated bytes and 62.5% fewer
allocations in this fixture. Unchanged skyline/count-only control code and
wide overlapping warm/skyless ranges expose scheduling variation; their timing
differences are not additional optimization claims. These are bounded operation
measurements, not sustained TPS, client latency or player-capacity evidence.

```sh
GOMAXPROCS=5 go test ./internal/server -run '^$' -bench '^(BenchmarkColumnPreparation|BenchmarkSendWindowPreparation)$' -benchmem -benchtime=200ms -count=5
```

Initial Java join preparation now trades longer cold-join readiness for shorter
observer delays in a measured two-observer fixture. One runtime scheduler owns
at most 32 reserved/building/queued/running jobs and runs independently of the
game tick, including while frozen. Jobs rotate after complete owner actions;
there is no parallel simulation, mid-action unlock or preemption of plugin
callbacks. Each column initialization and its flood remain atomic, while valid
initialization light marks can reach existing viewers earlier between units or
in different batches; final canonical light and block state remain authoritative.
Plans use the admitted client radius, at most 32. Their two coordinate
slice capacities total at most 9,530 entries, or 152,480 bytes on a 64-bit host,
plus a temporary 5,041-byte deduplication grid. This bounds scheduler coordinate
storage, not total heap; allocator overhead, receipts, callers and cache contents
are separate. Completion releases plan arrays before signaling its receipt.

Preparation captures only destination routing facts, without restoring session
state or creating an entity. Final ownership rechecks destination, view radius,
dimension and incarnation before the existing atomic join transaction. Changes
permit at most three replans after the initial plan. Final admission retains
ordinary cache rechecks and can refill facts invalidated during preparation.
Saturation, invalid targets,
exhausted replanning and known cancellation close the connection without adding
an invented pre-Join packet. Host/session/runtime cancellation stops between
whole units; a running unit retains its slot until it finishes. Remote peer EOF
is still detected only when the existing packet reader resumes. Shutdown joins
the scheduler before retiring world workers. Live teleports, movement, saves and
other long gameplay actions retain their existing synchronous behavior.

The unchanged public TCP harness is
`tests/blackbox/cold_join_responsiveness_test.go`, SHA-256
`b529308fcde7e4fbbc912c1134f1cfec366a2c446850a6565c76318f812b5272`.
Its baseline is a clean archive of
`b4e3e692e0ed3d492a60bf67deded1dbad4e6b3a` with only that file added.
Five sequential baseline and five optimized runs used the same flat world,
radius 2, initially ready 775/776 observers, a distant untouched world spawn,
and new 776 cold/warm joiners. Observers issue one token request at a time with
20 ms pacing. The first 775 request also edits one stone cell; that separately
labeled mutation/fence is excluded from token quantiles. Timings include socket
transport, command processing, ticks, decoding and client scheduling. Raw start,
end and readiness timestamps are retained; quantiles below use nearest rank on
pooled token-only samples across the five runs.

| Operation | Baseline | Scheduled preparation |
| --- | ---: | ---: |
| Cold full-49-column readiness median (range) | 865.5 ms (861.9–884.1) | 992.5 ms (980.0–1,052.6) |
| Warm full-49-column readiness median (range) | 61.0 ms (59.4–72.9) | 60.8 ms (53.4–65.6) |
| Cold 775 token count / completed before readiness | 9 / 9 | 175 / 172 |
| Cold 775 token p50 / p95 / maximum | 743.4 / 821.5 / 821.5 ms | 5.66 / 16.24 / 45.68 ms |
| Cold 776 token count / completed before readiness | 14 / 14 | 183 / 181 |
| Cold 776 token p50 / p95 / maximum | 39.79 / 821.59 / 821.59 ms | 5.33 / 30.11 / 45.68 ms |
| Warm 775 token count / p95 / maximum | 11 / 44.44 / 44.44 ms | 11 / 39.24 / 39.24 ms |
| Warm 776 token count / p95 / maximum | 11 / 44.51 / 44.51 ms | 11 / 39.66 / 39.66 ms |

Cold readiness increased about 14.7% while substantially more observer work
completed. This is a responsiveness tradeoff, not a preparation-speed result.
One outstanding request means baseline stalls yield fewer samples, so this is
not a fixed offered-rate capacity test. Warm ranges overlap. The separate
mutation/fence median is 38.01 versus 40.14 ms, with ranges 35.32–41.23 versus
34.57–41.04 ms. Both protocols independently verify final block/heightmap/light
contents and the required bootstrap order. Deterministic tests separately prove
real public avatar movement and block edits complete while preparation remains
unfinished; the timing harness alone does not prove the exact overlap point.

The optimized-only quantum benchmark uses
`internal/server/join_preparation_bench_test.go`, SHA-256
`950293380e9f4bc662e3436dcaffd00d040a8e3b70cf7b36a3835b493c94b9c1`.
It has no observer, sockets, ticks or pending deferred work. Three 100 ms samples
of the same mixed-canopy radius-2 fixture perform 49 facts units, 121 whole light
units and one completion turn. Total preparation median is 854.6 ms
(828.4–873.2). Work timing begins after ownership acquisition and ends at the
helper return: observed maxima are 4.628 ms for facts and 16.622 ms for light.
Complete uncontended turn wall time additionally includes acquisition and the
empty deferred-action/stream-flush boundary; maxima are 4.630 and 16.627 ms.
These are separate measurements, neither a contended owner-hold nor tick bound.
Whole light floods, callbacks and other actions can still exceed these samples.

Actual warm scheduler reservation/submission/worker completion takes a median
45.54 µs (44.58–47.93), 288 bytes and two allocations. Plan construction is
separate: radius 2 takes 6.237 µs and 3,160 bytes/six allocations; radius 32 takes
0.771 ms and 161,112 bytes/six allocations. These measure added components,
not the total warm-join overhead. All measurements used Linux/amd64 Go 1.27.3,
AMD EPYC 9V74, `GOMAXPROCS=5` and archive-compatible `GOFLAGS=-buildvcs=false`.
They establish no hard latency guarantee, sustained TPS or player-capacity claim.

```sh
MCGO_COLD_JOIN_REPORT=/tmp/join-probes.jsonl GOMAXPROCS=5 go test ./tests/blackbox -run '^TestColdJoinResponsiveness$' -count=5 -timeout=5m -v
GOMAXPROCS=5 go test ./internal/server -run '^$' -bench '^BenchmarkJoinPreparation(Quantum|WarmAdmission|Plan)$' -benchmem -benchtime=100ms -count=3
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
