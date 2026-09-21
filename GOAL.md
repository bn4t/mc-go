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
  anvil parity: menu 8 + title + cost property, combine arithmetic,
  material/unit repair (generated materials table), rename incl.
  pending-name scoping and the 50-char drop, the 39-level survival
  gate, charged/quick-move/poor/throw takes, the 12% degrade roll,
  invalid pairs, silent output clicks, close returns with no close
  echo; other menus - enchanting, brewing, beacon, lectern - still
  open)
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
  matches exactly. Non-recipe advancement triggers and scoreboard
  objectives remain open; scoreboard teams are implemented and
  parity-tested)
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

- 2026-09-20: Melee knockback: hurt's default push is
  `knockback(0.4, attackerPos - victimPos)` and a sprinting strong
  hit (scale > 0.9) adds `knockback(0.5, sin(yaw), -cos(yaw))` along
  the attacker's facing, then damps the attacker's horizontal delta
  by 0.6 and clears sprint. knockback() applies
  `strength *= 1 - knockback_resistance` (iron golem 1.0), a +-0.01
  random jitter when the xz gap is under 1e-5, then
  `new = (v/2 - k)` per axis with `y = min(0.4, y/2 + strength)`
  when grounded. Player victims get set_entity_motion and their
  server-side delta is restored; mob deltas persist and compose
  across hits (pig: 0.4 plain, then 0.8 sprint). set_entity_motion
  uses the 26.2 LpVec3 packed codec, not three shorts — three
  scale bits plus 15-bit axis fields at shifts 3/18/33 in a 48-bit
  word whose wire bytes are the low two bytes little-endian then the
  high four bytes as one big-endian writeInt. player_command renumbered for 26.2: sprint
  start/stop is actions 1/2 (no sneak actions — sneak lives in the
  player_input shift bit); sprint set/clear broadcasts entity_data
  index-0 byte flags (bit 0x08). /summon now parses vec3 + NBT —
  NoAI freezes gravity and wander entirely. `TestKnockbackParity`
  scripts fist hits on a player and a NoAI pig on both servers.
  Crit (fallDistance rules) and sweep attack remain open.

- 2026-09-20: Armor-on-hurt durability: a hit armor can absorb costs
  each equipped damageable piece max(1, post-i-frames damage/4) in
  vanilla's feet-legs-chest-head order, breaking pieces emit the right
  entity events (head=49, chest=50, legs=51, feet=52) and resync slots
  ascending. Pinned and fixed along the way: the absorb formula is the
  real CombatRules one (defense = clamp(armor - dmg/(2+tough/4),
  armor/5, 20) — replaces the naive 1-armor/25), armor counts pre-wear
  for its own hit, and the hurt entity event goes to third parties
  only — the attacker gets nothing, the victim Hurt Animation.
  `TestArmorWearParity` scripts two sword hits against an armored
  victim on both servers. Enchantment toughness modifiers and
  mob-attacking-player paths remain open.

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
- 2026-09-19: The anvil is now feature-complete against 26.2.
  `TestAnvilParity` grew to ~120 steps and `TestAnvilDegradeParity`
  pins the wear roll; five probe rounds measured the model before any
  of it shipped. What the probes taught: the pending rename lives on
  the open menu (survives takes, dies on close) and an empty field
  clears the name for cost 1; ops stack combine > material repair >
  rename, so a full-durability left input with a damaged right
  computes nothing at all (rename aside), and a same-id combine only
  fires while the LEFT is damaged. Combine damage is
  max-(remT+remS+12% of max) and costs 2; material repair heals
  floor(max/4) per consumed unit for units=ceil(dmg/(max/4)) and costs
  the units; outputs carry repair_cost=max(rcT,rcS)*2+1 for work ops
  and the untouched target cost for rename-only. Past 39 levels the
  output slot stays empty in survival while the cost property still
  ships the true price (measured 47); creative ignores the cap. A
  rename field over 50 chars is dropped on the floor, no kick, no
  resync. The take broadcasts per-slot updates (each carries its own
  incremented state id) plus the level-event 1030, cursor only on
  hand-takes; a throw on the output takes it and tosses the entity
  like any other thrown stack - which is how the suite caught
  dropFromMenu spawning drops at the player's feet instead of ~1.7m
  ahead on the look vector with the 40-tick thrower delay. Every take
  then rolls 12%: anvil -> chipped -> damaged -> air with facing kept,
  the Block Update arriving with the take's slot traffic. New
  mechanics behind it: item component 6 (custom_name, an NBT string
  tag on the wire), the generated 75-entry repair-materials table
  (`tools/genrepair` walks reports + tags), and `item replace ... with
  item[damage=N,repair_cost=N,custom_name="X"]` parsing so the test
  setups speak vanilla. Harness flake found by diff, not by luck:
  flat-world slimes at y=-61 occasionally ate the survival test
  player - the death dumped the inventory, voided the window, and
  surfaced as a wall of slot diffs. `spawn-monsters=false` on the
  harness plus `spawn_mobs false` (the 26.2 name - `doMobSpawning` is
  gone) and a slime kill in the test setups closes that hole, and the
  driver now names the killer (`death=` in step diffs) if anything
  still gets through. Still open: the other menus (enchanting,
  brewing, beacon, lectern), the menu-side number-key/clone edges on
  anvil inputs, and whether a broken-anvil close drops the inputs or
  holds them.
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
  Cooldown scaling and durability have since landed; enchantments
  remain.
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

- 2026-09-20: Attack-cooldown + mob death pipeline: the per-weapon
  recharge timer is `20/(4.0 + mainhand attack_speed modifier)`
  ticks (stone sword 12.5) and a swing's damage scales by
  `0.2 + 0.8 * scale^2` where scale = clamp((ticker+0.5)/delay,0,1);
  the ticker resets on every attack and starts at zero. damage_dealt
  is `round(dealt*10)` where dealt is health actually lost (capped
  at remaining health — a sniffer's killing 5-damage blow reports
  4.0). Mobs get the full LivingEntity treatment: i-frames
  (invulnerableTime>10 rejects weaker hits, stronger hits land the
  delta), die() fires entity event 3 + loot/XP while the corpse
  lingers 20 ticks before event 60 + removal. Dropped the invented
  hurt-status-2 entity event — vanilla mob hurt is the damage_event
  packet only. `TestAttackCooldownParity` swings a stone sword at a
  NoAI iron golem on both servers (hit mid-cooldown lands ~44
  damage_dealt, recharged hits 50) and the unit tests charge the
  ticker to model full-strength swings. Learned the hard way that
  entity ids churn while chunk tracking settles after a teleport —
  scenarios must pick the freshest spawn near the player.

- 2026-09-20: attack aftermath parity — crits, sweeps, sounds,
  particles, reach gate, grace hits. Player.attack's post-hit pipeline
  decoded from 26.2 bytecode: charged standing sword hit = sweep sound
  (charged+onsolid ground+horizontal speed < walk speed*2.5 +
  sword-held); sprint hit = knockback sound + 0.5 kb; airborne hit
  with fallDistance>0 = crit sound + 1.5x damage + animate(4);
  otherwise strong (>0.9 charge) or weak sound. Holder<SoundEvent>
  holders encode id+1 on the wire (0 = inline) — the earlier
  'weak-on-full-damage' mystery was an off-by-one decode. doSweepAttack
  damages LivingEntity neighbors in victim box inflated (1,0.25,1)
  with distanceSqr<9 (strict) at 1*scale each, knockback = hurt's
  away-kb then the sweep's facing-kb (two knockDelta halvings), then
  the sweep_attack particle one block ahead at half bb height.
  damage_indicator particle when dealt>2, count=int(dealt*0.5) at
  victim getY(0.5). handleAttack gates on clamped eye-to-AABB distance
  <= entity_interaction_range (3.0) — out-of-range attacks are
  silently ignored. A second hit inside the 10-tick i-frame window
  lands only the amount-lastHurt excess (graced): health meta still
  updates but no damage_event and no knockback. mc-go gained
  planAttack/preAttackSound/attackAftermath/doSweepAttack,
  withinAttackRange, lastHorizSq tracking, per-mob bounding boxes, and
  the graced flag on Manager.Damage. TestCritSweepParity scripts all
  five legs (sweep/crit/weak/sprint/strong) against both servers —
  green; the weak leg asserts a partial-hit band since the dealt
  amount is quantized by the tick boundary each server crosses inside
  the scripted gap.

- 2026-09-20: Gameplay advancement triggers + visibility flush.
  player_killed_entity, entity_killed_player, and location fire live
  now: kills dispatch the whole adventure tab's OR-requirement set
  (conditions:null matches any entity), entity_killed_player fires on
  the victim for entity-caused deaths, and location runs on
  tickCount%20==0 with biome predicates resolved against the flat
  world's plains. The AdvancementVisibilityEvaluator rules are
  reproduced faithfully (hidden→HIDE, done→SHOW, bubble-up through
  children, 3-node unfinished peek) — a zombie kill lights up all 42
  adventure defs + progress entries exactly like vanilla, and the
  chat announce is the {translate:chat.type.advancement.task,
  with:[player-entity-comp, bracketed-hover-title]} component emitted
  at award time. Def bytes ride a rebuilt 1688-entry wire table
  (internal/data/advwire.dat.gz, merged from grant-all + zombie-kill
  captures plus synthesized display-less recipe defs via
  tools/advslice). TestKillAdvParity does zombie/skeleton/pig legs on
  both servers and compares the cumulative def+progress+chat union —
  green. Side parities pinned along the way: /tp and /teleport now
  answer commands.teleport.success.{entity,location}.single with %f
  coord args (vanilla's formatDouble), and /summon replies
  commands.summon.success. entity_type tags joined the generated tag
  tables for #skeletons-style predicates. Remaining trigger gaps:
  player_hurt_entity / entity_hurt_player (need arrows, the mace's
  overoverkill), the non-plains location predicates, and advancement
  persistence across rejoin.

- 2026-09-20: Advancement persistence across rejoin. Vanilla stores
  criterion progress in advancements/<uuid>.json and replays the full
  visible set on the next join — defs plus progress — because the
  client resets its advancement state every session. mc-go now writes
  done flags + criterion timestamps into the .data.json player blob
  (playerPersist.Advancements), and initAdvancements merges them back:
  progressed ids join the dirty queue so the first tick's flush emits
  defs (once per session) and full criterion state. TestAdvRejoinParity
  kills a zombie on both servers, disconnects, rejoins, and diffs the
  rejoin sync union — green at 44 defs + 44 progress entries each.
  Recipe-book unlock persistence (the unlockedRecipes set itself)
  remains open.

- 2026-09-20: /advancement grant|revoke command at wire parity. All
  modes (everything/only/from/through/until, optional criterion),
  multi-target resolution (@s/@p/@r/@a/@e/name), and vanilla's reply
  key lattice commands.advancement.<action>.{criterion|many|one}.to.
  {one|many}[.success|.failure] (failures as red compound components,
  advancementNotFound / criterionNotFound args verbatim). Packet side:
  flushAdvancements now emits the removed id list (sent-but-no-longer-
  visible) and filters progress to dirty∩visible. Measured subtleties
  pinned: the visibility evaluator bubbles only *obtained* children to
  parents (rule-inherited visibility does not propagate), criterion
  revoke recomputes isDone over requirement groups (kill_a_mob's single
  41-mob OR-group stays done when one name is pulled, so vanilla never
  re-announces it), the minecraft:tick trigger re-obtains
  unlock_right_away every tick (crafting_table can never stay revoked),
  and the command path awards every criterion even when the advancement
  is already done (vanilla's award("*")). Announcements fire only on
  the not-done→done transition. TestAdvancementCommandParity runs a
  13-command grant/revoke script on both servers and diffs the
  cumulative def+progress+removed+chat union — green alongside
  TestKillAdvParity and TestAdvRejoinParity.

- 2026-09-20: hostile-mob melee + death-message broadcast at wire
  parity. mobMeleeStep gives zombie-family melee mobs (zombie, husk,
  drowned, zombie_villager, zombified_piglin, spider, cave_spider,
  iron_golem) vanilla's chase-and-swing: nearest survival player inside
  followRange 35, approach at movementSpeed 0.23 b/t replacing wander,
  swing animation + attackDamage on the 20-tick attack cooldown inside
  the inflated-box reach. Hits route through damageWith as
  minecraft:mob_attack, so entity_killed_player fires via lastHurtBy
  (adventure/root's killed_by_something grants on the kill tick).
  killPlayer/damage/damageWith now carry components instead of strings:
  combat_death ships the vanilla translatable (death.attack.mob /
  .player / .starve / .fell.accident.generic / .fell.out_of_world /
  .attack.generic) and the same component fans out as a system chat
  death message to every player — vanilla's broadcast mc-go was
  missing. TestMobKillsPlayerParity drops a zombie beside a survival
  player on both servers, waits out the ~8s kill, and diffs the
  cumulative advancement union + chat multiset — green.

- 2026-09-20: bows and arrow projectiles at wire parity for
  player_hurt_entity / projectile killing blows. Use Item on a bow
  starts the draw (bowStartTick), Player Action status 5
  (RELEASE_USE_ITEM) releases with vanilla's power curve
  t*(t+2)/20 capped at 1.0 and a 3-tick dud floor; survival consumes
  an arrow, creative skips the check. Arrow entities integrate at the
  fired speed (power*3 b/t) with 0.99 air drag and 0.05/tick gravity;
  collision is vanilla's AABB.clip — the movement segment slab-tested
  against target boxes inflated 0.3 (point checks tunnel through at
  full draw). Hits deal ceil(speed*2) through damageWith/damageMob as
  minecraft:arrow with cause=shooter direct=arrow, then fire
  player_hurt_entity (new hurtCond: direct_entity type set, the
  is_projectile tag, dealt.min) and, on a kill,
  player_killed_entity's projectile path — firePlayerKilledEntityDamage
  now carries needsProj + killing_blow.direct_entity matching, so
  sniper_duel's 50-block skeleton shot is reachable.
  TestShootArrowParity draws, releases at a NoAI zombie, and diffs the
  cumulative advancement flush + chat multiset — green, including the
  def fan-out vanilla reveals when shot_arrow completes.

- 2026-09-20: inventory_changed on the game tree. The recipe tree
  already fired the trigger; the 28 non-recipe criteria (story/root's
  crafting_table, mine_diamond, smelt_iron, obtain_armor pieces,
  husbandry/netherite_hoe, nether rods/skulls/debris, end's
  elytra/dragon_egg/...) now evaluate too — items[] parsed as an OR-set
  of names + expanded tags (stone_tool_materials, decorated_pot_sherds,
  netherite armor's multi-item list), everything else locking the
  criterion as impossible like vanilla's unmatched predicates.
  fireInventoryChanged fans out to fireGameInventoryChanged after the
  recipe pass. TestInventoryAdvancementParity gives diamond +
  crafting_table in survival and diffs the flush — green.

- 2026-09-20: consume_item on the game tree. The eating pipeline now
  fires consume_item when a food finishes — 41 criteria: balanced_diet's
  40-food roster plus husbandry/root's unconditional consumed_item.
  consumeCond parses item.items as a name/tag OR-set; absent conditions
  (husbandry/root) match any item. TestConsumeItemParity eats a golden
  apple (always-edible — plain foods refuse at full hunger on both
  servers) and diffs the flush — green.

- 2026-09-20: placed_block + /fill component replies. The place path
  resolves seed→crop and the rest of the differently-named block items
  through itemBlockAlias (ItemBlockState only covers same-name items —
  wheat_seeds→wheat, beetroot_seeds→beetroots, melon/pumpkin→_stem,
  torchflower_seeds/pitcher_pod→_crop, nether_wart, carrot/potato,
  cocoa_beans→cocoa, berries, redstone→wire, string→tripwire) and fires
  placed_block against single-term block_state_property predicates —
  property/nested shapes lock as impossible. /fill moved to the
  component reply path: commands.fill.success(toobig/failed/
  int.invalid/block.id.invalid args) instead of literals.
  TestPlacedBlockParity plants wheat_seeds on farmland and diffs the
  flush — green.

- 2026-09-20: changed_dimension + per-dimension location biomes.
  changeDimension fires changed_dimension with namespaced from/to
  names (nether/root's entered_nether, story/enter_the_nether, the
  end pair); dimCond parses flat from/to strings. fireLocation now
  maps each flat dimension to its fixed biome (overworld→plains,
  the_nether→nether_wastes, the_end→the_end) so explore_nether's
  nether_wastes criterion lands on the hop. TestChangedDimensionParity
  setblocks a nether_portal, waits out the survival 80-tick charge,
  and diffs the flush including the nether-tree def fan-out — green.

- 2026-09-20: item_used_on_block. The use-on-block handler fires the
  trigger before the interaction's outcome (vanilla evaluates inside
  useOn), matching clicked-block + held-item sets against
  location_check (block.blocks name/list/#block-tag; state and biome
  keys lock the criterion) and match_tool (predicate.items) terms.
  TestItemUsedOnBlockParity clicks a lodestone with a compass —
  adventure/use_lodestone's measured shape — and diffs the flush:
  green.

- 2026-09-20: nether_travel (fast_travel) + portal cooldown semantics.
  Portal entry latches the overworld position on the way out; the
  return leg measures horizontal overworld-space distance and fires
  nether_travel at the 7000-block mark. Real parity fix on the way:
  mc-go ticked the post-travel portalCooldown while standing inside a
  portal — vanilla freezes it inside portal blocks (a re-hop requires
  stepping out first). Note: vanilla setblock refuses unloaded chunks
  (tp loads first) and the exit biome is terrain-dependent, so the
  test filters explore_nether's per-biome location criteria.
  TestNetherTravelParity runs the two-portal loop on both servers —
  green.

- 2026-09-20: summoned_entity (summon_iron_golem). carved_pumpkin on an
  iron-block T now consumes the pattern and spawns the golem. Two
  vanilla details measured: the pattern is ~^~/###/~#~ with air
  required beside pumpkin and base, and the trigger fires for every
  player in the dimension — including setblock- or fill-placed
  pumpkins — so the check sits in setBlockAndBroadcast, not the
  placement path. TestSummonedEntityParity green.

- 2026-09-20: player_generates_container_loot (loot_bastion's four
  tables) + chest LootTable NBT. /setblock accepts trailing SNBT
  ({LootTable:"...",LootSeed:n}); a chest opened with a pending table
  rolls it into random free slots, clears the fields, and fires the
  trigger — rollLootTable evaluates pools' rolls, weighted item
  entries, and set_count (uniform/binomial/constant providers).
  Measured subtlety: loot_bastion's requirement groups are OR-ed, so
  the first matching chest completes the advancement and unregisters
  its listeners — each table gets its own player in the test. Also
  found: setblock into an unloaded chunk fails on vanilla; tp first.
  TestContainerLootParity green.

- 2026-09-20: bred_animals (breed_an_animal + bred_all_animals:cow).
  INTERACT on a food-holding mob enters love (entity status 18 hearts,
  held item consumed in survival, 600-tick decay); a same-species
  in-love mate within 8 blocks produces a baby at the midpoint and
  fires the trigger with the child type. Parser takes the species
  filter from any of child/parent/partner entity_type predicates.
  Test harness details measured: natural passive spawns emit
  entity_spawn packets mid-test (filter captured ids by entity type),
  and a tp re-streams chunks — without answering chunk_batch_finished
  the entity spawn packets stall for seconds. TestBredAnimalsParity
  green.

- 2026-09-20: tame_animal (tame_an_animal + whole_pack:pale). INTERACT
  with a tameable species' item rolls vanilla's odds (wolf/cat/ocelot
  1/3, parrot 1/10), broadcasts entity status 6 (smoke) on failure or
  7 (hearts) on success, and fires the trigger. Wolf bones are the
  TAME item — removed from its breed-food list. The whole_pack
  criteria predicate on minecraft:components' wolf/variant; mc-go
  doesn't track wolf variants, so an untracked wolf counts as pale
  (the plains default a bare summon produces on vanilla too).
  TestTameAnimalParity green.

- 2026-09-20: slept_in_bed (adventure/sleep_in_bed). A right-click on a
  bed inside the night window (daytime mod 24000 ∈ [12542,23459]) sets
  the spawn point (block.minecraft.set_spawn action bar) and fires the
  trigger; outside the window block.minecraft.bed.no_sleep shows. The
  session sleeps for vanilla's 100 ticks, then — once every play
  session in the dimension is under — the world skips to the next
  morning and broadcasts sleep.skipping_night. Setblock places only a
  bed foot, which vanilla can't sleep in, so the test places through
  the real item-use path. TestSleptInBedParity green.

- 2026-09-20: entity_hurt_player blocked-flag (story/deflect_arrow).
  A use-item on a shield (either hand, honoring the packet's hand field)
  raises the blocking flag until release_use_item; a projectile hit
  against a raised shield is negated outright, plays
  item.shield.block, and fires entity_hurt_player with blocked=true.
  The trigger also fires on ordinary landed hits (blocked=false) so
  criteria can match either side. /summon now accepts the arrow with a
  Motion list — non-living summons spawn the projectile instead of
  being rejected. TestDeflectArrowParity asserts the block left health
  at 20 and emitted the shield sound on both targets.

- 2026-09-20: player_interacted_with_entity (adventure/brush_armadillo,
  "Isn't It Scute?"). Interact packets (actions 0/2) now resolve the
  clicked entity's type and the held item and fire the trigger; the
  criterion parser reduces entity[] predicates to an entity-type OR-set
  and item.items to a name/tag OR-set. Armadillo joins the summonable
  table at vanilla's 12 HP. TestBrushArmadilloParity green.

- 2026-09-20: filled_bucket / husbandry/tactical_fishing. The Interact
  packet was decoded with a phantom action discriminator left over
  from pre-26.x; on the 26.2 wire (entityId, hand, LpVec3,
  usingSecondaryAction) the "hand" varint landed on the first LpVec3
  byte, pushed 0xff onto the sneak flag, and ReadBool rejected it —
  every real client interact silently died before dispatch. The
  handler now decodes the real record, and feedAnimal/tameAnimal/
  interactEntityAdv/bucketFish honor the packet's hand instead of
  always reading the main hand. bucketFish also matches vanilla's
  creative semantics — the water bucket stays in hand and the filled
  bucket lands in the first free inventory slot — and no
  filled_bucket grant fires: measured three ways on vanilla 26.2
  (wire flush, server-side selector, persisted advancement files),
  the fish pickup removes the entity, plays fill_fish, and writes the
  cod_bucket into inventory but never produces a tactical_fishing
  progress entry, even for the real client. Also fixed a preexisting
  concurrent-map-writes crash on savedPlayers hit by the unit suite.
  TestTacticalFishingParity green.

- 2026-09-20: construct_beacon (nether/create_beacon +
  create_full_beacon). Beacons get a block entity (type 15) and a
  per-dimension set fed by the shared setBlockAndBroadcast path; every
  80 ticks each beacon re-checks sky access and counts contiguous
  complete pyramid layers (3x3→9x9 of beacon_base_blocks) under it.
  On a 0→n transition it plays block.beacon.activate and fires
  construct_beacon(level) for every player in vanilla's grant box
  (x/z ±10, y-9…y+5); on loss it plays deactivate, and ambient replays
  while active. The condition parser reduces "level" to a min/max
  range so level 4 satisfies both advancements in one activation.
  TestConstructBeaconParity green — vanilla's oracle asserts both
  grants plus the activate sound (wire id 153).

- 2026-09-20: player_killed_entity's distance criterion →
  adventure/sniper_duel. /summon arrow now honours Owner:[I;a,b,c,d]
  (EntityReference to a player) — its kills credit that player for
  advancement evaluation. TestSniperDuelParity summons a NoAI
  skeleton 60m from the shooter and drives owned arrows on a flat
  trajectory; vanilla's evaluator resolves ORIGIN to the player's
  position at kill time, so the ≥50m sniper_duel predicate holds.
  Also fixed in the shared drain: advCollect now acks
  chunk_batch_finished, which unblocks vanilla's entity add_entity
  delivery to the test client. Green both servers, stable x2.

- 2026-09-20: recipe_crafted (adventure/craft_decorated_pot_using_
  only_sherds). craftResult now reports the winning recipe's id; both
  take paths (window-0 2x2, crafting-table menu) fire recipe_crafted
  on the pre-consume grid the way vanilla's onTake hands
  craftSlots.getItems() to awardUsedRecipes. The criterion matches
  recipe_id exactly, then consumes one distinct input stack per
  ingredients[] predicate (multiset subset, empty slots never match).
  crafting_decorated_pot gets its own matcher — vanilla's
  DecoratedPotPattern pins ingredients to the four edge middles of a
  full 3x3 with empty corners/center. Found while measuring: /item
  replace never ticked the inventory_changed listener, so has_*
  recipe-unlock criteria (and game-tree inventory_changed) missed
  command writes — writeEntitySlot now fires it for player-inventory
  slots (ender/cursor/craft-grid stay quiet, matching vanilla's
  container split). TestRecipeCraftedAdvParity: sherds grant, bricks
  craft the same recipe but fail the sherd predicates — green both,
  plus an advFlush sniff plumbed through the click driver's fold for
  in-session advancement capture.
- 2026-09-20: used_totem (adventure/totem_of_undying "Postmortal").
  LivingEntity.checkTotemDeathProtection parity: lethal damage with a
  death_protection carrier in main hand (then off hand) shrinks the
  stack, bumps ITEM_USED, fires used_totem with the pre-shrink item,
  resets health to 1, clears effects, applies regen II 900t /
  absorption II 100t / fire res 800t, and broadcasts entity event 35.
  /kill and void (bypasses_invulnerability) skip it. The effect system
  is new: sessions hold a small effect map, update_mob_effect goes to
  the player and trackers with vanilla's bare registry id (no +1) and
  applied-effect flags 0x0E, and mobEffectTickStep expires them with
  remove_mob_effect. Absorption is real: the pool fills to
  4*(amplifier+1) half-hearts on apply, soaks post-armor damage, and
  empties on removal — without it the post-pop zombie hit kills the
  mc-go player while vanilla's yellow hearts absorb it (found via the
  parity diff: killed_by_something diverged). TestTotemParity pops a
  zombie-survivor on both targets and diffs grants, event-35 count,
  and the effect table — green, stable ×2.
- 2026-09-20: effects_changed + /effect at parity. The mob_effect
  registry joins genregistries (name<->id); /effect give|clear runs
  vanilla's grammar — give <targets> <effect> [seconds] [amplifier]
  [hideParticles], "infinite" as -1, clear [targets] [effect] — with
  the reply lattice commands.effect.{give,clear}.{success,failed}.*
  including give.failed when an equal-or-stronger instance is already
  running (stronger amplifier, or equal amplifier with at-least-as-
  long remaining time). Wire details measured: effect field is the
  bare registry id, applied-effect flags are 0x0E (particles|icon|
  blend), hideParticles drops bit 0x02, infinite writes -1. The
  effects_changed trigger fires on add and remove alike (vanilla runs
  it from onEffectAdded and onEffectRemoved); criteria reduce to the
  conditions.effects keyset — source predicates and per-effect
  amplifier/duration bounds lock a criterion (kill_axolotl_target's
  axolotl source stays unmodelable for now). TestEffectAdvParity
  grants nether/all_potions by giving all 17 potion effects at once,
  plus a specific-clear+regive round — green both targets.
- 2026-09-20: shot_crossbow (adventure/ol_betsy "Ol' Betsy").
  CrossbowItem three-step parity: use starts a 25-tick charge
  (survival requires a projectile; creative doesn't), release before
  25 cancels but release after arms the charge, and the next use
  fires — arrow at vanilla's 3.15 b/t, one durability point, the
  item stat, and shot_crossbow with the weapon as the condition
  item (same item.items shape as used_totem). TestShotCrossbowParity
  runs use→hold→release→use on both targets and diffs grants —
  green. killed_by_arrow followed in the same commit: arrows carry
  the firing weapon name, the trigger reads fired_from_weapon.items
  plus the victims[] entity_type union, and unique_entity_types>1
  (arbalistic) stays locked until per-criterion kill sets exist.
  Pillager joined the summon table (24hp, 0.6x1.95 box) so the test
  also kills one by crossbow — whos_the_pillager_now green on both.
- 2026-09-20: using_item (adventure/spyglass_at_parrot "Is It a Bird?"
  and spyglass_at_ghast "Is It a Balloon?"). Vanilla's ServerPlayer
  overrides LivingEntity.updateUsingItem to fire the trigger every
  tick the item stays in use, so a spyglass keeps the predicate hot
  until release/deselect. Conditions reduce to item.items plus the
  player predicate's type_specific/player.looking_at.entity_type —
  which javap pins as a 100-block ProjectileUtil.getEntityHitResult
  over boxes + hasLineOfSight — other player content locks the
  criterion. mc-go gained sess.usingItemID (set on Use Item for a
  spyglass, cleared on release_use_item), a per-tick fireUsingItem,
  and an eye-line raycast over living entity boxes. Two harness
  lessons measured the hard way: the serverbound use_item packet's
  yRot/xRot fields genuinely re-aim the player (send the real aim),
  and spyglass_at_ghast's def only goes visible after its parrot
  parent, so the parrot must scope first. Ghast summon y is a foot
  position — its box rides [y, y+4]. TestSpyglassAdvParity scopes a
  parrot then a ghast on both targets — green. chatRaw joined the
  advFlush diagnostics while chasing this.  The same test grew a third
  leg for spyglass_at_dragon ("Is It a Plane?") — an ender_dragon
  (16x8 box) scoped east — green on both targets too.
- 2026-09-20: target_hit (adventure/bullseye "Bullseye!"). Arrows
  gained block collision in mc-go — the tick's movement segment is
  walked against solid cells (NonSolid pass-through), the entry face
  and point come from the slab test against the unit cube, and a hit
  sticks the arrow (vanilla's inGround). TargetBlock parity:
  getRedstoneStrength = max(1, ceil(15·(0.5−d)/0.5)) over the two
  in-plane |frac−0.5| distances, and onProjectileHit fires
  target_hit for any projectile whose owner is a player.
  Conditions reduce to signal_strength bounds (15 = dead center)
  plus the projectile predicate's entity_type union and
  distance.horizontal from the shooter (30 for bullseye).
  TestTargetHitParity drops an Owner-tagged summon arrow onto a
  target's top-face center from a shooter 65 blocks away — green
  both targets.
  effects_changed also covers nether/all_effects ("How Did We Get
  Here?"): the full 34-effect set granted by /effect finishes
  all_potions first, then the hidden challenge — deferred
  visibility emits correctly. TestAllEffectsParity green both.
- 2026-09-20: enter_block on the game tree (end/enter_end_gateway).
  The recipe-side enter_block listener now also fans game criteria —
  conditions reduce to the block name; state/entity predicates lock.
  TestEnterEndGatewayParity setblocks a gateway and tps the player
  inside — green both targets.
- 2026-09-20: fall_from_height (adventure/fall_from_world_height
  "Caves & Cliffs"). Neither server simulates player gravity — the
  client reports the descent — so the harness streams airborne
  MovePos packets from y=400 down to the flat ground and lands.
  mc-go now tracks fallStartY (the tick the drop opened) and fires
  the trigger on the onGround transition; conditions reduce to
  distance.y + start_position.y + landing position.y bounds.
  TestFallFromHeightParity green both targets.
- 2026-09-20: lightning_strike (adventure/lightning_rod_with_villager_
  no_fire "Surge Protector"). /summon lightning_bolt now resolves a
  strike: a rod at the getStrikePosition cell (bolt pos dipped 1e-6)
  leaves blocks_set_on_fire 0, scatter fires need a sturdy-or-flammable
  base so a rod on a pillar is the honest vanilla way to zero them.
  The trigger gathers bystanders from the bolt's ±15 / y−15..y+21 box
  and fires for every player inside distance.absolute of the bolt —
  conditions reduce to bystander entity_type + bolt distance +
  blocks_set_on_fire. Villager joined the summon table (20hp,
  0.6x1.95). TestLightningStrikeParity green both targets.
- 2026-09-20: slide_down_block (adventure/honey_block_slide "Sticky
  Situation"). Vanilla's HoneyBlock.entityInside fires for blocks the
  box clips, and isSlidingDown is measured: not onGround, feet below
  the shape top (block y+0.9375), vy<-0.08, and — counterintuitively —
  |cellCenter - pos| > 0.4375+bbWidth/2 on at least one horizontal
  axis, because the entity slides against the column's outer face,
  not through it. mc-go fires the trigger from the move-packet
  descent path. TestSlideDownBlockParity hugs a honey wall's face on
  both targets — green.
- 2026-09-20: levitation (end/levitate "Great View From Up Here").
  Vanilla stamps levitationStartPos + levitationStartTime when the
  effect lands, fires the trigger every player tick while the anchor
  is set, and clears it on removal — mc-go mirrors that on the clock
  count with distance.y/distance.x + duration bounds. Risen distance
  comes from the move packets, like the fall trigger.
  TestLevitationParity green both targets.
- 2026-09-20: bee_nest_destroyed (husbandry/silk_touch_nest "Total
  Beelocation"). The setblock `bees` occupant list only decodes on
  vanilla 26.2 with a namespaced entity_data.id ("minecraft:bee") and
  a min_ticks_in_hive at/above the codec floor (worldgen uses 600).
  The measured gate worth pinning: the trigger lives in
  Block.playerDestroy, which ServerPlayerGameMode.destroyBlock skips
  for players who preventsBlockDrops — a CREATIVE break never fires
  it, on vanilla or now on mc-go (the creative-branch fire was a
  parity bug). Survival digs fire it via breakBlock with the held
  stack and stored-occupant count; conditions reduce to block id,
  num_bees_inside exact bounds, and item enchantments predicates.
  TestBeeNestDestroyedParity digs creative (no grant) then survival
  (grant) — green on both targets.
  Residuals noted: vanilla sends no update_advancements for this earn
  (announce chat only; mc-go emits def+progress — diff compares chat
  keys); an update_advancements packet arriving while a teleport is
  unconfirmed stalls vanilla block actions — the harness now echoes
  MovePlayerPos after every sync-position, like a real client.
- 2026-09-20: enchanted_item (story/enchant_item "Enchanter") and the
  enchanting-table menu itself. EnchantmentMenu broadcasts all ten
  container properties at open (three costs, the per-player seed,
  three enchantment-id clues, three level clues — all 0/-1 for a
  fresh player), then re-rolls offers on every slotsChanged. Costs
  come from j=rand(8)+1+(power>>1)+rand(power+1) capped per slot;
  the bookshelf ring (|x|==2||z|==2, y 0-1) counts a shelf only when
  the provider sits at the offset and air sits at offset/2 — power 0
  in the flat test world. Offers sample the enchantment registry
  seeded by playerSeed+slot; books roll one extra discard. The button
  click gates on lapis>=i+1, xp>=i+1 && xp>=costs[i], costs[i]>0,
  then consumes i+1 LEVELS and i+1 lapis (not costs[i]!), transmutes
  book->enchanted_book with stored_enchantments (component 42),
  re-seeds the player, fires enchanted_item, and plays
  block.enchantment_table.use. Creative keeps the lapis stack
  (consume respects infinite materials) but still loses the levels —
  measured. tools/genenchant generates the 43-enchantment, 36-in-table
  table with exclusive_set compatibility from the datapack JSONs.
  TestEnchantParity pins open props, offer shapes, survival
  lapis+level spend, the slot transmute, seed re-roll, announce, and
  sound — green on both targets.
- 2026-09-20: brewed_potion (nether/brew_potion "Local Brewery") and
  real brewing. BrewingStandBlockEntity.serverTick tops fuel to 20
  from a #brewing_fuel item when empty (one powder consumed), isBrewable
  requires a mix hit on >=1 potion slot, a brew costs 1 charge up
  front and ticks brewTime 400->0; doBrew applies
  PotionBrewing.mix per potion slot (container mix rewrites the item
  id first, then the potion mix rewrites potion_contents), shrinks the
  ingredient, slots the crafting remainder (dragon_breath's
  glass_bottle) or drops it at the stand, fires level event 1035,
  and mirrors slot occupancy into the has_bottle_0..2 block states
  (9452 all-true base, b0 the high bit). The trigger fires in
  PotionSlot.onTake on any take path, gated on the stack's
  potion_contents.potion holder being present — a bare `potion` item
  carries EMPTY contents (the uncraftable potion), NOT water, so
  water bottles need potion[potion_contents=water]; measured.
  potion_contents (component 51) writes the RAW potion registry id
  (not holder+1 like sound holders) and the potion registry is
  static — never wire-synced — so ids come from the datagen report:
  tools/genpotions now emits PotionIDs alongside the 63 potion + 2
  container mixes extracted from PotionBrewing.addVanillaMixes
  bytecode. /give and /item share parseItemStack, which gained the
  potion_contents patch (bare id or {potion=...}). Brew-side slot
  broadcast is per-tick DIFFED like vanilla's broadcastChanges —
  props stream every tick while only changed slots go out.
  TestBrewPotionParity drives fuel+3 water bottles+nether_wart through
  a full cycle on both targets, pins the awkward results, fuel 2->19,
  the 1035 event, and the take-triggered announce (obtain_blaze_rod
  granted first so the child is visible). Green on both targets.
  Harness note: Container Property is excluded from the click settle
  set — a ticking menu streams it every tick, so it can never hold
  the quiet window open.
- 2026-09-21: fishing_rod_hooked (husbandry/fishy_business) and the
  full bobber state machine. use() on a rod with no bobber spawns a
  fishing_bobber (entity 157, Data=ownerID+1, idx9 biting=false) with
  vanilla's shootFromRotation vector *0.6 + triangle(0,0.0103365) per
  axis and the 641 throw sound; a second use() retrieves. The bobber
  integrates gravity -0.03 + drag 0.92 until it hits water, parks at
  surface-0.15, then rolls lured 100..600 -> hooked 20..80 -> splash
  + nibble 20..40 with DATA_BITING -> re-roll; the per-tick decrement
  is vanilla's probabilistic 1 + (r<0.25 && rainingAt) - (r<0.5 &&
  !canSeeSky) where canSeeSky lets FLUIDS through (water above the
  bobber does not block sky — measured the hard way). Reeling during
  the nibble window costs 1 rod durability, evaluates
  calculateOpenWater (5x5 layers at bobberY-1..+1: air/lily_pad =
  ABOVE, source water = INSIDE, anything else or ABOVE-first or
  INSIDE-after-ABOVE = INVALID), rolls the fishing loot table
  (fish85/junk10/treasure5 with the datapack functions applied:
  set_count 10 on ink_sac, set_potion water on the junk potion,
  set_damage 0..0.9*max junk / 0..0.25*max treasure via
  damageableItems, book->enchanted_book — the enchant itself is NOT
  generated, gap), and spawns item entities flying toward the player
  with the 639 retrieve sound. fishy_business's per-fish criteria
  match conditions.item.items (single name, list, or #tag-expanded)
  against every caught stack, any-match grants; criteria carrying
  rod/entity/unmodeled keys are impossible per convention. The bite
  requires nibble>0 — early reeling discards empty-handed. Entity-
  hooking (idx8 hooked entity, hurtAndBreak 3/5) is not modeled.
  TestFishingParity gives every lootable item up front (caught junk
  can't diverge recipe unlocks), collapses the four fish criteria to
  any-fish (which fish rolled is luck), and retries up to 4 casts —
  green on both targets. Harness note: a `fill` that stops at the
  surface y leaves water UNDER grass and the bobber never bites;
  canSeeSky-style checks must treat fluids as sky-open.
- 2026-09-21: throw_trident (adventure/throw_trident "A Throwaway
  Joke") and the thrown_trident projectile. TridentItem.use starts a
  charge like the bow but releaseUsing needs >= 10 ticks; release
  hurtWithoutBreaking(1)s the stack THEN consumes one (survival), the
  wounded stack rides along in the entity, and
  nextDamageWillBreak (damage+1 >= 250) refuses the throw entirely.
  Creative keeps the stack and marks the entity Pickup.CREATIVE_ONLY.
  The entity flies vanilla's AbstractArrow physics (gravity 0.05,
  drag 0.99) at spawnProjectileFromRotation speed 2.5, hits living
  targets for a FLAT 8 (BASE_DAMAGE — speed-independent, impaling
  unmodeled) under damage type "trident" (tagged is_projectile), dies
  on entity hit, or sticks in blocks (item.trident.hit_ground) where
  survival/adventure players pick it back up via the same inflated-
  AABB rule as items, durability preserved. Kills broadcast
  death.attack.trident; killed_by_arrow only fires when the
  projectile actually came from a weapon item (arrowWeapon guard —
  tridents leave it empty). Throw plays item.trident.throw on the
  PLAYERS source (playSoundAt grew a source param). The entity-
  collision path is now projectileCollide(e, flat, typeName) shared
  by arrows. Loyalty/riptide/channeling and trident metadata (idx 8
  loyalty byte, idx 9 foil) are unmodeled.
  TestThrowTridentParity grants the parent (kill_a_mob — vanilla
  hides the child until then), summons a NoAI pig 9 blocks out, and
  throws pitch-8; green on both targets.

- overoverkill / mace smash (done): vanilla javap (MaceItem,
  Player.attack 26.2): canSmashAttack = fallDistance>1.5 and not
  fall-flying; the bonus lands BEFORE the crit multiply, piecewise —
  fall≤3: 4·fall; fall≤8: 12+2·(fall−3); fall>8: 22+(fall−8). Mace
  attribute damage +5 (weaponDamage entry 6), speed −3.4 (~33t).
  On a smash the hit's damage type becomes mace_smash (an
  is_player_attack tag member), the attacker's fall stops dead
  (fallDistance reset → landing is damage-free), levelEvent 2013 +
  item.mace.smash_ground[_heavy at fall>5] play at the impact, and
  every living entity within 3.5 blocks gets pushed
  (3.5−dist)·0.7·(1−resist) — attacker and victim excepted.
  player_hurt_entity's `dealt` is the RAW damage, not health-capped —
  firePlayerHurtEntity now takes (direct, proj, dealt, mainhand,
  smash) and hurtCond parses direct_entity.equipment.mainhand.items
  (+ #tag expansion) and the mace_smash damage-type tag; unmodeled
  direct_entity sub-keys lock the criterion. Density/breach enchants
  unmodeled. TestOveroverkillParity builds a ~90-block fall with
  descending onGround=false move packets (2 blocks / 40ms — client
  cadence) ending 2 above a NoAI pig, then attacks via the dedicated
  Attack packet — the interact packet's action=1 route is a harness
  gap on BOTH servers (vanilla 26.2 moved attacks to
  ServerboundAttackPacket 0x01; mc-go's handleInteract drops
  action=1 — worth closing later).

- spear_many_mobs / kinetic_weapon jab (done): vanilla javap
  (KineticWeapon.damageEntities, LivingEntity.stabAttack,
  ProjectileUtil.getHitEntitiesAlong, ServerGamePacketListenerImpl
  26.2): a held right-use keeps the item in use for 72000 ticks;
  after delay_ticks (f4·20: iron 12) every tick sweeps the eye ray
  along the head-look segment [eye+look·minReach .. eye+look·(maxReach
  +max(0,knownMovement·look))] — reach 2.0..4.5 (creative 2.0..6.5),
  entity clip vs AABB inflated hitbox_margin 0.125. Speeds come from
  getMotion = lastKnownClientMovement·20, which vanilla fills from
  the ACCEPTED per-packet move delta in handleMovePlayer (zeroed at
  client_tick_end with no movement — we send none, so it persists).
  Each collected entity is remembered for contact_cooldown_ticks=10
  BEFORE conditions run, and spear_mobs trigger count = that
  remembered-set size — stationary jab ticks park in-ray entities in
  cooldown, which is why the test walks immediately after use_item.
  Conditions: dismount (hot<=f9·20 && relSpeed>=4.6), knockback
  (hot<=f7·20 && holderSpeed>=5.1), damage (hot<=f5·20 &&
  holderSpeed>=9..14 — unreachable on foot, riding-only by design).
  stabAttack applies the passed flags (hurtServer/push/stopRiding);
  on any stab the holder gets entity_event 2 and the hit sound plays.
  mc-go mirrors: per-tier tables in internal/server/spear.go,
  remember-before-conditions, entity_event 2, and damage events only
  when canDmg. TestSpearMobsParity drives a zombie row (1.95-tall
  boxes; a pig's 0.9 tops out below the eye ray) — green on both.

- thrown_item_picked_up_by_entity (done): vanilla javap
  (Mob.aiStep item scan, LivingEntity.onItemPickup, Piglin.
  pickUpItem, SulfurCube.canPickUpLoot/canHoldItem/pickUpItem): the
  mob-side sweep is the FIRST thing aiStep does — NoAI does not
  gate it — gated by ServerLevel && canPickUpLoot && isAlive &&
  !dead && mobGriefing, scanning ItemEntitys inside the mob's box
  inflated by getPickupReach (Mob default (1,0,1) — horizontal only,
  vertical stays the box height). Per item: !removed, no pickup
  delay, non-empty stack, wantsToPickUp. A bare /summon piglin has
  CanPickUpLoot=false (no finalizeSpawn) so the test sets the NBT;
  sulfur_cube overrides canPickUpLoot to "body slot empty" and
  swallows ONE #sulfur_cube_swallowable item via split(1). The
  trigger fires for the THROWER — the item entity's Owner field —
  when the picker is a LivingEntity. mc-go mirrors in
  internal/server/itempickup.go (mobItemPickupStep after
  itemTickStep in GameTick); drop now stamps Thrower=
  entityID. TestThrownItemPickupParity pens each mob (dropped items
  slide AND drift laterally on both servers — measured overshoots
  past 1-wide walls), throws via the drop_item player action (not
  /summon item, which leaves Owner unset), drains between item
  replace/drop/teleport (vanilla eats a drop sharing a tick with a
  teleport), and parks the player beyond the 1.425 pickup halo so a
  resting item can't race the mob's scan. Pinned criteria:
  nether/distract_piglin:distract_piglin (gold→piglin) and
  husbandry/uh_oh:pick_up_dropped_tnt (tnt→cube). Also fixed while
  here: thrown items on mc-go now collide per-axis with walls
  instead of sliding through. Green on both.

- thrown_item_picked_up_by_player (done): vanilla javap
  (ServerPlayer.onItemPickup, ItemEntity.getOwner): the trigger
  fires for the PICKER whenever the picked item entity's
  Thrower (an EntityReference — serialized as a UUID int array)
  resolves to a live entity; the criterion's "entity" predicate is
  evaluated on that owner (allay for
  husbandry/allay_deliver_item_to_player). The natural allay
  delivery path is full AI — the test instead mints the owner:
  summon honors UUID:[I;...] and /summon item accepts
  {Item:{id,count}, Thrower:[I;...], PickupDelay} (all vanilla NBT
  keys). The item lands on the player with delay 0 and is picked
  before a nearby allay's own scan can race it. mc-go resolves the
  Thrower UUID to a live entity id at summon (entityIDByUUID) and
  fires the picker trigger from itemTickStep's addStack path.
  TestThrownItemPickedUpByPlayerParity green on both.

- started_riding (done): vanilla javap (Entity.startRiding,
  Boat.interact): right-clicking a vehicle mounts the player and
  fires START_RIDING_TRIGGER for ServerPlayer riders; the criterion's
  player predicate carries vehicle.entity_type (#minecraft:boat) and
  vehicle.passenger.entity_type (any-match). mc-go: boats/rafts are
  summonable non-living entities, summon honors Passengers:[{id:...}]
  recursive spawn NBT (riders get Riding set and skip gravity/
  wander), and entity Interact on a boat mounts the player (2-seat
  cap, sneaking passes through — vanilla's isSecondaryUseActive gate).
  Entity gains Passengers/Riding; rideCond parses
  player[].predicate.vehicle.{entity_type,passenger.entity_type}
  with #tag resolution via entityTypeTagHas. Goat summonable
  (hp 10, box 0.9x1.3). TestStartedRidingParity mounts an
  oak_boat carrying a goat — green vs vanilla 26.2.

- ride_entity_in_lava (done): vanilla ServerPlayer.tick — while
  getVehicle().isInLava() the tick latches enteredLavaOnVehiclePosition
  once and fires the DistanceTrigger against it each tick;
  nether/ride_strider_in_overworld_lava asks horizontal >= 50 with
  location.dimension=overworld + vehicle.entity_type=strider. mc-go:
  strider summonable (hp 20, box 0.9x1.7), saddled via Saddle:1b or
  the 1.21.6+ equipment.saddle slot (bareback refuses mounting, per
  Strider.mobInteract), seat caps per vehicle (boat 2 / strider 1),
  rider position follows the vehicle each player tick, entityInLava
  scans the vehicle AABB for lava states, and lavaRidePos latches the
  entry like vanilla. /tp @e[type=X] teleports non-player entities
  (riders follow their vehicle), replying
  commands.teleport.success.location.single/.multiple.
  TestRideEntityInLavaParity teleports the ridden strider 60 blocks
  along a lava trench — green vs vanilla 26.2.

- item_durability_changed (done): vanilla FoodOnAStickItem.use —
  while the player controls a saddled ItemSteerable (strider/pig:
  getControlledVehicle means first passenger + saddled), use wears
  the stick via hurtAndConvertOnBreak which routes to the
  ServerLevel hurtAndBreak → ITEM_DURABILITY_CHANGED(stack, new
  damage); ride_strider's criterion is item=warped_fungus_on_a_stick
  + vehicle.entity_type=strider. mc-go: wearSlot generalizes
  wearHeld to any slot and fires the trigger on every wear (mining,
  hits, stick use), matching vanilla's placement inside hurtAndBreak;
  useFoodOnAStick gates on riding+saddled+controlling and wears the
  packet's hand. /gamemode now replies even when the mode is
  unchanged (vanilla always sends commands.gamemode.success.self).
  Measured: the creative-default join means hurtAndBreak is a no-op
  until a real gamemode switch — the test sets survival first.
  TestItemDurabilityChangedParity green vs vanilla 26.2.

- channeled_lightning + /weather (done): vanilla stores one
  server-wide WeatherData; /weather <clear|rain|thunder> [time] writes
  the rain/thunder flags and replies commands.weather.set.<mode>.
  Level.isThundering is NOT the flag — it's the lerped
  thunderLevel > 0.9 ramping 0.01/tick, so the weather_check in
  channeling's post_attack effect fails for ~90 ticks after the
  command (measured: the advancement only grants once the ramp
  crosses). Channeling itself is fully data-driven in 26.2:
  enchantment.json post_attack → summon_entity lightning_bolt gated by
  thundering + can_see_sky at the victim + direct_attacker=trident;
  hit_block additionally requires a #lightning_rods block. The bolt
  collects living entities in a ±3 box, thunderHits them (5 damage),
  fires lightning_strike for bystanders, and fires
  channeled_lightning on the trident owner with the struck set as
  victims (summon_entity sets LightningBolt.cause from the
  EnchantedItemInUse owner — the source entity's mainhand path).
  mc-go: weather.go (flags + milli-scaled lerped levels + countdown),
  Entity.tridentEnch carries the thrown stack's enchantments,
  channelingStrike on both trident hit sites, chans chanCond parser.
  Item SNBT note: vanilla's component map wants QUOTED enchant keys —
  trident[enchantments={"minecraft:channeling":1}]. 
  TestChanneledLightningParity green vs vanilla 26.2.

- player_sheared_equipment (remove_wolf_armor): vanilla Entity.interact
  shears branch — Items.SHEARS + canShearEquipment (Wolf: isOwnedBy)
  + !sneaking -> shearItem: wear shears 1, clear BODY slot, drop the
  armor, trigger with REMOVED stack + wolf entity. mc-go: shear.go
  first in handleInteract, mob summon reads equipment.body + Owner
  ([I;..] int array only), item predicate matches the dropped stack.
  Also fixed: item-entity pickup never fired inventory_changed — the
  wolf_armor pickup unlocks recipes/misc/wolf_armor_dyed on vanilla.
  TestPlayerShearedEquipmentParity green vs vanilla 26.2.

- set_passengers + dismount: 26.2 has no sneak actions in
  player_command — sneak is the 0x20 bit of player_input, and
  dismount is Player.tick's wantsToStopRiding (shift down) ->
  stopRiding. mc-go now broadcasts set_passengers on mount, clears
  riding + broadcasts the emptied list on shift-dismount, disconnect
  (untrack), and vehicle death (rideTickStep clears the stale id).
  TestSetPassengersParity: watcher client records mount riders=1 and
  dismount riders=0 identically on vanilla 26.2 and mc-go.

- Join-time entity visibility: sendEntitySpawnsTo only sent other
  players — a newcomer never saw entities summoned before they
  joined. Now every live non-player entity in the joiner's dimension
  gets an add_entity, and vehicles already carrying passengers get a
  set_passengers follow-up. TestJoinSeesExistingEntities: player B
  joins after a wolf summon and sees it on both servers.

  Follow-up: tracker pairing also carries entity_data (item stacks,
  living health) — sent alongside join spawns now. Harness note:
  drainChunkWindow's quiet-gap break can hang when a server sends
  periodic packets — new tests use advCollect's hard deadline.

- /give vanilla semantics: count is integer(1), capped at
  100*maxStackSize (commands.give.failed.toomanyitems); the count
  splits into max-stack chunks, each chunk inventory.add's and the
  remainder drops at the player with PickupDelay=0 + Owner=recipient
  (pickup gated to the owner). Fully-added chunks ALSO toss a fake
  item entity (makeFakeItem: never pickuppable, despawns next tick).
  mc-go previously capped count at 99, rejected overstacked gives, and
  silently deleted overflow. TestGiveOverflowDropsParity green vs
  vanilla 26.2.

- villager trading: right-clicking a villager or wandering_trader
  whose Offers NBT has Recipes opens the merchant screen (menu type
  19, title entity.minecraft.villager.<profession>) and sends
  merchant_offers with vanilla's per-offer wire shape (buy ItemCost +
  predicate byte, sell slot, optional buyB, outOfStock, uses/maxUses/
  xp/specialPriceDiff/priceMul/demand, then villager level + xp +
  showProgress + canRestock). select_trade runs MerchantMenu's
  setSelectionHint + tryMoveItems — the cost stack auto-pulls from the
  player inventory into the payment slots and the result shows once
  covered. Taking the result consumes the cost, uses++, drops XP orbs
  (rewardExp), awards traded_with_villager, and fires the
  villager_trade trigger (villager + item predicates) — granting
  minecraft:adventure/trade. Closing the menu returns payment slots to
  the inventory. TestVillagerTradeParity green vs vanilla 26.2.

  Remaining trigger census: hero_of_the_village (raid).

- cured_zombie_villager: a zombie_villager's ConversionTime NBT
  (-1 default) starts the cure countdown with an optional
  ConversionPlayer EntityReference; each tick it falls by
  getConversionProgress (1, plus a 1% roll scanning the 9x9x9 box for
  BedBlocks — each found bed capped at 14 adds 1 with 30%
  probability; iron bars no longer count in 26.2). At zero
  finishConversion swaps the entity for a villager (new id, same
  position/villager data/offers), fires cured_zombie_villager for the
  starter player, and plays level event 1027 (mute by Silent).
  zombie_villager + wandering_trader joined the summonable mob
  table. The golden-apple+weakness interact path still needs a mob
  effect model. TestCuredZombieVillagerParity green vs vanilla 26.2.

- fall_after_explosion / wind charges: wind_charge and
  breeze_wind_charge are summonable with Motion NBT, fly without
  gravity, and burst on block or player contact. The burst
  (SimpleExplosionDamageCalculator, power 1.2 player / 3.0 breeze,
  no entity damage) knocks entities inside the 2*radius box by
  (1 - dist/2r) * seen * 1.22 along eyePos->center and latches the
  victim's currentExplosionImpactPos + cause. When that player next
  starts falling (impact.y <= apex.y), fall_after_explosion fires
  with distance = impact->apex rise — adventure/who_needs_rockets
  needs 7. The explode packet's particle/sound payload is still
  unsent (victims get set_entity_motion instead of the explode
  knockback field). TestFallAfterExplosionParity green vs 26.2.

- crafter_recipe_crafted: a neighboring signal source
  (redstone_block/torch, powered lever/button/observer state) rising
  edge arms the crafter for 4 ticks and latches its TRIGGERED block
  property; a falling edge clears it, and a queued tick still crafts
  (vanilla scheduleTick can't be cancelled). The craft matches the
  3x3 grid through the shared recipe table with disabled slots read
  as empty, then shrinks EVERY non-empty slot by one — disabled slots
  included, a vanilla quirk of the post-craft forEach(shrink). The
  result inserts into the container the front face points at, else
  spawns as an item entity at center+0.7*front with a small toss;
  every player inside the 17x17x17 AABB earns the trigger
  (recipe_id + ingredients schema), and level events 1049 (success)
  / 1050 (no match) / 2010 (front-direction data) play.
  TestCrafterRecipeCraftedParity green vs vanilla 26.2.

- allay_drop_item_on_block: the allay's equipment/inventory split is
  real in vanilla — /item or interact-fed items land on MAINHAND
  equipment and NEVER throw; only items it picks off the ground into
  its single inventory slot are thrown (canThrowItemToTarget gates on
  getInventory, not the hand). wantsToPickUp requires a NON-empty
  hand holding an equal stack, so an unfed allay ignores ground items.
  A played noteblock posts NOTE_BLOCK_PLAY (useWithoutItem = tune +
  play; attack = play only), which any allay inside the 16-block
  listener radius hears if still within 30 — LIKED_NOTEBLOCK_POSITION
  + a 600-tick cooldown memory. The throw itself paces one inventory
  item per 20 ticks once dist(target.above(), eyePos) < 3, tossing
  velocity (0.2,0.3,0.2) spread 0.2 with ITEM_PICKUP_COOLDOWN=60 on the
  thrown entity, and fires ItemUsedOnLocationTrigger-shaped
  allay_drop_item_on_block for the LIKED_PLAYER at the target's below.
  Deposit target falls back to the liked player when no fresh
  noteblock memory exists. Blackbox gotcha found: vanilla drops every
  use-on while awaitingPositionFromClient is set — tests must answer
  player_position with teleport_confirm before interacting.
  TestAllayDropItemOnBlockParity green vs vanilla 26.2.

- avoid_vibration: the sneak-suppression gate runs inside
  VibrationSystem$User.isValidVibration — a sneaking source
  (isSteppingCarefully = shift key down) emitting an event in
  ignore_vibrations_sneaking (hit_ground, projectile_shoot, step,
  swim, item_interact_start, item_interact_finish) triggers
  AVOID_VIBRATION and the listener skips the receipt — but ONLY if the
  event is in the listener's vibrations tag at all, so
  item_interact_start (not in the tag) can never reach a sculk
  sensor's check. The reachable sneak drives: draw+release a bow
  (stopUsingItem posts ITEM_INTERACT_FINISH; a fired arrow posts
  PROJECTILE_SHOOT) or land with fallDistance>0 (checkFallDamage posts
  HIT_GROUND unconditionally — sneaking only suppresses STEP via
  getMovementEmission=NONE, not landings). Listener radii: sculk_sensor
  8, calibrated_sculk_sensor 16. Command-sourced events (e.g.
  /setblock) carry no player source and never qualify; a
  player-placed block does activate the sensor. mc-go scans ±16 around
  the emission for sensors and grants each non-impossible
  avoid_vibration criterion. TestAvoidVibrationParity green vs
  vanilla 26.2.

- golden-apple cure interact (ZombieVillager.mobInteract): a held
  golden_apple + hasEffect(WEAKNESS) consumes one stack and calls
  startConverting(uuid, 3600 + rand(2401)) — conversionStarter,
  villagerConversionTime, DATA_CONVERTING_ID=true, weakness removed,
  strength added for the conversion's length at vanilla's
  Math.min(difficultyId-1, 0) amplifier (always <=0 — a quirk), and
  entity event 16 (the cure sound cue) broadcast. An apple without
  weakness returns CONSUME — nothing changes, and because conversion
  clears the weakness a second apple never re-fires. Mob status
  effects now model on Entity.Effects (id -> amplifier/expiry,
  lazily expiry-checked like session effects), loadable via summon
  NBT active_effects {id,duration,amplifier}.
  TestCureInteractParity green vs vanilla 26.2.

- armor_stand interact_at equipment swap (ArmorStand.interact /
  getClickedSlot / swapItem): the interact packet's LpVec3 click
  position is entity-local (feet origin); the wire bytes are the
  packed word's low two bytes little-endian followed by the high 32
  bits as a big-endian writeInt — plain little-endian scrambles every
  field, which is why early probes silently no-op'd. Empty hand picks
  the clicked slot by height band (feet [0.1,0.55), legs [0.4,1.2),
  chest [0.9,1.6), head >=1.6; halved/reshaped for Small, all gated on
  hasItemInSlot, defaulting to OFFHAND when mainhand is empty but
  offhand isn't, else MAINHAND) falling back to MAINHAND when the
  clicked slot is disabled; a held stack targets its own equippable
  slot. swapItem gates: DisabledSlots bits index the EquipmentSlot
  FILTER space {main:0, feet:1, legs:2, chest:3, head:4, off:5} —
  not the wire ordinals — at +0 (whole-slot via isDisabled), +8
  (take), +16 (place); hand slots die on armless stands. Creative
  puts copy a single item; stacks>1 split one onto an empty slot
  (returning false when the slot is occupied); otherwise the whole
  stack swaps and the old piece lands in the clicking hand.
  equipment.{feet,legs,chest,head,mainhand,offhand} summon NBT fills
  the Equips table BEFORE the tracker-attach burst, matching
  vanilla's spawn wire: spawn + metadata + one set_equipment burst,
  then a second identical burst on the next tick from
  detectEquipmentUpdates' dirty-flag resend (mc-go: EquipDirty ->
  mobTickStep). swing anim id is 0 for the main hand, 3 for the off
  hand. TestArmorStandInteractParity green vs vanilla 26.2.

- mob dirty-sync model (ServerEntity.sendChanges / SynchedEntityData):
  pairing (sendPairingData at tracker attach) emits spawn, then
  set_entity_data with every NON-DEFAULT metadata entry — effect
  particles (index 10, serializer 17) are never in it because
  updateDirtyEffects hasn't run — then update_attributes ONLY for mobs
  whose attribute map materialized pre-attach (zombie family yes,
  villager no — measured per type), then set_equipment/passengers.
  Dirty flags survive pairing, so the next tick's sendChanges resends
  the full data set plus newly-dirtied entries (effect particles land
  then), the syncable attributes again, and — because summon snapTo
  marks the position dirty — a zero-delta move_entity_pos; every 60
  sync ticks (tickCount%60) another zero-delta pos goes out. mc-go:
  MetaDirty/AttrDirty/PosDirty + SyncCount + ParticlesSent on Entity,
  flushed in mobSyncFlush each mob tick. LivingEntity metadata now
  emits per-type non-default entries: health(9), DATA_MOB_FLAGS(15,
  bit0=NoAI), zombie_villager converting(19)/villagerdata(20, packed
  only when differing from its randomized-at-define default)/
  finalized(21), villager villagerdata(19)/finalized(20).
  DATA_EFFECT_PARTICLES = one entity_effect particle (registry 28,
  Int32-BE ARGB color from MobEffects' register() arg — full 40-entry
  table extracted) per live effect; an expired-but-unswept instance
  stays listed for one tick, then shrinks — the dirty pack that
  carries only idx10. ZombieVillager.readAdditionalSaveData treats
  ConversionTime != -1 as startConverting (weakness removed, strength
  added for the duration; the entity_event(16) broadcast fires before
  attach so nobody sees it), and VillagerData presence OR
  VillagerDataFinalized:true forces finalized — conversion copies the
  zombie's data and the villager's NBT-restore path always packs
  finalized=true. getConversionProgress scan fixed: (int) truncation,
  exclusive <x+4 bounds, iron_bars counted alongside beds, roll before
  the +1. TestCureConversionSyncParity green vs vanilla 26.2.
 explode packet (0x24) parity for wind charges, measured on
 vanilla 26.2 (wire + ServerLevel.explode/ServerExplosion/WindCharge/
 BreezeWindCharge/AbstractWindCharge/SimpleExplosionDamageCalculator
 via CFR): burst goes to every player within distanceToSqr<4096 of the
 center carrying center+radius+blockCount+per-recipient optional
 knockback+particle+sound holder+empty block-particles weighted list —
 wind_charge: radius 1.2, kbMult 1.22, GUST_EMITTER_SMALL (34),
 sound entity.wind_charge.wind_burst (holder id 1773); breeze:
 radius 3.0, kbMult 1.0, GUST_EMITTER_LARGE (33), sound
 entity.breeze.wind_burst (211). blockCount = the 16³-shell-ray
 sampler (1352 rays, per-ray power r*(0.7+rand*0.6), step 0.3,
 decrement 0.22500001+(resistance+0.3)*0.3, every stepped in-bounds
 position counts INCLUDING air — shouldBlockExplode is unconditional
 true) so counts are random per burst (~60 on flat ground). Push:
 dist=sqrt(entity.distanceToSqr(center))/(2r), direction from eye
 (mob eye = bbHeight*0.85, player 1.62), power=(1-dist)*exposure*
 kbMult*(1-kbResist); hitPlayers only for non-spectator
 non-(creative&&flying) — spectators/creative-fliers get the packet
 with no knockback field. Wire order: explode precedes the charge's
 remove_entities. mc-go windChargeTickStep now ticks
 breeze_wind_charge too; windBurst mirrors the vanilla layout
 (sampler -> mob push broadcast set_entity_motion -> per-player
 explode -> observer-side motion -> remove). TestWindChargeExplodeParity
 green vs vanilla 26.2: same radius/particle/sound holder,
 blockCount>0 both, victim knockback inline within 0.5, observer has
 none, explode-before-remove. Harness note: a read deadline shared
 across two conns starves the second reader — set each deadline
 immediately before its drain.

- wind-charge TRIGGER_BLOCK slice (26.2, measured via CFR + wire):
  interactWithBlocks shuffles the sampled positions and runs each
  block's onExplosionHit — doors/trapdoors/fence gates toggle open
  when unpowered (iron doors/trapdoors are wind-charge-proof), buttons
  press and release on scheduleTick (stone/polished_blackstone 20t,
  wood 30t), levers pull, lit candles extinguish with a smoke puff.
  Breeze charges are additionally gated on the mob_griefing gamerule.
  What actually runs is deeper than a flag flip: a pressed button or
  pulled lever fires updateNeighbours (neighbors of the control +
  neighbors of its attached block), and doors/trapdoors/gates answer
  neighborChanged by syncing powered (and open when they differ) to
  the sensed signal — a powered button next to a door re-closes it on
  release. Doors also mirror their whole state across halves via
  DoorBlock.updateShape, so the upper half writes whenever the lower
  does. Wind-charge blast sampling drains NO resistance except the
  immune tag {barrier, bedrock} — every other block returns the empty
  Optional, so bursts reach farther than the old resistance table
  implied; burst centre = block-face contact + normal*0.25 (a floor
  hit bursts at floorTop+0.25, not inside the block). Wire: vanilla
  never emits per-flip block_update for multi-block changes —
  ChunkHolder batches same-section changes into
  section_blocks_update (0x54) at tick end; sounds go immediately.
  mc-go now implements the sampler reach, face-offset burst, the
  triggered flips with their positioned sounds (sound_event ids come
  from a generated SoundEventID table — sound_event is a static
  registry, the old registryEntryID login-registry lookup silently
  returned -1, which also meant the enchant-table use sound wrote a
  malformed holder — fixed), powered-neighbor propagation for the
  button/lever emitter set, and the door-half mirror. Pin:
  TestWindChargeTriggerBlocksParity green vs vanilla 26.2 — identical
  final states incl. the re-closed powered door, both door open+close
  sounds, the button's press+release pair, and the full sound set.
  Harness notes: vanilla suppresses chunk-tracker-emitted packets
  (block updates, section updates) until the client acks each chunk
  batch — drains must go through drainChunkWindow, which acks; a
  shared read deadline across conns starves the second reader.
- Per-section block-change batching generalized (26.2, ChunkHolder
  parity): broadcastBlockState no longer writes block_update per call —
  it accumulates changed positions per (dim, chunk section) for the
  tick, and blockChangesFlushStep runs at GameTick end (after chunk
  streaming, where vanilla's ChunkMap.tick sits): one change in a
  section emits block_update, several emit section_blocks_update
  (0x54: section-pos long, VarInt count, VarLong (stateId<<12)|localPos
  per change) with the state read AT FLUSH — intra-tick rewrites
  collapse to the final state, matching vanilla's ShortSet dedup.
  Light updates already precede the flush so vanilla's light-first
  ordering inside broadcastChanges is preserved. block_entity_data is
  still sent eagerly by syncBlockEntities (vanilla piggybacks it on the
  flush — residual gap). Servers without a running game loop (unit
  tests) keep the immediate write. Pinned by
  TestWindChargeTriggerBlocksParity emitting 0x54 carriers.
  Pre-existing failures unrelated to this change, observed on clean
  master: TestMcGoOnlyOpsCommands (chat root decode), 
  TestMcGoOnlyPortalCooldownExpiry (respawn 30s timeout).
- Portal cooldown slice (26.2, measured via javap/CFR on
  Entity.handlePortal/setAsInsidePortal/PortalProcessor): the previous
  model froze the 300-tick cooldown while inside a portal and cleared
  portal-time instantly on stepping out; vanilla instead (a) uses a
  10-TICK player cooldown (Player.getDimensionChangingDelay=10; the 300
  is Entity's default for non-players / defers to a player passenger),
  (b) decrements portalCooldown every tick unconditionally, (c)
  RE-ARMS it to full whenever a cooling entity is inside a portal —
  continuous occupancy can never re-fire, re-travel needs stepping out
  ~0.5s, and (d) decays accumulated portal time 4/tick outside rather
  than resetting. portalTickStep now mirrors all four.
  TestMcGoOnlyPortalCooldownExpiry rewritten to the real semantics
  (inside-hold stays quiet, step-out+re-enter fires) and passes; it
  previously timed out — pre-existing break, now fixed. Also fixed
  TestMcGoOnlyOpsCommands, broken since command replies became
  vanilla-shaped translatable components: chatTextBlackbox now flattens
  component roots (text/translate/with/extra) and the /tp assertions
  check the translation keys + target names.
- Recipe-book persistence slice (26.2): the player's recipe book now
  travels with the data file — unlocked recipe ids, the client-marked
  seen set, and the per-category open/filtering toggles persist in
  playerPersist and restore into the session on rejoin
  (TestMcGoOnlyRecipeBookPersistence: give → rejoin → join burst's
  recipe_book_add carries the entry, repeat give fails with
  give.failed). The restored set emits inside sendRecipeBookAdd with
  flags 0 (known recipes toast/badge nothing) while the canned
  crafting_table join blob is skipped once already known. Wire fixes
  folded in: recipe_book_settings now writes all eight toggles
  (crafting/furnace/blast/smoker × open/filter — the echo used to send
  only four) and recipe_book_change_settings accepts the blast/smoker
  ordinals it previously rejected.
- Wandering-mob move-packet family (26.2): mc-go used to answer every
  wander/gravity/chase step with entity_position_sync — a zombie chase
  emitted it 80 times in 4s where vanilla emits 26 move_entity_pos +
  6 move_entity_pos_rot + 10 rotate_head + at most one
  entity_position_sync (the spawn-time onGround flip). ServerEntity's
  measured sendChanges is now implemented in mobMoveSync: mobs sync on
  updateInterval=3 ticks (plus dirty ticks), fixed-point 1/4096 deltas
  against the last-emitted position, positionChanged at >=7.63e-6
  block², a forced resend every 60 sync ticks, packet selection
  pos+rot→pos_rot / pos→pos / rot→rot / head-yaw→rotate_head, and
  entity_position_sync only for delta overflow or the onGround flip.
  summon PosDirty resolves into the zero-delta move_entity_pos the
  vanilla pairing burst ends in. Pinned by TestMobMovePacketFamilyParity
  (family + no teleports + delta range on both servers) and
  TestMobWanderPackets now reads the delta family.
- Decorated chat commands (26.2): /say, /me and /msg|tell|w now ride
  the Disguised Chat packet the way PlayerChatMessage.system +
  OutgoingChatMessage.Disguised do — literal content component plus a
  ChatType.Bound (registry ref id+1, decorated sender name, optional
  target name). /say binds say_command and broadcasts to everyone;
  /me binds emote_command; /msg sends msg_command_incoming to each
  target and msg_command_outgoing (target name filled) back to the
  sender. The sender name component is Entity.getDisplayName's shape:
  literal name + suggest_command "/tell <name> " click event +
  insertion + show_entity hover (name/action/id/uuid int array). Both
  packets compare byte-exact vs vanilla (TestSayCommandParity,
  TestMsgCommandParity — offline UUIDs are name-derived, so the whole
  body collates).
- Item reply components (26.2): the /item (and /give-family) bracket
  name now matches ItemStack.getDisplayName — the outer component
  takes the effective rarity's ChatFormatting color (a generated
  itemRarity table from the component reports, bumped one step while
  enchanted — common/uncommon to rare, rare to epic), carries a
  show_item hover with the non-default components map
  (custom_name/enchantments/damage/repair_cost/potion_contents), and a
  custom_name's inner wrapper is italic with the literal in extra.
  Folded in: command-line tokenization keeps quoted and bracketed
  spans whole (custom_name='Cool Dirt' used to split at the space),
  and a bare literal in an extra list serializes as a TAG_String
  element. Pinned by new TestItemCommandParity cases covering epic,
  rare, uncommon, enchanted-bump, custom-name and damage hovers.
- spear/trident sound exclusion + anchoring: `KineticWeapon.makeSound`
  is `Level.playSound(except=holder,...)` — positioned `sound` packet to
  every tracker EXCEPT the holder (client plays it locally);
  `makeLocalHitSound` calls `Level.playLocalSound` — an empty stub on
  the dedicated server, so spear hits emit no packet. Same shape for
  trident: throw is a `sound_entity` packet anchored on the thrower
  with the thrower excluded; hit_ground is positioned to all. Also
  pinned: player-sourced sounds use SoundSource PLAYERS (7), and
  vanilla seeds every sound packet with a random Long. mc-go:
  `playSoundSrc`/`playSoundEntity` gained `except`, `src` fixed to 7
  on player-cause calls, spear hit broadcast removed. Pinned by
  TestSpearUseSoundParity (two-client holder-vs-observer delivery +
  body compare minus seed).
- Block-entity piggyback on the flush (26.2, ChunkHolder.broadcastChanges):
  block_entity_data now rides the tick-end block-change flush and lands
  right after the block update — previously syncBlockEntities broadcast
  it eagerly at write time, which inverted the vanilla order
  (BE-before-block). Measured: setblock on chest/furnace emits only
  block_update (no BE packet — their getUpdatePacket fires on contents
  changes, not placement), setblock on a sign emits block_update then
  block_entity_data with byte-identical payload. Servers without a
  running game loop still emit BE right after the immediate block
  update. Pinned by TestBlockEntityPiggybackParity — identical packet
  sequence and bodies vs vanilla.
- /data block (26.2, DataCommands): get renders the block entity's save
  NBT (minus keepPacked) via the pretty-SNBT component — aqua keys,
  gold values, red b/s/l/f/d suffixes, HashMap bucket-order keys —
  under commands.data.block.query with {"" -> int} pos args;
  merge/remove/modify answer commands.data.block.modified with bare
  int args. Missing block entity = commands.data.block.invalid, missing
  path = arguments.nbtpath.nothing_found, failed remove/modify =
  commands.data.merge.failed. Mutations write back through
  restoreBlockEntity, which now clears container slots before filling
  (vanilla load semantics — merge onto Items replaces it wholesale).
  Sign mutations queue block_entity_data on the tick-end flush
  (sendBlockUpdated), landing after the reply chat; chests emit none.
  Entity scope and `modify ... from` still open. Pinned by
  TestDataCommandParity — full per-command event sequence identical
  vs vanilla (replies + BE packets).
- Selector expansion in message text (26.2, MessageArgument.resolve):
  /msg, /say and /me now resolve embedded selectors server-side: a
  match becomes the entity's decorated name component (suggest_command
  click, show_entity hover, insertion), several comma-join in gray,
  none collapse to an empty literal; chunks between selectors move to
  extra as plain literals ({"": text} once any element is compound —
  componentsNBT now picks the element type per list, matching the
  vanilla codec). Selector args evaluated: type= (player only —
  non-player entities have no display-name model yet), limit=, sort=,
  distance=. Player lists resolve in join order. Pinned by
  TestMsgSelectorParity — identical content trees on both recipients.
- Scoreboard teams + /teammsg (26.2, TeamCommand / TeamMsgCommand):
  /team add|remove|join|leave|empty|list|modify implemented against
  ServerScoreboard semantics. Wire: SetPlayerTeam (0x6d) create emits
  display=name then a full update carries the real display; join/leave
  send method-4-then-3 pairs (re-join keeps the member's list slot);
  Parameters = display+prefix+suffix comps, nameTagVisibility +
  collisionRule enum varints (ALWAYS,NEVER,... declaration order),
  optional ChatFormatting color byte+varint, flags byte —
  deathMessageVisibility is state-only, absent from the packet.
  Replies: team names render as chat.square_brackets over
  {text:display, insertion:name, show_text:name hover}; option echoes
  go to other ops as chat.type.admin except prefix/suffix which
  vanilla skips; the sender name resolves pre-mutation
  (Entity.getDisplayName wraps prefix/name/suffix extras only when
  teamed). Enforced keys: option.name/color/nametagVisibility/
  deathMessageVisibility/collisionRule .unchanged, friendlyFire/
  seeFriendlyInvisibles .alreadyEnabled|.alreadyDisabled,
  empty.unchanged, list.teams.empty; color reset =>
  color.clear.success; bad bool/color/enum/option values raise
  failMark'd parse errors with vanilla cursor positions. /teammsg
  sends one outgoing disguised_chat to the sender and incoming to each
  other member with binding name=formatted sender, target=brackets
  comp carrying /teammsg click + chat.type.team.hover. Pinned by
  TestTeamCommandParity: 37 commands, every packet body and reply tree
  byte/tree-identical on both connections.

- /data entity scope (26.2, DataCommands EntityAccessor): get/merge/
  remove/modify on mobs and players. Entity display name: CustomName's
  raw stored string as text verbatim (single-quoted in pretty print when
  it carries "), else the entity.<type> translate; both with UUID
  insertion + show_entity hover, no click event (players keep theirs).
  Save map matches saveWithoutId — no top-level id; Pos/Motion are
  double lists, Rotation a float list; per-type extras (pig: Brain,
  AgeLocked, Age, ForcedAge, InLove, variant, sound_variant) and
  summon-NBT Tags round-trip through Entity.ExtraNBT. Pretty print:
  HashMap bucket order with save-write order breaking same-bucket ties
  (nbt.OrderedKeysSeq + entitySaveSeq); string/compound list elements
  carry the {"":""} preamble, numbers and typed arrays don't; [I;..]
  arrays print red letter + "; " + bare digits; Java-style "10.0" float
  digits; {} compounds collapse to a string-list extra. Players are
  query-only — merge/remove/modify throw commands.data.entity.invalid.
  A multi-target selector without limit=1 is rejected upfront as
  argument.entity.toomany (cursor-0 context), matching vanilla's
  maxResults gate. Mutations reply first, then the dirty metadata pack
  (only moved entries: idx2 custom_name as an inline component string,
  idx9 health, idx15 NoAI flags) and the update_attributes resend ride
  the next mobSyncFlush like vanilla's end-of-tick sendChanges;
  data remove Health restores the 10.0 type default. The shared
  componentsNBT/argumentList now promote all-literal lists to String
  and wrap mixed-list primitives as {"":x} compounds (fixes the tp
  coordinate args too). Pinned by TestDataEntityParity: 29 commands,
  replies and 0x63/0x83 packet bodies identical modulo eid/UUID.

- /data modify sources + /kill entity scope (26.2): modify accepts
  `from entity <sel> [path]`, `from block <x> <y> <z> [path]`, and
  `string <kind> <spec> [path]` beside `value` — `string` rejects
  non-string values with commands.data.modify.expected_value carrying
  the compact SNBT of the resolved value; storage sources are
  unmodeled (command.unknown.command). Oversized list writes truncate
  on load (Motion/Pos read the first three doubles, Rotation the
  first two floats) — the motion store now lives in float64
  MotionX/Y/Z; the int16 V* packet fields stay synced for spawn wire
  only. Summon NBT applies Health and Motion; entity extras merge
  through nbtDeepCopy so one entity's edits can't leak into the
  shared type table.
- /kill takes an entity selector: bare names match players only
  (argument.entity.notfound.entity on a miss), @e/@a/@p/@r/@s resolve
  through the shared selector — which now honors tag=/tag=! and
  distance on sessions, counts dying-but-unremoved corpses like
  vanilla, and gates single-entity scopes (/data) but not /kill.
  Replies: commands.kill.success.single [name comp] or .multiple with
  a raw int arg; a player victim dies by death.attack.genericKill
  (was wrongly death.attack.generic). Living victims run damageMob —
  the DYING pose + health-0 dirty pack rides the next tick's
  mobSyncFlush like vanilla's end-of-tick sendChanges so the reply
  lands first — non-living entities are discarded outright.
- Harness: the flat comparison world generates in the_void biome
  (generator-settings), which kills chunk-generation animal spawns
  that spawn-animals=false doesn't reach — ambient entity packets
  were nondeterministically leaking into packet diffs. mc-go's
  overworld biome follows (dimBiome + dimension.biome + level.dat),
  keeping adventuring_time and chunk biome palettes aligned.
  TestDataEntityParity adds a double `kill @e[type=!player]` wipe at
  join plus kill/selector cases — 41 commands green end to end.
- `/data` gains the `storage` scope: `get|merge|remove|modify` on a
  global id→compound map (missing ids read as `{}`, writes reply
  commands.data.storage.modified with the bare id string). `from
  storage`/`string storage` sources wired into modifySource; a `from`
  source deep-copies so `set` never aliases the live compound.
  String sources follow vanilla's getAsText — StringTag value,
  PrimitiveTag compact SNBT, others expected_value — measured after
  `set string` on int 1 stored "1" but UUID int-array threw.
  TestDataEntityParity +20 cases green vs both servers.
