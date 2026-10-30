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
persistence snapshots and ticks use that ownership boundary. Public compact
JSON `SaveWorld` captures overrides under ownership, then sorts, serializes and
writes the detached snapshot outside it while the caller waits. Public
`SaveServerData` likewise captures player/block-entity JSON before writing
outside ownership. Public native `WorldDir` saves likewise detach world, entity
and player state before writing. Capture still pauses gameplay; `/save-all` and
owned disconnect saves also wait for writes under ownership.
Internal helpers whose names end in
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
window from merely receiving Play Login. Live movement and teleport windows
share that scheduler, preserving whole gameplay transactions while preparing
their destination in separate owned units before final stream publication.
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

Shared effective-attribute evaluation applies additive modifiers first, uses
that sum as the common base for every base multiplier, then applies total
multipliers. Combat and noncombat query controls pin the operation passes.
Player equipment detection materializes generated main-hand and slot-qualified
armor modifiers in the canonical attribute map. Per-slot snapshots preserve
explicit removals until that slot changes; a new life reapplies retained gear.
Initialized attack, armor and toughness consumers use that map. Raw dirty
attributes reach owner and tracked observers through the shared serializer.
Equipment modifiers are nonpermanent, including zero toughness rows. Initialized
player knockback also evaluates canonical resistance with generated bounds;
only detached/uninitialized sessions retain inventory synthesis. Arbitrary
item attribute components and general nested-component identity remain partial.
Player health consumers narrow the effective ranged maximum to float32 and
share bounded float healing. Late living-frame refresh clamps excess health;
presence-only avatars remain outside survival simulation.
Infinite periodic effects use signed living entity age, with separate runtime
receipts preventing repeated callbacks while that age is stationary. Finite
remaining-duration alignment is unchanged. Ordinary respawn creates a fresh
player body clock; live dimension transfer preserves it.
Within-pass floating-point ordering and each gameplay consumer's integration
remain separate coverage; this does not make every attribute consumer complete.

Player current collision scale is simulation-owned state on the canonical player
record. Late player-frame work refreshes it after touch processing; actual pose
changes also refresh dimensions. New physical IDs and respawn start with constructor
scale1, while an existing record's dimension transfer preserves current bounds.
Hypothetical pose fitting uses live effective attributes. Bed/wake, effect-triggered
refresh and all cold lifecycle combinations remain separate coverage. Batched wire
outcomes do not directly establish action timing within command counter windows.

NoGravity metadata is a shared native base field: pairing packs non-default true
without consuming its pending dirty value, and tracker updates emit explicit false
resets. Static families use the existing vehicle pass as a fallback; capability-only
presence records are excluded. Nonliving base-only updates do not acquire living
health fields. Initial join skips entities already paired through chunk delivery.
These metadata changes do not complete gravity/travel or generic movement.

Retracting piston source-base movement has measured routes for default-size
NoGravity armor stands and ordinary stands/cows. Physical carried-head geometry
differs from the live world-collision shape. Ordinary east-facing floor controls
retain the source base's internal occupied-cell planes during directional block
clipping and reverse ejection. NoGravity/NoAI cows also have six-axis
normal-collision pins. General entity move policies, vertical gravity/travel,
step-up, cumulative piston limits and scaled variants remain separate.

Embedded regular items apply fluid/gravity acceleration before closest-space
escape velocity, then move without collision and apply float air drag/friction.
Restitution uses zero effective gravity for NoGravity entities, including the
remaining fraction across a retained contact gap; item controls pin this path.
The same float arithmetic applies to ordinary items. Five full-block single-exit
chambers with and without gravity pin direction, bounds and motion/displacement
relations. Item neighbor selection now checks occupied collision volume in the
live world, including partial blocks and measured piston/shulker contexts. A
32366-state empty-context census pins the independent full-cube predicate;
thirteen tie/near-tie controls retain existing direction priority. Orb eligibility
keeps its material policy. Random sequence, exhaustive no-exit/embedded-fluid
combinations, initially grounded cases and XP-orb movement remain incomplete.

Opening shulker lids push supported native dropped items, armor stands and cows.
Six-axis NoGravity apparatus and wall controls distinguish stand noPhysics
bypass from cow block clipping; Marker and capability-only records are excluded.
Gravity/travel, scaled variants, existing velocity, hard entity collisions, other
move policies and actual player displacement remain separate measurements.

Vehicle cargo menus bind to the live entity inventory rather than a synthetic
block position. Native saves and data reloads share that inventory; reloads
preserve its identity and queue updates for current viewers. Validity follows
the current entity bounds, dimension and player reach. Chest boats offer one
passenger seat before ordinary inventory access. The kill-command removal path
spills current cargo before removing the entity, independently of entity_drops.
Item commands use the same cargo slots, capacity bounds and lazy loot path,
including existing hotbar/inventory slot aliases and shared-viewer updates.
Creative attack removal and survival destruction also spill cargo; entity_drops
gates only the survival vehicle item. Other damage causes and complete vehicle
runtime behavior remain separate work.

## Gameplay resources and callbacks

Startup advancement definitions belong to each Server. The built-in generated
metadata remains immutable; folder-pack overrides feed that server's
command lookup, modeled trigger evaluators, visibility tree and wire definitions.
Resource identity is separate from tree attachment: a valid definition with a
missing or cyclic parent can still be granted and rewarded without becoming a
visible tree node. Completion is recorded before reward callbacks, then rewards
run XP, loot, recipes and function before the completion announcement. Functions
use the rewarded player's source at permission level two. This covers startup
folder packs and modeled triggers. Displayed definitions use a runtime codec and
independent tidy-tree layout. Overrides reflow both affected tabs, including
displayless replacements; built-in wire data retains its captured icon/component
bytes while coordinates are rewritten. Complete component schemas, pack selection,
ZIP/reload and custom loot overlays are still separate work.

Scheduled file functions use a simulation-owned world-age queue independent
of block/fluid deadlines. Name plus deadline defines event identity; an event
is removed before callback dispatch so clear/replace/self-rescheduling are safe.
Execution resolves the startup registry with permission two, an Overworld
server source at the spawn block and no entity executor. Summon can resolve
that command frame directly. Native saves capture detached scheduled-event
containers under simulation ownership; background writers never read the live
queue. Startup restores event keys separately from callback targets/types and
resolves current functions or function tags on dispatch. Startup tags use
required-first dependency resolution, optional acyclic ordering and first-member
deduplication. Load/tick entry points run before world-age advancement; frozen
player ticks do not invoke them. Nonempty no-return function calls do not
complete execute-store consumers; empty tags report failure and numeric zero.
Full function syntax compilation, complex optional-cycle ordering, macros,
returns, command-sequence limits and equal-deadline wire ordering remain open.

Custom tick criteria fire on connection ticks after menu validation and before
inventory touching, including while the world is frozen. Startup compilation
caches supported player/entity/context predicates in the server registry.
Unevaluated valid conditions remain distinct from false, so inversion cannot
turn them into awards. Biome predicates query the current world; native NBT
views share player Tags. Advancement progress predicates reuse the locked
server-owned completion/criterion matcher, including custom resources. Numeric
fields retain exact JSON tokens where their
codecs consume integers, separately from typed NBT conversion.
Replacing either captured startup recipe advancement defers the first reset
until runtime visibility/progress is derived; replacing crafting_table also
suppresses its captured recipe unlock. Complete player-predicate evaluation
and reconciliation of saved criteria after pack changes remain separate work.

Jukebox state has one typed owner across command hydration, native disk data
and interaction. Migration retires legacy generic backing so an ejected record
cannot reappear. A private record codec preserves modeled persistent components and explicit
empty-container presence. Identity excludes root count/client selection and
normalizes measured item prototype defaults; optional elapsed-time omission
can retain an existing playback clock.
Content ejection and block-entity retirement are separate effects, allowing
command flags to suppress drops while still sending the stop notification.
Interaction ejection stops playback before spawning the record; block-update
packets can arrive later through their separate batch. General component
presence/removal semantics and nested prototype normalization remain partial.

Tool transformations use an explicit compatible block-entity write policy.
The same live storage object remains available during on-place and neighbor
callbacks; command replacements retain their separate retirement semantics.
Open copper-chest viewers use storage identity plus compatible owner states.
Transformation tables come from the reproducible tools/gentooltransforms
registry-fact generator; effect recipients are distinct from state/cost behavior.

Structure editors, powered structure blocks and jigsaw generation reuse the
runtime's native template repository and placement path. Placement options keep
shape handling separate from side-effect suppression, and preserve distinct
integrity and placement random streams. Powered SAVE captures in memory; editor
SAVE can write native templates. Powered LOAD bypasses the editor's size-preparation
step. Generation reuses worldgen pool planning and processors; feature-pool
placement, non-overworld planner context and complete pack resolution remain
unverified or incomplete. These mutable resources and placement actions retain
the simulation ownership boundary; they do not add parallel gameplay ownership.

Test-block editors preserve native mode, message and powered fields. Test-instance
controller packets preserve typed records and error markers, answer unresolved-test
initialization/query requests, and support editing, default barrier-shell reset
and AIR-skipping native SAVE. SAVE can use an unregistered selected resource key;
normal-server export reports that exporting is disabled. Barrier cleanup visits shell faces
rather than the full volume, without adding a size cap to native integer dimensions.
The captured registered test adds exact description/size queries, template-name
resolution for SAVE, and rotated volume clearing/placement for RESET. Its built-in
`always_pass` RUN prepares the controller and barrier shell, stays frozen with the
world, and finishes on the third world tick. Mutable template and runner state
belong to each server. Startup test/environment catalogs are server-owned;
profile-specific overlays preserve captured rows and append sorted custom keys
for protocols 775/776. Function/block-based definitions use canonical defaults;
difficulty/weather/builtin-clock environments compose with all_of. START emits power;
receiver edges retain transient activation across native merges, but not saves.
ACCEPT precedes FAIL. Success clears the enclosure immediately; failures publish
on the next world tick and retain it. Setup and timeout use world ticks.
Difficulty/weather activate immediately when RUN is accepted. Activation captures
prior effective settings; all_of restores children in reverse order, and completion
or accepted replacement retires each activation once. Weather restores a preset
chosen from effective rain/thunder, rather than the previous flags/countdown.
Freeze pauses test progression and weather interpolation, but not environment setup.
Weather state preserves float32 recurrence. Ongoing level packets carry rain/raw
thunder; initial join sends effective thunder (rain × thunder). Continuous level
broadcasts are dimension-filtered; threshold transitions use native global routing.
Native Weather.Type.apply writes global server weather while capture uses the
initiating level's effective weather (clear in non-weather dimensions). This slice's
wire coverage is Overworld; cross-dimension routing remains unpinned. Random cycles,
complete timers, rejected RUN during an active environment and shutdown cleanup
remain partial or unpinned.
Other functions/environments, holder references within all_of, LOG reports, retries,
pack selection/ZIP/reload,
nondefault padding/sky access/definition rotation, oversized regions and complete
text validation remain partial. Active geometry edits, force-load cleanup, exact
live/global placement RNG interleaving and outside notifications during strict
clearing remain unpinned.

Command blocks and command minecarts share execution mechanics while supplying
different spatial and entity sources. Result callbacks must read the current
source fields after each successful command leaf: a command can reload its own
NBT or replace its block while executing. Feedback occurs before the success
callback, and initial tracking eligibility is distinct from the subsequently
loaded fields. Command-block data reload therefore preserves the executing
block-entity identity and the accessor's original target type and coordinates.

Item-on-block dispatch uses the selected hand and explicit inventory slot.
Block-item callbacks precede main-hand default interactions and the item-use
wrapper. Creative costs depend on which path succeeds: measured hive bottle
harvesting shrinks the stack inside the block callback, while ordinary
creative item uses preserve it. Native jukebox records feed the same typed
store used by interactions, ticking, data queries and disk chunk serialization;
client chunk enumeration is separate and must be kept in sync. Full component
and song lifecycle parity is still under development.

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
the scheduler before retiring world workers. Native world/player files, owned
command/disconnect saves and other long actions retain synchronous behavior;
public compact saves and live-window preparation are described separately below.

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

## Live window preparation

Already admitted Java players share the initial-join preparation scheduler and
its global limit of 32 reserved/building/queued/running jobs. Movement, teleport,
respawn and dimension-change transactions commit their existing pose and control
packets atomically, then stage a detached desired window. New columns enter
neither the tracking sets nor the pending stream until preparation completes.
While a live job exists, tick and acknowledgement pumps pause the old pending
queue without spending its batch credits. Already admitted asynchronous chunk
futures keep their original outbound FIFO positions.

Each session retains one job and its latest scalar target. A generation changes
for target changes, reset and retirement, so an A-to-B-to-A sequence cannot
publish the first A plan. Retargeting retains its existing reservation and
releases old coordinate arrays before building replacements. Reset invalidates
the target while retaining that slot for a replacement in the same owned
transaction; retirement cancels it. An identical already published target uses
the existing pump/prune path without reserving a job. Saturation closes only
the requesting recipient and has no synchronous cold fallback.

Ordinary turns check at most 64 warm entries and perform at most one complete
cold facts-column scan or light initialization/flood. Final metadata checks may
visit the entire bounded plan, refill at most one invalidated facts column and
yield again. Continual edits can delay completion. The final owned transaction
revalidates the session incarnation, dimension and generation before publishing
tracking sets and performing the existing append/pump/prune sequence. Terminal
live scheduler bookkeeping retires the reservation before deferred actions run,
so a successor staged at that boundary can reserve its own job. Running
cancellation retains its quota until the whole unit returns; shutdown joins the
shared worker before world workers retire.

This preserves mc-go's historical pending FIFO and batch-before-forget order.
That order is not an exact vanilla ordering claim: both new wire measurements
observe old-window forgets before destination batches. New-dimension entities
remain gated on delivered chunks. Whole light floods, metadata scans, final
snapshot capture, plugin callbacks and unrelated gameplay work have no hard
deadline; simulation remains serial and cache preparation remains owner work.

The public TCP comparison uses
`tests/blackbox/live_window_responsiveness_test.go`, SHA-256
`c41f3b7d7c5265e54c79488dad7ffe9be6ab16c59bacb04f902f1c87601178b4`.
The baseline is a clean archive of
`d837805394d025626654fa07e26b9d87e5884fce` with only that test added.
Five sequential fresh-server runs per implementation use ready 775/776
observers and a ready 776 mover, radius two and an untouched destination at
chunk center (100, 100). Each observer issues one token request at a time and
waits 20 ms after each response. Readiness requires 49 unique independently
decoded chunks and the final acknowledged batch completion. A complete return
to center (0, 0), outside measurement, precedes the warm repeat to (100, 100);
that repeat also checks canonical center section, heightmap and light equality.
Timings include TCP, commands, ticks, decoding and client scheduling. Raw probe
start/end/readiness timestamps include responses completed beyond readiness.
Quantiles use nearest rank on pooled samples across the five runs.

| Operation | Baseline | Cooperative live preparation |
| --- | ---: | ---: |
| Cold full-window readiness median (range) | 904.13 ms (813.31–1,003.39) | 988.39 ms (923.25–994.66) |
| Cold 775 token count / completed before readiness | 7 / 7 | 179 / 178 |
| Cold 775 token p50 / p95 / maximum | 811.30 / 964.73 / 964.73 ms | 5.40 / 16.87 / 60.16 ms |
| Cold 776 token count / completed before readiness | 7 / 6 | 178 / 177 |
| Cold 776 token p50 / p95 / maximum | 811.24 / 972.87 / 972.87 ms | 5.68 / 18.16 / 60.00 ms |
| Warm full-window readiness median (range) | 62.44 ms (54.58–108.95) | 56.21 ms (53.94–75.12) |
| Warm 775 token count / p50 / p95 / maximum | 6 / 53.31 / 108.16 / 108.16 ms | 8 / 24.44 / 74.35 / 74.35 ms |
| Warm 776 token count / p50 / p95 / maximum | 5 / 59.96 / 108.23 / 108.23 ms | 8 / 24.02 / 74.30 / 74.30 ms |

Cold readiness grows about 9.3% while more observer work completes. This is a
responsiveness tradeoff, not faster preparation. One outstanding request yields
fewer baseline samples during stalls, so these are not fixed offered-rate
capacity measurements. Warm ranges overlap and sample counts are small; no warm
speed improvement is established. The TCP fixture does not measure allocation
bytes or prove exact overlap with a particular preparation unit; deterministic
tests separately pin that ownership handoff. These Linux/amd64 measurements use
Go 1.27.3, AMD EPYC 9V74, `GOMAXPROCS=5` and `GOFLAGS=-buildvcs=false`. Raw logs,
reports and immutable baseline exports remain outside the repository.

```sh
MCGO_LIVE_WINDOW_REPORT=/tmp/live-probes.jsonl GOMAXPROCS=5 GOFLAGS=-buildvcs=false go test ./tests/blackbox -run '^TestLiveWindowResponsiveness$' -count=5 -timeout=5m -v
```

## Compact world saves

Public `SaveWorld` in compact `WorldPath` mode captures all dimensions' block
overrides under the simulation owner, retaining only private override maps,
immutable catalogs and detached destination paths. Canonical version-two
conversion, sorting, JSON encoding and file writes run outside ownership. The
public call still waits for its own write result; returning success means the
file writes returned successfully, without an fsync or crash-atomic guarantee.
An accepted save represents its capture point, so later gameplay edits belong
to a later save.

Each runtime admits one unfinished compact snapshot/write job, reserving it
before copying any overrides. Other public callers wait outside ownership
without capturing another snapshot, then recheck shutdown and capture their
own state when admitted. This bounds admitted jobs, not bytes: capture and
retained maps still cost O(total overrides), and serialization creates further
temporary output. Capture itself can pause gameplay. A worker reads no live
world, dimension or session and never needs the simulation owner to finish.

Compact `/save-all` uses the same ordered writer, waits for any previously
submitted job, then captures and waits for its own save under ownership. Its
wait can still block gameplay. Shutdown rejects unadmitted compact saves and
drains an accepted receipt before world workers retire, without starting an
independent compact save. Separate runtimes or other processes writing the same
paths have no new coordination contract.

Native `WorldDir` saves retain precedence and use detached captures described
below. Owned disconnect data saves remain blocking;
the public `SaveServerData` path is described below. The CLI still calls
`SaveWorld` and `SaveServerData` separately; there is no atomic snapshot or commit across those
stores or across dimension files. Its periodic timer still does not consult
the existing `/save-off` state. Native player-file errors retain their existing
recording/propagation behavior. Shutdown adds no independent autosave.

The public API/TCP performance fixture is
`tests/blackbox/compact_save_responsiveness_test.go`, SHA-256
`0ebafbc3fd1a5b97618f438e76044cba20d0aa86f7832e645b2df187dd6acbe6`.
Its baseline is clean archive `d31459f6ea9a8b8ef468c300b04f95e39d93b47f`
with only that test added. Five sequential runs per implementation load a fixed
canonical-v2 fixture: 262,144 overrides, split 131,072 overworld and 65,536 each
nether/end, totaling 15,532,104 bytes. Entries use stone, dirt, glass and
`oak_log[axis=y]` at distant coordinates, with reversed input order. Public
runtime startup, loading, complete 49-column readiness on both 775/776
observers, token fences and a garbage collection precede measurement. There is
no injected writer delay. Each observer issues one token at a time and paces
20 ms after responses while the host calls public `SaveWorld`.

API timing ends when `SaveWorld` returns. Every saved row is then checked for
version, count, coordinate, canonical state, uniqueness and x/y/z order. Both
implementations produce identical file hashes in every included run. The
overworld output SHA-256 is
`03fa1403c04e90afb4c0823d9c2af5ae0225cbacc64c3875c901a94e422dafae`;
nether/end are each
`c2ab46fe99d553d51fc099e72b101f6e8e266b30682c865226c493d0946131be`.
The input SHA-256 values are
`3a5e78a96f223045b9242da541680f554c92c0118edc037be67c9249dfb63ce3`
and `ac0a3dc2085ed706fada8928dc6ee59c1a38e0a1aa603739d3601cead3434b8d`.
Raw call/API-completion and observer start/end timestamps retain responses
completed beyond API return; quantiles use nearest rank on pooled samples.

| Operation | Baseline | Detached public compact save |
| --- | ---: | ---: |
| API completion median (range) | 234.643 ms (200.975–293.542) | 215.295 ms (206.071–221.390) |
| 775 token count / completed before API return | 5 / 0 | 39 / 39 |
| 775 token p50 / p95 / maximum | 235.013 / 293.814 / 293.814 ms | 0.213 / 68.012 / 70.060 ms |
| 776 token count / completed before API return | 5 / 0 | 40 / 40 |
| 776 token p50 / p95 / maximum | 235.003 / 293.789 / 293.789 ms | 0.230 / 67.741 / 70.047 ms |
| 775 first-response median (range) | 235.013 ms (201.501–293.814) | 67.753 ms (53.133–70.060) |
| 776 first-response median (range) | 235.003 ms (201.465–293.789) | 67.741 ms (53.029–70.047) |

Observer work completes before the public save returns. The first response
still waits roughly 53–70 ms here, including capture, contention, transport and
client scheduling; it does not isolate or bound owner hold. API completion
ranges overlap, establishing no serialization or disk-speed improvement. One
outstanding request yields fewer baseline samples during stalls, so this is
not a fixed offered-rate capacity test. Filesystem/page-cache, CPU and GC
effects are included; there is no fsync operation or memory improvement claim.
The measurements use Linux/amd64 Go 1.27.3, AMD EPYC 9V74, `GOMAXPROCS=5` and
`GOFLAGS=-buildvcs=false`. Raw logs, reports and baseline exports remain outside
the repository. Race-instrumented runs establish functionality separately.

```sh
MCGO_COMPACT_SAVE_REPORT=/tmp/save-probes.jsonl GOMAXPROCS=5 GOFLAGS=-buildvcs=false go test ./tests/blackbox -run '^TestCompactSaveResponsiveness$' -count=5 -timeout=10m -v
```

## Native world saves

Native entity coverage includes registered boats/rafts and chest variants, plus
plain/chest/hopper/furnace/TNT minecarts. Their measured cargo, subtype state and
supported passenger trees survive cross-implementation save handoff. Root
suppression must match the loader's supported-type policy and actual passenger
membership; unsupported parent types retain supported children as independent
roots. Iterative tree restoration has no additional depth cutoff, and structural
Passengers data is rebuilt from live children rather than retained as stale extra
NBT. Player RootVehicle/remount, command-cart passenger trees and spawner carts
remain outside this subset. Persistence support does not establish complete
vehicle movement, cargo access or loot-generation behavior.

Public `SaveWorld` with `WorldDir` reserves one unfinished native job before
capturing level metadata, all selected dimension columns, block-entity NBT,
light layers, entity NBT and player data under simulation ownership. The worker
retains private values and paths, with no live worlds, sessions or callbacks.
NBT encoding, compression and file writing run outside ownership; the caller
waits for its own write result. Busy public callers wait outside ownership,
then recheck shutdown and capture their own state when admitted.

Native capture materializes full terrain columns selected by the existing
override-based region selection, in addition to nested data copies. Capture
and memory cost grow with the selected world and can pause gameplay; the one-job
limit bounds concurrency, not bytes, heap or latency. Native names/properties,
file layout and selection rules remain unchanged. The native player snapshot
seeds remembered records, then overlays current admitted Java incarnations;
this corrects the previous collector's stale remembered-player precedence and
excludes configuring, retired and replaced sessions from the active overlay.
Neutral avatars retain their existing transient persistence policy.

Owned `/save-all` waits for any accepted native job, captures its own state and
waits for its write while retaining ownership. Owned disconnect player and
metadata writes also wait for the earlier native job before touching its files,
so these paths still block gameplay. Disconnect writes remain allowed during
shutdown. Shutdown rejects unadmitted public saves, joins connection cleanup
and the final native receipt before retiring world workers, and adds no
independent autosave.

A successfully committed Java admission supersedes every still-tracked admitted
Java session with the same UUID for remembered/native persistence. This includes
retired transports waiting for kick grace or delayed cleanup. An owner-only
sticky flag survives the newer session's own retirement, so older cleanup cannot
overwrite its native inventory, statistics or advancements, or seed stale
remembered data into a later save. Configuring, denied and failed admissions do
not transfer authority; UUID matching follows native player-file identity.
Admission scans O(tracked sessions), retaining one flag per existing session and
no additional identity history. Ordinary disconnects still save, and cleanup,
JSON write scheduling, formats and native write fences retain their existing
paths. The remembered-content correction also protects JSON snapshots after a
kick/rejoin; this does not detach disconnect I/O.

Authoritative ordinary disconnects and native `.dat` ingestion synchronize
existing aliases of that UUID and write the incoming key. Native ingestion
retains its UUID filename key; name-only stores gain no extra alias. Active JSON
snapshots apply the same normalization to their private cloned player map, so
every alias receives the current incarnation's state without changing live
remembered records. Legacy name-keyed records without a UUID use the collector's existing
offline-UUID rule when matching aliases. Unrelated identities retain their
values. Native snapshots therefore do not choose among divergent aliases after
these updates; JSON preserves its schema, historical keys and alias cardinality.
Synchronization
scans O(remembered entries) per disconnect under the existing store lock; native
boot batches ingestion in O(existing aliases + loaded players). This establishes
no memory or latency bound. Metadata loading keeps its existing per-key behavior. Raw JSON import and name-first restore lookup
retain their compatibility rules: conflicting imported values have no reliable
recency information and remain ambiguous until an authoritative update for that
UUID. Malformed or conflicting native files are not newly reconciled.

Level, dimension and entity write errors return from `SaveWorld`; native
player/metadata errors retain their existing first-error recording in
`dataSaveErr` while `SaveWorld` can return success. Later jobs remain admissible
after failures. Successful return means writes returned successfully, without
fsync, crash durability or atomicity across files, dimensions or the separate
JSON store. Errors can leave partially updated files. Separate runtimes and
processes writing the same directory gain no coordination contract.

## Server-data JSON saves

Public `SaveServerData` captures its schema-three player/block-entity JSON under
the simulation owner, then waits outside ownership for detached serialization
and writing. The schema and its numeric item IDs remain unchanged; this is not
a canonical-item migration. Capture includes inventory, carried, ender and body
items, chest/container items, sign state, statistics, advancement criteria and
recipe-book data. A shared iterative memo copies every reachable item
`Contents` array, preserving private aliases and cycles. Cyclic graphs produce
the standard JSON serialization error.
Maps and slices, including remembered offline-player data, are detached too.

The snapshot starts with all remembered name/UUID keys, then overlays current
admitted Java players from the canonical active-player index. An otherwise empty
online set no longer erases remembered players on save. Retired/replaced Play
sessions cannot override a current incarnation, and late replacement cleanup
does not overwrite its successor's remembered record. The same authority fence
also covers retired kick/rejoin predecessors and native player-file writes as
described above; JSON write scheduling and schema remain unchanged.

Each runtime reserves one unfinished server-data job before capture, separately
from its compact-world save slot. Busy public callers wait without ownership or
another snapshot, then recheck shutdown and capture their own cut. Capture and
cloning still cost O(captured data) time and storage; job count is not a payload
or heap cap. Only detached workers take the global `dataSaveMu` around encoding
and file writing. The fence excludes overlapping JSON writes across runtimes,
without establishing a cross-runtime snapshot or an order between their
gameplay actions. Workers use no live sessions, stores or owner locks.

Owned disconnect saves wait on the same runtime receipt and write their own
snapshot while retaining ownership, so they still block gameplay. They remain
allowed during shutdown: connection cleanup finishes before Shutdown reads and
joins the final data receipt outside ownership, alongside the compact-world
receipt. Public unadmitted requests are rejected after shutdown begins. Stop
adds no independent autosave; existing disconnect writes are retained.
Native public world saves use their own detached job; owned disconnect
`.dat`/metadata writes and `/save-all` still wait under ownership. Compact block
overrides and server-data JSON have
separate capture points and no atomic cross-store commit or fsync guarantee.

Two unchanged regression bodies fail on clean baseline
`b5c544ed940a873167e5c8a510c2903de6849f54`: a save with only remembered
players drops their name/UUID records, and late replacement cleanup regresses
its successor's stored item count from seven to one. The baseline probe SHA-256
is `214537752e05a4be52621edffd461154125fa23f5a167311c7fe12b572a9013d`;
the two exact function bodies are
`ed6afd887475d322a12e4dc7e14a7f5373e050612cf2065101a3900f02f81003`.
The committed tests pin those failures independently of asynchronous writing.

The API/TCP performance harness is
`tests/blackbox/server_data_save_responsiveness_test.go`, SHA-256
`eb31a9b9f5037b352f78c7f1e6d6786d7b1c629c028d21a98938886a5a1b7d0a`.
Five sequential baseline and five optimized samples use a clean archive of that
same commit with only the harness added. A fixed schema-three seed contains
2,048 chests, 2,048 generic containers and 2,048 signs, with nested item contents;
two ready 775/776 players are active and no remembered offline players are
seeded. Excluding offline entries keeps the workloads comparable despite the
separate retention fix. The seed is 12,220,607 bytes, SHA-256
`383fad217fce36d70bf9fbe44669c74d3cd71a0e3fcc36b6da2ac390ebe4e7e6`.
Loading, frozen runtime startup, complete 49-column readiness, token fences and
fixture-garbage collection precede measurement. There is no injected delay.

Each observer keeps one token outstanding and waits 20 ms after responses while
the host calls public `SaveServerData`. After return, every saved block-entity
record is checked for dimension/position, uniqueness, nested items and scalar
state, alongside the expected two active player records. Unordered slices are
compared semantically. Output is approximately 12,224,750–12,224,764 bytes;
random player fields and map/list order preclude a single byte-hash comparison.
Raw API and observer timestamps retain responses completed beyond API return;
quantiles use nearest rank on pooled samples.

| Operation | Baseline | Detached public server-data save |
| --- | ---: | ---: |
| API completion median (range) | 185.373 ms (181.808–189.860) | 228.578 ms (223.354–257.983) |
| 775 token count / completed before API return | 5 / 0 | 40 / 40 |
| 775 token p50 / p95 / maximum | 185.686 / 190.210 / 190.210 ms | 0.159 / 77.117 / 97.340 ms |
| 776 token count / completed before API return | 5 / 0 | 40 / 40 |
| 776 token p50 / p95 / maximum | 185.570 / 190.142 / 190.142 ms | 0.173 / 76.997 / 97.246 ms |
| 775 first-response median (range) | 185.686 ms (182.267–190.210) | 77.117 ms (69.908–97.340) |
| 776 first-response median (range) | 185.570 ms (182.181–190.142) | 76.997 ms (69.831–97.246) |
| Whole-process TotalAlloc delta median | 144.17 MB | 232.86 MB |
| Whole-process heap after API return median | 217.98 MB | 327.84 MB |

The complete save is about 23.3% slower in this fixture while observer requests
complete before it returns. Deep copying also has a material memory cost. The
memory counters include the server and TCP clients, are neither isolated
operation allocations nor peak measurements, and establish no snapshot/heap
bound. Heap before measurement is approximately 139.60 MB in both cases; MB
here is decimal. First responses still wait roughly 70–97 ms, including capture,
contention, TCP and scheduling rather than isolated owner hold. Filesystem/page
cache, CPU and GC effects are included. One outstanding request produces fewer
baseline samples during stalls; there is no fixed offered-rate, throughput,
hard latency or player-capacity result. Measurements use Linux/amd64 Go 1.27.3,
AMD EPYC 9V74, `GOMAXPROCS=5` and `GOFLAGS=-buildvcs=false`. Raw logs, reports,
baseline exports and whole-process counters remain outside the repository.

```sh
MCGO_DATA_SAVE_REPORT=/tmp/data-save-probes.jsonl GOMAXPROCS=5 GOFLAGS=-buildvcs=false go test ./tests/blackbox -run '^TestServerDataSaveResponsiveness$' -count=5 -timeout=10m -v
```

## Conformance and provenance

The public [`conformance`](conformance/README.md) runner and
`cmd/mc-conformance` use declarative endpoint/version/capability manifests,
explicit pass/fail/unsupported/infrastructure results and bounded framed
wire transcripts. Its 775 and 776 smoke profiles check status, ping, login,
configuration/join and rejection/liveness contracts. They account for the
measured session-ID and Join Game field differences. The opt-in `play-chunks`
capability continues into a stationary view-distance-two session, requiring an
explicit caller-supplied dimension/registry shape. It validates chunk-batch
grammar, independently decodes the complete 49-column window, checks initial
position/cache-center/player-attribute readiness and sends the measured control
replies. This profile requires a compatible offline endpoint with a minimum
view distance of two; it does not derive bounds from implementation tables.
Deliberately broken independent TCP controls test its ability to detect
failures. These bounded checks do not provide a complete protocol grammar or
certify all gameplay parity.

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

Scheduled block dispatch retains a bounded current-dispatch record containing
original block identity, deadline and priority. Clone snapshots this record and
queued work before inserting destination requests; overlapping copies cannot
recursively copy their own additions. Area clearing also cancels collected work,
so the dispatcher skips canceled local entries. Fluid work remains independent.
Native global sub-tick ordering and chunk queue persistence are not implemented
by this mechanism.

Dropped items retain the item tick's collision-bypass decision for later piston
movement in that tick. Source-base retraction uses this retained decision, not
a new overlap test after closest-space escape. Stationary-warm NoGravity item
controls cover contact, backoff and cleanup in all six directions. Scalar
collision planes include the measured 1e-7 tolerance; coordinate updates depend
on resolved movement and the amount removed by collision, preserving tiny free
movement. Restitution samples the legacy support point 0.2f below the feet and
uses float32 nonliving damping, so retained gaps still bounce on slime. Full voxel
geometry, support X/Z caching, step-up and broader entity effects remain partial.

Builtin world clocks separate world age from Overworld and End totals. Each
clock owns float32 fraction/rate and a paused flag; global advance_time changes
progression and effective network rates without overwriting saved rates. Native
world_clocks.dat decoding defaults malformed/missing known entries independently
and discards unknown IDs. Legacy level Time/DayTime does not seed absent clocks.
The simulation owner captures detached nested state into LevelData.ClockStates;
the single existing native writer serializes it. Nil ClockStates preserves the
legacy internal writer API, while an explicit empty map remains empty. Clock-time
GameTest setup/teardown changes only the chosen integer total and clears its
fraction, preserving rate/pause and restoring no elapsed test time.
Command selection can explicitly choose either builtin clock independently of
the source dimension. Pause/resume/rate changes preserve the fractional position
and emit the selected clock, including repeated accepted controls. Rate parsing
uses float32 bounds; query results retain measured long-remainder behavior and
typed timeline errors. Set/add reuse the measured float32 time-argument parser
for day/second/tick units and integer saturation. Six builtin markers resolve
strictly to their next day-period occurrence, even at an exact marker boundary;
valid identifiers normalize default namespaces before lookup. Custom resources,
remaining delimiter/error precedence and completions remain partial.

Fluid scheduler keys retain exact builtin registered owners, distinguishing
source from flowing water/lava. A duplicate same-position/owner request retains
its first deadline and priority; different owners coexist. Dispatch rejects stale
owners after replacement. Intrinsic and waterlogged source water share the water
registration. This preserves block-only clone/clear semantics.
The simulation owner snapshots pending scheduled work into primitive per-chunk
records before asynchronous save work. World capture clones those slices and
includes tick-only generated columns. Native chunk lists retain registered
owner, signed relative int32 delay and priority; already-collected callbacks
are excluded. Output sorts by delay, priority and position because the current
queue does not retain native sub-tick insertion order. Native-server readback
is pinned. Full-chunk queue restoration retains distinct record IDs separately
from owner membership, so polling a loaded duplicate clears membership while
other records may remain. Per-owner minimum indexes keep deadline queries
constant-time; removals inspect only that owner. Restored deadlines are relative
to game age, and existing registered-owner dispatch guards remain in force.
Saved rows now activate under the bounded admission policy described below;
complete ticket lifecycles and native sub-tick order remain separate work.

Player retirement captures a detached attribute/effect snapshot before the
physical entity is removed. Subsequent disconnect/kick persistence uses this
snapshot, retaining remaining player-tick durations without offline decay.
Native attribute packing keeps permanent command/effect modifiers and omits
transient held equipment; the permanent category does not imply infinite effect
lifetime. Raw maps load before the native walking-ability movement-base override.
Effect duration, identity and modifiers remain coupled: only actual effect
transitions remove their IDs, including command-overridden active IDs. Absent
effect IDs remain ordinary command state. Hidden effect chains and attribute
modifier slices are deep-copied before asynchronous save work. JSON keeps finite
legacy numbers and represents nonfinite attribute/ability values by IEEE64 bits;
NBT retains its native numeric types. Effect templates reproduce through
`tools/geneffectattrs/README.md` and do not imply complete effect mechanics.


Scoreboard visibility derives from display-slot assignments. Hidden objectives
and score mutations do not broadcast updates. First tracking sends create,
ordered display assignments and the current score snapshot; later slot changes
use measured removal/reassignment ordering. Deleting a holder's final score
emits the global reset form, even for a hidden objective. Joining sessions receive
a detached visible-only snapshot ordered by display slot, creating each objective
once before its assignments and scores. Shared payload builders preserve live
update encoding; writes target only the new session. Persistence and exhaustive
multi-holder ordering remain separate.


Ordinary player death uses a new-body reset distinct from restart restoration.
Raw attribute bases remain, while old modifiers and effects are discarded.
Absorption and levitation/raid origins reset with those effects; constructor
walking ability returns to its default independently of the copied movement
base. Initial health uses the sanitized copied maximum-health base. Copied
instances become dirty for projection, and both equipment scans reinitialize.
The owner receives abilities during respawn. End-return/keepEverything policy,
complete constructor-state copying and all healing consumers remain separate.


Function execution uses a simulation-owned explicit stack with lazy line/member
continuations. Each call captures actor, executor, position, dimension and
rotation; temporary legacy session bindings restore after each action without
undoing actual player movement. Minimum pending costs bound the reachable
frontier, avoiding expansion of unreachable recursive tails or fork siblings.
Manual/tag, startup/tick, scheduled and advancement-reward calls use this path.
Player-only @s arguments use the bound executor, including neutral server-source
frames; nil or nonplayer executors cannot fall back to the original caller.
Argumentless clear uses that same bound player and reports the native
requires-player failure for nonplayer sources.
Supported execute forms compile before effects and share a root action budget
with the function queue. Modifier stages evaluate across source batches before
leaves mutate state; strict max_command_forks bounds and ordered score-store
callbacks preserve measured partial-fork behavior. Compiled leaves cover basic
function/tag calls, scoreboard player set/add/remove/get and clear. Unsupported
grammar falls back before effects to the existing dispatcher. Return/macros,
complete parser continuations and broader spatial consumers remain open;
the plan does not establish full command-framework parity.


Native full-chunk loading now decodes detached scheduled-tick records separately
from terrain. Valid list siblings survive malformed entries; registered IDs
normalize default namespaces, numeric tags follow Java int conversion and
priority clamps to the native range. X/Z chunk ownership is checked without
inventing a Y-bound codec rule. Duplicate records and list order remain intact.
The server consumes this handoff after regions and block entities load, before
normal ticking. Protochunk promotion and the full native ticket lifecycle remain
separate from the current full-chunk loader.


Saved-tick queue primitives distinguish unactivated relative records from active
absolute deadlines. They preserve pending membership, duplicates and list order,
and can poll only an eligible chunk set without rescheduling blocked records.
Startup now defers saved rows until actual materialization and loading admission
from ready players or existing forced chunks. Loading, simulation, FULL readability
and entity-loaded predicates have separate measured radii. Due dispatch checks
simulation admission without rearming blocked deadlines; only due buckets can
prepare chunks. Loaded queries remain observational. Frozen player passes can
activate queues without advancing time, while no-session manual ticking preserves
the embedding API. Clone activates eligible source rows before copying. General
unload/repack, temporary tickets, forced-ticket persistence and a complete
asynchronous chunk lifecycle remain open.


Vehicle movement validates NaN coordinates and nonfinite rotations before mount
authority or movement mutation, using the native typed disconnect. Coordinate
infinities remain on the existing clamp/speed-check path. This packet validation
does not establish complete vehicle physics or player-movement numeric parity.


Experience orbs expose live Age, Health and Value as short NBT fields and Count
as a positive integer. Summon and data reload share the measured counter codec,
including Java floating-count saturation. Orb health is separate from living
health, and loaded zero survives construction; Value updates use the orb metadata
slot. These live controls do not establish disk restart preservation, damage to
orb health or complete orb movement behavior.


Supported finite recurring effects include their final active callback before
expiration. A loaded one-tick regeneration, poison or wither instance executes
once, while a zero-duration instance remains inert. The final path requires an
actual entity tick; parked lifetime policy remains a separate limitation.


The Java player adapter validates raw numeric fields before shared movement
hooks, with omitted packet fields treated as zero for that check. Invalid raw
packets disconnect; a plugin-generated invalid destination still follows shared
correction/rejection behavior. Neutral avatar capability policy is unchanged.
Coordinate-infinity connection controls do not establish physical clamp parity.


Ordinary living-mob ticks maintain a runtime displacement sample independent of
stored velocity, including NoAI bodies. Parked bodies retain their last sample.
Teleport/data reload invalidate the next baseline while retaining the cached
vector until sampling. Spear relative-speed damage consumes this sample,
independently of stored velocity. Generated kinetic component facts provide
separate damage, knockback and dismount conditions; damage uses the raw attack
base and the promoted component multiplier. Spear knockback uses effective
resistance, preserves airborne Y and applies measured grounded motion. The
dragon loop and mounted-target controllers remain separate. Qualifying spear
contacts detach ordinary NPC boat riders, publish passenger/position changes
and start a boarding cooldown. Native full-block escape geometry and blocked
or water fallbacks are pinned for adult villager/cow riders. Source invulnerability
rejects contact; hurt cooldown rejection can still allow detachment. Other mounts,
players, partial floors, scaled geometry and attached seat-follow remain partial.


Java player rotation proposals wrap to the signed half-turn range and clamp
wrapped pitch to[-90,90] before movement hooks. Authoritative plugin replacements
keep the shared rule's existing semantics. Server-pose controls are independent
from observer rotation encoding and tracker delivery.


Protocol angle bytes use native float32 scaling, mathematical floor and Java
integer saturation before retaining the low byte. Negative fractional angles
therefore encode correctly. This shared encoder does not supply missing tracker
updates or establish packet ordering.


Slime walk damping reads vertical velocity after restitution for both its
threshold and factor. A large requested fall may bounce below the damping
threshold, and a suppressed bounce supplies zero. Collision, support selection
and existing vibration paths remain unchanged; orb tail behavior is separate.


Instant health, instant damage and saturation use raw command tick durations,
with a one-tick default. Stored Health/Harm instances deliver through living
ticks, including undead inversion; storing or declining an instance adds no
extra dose. Direct scaled potion APIs remain separate. The complete built-in
instantaneous classifier is reproduced by tools/probeinstanteffects.


Experience-orb movement retains shared collision, restitution and vibration
effects, then applies promoted float drag/friction and its separate floor
response. That response compares incoming vertical velocity with effective
gravity, including NoGravity. Ordinary air and stone/slime contacts are pinned;
embedded full-stone escape also advances through this tail after selecting its
impulse, while skipping ordinary gravity/fluid/follow updates. First-tick escape
bounds and damping are pinned. Escape candidates now use the existing live-world
full-collision-cube predicate, including measured partial shapes. The initial overlap gate now uses positioned collision boxes with the existing
1e-7 inset, preserving clear fence corners and slab gaps. Measured face-contact
and near-boundary controls pin this distinction. Embedded fluids, arbitrary
clipping and subsequent trajectories remain separate.


Saturation potion drinking delivers one food/saturation dose regardless of
stored duration or duration scale, without adding an active effect. Its shared
food-dose helper does not change the separate active-tick eligibility rules.
Cloud admission also delivers Saturation directly, at full food strength despite
cloud half-dose scaling; nonplayer mobs retain no Saturation status. Typed
DurationOnUse adjusts finite cloud lifetime after each victim, after radius
removal, and preserves the-1 sentinel. Raw summon and field save/load are pinned;
complete cloud persistence and reapplication delay remain separate. Splash
Saturation likewise delivers one full player food dose without a stored status;
nearby nonplayer recipients retain no Saturation status. Noninstant splash
durations still scale, and arrows retain their separate stored-effect route.


Execute contexts and child source frames retain rotation snapshots. At adopts
the selected entity rotation; as preserves source rotation. Numeric/relative
rotated and legacy rotated-as affect local teleport coordinates, including
nested function calls. Legacy fallback also binds a source frame. Orthogonal
yaw/pitch cases are pinned within3e-6 blocks; Go double trigonometry does not
reproduce native float/look-table precision at arbitrary angles. Existing numeric
parser, facing, anchor and other local-coordinate consumers remain partial.

Teleport destinations use the bound command-source dimension, including direct
execute chains, file functions and scheduled neutral sources. Player hops use
the existing dimension-change lifecycle and preserve rotation when the command
does not supply a replacement. Explicit source frames must not depend on legacy
temporary session-dimension swaps.


Real Java player head tracking runs on admitted player ticks independently of
world freeze. Packed head-yaw changes fan out to tracked observers; unchanged
yaw stays silent even when pitch changes. Presence-only incarnations retain
their capability boundary. Body packet selection and precise tracker phase,
ground, rider and teleport behavior remain separate work.


Ordinary server NPC damage honors the saved Invulnerable flag before species
dispatch. Bypass-tagged damage and an actual creative-player cause can bypass
it; kill-credit identity cannot. Damage commands expose numeric results for
accepted/rejected flag controls. Direct Manager/TNT and specialized dragon-part
paths, unrelated cooldown outcomes and kinetic physical contact remain separate.


Eyes/feet anchors propagate by value through source frames and function calls.
Numeric positioned resets feet; at preserves the anchor. Local teleport
coordinates add the current executor's modeled eye offset; relative coordinates
do not. Player eyes use cached effective scale and pose, including fixed sleeping
and dying dimensions. Entity eyes use reproducible baseline/named-baby facts
plus measured stand/slime variants. Runtime interaction dimensions, additional
dynamic poses and nonplayer scale lifecycle remain partial.


Entity-tag queries require operator level2 and resolve targets in the
requester's dimension. Missing targets produce no response; valid requests echo
their signed transaction ID and use the target's modeled NPC/player save view
without its outer type identifier. Player inventory/equipment comes from the
target, not the requester. Full subtype/default-field equivalence and dormant
entity lookup timing remain separate.


Block conditions resolve pure FULL/materialization readiness after predicate
parsing. Unavailable positions are runtime errors, not false predicates:
unless cannot invert them. Terminal conditions report the position error while
run chains stay silent. No-controller embedding access remains unchanged;
actual chunk unloading is a separate lifecycle.


Experience-orb attraction targets half the authoritative player eye height,
including effective scale, rather than a fixed vertical offset. Standing
scales0.5,1,2 are pinned with first-tick NoGravity displacement controls.
Default-scale standing, forced crouch, crawl and water/sprinting swimming targets
also have first-tick controls with gravity enabled and disabled. Sleeping,
transitions, orb-in-fluid attraction and broader offsets remain separate.


The scheduler core distinguishes registered queues, detached resident containers
with absolute deadlines, and serialized containers with pending relative delays.
Generation-checked discard rejects stale save completions after reattachment;
already-collected callbacks retain their dispatch lifecycle. World snapshots
include detached records. Native-persistence worlds with ready controllers now
bind scheduled-container retirement to measured FULL-admission loss. A shared
FIFO serializes subset and full region writes; generation checks fence late
completion, and fixed serialized delays pause through cold time. Full-save,
disconnect and shutdown fences reconcile completion even after controller loss.
Backend/entity eviction, full ticket timing and normal-request sub-tick ordering
remain separate.


Teleport relative angles resolve from the frozen command source, including
multi-target commands that also move their issuer. Each victim composes the
wrapped angular delta with its own history before pitch bounds apply. Ordinary
entities can retain whole-turn yaw; player arrival reduces whole turns with
sign-preserving remainder. This command-owned state is distinct from later
movement normalization and does not establish complete relative SyncPos parity.


Block-tag queries require operator2 and expose the modeled canonical saved
block-entity view, omitting only outer storage identity, coordinates and packing
markers. Nested items/components and unopened loot remain intact; missing block
entities return explicit TAG_End. This route is separate from smaller client
update tags. Full subtype coverage and unloaded lookup lifecycle remain open.


Experience-orb pickup runs on admitted player frames alongside regular item
touches, including while world age is frozen. The scan uses orb-sized boxes,
chooses one nearby orb and preserves the two-player-tick cooldown and mending
before XP. Each take publishes CollectItem(count1); exhaustion removes the source.
Default standing reach and spectator exclusion are pinned. Scaled/posed/vehicle
reach and moving-orb pickup timing after teleport remain separate.


Direct setblock container Items retain components supported by the common item
patch decoder, using the lossless component string bridge and preserving stack
identity/count. Chest and generic-container loot seeds accept typed long suffixes.
Unknown component models retain the previous fallback. Native item restoration
now shares modeled-field decoding while retaining dedicated recursive containers,
bundles, trim, books and potions. The common writer preserves positive repair
cost and modeled stored enchantment levels, keeping normal and stored maps
distinct. Map IDs and explicit bottle amplifiers retain zero versus absence;
nonzero dyed colors also persist. Base-color/dye palette values retain white
versus absence. Modeled banner layers, stew effects and pot sides preserve
order; stew duration160 uses the native omitted-default encoding while zero
remains explicit. Dyed-color zero still lacks a presence model.
Arbitrary components, explicit empty/removal semantics and full codec/error-boundary
parity remain incomplete.


Spear use age advances on admitted player-use passes, including during world
freeze, beginning at0 before increment. Remembered-contact cooldown remains on
level game time. Native controls distinguish these clocks independently and
retain exact contact outcomes. Stack changes, duplicate starts, complete use
retirement, hand metadata and wear remain separate.


Forceload reads the bound command-source level and origin. Execute-in scales
source X/Z by the old/new level coordinate ratio in both primitive and batch-plan
paths, retaining Y, rotation, anchor and executor without moving the physical
issuer. Forced-set responses remain distinct from actual chunk admission.


Selective dimension snapshots capture only explicitly named columns and merge
those records into existing regions, preserving untouched compressed bytes and
timestamps. Empty selected tick lists clear stale work. Capture occurs under the
simulation owner; callers must serialize all writers to the same region. Each
region replacement is atomic, without fsync or multi-file transaction guarantees.
Scheduled-container retirement now uses this storage API; it does not establish
full backend/entity unloading. Untouched external records survive; selected
oversized output and external-file cleanup remain open.


Frozen environmental/base player work uses the same due frame as living, food
and statistics; the faster command poll no longer advances it independently.
Normal simulation stepping and flushing retain their existing policy. Player powder-snow
accrual/thaw now follows the admitted player frame, with stationary exposure,
wearable immunity, metadata and freeze damage controls. Burning/melting, swept
paths and imported freeze state remain separate measurement boundaries.


Local teleport coordinates use independently synthesized periodic float32
trigonometry and a measured float32 basis before widening components. Public
numeric corpora pin every sine sample and representative basis outputs; command
wire controls pin their consumer. Execute-facing now updates the source's
rotation for numeric targets and forks entity feet/eyes orientations without
replacing its executor. Its independent public-observation numerical model is
pinned at command float-degree outputs; arbitrary extreme inputs, broader
execute planning and global physics math remain separate limits.


Food exhaustion spends only when strictly above four; an exact four remains
until more exhaustion arrives. Deferred regeneration cost keeps its existing
order. Fractional saturation and multiple spending quanta remain separate limits.


The native firework explosion leaf always includes shape, including small_ball;
it is a required field rather than an omitted default. Common firework component
coverage and parser admission rules are described below.


Native cat hydration restores known variant and sound holders. Missing fields retain existing body values; a fresh body defines black/classic. Spawn metadata omits those defaults and later tabby changes emit the matching holder. Loaded observer lifecycle and arbitrary custom holders remain unproved.


Natural swamp-hut piece flags emit a witch/cat descriptor once. The simulation owner admits both persistent residents, including with frozen world time and natural spawning disabled. Native save first reconciles scheduled retirement, then drains already-generated residents before its snapshot; a loaded-world restart preserves the pair without duplicates. Exact entity RNG replay and generation completing concurrently with capture remain outside the proof.


Firework command/native readers require an explosion shape. Rocket flight and explosion lists are optional; an explicit empty rocket is represented separately from absence. Typed slot controls pin admission and retained state, not all diagnostics or numeric codec bounds.


Common native item persistence now writes modeled explosions and rockets, including required default shapes and explicit empty rocket compounds. Instrument removal persists as an explicit removal compound. Root and nested disk restart controls preserve these distinctions; arbitrary prototype/default equivalence remains incomplete.


Common native item persistence writes and recognizes the existing complete Ominous banner aggregate. Its canonical four components preserve the eight ordered layers. Full recognition clears duplicate generic patterns; incomplete aggregates keep their supported generic representation. Arbitrary custom metadata preservation remains incomplete.


Explicit /place structure supports swamp huts, desert pyramids, jungle temples, igloos, ocean/beached shipwrecks, cold/warm ocean ruins, the flat-safe jungle/swamp ruined portals and nether fortresses. It validates the command source position and covered chunk readiness, builds an unregistered plan over detached live blocks, applies ordered writes, installs generated block entities and admits persistent residents. The planner receives the level generator's sea level and flat top height, and each chunk's postProcess and region stream come from the level random source, as PlaceCommand does; the jungle temple's moss selector, the pyramid's cellar/roof and the beached shipwreck's sink therefore vary per placement. Command chunk snapshots are built with NewProtoChunk and filled through ProtoChunk.Set so WORLD_SURFACE_WG and OCEAN_FLOOR_WG track live blocks below y=0 as well; template pieces such as the igloo and shipwreck sample them while placing. Natural structure caches remain untouched. Command cat finalization observes registered hut context and moon phase separately from natural hut generation. Other structures and broader coordinate/dimension semantics remain incomplete.

Fill commands deliver the actual changed count to result callbacks, zero for
runtime failure, and no numeric completion for syntax rejection. Endpoint FULL
readiness is observed without invoking generation; existing manual embeddings
retain their unrestricted-read convention.

Natural mineshaft chest carts use the same simulation-owned generated-entity
queue as hut residents. Generation retains the original random draws and emits
latent loot table/seed descriptors. Fresh nonliving carts retain their initial
first-tick save flag until the existing cart tick clears it; cargo unpacking
continues through the existing container machinery.


Measured positive entity type dimensions fill missing base bounds, preserving
existing runtime table entries. Already-declared static passenger seats use
measured coordinate precision. Boat class seats and rider VEHICLE offsets keep
their existing policies. Complete type tables do not imply full pose, scale,
zero-size or multi-passenger geometry parity.

An explicitly present black dyed color is distinct from absence. ItemStack keeps
existing nonzero representations and uses DyedColorZero for present RGB zero.
Effective presence participates in stacking, JSON, native/Jukebox persistence,
slot codecs and dye recipes, so black contributes to subsequent color mixing.
