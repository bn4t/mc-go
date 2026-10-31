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
Measured Java 26.1/775 and 26.2/776 clients, neutral presence/movement avatars
and finite canonical block views share that world. Parallel regions, remaining
numeric-save migrations and complete neutral world/gameplay replication remain
future stages.

## Implementation status

The server is under active parity development. The detailed supported cases
and remaining work are tracked in [GOAL.md](GOAL.md); a checked scenario does
not establish complete vanilla behavior for its entire subsystem.

Recent measured coverage includes command-block execution and editors,
command-minecart activator rails, structure-block save/load and redstone actions
with native templates, jigsaw editor/generation, test-block editors and
test-instance controller records/reset/native saves,
block-entity update coalescing, moving-piston collision timing and measured
dropped-item pushes, retracting source-base ejection of NoGravity armor stands
and NoGravity dropped items in all six directions,
container validity, chest events and ordinary main/offhand placement and consumption.
Dying mobs continue to obstruct block placement until their actual removal.
Current player bounds refresh on player frames or pose changes; hypothetical
pose-fit dimensions use live attributes. Settled scale requires player ticks.
Embedded dropped items now move with collision bypass, gravity-before-escape
ordering and float drag in measured single-exit chambers. Partial-shape escape
selection remains in progress. XP-orb escape uses precise shape overlap and
full-cube escape candidates, with measured first-tick movement and damping;
fluids and broader trajectories remain partial. Nearby XP pickup now follows
player ticks during world freeze and emits the client collect animation.
Measured contact
physics now preserves tiny free movement, clips near-plane contacts and retains
slime bounce support when collision leaves a small gap above the surface.
Invalid hand values in protocol 776 item-use packets disconnect without item
actions; broader malformed-packet coverage remains partial.
Selected-hand interactions include signs, hive harvesting, ignition, growth,
end items and disc insertion. Jukebox command, disk and interaction paths share
typed records with measured component, count/default and playback-clock
preservation. Paired ejection sends STOP before the item spawn. Arbitrary
component patches and custom song overrides remain in progress.
Axes, shovels, hoes and honeycomb support measured selected-hand transformations,
costs and effect recipients. Copper chest menus preserve named cargo and stay
usable while another player waxes or scrapes the same live chest. Mixed copper
double-chest variants, arbitrary item overrides and styled names remain partial.
Player melee damage combines attack-damage overrides and modifiers with
generated weapon values, including measured copper weapons. Attack speed also
uses player overrides and modifiers, with zero-speed and default-delay controls.
Generated main-hand damage/speed modifiers now participate in attribute queries
and are reapplied to retained weapons after respawn.
Explicit removal survives an identical stack rewrite; renaming or re-equipping
reapplies them. Attack-speed updates reach the owner and tracked observers.
Default head, chest, leg, foot and body armor modifiers also use the canonical
attribute map, including zero toughness entries and slot restrictions. Armor
and toughness reduce damage without counting equipped items twice; retained
armor reapplies after respawn. Player knockback uses effective resistance,
including command modifiers and explicit removal of equipped modifiers.
Ordinary mob damage and melee pushes use effective resistance and generated
species defaults. Mace waves use effective mob resistance, measured heavy-fall
strength and fixed additive lift. Spear pushes use effective mob resistance;
measured kinetic damage uses the raw attack-damage base and sampled target
displacement. Mounted wooden-spear controls and ordinary NPC boat dismounts are pinned,
including blocked escape and cooldown/invulnerability behavior. Other mounts,
partial-floor dismounts, arbitrary item attribute components, player smash
motion and broader mob consumers remain partial.
Scoreboard objective and score updates reach connected players only while the
objective is displayed. First display sends the objective and score snapshot;
slot changes and score resets follow measured packet behavior. Joining players
receive visible objectives and scores without rebroadcasting them to existing
viewers. Scoreboard commands expose measured numeric results to execute-store,
including multi-target sums, successful zero values and runtime failures.
Scoreboard persistence remains separate work.
Attribute getters supply scaled integer results to `execute store` while keeping
feedback unscaled; successful zero values and runtime failures retain distinct
execution status. Effective attribute queries use generated registry ranges;
raw base/modifier queries preserve their values. Broader equipment projection
and physical attribute consumers remain partial.
Native player saves retain raw attribute bases, permanent modifiers and active
effect lifetimes. Effect modifiers expire with the associated effect; command
modifiers with the same ID remain when no effect owns them. Reload reapplies
held gear and uses saved walking ability speed for the movement base, matching
native behavior. Compact JSON saves preserve nonfinite attribute values through
an explicit bit representation. Ordinary death keeps raw bases but drops old
modifiers/effects and their dependent absorption state; respawn resets walking
ability and initializes health from the copied maximum-health base. Retained
equipment reapplies. Player instant healing, regeneration and food healing use
effective maximum health. Exhaustion can spend saturation in Peaceful difficulty
while preserving food level. Lowering maximum health clamps excess health on the
living frame, while raising it grants no free healing. Complete ability and
effect mechanics remain partial. Finite regeneration, poison and wither on
players and mobs use native zero-interval and amplifier-wrap scheduling,
including the final active callback; loaded zero-duration instances remain inert.
Infinite regeneration, poison, wither, hunger and saturation use living entity
age and actual tick eligibility. Players continue through world freeze while
ordinary mobs pause; ordinary respawn resets the body clock. Command-created
instant health, instant damage and saturation use tick durations (default one);
finite/infinite active Health/Harm instances deliver on living ticks without an
extra dose when stored. Direct potion doses remain a separate path, with direct
saturation classification and broader effect mechanics still partial.

Native NoGravity projects to client metadata at pairing and on true/false changes
across measured living, item and boat paths. Crystal reloads retain dirty updates
even when a flag changes back within one tick. Summoned crystal tags support
selectors, deduplication and measured data replacement/removal. Vehicle gravity/travel and generic
entity movement remain partial.
Native boats and most minecart variants preserve measured cargo, subtype state
and supported passenger trees through vanilla-compatible saves. Chest boats and
chest/hopper minecarts provide measured shared cargo menus, clicks and spectator
restrictions. Killing cargo vehicles with `/kill` spills their inventory even
when entity drops are disabled. Creative and survival attack destruction also
preserve cargo; only survival with entity drops enabled drops the vehicle item.
Item commands access the same cargo slots. Player remount, spawner carts and
complete vehicle runtime behavior remain in progress.
Startup folder datapacks can supply displayed and displayless advancement
definitions with XP, built-in loot, recipe and function rewards. Measured
display fields, layout, built-in tab overrides and completion announcements
are supported. Icon counts and reward XP follow measured native JSON integer
coercion, including fractions and overflow wrapping. Custom tick criteria support
measured player mode/level, flags, NBT, equipment, location/biome and boolean
conditions, plus advancement completion and individual criterion requirements.
Rewards fire once per completion; startup recipe replacements use
their own progress and rewards. Selectors query custom advancement completion
and criterion maps using that server's definitions; unknown IDs or criteria
remain nonmatching.
Registered startup file functions support `/schedule function` with append or
replace and `/schedule clear`. Duration delimiters and trailing syntax are
validated before function lookup, without creating events for rejected requests.
Deadlines follow world time and frozen-world
controls; scheduled functions can summon from the server's world-spawn source.
File and function-tag callbacks and absolute deadlines survive native saves
and restart. Startup folder function tags support merge/replace and measured
required/optional references. The load tag runs once and tick tag each world
tick before time advances. Function calls now share a bounded action queue for
measured nested, recursive, conditional and forked execution. The default
65536-action limit terminates recursive work without growing the Go call stack;
queued calls retain their invocation source. Supported execute forms compile
before evaluation and share outer command and fork budgets, batched predicates
and ordered score-store callbacks. This covers basic function/tag, scoreboard
player and clear leaves. Source rotation propagates through measured rotated/as/at
chains and nested functions into local teleport coordinates. Measured eyes/feet
anchors use cached player pose/scale and generated entity eye constants. Other grammar uses
the existing dispatcher; macros,
returns, full syntax validation and optional-cycle ordering remain partial.
Complete player-predicate and rich-text/icon schemas, datapack selection/ZIP/reload,
custom loot overlays and several vehicle/physical interactions remain in
progress. See individual slice entries for exact tests and limitations.

Test-instance controllers query registered templates and reset rotated volumes.
Startup folder packs can add function/block-based tests and difficulty, weather
and builtin-clock environments composed with `all_of`. Accepted RUN applies its environment
immediately, including while frozen; runner setup and completion progress on
world ticks. Completion and accepted replacement restore captured settings.
START/ACCEPT/FAIL and typed failure/timeout workflows remain supported. Other
environments, arbitrary test functions, LOG reports and the full GameTest runner
remain partial. Pack selection, ZIP loading, metadata compatibility and reload
are not implemented for these registry overlays.
Overworld and End clocks retain independent totals, fractions, rates and pause
state across native saves and restart. `/time set` and `/time add` use the source
dimension's default clock; Nether has none, and day markers belong to Overworld.
Clock environments restore the captured integer total and clear its fraction.
Missing native clock data starts each clock at zero; legacy level time does not
seed absent clocks. Builtin clocks support `/time of <clock>` selection and
`pause`, `resume` and `rate` controls, with rates from 0.00001 through 1000.
Explicit selection works from the Nether. Absolute and four builtin timeline
queries include repetition counts. Decimal time arguments support days, seconds
and ticks with native float32 rounding. Builtin markers, including hidden
markers and default-namespace aliases, advance to their next occurrence.
Time syntax checks numeric-unit boundaries, marker identifiers and extra
arguments before changing clocks; nested execute errors retain their input
positions. Builtin time completion includes clock/marker/timeline choices and
numeric unit suffixes; root completion combines builtin and plugin commands.
Custom resources, nested completions and broader parser cases remain partial.

Weather intensities preserve native float32 steps and project rain/thunder
changes to clients, including initial weather for late joins. Natural/random
weather cycling and complete duration behavior remain partial.

Scheduled block and fluid callbacks retain the registered type that queued them;
source and flowing fluid types remain distinct. Duplicate pending requests
preserve their first deadline and priority. Clone copies scheduled block
work across the full source box, including collected callbacks and empty filtered
selections. GameTest resets cancel queued and collected block callbacks. Native
chunk saves now write pending block/fluid callbacks with their registered owner,
relative deadline and priority. Go restores valid full-chunk queues, retaining
duplicate records, rejecting stale owners and preserving measured expiry and
clone behavior across real restarts. Saved delays remain relative until measured
player/forced chunk admission activates them; due dispatch separately requires
simulation readiness. Loaded queries do not generate their own result. General
unload/repack, the complete ticket lifecycle and native sub-tick order remain open.

Native structure-template lookups cache both present and missing results until
corner unload or `/reload`; unreadable generated NBT can fall back to bundled
templates. Complete datapack selection and reload coverage remains partial.

Execute if/unless block, fill replace and clone filtered support built-in block
tags, member properties and typed nested block-entity NBT subsets. Clone also
preserves measured native block-entity payloads and supported item components
across copies, moves and overlapping force copies. Barrel native data access
uses its live container storage, including supported stored item components. Strict clone suppresses callbacks and shape updates at destinations and moved
source cells. Ordinary clone notifies neighbors after copied block entities
are hydrated, including measured redstone-driven structure actions. Exact native
placement grouping, datapack tags and exhaustive parser diagnostics remain in
progress.

Scripted protocol tests exercise real vanilla server behavior. A graphical
client playtest is a separate check: client dependency downloads are currently
blocked in the managed environment. The [real-client skill](.agents/skills/testing-mc-go-real-client/SKILL.md)
records prerequisites, the observed blocker and verification procedures.

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
- `api/event/`, `api/plugin/`, `api/frontend/`, `api/player/`, `api/worldview/`,
  `api/registry/`, `server/` — public
  extension contracts and runtime constructor, usable by external modules.
  Frontends can admit transient presence/movement avatars and submit privileged
  player actions; Java accepts measured 26.1/775 and 26.2/776 wire profiles.
- `plugins/` — example plugins (`hello`, `anticheat`); the `cmd/mc-go`
  binary links only `hello`.
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
- `-level-type normal` — generate the vanilla overworld, nether and end
  from `-seed` (the flat preset is the default without a
  server.properties).

Run it like the real server instead: put a `server.properties` in the
`-server-dir` (or pass `-properties <file>`) and mc-go reads it, fills
every missing key with vanilla's default and rewrites it the way the
real server does. Its world is then the vanilla-layout directory
`level-name` (default `world`), generated (`level-type`, `level-seed`)
on first start; `server-port`, `motd`, `max-players`, `online-mode`,
`gamemode`, `difficulty`, the view/simulation distances, RCON and query
come from the file. Command-line flags still win over it.

```sh
printf 'online-mode=false\n' > server.properties
./mc-go
```

Join with a vanilla 26.1 or 26.2 client at `localhost:25565`; the server
runs offline mode, so any username works.

Java profile metadata is available through `server.JavaProfiles`. Both profiles
share one authoritative world and 26.2 gameplay rules. The 775 block mapping
represents 29,873 states; 2,493 newer states have no exact representation. Strict
mandatory-state failures retire the affected viewer; projection never
substitutes or rewrites authoritative state, and compatible viewers continue.
Some newer optional counters, attributes and cosmetics are omitted. The server
rejects unknown or unmodeled component input before mutation. This does not
establish complete 26.1 gameplay or client-physics parity.

Public `frontend.Core.Admit` accepts a trusted frontend's authenticated profile
and returns an `api/player.Connection` controlling that avatar. Java observers
see the same authoritative entity. A detached player snapshot precedes ordered,
revisioned presence, movement and correction updates, including Java dimension
and mode changes. Updates coalesce committed state at ownership boundaries;
they do not report every intermediate setter. Avatars support presence and
movement only: no inventory,
combat, survival simulation, persistence or world/chunk replication. Frontends
remain responsible for transport, authentication and authorization.

The attachment and admission context own each connection's lifetime. Detachment
now revokes retained `Core.Snapshot` and `Core.Execute` handles as well as avatar
connections; earlier releases retained those core handles until server shutdown.
Other attachments continue. Queue defaults are 128 updates and 1 MiB of queued
detached values; overflow retires the affected avatar. Dequeued adapter copies
and total process memory are outside this budget. One admission per attachment
and one move per connection can wait for simulation ownership; concurrent calls
return `player.ErrBusy`. Cancellation is rechecked before commit, but cannot
interrupt that mutex wait. `Next` cancellation and `Close` signal immediately;
retirement completes under simulation ownership.

Frontends can type-assert their supplied Core to the optional `frontend.Blocks`
capability.
`WatchBlocks` returns a fixed, finite box of canonical block states, followed
by ordered final-state deltas. A view contains at most 65,536 cells; snapshots
carry an immutable catalog for resolving runtime handles to complete state keys.
This covers owner-committed gameplay, commands and typed edits, including owned
direct block writes/restores. Snapshot capture and registration share that owner;
raw concurrent world access is not a new atomic transaction API. Views omit
lighting, block entities/NBT, entities and moving windows. Overflow terminates
the affected view while avatars and other views continue.

`EditBlock` is a privileged single-cell operation using an explicit dimension,
complete canonical state key and optional expected-state comparison. It reuses
the existing write side effects; it is not player placement or a rollback
transaction. Plugins can veto or modify this typed entry point and submit
callback-safe deferred edits with one completion receipt. The result describes
that edit's immediate side effects, independently of later scheduled changes
or coalesced stream revisions. Existing frontend interfaces and legacy block
observations remain compatible.

JSON block override saves write complete canonical state keys in version 2 and
read historical version 1 IDs through their 776 mapping. Native region-format
persistence keeps its existing names/properties encoding. Legacy item JSON
numeric persistence has not been migrated to catalog-independent keys.

Public `SaveWorld` in compact `-world` mode captures overrides under ownership,
then writes the detached snapshot outside it while the caller waits. Public
`-world-dir` saves also capture native world, entity and player state before
writing outside ownership. Capture can still pause gameplay and increase memory;
owned disconnect saves and `/save-all` still wait under ownership.
Public `SaveServerData` also writes a detached player/block-entity JSON snapshot;
its schema-three numeric item IDs are unchanged, and remembered offline players
survive subsequent saves. A committed newer Java session also prevents delayed
older cleanup from overwriting its remembered and native player state. Ordinary
disconnects and native player-file loading synchronize remembered aliases for
that UUID before later offline saves.
See [ARCHITECTURE.md](ARCHITECTURE.md#compact-world-saves) for ordering,
shutdown and persistence boundaries.

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


Recent bounded gameplay support includes persistent natural swamp-hut residents,
`/place structure` for swamp huts, desert pyramids (cellar, chests and
suspicious sand), jungle pyramids (moss selector, traps, chests and
dispensers), igloos (top/laboratory templates, chest, sign and furnace),
ocean/beached shipwrecks (supply/treasure/map chests), cold/warm ocean ruins
(large/cluster ruins, archaeology blocks and underwater ruin chests) and
jungle/swamp/desert ruined portals (portal chests) and nether fortresses (bridge/
castle pieces, nether-bridge chests and blaze spawners); correct `/fill`
result counts and loaded endpoint checks; and player powder-snow freezing while
world time is paused.
Native item saves preserve modeled fireworks, instrument removal, ominous
banners and explicit black dye. Missing base mount bounds and selected static
seats use measured dimensions and precision. These are measured subsets; see
`GOAL.md` for their tests and remaining
world, gameplay and compatibility gaps.

Ocean ruined portals are also available through `/place structure` on the flat
overworld. Their coldness follows the original generator biome even after
`/fillbiome` edits; other generator/dimension contexts remain unsupported.
