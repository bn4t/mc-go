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
- [x] Chunk streaming, light packet. The streamed window is measured
      and matched exactly (square radius viewDistance+1 less a circular
      corner cut at viewDistance+2.75, 49/81/117/213/329 columns for view
      distances default/3/4/6/8, with a late Client Information
      re-streaming it and no chunk cache radius packet sent -
      `TestChunkWindowParity`), and so is the acknowledgement throttle
      that paces the batches: nine columns before the first
      acknowledgement and nothing after it, then ten batches per
      acknowledgement, at the acknowledged rate truncated and clamped to
      [1, 64], one batch a tick - `TestChunkThrottleParity`,
      `TestChunkThrottleStaysBlocked`
- [ ] Vanilla terrain generation (seed, noise, biomes, structures)
- [x] Full block-state registry: all 32366 states with their property
      schemas, generated from the official report and checked back
      against it state for state
- [ ] Lighting engine (sky + block, incremental)
- [~] Fluids: water and lava flow with vanilla's levels, drop-off,
      falling columns and dry-up. Redstone, block ticks and random
      ticks remain (the random-tick sampler and its per-section gate
      are in place, with no block rules registered yet)
- [x] Block entities (chests, furnaces, signs, hoppers, barrels, ...):
      saved and loaded in the real server's own chunk format, compared
      block entity for block entity both ways
      (`TestWorldBlockEntityParity`), with hopper transfer, hopper and
      furnace sided rules, and the smoker/blast-furnace fuel rate
      measured and matched (`TestHopperTransferParity`). The furnace
      family's recipe sets are the data pack's own, and what each of the
      three blocks will and will not cook, at what cook time, and which
      recipe id each counts it under, are compared against the real
      server's own blocks (`TestFurnaceRecipeParity`)
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
- [~] Full inventory + container protocol (chest + window-0 click
  parity scripted step by step, stale-state resync, menu titles, ender
  chest over shared player storage with close/reopen/break persistence;
  creative middle-click clone, close-returns-cursor (order + full
  probes), hopper/crafting/barrel/furnace menu parity with the table's
  grid-return, survival click restrictions, /gamemode command parity;
  anvil repair-combine parity: menu 8 + title + cost property, combine
  arithmetic, charged/quick-move/poor takes, invalid pairs, silent
  output clicks, close returns with no close echo; other menus
  - enchanting, brewing, beacon, lectern - still open)
- [~] Survival mode: health, hunger (two-clock regen + deferred
      exhaustion drain + i-frames measured), starvation, void, death/
      respawn, item drops on death — eating/fall damage present
- [~] Crafting, recipes, recipe book (full data.Recipes table wired,
  shaped/shapeless/mirror/repairitem/transmute parity vs 26.2 measured;
  /recipe give|take + RecipeBookAdd/Remove wire-parity including the
  1568-entry give-all blob; decorated_pot needs an unmodeled component,
  smithing needs the menu, bookcloning/shielddecoration/fireworks/
  mapextending need content components)
- [~] Advancements (recipe advancements: recipe_unlocked,
  inventory_changed, enter_block and the tick trigger all grant, the
  requirements-group semantics (every group needs one satisfied
  criterion) drive completion + reward unlocks, the deferred per-tick
  UpdateAdvancements flush ships added defs + criterion progress, and
  completed-advancement grants are no-ops - implemented and
  parity-tested incl. the 1560-entry give-all sync and give-triggered
  auto-unlocks; vanilla emits added/progress entries in identity-hash
  order which varies per run, so the test compares them as sets while
  per-criterion order — String-hash buckets seeded by a fastutil set —
  matches exactly. Non-recipe advancement triggers, scoreboard,
  teams remain open)
- [~] Statistics (Award Statistics on Client Status 1: counters for
  mined/crafted/used/broken/picked_up/dropped/killed/killed_by and the
  custom family — ticked play_time/total_world_time/time_since_death/
  time_since_rest/sneak_time, drop, deaths, damage_dealt/taken,
  mob_kills, player_kills, jump, leave_game, distance buckets
  walk/sprint/crouch/swim/fall/fly/climb/on-water/under-water —
  increment at their events; emit order reproduces vanilla's
  dirty-HashSet-copy → Object2IntOpenHashMap-descending-slot chain
  byte-for-byte, pinned by TestStatsParity and TestStatEmitOrder.
  Stats persist in the data file; item-break (broken) is emitted when
  a tool's durability is exhausted — pinned by TestToolWearParity —
  and exotic distance buckets (vehicles, walk_under_water edge cases)
  remain open)
- [x] Player data persistence (position, dimension, inventory, ender
  chest, xp, health, food restore on rejoin — plus statistics; world
  block data ships in the save file; per-player .dat files and
  advancement/recipe-book persistence remain open)

### Server
- [~] Multithreaded session/tick/world-shard model
- [~] Full command tree (Brigadier) parity: `/item replace` (with/from,
  block/entity) and `item modify` (registry ids, loot-function SNBT;
  bare set_damage compounds apply, damage truncating (1-f)*max in
  float32) match the real server reply for reply (`TestItemCommandParity`),
  and the whole modifier grammar is pinned case for case
  (`TestItemSNBTParity`)
- [ ] World save format parity (region files)
- [ ] RCON, query, properties parity
- [ ] Performance targets under vanilla-like load

## Progress log

- 2026-09-20: Tool/weapon durability: mining a non-instant block
  wears the held tool by its damage_per_block (generated
  itemToolDamagePerBlock/itemWeaponDamagePerAttack maps from the
  component reports), a landed hit wears a weapon by
  item_damage_per_attack, and exhausting an item empties the slot,
  broadcasts entity event 47, and emits the `broken` item stat.
  Order matches vanilla bytecode: mineBlock wears (and breaks, and
  emits `broken`) *before* the caller's `used` award, while attack
  is `used` then `postHurtEnemy` wear. Instant-break blocks (hardness
  0 — torch & friends added to the hardness table) skip wear but
  still count as `used`; a mob's hurt entity event goes to chunk
  trackers but never the attacker (measured). `TestToolWearParity`
  digs a pickaxe to break and swings a sword against both servers.
  Attack-cooldown scaling, enchantments (unbreaking), and the
  item_durability_changed advancement trigger remain open.

- 2026-09-20: Statistics slice: stat store with vanilla emit-order
  model (dirty HashSet → Sets.newHashSet copy iterates bucket order →
  Object2IntOpenHashMap descends slots; Stat name =
  `minecraft.<type>:minecraft.<value>` hashed as String), all the
  wired increments (mined on breakBlock, dropped/drop on throw,
  used on place+eat, crafted on take, picked_up on collect, killed/
  killed_by/player_kills/mob_kills on deaths, damage_dealt/taken in
  tenths, the four ticked timers + sneak_time, jump, leave_game,
  distance buckets), thrown-item drift physics so Q-drops land out of
  pickup reach, stats persisted in the data file with the map-order
  round-trip vanilla's stats/*.json does. TestStatsParity (join,
  give+Q-drop, mine dirt, walk) is byte-order-exact vs vanilla;
  TestStatEmitOrder pins both captured emit orders. Side observation:
  multi-case tests share one world and one spawn — items a previous
  case's player left on the ground can be picked up by the next joiner
  (seen once as a repair-mismatch diff of leftover hotbar stacks).
  Intermittent; a per-case arena would remove it.
- 2026-09-13: Anvil repair-combine parity, green against 26.2
  (`TestAnvilParity`, 41 steps on each server: two charged takes by
  hand and by quick-move, a poor take at 0 levels, a dirt+pick invalid
  pair, empty-output and place-into-output clicks, a close with the
  inputs full, and a reopen). Every step compares damage- and
  repair-cost-sensitive slot state, the cost property, XP levels,
  anvil-use sounds, Set Player Inventory returns, and the absence of
  a Container Close echo; both opens compare the title bytes. The
  script's setup wears picks with `item modify ... set_damage`, which
  forced the modifier engine's one exclusion half open: six vanilla
  fractions (0.0/0.1/0.2/0.5/0.8/0.9 -> damage
  250/225/200/125/49/25) pin the arithmetic to damage truncating
  (1-f)*max in 32-bit floats - the only rounding fitting 0.8 -> 49
  and 0.1 -> 225 at once (no linear f*M model fits all three) -
  pinned by internal `TestApplySetDamage`. Out-of-range fractions
  clamp; lists, compounds with conditions, empty stacks, and
  undamageables stay no-op successes; all unmeasured guesses flagged
  in code. Two
  bugs the differential caught: `clickLeft` consulted the spec's
  accept filter for player-side slots too, and `accepts` refuses any
  slot past the container, so parking the repaired pick into hotbar.8
  stranded it on the cursor - a new `menuAccepts` limits the filter
  to the container side (window-0 clicks never reach `clickLeft`,
  they have their own path); and `closeAnvil` returned the virtual
  output slot alongside the inputs, duplicating them when closing
  with an output computed - it now clears. Still open behind this:
  renames, material/unit repair, the degrade roll, costs past 39,
  throw/number-key/creative-clone on the output, full-durability
  non-fresh pairs, and main/offhand close-return numbering. The four
  `probe_anvil*_test.go` throwaways are deleted; the click driver's
  `stackKey` now carries nonzero damage/repair-cost and `foldPkts`
  folds Set Player Inventory (hotbar 0-8 shifted to window 36-44).
- 2026-09-13: /gamemode and the furnace script verified green, and the
  differentials rewrote three rules. `TestGamemodeParity` runs twelve
  /gamemode steps on two same-named ops per server: chats compare as
  canonicalized NBT trees (mc-go encodes compounds in Go map order,
  which is nondeterministic, while vanilla follows builder insertion
  order, so field byte order is out of scope - every key, value,
  style, and nesting level still pins), GameEvent byte for byte,
  attributes parsed as sets, playerinfo parsed, abilities as flag
  sets. Measured corrections: spectator abilities are flags 0x07, the
  switch never sets the flying bit (leaving a flighted mode only
  clears it, so a later creative switch resends bare 0x0D), the
  gamemode-only Player Info Update goes to every player including the
  target, the reach resync goes to the target only, an invalid mode
  stops the cursor at the end of the token (errTokenEnd, not errToken),
  and the switch answers abilities twice (count not pinned, flags
  compared as a set). One vanilla behavior stays out of the test's
  scope on purpose: whether a bystander sees the target's resync
  depends on entity tracking, not /gamemode (vanilla delivered it on
  three of six switches), so only the changed side's attrs compare.
  The furnace script (menu 14, dark throughout - a lit furnace streams
  ContainerProperty ticks and the settle never sees quiet) proved
  manual placement and hotbar swaps ignore the accept rules: vanilla
  takes dirt in the input by hand and swaps sticks into it, while
  quick-move honors them (coal merges to fuel, sticks stay out). Oak
  logs are both fuel and smeltable, so the shift probe uses sticks and
  the script never lights. Still open: manual placement into the
  output, the dual-nature quick-move order, lit-furnace menus (the
  settle cannot run on them), and the admin-broadcast gate level.
- 2026-09-13: Furnace menu click rules and /gamemode implemented,
  shipped unverified. The furnace output is take-only (empty cursor
  takes all, same-id merges up to the max stack, foreign placements
  resync), input takes what the block's recipe set smelts and fuel
  takes what burns (`furnaceAccepts`), quick-move and hotbar-swap
  into input/fuel obey the same rules; `TestMenuClickParity` grows a
  furnace script (menu 14, geometry 0/1/2/3-29/30-38) covering dirt
  rejection, coal/iron placement, shift order, swap, /item output
  fill, clone/take output, and close. /gamemode was measured with
  three throwaway probes (kept in tree as `probe_gamemode*_test.go`
  until the differential test is written): a switch sends GameEvent
  reason 3 with the mode, a success.self chat (or .other naming the
  target), gameMode.changed plus a gray-italic chat.type.admin
  broadcast to the other ops for remote targets, refreshed abilities,
  bare two-entry reach attributes when creative is left or entered
  (nothing otherwise, no speed entry, entry order nondeterministic),
  and a gamemode-only Player Info Update to every other player but
  the changed one; repeating the mode is silent. Implementation adds
  `runGamemode`, `updateAttributesMode`, `encodePlayerInfoGamemode`,
  and `broadcastAdmin`, and `sendAbilities` now follows the player's
  own mode instead of the server default. Still open: the
  `gamemode_parity_test.go` differential (two same-named ops per
  server, byte-compared chat/events, parsed attributes/playerinfo,
  behavior-proved mode) and the furnace blackbox run; entry-order
  and admin-gate-level guesses are flagged in code. Anvil and other
  menus still open. Drive-by repair in the same tree:
  `TestMcGoOnlySetblockStreamsChunk` was red since 10f1018 changed
  the /setblock confirm to the measured `commands.setblock.success`
  component - the test still decoded a plain-text root, so it now
  accepts the component form.
- 2026-09-13: Item handling finished, second half: creative clone,
  close-returns-cursor, and hopper/crafting/barrel menu parity, all
  measured off the live 26.2 server. Creative middle-click clones only
  for creative players, only with the middle button, only from a filled
  slot - a full stack keeping the slot's components, the slot unchanged
  (mc-go cloned on any button, dropped components, and let survival
  clone). A click on window 0 while any menu is open is ignored outright
  (mc-go applied it, moving the cursor under the open menu). Two
  client-liveness rules the failures taught: an ignored menu click
  resyncs the window instead of bumping state in silence, and a
  successful clone carries its slot's update - either silent path leaves
  the client's tracked state stale and the next real click is swallowed
  on one side only. Closing with something on the cursor returns it to
  the player inventory through the hotbar-first add path, with no cursor
  packet when it empties and the remainder staying on the cursor when
  the inventory is full (order pinned by occupying hotbar.0: the stack
  takes hotbar.1 over cleared main slot 1; the full case keeps 64 on the
  cursor on both). The crafting table keeps no storage, so its close
  also returns the grid - cursor first, then grid in slot order, pinned
  by ending with grid dirt and a cobble cursor (vanilla: cobble to
  hotbar.0, dirt past full hotbar.1 to hotbar.2). Two real gaps the new
  menus exposed: the hopper and crafting-table click answers never sent
  the cursor at all, now fixed. `TestContainerClickParity` grows the
  clone steps, the merge-back, the close return, and the order/full
  probes; new `TestMenuClickParity` runs pickup/place/clone/close on
  hopper, crafting table, and barrel; new
  `TestSurvivalClickRestrictions` pins the survival ignores on servers
  booted in survival, dig-test style. That style is load-bearing: mc-go
  has no /gamemode command (the first draft of the survival test used
  one and mc-go stayed creative while vanilla switched), so /gamemode
  joins the open list with the furnace/anvil/etc menus. Geometry note
  the close diagnosis depended on: the click cell opens a single chest,
  63 slots with the hotbar at 54-62, so slot 54 is hotbar.0.
- 2026-09-13: Ender chest opens over the player's own storage
  (`TestEnderChestParity`, green first run against 26.2). The menu is
  the chest's shape (generic_9x3, title container.enderchest compared
  byte for byte) and clicks run the chest path, so pickup/place, merge,
  and both quick-move directions match; close and reopen keep the
  contents, and breaking the block keeps them too - a second chest
  elsewhere shows the same stacks, on both servers. mc-go had no open
  path (a use fell through to placing); the window is one new
  openWindow arm over sess.ender, which already persisted with the
  player. The click driver's setup is parameterized behind
  newClickDriverOn for the next menu.
- 2026-09-13: `/item` slot registry corrected against the live server in
  a 38-command differential probe (replies plus from-copy reads, no
  dumps). The numbered families alias: hotbar.N is container.N (both
  index N, so a chest and a furnace both take hotbar.0-2 and the furnace
  refuses hotbar.3 as i(3)), and inventory.N is container.9+N (a chest
  takes inventory.0 as index 9, refuses inventory.26 as i(35)). The
  player has only 0-35: container.36-53 and horse.4-14 are known names it
  does not have, refused as no_changed naming the item on write,
  source.no_such_slot on read, no_changes on modify. mc-go had hotbar.N
  at 36+N and the player at 0-44, which the reply-only parity script
  could not see; fixed in parseItemSlot, playerSlotValid,
  read/writeEntitySlot, and syncItemSlot behind an entityMainSlot helper,
  with the aliasing pinned by a new TestItemSlotRegistry unit test.
  TestItemCommandParity, TestItemSNBTParity, TestContainerClickParity,
  and TestUseGateParity all pass against 26.2. The probe file is
  deleted; the evidence it added to item_slots.tsv's account lives in
  the itemcmd.go header comment.
- 2026-09-13: Item handling finished: max stack sizes, `/item` slot
  mapping, and a full container-click parity script. `TestItemSNBTParity`
  and the max-stack table work stood; what remained was the click
  protocol, and the real server corrected five mc-go rules, all measured
  off its own bytes. ContainerSetSlot carries its slot as a Short, not a
  VarInt (the vanilla bytes read `01 05 00 01 01 3e 0000`), fixed in every
  sender and every test reader. Quick-move from a container into the
  player fills backward from the hotbar's last slot (a dirt64 landed in
  hotbar.8), while shift-click inside window 0 fills forward from
  hotbar.0 - both directions pinned. Throw-all (mode 4, button 1) drops
  the stack into the world; the chest path moved it to the cursor
  instead. Creative and spectator players never pick up item entities -
  mc-go's magnet was quietly re-adding thrown drops to the hotbar. A
  Click Container with a stale state id is ignored, and the server
  answers with the whole window carrying the live state (measured on
  window 0, applied to all five click entry points: chest, generic
  container, crafting table, hopper, player inventory).
  `TestContainerClickParity` replays a 24-step script on both servers -
  pickup/place, right-click half and single, merge, quick-move, hotbar
  swap, throw one/all, drag, double-click collect, a stale menu click, a
  stale window-0 click - and compares carried, menu, and player slots
  after every step. Use Item On gates stand beside it
  (`TestUseGateParity`, 14/14): reach and teleport-confirm gates, menu
  titles as translation keys, the silent empty lectern. Three harness
  lessons, each paid for in debugging time: the sentinel's `has()`
  consumes packets, so setup traffic cannot be folded race-free and the
  click script heals window-0 state off a deliberate stale click
  instead; world spawn is random per boot, so the no-teleport cell reads
  the join position syncs and places its chest relative to the player;
  and a `0x71` that increments by 20 per step is the UpdateTime world
  age, not slot traffic. The six older packet-level click tests now
  carry live states through a shared `syncClickState` helper, and a unit
  test pins the stale-click resync directly
  (`TestClickStaleStateResyncs`).

- 2026-09-12: Hopper transfers, measured tick by tick and then made to
  match, and the block entity file behind them. The rules came from the
  real server's own tick loop rather than from memory: `/tick freeze`
  with `/tick step 1` and `data get block` after each step, in seven
  arrangements covering every facing a hopper can have and every
  container it works with. A hopper pushes toward its own `facing` and
  not always down; it pushes and pulls in the same tick, pushing first;
  it moves exactly one item per direction per transfer; the countdown
  runs *before* the hopper acts, so a transfer every eight ticks is
  eight ticks between one item and the next; a hopper that has never
  ticked carries `TransferCooldown` -1, which its first tick turns into
  the 0 an idle hopper keeps; and `hopper[facing=up]` is not a block the
  real server accepts at all ("does not accept 'up' for facing
  property"), so a hopper never pulls from the block above. A hopper
  feeding a furnace follows the real server's sided rules - above fills
  the input with anything, beside fills only the fuel slot and only with
  fuel, and only the result ever leaves - and an insert into the input
  slot is what makes the furnace recompute `cooking_total_time` (to the
  recipe's own time, or 200 when its recipe type has no recipe, which
  includes an input just emptied; a world load and the furnace's own
  smelting do not). `TestHopperTransferParity` builds the world once on
  the real server, copies it to both, advances each by the same 240
  ticks, and requires the two saved chunks to be identical block entity
  for block entity - which they now are. Two more furnace rules the
  same session turned up: a smoker and a blast furnace burn fuel at
  twice the rate (a coal lasts 800 ticks there against 1600 in a
  furnace, in `lit_total_time` as well as `lit_time_remaining`), and
  coal is not the only thing with no recipe. Alongside it, the chunk
  format: a saved palette names *every* state's block - a sign facing
  east is `minecraft:oak_sign` with `rotation=1`, not an unnamed state -
  and a palette entry mc-go cannot resolve now leaves the generator's
  own blocks standing rather than silently writing air over them
  (`TestChunkPaletteNamesEveryState`, `TestChunkLoadLeavesUnknownPalettesAlone`).
  Since this work: the furnace family's recipe *sets* are the data
  pack's own rather than a hand-written table, with the three sets'
  accept and reject edges, their cook times and their recipe ids
  compared against the real server's own blocks
  (`TestFurnaceRecipeParity`). Still open: mc-go has no `/item`
  command, which is how the real server builds the fixtures these tests
  hand both servers - the furnace test builds them on the real server
  for that reason.

- 2026-09-12: World saving, in both directions, against the real server.
  `TestWorldSaveParity` hands a world each way: the real 26.2 server
  saves one and mc-go loads it, mc-go saves one and the real server
  loads it, and the chunk mc-go writes for a given world state is
  compared field by field - DataVersion, status, section indices,
  palettes, all four heightmaps - with the chunk the real server wrote
  for the same state. Three things the test found and fixed, each
  invisible from inside mc-go: the saved palette spells its property map
  `Properties`, capitalised (the network palettes carry global state ids
  instead, and the lowercase key made every mc-go palette entry
  unreadable to the real server); a chunk the real server saves while
  still generating - "minecraft:biomes", "carvers", "structure_starts" -
  holds all-air placeholder sections that must not be applied, or loading
  a world erases the terrain it describes; and saving into a directory
  that does not exist yet failed outright, which is the ordinary case for
  a first save. The test also had to be made honest about its own
  premises: 26.2 keeps no spawn chunks loaded, so `/setblock` answered
  "That position is not loaded" until the test forced the chunk, and a
  player needs `op` before `/time` answers anything at all. The world
  files (level.dat, the three data/minecraft/*.dat, the dimension
  layout) round-trip through mc-go's own reader too, pinned by
  `TestLevelRoundTrip`; block entities in chunks, `poi/`, and the
  `entities/` region directories remain.

- 2026-09-12: Where water meets lava, measured. The fluid parity scenario
  that pours water at lava failed, and the fix was three wrong rules
  rather than a tuning problem: water *below* lava does not set the lava
  solid at all (the water is what changes, to stone, and the lava is
  untouched), the block below is not one of the five neighbours that count
  as contact, and a lava *flow* that water reaches sets to cobblestone
  rather than being swept away — a source sets to obsidian. The
  solidification also happens in the block-update path, in the same tick
  the water lands, not on the lava's own 30-tick schedule.
  `TestFluidContactParity` pins all of it: nine geometries (water beside,
  above, under and two and three blocks from a source and a flow), each
  run on mc-go and on vanilla, compared block by block at the moment the
  water lands and again once both worlds have settled.

- 2026-09-12: Command replies byte for byte, and the context message.
  `/time` and `/tick` now answer with exactly the components the real
  server sends, which is a stricter claim than "the same key and
  arguments": a translatable component carries no `text` field at all
  (not even an empty one), the tick count of `/tick step` rounds half up
  and the tick count of `/time set` saturates at 2147483647 rather than
  wrapping, a too-small float is `argument.float.low` and not
  `argument.float.small`, and `/time add`'s own reply is a long while
  `/time set`'s is an int wrapped in a compound.
  A failed parse is two messages, not one: the error, then a gray
  clickable echo of the input with the token the parse stopped on marked
  in red underline and a cursor marker at the end. The second message is
  only sent for failures the real server reports while reading — an
  unknown command, an unknown argument, a number that would not parse, a
  value it read and rejected — and not for failures it reports while
  running (`/tick step` while the game is running, a time marker that
  does not exist), which is why the cursor of the first kind is part of
  the reply and the second kind has no cursor at all.
  `TestTimeCommandParity` replays 40 clock commands against both servers
  and compares every message field by field, tags included, so a wrong
  cursor or a missing style flag fails the test.

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
- 2026-09-12: Survival digging is now the server's own model instead of
  the client's word. A start records the block, digTickStep adds one
  tick of progress per game tick from the public breaking formula
  (speed/hardness/30 when the block can be harvested with what is held,
  /100 when it cannot), and the block is destroyed by the server when
  the progress completes; the client's finish only commits the dig, so a
  scripted client that stops early cannot buy a faster break. Measured
  against the real server with a scripted client: grass bare-handed
  takes 18 ticks and stone 150, a start on its own never destroys
  anything, an abort discards the dig, a duplicate start cancels it and
  is answered with a resync of the true block, and swings, tick ends,
  inputs, and unrelated commands do not advance it.
  TestDigParity replays nine of those sequences against both servers and
  requires the same outcome and break time. The old 50 ms wall-clock
  guard (which rejected the dig a lagging client actually completed) and
  the look-direction raycast on start (vanilla only checks reach) are
  gone; /tp now accepts the vanilla <yaw> <pitch> form.
- 2026-09-12: Lava and water contact, measured against the real server:
  a lava source touched by water anywhere sets to obsidian; a lava flow
  with water falling onto it sets to cobblestone (the cobblestone
  generator); and a lava flow with water merely beside it is swept away
  to air, with the water flowing into the gap. The previous "flow beside
  water becomes cobblestone" rule was wrong and left cobblestone where
  vanilla leaves water. TestFluidParity gains scenario E (water falling
  onto a lava flow) and scenario D now passes.
- 2026-09-10: Scoreboard basics: /scoreboard objectives add/remove/
  setdisplay sidebar and players set/get/reset with the Update
  Objectives, Update Score, Display Objective, and Reset Score
  packets. `TestScoreboardPackets` covers the wire flow; a unit test
  decodes every field including the NBT display component. Teams and
  other objective criteria remain. TestJoinBurstSequence now drops
  tick-driven Update Time/Keep Alive packets before comparing, keeping
  the state-machine comparison strict without run-to-run flakes.
- 2026-09-12: The block entity array inside the chunk packet, which had
  been sent empty, and with it the first thing a real client could not
  see: a sign's text. The array is not the saved form. Measured by
  joining the real 26.2 server and reading its own chunk packets back,
  each entry is a packed column position byte, the y as a big-endian
  short, a varint block entity *type* (not the id string the file
  carries), and then the entry's client data - and for everything mc-go
  models except a sign that data is nothing at all: a chest holding
  three diamonds, a barrel, a furnace mid-cook with a lit time and a
  cook time, a hopper, a dropper, a dispenser, a shulker box, a brewing
  stand and a crafter all arrive as a single TAG_End byte, so a
  container's contents never travel with the chunk and the client learns
  them only when the menu is opened. A sign and a hanging sign are the
  exception, and the compound they carry is neither the file form (no
  id, x, y, z, keepPacked or components) nor a subset of it: front_text,
  back_text and is_waxed, each side being four text components held as
  strings plus its colour and glow. The packed byte's high nibble is x
  and its low nibble z, verified at a position where the two differ
  (the fixture sign sits at x=14, z=3, so a swapped pair could not pass),
  and the real server's own array is in its block entity map's hash
  order, which is not a property of the world; mc-go orders by position
  so the packet is a function of the world state. The fixture world also
  turned up two divergences of mc-go's own: its sign store discarded the
  colour, the glow and the waxed flag, so a dyed sign lost its styling
  both in the packet and across a save, and there is no way to *make* a
  styled sign yet - the dye, glow ink sac and honeycomb interactions are
  not implemented. Styling is now carried through the store, the saved
  chunk and mc-go's own data file. `TestChunkPacketBlockEntityParity`
  builds one of every block entity mc-go models on the real server
  (twelve containers holding state only a block entity can hold, a
  hanging sign, a two-line sign, and a sign whose front is red, glowing
  and waxed), hands identical copies to both servers, joins each with a
  scripted client and requires the two chunk packets' block entity
  arrays to match entry for entry. Two protocol facts the same
  measurement pinned down: `player_loaded` carries no body in 776 (a
  byte in it makes the real server disconnect the client with "was
  larger than I expected"), and the real server ignores interactions
  from a client that has not yet accepted a teleport - `use_item_on` is
  acted on only after the client answers the `player_position` packet's
  teleport id with `accept_teleportation`, which any test that drives an
  interaction has to do first.
  Still open: the dye/glow-ink/honeycomb interactions themselves, and
  a blackbox test for the update_sign broadcast (the real server
  re-sends the same nameless sign compound to every client tracking the
  chunk as block_entity_data).
- 2026-09-13: Three measured rules of the real 26.2 server, all found by
  scripted blackbox probes and now implemented and compared step for
  step against it.
  (1) The interaction gate. A use or dig a player sends is dropped until
  the client reports its terrain loaded (`player_loaded`, no body in
  776), or until 3.0 seconds (60 ticks) have passed since the join,
  whichever comes first. Measured by sending the same use from the
  moment of joining, one fresh client per delay: swallowed at 0.72, 1.62,
  2.12 and 2.62 seconds, acted on at 3.12, 3.62, 4.12 and 5.12. The
  measurement only means anything with the target chunks loaded - a
  forceload command that vanilla silently refused (`forceload add -64
  -64 256 256` is over its limit) left the target chunks unloaded and
  produced an all-zero "the grace never ends" curve.
  `TestInteractionGateParity` runs the script against both servers: a use
  inside the first moments is dropped, the same use works the instant the
  report goes out, and a client that never reports is let through once
  the grace has run out. The scripted clients in this suite report
  loading as they join (joinPlayBlackbox), so no other test has to know
  about the gate.
  (2) The sign editor lock. A sign's editor is held by one player at a
  time. Another player's use of that sign is answered with *nothing at
  all* - no editor and not even the block entity data a dye would bring -
  and the lock is per sign, not per side (a player behind the sign is
  refused too) and not per player (the holder can reopen its own editor
  as often as it likes). It is released by the holder writing the sign on
  the side it opened (the next player is given the editor at once), by
  the holder disconnecting (free at once), and by the holder moving more
  than ten blocks away (locked at nine blocks from the sign, free at ten;
  the readings fit a squared distance of 100 from the player's feet to
  the centre of the sign's block). There is no timeout: thirty seconds of
  standing still with the editor open still locks the sign. A waxed sign
  answers nothing whatever the item. And `update_sign` itself is gated on
  being the current holder - measured by reading the sign back with the
  real server's own `/data get block`, a scripted client that was never
  given the editor changes nothing, whether or not another player was
  editing at the time. mc-go's sign store grew an `editors` map with the
  measured release rules, and `TestSignEditorLockParity` drives three
  clients through the whole rule (refusal, the per-sign control, the
  released-by-write hand-off, the five-block/ twelve-block edges, the
  disconnect) and requires the same answer from both servers at every
  step.
  (3) The chunk window. It is not a square: it is the square of radius
  viewDistance+1 with the corners beyond a circle of radius
  viewDistance+2.75 cut away. Measured by joining with one view distance
  after another, acking the chunk batches, and listing every column
  streamed: view distance 2 (and a client that announces nothing at all,
  which the real server reads as 2) gets the whole 7x7 square, 49
  columns; 3 gets the whole 9x9 square, 81; 4 gets 117 (the 11x11 square
  less its four corners: (5,4) at squared distance 41 is in, (5,5) at 50
  is out); 6 gets 213; 8 gets 329. An integer radius cannot produce the
  window at 8 (radius 10 gives 317 columns), and the readings pin the
  constant to [2.7279, 2.7648) - 11/4 is the value used, and every
  measured distance is reproduced exactly. A client that announces its
  view distance late has the wider window streamed to it at once (329
  columns the moment an 8 arrives), and asking for less takes the excess
  away. Two divergences of mc-go's own came out of the same measurement:
  it streamed a 3x3 window to a client that never announced a distance
  (the real server streams 7x7), and it never re-streamed when a late
  Client Information arrived. Both are fixed, and `TestChunkWindowParity`
  compares the *column sets* of both servers for the default, 2, 3, 4 and
  8 - a window that is merely the right size does not pass.
  The same probes settled two protocol facts. The real server never sends
  Set Render Distance (chunk cache radius, 0x5F): with no Client
  Information at all, after announcing 8, and after asking for 2, every
  play packet it sent was listed and the packet never appeared, while Set
  Center Chunk (0x5E) did - so mc-go no longer sends it, and the tests
  that expected it now require its absence. And chunk streaming is
  throttled by the client's batch acknowledgements: a client that answers
  keep-alive and teleports but never acks a chunk batch is sent nine
  columns and nothing more, however long it waits, while mc-go sends the
  whole window in one batch.

  The throttle is now closed. Chunk Batch Received carries the client's
  chunks-per-tick rate, and on 26.2 one acknowledgement buys exactly ten
  batches: the opening nine columns for a client that has not
  acknowledged, and then, per acknowledgement, ten batches of that rate,
  one batch a tick, the first of them at once. Measured rate by rate:
  ack(1) gives ten columns as ten batches of one before falling silent,
  ack(2) gives twenty, ack(3) thirty, ack(25) the whole 81-column window
  of a view distance of 3 as 25, 25, 25, 6. The rate is the float
  truncated and clamped to [1, 64] (1000 gives batches of 64), and a rate
  below one is still a batch of one column but spaced ceil(1/rate) ticks
  apart - 0.1 gives ten columns over five seconds, 0.25 four ticks apart,
  0.5 and 0.9 two, and a rate of zero is one column every 5050 ms. The
  silence before the first acknowledgement is a property of the player,
  not of the window: a never-acking client teleported four hundred blocks
  into new country, with the teleport confirmed and the whole new window
  waiting, is sent nothing at all. Two smaller facts fell out of the same
  probes: an acknowledgement that arrives before the opening batch has
  gone out replaces it, so the first batch is 25 rather than nine; and
  the acknowledged rate survives a dimension change, which mc-go already
  did. `TestChunkThrottleParity` compares both servers batch size by
  batch size and tick by tick, and `TestChunkThrottleStaysBlocked`
  requires the confirmed-teleport silence from both.

  Matching the pacing needed a world-side fix rather than a protocol one:
  a batch could not go out a tick after the last because building it cost
  ten milliseconds a column. `World.shardFor` allocated and ran an FNV
  digest per *block* - 98304 times per column - and `FillBox` collected a
  position and two slice appends per block as well, so a 25-column batch
  took 300 ms and its batches came 300 ms apart instead of 50. Shards are
  now routed with an arithmetic mix and grouped by column, a chunk column
  costs one shard round trip instead of 256, and `encodeChunk` fell from
  10 ms to 1.4 ms - which the parity test can see, because it compares
  when each batch lands and not only what it holds.
  Test infrastructure: two goroutines writing the same connection
  interleave the two writes of a packet (protocol.Writer writes the
  length prefix and the body separately) and desynchronise the server's
  decoder, which showed up as random disconnects in the probing clients.
  The blackbox harness now wraps every client connection in a mutex for
  writes.
- 2026-09-13: The furnace family, at the level below the one the hopper
  session reached: which inputs each of the three blocks will and will
  not cook, how long each takes, and which recipe id each records.
  `cookRecipes` is now built from the bundled data pack rather than a
  hand-written table, so the three sets are the real server's own - and
  they are genuinely three: the bundled pack lists 110 smelting recipes
  (156 inputs after tags are expanded), 62 blasting and 9 smoking, and
  the sets are asymmetric in both directions. A blast furnace smelts an
  iron pickaxe into nuggets at half the furnace's time and refuses beef;
  a smoker cooks beef and refuses sand and raw iron, both of which are
  perfectly good smelting recipes. The thirteen-entry hand table accepted
  the same thirteen inputs in all three blocks, which is wrong in both
  directions at once, and no fixture built from it could tell the sets
  apart.
  `TestFurnaceRecipeParity` is built to fail on exactly that: nine
  fixtures, one world built once on the real server and copied to both,
  451 simulated ticks, and every block entity compared field for field
  afterwards. Each fixture is chosen for one edge - a tag member, a
  tool, food, an ore - and the reject fixtures are the point: a block
  given an input its own recipe type does not list comes out untouched,
  never lit, with its cook total left at the recipe lookup's 200
  fallback rather than its own type's time. The real server's own blocks
  are checked against those rules before the comparison, so the test
  cannot pass by comparing two worlds where nothing happened.
  What the same run turned up, none of it visible from inside mc-go:
  (1) `RecipesUsed` - the compound a furnace family block saves with a
  count per recipe id, which names the *variant* that was cooked: a
  smoker's beef is `cooked_beef_from_smoking` and a blast furnace's raw
  iron is `iron_ingot_from_blasting_raw_iron`, where a plain furnace
  writes `cooked_beef`. mc-go wrote an empty compound for every furnace
  and now writes the real ids.
  (2) The real server will not over-stack an item through `/item
  replace`: `with minecraft:iron_pickaxe 4` is refused with
  "minecraft:iron_pickaxe can only stack up to 1", so the tools in the
  fixture are given one and the fixture's item count is 39 and not 45.
  (3) Two save hazards that make a fixture world silently different from
  the one that was built, both measured: a `/stop` after `/save-all
  flush` sometimes writes the *older* of the two copies of a chunk back
  out (the last-written chunk, about half the time - which is why the
  builder flushes and is then killed rather than stopped, and why the
  test reads the runner's world after the flush and before its stop);
  and a `/forceload` ticket is persisted into the world's
  `chunk_tickets.dat`, so a world handed over with one has that chunk
  loaded and *ticking* from the moment the next server boots. Seven
  ticks of it, in every fixture, on a world whose only difference was
  the ticket - and a player's ticket, which is transient, leaves the
  world clean. mc-go gained `Server.StartFrozen` for the other half of
  the same problem: a joining player ticks the world for the seconds it
  takes a freeze command to land, so a test that hands over a world and
  needs it unstepped has to be able to say "no ticks yet".
  (4) The game loop slept a flat period *after* each tick, which makes
  the period the tick's own duration plus the sleep. A tick that
  streams twenty-five columns costs as much as a tick, so mc-go sent its
  chunk batches every hundred milliseconds where the real server sends
  them every fifty - found by `TestChunkThrottleParity`, which compares
  when each batch lands. The loop now sleeps what is left of the tick's
  budget once the tick has run, which is the real server's own
  arithmetic.
  (5) `/tick sprint n` simulates n+1 ticks. Measured on 26.2, three
  sprints of ten in a row moved the game clock by eleven each, a sprint
  of one by two, and a sprint of forty by forty-one - read off `/time
  query gametime` and cross-checked against the cook and burn counters
  of a furnace that had never been lit, which can only move on a
  simulated tick. The command is handled inside a tick and the sprint
  applies to the rest of that tick as well, so the tick it arrives on is
  simulated too. `/tick step n` is the other way round: exactly n, run
  at twenty ticks a second whatever the machine can do (a step of a
  hundred is still going five seconds after it has answered, which is
  why the tests sprint). mc-go now does both, and `TestTickCountParity`
  pins all five cases against the real server's clock.
  Test-side: `checkFurnacesRan` read its fields as `CookTimeTotal`,
  `CookTime` and `BurnTime` off a compound the real server spells
  `cooking_total_time`, `cooking_time_spent` and `lit_time_remaining`.
  With the type assertion allowed to fail, every one of those reads was
  zero - and zero is what half of those checks are looking for - so the
  oracle check passed whatever the world said. The fields are read
  through a helper that fails when the field is missing. The sign editor
  lock test had the same shape of bug in its setup: it placed its three
  signs before forcing the chunks, so the far sign, three chunks out and
  outside the window the setup player's own connection loads, was
  refused with "That position is not loaded" - a refusal its filter did
  not name, leaving a missing sign whose every use read as the lock.
- 2026-09-13: `/item replace` and `item modify` are done, with the
  modifier grammar measured token by token against the live 26.2 server
  in twenty-two differential rounds and pinned by `TestItemSNBTParity`
  (612 cases, every reply compared component for component). The
  command itself was the smaller half: replace with/from over block and
  entity slots, modify by registry id or loot-function SNBT, per-item
  max stacks from the generated table refusing over-stacks naming the
  cap. The grammar was the real work, and vanilla's parser has opinions
  a clean-room guess would never land on: a `_` digit separator is only
  a separator between digits, otherwise the number ends before it (so
  `[1_.5]` is a word but `[1__2]` is 12); in arrays a trailing one voids
  the element (`[B;1_]` wants `+`) unless the run is zero-led, which
  wants the hex marker instead; a separator inside array hex is its own
  error (`underscore_not_allowed`) at the run's start; bytes pre-parse
  at 32 bits and cast to 8 (`[B;2147483647]` is out of range,
  `[B;2147483648]` names its digits); int hex parses unsigned
  (`[I;0xFFFFFFFF]` is -1); longs peek past a `-0x` (empty wants a hex
  numeral, digits are rejected as negative); overflow messages strip a
  `+` but keep a `-`, and quote only the digits
  (`For input string: "ff.." under radix 16`); doubles print by exact
  decimal, shortest from two significant digits up. Two things the
  rounds turned up that stay open: a modifier that parses but must then
  run (`{function:"minecraft:set_count",count:2}`) is accepted and not
  applied - the one exclusion in both suites - and signed zero-led
  array edges like `[B;-01]` are guessed from composition, not measured.
  The probe files are deleted; the pins live in
  `tests/blackbox/itemsnbt_test.go`. Next in line behind this: the
  unchecked Player rows (inventory/container protocol, survival,
  crafting, persistence) all build on stacks being right, which they
  now are.
- 2026-09-13: Join recipe burst served verbatim. A fresh join on the
  live server carries Update Recipes (3360 bytes: 7 property sets, then
  319 stonecutting recipes, no fuel set), 8-false book settings, and
  the empty book add; mc-go now sends the registry bytes as captured
  (`internal/server/testdata/vanilla-26.2/play/update_recipes.bin`) and
  `TestRecipeBurstParity` compares all three packets fresh-join to
  fresh-join. Measurement changed the test design: the property-set
  order and the item order inside each set vary boot to boot (first set
  smoker in one boot, campfire the next), while the sets as multisets
  and the whole recipe tail are byte-stable across boots - so the test
  canonicalizes (sets sorted by id, items numerically) before comparing.
  The furnace_input ids resolve through `reports/registries.json` to
  exactly the smelting inputs. Still open behind this: the post-spawn
  unlock add (one entry, crafting_table), advancement sync, the unlock
  engine itself, and known-recipe persistence.
- 2026-09-13: Fresh-spawn recipe unlock and advancement sync served.
  After the entity metadata the live server sends a second book add
  (166 bytes, the single crafting_table entry) and the advancement
  sync (275 bytes); mc-go sends both in the same slot, pinned by
  `TestSpawnUnlockParity`. The unlock bytes are boot-stable and go out
  verbatim. The advancement packet decoded fully: reset, the two
  recipe advancement definitions, then a two-entry progress map
  (recipes/root locked on impossible; crafting_table with
  unlock_right_away achieved and has_the_recipe locked), then show.
  Two nondeterminisms, both measured across boots: the progress entry
  order shuffles (same HashMap habit as the recipe property sets) and
  the award timestamp is the server's own wall clock - so the test
  compares definitions byte for byte, progress by entry, and the
  timestamp by recency window instead of equality, and mc-go splices
  the live join time into the capture at the measured offset
  (`buildAdvancements`, splice pinned by the internal
  `TestAdvancementSplit` round-trip). The full-burst diff this fell
  out of also mapped the next gaps without touching them: Set
  Experience and Update Attributes are absent on mc-go, the command
  tree is smaller, and the post-chunk player-info pattern differs.
  - 2026-09-13: Spawn-state sync is done: Set Experience, Update
  Attributes, and the three Update Time shapes, all measured against
  the live server and pinned by `TestSpawnStateParity`,
  `TestRejoinAttributesParity`, `TestPeriodicTimeParity`, and
  `TestTimeSetBroadcastParity` (plus internal `TestUpdateAttributesFull`,
  `TestUpdateAttributesRejoin`, `TestUpdateTimeBodies`). Positions in
  the burst: experience (0,0,0, 6 bytes) rides immediately after Set
  Health, and attributes go out right after the opening chunk batch.
  A fresh name gets one attributes packet with the full creative set -
  block range 8 (base 4.5, +0.5 creative_mode_block_range), entity
  range 13 (base 3.0, +2.0 creative_mode_entity_range), speed 26 (base
  float32(0.1) widened, no modifiers) - count 03, entry order
  shuffling per send. A returning name gets a reset stub (always the
  bare speed entry, other slots bare ranges and/or degenerate fillers,
  3 slots, once 4) plus a second packet with exactly the two ranged
  modifiers, which complete the set. The fillers replay byte-for-byte
  (`d4 00 08` and `84 01 20`, each plus seven zero bytes): ids 84/132
  sit outside the 40-entry attribute registry, so the client skips
  them, and every vanilla rejoin carrying them proves the tolerance.
  Update Time comes in three shapes: the join packet (age plus game
  clock 1 at the age and the day clock at the day, 29 bytes), the
  once-a-second tick (clockless age, 9 bytes, +20 ticks a second), and
  the /time set/add broadcast (age plus the single day clock 0 at the
  new day, 20 bytes) - mc-go used to send the clocked form for all
  three. Two shuffles the measurements forced into the tests: the day
  clock's id is 0 or 2 per join (it flips even across spaced rejoins
  of one name, and entry order flips too - 2,0,0,0,0,2 across six
  spaced joins of one boot), so mc-go sends 2 and the tests accept
  either; the stub mix is unpatterned (bare-8, bare-13, one filler, or
  two across samples), so the tests pin the invariant (speed always
  present, slots from the observed set) rather than one mix, and mc-go
  sends speed plus both fillers. Server Data needed no code: both
  sides send an unnamed NBT string MOTD plus icon-absent, MOTD-driven.
  Method note that cost real time: eyeballing hex substrings invents
  entries (`0d 40 08` contains `d4 00 08` across a boundary, `e7 01`
  wide-eid headers read as extra body bytes) - every parity claim
  here was re-verified with a parser that consumes end-exact, and the
  committed attribute decoder tells well-formed from degenerate by
  trial for exactly that reason. Still open, all measured: the day
  clock's 2-to-0 flip past a few hundred ticks of age (between 222
  and 382 - needs an idle-sweep threshold hunt); count-01 on solo
  wide-eid first packets (3 samples: two stubs, one full; the one
  accompanied wide-eid full drew 03, so the leading candidate trigger is
  solo-and-wide, n=4); the 4-slot stub singleton (one ops rejoin on a
  post-/time-set world); survival-join modifiers (only creative
  measured); rejoin experience with nonzero XP; mob attribute tracking
  (one ambient packet: max_health 16, speed 0.5875); entity-id gaps of
  ~50 per join (chunk animals); the accompanied clock form
  ([0,day],[1,day] with epsilon fractions, n=1); and confirming the
  rapid-rejoin corruption bundle (clock ids 8/9, count-01) with a
  clean spaced four-join run. None of these bite the harness, whose
  joins are young, narrow-eid, and solo.

- 2026-09-20: Real-client playtest round found three join-blocking
  wire bugs and one dead loop, all fixed and pinned. Login finish now
  appends the trailing session-id UUID the 26.2 client requires
  (DecoderException without it; readLoginSuccess parses the property
  array and the 16-byte tail end-exact). The join-time set_time now
  sends the two-clock form measured live - clock 0 (overworld) carries
  DAY time, clock 1 (the_end) carries AGE, disambiguated by adding 5000
  and rejoining; the stale {1,2} ids kicked the client because the
  clock registry only has entries 0 and 1. The rejoin
  update_attributes stub is now the real triple (block range 8=4.5,
  entity range 13=3.0, speed 26=0.1, all bare) - the old filler ids
  84/132 are past the static 40-entry attribute registry and kick.
  Survival dig had two bugs: a repeated start on the same block must
  RESTART the timer with the finish latched (measured: re-start at
  ~10t then finish breaks grass at ~28t, not ~18t, not never), which
  mc-go previously cancelled; and the shipped binary never ticked at
  all - main called fe.Serve directly so startGameTicks/startTimeSync
  were dead code (proven live: gametime stayed 0 for minutes, /tick
  step advanced it, and a stepped tick completed a latched dig).
  Server.ServeFrontend now owns the lifecycle for any named frontend
  and main uses it. Join/leave log lines added. Crafting now resolves
  against the full generated data.Recipes table (shaped with vanilla's
  horizontal mirroring, shapeless as multisets, tag-flattened
  ingredients) and the recipe book indexes the same table - the old
  hand-written 11-recipe list and its broken key resolver are gone.
  TestCraftRecipeParity replays 8 click sequences on both servers
  (pickaxe, stairs + mirrored stairs, corner-placed log, shapeless
  andesite, cake, a damaged-bow dispenser, 2x2) and every slot/carry
  state matches vanilla end-to-end. Probes deleted once pinned; the
  repeated-start discriminator lives on in TestDigParity.

- 2026-09-20 (cont.): Two more playtest-found bugs fixed. Rejoin
  position restore now actually reaches the client - the persist path
  existed but untrack gated even the in-memory savedPlayers write on
  dataPath() != "", so a -world "" server resumed nothing; and
  handlePlay's sendSyncPos + entity record used the WorldSpawn locals
  captured BEFORE applySavedPlayer ran, so restored state never hit
  the wire. Both fixed; TestRejoinPositionParity lands the player back
  on the teleported spot on both servers. It also surfaced a /tp
  argument gap: bare-integer x/z center on the block (100 -> 100.5)
  while decimals and ~ stay exact, and ~ resolves against the entity's
  exact position rather than the block floor. parseDouble now takes
  the double base + a per-axis center flag; portal teleports center
  the same way.

Special crafting types are wired: the single-pass craftResult loop
dispatches per recipe type, so crafting_special_repairitem (two
same-id damageables merge, damage = dmgA+dmgB-max-floor(max/20),
clamped at 0 - confirmed against vanilla at 80+100 on stone
pickaxes) and crafting_transmute (input stack retyped to
r.Result.Item keeping its components; bundle+dye measured) now
answer identically to vanilla. Three new TestCraftRecipeParity
cases pin repair, repair's same-id gate, and bundle recoloring.
The measurement surfaced a /item grammar gap along the way: the
item-spec bracket form (item[comp=v,...], plus !comp to clear)
wasn't parsed at all, so `item replace ... with
stone_pickaxe[damage=80]` refused its target. parseItemStack now
splits the bracket and applyItemComponents sets damage and
repair_cost (the only modeled scalars); unknown components are
ignored.

The health/hunger surface turned out to be a tick-model bug, not a
content gap: measured on 26.2, a player parked in the void while the
game was frozen died in seconds and a hurt player healed nothing across
hundreds of /tick step'd ticks — the per-player connection tick
(void damage, hunger, eating, digging, combat, portals) runs on wall
clock, ungated by /tick freeze and never stepped. Those steps moved
from GameTick to a playerTick the run loop calls every wall-clock tick
regardless of freeze. Vanilla also emits at most one Set Health per
tick with end-of-tick values, so damage/starvation/exhaustion now set a
healthDirty flag the player tick flushes. Two more measured details:
post-hit invulnerability is 20 ticks with the excess-only rule (a 4.0
void hit lands every ~10, not every tick), and regen is two clocks -
min(sat,6)/6 every 10 while saturation lasts and food is 20, then 1
every 80 while food >= 18; the heal adds exhaustion that only drains
on the NEXT food tick, which is why vanilla's heal packet still shows
the pre-drain saturation. TestHungerRegenParity diffs the packet
physics through a void dip plus a fed recovery window.

- 2026-09-20: Recipe unlock engine: /recipe give|take with the full
  RecipeDisplayEntry wire model reverse-engineered from the 26.2 jar -
  display types (shapeless/shaped/furnace/stonecutter/smithing), all 11
  slot-display codecs (composite, tag, with_remainder, dyed,
  only_with_component, with_any_potion, smithing_trim...), trimmed
  shaped patterns, per-type cook-time defaults (200/100/600),
  ranged-transmute expansion (map_cloning emits one display per
  material count), suspicious-stew component patches, and the recipe's
  own show_notification flag driving the notify bit. Entry order is
  bookable-recipes-in-data order with group/category/requirements
  resolving exactly as measured. /recipe give emits only newly-known
  recipes (already-known are skipped, not re-sent), give on a special
  or unknown recipe fails with the right translation keys, take emits
  RecipeBookRemove for the removed displays only, and success replies
  carry {int N, player} under give/take.success.single. Compound NBT
  now writes keys in Java HashMap bucket order, which is what makes the
  reply bytes line up at all. Pinned byte-exact:
  TestGiveAllBytes re-generates the whole give-all Add body (91865
  bytes, 1568 entries) and TestRecipeCommandParity diffs every add,
  remove and reply through a give/dup/take/take-all/give-all script.

- 2026-09-20: Recipe advancement sync: /recipe give now grants the
  recipe's has_the_recipe criterion and queues a dirty advancement, and
  the player tick flushes UpdateAdvancements with the defs the client
  has not seen plus criterion progress - the deferred flush is why the
  packet lands after the reply. Granting a completed advancement's
  leftover criteria is a no-op (crafting_table's unlock_right_away
  completes it at join, so give * reports 1560 progress entries, not
  1561). Both the added set and progress map iterate in identity-hash
  order on the real server - two vanilla runs of give * produced
  different wire orders - so the parity test compares entries as sets;
  criterion order inside an entry IS deterministic and matched all
  1560 entries exactly once modelled as HashMap buckets fed by the
  requirements' fastutil ObjectOpenHashSet order. Recipe advancement
  defs come from a new generated table (recipeadvs_gen.go: recipe ->
  advancement id, requirements, JSON-order criteria).
- 2026-09-20: inventory_changed + enter_block trigger engine: the tick
  diffs the inventory snapshot and addStack fires synchronously, so a
  /give that completes a has_* criterion emits the reward's
  RecipeBookAdd before the command reply exactly as vanilla does (one
  Add per completed advancement). Also cracked the advancement
  requirements semantics: each outer group is satisfied by ANY one of
  its criteria (AdvancementRequirements.test = all-groups-any-match),
  so `recipe give *` completes every recipe advancement through
  has_the_recipe and later pickups stay silent - verified against
  vanilla which emits nothing for a give after give-all. /give now
  takes an optional target (@s or player name) and answers with the
  real commands.give.success.single component ({""-keyed count arg,
  square-bracket item display with show_item hover, entity display
  with suggest_command + show_entity hover) - byte-exact vs vanilla.
  TestRecipeCommandParity gained keepalive echo (vanilla kicks a client
  that never answers) and the give-oak_log/give-stone steps; trigger
  adds compare as a multiset since vanilla's listener order is
  identity-hash dependent.
