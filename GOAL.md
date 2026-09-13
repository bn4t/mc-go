# GOAL: Full-parity Minecraft Java Edition server in Go

Objective: a clean-room, from-scratch, multithreaded Minecraft Java Edition
server in Go that reaches **full behavioral parity with the vanilla 26.2
server (protocol 776)** and is verified by blackbox tests against the real
Mojang server over the wire.

## Ground rules (clean room)

- Go standard library only; no Minecraft libraries, no generated Mojang code.
- No decompiled or otherwise derived Mojang source. The vanilla jar is used
  only as (a) a black-box oracle (run it, observe bytes) and (b) a data
  source (version.json, bundled data packs).
- Public protocol documentation (minecraft.wiki protocol 776) is the
  reference for packet IDs, shapes, and semantics.
- Every behavior change is proven by a blackbox test comparing mc-go and
  vanilla, or by a scripted-client test when vanilla has no counterpart.

## Definition of done

- Every serverbound packet the 26.2 client can send is parsed and handled
  with vanilla semantics (no ignored packets that alter observable state).
- Every clientbound packet needed for a real client to play a full game is
  emitted with vanilla-compatible framing, IDs, and semantics.
- Parity areas below are all green in `tests/blackbox` against the real jar.
- A real 26.2 client can join, walk, mine, place, craft, fight, die, respawn,
  change dimension, trade, use containers, and persist its world and player
  data, matching vanilla behavior.

## Parity matrix (living document)

### Protocol (776)
- [x] Handshake / status / ping
- [x] Login (offline), compression
- [~] Configuration: known packs, feature flags, registry data, tags
- [~] Play join burst
- [ ] Full packet ID + shape coverage for all states
- [ ] Encryption / online mode
- [ ] Cookie / transfer / dialog / code-of-conduct packets

### World
- [~] Sharded world, block overrides, persistence
- [~] Chunk streaming, light packet
- [ ] Vanilla terrain generation (seed, noise, biomes, structures)
- [x] Full block-state registry: all 32366 states with their property
      schemas, generated from the official report and checked back
      against it state for state
- [ ] Lighting engine (sky + block, incremental)
- [~] Fluids: water and lava flow with vanilla's levels, drop-off,
      falling columns and dry-up. Redstone, block ticks and random
      ticks remain (the random-tick sampler and its per-section gate
      are in place, with no block rules registered yet)
- [~] Block entities (chests, furnaces, signs, ...)
- [~] Dimensions: overworld/nether/end worlds, nether portal travel
  with 8x coordinate scaling and auto-built return portals, Respawn
  field parity (dimension type, sea level, obfuscated seed, flat flag,
  cooldown, data-kept), per-dimension chunk/light encoding and entity
  scoping

### Entities
- [~] Entity manager, spawn/remove/teleport
- [ ] Full entity type registry
- [ ] Physics, collision, AI, pathfinding
- [ ] Damage, health, death, drops, XP
- [ ] Projectiles, vehicles, mobs, villagers

### Player
- [~] Movement tracking, keep-alive, chat, tab list
- [ ] Full inventory + container protocol
- [ ] Survival mode: health, hunger, regen, death/respawn
- [ ] Crafting, recipes, recipe book
- [ ] Advancements, statistics, scoreboard, teams
- [ ] Player data persistence

### Server
- [~] Multithreaded session/tick/world-shard model
- [ ] Full command tree (Brigadier) parity
- [ ] World save format parity (region files)
- [ ] RCON, query, properties parity
- [ ] Performance targets under vanilla-like load

## Progress log

- 2026-09-12: Fluid simulation. Water and lava now flow: levels 0-7 with
  8 as the falling state, a level of 0 as the permanent source, a
  per-fluid drop-off that makes water reach seven blocks and overworld
  lava three (the nether, being ultra-warm, gives lava water's drop-off
  and a 10-tick delay instead of 30), and flow that drains away when its
  supply is cut.
  The rules were read off the real server rather than assumed, and two
  of them are only visible in the shape a waterfall makes: a source
  feeds both the block below and its four neighbours, while a non-source
  fluid that can still fall spreads only downward — so a column stays
  one block wide — and the column's foot fans out one block further than
  a resting source does (level 7 at distance 8, not 7). `TestFluidParity`
  runs three scenarios on mc-go and on vanilla, both frozen and advanced
  with `/tick step`, and compares the resulting worlds position by
  position over ~150 probed blocks. The one place the two are allowed to
  differ is the outermost block of a falling pool's arms, where vanilla
  does not reproduce its own answer run to run.
  Reading the world back needed `/execute if block` with a real block
  predicate, so `/execute` is now a proper subcommand chain
  (`if`/`unless block|entity|loaded`, `in`, `run`), joined by `/say`,
  `/fill` and `/tick`.

- 2026-09-12: The game clock. Every time-driven subsystem now hangs off
  one `GameTick`, driven by a clock that supports vanilla's `/tick`
  surface: freeze, unfreeze, step (synchronous — when the command
  returns, exactly N ticks have run), sprint, rate and query. Stepping
  is what made the fluid comparison possible at all: both servers can be
  frozen and advanced by the same exact number of ticks, instead of
  comparing two servers' luck with wall-clock timing. Two bugs the
  stepping exposed: a long synchronous command blocks the session read
  loop, so the keep-alive watchdog has to know the silence is ours (the
  session is now marked busy around each handler); and the random-tick
  sampler was reading twenty thousand blocks a tick across every section
  a player keeps loaded, so it is now gated per 16^3 section by a cache
  invalidated on block writes.

- 2026-09-12: The full block-state registry: all 32366 states with their
  property schemas, generated from the official report into a compact
  packed table and checked back against the report state for state.
  `BlockStateOf(name, props)` and `DescribeState(id)` make states
  addressable both ways — which is what let `/setblock` accept
  `water[level=3]` and what the fluid engine is built on. Two facts the
  generator now asserts rather than assumes: state ids are dense over
  [0, 32366) and each block's states occupy one contiguous range, which
  is what makes resolving a state to its block a binary search instead
  of a 32k-entry map.

- 2026-09-11: Dimensions: a per-dimension world/generator model with
  overworld (-64..320 flat), nether (0..256, netherrack, no skylight),
  and end (island). Sessions carry their current dimension; every
  entity broadcast, block-entity store, light update, and world query
  is dimension-scoped. Nether portal travel honors the creative/
  survival delay game rules, scales coordinates 8x both ways, and
  builds a return portal when none exists; a 300-tick cooldown gates
  re-entry. `/execute in <dim> run tp @s ...` crosses dimensions
  directly and `@s`/`@p` resolve to the sender. Wire-verified:
  `TestDimensionRespawnParity` matches vanilla's Respawn packet
  field-for-field including the sha256-obfuscated seed (little-endian
  both ways), sea level 32 in the nether, flat flag from the world
  type, 10-tick reported cooldown, and dataKept=3; portal round-trip,
  entity scoping, and the no-skylight light profile are covered by
  mc-go-only wire tests.
- 2026-09-10: Harness bug fixed (relative jar path vs. temp cwd); vanilla
  oracle operational via brew openjdk 26. Packet-ID tables for 776
  extracted from public docs and cross-checked against the official
  `--reports` output; `TestPacketIDConstantsMatchOfficialReport` pins
  every constant. Config stage now matches vanilla order (brand plugin
  message, feature flags, known packs, registries, tags, finish).
- 2026-09-10: Full configuration parity: all 29 synced registries and
  the Update Tags body are served verbatim from captures of the real
  server (`TestCaptureVanillaConfig`), in vanilla order.
- 2026-09-10: Official block-state/item/entity-type tables generated
  from `--reports` (`tools/genregistries`); pig=100, player=156, global
  block-state ids exact.
- 2026-09-10: Vanilla flat-world parity: flat preset terrain (bedrock,
  2 dirt, grass), 776 chunk format (prefixed heightmaps, fluid counts,
  padded palettes, inline light). `TestChunkParity` proves chunk (0,0)
  is byte-identical to the real server apart from vanilla's own
  nondeterministic heightmap ordering.
- 2026-09-10: Vanilla join burst parity: ability/held/recipe/command/
  border/time/spawn/ticking/container/health sync packets in vanilla
  order. `TestJoinBurstSequence` proves the pre-chunk packet sequence
  matches the real server (mc-go sends the empty Recipe Book Add once;
  recipe data is a pending milestone).
- 2026-09-10: Player inventory model (46-slot window, item stack codec,
  click modes 0-4, cursor, state ids), creative slot handling, drops
  and offhand swap, swing animation broadcast, client information
  parsing, command tree with per-command literals, Player Chat Message
  relay (unsigned), and periodic world-time sync.
- 2026-09-10: Client-announced view distance drives batched chunk
  streaming (17x17 for distance 8) with Chunk Batch Start/Finished and
  Set Chunk Cache Radius; scripted clients keep the minimal window.
  `TestMcGoOnlyViewDistanceStreams` proves the full stream over TCP.
- 2026-09-10: Survival gameplay foundation, verified at the packet
  level (no timing assertions): game-mode config (binary defaults to
  survival, library to creative), bare-hand break rules with look-vector
  targeting, block drops as item entities with gravity and pickup delay,
  pickup merging into the inventory, Take Item Entity + Remove Entities
  + Container Slot sync, fall damage with vanilla's 3-block threshold,
  death (Set Health 0 + Combat Death), and Client Command respawn
  (Respawn + SyncPos + health + chunk window). Player melee: Attack
  applies fist damage within reach, Hurt Animation to the victim,
  hurt Entity Event to everyone else, and Combat Death on lethal hits.
- 2026-09-10: Survival placement consumes the held item and syncs the
  slot (creative unchanged), with `/give` feeding the inventory through
  the normal pickup path. Per the project direction, blackbox coverage
  is packet-level (exact IDs and field encodings, event-driven waits);
  timing-based comparisons were removed as flaky.
- 2026-09-10: Column-model sky light engine: block changes recompute
  affected sections and emit Update Light after the block ack (vanilla
  order), with explicit arrays for changes and empty masks for all-dark
  sections. `TestBlockChangeSendsLightUpdate` decodes the arrays nibble
  by nibble for both shadowing and restore. Chunk-load light stays
  byte-identical to vanilla.
- 2026-09-10: Tab-list ping sync (keep-alive RTT -> Player Info Update
  update-latency to every session) and sneak pose metadata (Player
  Input 0x20/clear -> Set Entity Data index 6, type 20,
  SNEAKING=5/STANDING=0), each covered by deterministic packet-level
  tests. Chunk parity now canonicalizes light per section before
  comparing, since vanilla's mask coverage is nondeterministic across
  runs; heightmaps, sections, and block entities still compare byte
  for byte.
- 2026-09-10: Mob basics: `/summon <entity>` for the common passive and
  hostile mobs, living-entity health metadata (index 9, float),
  gravity ticks, and player attacks that damage and remove mobs.
  `TestMobSummonAttackPackets` proves Spawn Entity (pig type), health
  metadata 10 -> 9..1, hurt Entity Events, and the final Remove
  Entities over the wire.
- 2026-09-10: Mob loot drops (per-mob tables rolled into item entities)
  and inventory drops on player death with vanilla's 40-tick pickup
  delay, both covered by packet-level tests
  (`TestMobSummonAttackPackets` now also checks the porkchop drop;
  `TestDeathDropsInventoryPackets` checks the full stack drop and the
  cleared inventory).
- 2026-09-10: Experience: mobs drop XP orbs (type 49) with value
  metadata, orbs are collected into the player, and Set Experience
  syncs bar/level/total using vanilla's level thresholds.
  `TestMobXPOrbPackets` covers the full wire flow.
- 2026-09-10: Mob wander AI: deterministic heading changes and a
  ledge-safe walk stride, broadcast as Entity Position Sync; mobs fall
  before they walk. `TestMobWanderPackets` waits for the movement
  event on the wire (no timing assertion); unit tests cover the path
  and ledge refusal. Chasing, fleeing, and pathfinding remain.
- 2026-09-10: Sign block entities: sign placement creates block entity
  state and broadcasts Block Entity Data (type 7, vanilla-shaped NBT),
  using a sign opens the editor, and Update Sign stores and fans the
  text back out. `/setblock` now resolves the full official block
  palette instead of the four-block subset. `TestSignEditorPackets`
  covers the wire flow; unit tests cover lifecycle and NBT shape.
- 2026-09-10: Player abilities sync: the serverbound ability update
  toggles creative flight and echoes the server-authoritative flags;
  survival cannot start flying. Covered by unit tests.
- 2026-09-10: Chest containers: chest block entities with 27 slots,
  Open Screen (generic 9x3) plus a 63-slot menu, click modes
  pickup/place, quick-move (main inventory first), hotbar swap and
  throw, close acknowledgement, and contents dropped as item entities
  when the chest is broken. `TestChestPackets` covers the wire flow;
  unit tests cover transfer, quick-move, and drops. Furnaces, hoppers,
  and the rest of the container family remain.
- 2026-09-10: Server data persistence (versioned JSON next to the block
  overrides): player inventory, cursor, XP, and survival stats survive
  disconnects; sign text and chest contents persist and restore.
  Leaving sessions are snapshotted synchronously; the binary loads at
  boot and saves on the autosave tick and shutdown.
  `TestPersistenceReconnectPackets` proves the join Container Content
  carries persisted items on the wire. Region-file format parity is
  still pending.
- 2026-09-10: Furnaces: furnace block entities with input/fuel/output,
  the vanilla furnace menu (type 14, 39 slots) and the four burn/cook
  progress properties, a deterministic 20 TPS smelting tick with a
  starter recipe and fuel table, take-only output, quick-move support,
  and contents dropped on break. `TestFurnacePackets` covers the wire
  flow (open, 39 slots, item transfers); unit tests prove smelting.
  Crafting, hoppers, and the rest of the container family remain.
- 2026-09-10: Crafting (2x2 player grid): shaped and shapeless recipe
  matching with bounding-box normalization (logs -> planks, sticks,
  crafting table, torches), take-only result slot with left-click,
  shift-click craft-all, grid consumption, and result refresh on every
  grid click. `TestCraftingPackets` proves the wire flow (result
  appears, taking it clears grid and result); unit tests cover recipe
  shapes and stale-result clearing. The 3x3 crafting-table menu and
  the recipe book remain.
- 2026-09-10: 3x3 crafting table menu (type 12, 46 slots): grid
  placement/removal, quick-move, result recompute on every click, and
  take/shift-craft semantics, with 3x3 recipes (furnace ring, chest
  ring). `TestCraftingTablePackets` proves the wire flow; unit tests
  cover the 2x2-in-3x3 table recipe and the furnace ring. The recipe
  book and recipe-unlock sync remain.
- 2026-09-10: Hunger and regeneration: exhaustion from sprinting
  (per block), jumping, and attacking drains saturation then food;
  well-fed players regenerate health (synced every 80 ticks with the
  exhaustion cost); empty hunger deals starvation damage down to one
  heart. Set Health carries health/food/saturation to the client.
  `TestHungerSprintPackets` proves sprint drain on the wire; unit tests
  cover exhaustion, regeneration, and the starvation floor.
- 2026-09-10: Equipment sync: Set Equipment (all six visible slots with
  the continuation-bit framing) broadcasts armor/offhand changes, is
  sent for existing players when someone joins, and rides held-slot
  changes (main hand). A latent deadlock was fixed: the equipment
  broadcast used to run while holding the inventory write lock, which
  wedged joining sessions; the click handler now reports equipment
  changes so the caller broadcasts after unlocking.
  `TestEquipmentPackets` proves the wire flow for observers and
  join-time equipment; an encoding unit test pins the framing.
- 2026-09-10: Status player sample: up to 12 online players with
  dashed UUIDs, matching vanilla's `players.sample`.
  `TestStatusSampleParity` asserts both servers list the same fetched
  player and UUID. Fixing this surfaced a real protocol bug: Client
  Information's chat-mode VarInt was missing from our parser and test
  clients, which made vanilla treat scripted clients as anonymous and
  blocked their entity spawns; the shape now matches the 776 spec.
- 2026-09-10: Damage Event (0x19): attacks, falls, starvation, and
  /kill emit the packet with damage_type registry ids, cause/direct
  entity ids (+1 framing), and the optional source position.
  `TestDamageEventPackets` proves the wire fields for player attacks;
  unit tests cover the player_attack and fall type ids and the
  cause/direct framing.
- 2026-09-10: Combat state: attacking tags both fighters for vanilla's
  15-second combat window, sending Enter Combat once and End Combat
  with the duration when it lapses. `TestCombatEnterPackets` proves
  the empty Enter Combat reaches both over the wire; a unit test walks
  the timer through to End Combat with the full 300-tick duration.
- 2026-09-10: Hoppers: five-slot block entities with the vanilla
  hopper menu (type 16, 41 slots), click transfer modes, contents
  dropped on break, and the 8-tick transfer tick that pulls from a
  container above into a container/furnace below (smeltables to the
  input, fuels to the fuel slot). `TestHopperPackets` covers the wire
  flow; unit tests cover chest-to-chest transfer and menu clicks.
  Dropper/dispenser and full transfer cooldown semantics remain.
- 2026-09-10: Eating: Use Item starts a 32-tick food use for the food
  table (bread, meats, fish, apples, carrots, rotten flesh), golden
  apples are edible at full hunger, and finishing restores food and
  saturation (capped), consumes the item, and syncs Set Health.
  `TestEatingPackets` proves the wire flow (saturation 14.6 and item
  consumption); unit tests cover bread restoration and the always-
  edible flag.
- 2026-09-10: Void/out-of-world damage: players below the world floor
  minus 64 take 4 damage every ten ticks with the out_of_world damage
  type until death. `TestVoidDamagePackets` proves the wire flow
  (/tp into the void -> Damage Event + Set Health); unit tests cover
  the ten-tick pacing and the lethal outcome.
- 2026-09-10: Weapon damage and armor reduction: main-hand swords,
  axes, pickaxes, shovels, and tridents deal their vanilla attack
  damage; the victim's armor points reduce it with vanilla's
  1 - min(20, armor)/25 formula. `TestWeaponDamagePackets` proves the
  iron-sword hit (health 14) on the wire; unit tests cover weapon
  damage and full iron armor (15 points -> 0.4 damage from a fist).
  Attack-cooldown scaling, enchantments, and durability remain.
- 2026-09-10: Scoreboard basics: /scoreboard objectives add/remove/
  setdisplay sidebar and players set/get/reset with the Update
  Objectives, Update Score, Display Objective, and Reset Score
  packets. `TestScoreboardPackets` covers the wire flow; a unit test
  decodes every field including the NBT display component. Teams and
  other objective criteria remain. TestJoinBurstSequence now drops
  tick-driven Update Time/Keep Alive packets before comparing, keeping
  the state-machine comparison strict without run-to-run flakes.
