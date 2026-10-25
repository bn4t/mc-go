# Runtime foundations and evidence boundaries

mc-go targets Minecraft Java 26.2 (protocol 776) behavioral parity. The
current work establishes ownership, lifecycle, canonical block identities,
detached chunk encoding, extension and measurement boundaries. Full parity,
parallel region simulation, a protocol-independent game model and a second
production game protocol remain unfinished.
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
privileged command actions for existing players. The action boundary
checks cancellation and shutdown under simulation ownership before
dispatch; cancellation does not roll back an admitted action. Movement
shares Java's validation, collision, hooks and canonical commit path.
Custom frontends must stop their own transport workers when their supplied
context ends; the runtime cannot forcibly join arbitrary adapter goroutines.

The bundled Java frontend uses immutable profiles to select codecs,
gameplay dispatch and block projections. Protocol 776 is admitted. The
775 candidate has measured login/join codecs and a partial block projection;
its missing configuration, item, entity and other domain mappings keep
login and gameplay dispatch disabled. Public `server.JavaProfiles` reports
these gates separately from `SupportedVersions`.

The Java frontend still has substantial version-specific registry and
replication code. The neutral API does
not yet support player admission, world/chunk replication or canonical
entity/item/block mappings. A synthetic second adapter observes Java
client movement and submits changes seen by that client; this proves the
shared action seam, not support for a second real game protocol.

[`api/plugin`](api/plugin/plugin.go) exposes copied query values, commands,
observations, movement hooks and queued enforcement. API version 1 and
optional dependency metadata are validated before plugin initialization.
A manager cancels plugin resources and closes initialized plugins in
reverse dependency order; partial initialization also rolls back. Legacy
internal import aliases preserve existing in-tree plugin compatibility.
Plugin-owned goroutines must cooperate with `Context` cancellation and
`Close`; the manager does not discover or forcibly stop them.

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
existing names/properties codec. Items, entities and other numeric domains
still need equivalent canonical boundaries.

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
Named minimal login/join wire fixtures carry their own measurement provenance.

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
3. Extend canonical keys and adapter-owned mappings to items, entities and
   other numeric domains; complete the remaining protocol admission gates.
4. Route more Java gameplay through the neutral boundary and implement a
   second real protocol, including admission and world replication.
5. Add sustained load scenarios with tick p50/p95/p99, queue bytes,
   disconnect reasons, CPU, heap, allocation rate and tracked entity count.
6. Expand differential gameplay coverage and the conformance corpus;
   retain per-slice evidence and explicit unsupported behavior.
