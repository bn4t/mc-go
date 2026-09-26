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
      falling columns and dry-up. Redstone and scheduled block ticks
      remain. Random ticks now cover the crop family — wheat/carrots/
      potatoes/beetroots, melon/pumpkin stems (attach + fruit on tagged
      supports), nether wart, cocoa, sweet berry bush, pitcher crop's
      two-half growth, torchflower_crop's 1/3 pre-gate and morph into
      torchflower at maturity, farmland moisture/revert, turf spread
      and shade die-back, nylium reversion (the sampler and its
      per-section gate are in place; fire spread/decay,
      cactus/sugar cane/bamboo/chorus columns, saplings, snow
      form-and-melt, frosted ice and cave vines remain open)
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
- [~] RCON, query, properties parity
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
  Crits and sweep are pinned (planAttack, TestCritSweepParity);
  enchant damage modifiers in combat remain open.

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
- `execute` gains its `if`/`unless` condition forms measured on the
  vanilla jar: `items` (block coords / entity slot, single `name.N`,
  `name.*` families and `*`, tag `#` and `count=N` predicates, empty
  slots never match), `data` (block/entity/storage plus a REQUIRED path
  arg — `if data storage x run` swallows `run` as the path and errors
  on the next keyword), `score` (`matches` int-range + `= < <= > >=`
  holder compares, missing objective/holder → silent false), `entity`
  (bare player miss is silent, not an error), `dimension`, `biome`,
  `predicate` (resource error + context). A failed condition kills the
  chain with no packets — `run` on a dead chain replies nothing.
  `unless` was double-broken (negated the AND and flipped
  unconditionally). Token-cursor offsets threaded through conditions
  so error context packets underline the right token. Scoreboard
  replies rewritten to components: objectives.add.success,
  players.set.success.single with the bracketed hover objective and
  tell-suggest holder; set on a missing objective is
  arguments.objective.notFound with NO context line. `data get
  storage` no longer creates the entry it reads. TestExecuteIfParity:
  ~60 commands green vs vanilla.
- `execute` finishes its condition forms and gains `store`: `if blocks`
  (int blockpos triple + all|masked — `strict` is clone-only — plus an
  untracked-column failure modeled on session-tracked chunks), `if
  loaded`, `if function` (every id misses — no function table),
  and `if block`'s int-parse error shape. A chain ending on a bare
  condition is a leaf: `conditional.pass`/`fail`, or `pass_count` with
  `if entity`'s match count (even 1); a missing-objective `if score`
  errors at the leaf but reads false mid-chain. `store result|success`
  writes block (eager commands.data.block.invalid), entity (players
  silently unwritable), score (eager arguments.objective.notFound;
  fake holders allowed), storage (path+type+scale — `run` landing on
  the type slot is unknown.argument) and bossbar (always
  commands.bossbar.unknown — no bossbar table). reply carries the
  leaf's numeric result: time query value, data-get getNumeric
  (number/size), players get score — dead chains and parse errors
  store nothing. Also fixed players.get.success arg order
  [holder,score,objective] with fake holders as bare {"":name} args,
  and the long SNBT suffix is uppercase L. TestExecuteIfParity ~100
  commands green vs vanilla.
- `execute` gains its positional and executor modifiers:
  `positioned <pos|as <sel>|over <heightmap>>`, `as`, `at`, `align`
  (per-axis floor of the chain origin), `rotated`/`facing` (consumed —
  unobservable without ^ local coords — with entity-sourced forms
  forking per match), `anchored`, `summon` (redirect: spawns at the
  chain origin, binds @s, leaf errors still print) and `on` (every
  relation resolves empty — no entity relations modeled). Vanilla's
  fork-vs-redirect split is the load-bearing semantic: forks
  (as/at/positioned-as/facing-entity/rotated-as) run the tail once per
  match and swallow the leaf's runtime failures — `data get block` on
  a non-BE and bare-leaf objective.notFound both print inside a
  redirect but go silent inside a fork — while chain PARSE errors
  (unknown.argument on `run nosuchcmd`, anchor/enum/entity not-found)
  escape either way. The chain carries a session-scoped frame
  (execEnt/execPos) the fork binds and restores: @s reads the executor,
  ~ the moved origin, `at` additionally rebinds the dimension.
  `summon` needs a tail — bare `execute summon pig` is
  command.unknown.command and spawns nothing; a bad id is
  argument.resource.not_found[id, minecraft:entity_type]; spawned
  entity xz centers on the block like vanilla, y stays exact. The
  summon leaf's own errors print since it redirects a single source.
  TestExecuteIfParity ~130 commands green vs vanilla.
- Selector arguments (26.2, EntitySelectorParser): the @[...] parser is
  now a real tokenizer — commas inside {}/[] and quotes no longer break
  the split — with the full option set: type= (single — a repeat,
  even negated, is argument.entity.options.inapplicable[key] spanning
  the arg through the bracket), entity-type tags via #id (a literal
  membership table — unknown tags simply never match), gamemode=,
  level=, scores={obj=range}, team= (empty arg = unteamed, ! = any
  team), advancements={id|=bool|={crit=bool}} (unknown advancement ids
  fail the term outright), predicate= (accepted but always-false — no
  predicate table), nbt={...} partial-match (compounds recurse, lists
  compare elementwise, tag types must match: {Health:10} ≠ 10.0f),
  x_rotation/y_rotation (the RANGE bounds are themselves wrapped with
  wrapDegrees — -180..180 becomes [180,180] and misses everything —
  and a wrapped lo>hi tests the wrap-around union; absent ends default
  to 0 and 359), dx/dy/dz volume (the box normalizes negative deltas),
  name= with quoting and !negation, distance=, sort= (nearest/furthest/
  random/arbitrary) and limit=. Error keys measured per option:
  options.unknown, options.mode.invalid, options.sort.irreversible,
  options.type.invalid, options.inapplicable, range.empty,
  parsing.double.expected, parsing.expected, nbt.expected.compound,
  and parse-time argument.entity.toomany whenever a single-target
  scope gets an effective limit≠1 — cursor at input start. The
  implicit orderings are EntitySelectorParser's fields: @e/@a gather
  order (mc-go's is entity-id stable now), @p nearest, @r shuffled.
  Player-bound options (gamemode/level/advancements) exclude
  non-players even negated, players' entity records no longer
  double-count in @e, and non-players with no scoreboard entry fail
  scores= unconditionally. TestSelectorArgsParity ~52 commands green
  vs vanilla.

- /tellraw + /title (all forms): component args parse the joined
  remainder as SNBT — bare words become string components, lists use
  the first element as base with the rest folded into extra — and
  {score}/{selector} keys resolve server-side before send
  (scoreboard values to literal text, selectors to the decorated
  display name; misses resolve to ""). {text:X}-only compounds
  collapse to the bare string on the wire at every nesting level.
  /title clear/reset send PlayClearTitles with the reset flag; times
  args take t/s/d suffixes (×1/×20/×24000, math.Round), fail
  parsing.float.expected at arg start on non-floats and
  argument.time.tick_count_too_low at arg end on negatives, and cap
  negative-input errors at the first bad arg. @e-family selectors in
  the player slot fail argument.player.entities at parse with the
  whole input underlined. Component parse failures carry an absolute
  input cursor (they span whitespace-separated tokens), and error
  echoes now preserve raw input whitespace — including a trailing
  space after the last token. TestTellrawTitleParity 35 commands
  green vs vanilla, comparing chat replies plus the five title/tellraw
  packet ids.

- /difficulty, /seed, /list, /whitelist, /banlist, /setidletimeout,
  /say, /me, /msg|/tell|/w, and the /experience alias: difficulty
  queries report options.difficulty.<level> and sets fan Change
  Difficulty out; /seed answers the green bracketed copy_to_clipboard
  component (worldSeed() now feeds hashedSeed in join/respawn too);
  /list is commands.list.players with count/max/name-or-nameAndId
  components; bare utility names fail command.unknown.command with the
  cursor at end of input, bad args fail unknown.argument at their
  token, and /msg rejects @e-family selectors argument.player.entities
  and unknown players argument.entity.notfound.player (no context).
  TestUtilCommandParity 33 commands green vs vanilla.

- /spawnpoint + /setworldspawn: blockpos args are ints or ~relatives
  with parsing.int.expected / parsing.int.invalid (overflow int32 keeps
  the literal back) / argument.pos3d.incomplete cursors, the rotation
  tail is a yaw+pitch pair (argument.rotation.incomplete on one value,
  parsing.double.expected on a bad element, ~ resolves against the
  sender's view), and success carries [x,y,z,yaw,pitch,dimension] plus
  the decorated target name for spawnpoint's single form. /spawnpoint
  writes per-session respawn data that respawnPlayer now honors (incl.
  cross-dimension); /setworldspawn updates the world spawn and fans
  Set Default Spawn Position out only when the point actually changed.
  TestSpawnCommandParity 27 commands green vs vanilla.

- /gamerule + send_command_feedback: query/set replies carry
  [name, value] strings; minecraft:-prefixed names resolve to the bare
  rule; bool rules fail parsing.bool.invalid on other literals, int
  rules fail parsing.int.expected / parsing.int.invalid (overflow keeps
  the literal) / argument.integer.low[i(0),i(-1)]; bad rule names are
  unknown.argument at the token. Setting send_command_feedback false
  suppresses every later command reply — including the set's own — and
  error context, gated at both dispatch sites. TestGameRuleParity 23
  commands green vs vanilla.

- Operator commands + /tag: op/deop reply with the resolved name and
  send the affected player entity-status 24+level plus a command-tree
  resend; kick drops the target with multiplayer.disconnect.kicked (or
  the typed reason as a literal) and reports the decorated name plus a
  nested-compound reason; ban/pardon and ban-ip/pardon-ip track
  bannedNames/bannedIPs, kick live sessions with banned[_ip].reason,
  and reject at login with the same component. /tag resolves entities
  (not players) so misses report argument.entity.notfound.entity, add
  and remove report .single/.multiple with the tag as an {"" -> name}
  compound, and list joins green tag literals with gray ", ". Join and
  leave notices switched to the yellow multiplayer.player.joined/left
  translatables. TestAdminTagParity 35 commands green vs vanilla, Q's
  disconnect packets included.

- /worldborder: get reports the size unformatted, center broadcasts
  set_border_center with the vec2 .5-centering ("10" -> 10.50), set/add
  take a tick-duration second arg (5 -> lerp VarLong 5, set.grow reply
  [size.1f, ticks*0.05.2f]; set.immediate "600.0") and fail
  nochange when the size already matches. warning time is a
  time()-arg (tick_count_too_low at the arg end), warning distance a
  plain int (integer.low at the token); damage amount/buffer reply
  %.2f with float.low. The join-time initialize_world_border now
  carries the real per-dimension state. TestWorldBorderParity 36
  commands green vs vanilla, border packets 0x58-0x5c included.

- /bossbar: bars keyed by normalized minecraft: id with a random v4
  uuid; create/list/get/set/remove replies carry the hoverable
  [name] label (square_brackets + show_text + insertion) colored by
  the bar's chat formatting (purple -> dark_blue). boss_event emits
  op0 add/op1 remove/op2 progress/op3 name/op4 style to members:
  hiding or clearing players sends remove, re-showing resends the full
  add, progress clamps to 1.0. unchanged/unknown/dup failures are red
  errors (unchanged via commandError too), name/selector args reuse
  the component machinery. TestBossbarParity 43 commands green vs
  vanilla incl. the member packet stream.

- /playsound /stopsound /particle: playsound sends per-target sound
  packets (holder=inline name + category + fixed-range flag +
  pos*8 ints + volume/pitch + random seed, seed masked in the pin),
  stopsound packs its flag byte over source/sound, particle walks
  particle/options -> vec3 pos -> f32 delta triple -> speed -> count
  -> force|normal -> viewers with the all-or-nothing delta+speed+count
  chain, invalidOptions listing each missing option key, and
  notFound landing at input end. Delta args parse as doubles though
  the wire writes floats. TestFxParity 48 commands green vs vanilla.

- Misc server commands: save-all/off/on (saving+success pair,
  alreadyOff/alreadyOn, save.failed when the world has no path),
  defaultgamemode, setidletimeout (0 -> disabled reply), reload
  resending update_tags + update_recipes + recipe_book_settings in
  vanilla's live-reload registry order (bodies captured; packet
  presence and length pinned since the map order is not stable),
  debug/perf/jfr profiling shells with alreadyRunning/notRunning
  edges, trigger's objective.notFound, random value|roll ranges,
  spectate (self/not_spectator/entity resolution) and transfer's
  transfer packet + port bounds. TestMiscParity 69 commands green.

- Entity commands: rotate (player_rotation 0x49 = [f32 yaw][00][f32
  pitch][00], look_at 0x47 = [anchor][3 f64][entity tail], ~ and
  greedy vec parsing), ride (set_passengers 0x6B + rider position
  sync, not_riding/already_riding/cant_ride_players/loop edges),
  spreadplayers (pos2d ~, float.low bounds, optional "under
  <maxHeight>" literal, teams share one slot so n>1 same-slot spreads
  report "Infinity"), damage (damage_type registry validated,
  invulnerable gate for creative/spectator, at|by|from forms,
  death.attack.<msgId> broadcast). TestEntParity 74 commands green.

- Item commands: enchant (entity selector, resource registry, level
  cap/compat order, itemless takes the entity's literal display name —
  no hover component) and clear (player-only arg rejects @e with a
  whole-input underline, item id + maxCount bounds, maxCount 0 counts
  without removing). itemnames_gen.go + entitynames_gen.go hold the
  en_us display tables. TestInvParity 28 commands green.

- World-state commands: forceload (pos2d + 256-chunk bound + clamped
  30M, add/remove single|multiple|failure, query list sorted by
  ChunkPos.toLong, added.none on empty), fillbiome (quart-quantized
  volume bound from max_block_modifications, unloaded before toobig,
  resource.not_found with the worldgen/biome registry key, count =
  quart cells), datapack (enabled/available two-line list, decorated
  pack components, enable/disable/create edges), help (bare-string
  per-root usage table captured from vanilla, smart-usage overrides),
  version (the 10-line identity block), waypoint (list/modify color +
  style on the locator-bar waypoint), swing (entity-plural, living
  filter, animate broadcast). TestWorldCmdParity 68 commands green.

- Admin/debug commands wave two: stopwatch (real elapsed-second
  stopwatches keyed by normalized resource id), return (silent bare
  int/fail; `run` relays the inner command's reply verbatim, unknown
  inner name is unknown.argument), schedule (function arg normalizes
  and misses with arguments.function.unknown, tick_count_too_low for
  negative times, cleared.failure), function (nbt.expected.compound
  for a bare-word tail, unknown for every name — no datapack
  functions), fetchprofile (name/id failure for offline-unresolvable
  lookups; entity resolves players to the four green bracket actions —
  copy_component/give_item/summon_mannequin/copy_text — and mobs to
  no_profile), attribute (resource registry vs minecraft:attribute,
  per-type attribute tables, base get/set/reset and modifier
  add/remove/value get with vanilla's argument ordering — the parsed
  [scale] arg is ignored by base/modifier gets), dialog (players-only
  entity arg validates before the dialog id; three vanilla dialogs:
  custom_options/quick_actions/server_links), locate (structure/
  biome/poi — not_found vs invalid vs resource.not_found per arg
  kind, biome success only when the requested biome is the world's
  uniform one), place (feature success pos ints, template/jigsaw/
  structure invalid+failed paths). spawn_armor_trims/serverpack/
  unpublish/raid/chase are pinned UNREGISTERED — the dedicated server
  never registers them. adminnames_gen.go holds the attribute/poi/
  dialog/structure/feature/pool registries from the jar.
  TestAdmin2Parity 124 commands green.

- /clone: the [from <dim>] <begin> <end> [to <dim>] <destination>
  [strict] [replace|masked|filtered <block>] [normal|force|move]
  grammar (strict is a literal before the mask; move/force/normal only
  parse after a mask literal; a missing filter block or position is
  command.unknown.command, an unknown filter block id.invalid with the
  normalized name, an unknown property property.unknown underlined
  inside the token). Execution checks dimensions, then loaded bounds,
  then toobig against max_block_modifications, then the overlap guard
  — which only applies to mode normal; force and move allow
  self-overlap. masked skips air cells and filtered keeps only the
  predicate (names, [props], and #tags); zero written cells is
  commands.clone.failed. move erases exactly the cells that were
  copied, skipping cells inside the destination. BlockPropNames is the
  new world export backing the property-name check.
  TestCloneParity 55 commands green.

- /loot: the give <players> / spawn <pos> / insert <pos> /
  replace entity <ents> <slot> [count] / replace block <pos> <slot>
  [count] targets crossed with fish <table> <pos> [tool] / loot
  <table> / kill <entity> / mine <pos> [tool] sources. Grammar details
  measured: a bare token after the target is unknown.argument at the
  token; entity arguments resolve at EXECUTE time (bare components,
  no context) — a selector syntax error keeps the whole-input mark,
  a resolve miss is argument.entity.notfound.{player,entity};
  replace's [count] is the number of table rolls, not a stack size,
  and falls through to the source when it doesn't parse as an int;
  slot.unknown underlines the slot token's end; insert/replace-block
  reject non-containers with commands.item.target.not_a_container and
  unloaded positions; fish's position must be loaded but spawn's
  doesn't; mine on a block without a table is
  commands.drop.no_loot_table.block. Replies distinguish one produced
  stack (drop.success.single with the clickable item bracket
  component) from several (drop.success.multiple with a bare count);
  the kill source reports multiple_with_table with the table path.
  rollLootTable now resolves nested loot_table entries, sequences,
  groups, and picks the first productive alternatives child —
  conditions aren't evaluated yet (noted: silk_touch on mine).
  TestLootParity 38 commands green.

- Messaging and operator-set commands: /msg /tell /w selector
  expansion was already pinned; the grammar around it is now measured
  — a bare /teammsg is command.unknown.command (the message arg is
  required), and /save-all accepts only the optional flush literal
  (a second arg fails inside it, underlined at the extra token).
  Successful state-changing commands broadcast a gray-italic
  chat.type.admin [issuer, message] wrap to the other operators:
  /save-all (saving to the issuer, success broadcast), /save-off and
  /save-on (enabled/disabled), /ban-ip and /pardon-ip. ban-ip
  validates dotted-quad octets (999.999.999.999 is
  commands.banip.invalid, as is an unknown player name), takes the
  typed reason as a bare string in banip.success's with list, emits
  commands.banip.info with the kicked-player components joined by a
  gray ", " when anyone was connected on the address, and kicks in
  join order — flushing each kick's replies and leave broadcast
  before the next disconnect. pardon-ip rejects non-IP literals with
  commands.pardonip.invalid and a valid-but-unbanned address with
  pardonip.failed. TestAdminCmdParity 37 commands green.

- Operator-set commands round two: /ban resolves the target's
  canonical name (session name online, typed literal offline),
  takes a typed reason as a bare string in ban.success, broadcasts
  the success wrap to the other operators, then kicks with the
  leave broadcast ordered before it returns — a self-ban still
  reports because the issuer's reply flushes inline. /pardon echoes
  the canonical name too. /op dupes to commands.op.failed, /deop
  removes the seeded ops entry; neither broadcasts. /whitelist grew
  the full grammar: on|off with alreadyOn/alreadyOff, add/remove
  with canonical names and add/remove.failed, list with
  {"":n}-wrapped count plus joined names, reload — none broadcast.
  TestOpsAdminParity 46 commands green; the compare splits events
  per connection (cross-socket order is delivery racy) and sorts
  hash-ordered name/rule listings.
- Entity command set completion: /damage, /ride, /rotate,
  /spreadplayers were already present; verified end-to-end against
  vanilla (104 probes). Fixed /ride's mount check order — vanilla
  reports already_riding (naming the rider's CURRENT vehicle)
  before cant_ride_players and mounting_loop. Added /test's literal
  surface: runclosest/runthese/runfailed/runthat print
  commands.test.no_tests plain, clear*/reset* land on
  clear.error.no_tests, pos fails no_test_containing_pos with the
  sender's block position, stop is silent, run/runmultiple/locate/
  verify reject unknown test ids via resource_selector.not_found
  with the end-of-input cursor, create succeeds with a namespaced
  id, and every unregistered literal underlines as
  command.unknown.argument. TestEntCmdParity green.
- Admin file persistence: mc-go gained a ServerDir holding vanilla's
  four admin files byte-for-byte — ops.json, whitelist.json,
  banned-players.json, banned-ips.json — created as [] at boot,
  rewritten pretty-printed on each /op /deop /whitelist add-remove /
  ban /pardon /ban-ip /pardon-ip, and reloaded at startup and on
  /whitelist reload. Entries keep vanilla's HashMap iteration order
  (String hash of the dashed-uuid key over buckets, insertion inside
  a bucket), the "yyyy-MM-dd HH:mm:ss -0700" created stamp, the
  issuer-name source, and "forever" expires. The whitelist flag now
  rejects unlisted logins with not_whitelisted. The binary's new
  -server-dir flag defaults to "." like a real server's workdir;
  leaving it empty disables the files for library embeds. Vanilla's
  white-list=true server.properties rewrite is not mirrored — mc-go
  has no properties file. TestAdminFilesParity diffs the files.
- Per-command op-level gating: vanilla's measured level map (0:
  help/list/me/msg+tell+w/random/teammsg+tm/trigger; 3: ban, ban-ip,
  banlist, debug, deop, kick, op, pardon, pardon-ip, setidletimeout,
  tick, transfer, whitelist; 4: jfr, perf, save-all/off/on, stop;
  2: everything else) now lives in commandLevels. runCommand reports
  gated names as unknown.command with the root literal underlined —
  the same shape as an unregistered command — and the commands-tree
  packet and /help both filter to the caller's level (help drops
  /random's reset and /datapack's create below their levels, and
  answers redirect aliases like `help tp` with silence).
  TestCommandLevelParity drives all registered names plus help-arg
  cases at levels 0/2/3 against both servers.
- Bare-command replies: roots with no bare executable (data, execute,
  fill, give, scoreboard, setblock, summon, team, teleport, tellraw,
  title, tp) now answer a bare call with command.unknown.command and
  the root literal underlined — vanilla's parser-exhaustion shape —
  replacing the old "Usage:" strings. /version's build_time with-arg
  is the real 26.2 stamp. TestCommandLevelParity now compares all
  names strictly at every level.
- RCON: the Source-style remote console, measured on vanilla's
  enable-rcon/rcon.password/rcon.port. Auth echoes the request id in a
  type-2 packet, a wrong password and every packet on an
  unauthenticated connection answer id -1 (the connection stays open),
  type-2 commands run at level 4 and answer with the flat text of all
  feedback messages concatenated, replies over 4096 bytes split, and
  other types answer "Unknown request %x". Flat rendering goes through
  component.plain against langEnUS, the jar's en_us.json generated
  verbatim by tools/genlang into langtext_gen.go. RconAddr/
  RconPassword configure it; -rcon-addr/-rcon-password on the binary.
  TestRconParity compares every case byte for byte.
- GameSpy4 query: enable-query/query.port measured on vanilla's
  QueryThreadGs4 — handshake echoes the request's ident bytes and a
  random challenge (valid 30s, pruned on receive), stat answers reuse
  the handshake ident rather than the packet's sid field, a request of
  exactly 15 bytes triggers the full stat (any other padding falls back
  to basic), the full answer is key/value pairs between splitnum and
  player_ sentinels, and unauthenticated or malformed packets are
  dropped. EnableQuery/QueryAddr on the server, -enable-query/
  -query-addr on the binary. TestQueryParity diffs every case.
- /tp grammar parity: rewritten as a brigadier-model emulation —
  literal children in vanilla's registration order (vec3 self, entity
  dest, entities targets), potentials sorted by full consumption then
  error-free then order, single-exception throw else
  command.unknown.argument at the winning subtree's rest token.
  Covers entity/coords forms, multi-victim .multiple replies with int
  counts, name/UUID/@-selector resolution, victim vs source coord
  bases (~ is source-relative, rotation ~ is victim-relative),
  ^-local look-frame coordinates, absolute/relative yaw-pitch with
  wrapDegrees, the isInSpawnableBounds invalidPosition check
  (|x|,|z| < 30M, -20M <= y < 20M), notfound.entity exec errors, and
  entity() vs entities() single/multi parse-time enforcement. Vanilla
  has no facing node under tp's rotation in 26.2 — trailing args
  underline from tok2. TestTpParity diffs 41 cases event-for-event,
  including error underlines.
- Invulnerability + void-damage parity: damage gating switched from
  "survival only" to vanilla's isInvulnerableTo — creative and
  spectator reject every source except bypasses_invulnerability types
  (out_of_world, generic_kill), so they still die to the void and
  /kill; adventure takes damage normally. Gate sites unified:
  damage(), damageWith(), voidTickStep, /damage's invulnerable reply,
  melee attacker (spectator can't attack, creative can), mob targeting
  and player-victim filtering (creative+spectator skipped, adventure
  included), tool/armor durability (creative-exempt, adventure wears).
  Void death key corrected to death.attack.outOfWorld with the player
  chat component. Dead-player selector semantics pinned: @e drops
  dead players (entity list filter), @a/@s still resolve them.
  TestTpVoidDeathParity diffs the void death broadcast plus eight
  post-death tp command replies event-for-event.
- /locate parity: structure/biome/poi now follow vanilla's argument
  and exec-stage split. Biome arg is resource_or_tag: '#' enters tag
  mode (missing tag -> argument.resource_tag.not_found), charset-stops
  report resource.not_found at the stop offset with the resolved
  "ns:path-prefix", '#' mid-token is expected.separator, and malformed
  ids (second ':') are argument.id.invalid at the token start.
  Structure arg is resource_or_tag_key — bad charset reports
  expected.separator and registry misses report structure.invalid/
  not_found at exec (no context event); tags resolve the same way.
  POI mirrors biome. findClosestBiome3d reimplemented as the flat-world
  specialization: uniform biome means the first spiral cell always
  hits, so success = source block pos with Y clamped to
  [minY+1, maxY+1] and floor(sqrt(distSqr)) distance; tag success
  prints "#tag (resolved biome)". Tag/biome member tables mirror the
  vanilla datapack (the_void only in without_wandering_trader_spawns).
  TestLocateParity diffs 48 slots including underlines and Y=-3000.
- Entity selector options parity: parseSelector rewritten to vanilla's
  grammar — option names bind before '=', the type-repeat cursor uses
  the two-rule form (a seen positive type errors at the option-key
  start, negatives-then-positive at the value start), scores and
  advancements are single-use (repeatable options are tag/nbt/
  predicate), and inner cursors resolve via voff + Index(val,e) for
  compound-aware comma/range splitting. parseRangeStrict keeps
  presence flags: distance=0/..0 is an exact-zero match, the negative
  check only fires on present bounds, '..'/'abc' ->
  argument.range.empty, '5..3' -> argument.range.swapped (rotations
  skip the swap check — degrees wrap). selectDataEntities now runs
  selMatch for @s (options filter self; only limit/sort are rejected
  as inapplicable), '@n' maps to entity domain + nearest + limit=1
  with p/r/n defaults overridable by explicit sort/limit, name/tag/
  team/gamemode/type/nbt store '!-stripped value + neg flag (team= is
  no-team, team=! is has-a-team, tag= needs tagCount==0, tag=! any
  tag), x/y/z alone re-anchor per axis with no volume, and a volume is
  the per-axis [a+d,a]|[a,a+d+1] span moved to the anchor with
  AABB-intersect matching via mobBounds. summon learnt the missing
  mobHealth/mobBounds entries (husk, item, armor_stand, ...) and now
  stores Tags as nbt.List while mobTagList reads either storage form.
  Player displayName follows the team wrap ({text:P} bare, {text:"",
  extra:["",P,""]} teamed) so entityTargetComponent became a *Server
  method via s.teamOf. /kill removes a living mob immediately on death
  (no 20-tick corpse — that's combat damage only). Pinned by the new
  ~107-command TestSelectorOptionsParity plus the healed
  TestSelectorArgsParity, both verbatim vs vanilla.
- /gamemode unchanged-mode silence: vanilla's setGameMode returns
  false when the target already has the requested mode and the
  command emits nothing — no success chat, no GameEvent, no
  abilities/info updates, no admin broadcast — for self and other
  targets alike (errors still fire first). runGamemode now early-
  returns reply{} when old == mode; TestGamemodeParity's silent-
  repeat assertions pass verbatim.
- /scoreboard rewrite to vanilla's grammar: objectives
  list|add(criteria required, optional SNBT display)|remove|setdisplay
  (list/sidebar/below_name/sidebar.team.<color> slots, set/cleared/
  alreadySet/alreadyEmpty replies) and players list (all holders in
  HashMap keySet order; single-entity form renders each score as a
  separate entity.entry chat and accepts only single-target selector
  shapes — @a/@e/limit>1 is a parse-time toomany at the token end) plus
  get/set/add/remove/reset/operation with single vs multiple reply
  shapes, fake-name holders as bare strings vs player/entity holders
  as decorated components (tell-suggest / insertion+uuid hover), and
  vanilla's value-added ordering (int parses before selector
  resolution, notfound.entity at exec time). Holder-set iteration
  pinned to java.util.HashMap spread-hash buckets and objectives to
  fastutil descending-slot order; the blackbox test normalizes the
  genuinely nondeterministic parts (uuid-hashed holder names,
  identity-hashed per-holder score maps). ~85-command
  TestScoreboardListParity passes verbatim vs vanilla.
- scoreboard objectives modify + players enable/display: modify gains
  displayname (component, re-ships mode-2 Update Objectives),
  displayautoupdate (parsing.bool.invalid at token start for junk),
  numberformat (bare clears; blank/fixed <component>/styled <style>)
  and rendertype integer|hearts; players enable releases the
  trigger-score locked flag (non-trigger -> enable.invalid, nothing
  left to enable -> enable.failed); players display
  name|numberformat set/clear writes the per-score overrides that
  ride on every Update Score. Update Objectives packet corrected to
  vanilla's layout (unconditional display component + render-type
  enum + optional number format — no presence bools) and Update
  Score carries the two optional overrides. Score entries are now
  sbScore{value, locked, display, format} so overrides survive the
  value write.
- /trigger wired to the scoreboard: trigger <objective> [add|set
  <value>] runs the caller's own score — objective missing is
  arguments.objective.notFound, non-trigger is
  trigger.failed.invalid, a missing or still-locked score is
  trigger.failed.unprimed, and a successful write re-locks the
  score (enable releases it again). simple/add/set success replies
  carry [formattedObjective] / [formattedObjective, i(value)].
- Survival dig state machine + break-speed parity (javap-measured on
  26.2): the tick loop only accumulates progress and never breaks —
  START insta-breaks when first-tick progress >= 1, otherwise latches;
  STOP on the latched pos commits the break inside the action handler
  when progress completed, and an early STOP arms hasDelayedDestroy
  which still breaks the block once the counter crosses the requirement
  (the arm survives both a re-start and an abort); ABORT unlatches the
  dig only. digTicks now mirrors getDestroyProgress =
  speed/hardness/(/30 if the tool passes hasCorrectToolForDrops else
  /100): digger materials gate on mineable/<kind> tags (wood 2, stone 4,
  copper 5, iron 6, diamond 8, gold 12, netherite 9), swords special-case
  cobweb/15 + sword_instantly_mines/inf + sword_efficient/1.5, shears
  take cobweb+leaves/15, wool/5, vine+glow_lichen/2; efficiency adds
  lvl^2+1 once speed > 1 and an airborne player pays /5. A delayed arm
  re-evaluates the held tool each tick, so swapping tools mid-dig changes
  break time as vanilla does. TestBlockDropParity digs 12 real probes on
  both servers end to end (tp + real timed digs + item-entity watches)
  and pins the drop multiset: tool gating, tier denial, silk-touch
  absence, door halves, crop age, shears-vs-bare cobweb. Harness notes
  that surfaced: vanilla ignores dig actions until the client reports
  loaded (player_loaded), ignores move packets that travel too far
  (moved-too-quickly resync leaves the player at spawn — /tp instead),
  only broadcasts block updates/entity spawns to clients that answered
  chunk_batch_finished (chunk_batch_received), and kicks a client that
  leaves keepalive unanswered. The scripted client now services all of
  it inside the dig loop. Full go test ./internal/server green;
  TestBlockDropParity identical on vanilla and mc-go.

- 2026-09-22: Random-tick crop family, measured on vanilla 26.2 bytecode
  and pinned end-to-end by `TestCropGrowthParity` (24 probes, identical
  converged end states on both servers): CropBlock gets the
  rawBrightness>=9 gate, the `(25/growthSpeed)+1` roll and the full
  getGrowthSpeed formula (3x3 farmland scan, hydrated=3/dry=1, off-axis
  quarters, halving only on (W|E)&(N|S) or a diagonal same-crop);
  StemBlock ages to 7 then spends the roll on a random side whose fruit
  cell is air over a `supports_<fruit>_stem_fruit` block and swaps to
  attached_<fruit>_stem[facing]; NetherWartBlock (10, no light), CocoaBlock
  (5, roll before the age<2 check), SweetBerryBushBlock (5 with light
  probed at pos.above), PitcherCropBlock (lower-half only, upper half
  written at age>=3, canopy-checked), TorchflowerCropBlock's extra
  nextInt(3) gate and AGE_1-capped morph into the torchflower flower;
  FarmlandBlock moisture (9x2x9 water box incl. waterlogged, drain, and
  dirt reversion when the block above is not in #maintains_farmland —
  isRainingAt absent until weather exists), SpreadingSnowyBlock turf
  spread (4 rolls into the 3x5x3 reach, target must be the base block,
  survive check and a non-water-above gate) and shade/submersion
  die-back, and NyliumBlock's reversion (bonemeal spread stays open).

  Supporting fixes the comparison forced: the gamerule is
  `minecraft:random_tick_speed` — the reader looked up the pre-26.x
  camelCase key and silently returned 3; and the sampler hit a real
  throughput wall — a block read is a shard round trip, so sampling each
  roll live ran ~14us per draw (seconds per tick at speed 4096, ~1 tick
  per 20s observed). The sampler now snapshots each tickable section once
  per tick (FillBox is one round trip per shard) and rules resolve
  in-section reads from the snapshot; the section scan also caches a
  has-opaque bit per section so the light check skips air-only column
  sections without a single read. And a client-side subtlety worth
  remembering: vanilla's /tp does not move the entity until the client
  confirms the teleport and echoes the position — an unconfirmed scripted
  client stays at spawn for chunk-tick purposes, so the crop harness
  sink now answers SyncPos (confirm + position echo) the way a real
  client does.

  Known residual divergences the test deliberately avoids pinning:
  vanilla pops a crop when a neighbor update makes its support invalid
  (observed: wheat planted on dirt breaks when the dirt grassifies —
  mc-go has no updateShape survival re-check on block changes yet — an
  open gap covering crops, torches, snow, cactus, ...) and the light
  check is column-opacity, not real light propagation (a one-block cap
  shades a crop here where vanilla's side leakage lights it — blocked
  on the lighting engine entry). `execute if block` on mc-go also still
  wants full-state specs for multi-property blocks where vanilla
  subset-matches.

- Random tick pass 2 (upward growers, commit pending): sugar cane,
  cactus (+ the age-8 cactus_flower roll, 0.1 / 0.25 at height 3, and
  its canSurvive gate), bamboo stalk + sapling (leaf cascade, the
  height-11+ 25% / height-15 terminal stage ramp, and the downward
  age-1 ripple vanilla gets via updateShape — a grown column ends
  all-age-1, and the tip caps stochastically at 12..16 rather than
  16), and chorus flower (up-growth is free when the plant run below
  is < 2, throttled run <= nextInt(5 grounded / 4) after that, dead
  flower at age 5, grown/dead level events 1033/1034) + the
  connection-mapped chorus_plant write. Pinned by
  TestColumnGrowthParity (tests/blackbox/coltick_test.go), 23 probes
  vs both servers.

  Two fixes the differential forced: grower rules must read the live
  world, not the section snapshot — the snapshot is one FillBox per
  section per tick, so a write-then-read-back (bamboo's demote-then-
  ripple) clobbers fresh state with stale when async shard sets land
  after the next snapshot. And chorus up-growth was inverted: run < 2
  always climbs; only deeper runs roll, never at run 5+.

  Same residual divergences as pass 1 apply here: no updateShape
  survival pops, no scheduled ticks (kelp etc.), no real light.

- updateShape survival pops (commit pending): neighbor-write support
  re-checks for the full canSurvive family — sugar cane, cactus,
  bamboo stalk + sapling, chorus plant + flower, crops (wheat,
  carrots, potatoes, beetroots), nether wart, torch, snow layer. A
  write at pos re-checks its six face neighbors and pops failures
  with the 2001 block-break effect; chorus flower keeps vanilla's
  direction==UP skip, and flag-2 silent writes (chorus growth's
  internal rewrites) skip the pass entirely. Pinned by
  TestSupportPopParity (tests/blackbox/poptick_test.go), 13 probes
  vs both servers.

  The differential forced the real placement semantics out of
  BlockInput.place: /setblock and /fill fold the requested state
  through updateFromNeighbourShapes, but an air result is discarded
  in favor of the requested block — so the return-air family (crops,
  torch, snow, ...) NEVER pops on placement, while the scheduleTick
  family (cane, cactus, bamboo, chorus) still pops one tick later
  via the fold's side-effect tick. Internal writes don't self-check
  at all. cropCanSurvive keeps only the structural half
  (#supports_crops): the hasSufficientLight term needs the lighting
  engine (a one-block roof fails column-opacity where vanilla's
  side leakage keeps the crop lit), so over-shaded crops surviving
  is a recorded gap.

- Random tick pass 3 (growing vines, commit pending): weeping,
  twisting, and cave vine heads grow one cell along their growth
  direction at age < 25 on a 10% roll, the new head carrying age+1
  (and cave vine heads an 11% berries roll). The old head's
  conversion to its plant body rides the same write — its updateShape
  sees a same-vine ahead — and the inverse holds too: a body whose
  growth-side neighbor stops being the vine becomes a head again with
  a fresh age 0..24 (berries carried through in both directions).
  Survival is attach-side gated — a vine pops when the cell it hangs
  or stands on is written away, cascading the cut column, and all six
  vine blocks are tick-family so a /setblock'd unsupported vine pops
  through the placement fold. Pinned by TestVineGrowthParity
  (tests/blackbox/vinetick_test.go), 20 probes vs both servers.

  Drive-by: block predicates now subset-match (name + only the spec's
  pinned properties) — vanilla's BlockPredicate semantics — so
  `if block cave_vines[age=2]` ignores berries, and the same fix
  flows into /fill replace filters and /clone filtered.

- Random tick pass 4 (kelp, commit pending): kelp rides the same
  GrowingPlant machinery — 14% roll into a WATER cell upward, head to
  kelp_plant on the way up — with kelp's two twists: canAttachTo
  rejects #cannot_support_kelp (magma_block), and its pop writes the
  cell's fluidState legacy (a water source) instead of air. The
  support-check predicate also corrected: vanilla's isFaceSturdy
  rejects fluid cells, so a column whose lower cell popped to water
  keeps cascading. TestVineGrowthParity now 31 probes.

  Two fluid-model fixes fell out of it, both measured on vanilla:
  - A cell already holding a fluid is never a spread target —
    intrinsically-waterlogged blocks (kelp, kelp_plant, seagrass,
    tall_seagrass, bubble_column; LiquidBlockContainer family) and
    explicitly waterlogged states hold a water source, so
    replaceableByFluid now excludes them. Before this, placing kelp
    under water let the column's water flow down through it and
    destroy it.
  - Natural writes wake fluids: setNaturalBlock now schedules fluid
    ticks for the cell and its neighbours, matching vanilla's
    setBlock→neighbour-update scheduling, so a popped kelp's water
    legacy immediately falls/spreads.

  Measured but NOT yet implemented: a water source directly above
  magma_block or soul_sand becomes a bubble_column (drag down / push
  up) that propagates up through a column of sources — the pop of a
  kelp sitting over magma lands water which then converts. Probe
  evidence: vanilla's cell after `setblock magma; setblock kelp` is
  none of water/air/kelp.

- Random tick pass 5 (leaves, commit pending): leaf decay is
  LeavesBlock's two-part dance — a neighbour write near a leaf
  schedules a +1 block tick that rewrites distance to
  min(six neighbours' getDistanceAt)+1 (log/wood/stem/hyphae via
  #prevents_nearby_leaf_decay read 0; anything with a `distance`
  property — scaffolding included, same vanilla quirk — reads it;
  everything else reads 7), with flags-3 writes cascading the
  recompute down a chain. isRandomlyTicking gates on distance==7 &&
  !persistent; the random tick then drops the leaf. Placement folds
  through the same updateShape, so setblock'd leaves settle distance
  and, being persistent=false by default, decay; persistent leaves
  hold. Differential test: floating leaf decays, leaf-on-log holds at
  distance 1, a seven-leaf chain settles 1..7 and the far tip
  crumbles, breaking the chain's log cascades all of it to decay.
  TestLeafDecayParity green both directions.

- Random tick pass 6 (amethyst, commit pending):
  BuddingAmethystBlock.randomTick — 20% roll picks a random face;
  air or a full water source sprouts small_amethyst_bud, a bud
  already facing that direction climbs small → medium → large →
  amethyst_cluster, all carrying facing=dir and waterlogged from the
  target cell's fluid state. Buds are the return-air family:
  updateShape folds to air only when the write lands on the attach
  side (facing.getOpposite) and the cell isn't sturdy — placed buds
  keep floating until then. Wrong-facing buds never upgrade (the
  facing gate blocks it). TestAmethystParity drives all of it:
  single-open-face fixtures for deterministic growth location,
  waterlogged cluster on a water face, pedestal pop, floating bud.

- Random tick pass 7 (mushrooms, commit pending):
  MushroomBlock.randomTick — 1/25 roll, spread via a 4-hop random
  walk (dx,dz ±1, dy = nextInt(2)-nextInt(2)); each hop sticks only
  where the target is empty and canSurvive. A 9×3×9 box holding 5+
  same-type mushrooms aborts. canSurvive: a soil in
  #overrides_mushroom_light_requirement (mycelium, podzol, nylium)
  always allows it; otherwise rawBrightness < 13 on a solid-render
  floor — approximated as sky-occluded (any solid block overhead)
  since mc-go has no light engine yet. VegetationBlock.updateShape
  pops on ANY neighbour change when canSurvive fails — a setblock'd
  mushroom on grass in daylight dies on its first neighbour write.
  TestMushroomSpreadParity: mycelium spread, saturated box,
  roofed-dark spread, sunlit pop, floor-break pop.

- Random tick pass 8 (regular vines, commit pending):
  VineBlock is its own five-face multiface block (up + four sides).
  randomTick: spread_vines gamerule, then 1/4 and a random direction.
  An unheld horizontal face either wraps around a corner — clockwise
  or counter-clockwise, into the flanking cell or the next-over one
  with the opposite face — or (cell already facing a wall on that
  side) gains the face onto a solid neighbour. Up grows an `up` face
  under a ceiling or sprouts a thinned copy of the cell above (each
  face kept on a coin flip only if the new cell can support it).
  Down (and any already-held face) copies random horizontal faces
  into the cell below. canSpread caps at five vines per 9x3x9 box.
  updateShape prunes every unsupported face on any write not coming
  from below, air when none remain. canSupportAtFace: sturdy side or
  a vine above holding the same face. TestWallVineParity: wall wrap,
  spread-down, ceiling-hole sprout, face pruning, spread_vines gate.
- Random tick pass 9 (ice melt, commit pending):
  IceBlock.randomTick: block light over 11 minus the block's own
  dampening (ice dampens 0) thaws — sky light plays no part, so
  sunlit ice holds. melt() is air under WATER_EVAPORATES (ultraWarm
  dims), a water source otherwise; the water then flows on the
  usual fluid ticks and can wash out torches before they melt
  their neighbour. mc-go has no light engine: blockLightAbove
  scans a curated emitter table (lit state honoured) inside
  taxicab 15-level of the cell. TestIceMeltParity pins the 11/12
  edge — torch at taxicab 2 delivers 12 and melts, at 4 delivers
  10 and holds — plus dark ice and the post-melt water source.
  Driving it surfaced a write-path stall worth its own note: every
  setSilentBlock recomputed the chunk's 256 column tops with
  per-cell reads (~98k shard ops, ~50ms/write) so a 4096-speed
  tick ran for half a minute. columnTop/chunkTops now read whole
  columns via FillBox and setSilentBlock skips the post-write
  scan when the write cannot move the top — ~50x faster writes.
- Random tick pass 10 (frosted ice + notify semantics):
  FrostedIceBlock is NOT random-ticking — its decay is a scheduled
  chain: onPlace arms 60-120 ticks; each tick slightly-melts (age
  bumps to 3 via flag-2 writes, then a liquid write) when
  nextInt(3)==0 or the cell keeps <4 frosted neighbours, gated on
  brightness > 11 - age - dampening. getLightDampening resolves to
  1 for ice/frosted (noOcclusion without propagatesSkylightDown),
  so the gate is really light >= 11 - age — a torch at taxicab 3
  (11) melts, at 4 (10) holds; plain ice's melt threshold is
  likewise 11, not 12. slightlyMelt's full melt cascades to frosted
  neighbours and re-arms the ones that only aged in 20-40.
  neighbourChanged melts a sparse cell (<2 frosted neighbours)
  outright when the notified block is frosted — and vanilla hands
  the callback the OLD state on gameplay writes (Level.setBlock
  reads the setBlockState return) but the NEW state on command
  writes (updateNeighboursOnBlockSet reads the cell post-write, and
  only setblock/fill/clone call it). A /setblock'd frosted pair in
  the dark collapses BOTH cells instantly: arrival melts the donor,
  its departure melts the writer. mc-go threads a notify block
  through neighborSurvival/checkSurvival: gameplay paths capture
  the pre-write state, runSetblock/runFill/clone pass the new one.
  TestFrostedIceParity (17 probes): sky/dark/torch-2/3/4 gate, the
  pair's instant double collapse, a 3-line departure chain (head
  and mid water, tail holds), midnight hold to noon melt.
  Dampening fix also applied to IceBlock's melt gate.
  Known nearby gap: IceBlock.playerDestroy melts to water on dig —
  mc-go digs to air today.
- Scheduled tick family: FIRE (FireBlock + BaseFireBlock + SoulFireBlock
  semantics, all javap-verified). onPlace pops unsupported writes
  instantly (old.is(this) skips, portal-lighting unmodeled — no portal
  blocks) and arms 30-40 ticks; soul_fire shares the survive check but
  never schedules. tick: reschedule -> canSpreadFireAround
  (fire_spread_radius_around_player rule, default 128, -1=always) ->
  !canSurvive removeBlock WITHOUT return (the removed fire still runs
  the whole tick — the age write resurrects it and onPlace re-pops;
  bytecode-confirmed) -> infiniburn from below via the dimension tag
  (overworld/nether {netherrack,magma}, end +bedrock) -> rain gate
  0.2+0.03*age gated on isRaining && isNearRain (pos + 4 horizontals),
  itself heightmap+biome-gated — the_void never precipitates ->
  newAge=min(15,age+nextInt(3)/2) flag-260 write -> !infiniburn:
  !validLocation pops when !sturdyBelow||age>3 (always returns);
  age==15 && nextInt(4)==0 && !canBurn(below) pops and RETURNS.
  checkBurnOut x6 (E W below above N S, 300 sides / 250 vert, +bonus
  -50 nether unmodeled): nextInt(chance)<burnOdds -> relight cell as
  fire[min(15,age+nextInt(5)/4)] when nextInt(age+10)<5 and dry, else
  air; TNT priming unmodeled. Spread box x,z in [-1,1], y in [-1,4],
  maxT=100+(dy-1)*100 for dy>1; target odds = max igniteOdds of the
  air cell's 6 neighbors (0 if occupied); spread=(odds+40+diff*7)/
  (age+30), halved in nether; skip when nextInt(maxT)>spread or rain
  at target; write getStateWithAge(min(15,age+nextInt(5)/4)) flag 3.
  Flammability table (firetable_gen.go) is the 175-entry javap dump —
  ignite/burn pairs; waterlogged->0. updateShape = canSurvive ?
  getStateWithAge(age) : air on every neighbor write. Fire odds key
  off bare names; wool/carpets are NOT flammable in 26.2.
  TestFireParity pins: instant pop on unsupported write, age-15 stone
  pop vs netherrack persist, soul-fire base on/off, deprive-pop,
  face-fold via later write, 8-cell grass burnout row, 32-cell spread
  band (2..31), and the void-biome rain pin (fires survive /weather
  rain — a correct isRainingAt must answer NONE there).
  Notable fix in passing: tick's early-outs now match the bytecode —
  the survive-pop does NOT return (dead fires still tick/spread and
  can resurrect via the age write), while the age-15 pop does.
- Random tick pass 11 (weathering copper): every WeatheringCopper*
  block class runs ChangeOverTimeBlock.changeOverTime — javap-verified:
  an outer nextFloat()<0.05688889 gate, then getNextState scans all
  positions within manhattan 4 (origin skipped): a less-oxidized
  weathering-copper neighbour aborts outright, otherwise
  ratio=(more+1)/(more+same+1) and the final roll is
  nextFloat()<ratio^2*chanceModifier with 0.75 on UNAFFECTED and 1.0
  past it. The write is setBlock(..., 2) carrying shared properties
  (Block.withPropertiesOf). 26.2 restructured the table into
  WeatheringCopperCollection — 15 families x 4 stages (block, cut,
  chiseled, slab, stairs, door, trapdoor, bars, grate, bulb, lantern,
  chest, golem statue, lightning rod, chain); waxed and oxidized
  blocks are not ChangeOverTimeBlock and neither tick nor count in the
  scan (so oxidized stays out of randRules, matching
  isRandomlyTicking). TestCopperOxidizeParity freezes both servers and
  uses /tick sprint for exact roll counts: lone-row advance, boosted
  (more-oxidized walls) advance, waxed+oxidized holds, prop retention
  across the transition (stairs facing/half, bulb lit), and the
  fresh-armed less-oxidized gate vs free controls in a 40-tick window.
- Scheduled tick pass (bubble columns): LiquidBlock.onPlace,
  neighbourChanged and updateShape(dir==DOWN) all arm a 20-tick block
  tick on a water cell that is a full source
  (shouldBubbleColumnOccupy: fluid in BUBBLE_COLUMN_CAN_OCCUPY = water,
  isSource, isFull) over soul_sand or magma_block; LiquidBlock.tick
  and BubbleColumnBlock.tick both run BubbleColumnBlock.updateColumn —
  derive the state from the cell below (soul_sand -> drag=false,
  magma_block -> drag=true, a bubble cell inherits, anything else
  writes plain water back) then setBlock(..., 2) that state into pos
  and keep writing it upward through every occupiable cell. A bubble
  cell's updateShape always arms a water fluid tick, plus its own
  5-tick block tick when !canSurvive (below not bubble/soul/magma),
  on any below-write, or when an above write opens an occupiable
  non-bubble cell — that is what grows the column into newly placed
  sources and what drains it to water when the base goes. The arm on
  the water cell fires on any neighbour write, not just below ones.
  mc-go wires it in survive.go (checkSurvival early-dispatch for water
  and bubble_column, writeOnPlace for the self-write) and blocktick.go
  (water -> bubbleLiquidTick, bubble_column -> bubbleColumnTick).
  TestBubbleColumnParity seals six shafts, freezes both servers and
  /tick sprints: soul_sand column -> drag=false, magma -> drag=true,
  base swap flips drag in place, base removal drains to water, a fresh
  source above a live column is pulled in at +5, lava and stone-based
  controls stay put. NOTE for the suite: /tick step on the real server
  does NOT process scheduled block ticks — always /tick sprint.

- Random tick pass 12 (speleothems): SpeleothemBlock.randomTick
  (0.011377778 gate on the topmost hanging cell only) +
  growStalactiteOrStalagmiteIfPossible — canGrow (dripstone: root is
  dripstone_block AND water one above it; sulfur_spike: sulfur), findTip
  (same-block same-direction walk, maxScan 7 dripstone / 2 sulfur),
  free-hanging non-waterlogged tip + canTipGrow (fluid-empty cell past
  the tip, or an opposite-facing unmerged tip -> merged tip_merge pair),
  then nextBoolean: grow the tip down vs growStalagmiteBelow (10-cell
  down-scan, fluids abort, dripstone also aborts on cells a drip can't
  pass). PointedDripstoneBlock.maybeTransferFluid rides a separate
  earlier float: hanging start cell, root found by walking up to the
  first non-same-block cell, fluid above it (mud reads as water);
  thresholds 0.17578125 water / 0.05859375 lava; mud -> clay on the
  spot, otherwise the first drippable cauldron within 10 below the tip
  gets a block tick at 50+distance; AbstractCauldronBlock.tick re-finds
  the tip and fluid, fills empty cauldrons to water_cauldron/
  lava_cauldron and increments water levels (<3). updateShape port:
  waterlogged cells re-arm their fluid tick, support-side writes that
  break survival arm +2 (stalactite) / +1 (stalagmite) block ticks —
  stalactites fall unconditionally, stalagmites re-check and break —
  and any other vertical write recomputes thickness (tip/tip_merge/
  frustum/middle/base ladder) silently. mc-go approximation: falling
  stalactites empty the cell (no FallingBlockEntity system).
  TestSpeleothemParity pins growth, the two never-grow controls, the
  cauldron fill, mud->clay, sulfur growth with no water, the cascade
  fall on root removal, and the stalagmite pop on floor removal.

- IceBlock.playerDestroy (dig melt, commit pending): a survival dig
  on ice or frosted_ice without #prevents_ice_melting (silk touch)
  writes a water source back over the broken cell when the block
  below is motion-blocking or liquid; over air it stays air, and in
  a WATER_EVAPORATES dimension removeBlock leaves it empty. Vanilla
  gates playerDestroy behind !player.preventsBlockDrops(), so the
  creative break path skips the melt entirely. FrostedIceBlock
  inherits the method — packed/blue ice are plain classes and never
  melt. Dig-side measurement that fell out of the harness: the
  scripted client's SyncPos echo carries onGround=false, so every
  tp'd player digs at the airborne 5x penalty until an explicit
  grounded move packet lands; and ice's hardness was missing from
  the dig table (defaulted to 1.0 — vanilla is 0.5, frosted/packed
  too, blue 2.8, none requires-tool). TestIceDigMeltParity: creative
  break stays air; survival ice-over-stone and frosted-over-stone
  become water[level=0]; ice-over-air, packed_ice, and silk-touch
  ice stay air; nether ice ends air.

- TNT (commit pending): TntBlock.useItemOn primes on
  flint_and_steel or fire_charge — ServerLevel + tnt_explodes gate,
  PrimedTnt entity spawns at (x+0.5, y, z+0.5) with fuse 80 and the
  ctor nudge vel=(-sin θ·0.02, 0.2, -cos θ·0.02), the block clears
  (flag 11 semantics), flint wears 1 / charge shrinks (creative
  exempt), ITEM_USED lands; gamerule-off shows the disabled overlay
  and leaves everything. onPlace/neighbourChanged re-check
  hasNeighborSignal and prime+remove — reachable today only through
  adjacent button/lever state via explodeHasNeighborSignal (no
  redstone engine). PrimedTnt.tick: gravity 0.04, move, air drag
  0.98, grounded bounce ×(0.7,-0.5,0.7), fuse--, at <=0 the entity
  discards and explodes at y+0.0625 with power 4.0. ServerExplosion
  reuses the measured 16³ shell-ray sampler now parameterized on the
  calculator: the TNT calculator resolves max(block, fluid 100)
  resistance per cell — generated blastResistance table from
  Blocks.<clinit> strength/instabreak/explosionResistance args plus
  the helper factories (log/leaves/piston/button/pot/stem/candle/
  shulker) and ofLegacyCopy/ofFullCopy chains — absent iff the cell
  is air holding no fluid. DESTROY_WITH_DECAY: shuffle positions,
  per-cell onExplosionHit drops loot with EXPLOSION_RADIUS set
  (survives_explosion = 1/radius — first real consumer of that loot
  param), writes air flag 3, calls wasExploded — TNT cells skip loot
  and re-prime with getRandomShortFuse nextInt(20)+10. hurtEntities
  damages every living entity in the 2r box
  ((impact²+impact)/2·7·radius+1 where impact=(1-dist/2r)·seen)
  and pushes along eye->centre at impact·(1-kbResist) with the +0.5
  upward bias; the explode packet carries the victim's knockback
  inline (spectator and flying creative excluded) and everyone within
  64 blocks gets the explosion_emitter particle + generic.explode
  sound. Explosion death type "explosion" (player_explosion for a
  player-ignited chain is a tracked gap). Also landed the measured
  packet-level gate: handleUseItemOn rejects clicks where
  eye->block-AABB distance² >= (block_interaction_range+1)² = 5.5² —
  the reach check vanilla runs before useItemOn ever dispatches.
  TestTNTParity pins priming (cell clears, entity spawns), blast
  destroys dirt and spawns item drops, obsidian holds at 1200 resist,
  player damage inside the predicted band via a scoreboarded Health
  probe, the TNT->TNT chain with short fuse, and tnt_explodes=false.
- Igniter items + portal lighting (commit pending):
  FlintAndSteelItem.useOn / FireChargeItem.useOn measured and
  implemented (internal/server/fireplace.go): the clicked block's
  light branch fires on #campfires|#candles && !waterlogged && !lit
  or #candle_cakes && !lit — playSound (flintandsteel.use 642 /
  firecharge.use 626, source BLOCKS, vanilla pitch formulas), the LIT
  write is flag 11 + gameevent, flint wears 1 / charge shrinks.
  Otherwise the held useOn targets clicked+face and gates on
  BaseFireBlock.canBePlacedAt — air cell && (getState.canSurvive ||
  isPortal) — then writes BaseFireBlock.getState (soul_fire over
  #soul_fire_base_blocks, else FireBlock.getStateForPlacement's
  direction-folded face props) with flag 11 + PLACED_BLOCK trigger +
  the same wear/consume. FAIL acks and does nothing. useTnt's
  gamerule-off path now returns false so the packet falls through to
  the igniter (vanilla PASS semantics). BaseFireBlock.onPlace: in a
  portal dimension (overworld|nether) a fresh fire runs
  PortalShape.findEmptyPortalShape on axis X (falling back to Z —
  vanilla's findPortalShape tries the opposite axis when the first
  misses) and createPortalBlocks fills the interior with
  nether_portal[axis] flag 18; survive-pop only when the cell is
  still fire afterward. PortalShape itself is the measured
  bottomLeft-sink + edge-scan + top-frame check (width 2..21, height
  3..21, frame=obsidian, interior air|#fire|nether_portal).
  FireBlock.checkBurnOut's tail primes an adjacent TntBlock — the
  prime carries its own tnt_explodes gate. TestFireIgniteParity pins
  all of it over the wire: plain fire, soul_fire on soul_sand, the
  direction-folded face prop, the no-survive fail, campfire/candle
  lighting, fire_charge, both portal axes (nether_portal[axis=x|z]
  inside the frame, obsidian intact), and the gamerule-off fire on
  TNT. Test harness note carved in code: vanilla's /fill refuses
  unloaded chunks and vanilla+mc-go both drop uses until
  player_loaded, so the suite teleports into the region and sends
  IDPlayPlayerLoadedSB before the first click.

- 2026-09-19: updateShape connection rewrites for the
  CrossCollisionBlock family — fences (#fences) and panes/bars
  (IronBarsBlock set: *_pane / *_bars). On every block write the
  neighbour pass (checkSurvival) now runs the updateShape half first:
  the block's prop toward the changed horizontal neighbour is
  rewritten to connectsTo/attachsTo, and a rewrite cascades
  setBlock-style until the connected run settles. FenceBlock joins a
  same-family fence (matching #wooden_fences membership — oak joins
  oak, nether_brick only nether_brick), a fence gate whose facing is
  perpendicular to the connection direction
  (connectsToDirection: gate.FACING.axis == dir.opposite.clockwise.axis),
  or a sturdy non-exception side; IronBarsBlock joins panes/bars,
  #walls members, or a sturdy non-exception side. The exception list
  is Block.isExceptionForConnection: #leaves, barrier, the pumpkin
  heads, melon/pumpkin, #shulker_boxes. Sturdy is the mc-go
  approximation (non-fluid, not washed-away, minus a name-level
  non-full-cube set in connectPartialFace) — same convention as
  fireSturdyBelow. Player placement folds all four props via the
  connectPlaceState getStateForPlacement half (plus waterlogged when
  placed in water); command writes keep the exact state and only
  neighbours update. TestConnectParity pins 18 legs: pair, sturdy,
  thin-face no-connect, leaves exception, wood-vs-nether material,
  both gate orientations, pane pair, pane-fence mutual no-connect,
  bars-to-wall, a broken middle run, and the placement fold.

- 2026-09-19 (cont): WallBlock joined the connection fold — the
  richer updateShape with none/low/tall sides plus the up post.
  sideUpdate keeps the unchanged directions' connect flags and only
  re-evaluates the changed one; topUpdate (dir UP) keeps all four and
  just re-folds; a change below is ignored. connectsTo reaches
  #walls, sturdy non-exception sides, IronBarsBlock (panes/bars), and
  perpendicular fence gates. makeWallState gives tall when the above
  block's bottom face covers that direction's test strip, else low;
  shouldRaisePost keeps up on wall-on-wall tops, lone/corner/T
  patterns, drops it on straight runs and flat-topped crosses unless
  the above block is #wall_post_override (torches, signs, banners,
  pressure plates, tripwire) or covers the centre post. The bottom-
  face coverage tests approximate isCovered's VoxelShape check by
  name: full cubes cover, bottom slabs/stairs and closed bottom
  trapdoors cover, the partial-face set doesn't. Placement folds all
  four sides + up + waterlogged; command writes place the exact
  state. TestConnectParity pins it: wall pairs with posts, the
  flattened middle of a three-run, tall sides + post under a solid
  block, torch-on-wall post override, and wall<->pane two-way attach.
  Stairs shapes done: StairBlock.getStairsShape fold (front→outer,
back→inner, perpendicular-axis + same-half + canTakeShape gates),
updateShape re-folds on horizontal change, placement folds
facing=player-dir + half (top-face→bottom, bottom-face→top, else
cursorY>0.5→top) + shape. Pinned by TestConnectParity (stair_* legs).

- 2026-09-19 (cont): Chest merge — ChestBlock.updateShape joins two
  chest halves. A neighbour becoming a non-single same-name,
  same-facing chest whose connected direction points back flips this
  chest to the opposite type (left<->right); losing the neighbour on
  its connected direction (left=cw(facing), right=ccw) drops it back
  to single — vertical neighbours never merge. Trapped chests merge
  only with trapped chests (chestCanConnectTo is same-block).
  Placement folds ChestBlock.getStateForPlacement: facing is the
  player's opposite, and a non-sneaking place pairs with a
  neighbouring single same-facing chest (cw-side partner -> left,
  ccw-side -> right). Sneaking on a horizontal face of a single chest
  merges with that clicked chest instead — the placed chest takes the
  partner's facing and right/left by ccw(partner) ==
  face.opposite. The same slice adds vanilla's isSecondaryUseActive
  gate to handleUseItem: sneaking with any non-empty stack skips the
  whole block-use dispatch (TNT prime, bed, noteblock, sign, menus) —
  the held item's useOn still runs, which is what lets sneak-clicks
  place blocks on containers. Sneak now round-trips through
  player_input bit 0x20. TestConnectParity pins 10 chest legs: facing
  fold, placed pair (new=left/old=right), partner break, setblock
  stays single, trapped no-merge both ways, sneak-merge.

- 2026-09-19 (cont): updateShape fold family #2 — every remaining
  updateShape-side prop fold measured on the jar and pinned by
  TestNeighborFoldParity (27 legs, both servers). SnowyBlock: snowy
  refolds on the cell above (#snow = powder_snow/snow/snow_block;
  grass/mycelium/podzol only). ChorusPlantBlock: per-direction fold
  on any of 6 neighbours (plant/flower, down also counts
  #supports_chorus_plant). ScaffoldingBlock: write+neighbour changes
  schedule the 1-tick getDistance/bottom refold; distance 7 with
  old-prop 7 becomes a falling block that re-lands on the first
  sturdy cell below, a newly-arrived 7 pops with drop+2001.
  CoralBlock: no water-fluid neighbour in any of 6 faces (waterlogged
  counts) schedules the 60+rng(40) die tick → dead_ variant keeping
  facing/waterlogged. ConcretePowderBlock: touchesLiquid solidifies
  to the _concrete colour — five faces only; DOWN is skipped unless
  the powder itself holds water, so water below never sets it off.
  TripWireBlock refolds the changed side toward wires and back-facing
  hooks; TripWireHookBlock pops (drop+2001) when its facing.opposite
  anchor breaks, and calculateState walks up to 42 cells — a
  contiguous wire run ending on an opposite-facing hook sets
  attached + powered on both ends.
  Also landed the missing half of BlockInput.place: command writes
  (setblock/fill) run updateFromNeighbourShapes — the written block
  folds itself against all six neighbours before landing, and an air
  fold is discarded (a floating tripwire_hook stays placed). mc-go
  selfFoldShapes covers setblock + fill.

- 2026-09-19 (cont): MultifaceBlock — sculk_vein/glow_lichen/
  resin_clump. updateShape: a state with no faces pops; a changed
  neighbour on a set face's direction removes it when canAttachTo
  (the attach cell's face back toward the vein is full) fails —
  removeFace itself returns air when the last face drops, and
  updateOrDestroy turns that air into destroyBlock (pop effect, no
  item drop without shears). Placement iterates
  getNearestLookingDirections (clicked face's attach side, then yaw
  order, then pitch-ordered verticals) and sets the first attachable
  face — folding onto the existing vein's state when the cell already
  has one. Command writes get BlockInput.place's updateFromNeighbourShapes
  self-fold with the air-fold discard, so a floating
  `setblock vein[up=true]` keeps its state.
  TestNeighborFoldParity pins 5 vein legs (32 total).

- 2026-09-19 (cont): SnowLayerBlock.randomTick — block light
  strictly above 11 (torch dist 2 melts, dist 3 holds; one notch
  stricter than ice's >10) drops the snowballs and removes the
  layer. snow_block never ticks. RedStoneOreBlock.randomTick — a
  lit ore (redstone_ore + deepslate variant) settles lit=false.
  TestSnowMeltParity (8 legs) + TestRedstoneOreUnlitParity (5 legs)
  pin both against vanilla.

- 2026-09-19 (cont): Entity.checkInsideBlocks landed as
  entityInsideStep — per entity per tick, cells under its box run the
  block's inside effect (a curated width x height table; 0.6x1.8
  default; spectator players never trigger). TripWireBlock's pair:
  entityInside powers an unpowered wire with no pending recheck;
  checkPressed flips powered (flags 3), runs updateSource — south and
  west walks only, recalcing the hook that faces back at the run —
  and arms a 10-tick recheck that releases when the cell empties.
  Wire shape is a 2.5px column attached / 8px loose, so a standing
  entity only presses a wire at its feet cell.
  Found while measuring: vanilla calculateState runs from wire
  onPlace/affectNeighborsAfterRemoval (updateSource) and hook
  setPlacedBy/tick — a setblock'd hook NEVER recalcs, so mc-go's
  every-write hook recalc over-attached; moved to the placement path.
  TestTripwirePoweredParity pins press/release/hooks; a killed pig's
  dropped porkchops keep the wire pressed on vanilla (items press).

- 2026-09-19 (cont): Scoreboard criteria feeds — stat awards write the
  stat's new total into every objective tracking
  minecraft.<type>:minecraft.<value>, plus the custom-stat fixed
  criteria (deathCount←deaths, playerKillCount←player_kills,
  totalKillCount←player_kills+mob_kills). Live criteria
  (health/xp/level/food/air/armor) are event-driven, not ticked: each
  baselines its live value at objective creation and writes only on
  real change — health/food feed from sendSetHealth, xp/level from
  sendSetExperience, armor from equipment writes. The six are
  read-only: players set/add/remove/operation reply
  "Scoreboard objective '%s' is read-only" (reset works). /xp gained
  the add form and set-points now sets (was +=). Measured: fresh
  live-criteria objectives stay unset on vanilla (no polling), and
  /damage does not feed zh on either server.
  TestScoreboardCriteriaParity pins 15 legs.

- 2026-09-19 (cont): Frost Walker — the 26.2 data-driven
  location_changed effect (replace_disk in the enchantment json):
  radius clamp(3+(level-1),0,16), height-1 layer at feet-1, disk test
  distToCenterSqr(entity pos) < r^2, predicate air-above + water block
  + unobstructed. Gated is_on_ground and not-a-passenger, and a
  same-position resend does not retrigger it (measured — vanilla runs
  it off the entity tick's change detection, not raw packet arrival).
  mc-go hooks the move handler post-setPos on a real delta. Frozen
  cells write frosted_ice[age=0] through setNaturalBlock so the
  existing onPlace tick scheduling and neighbor-collapse melt run
  unchanged. TestFrostWalkerParity pins 9 legs: bare/airborne/off-boots
  misses, r3 boundary cells (dz 2 in, 3 exactly out — strict <), the
  same-pos non-trigger, and the level-2 r4 flip of the boundary cell.

- 2026-09-19 (cont): Precipitation — ServerLevel.tickPrecipitation:
  one 1/48 roll per random-tick-speed iteration per ticking chunk picks
  a random column; the MOTION_BLOCKING surface decides — the surface
  cell freezes to ice in cold biomes (NOT rain-gated), and at the free
  cell above, snow lands/piles while raining when the biome snows and
  max_snow_accumulation_height>0, capping at min(rule,8) layers.
  fillbiome became real: quart-keyed per-dimension biome overrides feed
  a biomeAt() reader now used by precipitation, fire's rain check, and
  execute if biome. The world layer gained the live MOTION_BLOCKING
  heightmap vanilla maintains: shards cache each column's top and drop
  it on writes at-or-above the surface (fluid and 8-layer snow count
  as blocking). Without it, one column FillBox per roll starved the
  tick loop at speed 4096 (~24k shard round-trips per tick) and snow
  never piled. TestPrecipitationParity pins 9 legs incl. the cold-vs-
  void control and layers=8 saturation.

- 2026-09-19 (cont): Cauldron precipitation — tickPrecipitation's third
  leg, on pos1 (the surface cell): gated by isRaining + the biome's
  getPrecipitationAt != NONE (rain biomes count too). Empty cauldron
  fills to water (RAIN, 5% roll) or powder_snow (SNOW, 10%); a layered
  cauldron whose precipitationType matches gains a level to 3. The
  biome cold checks are now table-driven from a generated climate table
  (66 biomes, temperature + has_precipitation, tools from the datapack
  jsons) with the height-adjusted temperature rule (seaLevel+17
  boundary, -0.00125/block; the +-8-block noise term is dropped — it
  shifts the boundary under 0.01 degrees). Freeze now also requires
  source water (level 0). Column surface reads go through a
  shard-cached MOTION_BLOCKING top that updates in place on writes
  (raises on blocking writes above, rescans only when the surface cell
  unblocks) — without it the per-roll column scan starved the tick
  loop at speed 4096 and marginal legs flaked. TestPrecipitationParity
  pins 11 legs incl. both cauldron fills, the plains-vs-snowy split,
  and layers=8 saturation.

- 2026-09-23: Enchanted damage model + mob armor attribute. Melee hits
  fold the weapon's damage-component enchants in scaled linearly by
  the attack charge after the crit multiply (sharpness 1+0.5·(l-1)
  unconditional; smite/bane/impaling 2.5·l vs undead/arthropod/
  aquatic). The victim's protection EPF (protection 1/l, typed 2/l,
  feather_falling 3/l, cap 20, skipped only for bypasses_enchantments)
  shaves epf/25 post-armor; frost_walker boots negate
  burn_from_stepping outright. Mobs now carry their vanilla ARMOR
  attribute — the zombie family's natural 2 and the wither's 4, plus
  worn armor pieces — and armor absorbs inside the same grace-window
  pipeline as players (absorb applies to the post-delta amount);
  Manager.Damage takes the armor value so every path (melee, sweep,
  spear, arrow, lightning, /damage, TNT, /kill) routes one formula.
  Turtle became summonable (health 30, bounds 1.2x0.4).
  TestEnchantDamageParity pins 10 legs vs vanilla — sharpness/smite/
  bane/impaling hits on players + armored mobs, feather_falling/
  protection EPF, and the frost_walker immunity — with natural regen
  switched off so the health timeline is only the scripted hits.

- 2026-09-23: Combat enchants round 2 — knockback, fire_aspect,
  sweeping_edge. The enchant's knockback level halves into the extra
  knockback strength (attr + l/2, plus 0.5 while sprinting) and pushes
  along the attacker's yaw facing without needing the sprint gate;
  both the hurt push (0.4 away-from-attacker) and the extra compose
  through knockback()'s halve-then-subtract rule so the emitted
  LpVec3 is byte-identical to vanilla's. fire_aspect ignites the
  victim 80·l ticks via post_attack: Entity.FireTicks/Session
  .fireTicks burn 1 damage at each multiple-of-twenty tick through
  the same armor+EPF pipeline (on_fire type), the shared entity-data
  flag bit 0x01 broadcasts while burning, and mobs marked fire-immune
  (nether natives) never ignite. sweeping_edge adds l/(l+1) of the
  pre-enchant attack damage to the sweep's 1.0 base and re-folds each
  neighbor's own enchant damage bonus per victim. Vanilla's own
  resend of the victim's set_entity_motion over ~3 ticks is captured
  but only the tail payload is compared (the resend count is
  resync noise). TestEnchantDamageParity now pins 15 legs — the new
  kb2 motion bytes, the 8×1 burn chain, and the 3-pig sweep spread —
  all byte-identical. Harness fix: a background pump answers W's
  keep_alives and buffers its packets so long mob legs no longer let
  vanilla kick the idle victim. Thorns (15%·l rng proc), unbreaking,
  and looting still open.

- **Loot/XP context gates**: mc-go now models vanilla's
  dropAllDeathLoot rules measured from the 26.2 jar +
  datapack: drops roll on any death when shouldDropLoot
  (!baby && mob_drops) — no player needed, so /kill still
  drops — while XP orbs additionally require the
  lastHurtByPlayerMemoryTime window (100 ticks after any
  player-caused hurt, replumbed as e.LastHurtByPlayerTicks
  set by every damageMob caller that knows the session).
  killed_by_player pools (spider_eye, zombie rares,
  drowned copper, witch spread, blaze_rod) only open inside
  the window; furnace_smelt swaps to the cooked item when
  the victim burns or the killer's mainhand carries
  #smelts_loot (fire_aspect); enchanted_count_increase adds
  round(uniform[0,1]*level). mobLoot tables transcribed for
  15 mobs (pig..iron_golem incl. sheep's wool, wither skull,
  stray tipped_arrow). TestLootParity pins 8 legs
  byte-identical on both servers: kill-pig drops no XP,
  melee-pig drops + XP, baby pig nothing, fire_aspect cooks,
  /kill zombie x6 no rares, /kill spider no eyes, looting3
  reaches >=4 stacks in 10 pigs, mob_drops=false drops
  nothing. Still open inside looting's model: item quality
  per-level weights on multi-entry pools, container drops.

- **Thorns + unbreaking (post_attack / item_damage enchantment
  passes)**: measured off the 26.2 datapack + jar — thorns is a
  per-equipment-piece roll (random_chance linear 0.15+0.15/lvl) that
  deals the attacker uniform[1,5) thorns damage with the victim as
  the damage-source entity and wears the procced piece a flat 2;
  unbreaking is RemoveBinomial — every durability point is removed
  independently with p = l/(l+1) on tools, 2l/(5l+5) on armor
  (#enchantable/armor). mc-go: damageWith tails into
  thornsVictimWear over hotbar+offhand+armor slots (procced piece
  also runs wearSlot 2 through unbreaking), damageMob runs
  thornsMobWear over e.Equips with the same roll, and wearSlot /
  armorWear now route durability through unbreakingReduce. Mob
  equipment now serializes into /data get entity NBT (vanilla's
  equipment map: mainhand/offhand/feet/legs/chest/head + saddle)
  and item stacks emit the damage + enchantments components.
  TestThornsUnbreakingParity pins 6 legs on both servers — thorns
  proc on a mob victim (absorb drop + chest damage exactly 16 =
  8 hits x flat-2 at thorns10, which forces every roll), thorns on
  a player victim (zombie dies to the reflect, A's chestplate wears
  hits+2/proc), unbreaking3 weapon stays in the binomial(30,0.25)
  band while a plain sword wears exactly 30, and armor likewise
  (binomial(200,0.7) bound vs exactly 200). Still open: the other
  post_attack effects (channeling is hit_effect, not post), mob
  bodies (horse armor slot), and the >128-unit normal-approximation
  branch vanilla switches to inside RemoveBinomial.

- **Armor attributes, hurt knockback, and the no_knockback gate
  (player side)**: re-measured CombatRules.getDamageAfterAbsorb off
  the 26.2 jar — the real formula is
  `defense = clamp(armor - dmg/(2+toughness/4), armor*0.2, 20)` with
  `damage*(1-defense/25)`, so armor_toughness shrinks the defense
  the hit removes rather than adding reduction; hurtArmor now feeds
  it `(armorValue, armorToughness)`. mc-go learned armorToughness
  (diamond 2/piece, netherite 3) and armorKBResist (netherite 0.1/
  piece) summed over the four armor slots, and dealDefaultKnockback
  was wired in for entity-sourced hurts: `damage A N <type> by E`
  and mob melee shove the victim 0.4 away from the source, folded by
  the victim's knockback_resistance and gated by the damage-type
  `no_knockback` tag (`generic` is in it, `mob_attack` is not).
  player_attack skips that push — the attack call site applies its
  own 0.4 (plus knockback-ench/2 and the 0.5 sprint bonus along the
  attacker's facing) — and sweep victims take hurt-kb + facing push.
  Two wire-parity corrections fell out of the blackbox probing:
  join_game now ships the player's real entity id (it was hardcoded
  1, so trackers' add_entity never matched), and the hurt-motion
  packet goes to the victim as well as its trackers (vanilla sends
  the delta to both). Mobs got mobHurtKnockback the same way, with
  per-type knockback_resistance (iron_golem 1.0). TestArmorAttrParity
  pins it all on both servers: health drop for a 20-point mob_attack
  is 8.0 diamond / 7.2 netherite / 16.0 iron / 18.88 leather
  byte-for-byte, a 4-point mob_attack by an armor stand shoves the
  victim ~0.4 (0.24 through netherite), generic moves nobody, and a
  real zombie melee registers. Still open: armor_breach —
  getDamageAfterAbsorb has an optional modifyArmorEffectiveness pass
  when the source carries a weapon — and the victim's vy component
  follows the client's on_ground flag (mc-go) vs server physics
  (vanilla), which the scripted client never asserts.

- **Instant effects and the MobEffectInstance update/hidden chain**:
  /effect give of instant_health / instant_damage now applies
  (HealOrHarmMobEffect.onEffectStarted — heals 4<<amplifier clamped
  to max health, hurts 6<<amplifier as magic, amplifier shifts wrap
  like Java int math). The deeper parity was in update(): a same
  amplifier re-give only succeeds when the new duration is
  longer-or-equal to the remaining (inclusive — vanilla's
  isShorterDurationThan is `<=`), a strictly stronger instance
  replaces outright and — when shorter — pushes the old instance
  into the new one's hiddenEffect chain, a weaker-but-longer give
  fails the command yet still stashes itself in that chain, and the
  hidden instance keeps decaying and RESUMES on the stronger one's
  expiry (downgradeToHiddenEffect emits an Update Mob Effect with
  the lower amplifier). onEffectStarted — where the instant apply
  lives — fires even on a declined store, so a failed re-give of
  instant_health still heals while replying give.failed.
  mobEffectInst carries hide+hidden; mobEffectTickStep downgrades
  instead of removing when a hidden is pending. TestInstantEffectParity
  pins health byte-equal across a 6-leg sequence plus the amp
  downgrade on the wire. Harness fix: client() sets a 10s absolute
  deadline covering writes — cleared once play state is entered
  (joinPlayBlackbox) so long tests don't hit i/o timeouts. Still
  open: entity NBT `active_effects` on players (vanilla answers
  `data get entity P active_effects`, mc-go returns
  nothing_found) and mob targets for /effect (undead inversion).

- **Entity NBT `active_effects`**: the save compound now carries
  live status effects on both players (playerSaveMap) and mobs
  (entitySaveMap), serialized the way MobEffectInstance's codec
  writes them — id, duration always (-1 = infinite), amplifier
  omitted at 0 (so `active_effects[0].amplifier` on an amp-0
  instance replies nothing_found), show_particles only when hidden,
  show_icon, and a nested hidden_effect when an instance is queued.
  The indexed-path query and the nothing_found replies are pinned
  on both servers by TestInstantEffectParity.

- **/effect on world entities**: `effect give`/`effect clear` now
  resolve LivingEntity targets (mobs + players) via the shared
  selector union — the same update()/hiddenEffect gate runs on
  Entity.Effects, instant arms route through the undead
  inverted_healing_and_harm tag (heal 4<<amp / hurt 6<<amp swap on
  undead), and trackers see the vanilla surface: entity-data particle
  resends + update_attributes carrying the effect.speed modifier
  (0.2*(amplifier+1), add_multiplied_total). Vanilla sends
  update_mob_effect/remove_mob_effect only to passengers — mc-go now
  drops the tracker broadcast for mobs. Duration fix from EffectCommands
  bytecode: instantaneous effects take the duration arg raw (ticks,
  not seconds) and default to a single tick, so stored instants expire
  after one tick instead of lingering. mobEffectTickStep's world-entity
  arm expires/downgrades mob instances like tickEffects. Fixed a real
  wire bug found mid-pin: mobSyncAttrs had dropped the attribute-count
  VarInt. TestEffectMobParity pins all of it byte-equal on both
  servers (undead + normal instant arms, modifier amounts across
  give/downgrade/clear, zero effect packets to trackers,
  clear.everything.failed on an empty set).

- **Mob equipment.body**: the wolf/horse BODY slot now round-trips —
  summon NBT already read it into BodyItem; the save compound emits it
  (equipment.body after head, before saddle — EquipmentSlot order) and
  the spawn/attach set_equipment packet carries wire slot 6.
  TestEquipBodyParity pins a wolf+wolf_armor summon: slot-6 stack on
  the wire plus `equipment.body[.id]` data queries on both servers.
  Still open: /item entity on mobs (player-session-only slots today),
  wolf_armor's absorb-damage behavior, and un-summonable mob types
  (mobHealth table coverage, e.g. horse).

- **/item on world entities**: `item replace|modify ... entity` now
  resolves any LivingEntity through the shared selector path, not just
  sessions. Mob slots are the six equipment entries (weapon.*, armor.*),
  armor.body, and saddle — all unfiltered (wolf_armor on a husk, a
  saddle on one too), while container/crafting/cursor names and
  non-living entities fail no_changed. Selectors that hit several
  entities aggregate into set.success.multiple; `from` copies between
  mobs, and the stack cap is a parse-time error ahead of any slot
  check. Two supporting fixes fell out: the SNBT pretty printer quotes
  compound keys outside [0-9a-zA-Z_.+-] the way vanilla does
  ("minecraft:damage" inside components), and a summoned item entity
  names its stack, not entity.minecraft.item. TestItemMobParity diffs
  a 27-leg transcript verbatim on both servers. Still open: worn gear
  taking durability on hurt (the husk keeps legs deterministic), the
  per-slot delta vanilla's broadcastEquipmentUpdates sends (mc-go
  resends the whole set), and horse-style chest/container mob slots.

- **Mob melee equipment wear — measured, deliberately absent**: the
  earlier "a meleeing zombie's sword accrues damage" theory was wrong.
  Vanilla's weapon wear lives in ItemStack.postHurtEnemy (hurtAndBreak
  of weapon.item_damage_per_attack), which only Player.attack calls;
  Mob.doHurtTarget invokes hurtEnemy, an empty Item hook (MaceItem's
  smash override excepted). LivingEntity.hurtArmor/hurtHelmet are
  likewise empty — only Player.hurtArmor/helmet call doHurtEquipment —
  so a mob's equipment is NEVER worn by combat, attacking or hurt.
  mc-go stays wear-free there. TestMobWeaponWearParity pins it: a
  husk landing hits on an armored survival player keeps
  equipment.mainhand verbatim-pristine on both servers while the
  target's health drops (the hits-landed anchor).

- **Mob BODY-slot armor — wolf absorb shield + hurtArmor wear**: the
  earlier "hurtArmor only Player" claim was incomplete — Wolf and
  Horse (literal Horse, not AbstractHorse) also override it, for the
  BODY slot only. Wolf.actuallyHurt first runs its full-absorb
  shield: when the body item is minecraft:wolf_armor and the damage
  type is NOT in bypasses_wolf_armor (invulnerability pair + cramming,
  drown, dry_out, freeze, in_wall, indirect_magic, magic,
  outside_border, starve, thorns, wither), the wolf takes 0 damage and
  the armor eats hurtAndBreak(ceil(raw)) — measured 8+12 hits wore
  exactly 20. Bypass types fall to the normal path: body armor enters
  reduceDamage (wolf_armor = 11 armor) AND doHurtEquipment wears
  max(1, raw/4) — gated on equippable.damage_on_hurt (wolf armor yes,
  all horse/nautilus armor NO), isDamageableItem, and canBeHurtBy.
  mc-go implements all of it inside Manager.Damage: wolf absorb
  branch, wolf/horse body-slot wear, armor+toughness attribute credit
  from BodyItem (armorPoints/armorToughness extended for the 13 horse/
  nautilus materials), and the new body-equipment-break entity event
  65. TestMobBodyArmorParity diffs a 7-leg transcript (wolf absorb,
  horse /4 wear = 1 on diamond, thorns bypass, Health legs)
  verbatim on both servers. Two supporting fixes: SNBT floats now
  print shortest-round-trip at tag width (94.88 not
  94.87999725341797 — javaFloatStr32), and "horse" joined mobHealth
  (vanilla rolls 15-30 at spawn; mc-go fixes 25, so its Health leg is
  excluded). Still open: WOLF_ARMOR_CRACK sound + armadillo_scute
  particle burst + WOLF_ARMOR_DAMAGE hurt-sound swap on crackiness
  transitions, and knockback/last-hurt bookkeeping on absorb hits
  (mc-go returns early before the retaliation hooks).

- **Wolf-armor wire feedback — crack transition, hurt-sound swap,
  damage_event ordering**: pinned by packet diff, not just data.
  Vanilla's wire order on an absorbed hit is [equipment-break event 65
  → item.wolf_armor.crack → armadillo_scute ITEM particle burst →
  damage_event → hurt sound]: the break fires inside hurtAndBreak
  (before the crackiness compare), and playHurtSound runs in hurt()
  AFTER hurtServer — so the breaking hit already reads an empty body
  slot and plays entity.wolf.hurt, not item.wolf_armor.damage.
  Crackiness is Crackiness.WOLF_ARMOR on the remaining-durability
  fraction (LOW below 0.95, MEDIUM below 0.69, HIGH below 0.32; a
  break counts as -> NONE). The ITEM particle's option is an
  ItemStackTemplate (VarInt item, VarInt count, patch), not the slot
  codec. mc-go's Damage now returns a DamageResult (Exists/Graced/
  BodyBroke/Absorbed/CrackChanged), damageMob gained the damageInfo
  param so damage_event lands at the measured point, and the absorb
  path no longer short-circuits the hurt tail (killed_by_player
  window + thorns retaliation now run on absorbs, matching hurtServer
  accepting). Every damageMob caller's ad-hoc broadcastDamageEvent
  folded in — including sites that wrongly gated on dealt>0 or
  emitted nothing (thorns retaliation, /kill, on_fire, magic
  harming). mobHurtSound currently covers the wolf only; other mobs'
  hurt/death/ambient sound tables remain open.

- **damages_helmet player path**: hurtServer's helmet branch — when
  the damage type is in the damages_helmet tag (falling_anvil,
  falling_block, falling_stalactite) and the head slot is nonempty,
  Player.hurtHelmet runs doHurtEquipment on HEAD (max(1, raw/4) wear
  on the post-grace amount) and then multiplies the hit by 0.75
  BEFORE getDamageAfterArmorAbsorb — and hurtArmor still walks all
  four armor slots, so the helmet wears twice on those hits (verified
  verbatim: 8-anvil on a leather helmet lands damage 3, not 2). The
  pre-break armor value is the post-wear one: the breaking hit
  reduces through zero armor but still gets the 25%. armorWear was
  factored into a slot-scoped doHurtEquipment so armorWear and
  hurtHelmet share the walk. TestHelmetWearParity diffs a 15-leg
  transcript (damage path, equipment.head, Health) on both servers —
  including the break leg, where nothing_found must match.

- breach/armor_effectiveness + attribute armor (2026-09): `/damage ... by`
  resolves the breach level on the direct entity's mainhand
  (`DamageSource.getWeaponItem`) — a breach-4 mace drops an armor-20
  player's protection from 0.7 to 0.1 (dealt 4.5 vs plain 1.5) and
  zeroes a chestplated mob's defense. armor/armor_toughness now run
  through the attribute pipeline on both victims: `sess.sessAttr`
  folds the four worn pieces (plus bodyArmor) into the attribute as
  synthetic add_value modifiers so `/attribute base set` and equipment
  compose, and `mobArmor`/`mobToughness` do the same over
  `e.AttrBase`/`e.AttrMods`, clamped to the attribute ranges
  (armor 0..30, toughness 0..20). New `TestBreachParity` — husk with a
  plain vs breach-4 mace against an attribute-armored player and a
  chestplated creeper (creeper chosen because zombies spawn
  reinforcements on hurt and skeletons burn in daylight).

- **Mob hurt/death voices — the full summonable table**: every
  summonable type's `playHurtSound`/`getDeathSound` voice now goes out
  as a positional `sound` packet (0x75) at the entity's feet — NOT
  `sound_entity` (0x74); the wolf-armor work had lumped both packet
  ids so the wrong packet family hid behind the merge. Measured per
  type live: hurt/death registry ids (witch hurts carry a random
  extra `entity.witch.drink` from the potion mechanic — filtered as
  nondeterministic), getSoundVolume (cow/wolf/allay/sulfur_cube 0.4,
  horse 0.8, ghast/dragon 5, rest 1), and the SoundSource — HOSTILE
  for Enemy types, NEUTRAL otherwise. `hurt()`'s dying arm runs
  `makeSound(getDeathSound())` + `playSecondaryHurtSound` (iron_golem
  adds `iron_golem.damage`) then `die()` — a lethal hit plays NO hurt
  sound — and the sounds land AFTER `damage_event` on the wire.
  Armor stands and the ender dragon (part-based damage, custom death
  sequence) are silent both paths. The crack sound in the wolf-armor
  feedback path was also positional — moved off playSoundEntity.
  New `TestMobHurtDeathSoundParity` sweeps 38 types × (damage, kill)
  comparing (packet, sound id, source, x, y, z, volume) tuples.
  Left out of the sweep: enderman (teleports out of earshot), the
  fish + sulfur cube (measured ids in the table, but the fixture
  can't hold them reliably — fish despawn inside the leg window and
  the cube sometimes summons at another size). Still open: ambient
  voices, per-damage-type hurt variants (drowning/fall overrides),
  baby-pitch voicePitch.

- **Player hurt/death voices**: the player victim path now emits the
  positional voice too — `entity.player.hurt` (or the drown/freeze/
  on_fire/sweet_berry_bush variant by damage type) at the victim's
  feet on the PLAYERS source, and `entity.player.death` on a lethal
  hit. Crucially the packet goes to every listener EXCEPT the victim:
  vanilla excludes the source so the victim's client plays the hurt
  locally off the damage_event — a lone listener hears a nearby
  player's hurt, the victim hears nothing over the wire. New
  `TestPlayerHurtSoundParity` diffs 13 damage types + a kill through
  a second-session listener, asserting victim silence both sides.

- **/data merge Health semantics**: the entity reload now clamps a
  merged Health to [0, effective max_health] — {Health:100f} on a
  wolf lands at 8, on a husk at 20 — and a merge to zero marks the
  mob Dying so the corpse lingers like a combat death. The tameable
  quirk is pinned too: a wolf merge re-asserts the wild max_health
  base (TamableAnimal.setTame(false) on load), undoing an
  /attribute base override before the clamp. Player targets still
  fail commands.data.entity.invalid. New TestDataMergeHealthParity.
- **/kill corpse linger**: kill on a living mob no longer removes the
  entity instantly — die() marks it Dying and the tick pass flushes
  the dying metadata pack and poofs it twenty ticks later, matching
  vanilla's hurt(Float.MAX)→die() flow (fixes a TestDataEntityParity
  regression where the 0x63 dying pack was never emitted).

- **Per-slot equipment deltas**: world-entity equipment writes now go
  through a dirty-slot set (EquipDelta) instead of the whole-set
  EquipDirty flag — an /item write on a mob emits set_equipment with
  only the changed slot, a no-op write emits nothing at all, and a
  cleared slot rides as an empty stack. The summon-NBT burst still
  replays whole once after attach. Wolf-armor wear marks the body
  slot (6). TestEquipDeltaParity diffs six write/clear/noop legs on
  a husk, slot bytes + counts + item ids byte-for-byte. Still open:
  the `attributes` save-list (data get attributes on a mob is a
  packed supplier list — needs the per-type supplier table; NoAI
  strips follow_range/attack_damage entries on some types, and
  tameables re-assert the wild max_health base on merge — wolf done).

- **Guardian-family voices + the audible-range gate**: guardians and
  elder guardians are summonable now (30/80 health, 0.85/1.9975
  boxes) and carry the full isInWater voice split — hurt/death on
  dry land ship the *_land registry ids (774/771, 580/577), in water
  the base ids (773/770, 579/576). The bigger fix underneath: both
  playSound paths now gate on vanilla's sendToAllNearExcept rule —
  a sound packet only ships to listeners strictly within
  16*max(1,volume) blocks of the position; before, every session in
  the dimension got every sound. Also fixed a double death voice on
  /kill (leftover call next to the mobDie extraction).
  TestGuardianWaterVoiceParity teleports a spectator to each mob
  (the range gate itself is what makes distant-mob legs silent) and
  diffs land/water hurt+kill legs byte-for-byte. Still open:
  ambient voices (random cadence — measured 573/574 for elders but
  not pin-able per-tick), guardian flop, the beam attack sound.

  Known-failing: TestCritSweepParity's weak leg diverges (vanilla
  deals ~2.08 to weakB with no damage_event packet seen, mc-go ~1.77
  with the event) and mc-go entity ids run +1 vs vanilla's (counter
  starts at 2 — "entity 1 is the player" assumed a single player).
  Failed identically before and after the sound-range change —
  pre-existing, needs a mid-recharge swing-formula measurement slice.

## Slice — held-shears mob shear + interact range gate

- `handleInteract` now applies vanilla's
  `isWithinEntityInteractionRange` gate: the interact is dropped when
  the eye→entity-box distance exceeds `entity_interaction_range`
  (base 3.0) + 3.0 — measured live: vanilla silently ignores shears
  clicks past ~6 blocks to the box.
- `shearAnimal` falls through to `shearMob` when there's no shearable
  BODY equipment — `ShearsItem.interactLivingEntity` →
  `Shearable.shear` on all four shearable mobs: sheep wool + Sheared
  flag, snow golem pumpkin pop, bogged mushrooms + `sheared` flag,
  mooshroom `convertTo(COW)` + 5 red mushrooms — all wearing the
  shears by 1 and counting `item_used`.
- New `TestShearMobParity` tps beside each summoned mob and sends a
  raw Interact packet — all nine probes hit/hit.

## Slice — dispenser shear cascade (bogged + mooshroom)

- `DispenseItemBehavior` shears now shear the remaining Shearable
  mobs at the mouth:
  - `bogged` — `Bogged.shear` drops `BOGGED_SHEAR` (2 rolls × {1
    brown + 1 red mushroom}) and sets `DATA_SHEARED` — measured at
    entity-data index 16 as a *boolean* (NBT key `sheared`);
  - `mooshroom` — `MushroomCow.shear` runs `convertTo(COW)`: the
    mooshroom despawns, a cow spawns at the same spot (inheriting
    NoAI), and the red-variant shearing table drops 5
    `red_mushroom`s; shears take 1 wear.
- Pinned by `TestDispenseBehaviorParity` probes — drops,
  `nbt={sheared:1b}`, cow-spawned/mooshroom-gone — hit/hit vs
  vanilla.

## Slice — dispenser shear cascade (snow golem)

- `DispenseItemBehavior` shears now shear a `snow_golem` at the mouth:
  `SnowGolem.shear` pops its carved pumpkin (`SHEAR_SNOW_GOLEM` = 1×
  `carved_pumpkin` dropped at the entity) and clears the pumpkin flag;
  the shears take 1 durability.
- New `Entity.Pumpkin` flag models `SnowGolem`'s `DATA_PUMPKIN_ID` —
  live-measured as entity-data index 16 (byte, bit `1` = head on). It
  packs only when cleared (vanilla default 1 doesn't ship at spawn),
  in both the attach burst and `data merge {Pumpkin:0b}` dirty packs;
  `/data get` round-trips `Pumpkin` via SnowGolem's save key.
- Pinned by `TestDispenseBehaviorParity` probes (item drop +
  `nbt={Pumpkin:0b}` selector) — hit/hit vs vanilla.

## Slice 2026-09-19d — `attributes` save-pack (touched-instance set)

`data get entity X attributes` now emits the lazy AttributeMap's
touched set, matching vanilla's pack semantics measured on 26.2:

- Every /summon touches `movement_speed`; tamables (wolf) also touch
  `max_health` via the setTame side effect.
- `Mob.finalizeSpawn` runs ONLY when the summon carried no NBT arg
  (`SummonCommand` passes init=true for the bare form, false for the
  NBT form — any tag, even `{Tags:[a]}`, suppresses the rolls).
  finalizeSpawn touches `follow_range` + a `random_spawn_bonus`
  `add_multiplied_base` triangle(0,0.11485) roll for types whose
  supplier carries it (wolf 16, pig 16, skeleton 16, zombie-family 35),
  and `Zombie.handleAttributes` adds `spawn_reinforcements` base
  (rand*0.1) + `knockback_resistance` `add_value` (rand*0.05) for
  zombie/husk/zombie_villager.
- `/attribute` writes and `data merge` attribute lists join the pack;
  the merge now also APPLIES the packed list (it was silently dropped).
- `livingAttrBases` mirrors LivingEntity.createLivingAttributes so
  `/attribute` accepts the shared living supplier (e.g. armor on pig).
- Pack order on vanilla is fastutil identity-hash order — unstable
  across JVM runs — so the test compares normalized sets and masks the
  random amounts.

Pinned by TestAttrSavePackParity (12 legs, both servers). Test-side:
`data get` replies are fragmented styled `text` fields — chatTextPieces
reconstructs the SNBT before parsing.

Known flake fixed mid-run: the test's own SNBT list parser could loop
unbounded at EOF — capped loops + anchored parse now.

## Slice 2026-09-19e — i-frame grace suppression + entity-id offset + sim-distance gate

Measured `LivingEntity.hurtServer` on 26.2 (javap) and fixed the
graced-hit tail: a hit landing inside the 10-tick grace window now
deals only the excess over `lastHurt` with NO damage_event, NO hurt
sound, NO knockback, and — the subtle part — does NOT refresh
`invulnerableTime` (grace branch leaves the original window running;
only the full-hit arm sets 20). Applied to both `Manager.Damage`
(mobs) and `Session.damageWith` (players): `DamageResult.Graced`,
broadcasts/hurt voice/knockback all gated on `!graced`, health
metadata still emitted (vanilla updates Health on graced hits). Death
still runs on graced hits; `mobDie` gained a `silent` arm for the
b8=false path (no death sound/voice). Pinned by TestCritSweepParity.

Entity ids: the global eid counter now starts at 1 like vanilla (first
joiner = 1); it was 2 ("entity 1 is the player" assumption) so every
spawned entity was +1 across the whole wire. Three compensating `+1`s
removed from spawn-packet `Data` fields (fishing bobber + projectiles)
— vanilla writes the raw id there; `damage_event`'s `+1` is wire-level
cause/direct encoding (0 = absent) and stays.

Simulation distance: entity ticks (move, gravity, fire, invuln decay,
death linger) are now gated on the mob's chunk being inside a live
non-spectator player's Chebyshev simulation-distance window — measured
`<= simDistance` on vanilla 26.2 (chunk 8 ticks at sim=8, chunk 9
never; summons into unloaded chunks just don't materialize). Mobs
outside the window freeze completely, matching vanilla — which also
means `movement_speed` only joins the attributes pack once the entity
actually ticks inside the window (lazy AttributeMap instantiation on
first use), so `summonAttrTouches` no longer pre-touches it;
`mobTickStep` touches it on each live tick instead. With no eligible
sessions the gate opens — unit-test worlds keep ticking.

Test-side: TestAttrSavePackParity now `damage ... 1`-pokes each
summoned mob before reading so vanilla's first-travel ms touch fires
deterministically, and moved the AI passives back inside range on a
separate z-row from the hostiles. chatTextPieces resyncs past bogus
tag headers inside non-String fields instead of truncating the tail.

Also fixed: TestWeaponDamagePackets was a test bug, not a server bug
— Bob attacked ~30 ms after equipping the sword, so attack-strength
cooldown scaling (which mc-go already implements correctly, matching
vanilla) cut the hit to ~1.3 instead of 6. The test now waits 750 ms
(sword = 1.6 attack speed → 625 ms cooldown) before attacking.

## Slice 2026-09-19f — per-type attribute suppliers from bytecode

`entityAttrBases` now carries vanilla's full supplier table: extracted
`DefaultAttributes`' clinit (EntityType -> createAttributes factory)
from the 26.2 jar and walked each factory's bytecode, chaining
`getstatic Attributes.X` + `ldc2_w` pairs through `Builder.add`, with
`Builder.add(Holder)` resolving to the attribute's registered default
(40 defaults mined from `Attributes.<clinit>`'s
`register(id, RangedAttribute)` calls) and inherited factories
resolved up the class hierarchy. 91 types, 2385 entries — includes
the player row (waypoint transmit/receive 6e7) and exact float32
bases like zombie ms 0.23000000417232513.

The generated table now feeds: `attrInstance` (replaces the three-row
hand `mobAttrBases`; `livingAttrBases` stays as the generic fallback,
corrected to registered defaults — air_drag_modifier is 1.0, not 0),
`mobAttrSupplierBase`, the follow_range rsb-roll gate (membership in
the supplier, matching Mob.finalizeSpawn), and the zombie-family
handleAttributes gate (spawn_reinforcements membership — exactly
zombie/husk/zombie_villager/zombified_piglin/drowned). /summon now
accepts every type with a supplier — max_health supplier base is the
spawn health except where mobHealth overrides (horse rolls 15-30,
sulfur_cube 4*size). Witch health corrected 24 -> 26.

TestAttrSavePackParity gained a 12-type sweep leg set (cow, horse,
bee, warden, elder_guardian, bogged, breeze, panda, dolphin,
armadillo, villager, witch): summon + `/attribute <attr> base get`
touches the instance inside vanilla's lazy map, then the `data get
attributes` pack exposes the bases verbatim on both servers.

## Slice 2026-09-23a — doHurtTarget post-attack riders + melee damage table

Vanilla's per-type doHurtTarget overrides are now wired off the melee
step (mobPostAttack, runs when the hit lands — hurtServer's flag is
still true on excess-inside-iframe hits):

- husk: hunger for round(effectiveDifficulty*140) ticks — the flat
  harness world's regional factor resolves effectiveDifficulty to half
  the difficulty id (140t at NORMAL), amp 0, flags 0x0E (bit 3 = blend).
- cave_spider: poison 140t at NORMAL, 300t at HARD, none below.
- iron_golem: attackAnimationTick=10 + broadcastEntityEvent(this,4)
  fired on the swing, entity.iron_golem.attack plays unconditionally
  (even on misses), and a landed hit launches the victim
  dv.y += 0.4*(1-kbr) with damage rolled attack_damage/2 +
  nextInt(attack_damage) — 7.5..21.5 at the 15 base.

mobMelee's hand table is gone: damage reads the generated
entityAttrBases attack_damage (fixing spider/cave_spider 3->2 and
zombified_piglin 8->5), and the chase follow-range cutoff now uses the
type's supplier follow_range (35 zombie-family, 16 spider) instead of
a flat 35.

TestMobPostAttackParity pins all three legs vs vanilla — summon ~2 ~ ~
beside a survival player with a fat absorb pool (a dead player anchors
no relative summon and gets no entity stream), provoke the golem with
a bare attack, then compare the update_mob_effect / entity_event /
launch predicates. entity_event's eid is a raw int32, not VarInt.

Open items remaining: entity AI beyond the chase-melee step (ranged
attacks, flee, neutral-mob provoke state), horse chest/container
slots, ambient mob voices, guardian flop/beam, per-damage-type hurt
variants, baby-pitch voicePitch, witch.drink, scoreboard objectives,
region save, online mode, redstone, terrain gen, lighting, .dat
persistence.

## Slice 2026-09-23b — ambient voices, daylight burn, Age/IsBaby keys

Mob.baseTick's ambient voice is wired: ambientSoundTime climbs once
per living tick, fires when random.nextInt(1000) < count, resets to
-getAmbientSoundInterval(); playHurtSound resets it first. The
generated table (74 types, tools/genmobambient +
testdata/vanilla-26.2/ambient_sounds.tsv) carries the sound id, the
baby-specific sound id where 26.2's *_sound_variant datapack defines
one (pig -> entity.baby_pig.ambient & friends), the per-type interval
(80 default, 120 Animal, 400 horse family, 900 ocelot, 200 turtle, 160
guardian, 6 happy_ghast) and volume (bat 0.1, cows 0.4, ghast/dragon
5.0, warden 4.0). voicePitchE centers babies at 1.5 — and the baby
sound-variant id pins the second half of vanilla's baby voice model.

Mob.aiStep's daylight burn follows: #minecraft:burn_in_daylight
(skeleton, stray, wither_skeleton, bogged, zombie, zombie_horse,
zombie_villager, drowned, zombie_nautilus, phantom — husk absent) run
isSunBurnTick every living tick, NoAI or not: MONSTERS_BURN
(overworld daylight outside the 12542-23459 sleep window, rain off),
sky visible at the eye block, rand*30 < (light-0.4)*2 (~4%/tick at
full sky light), dry. A bare head ignites for 160t with max()
semantics; a helmet absorbs it at nextInt(2) wear per qualifying tick
and snaps with entity_event 49 (HEAD on 26.2's break table). One wire
bug surfaced fixing the burn's own flag broadcast:
broadcastMobSharedFlags was embedding the eid inside the metadata
payload on top of broadcastEntityMetadata's own prepend, so
set_entity_data went out with a doubled id.

Summon NBT key scoping is corrected: Age is only read by AgeableMob
types (52 leaf types incl. cubemobs and nautiluses) and IsBaby only by
the Zombie/AbstractPiglin/Zoglin hierarchies — an IsBaby pig is an
adult on vanilla. Age ticks like vanilla's: babies count up and grow
up at 0, adults count a breeding cooldown down; the live value lands
in the entity save map.

TestAmbientVoiceParity (night leg, pigs day leg) and
TestUndeadSunBurnParity (noon ignite+helmet, night quiet) pass vs both
servers. The test pumps answer keepalives now — vanilla kicks a silent
client mid-leg at ~30s.

Open items remaining: entity AI beyond the chase-melee step (ranged
attacks, flee), horse chest/container
slots, guardian flop/beam, per-damage-type hurt variants, witch.drink,
scoreboard objectives, region save, online mode, redstone, terrain
gen, lighting, .dat persistence.

## Slice 2026-09-23c — neutral-mob provocation (anger + darkness gates)

NeutralMob provocation lands: damageMob now stamps the hurt player as
the mob's AngryAt target — through the manager's stored pointer, since
several call sites hand damageMob a snapshot copy (this also repairs
LastHurtByPlayerTicks, which silently never persisted). A zombified
piglin additionally arms persistentAngerEndTime with vanilla's
PERSISTENT_ANGER_TIME roll of 400-780 ticks; golem and spiders run the
open-ended HurtByTargetGoal retaliation. The tick loop expires anger
at the timer's end, clears it when forgive_dead_players sees the
provoker dead, and every ALERT_INTERVAL (80-120t) an angry piglin
copies its anger to un-angry zombified piglins inside the 10-block
alert box — alertOthers.

mobMeleeStep's target pick now gates neutrals: chase only the player
they're angry at, except spider/cave_spider which also hunt unprovoked
under SpiderTargetGoal's light<0.5 gate — night in the 12542-23459
window, skylight-less dims, or an opaque roof over the eye block.

Two adjacent fixes the measurement surfaced: /damage ... by @s now
passes the direct entity as killer when no `from` cause is given
(vanilla's DamageSource does the same — so the blackbox provoke is a
command, not a whiffable punch); and paPump's dmgEvent counter filters
by the player's own entity id from join_game — /kill @e broadcasts a
generic_kill damage_event per mob, which was polluting calm windows
non-deterministically.

TestNeutralProvokeParity pins six legs vs both servers: piglin/spider/
golem calm-then-provoked at noon, spider hostile at night and under a
deep shade slab, and the two-piglin herd alert. TestMobPostAttackParity
and the ambient/burn tests still pass; unit suite 38.5s.

## Slice 2026-09-23d — skeleton ranged attack + summon default equipment

RangedBowAttackGoal lands for the AbstractSkeleton family
(skeleton/stray/bogged): while the target is beyond the 15-block
attack radius or hasn't been seen 20 ticks the skeleton pathfinds in
(chase speed from entityAttrBases), then navigation.stop and strafes —
±0.5 move/strafe inputs with the 20-tick 30%-flip rolls for clockwise
and back-off direction, and the radius-quartile back-off reset. The
draw cycle is vanilla's: attackTime counts down, a 20-tick bow draw
(living_entity_use metadata flag 0x01 broadcast to the tracker), then
mobShootArrow releases an arrow entity aimed at the target's 1/3-height
with the horizontal-distance gravity fudge, 1.6 b/t speed, shooter +
weapon "bow" set, and entity.skeleton.shoot on source HOSTILE.
attackInterval resets to 40 (20 on hard) per getAttackInterval —
measured on AbstractSkeleton.bowGoal.

Measuring the bare-summon leg surfaced a second gap: vanilla's /summon
calls loadEntityRecursive then finalizeSpawn, and
populateDefaultEquipmentSlots arms the mob — skeletons get a bow,
which is why a "bare" summoned skeleton shoots arrows. Explicit
equipment NBT wins (the drop-chance guarantee keeps it). mobDefaultEquip
now fills an empty adult mainhand at summon: bow for the skeleton
family, golden sword for zombified piglin (5% golden_spear, a 26.2
addition), stone_sword wither_skeleton, crossbow pillager, golden_axe
piglin_brute, iron_axe vindicator, and piglin's 50/50 crossbow vs
sword (10% of swords are spears). Armor populate, enchantment rolls,
and drowned's chance slots are follow-ups.

TestSkeletonRangedParity pins three legs vs both servers: NBT-bow
skeleton shoots and hits, stone-sword skeleton melees with no arrows,
and a bare summon shoots (proving the populate). paObs.spawned is
zeroed per leg before the summon — a previous leg's stuck arrows
re-broadcast to a fresh tracker and polluted the spawn window.

Open items remaining: entity AI beyond chase/ranged-melee (flee,
RestrictSunGoal/FleeSunGoal sun-avoidance, AvoidEntityGoal wolf-flee),
crossbow/piglin ranged variants (RangedCrossbowAttackGoal) and
stray/bogged tipped arrows, horse chest/container slots, guardian
flop/beam, per-damage-type hurt variants, witch.drink, scoreboard
objectives, region save, online mode, redstone, terrain gen, lighting,
.dat persistence.

## Slice 2026-09-23e — AvoidEntityGoal flee + summon NBT-wins equipment

AvoidEntityGoal lands with the measured flee table: creeper runs from
ocelots and cats (6 blocks, 1.0 walk / 1.2 sprint), the AbstractSkeleton
family from wolves, rabbits from players (8), wolves (10) and any
Monster-superclass mob (4) at 2.2, foxes from players (16) and
wolves/polar bears (8). Flee preempts the attack goals — vanilla's goal
priority ordering (avoid 3 < attack 4). While the goal is armed the
fleer keeps stepping away from the threat's position for ~40 ticks
past the trigger window — vanilla's posAvoider paths to a point up to
16 blocks out, so separations overshoot the trigger band.

The flee pin measures with `data get entity ... Pos` read back through
system chat (the reply flattens to bare d-suffixed doubles). First run
taught it the hard way: a creeper summoned one block off the player
swelled and detonated before the query — the flee pairs now stage 15+
blocks out, and the feared mobs are NoAI-anchored so a wolf can't
chase its own test.

TestMobFleeParity pins three legs vs both servers: creeper↔cat and
skeleton↔wolf separate past 6 blocks, rabbit↔player past 8. Unit
suite 38.5s.

## Slice 2026-09-23k — creeper SwellGoal freeze, real blast damage

SwellGoal measured: the fuse arms only inside 3 blocks (canUse
dist2<9), holds while the target stays within 7 (tick defuses
beyond 49 or on lost LOS), and the goal claims Flag.MOVE — a
swelling creeper FREEZES in place instead of walking into its
target. mobSwellStep now arms <3, defuses >7 with the 3-7 band
keeping the current dir, and stops all movement while swelling or
primed inside 3 (Minecraft's MeleeAttackGoal is mutex'd out).
Alongside: ServerExplosion.getEntityDamageAmount multiplies by
2*radius not radius — every mc-go explosion dealt HALF vanilla
damage (creeper: 9.7 vs 18.9 at dist 2.7). Fixed to 7*2r, and the
+0.5 knockback y-bias was removed — 26.2 pushes pure eye-direction
scale((1-d0)*seen*kbMult*(1-kbResist)). Also fixed: instant_health
/instant_damage amplifier shifts — Java's ishl masks &31, Go's <<
does not, so amp 200 healed 0 instead of 1024. New
TestCreeperSwellParity: leg A samples the freeze point (~2.75 both)
+ post-blast health band (vanilla 1.10, mc-go 1.68), leg B teleports
the target 15.5 out mid-fuse and pins defuse+survival. Green both.

Open items remaining: crossbow/piglin ranged variants
(RangedCrossbowAttackGoal) and stray/bogged tipped arrows, creeper
swell/explode approach, RestrictSunGoal/FleeSunGoal sun-avoidance,
summon armor-populate + enchantment rolls + drowned chance slots,
horse chest/container slots, guardian flop/beam, per-damage-type hurt
variants, witch.drink, scoreboard objectives, region save, online
mode, redstone, terrain gen, lighting, .dat persistence.

## Slice 2026-09-23f — creeper swell/explode

Creeper lands its defining behavior: SwellGoal flips DATA_SWELL_DIR to
+1 while the target is inside 7 blocks with line of sight, -1 outside
(deflating only — once lit past 0 it stays lit while the goal keeps
running); the hiss is entity.creeper.primed at the first rising tick,
pitch 0.5. The fuse itself is Creeper.tick bookkeeping, moved outside
the NoAI gate so `summon creeper ... {ignited:1b,NoAI:1b}` still blows
up — 30 ticks (NBT Fuse resizes) to explodeCreeper: discard + a MOB
explosion at feet, radius = ExplosionRadius (default 3) doubled when
powered (NBT powered, metadata idx 17).

tntExplode generalized to explodePower(src, x,y,z, radius, grief) — the
16³ raycast sampler, hurtEntities impact formula, and explode packet
now serve both TNT (grief=tnt_explodes) and creepers (mob_griefing).
The explode packet's particle picks explosion_emitter at radius>=2,
explosion below — isSmall's cutoff.

TestCreeperBoomParity pins four legs vs both servers: swell→explode→
radius-3 packet + player damage + dirt carve (execute-if-block air
probe), powered → radius 6, and the NoAI+ignited boom. The summon
leg for leg 1 (stone_sword) earlier proved the same populate-defaults
machinery on skeletons.

## Slice 2026-09-23k — creeper SwellGoal freeze, real blast damage

SwellGoal measured: the fuse arms only inside 3 blocks (canUse
dist2<9), holds while the target stays within 7 (tick defuses
beyond 49 or on lost LOS), and the goal claims Flag.MOVE — a
swelling creeper FREEZES in place instead of walking into its
target. mobSwellStep now arms <3, defuses >7 with the 3-7 band
keeping the current dir, and stops all movement while swelling or
primed inside 3 (Minecraft's MeleeAttackGoal is mutex'd out).
Alongside: ServerExplosion.getEntityDamageAmount multiplies by
2*radius not radius — every mc-go explosion dealt HALF vanilla
damage (creeper: 9.7 vs 18.9 at dist 2.7). Fixed to 7*2r, and the
+0.5 knockback y-bias was removed — 26.2 pushes pure eye-direction
scale((1-d0)*seen*kbMult*(1-kbResist)). Also fixed: instant_health
/instant_damage amplifier shifts — Java's ishl masks &31, Go's <<
does not, so amp 200 healed 0 instead of 1024. New
TestCreeperSwellParity: leg A samples the freeze point (~2.75 both)
+ post-blast health band (vanilla 1.10, mc-go 1.68), leg B teleports
the target 15.5 out mid-fuse and pins defuse+survival. Green both.

Open items remaining: crossbow/piglin ranged variants
(RangedCrossbowAttackGoal), stray/bogged tipped arrows,
RestrictSunGoal/FleeSunGoal sun-avoidance, summon armor-populate +
enchantment rolls + drowned chance slots, charged-creeper
skull drops, spawnLingeringCloud for effect-carrying creepers, horse
chest/container slots, guardian flop/beam, per-damage-type hurt
variants, witch.drink, scoreboard objectives, region save, online
mode, redstone, terrain gen, lighting, .dat persistence.

## Slice 2026-09-23g — FleeSunGoal (skeletons only)

Measured in bytecode: in 26.2 only the AbstractSkeleton family
(skeleton, stray, bogged, wither_skeleton) registers RestrictSunGoal +
FleeSunGoal — zombies, drowned, and every other #burn_in_daylight
type burns where it stands (Zombie.registerGoals lists BreakDoor,
LookAtPlayer, RandomLookAround, SpearUse, ZombieAttack; no sun goals).

mobSunFleeStep runs between the combat goals and the ambient wander
for a burning bare-headed skeleton: canUse is target==nil &&
isBrightOutside (same sleep-window day check as sunBurnTick, plus a
rain veto) && isOnFire && canSeeSky(feet) && head slot empty. The
shelter is getHidePos — up to ten random samples in a ±10 XZ / ±3 Y
box, first cell with no sky view and a standable floor (air at feet
and head, solid below) — latched on the entity so the path runs to
completion like canContinueToUse = !navigation.isDone, then released.
Walk speed is the goal's speedModifier 1.0 via the new mobWalkTo
shim.

TestMobSunFleeParity pins three legs vs both servers: a burning
skeleton drifts east into the only shaded patch under a stone roof
(east >= 5 by 10s), a burning zombie never seeks, and a helmeted
skeleton never ignites.

## Slice 2026-09-23k — creeper SwellGoal freeze, real blast damage

SwellGoal measured: the fuse arms only inside 3 blocks (canUse
dist2<9), holds while the target stays within 7 (tick defuses
beyond 49 or on lost LOS), and the goal claims Flag.MOVE — a
swelling creeper FREEZES in place instead of walking into its
target. mobSwellStep now arms <3, defuses >7 with the 3-7 band
keeping the current dir, and stops all movement while swelling or
primed inside 3 (Minecraft's MeleeAttackGoal is mutex'd out).
Alongside: ServerExplosion.getEntityDamageAmount multiplies by
2*radius not radius — every mc-go explosion dealt HALF vanilla
damage (creeper: 9.7 vs 18.9 at dist 2.7). Fixed to 7*2r, and the
+0.5 knockback y-bias was removed — 26.2 pushes pure eye-direction
scale((1-d0)*seen*kbMult*(1-kbResist)). Also fixed: instant_health
/instant_damage amplifier shifts — Java's ishl masks &31, Go's <<
does not, so amp 200 healed 0 instead of 1024. New
TestCreeperSwellParity: leg A samples the freeze point (~2.75 both)
+ post-blast health band (vanilla 1.10, mc-go 1.68), leg B teleports
the target 15.5 out mid-fuse and pins defuse+survival. Green both.

Open items remaining: crossbow/piglin ranged variants
(RangedCrossbowAttackGoal), stray/bogged tipped arrows,
RestrictSunGoal's still-daylight wander bias, summon armor-populate +
enchantment rolls + drowned chance slots, charged-creeper skull drops,
spawnLingeringCloud for effect-carrying creepers, horse chest/
container slots, guardian flop/beam, per-damage-type hurt variants,
witch.drink, scoreboard objectives, region save, online mode,
redstone, terrain gen, lighting, .dat persistence.

## Slice 2026-09-23h — mob environment conversions

Zombie.doUnderWaterConversion (measured): eye in water 600 ticks
starts a 300-tick conversion that finishes even if the mob surfaces;
zombie/zombie_villager -> drowned, husk -> zombie first (a two-hop
chain — the fresh zombie then drowns again). Skeleton.tick's freeze
chain: 140 ticks in powder snow, then a 300-tick countdown that
CANCELS on stepping out; skeleton -> stray. Both sit inside the NoAI
gate.

mobConvert is Mob.convertTo(single(keepEquipment, preserveName)):
spawn the converted entity (fresh EID/uuid) carrying position, look,
clamped health, baby flag, equipment map — then discard the old, so
the wire shows spawn-then-remove. Non-silent mobs emit level events
1040 (drowned) / 1048 (stray) at the mob position.

Wire fix found by the test: ClientboundLevelEventPacket's event and
data fields are raw int32s — broadcastLevelEvent was writing VarInt,
malforming every level event (block-break particles, anvil sounds,
conversions). Entity metadata: zombie-family idx17 is
is_under_water_converting (zombie_villager's cure flag stays at 19),
skeleton idx16 is is_freeze_converting; zombie-family idx16 baby is
now emitted too.

TestMobConvertParity pins both legs on both servers: drowned appears
+ zombie gone + ev1040, stray appears + skeleton gone + ev1048.

## Slice 2026-09-23k — creeper SwellGoal freeze, real blast damage

SwellGoal measured: the fuse arms only inside 3 blocks (canUse
dist2<9), holds while the target stays within 7 (tick defuses
beyond 49 or on lost LOS), and the goal claims Flag.MOVE — a
swelling creeper FREEZES in place instead of walking into its
target. mobSwellStep now arms <3, defuses >7 with the 3-7 band
keeping the current dir, and stops all movement while swelling or
primed inside 3 (Minecraft's MeleeAttackGoal is mutex'd out).
Alongside: ServerExplosion.getEntityDamageAmount multiplies by
2*radius not radius — every mc-go explosion dealt HALF vanilla
damage (creeper: 9.7 vs 18.9 at dist 2.7). Fixed to 7*2r, and the
+0.5 knockback y-bias was removed — 26.2 pushes pure eye-direction
scale((1-d0)*seen*kbMult*(1-kbResist)). Also fixed: instant_health
/instant_damage amplifier shifts — Java's ishl masks &31, Go's <<
does not, so amp 200 healed 0 instead of 1024. New
TestCreeperSwellParity: leg A samples the freeze point (~2.75 both)
+ post-blast health band (vanilla 1.10, mc-go 1.68), leg B teleports
the target 15.5 out mid-fuse and pins defuse+survival. Green both.

Open items remaining: crossbow/piglin ranged variants
(RangedCrossbowAttackGoal), stray/bogged tipped arrows,
RestrictSunGoal wander bias, summon armor-populate + enchantment
rolls + drowned chance slots, charged-creeper skull drops,
spawnLingeringCloud for effect-carrying creepers, horse chest/
container slots, guardian flop/beam, per-damage-type hurt variants,
witch.drink, zombie->drowned equipment copy edge cases, scoreboard
objectives, region save, online mode, redstone, terrain gen,
lighting, .dat persistence.

## Slice 2026-09-23i — stray/bogged tipped arrows

Stray.getArrow and Bogged.getArrow (javap-measured) each addEffect on
the released arrow: MobEffectInstance(SLOWNESS, 600) and
MobEffectInstance(POISON, 100). Arrow.customEffects lands in
potion_contents.custom_effects; on a hurting hit forEachEffect runs
at the stack's potion_duration_scale — a mob-built stack has no such
component, so victims get the FULL duration (stray slowness is the
long 30s shot).

Entity.arrowEffects carries the pair; projectileCollide applies them
to mob hits (mobAddEffect) and player hits (playerGiveEffect →
update_mob_effect) only when the hit dealt damage. The arrow's
DATA_ID_EFFECT_COLOR (meta idx 9, int serializer) is emitted after
spawn — slowness 9154528, poison 8889187.

Test wrinkle measured on vanilla: `summon stray ~8 ~ ~ {}` with an
explicit empty compound arrives BARE-HANDED and melee-walks the
player, while `summon stray ~8 ~ ~` (no NBT) gets the populate bow —
an explicit-NBT summon skips something in vanilla's finalizeSpawn
worth a follow-up pin.

TestTippedArrowParity pins arrows>0 + update_mob_effect(id=1,dur=600)
for stray, (id=18,dur=100) for bogged, and no effect packet at all
for a plain skeleton — green on both servers.

## Slice 2026-09-23k — creeper SwellGoal freeze, real blast damage

SwellGoal measured: the fuse arms only inside 3 blocks (canUse
dist2<9), holds while the target stays within 7 (tick defuses
beyond 49 or on lost LOS), and the goal claims Flag.MOVE — a
swelling creeper FREEZES in place instead of walking into its
target. mobSwellStep now arms <3, defuses >7 with the 3-7 band
keeping the current dir, and stops all movement while swelling or
primed inside 3 (Minecraft's MeleeAttackGoal is mutex'd out).
Alongside: ServerExplosion.getEntityDamageAmount multiplies by
2*radius not radius — every mc-go explosion dealt HALF vanilla
damage (creeper: 9.7 vs 18.9 at dist 2.7). Fixed to 7*2r, and the
+0.5 knockback y-bias was removed — 26.2 pushes pure eye-direction
scale((1-d0)*seen*kbMult*(1-kbResist)). Also fixed: instant_health
/instant_damage amplifier shifts — Java's ishl masks &31, Go's <<
does not, so amp 200 healed 0 instead of 1024. New
TestCreeperSwellParity: leg A samples the freeze point (~2.75 both)
+ post-blast health band (vanilla 1.10, mc-go 1.68), leg B teleports
the target 15.5 out mid-fuse and pins defuse+survival. Green both.

Open items remaining: crossbow/piglin ranged variants
(RangedCrossbowAttackGoal), RestrictSunGoal wander bias, summon
armor-populate + enchantment rolls + drowned chance slots,
charged-creeper skull drops, spawnLingeringCloud for effect-carrying
creepers, horse chest/container slots, guardian flop/beam,
per-damage-type hurt variants, witch.drink, scoreboard objectives,
region save, online mode, redstone, terrain gen, lighting, .dat
persistence.

## Slice 2026-09-23j — charged-creeper explosion riders + blast-lethal die() wiring

javap on Creeper:
- explodeCreeper: level.explode() → spawnLingeringCloud() →
  triggerOnDeathMobEffects → discard — the explode packet lands BEFORE
  the removal (ours sent remove first).
- spawnLingeringCloud: getActiveEffects() non-empty → AreaEffectCloud
  at the blast point, radius 2.5 / radiusOnUse -0.5 / waitTime 10 /
  duration 300 / potionDurationScale 0.25 / radiusPerTick -2.5/300,
  copying each MobEffectInstance.
- killedEntity: shouldDropLoot (the CREEPER's own !baby && doMobLoot)
  && isPowered && !droppedSkulls → victim.dropFromLootTable
  (CHARGED_CREEPER, consumer sets droppedSkulls=true). The root loot
  table routes on the victim's entity_type; zombie->zombie_head,
  skeleton->skeleton_skull, creeper->creeper_head, piglin->piglin_head,
  wither_skeleton->wither_skeleton_skull. One head max per creeper —
  an unmapped victim never latches the flag.

Found while implementing: explodePower damaged mobs with
entities.Damage directly and never ran the lethal arm — explosion
kills left mobs at Health 0 forever, no death event, no drops. Now
res.Health<=0 → mobDie(silent on graced hits) → chargedCreeperSkull.
Also entities.Item never serialized to data-NBT — added
Item/PickupDelay on the item arm of entitySaveMap so `data get entity
@e[type=item] Item.id` works.

TestChargedCreeperParity (4 legs, green on both):
powered-kill drops exactly one zombie_head; two victims in one blast
still produce one head (droppedSkulls latch); unpowered blast kills
the victim but drops nothing; an effect-carrying creeper leaves an
area_effect_cloud while a clean one leaves none. Staging: NoAI
everywhere (vanilla ignited creepers still pathfind), Health:1f
victims so any impact is lethal regardless of blast-power math,
creative player + `execute as @e[type=item] run data get entity @s
Item.id` for the ground-item multiset (survival pickup races the
assert).

Cloud effect application on entities inside the radius is still open;
the entity itself + its 10/300 countdown are in.

Follow-up inside the same slice: AreaEffectCloud.serverTick
measured — discard at age>=wait+duration, waiting while age<wait,
radius+=radiusPerTick per tick (discard <0.5), and every 5 ticks the
potion contents (at potionDurationScale 0.25) dose living entities
inside the XZ radius, minus victims on the 20-tick reapplication
cooldown, minus radiusOnUse=-0.5 per dose. cloudTickStep implements
all of it for mobs and players (mobGiveEffect / playerGiveEffect so
the update-gate and instant arms apply). Leg E pins a survival
player catching the slowness — 5 legs green on both.

## Slice 2026-09-23k — creeper SwellGoal freeze, real blast damage

SwellGoal measured: the fuse arms only inside 3 blocks (canUse
dist2<9), holds while the target stays within 7 (tick defuses
beyond 49 or on lost LOS), and the goal claims Flag.MOVE — a
swelling creeper FREEZES in place instead of walking into its
target. mobSwellStep now arms <3, defuses >7 with the 3-7 band
keeping the current dir, and stops all movement while swelling or
primed inside 3 (Minecraft's MeleeAttackGoal is mutex'd out).
Alongside: ServerExplosion.getEntityDamageAmount multiplies by
2*radius not radius — every mc-go explosion dealt HALF vanilla
damage (creeper: 9.7 vs 18.9 at dist 2.7). Fixed to 7*2r, and the
+0.5 knockback y-bias was removed — 26.2 pushes pure eye-direction
scale((1-d0)*seen*kbMult*(1-kbResist)). Also fixed: instant_health
/instant_damage amplifier shifts — Java's ishl masks &31, Go's <<
does not, so amp 200 healed 0 instead of 1024. New
TestCreeperSwellParity: leg A samples the freeze point (~2.75 both)
+ post-blast health band (vanilla 1.10, mc-go 1.68), leg B teleports
the target 15.5 out mid-fuse and pins defuse+survival. Green both.

Open items remaining: crossbow/piglin ranged variants
(RangedCrossbowAttackGoal), RestrictSunGoal wander bias, summon
armor-populate + enchantment rolls + drowned chance slots,
cloud entity metadata on the wire (radius/potion particles),
horse chest/container slots, guardian flop/beam, per-damage-type
hurt variants, witch.drink, scoreboard objectives, region save,
online mode, redstone, terrain gen, lighting, .dat persistence.

## Slice 2026-09-23l — explosion drop pooling + item-entity merge

The dirt-drop divergence was NOT decay rate or crater size (both
already parity: ~185 vs ~187 cells) — it was StackCollector:
ServerExplosion.interactWithBlocks feeds every destroyed cell's
rolled stack through addOrAppendStack, which pours it into the first
collector whose stack is mergable via ItemEntity.merge(a,b,16) —
drops pool into 16-count entities popped at the contributing cell.
Vanilla's powered blast leaves 2 entities holding 16+4; mc-go
spawned ~30 singles. explodeDestroyBlocks now pools identically,
and spawnItemDrop adopted popResource's real spawn shape: cell
center ±0.25 scatter, (rand·0.2−0.1, 0.2, rand·0.2−0.1) toss.
Runtime ItemEntity.mergeWithNeighbours is in too: merge cadence
2 ticks after a block-boundary crossing else 40, isMergable gates
(pickupDelay≠32767, age<6000, count<maxStack, owner equality),
box inflate(0.5,0,0.5) → |dx|,|dz|≤0.75 |dy|≤0.5, larger stack
absorbs smaller with tie → neighbour, max(pickupDelay)/min(age)
on the survivor, ItemAge drives the 6000-tick despawn, and
Entity.moveTowardsClosestSpace slides wall-trapped items toward
the nearest open N/S/W/E/UP face (0.1-0.3 push, others ×0.75).
New TestItemDropMergeParity: A) two co-located dirt merge to one
count-2 entity; B) powered blast leaves ≤8 entities all ≤16-count
(van 16+4, mc 16+16). Green both; charged+swell suites still green.

Open items remaining: crossbow/piglin ranged variants
(RangedCrossbowAttackGoal), RestrictSunGoal wander bias, summon
armor-populate + enchantment rolls + drowned chance slots,
cloud entity metadata on the wire (radius/potion particles),
horse chest/container slots, guardian flop/beam, per-damage-type
hurt variants, witch.drink, selector nbt= arg, scoreboard
objectives, region save, online mode, redstone, terrain gen,
lighting, .dat persistence.

## Slice 2026-09-23m — summon finalizeSpawn equipment populate

Bare `/summon` runs Mob.finalizeSpawn (SummonCommand passes
shouldFinalizeSpawn=false whenever an NBT tag is present — tagged
summons are fully user-specified and get NO populate, no piglin
baby roll, no patrol-leader roll). mobDefaultEquip therefore runs
only when the summon tag is nil.

Measured + implemented:
- Monster.populateDefaultEquipmentSlots armor cascade: gate
  rand<0.15·specialMultiplier; tier=nextInt(3) plus two 10.87%
  upgrades (max iron; diamond unreachable); HEAD→CHEST→LEGS→FEET,
  each next slot continues unless rand<(hard?0.1:0.25). Applies to
  zombie/husk/zombie_villager/zombified_piglin/skeleton/stray/bogged.
- DifficultyInstance.getSpecialMultiplier: eff = difficultyId ·
  (0.75 + dayTime/1.44e6·0.25 + inhabited/3.6e6·(hard?1:0.75) +
  moon·0.25 capped by the day term); easy halves the last two.
  Fresh flat normal world → eff 1.5 → special 0: no cascade, no
  enchant rolls (empirically confirmed: vanilla zombie bare).
- populateDefaultEquipmentEnchantments: mainhand rand<0.25·special,
  each armor slot rand<0.5·special, enchantItemFromProvider
  MOB_SPAWN_EQUIPMENT by_cost_with_difficulty, cost 5+nextInt(1+
  int(special·17)); pool is non_treasure (== mc's enchantAvailable).
- Per-type populates: skeleton/stray/bogged cascade + unconditional
  bow; wither_skeleton stone_sword; pillager crossbow (+1/300
  piercing-1 crossbow via PILLAGER_SPAWN_CROSSBOW); piglin_brute
  golden_axe; vindicator iron_axe; drowned rand>0.9 → nextInt(16)
  <10 trident else fishing_rod + rand<0.03 offhand nautilus_shell;
  zombie-family adds a rare mainhand: rand<(hard?0.05:0.01) →
  nextInt(6) → 0=iron_sword, 1=iron_spear, else iron_shovel;
  zombified_piglin golden_spear 1/20 else golden_sword.
- Piglin.finalizeSpawn (reason != STRUCTURE): rand<0.2 → baby,
  else adult gets createSpawnWeapon (0.5 crossbow / nextInt(10)==0
  golden_spear / golden_sword); armor populate is adult-gated,
  maybeWearArmor 10% per golden piece.
- PatrollingMonster.finalizeSpawn: reason ∉ {PATROL, EVENT,
  STRUCTURE} → rand<0.06 && canBeLeader → PatrolLeader + ominous
  white_banner on HEAD with drop_chance 2.0 (witch/ravager can't
  lead; pillager/vindicator/evoker/illusioner can). mc emits
  PatrolLeader + drop_chances{head:2.0f} on patrolling types and
  loads PatrolLeader from summon NBT (tagged path: flag but no
  banner, since finalizeSpawn is skipped).

New TestSummonEquipmentParity (segmented parser: flattened SNBT
glues `components{...}count<N>idminecraft:` together, so slot
segments split on `slot(count|components)` and count/id extract
inside each segment): piglin 40 — ~80% weapons, ~50% crossbow,
≤10%/slot gold; drowned 48 — trident/rod ≤14 total, nautilus ≤5,
zero armor; zombie 30 — ≤2 slots (rare iron weapon allowed);
pillager 60 — 60 crossbows, ≤10 white_banner heads; skeleton 10
bows; zero "enchant" anywhere (special=0). Green on both targets
(van piglin 20/0/14 + 12 gold, mc 15/1/17 + 6; van zombie bare,
mc rolled shovel+spear; banners 4 van / 3 mc).

Open items remaining: piglin zombification timer (TimeInOverworld
→ zombified_piglin at ~15s overworld), crossbow/piglin ranged
variants, RestrictSunGoal wander bias, leftHanded 5% + follow-range
triangle(0,0.11485) modifier (no mc modifier model yet), cloud
entity metadata on wire, horse chest/container slots, guardian
flop/beam, per-damage-type hurt variants, witch.drink, selector
nbt= arg, scoreboard objectives, region save, online mode,
redstone, terrain gen, lighting, .dat persistence.

## Slice 2026-09-23n — leftHanded 5% + IsBaby/PatrolLeader emit

Mob.finalizeSpawn's remaining flat pieces: rand<0.05 →
setLeftHanded(true) on every mob (now rolled in
summonAttrTouches's init path and emitted as LeftHanded:1b in the
save map instead of a hardcoded 0b); the follow_range
random_spawn_bonus triangle(0,0.11485) add_multiplied_base
modifier and zombie-family reinforcement rolls were already wired.
IsBaby now emits for the Zombie/AbstractPiglin/Zoglin set (was
read but never written — piglin baby rolls were invisible to
`data get`), and PatrolLeader + drop_chances{head:2.0f} emit for
the PatrollingMonster set.

TestSummonEquipmentParity gained a flagScan leg: piglin IsBaby
∈[2,16]/40 (~20%) and zombie LeftHanded ≤8/30 (~5%). Note: a
tagged summon skips ALL of this — PatrolLeader:1b in summon NBT
loads the flag without the banner finalizeSpawn would place.
Green on both (babies 6/6, lefties 3/2, banners 5/5).

Open items remaining: piglin zombification timer (TimeInOverworld
→ zombified_piglin at ~15s overworld), crossbow/piglin ranged
variants, RestrictSunGoal wander bias, ominous-banner item
components on wire (banner_patterns/item_name/tooltip_display —
mc emits plain white_banner), cloud entity metadata on wire,
horse chest/container slots, guardian flop/beam, per-damage-type
hurt variants, witch.drink, selector nbt= arg, scoreboard
objectives, region save, online mode, redstone, terrain gen,
lighting, .dat persistence.

## Slice 2026-09-23o — overworld zombification (piglin/hoglin → zoglin)

AbstractPiglin.customServerAiStep / Hoglin.customServerAiStep:
`isConverting = !IsImmuneToZombification && !NoAI &&
envAttr(PIGLINS_ZOMBIFY @ pos)` — overworld sets that attribute
true for every position — so `TimeInOverworld` increments while
converting and resets to 0 otherwise; past 300 ticks the mob
plays its convert sound (skipped on peaceful) and
`convertTo(ZOMBIFIED_PIGLIN|ZOGLIN, keepEquipment+keepInventory)`
fires, then 200 ticks of nausea land on the fresh convert. mc:
`mobOverworldZombifies` map + a `mobConvertTick` branch inside
the existing `!NoAI` gate; `mobConvert` now returns the new
entity so nausea applies to it. Crucially `Mob.convertTo` never
runs finalizeSpawn — equipment is copied slot-for-slot, so a
bare baby piglin becomes a BARE zombified and the brute's
golden_axe survives — the test discriminates exactly this.
IsImmuneToZombification/TimeInOverworld round-trip through the
save map and summon-tag reads.

TestOverworldZombifyParity: tagged baby piglin + bare brute + two
immune/noai survivors (PersistenceRequired+Invulnerable — a fresh
zoglin eats nearby piglins) + two hoglins; after 18s assert
piglin=2/brute=0/zombified=2/zoglin≥1, exactly one golden_axe
carried over and zero repopulated weapons. Green on both.

Open items remaining: crossbow/piglin ranged variants
(RangedCrossbowAttackGoal), RestrictSunGoal wander bias,
ominous-banner item components on wire
(banner_patterns/item_name/tooltip_display — mc emits plain
white_banner), cloud entity metadata on wire, horse
chest/container slots, guardian flop/beam, per-damage-type hurt
variants, witch.drink, selector nbt= arg, scoreboard objectives,
region save, online mode, redstone, terrain gen, lighting, .dat
persistence.

## Slice 2026-09-23p — witch potion drinking

Witch.aiStep's using-item state machine, measured off the bytecode:
while `usingTime` (32-tick potion use duration) counts down the
mainhand holds a potion + SPEED_MODIFIER_DRINKING (-0.25 add_value,
id minecraft:drinking) sits on movement_speed; at zero the contents
apply, the slot empties, the modifier lifts. The pick runs every
idle tick in vanilla's order: rand<0.15 && eye-in-water &&
!water_breathing → water_breathing; rand<0.15 && (onFire ||
lastDamageSource in IS_FIRE) && !fire_resistance → fire_resistance;
rand<0.05 && health<max → healing (instant_health, +4); rand<0.5 &&
target && dist²>121 && !speed → swiftness. The aiStep block has no
isEffectiveAi gate — a NoAI witch still drinks. New entity state:
WitchDrink/WitchDrinkTime, and LastDamageType which damageMob now
records (lastDamageSource). Summon NBT reads Fire (Entity.load's
remaining burn ticks — was ignored) and itemComponentsNBT emits
minecraft:potion_contents for stacks carrying a potion id.

TestWitchDrinkParity: burning witch gains fire_resistance, a
10-damage witch heals back above 17, a survival player 13 blocks
out drives the swiftness drink — all three asserted via
active_effects/Health reads on both servers. Green identical.

Open items remaining: crossbow/piglin ranged variants
(RangedCrossbowAttackGoal), witch ranged attack (RangedAttackGoal
splash throws), RestrictSunGoal wander bias, ominous-banner item
components on wire (banner_patterns/item_name/tooltip_display),
living hand-active metadata byte on wire (witch drink state),
cloud entity metadata on wire, horse chest/container slots,
guardian flop/beam, per-damage-type hurt variants, scoreboard
objectives, region save, online mode, redstone, terrain gen,
lighting, .dat persistence.

## Slice 2026-09-23q — witch ranged attack (splash potion throw)

Witch.performRangedAttack + ThrowableProjectile/SplashPotion physics,
measured off the bytecode:

- RangedAttackGoal(this,1.0,60,10.0): attackTime starts -1, ticks
  down; ==0 fires and resets to floor(lerp(dist/radius,60,60))=60;
  <0 resets. performRangedAttack early-returns while drinking.
- Aim: dx/dz at the target's position + delta, dy = target.eyeY-1.1
  - self.y, then +dist*0.2 before normalize; velocity 0.75 (0.45 at
  dist≤2), inaccuracy 8.0 — shared shoot().
- Potion pick order: Raider target → health≤4 healing else
  regeneration (and clears target); dist≥8 && !slowness → slowness;
  health≥8 && !poison → poison; dist≤3 && !weakness && rand<0.25 →
  weakness; else harming. (Raider branch noted, not wired — no
  raider-target plumbing yet.)
- ThrowableProjectile: gravity 0.03 (arrows use 0.05), same 0.99
  drag; entity collide → splash at entity pos; block hit → splash at
  the contact point; never stuck.
- Splash (onHitAsPotion): everything living within ~4 blocks gets
  the contents — timed effects at duration*proximity, instant ones
  scaled on the amount (instant_damage 6*prox as indirect_magic
  sourced at the thrower, instant_health 4*prox heal); level event
  2002 plays break particles/sound with the potion's packed color.
- Potions registry entries measured: slowness 1800, poison 900,
  weakness 1800, regeneration 900, speed/water_breathing/
  fire_resistance 3600; harming/healing instant amp0.

The splash_potion entity rides the arrow flight path with its own
collide (no damage on contact) and splash on either hit kind; the
spawn carries the thrower as owner and the potion_contents item.

TestWitchThrowParity: 12-block survival target → slowness lands;
healthy 6-block target → poison lands; damaged to <8 → harming (death
broadcast or a sub-6 Health read — resistance cleared first so the
`damage` amount is real). Green identical on both servers.

Open items remaining: crossbow/piglin ranged variants
(RangedCrossbowAttackGoal), witch raider-heal throw branch,
RestrictSunGoal wander bias, ominous-banner item components on wire,
living hand-active metadata byte on wire (witch drink state), cloud
entity metadata on wire, horse chest/container slots, guardian
flop/beam, per-damage-type hurt variants, scoreboard objectives,
region save, online mode, redstone, terrain gen, lighting, .dat
persistence.

## Slice 2026-09-23r — crossbow mobs: pillager + piglin ranged attack

RangedCrossbowAttackGoal (pillager's goal, (this,1.0,8.0)) and
PiglinAi's CrossbowAttack behavior share one state machine, measured
off the bytecode:

- checkExtraStartConditions: isHolding(CROSSBOW) && canSee(target)
  && isWithinAttackRange → ProjectileWeaponItem range 8.
- Approach gate: out of radius² or seeTime<5 (and no pending
  attackDelay) → navigate at movement_speed, no charge start.
- UNCHARGED → CHARGING on gate clear (startUsingItem + the
  hand-active metadata byte), CHARGING → CHARGED after
  getChargeDuration = floor(1.25*20) = 25t unenchanted
  (releaseUsingItem + attackDelay=20+rand(20)), CHARGED → READY on
  delay 0, READY → performRangedAttack if still seeing → UNCHARGED.
- performRangedAttack → performCrossbowAttack →
  CrossbowItem.performShooting at speed 1.6, divergence
  14-difficulty.id*4 (=6 on normal). The arrow rides mobShootArrow's
  existing 1.6-velocity path.
- Piglin hostility gate: adults only, and PiglinAi.isWearingGold —
  any golden_ armor piece exempts the player.

TestCrossbowShootParity: a survival player inside range takes arrow
damage from both a spawned pillager and a crossbow-equipped adult
piglin (IsImmuneToZombification so the overworld 300t clock can't
preempt the leg). Green identical on both servers.

Open items remaining: witch raider-heal throw branch,
RestrictSunGoal wander bias, ominous-banner item components on wire,
living hand-active metadata byte on wire (witch drink state — the
crossbow goal now broadcasts it), cloud entity metadata on wire,
horse chest/container slots, guardian flop/beam, per-damage-type
hurt variants, scoreboard objectives, region save, online mode,
redstone, terrain gen, lighting, .dat persistence.

## Slice 2026-09-23s — witch DATA_USING_ITEM on wire

The witch's drink state isn't the shared living-flags byte —
Witch.defineSynchedData adds its own DATA_USING_ITEM boolean after
Raider's fields, landing at metadata index 17 (boolean serializer
type 8). Measured on the wire: drink start emits [17,8,1], finish
[17,8,0]; mc now broadcasts the same from broadcastWitchUsing.
(living hand-active idx8 stays correct for bow/crossbow draws —
startUsingItem writes that field instead.)

Also measured while in here: the raider-heal throw branch needs
NearestHealableRaiderTargetGoal.canUse's hasActiveRaid() — no raid
system → the branch is correctly unreachable on both servers for
now; note it under raid work, not witch work.

TestWitchHandUseParity scans set_entity_data packets for the witch's
idx-17 flag toggling 1→0 across a fire-triggered drink. Green on
both. (TestWitchDrinkParity's swiftness leg widened to 15 blocks +
9s — vanilla's target-acquire delay plus the dist²>121 drink gate
made the old 13-block/5s window marginal; probing showed the drink
fires ~6s in.)

Open items remaining: witch raider-heal throw (blocked on raids),
RestrictSunGoal wander bias, ominous-banner item components on wire,
cloud entity metadata on wire, horse chest/container slots,
guardian flop/beam, per-damage-type hurt variants, scoreboard
objectives, region save, online mode, redstone, terrain gen,
lighting, .dat persistence.
## Slice 2026-09-23t — guardian attack beam

GuardianAttackGoal, measured on the vanilla jar + live wire: the goal
starts attackTime at -10 on target acquire; each tick it stops
navigation, lookAt(90,90)s, drops the target on lost line of sight,
and counts up. At attackTime==0 it syncs DATA_ID_ATTACK_TARGET —
guardian metadata index 17, int serializer — to the target's entity
id and (unless silent) broadcasts entity event 21 (the beam cue).
At attackTime>=getAttackDuration — 80 ticks, ElderGuardian overrides
to 60 — it zaps: hurtServer(indirectMagic(guardian,guardian), 1.0
+2*hard +2*elder) then doHurtTarget — Mob.doHurtTarget has NO range
gate, so the melee swing lands in the same tick — then setTarget(null),
whose stop() clears the synced id to 0.

Observed vanilla wire: entity_event(int32 eid, 0x15) one tick before
set_entity_data [eid,17,1,targetEid]; ~4.0s later (80t) the zap —
a single damage_event indirect_magic + set_health 20→14.0: the 1.0
magic hit opens i-frames and the 6.0 melee (attack_damage attr) lands
its 5.0 delta — and [eid,17,1,0] clears. The cycle then repeats.

Acquisition gating measured too: NearestAttackableTargetGoal's
GuardianAttackSelector only takes Player/Squid/Axolotl at dist²>9,
and the regular guardian's canContinueToUse drops the beam inside
three blocks (elder keeps it). Player-side only in mc for now —
squid/axolotl targeting noted below. Guardian aiStep's flop/tail
animation is all behind isClientSide — nothing to mirror server-side,
and DATA_ID_MOVING (idx 16) is never set server-side either.

TestGuardianBeamParity pumps the wire for entity event 21, the idx-17
sync set/clear around the player eid, and the zap's landing health +
event→zap gap (~80t). Vanilla-side the leg re-summons up to 3x:
entity tracking for a guardian summoned into a player-tp'd chunk
silently flakes (zap still lands — damage events are self-referenced —
but spawn/metadata never reach the client), so the leg sits near world
spawn where tracking is reliable. Green identical on both servers:
gap 4.005s vanilla / 4.102s mc, zap health exactly 14.0 both.

Open items remaining: witch raider-heal throw (blocked on raids),
RestrictSunGoal wander bias, ominous-banner item components on wire,
cloud entity metadata on wire, horse chest/container slots, guardian
squid/axolotl targeting + knockback on beam melee, per-damage-type
hurt variants, scoreboard objectives, region save, online mode,
redstone, terrain gen, lighting, .dat persistence.
## Slice 2026-09-23u — elder guardian curse

ElderGuardian.customServerAiStep, measured on the jar: every
(tickCount + entityId) % 1200 == 0 — once a minute, phased per elder —
MobEffectUtil.addEffectToPlayersAround pushes
MobEffectInstance(mining_fatigue, 6000t, amplifier 2) to every player
who is isSurvival() (survival or adventure), not allied, and strictly
inside 50 blocks; the predicate re-applies only when the player lacks
the effect, holds a weaker amplifier, or the live instance
endsWithin(1199) — a fresh amp-2 cast isn't re-pushed. Each affected
player then gets ClientboundGameEventPacket(GUARDIAN_ELDER_EFFECT,
silent ? 0 : 1) — Type id 10 per the enum's static init — the ghost
render + curse sound. The same method also calls setHomeTo(pos, 16)
when homeless; mc has no mob-home wander restriction so that half is
noted only.

Wire: update_mob_effect [eid, VarInt 3, amp 2, VarInt 6000, flags]
then game_event [byte 10][float 1.0] — both self-referenced to the
cursed player so no entity tracking is needed.

mc-go: mobGuardianStep now calls elderCurseStep for the elder kind —
it runs under the same NoAI/sim-range gate as vanilla's
customServerAiStep, using e.SyncCount+e.ID for the 1200-tick phase and
the same survival/50-block/refresh predicates before
playerGiveEffect + sendGameEvent(10).

TestElderCurseParity summons the elder ~20 blocks out (inside the 50-
block curse radius, outside follow_range 16 so no beam interferes) and
pumps up to 72s for the game event and the effect packet. Green
identical: event param 1.0, amp 2, duration 6000 on both servers
(128s wall — the 60s curse period dominates).

Open items remaining: witch raider-heal throw (blocked on raids),
RestrictSunGoal wander bias, ominous-banner item components on wire,
cloud entity metadata on wire, horse chest/container slots, elder
setHomeTo(pos,16) home restriction, guardian squid/axolotl targeting +
knockback on beam melee, per-damage-type hurt variants, scoreboard
objectives, region save, online mode, redstone, terrain gen,
lighting, .dat persistence.
## Slice 2026-09-23v — guardian beams squid/axolotl targets

GuardianAttackSelector measured earlier admits Player OR Squid OR
Axolotl (each candidate past dist²>9, all competing by distanceToSqr
inside follow_range). Probed vanilla live: guardian summoned beside a
squid beams it — event 21, idx-17 sync = the squid's entity id, the
zap lands as damage_event on the squid, then idx-17 → 0. A 10-hp
squid dies on the second zap (1.0 indirect_magic + 5.0 melee delta
through i-frames per zap).

mc-go: beamTarget now resolves session ids first then manager entity
ids; acquisition scans s.entities.byID for squid/axolotl (3D dist,
>3 blocks, inside follow_range) and competes them against the nearest
player candidate — nearest wins. Zap splits: players keep the
damageWith pair, mobs take damageMob(indirect_magic) +
damageMob(mob_attack) with cause=direct=guardian, same i-frame fold.
Non-elder >3-block drop applies to mob targets too.

TestGuardianSquidBeamParity: creative player (can never win the
selector), guardian + squid summoned 5 blocks apart near spawn —
asserts event 21, idx-17 == the squid eid, ≥1 damage_event on the
squid, idx-17 → 0. Identical on both servers: 2 zaps each (squid
dies mid-second cycle), 44s wall.

Open items remaining: witch raider-heal throw (blocked on raids),
RestrictSunGoal wander bias, ominous-banner item components on wire,
cloud entity metadata on wire, horse chest/container slots, elder
setHomeTo(pos,16) home restriction, beam-melee knockback on the
target, per-damage-type hurt variants, scoreboard objectives,
region save, online mode, redstone, terrain gen, lighting, .dat
persistence.
## Slice 2026-09-23w — dealDefaultKnockback on mob victims

LivingEntity.hurt's merged tail calls dealDefaultKnockback for every
landed entity-sourced hit outside the no_knockback damage-type tag —
graced-excess hits included — pushing the victim 0.4 away from the
source's getEntity() (the cause, falling back to the direct entity).
player_attack is exempted (Player.attack applies its own knockback).
mc-go had this only for the /damage path — the caller now lives inside
damageMob so every entity-sourced damageMob (beam zap pair, projectile
hits, thorns procs, /damage) pushes the victim like vanilla; the
player-victim path was already covered by playerHurtKnockback.

Measured wire: 26.2's set_entity_motion carries an LpVec3 (VarInt id +
packed low-precision vector) — not three shorts; ReadLpVec3 decodes it.
Blackbox probe noise ruled out a flop-driven victim (squid/axolotl
random-motion spam masks the push; a NoAI pig floats stock-still on
vanilla — NoAI freezes even gravity — so any motion packet on it IS
the knockback).

TestDamageKnockbackParity: NoAI pig + `damage ... 1 mob_attack by @p`,
12 blocks east → identical (0.4, 0, 0) LpVec3 push on both servers.

Found while probing: vanilla /damage needs a single-entity selector
(`@e[...]` without limit=N errors argument.entity.toomany even when
one entity exists), `by @s`/`by <name>` fail from the chat runner,
and a pig feet-on-bedrock (-63) takes periodic damage (likely in_wall
at the boundary) — the -60 float avoids it.

Open items remaining: witch raider-heal throw (blocked on raids),
RestrictSunGoal wander bias, ominous-banner item components on wire,
cloud entity metadata on wire, horse chest/container slots, elder
setHomeTo(pos,16) home restriction, knockback vy grounded-pop on
NoAI-frozen mobs (mc Grounded stays false), per-damage-type hurt
variants, scoreboard objectives, region save, online mode, redstone,
terrain gen, lighting, .dat persistence.
## Slice 2026-09-23x — area_effect_cloud summon + wire metadata

/summon of a cloud now works: it is an Entity (not LivingEntity), so
the max_health summon gate rejected it outright. The cloud path gets
vanilla's constructor defaults (duration -1 = infinite, waitTime 20,
reapplicationDelay 20, radiusOnUse/radiusPerTick 0) and the summon
NBT keys vanilla's readAdditionalSaveData reads: Radius (clamped
0..32 by setRadius), Duration, WaitTime, RadiusOnUse, RadiusPerTick,
Age, and potion_contents.custom_effects seeding the dose map.

Wire parity measured: the tracker-attach dirty pack carries only the
synced entries the NBT touched — DATA_RADIUS (idx 8, float) when it
differs from the 3.0 defineSynchedData default, and DATA_PARTICLE
(idx 10, single-particle serializer 16) as an entity_effect holder
with the mixed potion ARGB (per-channel integer average; speed's
0x33EBFF lands as ff33ebff). DATA_WAITING (idx 9, bool) flips on for
the wait window and back off on the first active tick — each
transition its own set_entity_data packet. The creeper lingering
cloud (radius 2.5 shrinking) emits the same pairing entries now.
Duration -1 no longer ages out immediately: the discard check gates
on CloudDuration >= 0.

TestCloudMetaParity asserts radius/particle/waiting byte-equal on
both servers (van idx8=40a00000, idx10=1cff33ebff, idx9 t8).

Open items remaining: witch raider-heal throw (blocked on raids),
RestrictSunGoal wander bias, patrol-captain ominous gear + bottle
drops (isCaptain is head-item equality — unreachable via /summon;
needs natural patrol spawn or raids), horse chest/container slots,
elder setHomeTo(pos,16) home restriction, per-damage-type hurt
variants, scoreboard objectives, region save, online mode, redstone,
terrain gen, lighting, .dat persistence.
## Slice 2026-09-23y — tamed mount sneak-inventory

AbstractHorse.mobInteract's `isTamed && isSecondaryUseActive` branch
now opens the mount inventory: mount_screen_open(0x29) carries
(containerId VarInt, inventoryColumns VarInt, entityId int32 — a plain
big-endian int, not a VarInt) then container_set_content lays out
2+3*columns mount slots plus the 36 player rows with stateId 1
(initializeMenu increments the container counter). A chested pack
animal's getInventoryColumns is 5 — the donkey menu is 53 slots.

Summon-NBT: Tame and ChestedHorse now load for the AbstractHorse /
AbstractChestedHorse families. The donkey/mule/llama/trader_llama
summon gate unblocked: those suppliers carry no max_health because
finalizeSpawn randomizes it (15-30) — a tagged summon falls back to
the attribute default 20.

TestMountScreenParity: cid=1, cols=5, eid, 53 slots, stateId 1 — all
byte-identical on both servers.

Open items remaining: witch raider-heal throw (blocked on raids),
RestrictSunGoal wander bias, patrol-captain ominous gear + bottle
drops (needs natural patrol spawn/raids — isCaptain is head-item
equality, unreachable via /summon), mount-inventory slot edits
(saddle/chest cell writes), elder setHomeTo(pos,16) home restriction,
per-damage-type hurt variants, scoreboard objectives, region save,
online mode, redstone, terrain gen, lighting, .dat persistence.

## Slice 2026-09-23z — /damage `at` source position

DamageSource carries an optional sourcePosition that lands verbatim
on the damage_event tail. DamageCommand's `at <pos>` clause sets it;
mc parsed the clause but discarded the coords. Worse, mc's player-side
broadcast emitted hasPos=true with the VICTIM's own position whenever
a cause entity existed — vanilla sends hasPosition=false for every
entity-sourced hit (`by`/`from`); the client pulls direction off the
entity itself.

Measured: `by @p` → cause=direct, pos=false; `at` → cause=direct=0,
pos=true + coords. `by @p from @e` → both ids, pos=false.

damageSource grew hasPos/x/y/z; damageWith no longer fabricates the
position from the victim. TestDamageAtPosParity pins all three forms
byte-equal modulo entity ids on both servers.

Open items remaining: witch raider-heal throw (blocked on raids),
RestrictSunGoal wander bias, patrol-captain ominous gear + bottle
drops (needs natural patrol spawn/raids), mount-inventory slot edits
(saddle/chest cell writes), elder setHomeTo(pos,16) home restriction,
per-damage-type hurt variants, scoreboard objectives, region save,
online mode, redstone, terrain gen, lighting, .dat persistence.

## Slice 2026-09-23za — mount-inventory slot writes

container_click on a mount screen now lands. The packet gate dropped
any window without a container or merchant — mount windows have
neither. handleMountClick wires mode-0 pickup/place through
menuStack/setMenuStack's new mount branches (slot 0 saddle →
Entity.Saddle, slot 1 body → BodyItem, 2.. → MountInv cells) with the
measured mayPlace gates: saddle slot active on the can_equip_saddle
intersect (horse/zombie_horse/skeleton_horse/donkey/mule/camel — not
llamas), body slot on can_wear_horse_armor + llama carpets.

Wire shape, measured: pickup echoes container_set_slot clearing the
slot plus set_cursor_item carrying the saddle; a valid-state place
answers container_set_slot slot-0 saddle (state++) then set_equipment
carries wire slot 7. A stale stateId still earns a full
container_set_content resync — checkClickState's default branch now
covers mount windows.

openMountInventory switches entityByID's copy for GetPtr — the
equip-delta marks must land on the managed entity or the mob-sync
flush never sees them.

TestMountClickParity: saddle give → tamed chested donkey → sneak-open
→ pickup hotbar → place slot 0; both servers emit the same
set_slot/set_cursor_item/set_equipment sequence.

Open items remaining: witch raider-heal throw (blocked on raids),
RestrictSunGoal wander bias, patrol-captain ominous gear + bottle
drops (needs natural patrol spawn/raids), elder setHomeTo(pos,16)
home restriction, per-damage-type hurt variants, scoreboard
objectives, region save, online mode, redstone, terrain gen,
lighting, .dat persistence.

## Slice 2026-09-23zb — entity persistence (entities/*.mca)

Mobs and item entities now survive a restart through the real
server's own format: saveWorldDir writes
dimensions/<ns>/<path>/entities/r.<rx>.<rz>.mca — the Anvil
container with per-chunk records {Position int[2], DataVersion,
Entities} — and loadWorldDir respawns each compound via
summonMobRaw + applyEntitySave, so vanilla-only fields ride back
through ExtraNBT untouched. Two write-side fixes were measured the
hard way: the save compound needs an explicit `id` (entitySaveMap's
/data callers add it themselves), and peaceful difficulty silently
drops hostile entities at vanilla load (allowedInPeaceful gate in
loadEntityRecursive), which masqueraded as a format bug.

TestEntitySaveParity: vanilla saves a zombie+pig → mc-go loads it
(spawn positions pinned through add_entity), then mc-go saves →
vanilla loads it back. TestWorldSaveParity's chunk coverage stays
separate.

Open items remaining: witch raider-heal throw (blocked on raids),
RestrictSunGoal wander bias, patrol-captain ominous gear + bottle
drops (needs natural patrol spawn/raids), elder setHomeTo(pos,16)
home restriction, per-damage-type hurt variants, scoreboard
objectives, online mode, redstone, terrain gen, lighting, player
.dat format (mc-go still writes its JSON store into a vanilla
world — playerdata/<uuid>.dat round-trip is open).

## Slice 2026-09-23zc — player files (players/data/<uuid>.dat)

WorldDir mode now speaks vanilla's player store end to end:
players/data/<uuid>.dat (gzip NBT) is written on disconnect and on
save-all, read back into the saved-player table keyed by uuid
(applySavedPlayer falls back from the JSON store's name key). The
compound is the measured 26.2 field set — Inventory with vanilla
slot numbering (hotbar 0-8, main 9-35), equipment{head,chest,legs,
feet,offhand,body}, cursor, EnderItems, abilities, attributes,
recipeBook (names for unlocked, toBeDisplayed for unseen),
playerGameType/previousPlayerGameType, Xp*/food*/fire(-20 rest),
warden_spawn_tracker, Brain.memories. playerPersist grew the fields
that back it (yaw/pitch/onGround restore too — rejoin now lands
looking the same way the player left), and Session tracks
prevGameMode per vanilla's previousPlayerGameType. The /data read
surface gained Inventory and EnderItems lists in the same slot
numbering, so the reload is checkable on the wire.

TestPlayerDatSaveParity: vanilla equips+teleports a player → mc-go
loads the world and reports sword/helmet/ender-apple/XP 7/
adventure/pos+rotation through /data get; then mc-go saves → vanilla
loads it back with the same report. The .dat file itself is asserted
non-empty on both writes.

Open items remaining: witch raider-heal throw (blocked on raids),
RestrictSunGoal wander bias, patrol-captain ominous gear + bottle
drops (needs natural patrol spawn/raids), elder setHomeTo(pos,16)
home restriction, per-damage-type hurt variants, players/
advancements+<uuid>.json + stats/<uuid>.json (vanilla's other two
player stores — advancements/stats still live only in the JSON
store), online mode, redstone, terrain gen, lighting.

## Slice 2026-09-23zd — stats + advancements player files

The other two vanilla player stores round-trip now:
players/stats/<uuid>.json ({"stats":{<type>:{<stat>:count}},
"DataVersion":4903}) and players/advancements/<uuid>.json ({<id>:
{criteria:{<crit>:"2006-01-02 15:04:05 -0700"},done:b},
"DataVersion":4903}) are written alongside the .dat on disconnect
and save-all and read back into the same saved-player entry. Stats
keep the file's own key sequence as StatsOrder so a reload replays
the order vanilla iterated; advancement criterion timestamps parse
to the same millis they were written from.

TestPlayerDatSaveParity extended: vanilla writes stats+advancements
→ mc-go restores the advancement (selector predicate proves the
load); mc-go writes both → vanilla loads, and vanilla's own re-save
still carries leave_game and story/mine_diamond — the counters made
it into its model, not just past its parser.

Open items remaining: witch raider-heal throw (blocked on raids),
RestrictSunGoal wander bias, patrol-captain ominous gear + bottle
drops (needs natural patrol spawn/raids), elder setHomeTo(pos,16)
home restriction, per-damage-type hurt variants, online mode,
redstone, terrain gen, lighting.

## Slice 2026-09-24a — organic raids end-to-end

The full organic raid lifecycle is live and pinned against vanilla:
villager POI claims (claimedPOIs is the PoiManager occupied set —
a claimed bed keeps the position out of raidIsVillage's available
scan), Bad Omen → Raid Omen conversion in the tick pass
(BadOmenMobEffect.applyEffectTick: spectator/peaceful/isVillage/
omen<max guards, 600-tick instance at the same amplifier, position
latched to ServerPlayer.raidOmenPosition), RaidOmenMobEffect firing
exactly at duration==1 → createOrExtendRaid (POI 64-block centroid,
fallback to the omen position when the scan is empty; the
spectator/RAIDS-gamerule/CAN_START_RAID env-attribute guards sit
ahead of it), the 300-tick cooldown driving the event bar progress
(prog = (300-cd)/300), wave spawns on the ~98-block ring, raider
auto-join through Raid.joinRaid, and the victory/defeat renames.

Measured on the jar this slice: the raid bar is a ServerBossEvent
with the compound translatable name {"translate":"event.minecraft.raid"},
RED + NOTCHED_10 — mc-go's writer was collapsing the component to a
bare "" (componentBytes has no struct arm; the raid path now goes
through component.content() like every other sender). The conversion
also emits a second Update Mob Effect for the fresh raid_omen in the
same tick — identical to the add except blend bit clear (0x06 vs
0x0E, ServerPlayer.onEffectUpdated's hardcoded full=false) — and
/effect give's success.single/multiple replies carry a third arg,
duration/20 as a raw Integer. Raid-victory Hero of the Village is
the icon-only wire form (flags 0x04) at omen-1 amplifier for 48000t;
RAID_OMEN (voluntary_exile) and RAID_WIN (hero_of_the_village)
criteria triggers fire at absorbRaidOmen / victory. /raid exists
mc-go-side only — vanilla's RaidCommand is gated behind
CommandSelection.INTEGRATED so the dedicated jar never registers it;
its replies are pinned off javap literals (level 3, needs args).

TestRaidParity drives the organic flow on BOTH targets — bed at
foot level for the ~1s AcquirePoi claim, effect give bad_omen,
poll active_effects for the id-34 swap, collect until the bar add
shows the translate key, then a pillager probe + kill — and diffs
the flat (eff/eff-/chat/bars) streams: byte-identical now, and a
length-only mismatch errors instead of silently dumping.

Open items remaining: witch raider-heal throw, RestrictSunGoal
wander bias, patrol-captain bottle drops + voluntary_exile
equipment-predicate kill condition (ominous banner stack itself is
wired — getBannerComponentPatch's four components are on the
white_banner slot form), elder setHomeTo(pos,16) home restriction,
per-damage-type hurt variants, online mode, redstone, terrain gen,
lighting.

## Slice 2026-09-24b — patrol captain gear + ominous bottle drops + datapack /function

TestCaptainParity pins the raid-captain slice on both servers: a
deterministic captain summon (equipment.head carrying the
getBannerComponentPatch four-component set + drop_chances{head:2.0f}
+ PatrolLeader), its set_equipment patch on the wire ([9 12 18 72]),
the kill drops (exactly one ominous banner + one ominous_bottle with
amplifier uniform 0-4), the voluntary_exile advancement grant, and a
non-captain pillager leg that must show none of it.

Measured this slice:

- The 256-byte serverbound command cap is real on both ends (vanilla
  fails argument.literal.incorrect; mc-go truncated silently — same
  cap, already there). The ~800-char captain summon therefore runs
  through a datapack function on BOTH legs.
- Packrat SNBT quirk: inside a .mcfunction the summon NBT only parses
  when `equipment` leads the compound — `drop_chances`-first variants
  with a full component map fail "Expected literal" at end of input.
- Raider.isCaptain = head ItemStack matching
  Raid.getOminousBannerInstance AND patrolLeader — not the flag alone.
- dropAllDeathLoot: shouldDropLoot (!baby + mob_drops) gates
  dropFromLootTable + dropCustomDeathLoot; dropEquipment is a stub —
  Mob.dropCustomDeathLoot owns the per-slot roll (dropChance 0 → skip,
  preserved >1 + hitByPlayer bypasses the killer check, random<f).
  Pillager is the only raider with a captain loot pool:
  type_specific/raider {is_captain:true} → 1 ominous_bottle +
  set_ominous_bottle_amplifier uniform 0-4, no killed_by_player gate.
  Captain death = head-slot banner (dropChance 2.0) + bottle — no
  second banner (verified live via `data get` on spawned items).
- voluntary_exile.json = player_killed_entity on #minecraft:raiders
  with entity predicate equipment.head = the ominous patch. (The
  sibling `minecraft:raid_omen` trigger handles raid absorption; a
  `minecraft:voluntary_exile` trigger literal does not exist — mc-go
  maps it onto raid_omen's fire path.)
- Component wire shapes re-verified: item_name is a full unnamed NBT
  tag; tooltip_display hidden_components are RAW component-type ids
  (not holder+1); rarity VarInt ordinal; ominous_bottle_amplifier a
  bare VarInt (id 63).
- Vanilla's /function reply is commands.function.scheduled.single
  (functions run through the deferred execution queue); inner-command
  feedback still reaches the player.
- Summoned pillagers pick up a dropped ominous banner into head and
  become captains (ObtainRaidLeaderBannerGoal/pickUpItem) — the test
  clears ground items between legs or every retry rolls captain.

mc-go side: loadFunctions scans WorldDir/datapacks/*/data/
<ns>/function/**.mcfunction at world load (pack.mcmeta required, same
as vanilla's folder pack source), /function executes lines through
runCommandAt at the function-permission-level (2), and summon NBT's
equipment block lands head-slot ItemStacks incl. the Ominous marker
the loot-pool captain gate and set_equipment patch emit. writeSlot's
tooltip_display now writes raw component ids (was holder+1).

Open items remaining: witch raider-heal throw, RestrictSunGoal
wander bias, natural patrol spawn cycle (PatrolSpawner), elder
setHomeTo(pos,16), per-damage-type hurt variants, online mode,
redstone, terrain gen, lighting.

## Slice 2026-09-24c — natural patrol spawn cycle (PatrolSpawner)

PatrolSpawner.tick is live and pinned on both servers. Measured on the
jar:

- Gate chain: ServerChunkCache.tickChunks wraps the WHOLE
  customSpawners pass in the spawn_mobs gamerule (so the patrol
  countdown freezes under it, not just natural chunk spawning), then
  PatrolSpawner.tick takes the spawn-monsters property as its
  spawnEnemies arg, then SPAWN_PATROLS, then --nextTick with a
  12000+rand(1200) rearm — the first patrol can't roll before ~10 min.
- Per rearmed cycle: isBrightOutside (day window + rain veto), the
  1-in-5 roll, a random overworld player, spectator veto,
  isCloseToVillage(pos,2) on claimed POI sections, ±(24..47) signed
  axis offsets, hasChunksAt(±10), and the CAN_PILLAGER_PATROL_SPAWN
  environment attribute — which the early_game timeline ANDs false
  until the overworld clock reaches tick 120000 (5 days). That last
  one cost two test runs: `time set 1000` re-pins BELOW the timeline
  flip, so the attribute read false forever.
- Group: count = ceil(getEffectiveDifficulty)+1 pillagers (3 on a
  fresh NORMAL world), first spawned = leader (findPatrolTarget picks
  a ±500 wander anchor and sets patrolling), each member re-samples
  MOTION_BLOCKING_NO_LEAVES then jitters XZ by rand(5)-rand(5).
- spawnPatrolMember = isValidEmptySpawnBlock(feet) +
  checkPatrollingMonsterSpawnRules (block light ≤8, difficulty veto,
  AABB obstruction) + PILLAGER.create + finalizeSpawn(PATROL).
- finalizeSpawn's 6% leader roll is reason-INVERTED from the naive
  read: it runs on every spawn reason EXCEPT patrol/event/structure —
  a bare /summon pillager really does roll captain — while the ominous
  head lands whenever patrolLeader is already set (forced leader,
  raid setLeader), and PATROL also sets patrolling.

mc-go side: patrolTickStep in GameTick (after raidTickStep, where
vanilla ticks customSpawners in the level pass), the spawn_mobs outer
gate, dayTime>=120000 for the early_game timeline, patrolNearVillage
over claimedPOIs, and mobDefaultEquipReason threading
spawnDefault/spawnEvent/spawnPatrol through the equip pass. Entity
gains Patrolling + PatrolTarget.

TestPatrolParity accelerates with /tick rate 5000 — advance_time must
stay ON (tickTime gates tickChunks entirely), spawn-monsters=true on
the vanilla props (the spawnEnemies arg), random_tick_speed 0 keeps
mc-go's tick affordable (randomTickStep iterates the 17×17-chunk sim
window at ~17ms/tick — the tick-rate bottleneck, worth revisiting if
fast-forward parity tests multiply), phantoms/traders off. `time set
122000` re-pinned every poll holds day 5+ morning so the early_game
gate stays open. Both legs: 3 pillagers, exactly one ominous-head
leader [9 12 18 72], every member crossbow'd.

Open items remaining: witch raider-heal throw, RestrictSunGoal
wander bias, patrol LongDistancePatrolGoal movement (leader walks
PatrolTarget, members follow), elder setHomeTo(pos,16),
per-damage-type hurt variants, online mode, redstone, terrain gen,
lighting.

## Slice 2026-09-24d — LongDistancePatrolGoal + patrol NBT + 128 despawn

Vanilla side (javap on PatrollingMonster$LongDistancePatrolGoal,
PatrollingMonster, DifficultyInstance):

- Goal priority 4 in registerGoals, speedModifier 0.7 (leader 0.595),
  MOVE flag, cooldownUntil=-1. canUse: gameTime>=cooldownUntil &&
  isPatrolling && getTarget()==null && !hasControllingPassenger &&
  hasPatrolTarget. tick is gated on navigation.isDone().
- tick: companions = getEntitiesOfClass(PatrollingMonster,
  aabb.inflate(16), canJoinPatrol && !=self); patrolling &&
  companions empty -> setPatrolling(false). Leader within 10 of the
  patrol target's center (strict <, distToCenterSqr vs +0.5/+0.5/+0.5)
  re-rolls it via findPatrolTarget (±500 xz, sets patrolling).
- Waypoint: atBottomCenterOf(target) + yRot(90.0F)(pos-target)*0.4 —
  and yRot's argument is RADIANS (goes straight into Mth.cos/sin), so
  the sweep is ~117deg, not 90. walkPt = pos + normalize(waypoint-pos)
  *10; bp = heightmapPos(MOTION_BLOCKING_NO_LEAVES, containing(walkPt));
  nav.moveTo(bp, leader?0.595:0.7). On success a leader pushes bp to
  every companion's patrolTarget; on failure moveRandomly (±8 xz
  heightmap wander) + cooldownUntil=gameTime+200.
- removeWhenFarAway = patrolling && distSqr>16384: a patrolling mob
  >128 blocks from every non-spectator player in its dim discards
  instantly; non-patrolling raiders are despawn-exempt. Sim-distance
  (8 chunks <= 143 blocks) means a mob at 129-143 is ticked AND far
  enough to die.
- NBT round trip: addAdditionalSaveData writes patrol_target
  (BlockPos.CODEC int array [I;x,y,z]), PatrolLeader, Patrolling;
  readAdditionalSaveData reads the same three keys — summon NBT works.

mc-go side: mobPatrolStep in the goal cascade (patrol(4) ahead of
sunflee(5) ahead of wander), mobGrounded extracted out of
mobMoveStep's gravity preamble, mobWalkToSpeed threading the
speedModifier*movement_speed product, patrolCompanions' 16-block
PatrollingMonster-class box, patrolFindTarget, patrolFarDiscard in
mobTickStep (sim-range gated like vanilla), Entity.HasPatrolTarget,
and entity save data emitting Patrolling + patrol_target [I;]. Summon
NBT learned Patrolling/patrol_target to mirror readAdditionalSaveData.

Blackbox: TestPatrolSpawnParity keeps the natural cycle (tick rate
2000 pacing — NOT sprint, whose reply batching hides a mid-burst
spawn until it's marched past the despawn radius — tp-hop every poll
keeps the player glued to the leader so the group can't cross 128);
TestPatrolMoveParity summons a deterministic pair (leader + member,
patrol_target due east) in creative — no target acquisition, despawn
anchored — and pins the skewed march heading (~east-positive, slight
+z), Patrolling round-trip, and member trailing. The 128-block
despawn rule is also why every observation tp-anchors first.

Open items remaining: witch raider-heal throw, RestrictSunGoal
wander bias, elder setHomeTo(pos,16), per-damage-type hurt variants,
generic monster despawn (only the patrolling rule exists now),
online mode, redstone, terrain gen, lighting.

## Slice 2026-09-24e — Mob.checkDespawn (generic despawn rules)

Vanilla side (javap Mob.checkDespawn + MobCategory + Animal):

- Order: peaceful && !allowedInPeaceful -> discard; else
  isPersistenceRequired || requiresCustomPersistence (isPassenger ||
  isLeashed) -> noActionTime=0, return; else nearest =
  getNearestPlayer(this,-1) and if none, return.
- dist > category.despawnDistance && removeWhenFarAway -> discard.
  despawnDistance = 128 for every category except WATER_AMBIENT (64).
- noActionTime > 600 && rand(800)==0 && dist > 32 &&
  removeWhenFarAway -> discard. dist < 32 -> noActionTime = 0.
- serverAiStep unconditionally noActionTime++ per tick.
- MobCategory: noDespawnDistance is a hardcoded 32 for ALL categories;
  despawnDistance rides the ctor (128 everywhere but 64 water_ambient).
- removeWhenFarAway: Mob base returns true unconditionally; Animal
  overrides it false; PatrollingMonster = patrolling && dist>16384.
- PersistenceRequired is the save/load key (already in mc-go's shared
  save fields, was hardcoded 0 — now a real Entity field + summon NBT
  + load round-trip).

mc-go side: mobCheckDespawn in the mobTickStep sim-range window
(replacing the standalone patrolFarDiscard call — its >128 branch is
literally removeWhenFarAway), NoActionTime++ per tick,
mobRemoveWhenFarAway dispatching patrolling/animal/base,
despawnAnimalTypes, despawnWaterAmbientTypes (fish 64),
despawnPeacefulBans, mobDespawnKinds (Mob-subclass gate — armor
stands are LivingEntities, not Mobs).

TestDespawnParity on both legs: tp 140 east instant-discards a plain
zombie; PersistenceRequired zombie + pig survive (exempt paths); a
6000-tick sprint idle-rolls a >32 zombie away (~99.9%); difficulty
peaceful purges the persistent zombie too (peaceful check precedes
persistence) but not the pig. The sprint had to run at night —
daylight burns the test zombies before the window ends.

## Slice 2026-09-24f — elder home + Mob home pair + NoGravity

Vanilla 26.2 measured (javap + live jar probes):

- ElderGuardian.customServerAiStep tail: `if (!hasHome())
  setHomeTo(blockPosition(), 16)` — latches on the first tick.
- Mob home pair is `homePosition` + `homeRadius` — `hasHome()` is
  `homeRadius != -1` (so home_radius:-2 counts, -1 doesn't).
- Mob.addAdditionalSaveData writes home_radius + home_pos (BlockPos
  codec [I;x,y,z]) only while hasHome; readAdditionalSaveData reads
  home_radius getIntOr(-1) then home_pos when >= 0 (ZERO default).
- addEffectToPlayersAround predicate (curse target): survival-or-
  adventure game mode required, !isAlliedTo, closerThan(50), and the
  replace rule !hasEffect || getEffect.endsWithin(1199) — a fresh
  amp-2 cast is not re-pushed.
- GUARDIAN_ELDER_EFFECT is game_event type 10, param isSilent?0:1
  (already pinned by TestElderCurseParity).
- Entity.saveWithoutId writes Silent and NoGravity bytes only while
  set (ifeq-skipped) — Glowing likewise under hasGlowingTag.

mc-go side: Entity.HomePos/HomeRadius/HasHome fields; the elder
branch of mobGuardianStep plants the home after elderCurseStep;
summon NBT + applyEntitySave honor home_radius/home_pos with the
-1 means homeless rule; entitySaveMap emits the pair only while
HasHome; Entity.NoGravity added end to end (summon NBT, mobGrounded
skip — ai keeps running, save/load round-trip); Silent joined the
conditional save keys; nbtGet indexes [B;]/[I;]/[L;] arrays.

TestElderHomeParity on both legs: a bare NoGravity elder plants
home_pos [20,-59,0] + home_radius 16 within a tick of summon; a
{home_radius:5,home_pos:[I;9,10,11]} summon keeps its own values
(latch never overwrites). Elements are queried by index because
vanilla's flattened chat drops IntArray separators entirely.

Open items remaining: RestrictSunGoal
wander bias, per-damage-type hurt variants, online mode, redstone,
terrain gen, lighting.

## Slice 2026-09-24g — witch NearestHealableRaiderTargetGoal + CanJoinRaid

Vanilla 26.2 measured:

- Witch registers NearestHealableRaiderTargetGoal(this, Raider.class,
  mustSee=true, selector) at targetSelector priority 2 (HurtByTarget 1,
  attackPlayers 3); the selector is witch.hasActiveRaid() &&
  !target.is(WITCH) — the TARGET's raid membership is NOT required,
  only the Raider class. canUse: cooldown<=0 && rand.nextBoolean() &&
  hasActiveRaid() && findTarget()!=null; start() sets cooldown to
  reducedTickDelay(200); aiStep decrements every tick and bars
  player targeting while it runs (attackPlayersGoal.setCanAttack).
- Witch.performRangedAttack on a Raider target picks healing when
  health <= 4.0f else regeneration, then setTarget(null). Splash
  aim: dy = targetEyeY - 1.1 - eyeY, vertical lead dist*0.2,
  speed 0.75 (0.45 within 2 blocks).
- Raider.canJoinRaid: ctor false; finalizeSpawn sets
  !isWitch || reason != NATURAL (so a bare /summon — COMMAND — flags
  it, a tagged /summon skips finalizeSpawn entirely and it stays
  false); Raid.joinRaid latches it true; Raider.aiStep auto-joins an
  unjoined canJoinRaid raider inside an ongoing raid on the shared
  level.getGameTime()%20 tick. Raider save map writes Wave +
  CanJoinRaid always, RaidId only while in a raid; load reads them
  back and rejoins by id.

mc-go side: Entity.CanJoinRaid field; bare summon sets it for
raidRaiderType, tagged summon reads the NBT bool; the auto-join gate
moved from a per-id stagger to the real gameTime%20 tick plus the
field; raidJoin latches CanJoinRaid. Witch heal goal added to
mobWitchAttackStep — witchHealCd decrement, a locked witchHealTgt
that survives between ticks until the throw's setTarget(null)
equivalent clears it, chase-to-10 rangedAttack countdown, then
witchThrowAt picks regeneration/healing on the raider's health.
witchThrow now shares witchThrowAt with the player path. Entity save
map writes Wave/CanJoinRaid/RaidId for raiders and load rejoins the
raid by id.

TestWitchHealParity on both legs: village + bad_omen raid, bare
witch summon + tagged NoAI pillager, then damage the pillager to 3hp.
Both servers threw splash_potions carrying potion_contents ids for
regeneration while the pillager was healthy and healing once below
4 (van [h31 h31 h24 h24], mc-go [h31 h24]).

Test-harness lesson baked in: a scripted client must ack
chunk_batch_done AND echo its position back on teleport or vanilla
never moves the server-side entity and chunk watching never reaches
the village — the bed claim then silently never happens.

## Slice 2026-09-24h — per-damage-type hurt sounds + thorns secondary

Player.getHurtSound dispatches on the damage_type's `effects` field,
not the type name — DamageEffects: HURT/THORNS→player_hurt,
DROWNING→hurt_drown, BURNING→hurt_on_fire, POKING→hurt_berry_bush,
FREEZING→hurt_freeze (measured: Player.getHurtSound =
`source.type().effects().sound()`; mobs' getHurtSound ignores the
source). damageTypeEffects in damage.go now mirrors the datapack
field — mc-go previously only special-cased drown/freeze/berry, so
campfire/fireball/hot_floor/lava/sulfur_cube_hot/unattributed_fireball
played the generic hurt. hurt() also runs playSecondaryHurtSound
after the hurt voice on every accepted hit: DamageTypes.THORNS →
enchant.thorns.hit (id 1622) on PLAYERS source for players, HOSTILE
for mobs, flat vol/pitch, and NOT excluded for the victim.

The hurt voice packet is a positional `sound` (0x75) on PLAYERS
source broadcast to everyone EXCEPT the victim (client synthesizes
locally); reach is volume*16 = 16 blocks, so test players must be
pinned together — spawn scatter in a fresh void world can exceed it.

TestDamageTypeHurtSoundParity: 14 /damage types on a victim while an
observer session collects holder ids — asserts vanilla's map AND
mc-go's both equal the expected table (identical output on both legs).

Open items still ahead: RestrictSunGoal wander bias, online mode,
redstone, terrain gen, lighting.

## Slice 2026-09-24i — RestrictSunGoal / avoidSun path trim

Measured: RestrictSunGoal lives only on AbstractSkeleton (skeleton,
stray, bogged, wither_skeleton); canUse = isBrightOutside + bare head
+ ground navigation, and start/stop just toggle
GroundPathNavigation.avoidSun. The bite is in trimPath: while the
mob's own position (y+0.5 block) can't see sky, every computed path
is walked node-by-node and truncated at the first sky-visible node —
a shaded skeleton never paths into the sun. Once sunlit, no trim at
all (it can still reach shade, and FleeSunGoal owns the burn-driven
dash).

mc-go has no path graph, so mobMoveStep refuses the same crossing:
sun-shy skeleton type + bright + bare head + currently shaded +
next-step block sees sky → skip the step and re-aim the heading.

TestRestrictSunParity roofs a 13x13 footprint over a summoned
skeleton at noon; after 25s of wandering both legs report Pos inside
the shaded blocks (van 1.8,4.2 / mc-go 1.3,0.1 — block x/z in -6..6
never sees sky, center bounded to |coord| < 7.9).

Open items still ahead: online mode, redstone, terrain gen, lighting.

## Slice 2026-09-24j — golem construction patterns + snow trail

CarvedPumpkinBlock.onPlace (every block write, /setblock included)
tries three patterns in order — snow "^,#,#" (pumpkin over two
snow_block), iron "~^~,###,~#~" (pumpkin apex over a 3-iron crossbar
plus one below, ~ = real air, crossbar along X or Z), copper
"^,#" over the #minecraft:copper tag. A match airs every cell with a
2001 block-break level event, spawns TRIGGERED at the bottom-cell
anchor (+0.5/+0.05/+0.5) — copper instead replacing its block with
the matching-weathering copper_chest and spawning atop it.
WitherSkullBlock.checkSpawn instead hangs off setPlacedBy: only an
item-place of wither_skeleton_[wall_]skull completing "^^^,###,~#~"
(### = #wither_summon_base_blocks, soul center below) summons the
wither at +0.55 — /setblock'd Ts stay silent.

SnowGolem.aiStep (no isEffectiveAi gate, so NoAI too): the
mobGriefing-gated trail drops snow_layer in any air feet cell where
a layer survives (solid below — no onGround check in 26.2), and the
gameplay/snow_golem_melts env attribute (hot biomes: desert, both
savannas, all badlands; plus the whole nether) ticks 1 on_fire
damage.

mc-go: golem.go dispatches from writeOnPlace (pumpkins) and the
item-place path (wither skulls); snowGolemStep rides the every-tick
mob region beside the daylight burn.

TestGolemBuildParity builds all four structures per leg via
/setblock, then re-places the last wither skull through a real
IDPlayUseItemOnSB — 12-field parity incl. chest swap and the
setblock silence. The suite's build region is air-filled first
(generated terrain puts grass where `~` needs air) and the snow
golem is caged — its own trail otherwise spoils sibling patterns.

Open items still ahead: online mode, redstone, terrain gen,
lighting.

## Slice 2026-09-24k — mob fall damage (causeFallDamage)

LivingEntity.causeFallDamage: floor((fallDistance + 1e-6 -
safe_fall_distance) * fall_damage_multiplier) — both attributes read
from the entityAttrBases supplier table — lands as minecraft:fall
damage with the full hurt tail. #fall_damage_immune (the 18-type
tag: golems, wither, flyers, slime family) skips outright. The fall
sound is getFallDamageSound — damage > 4 picks big — over the
type's Fallsounds pair: Monsters emit entity.hostile.small/big_fall,
everyone else entity.generic.small/big_fall; Entity.playSound's
coordinate overload makes it the positional 0x75 packet. Water
contact resets fallDistance silently (vanilla zeroes it per tick in
fluid). Note: LivingEntity.aiStep gates travel() itself on
isEffectiveAi — a NoAI mob is fully physics-frozen on vanilla, it
never falls.

mc-go: mobGrounded (now a Server method, also shared by patrol)
accumulates fallDistance per downward step, folds the final residual
to the floor top in on the landing edge, then mobFallDamage runs
the measured tail. playBlockFallSound (the landed block's
SoundType.fallSound) stays open — needs a block→SoundType table.

TestMobFallDamageParity: tagged zombie at 4 (dmg 1, hostile small),
zombie at 10 (dmg 7, hostile big), pig at 10 (dmg 7, generic big),
chicken at 10 (immune) — Health + captured 0x75 holder ids identical
on both legs.

Open items still ahead: block→SoundType fall-sound table, online
mode, redstone, terrain gen, lighting.

## Slice 2026-09-24l — entity cramming (max_entity_cramming)

Vanilla 26.2 (LivingEntity.pushEntities, called from aiStep — no
isEffectiveAi gate, so NoAI mobs still cramp): each living tick a mob
counts pushable entities whose bounding boxes intersect its own
(itself excluded; riding passengers excluded from the count but their
hosts count); above max_entity_cramming-1 neighbors and a 1-in-4 tick
roll, that mob takes cramming 6. A pile therefore thins to at most
the cap; the gate `> max-1` means a pile exactly at the cap is safe,
and max_entity_cramming 0 disables the whole check. doPush's shove-
apart follows in the same method (not implemented — cages hold test
piles together).

mc-go: mobCrammingStep beside snowGolemStep in the every-living-tick
region — AABB overlap via mobBoundsTable, count>cap-1 && 1-in-4 →
damageMob(e, 6, "cramming").

TestEntityCrammingParity: three 1×1 stone cells each stacked with 8
NoAI chickens (0.4×0.7 boxes all intersect at one point) under caps
4 / 8 / 0 → survivors 4 / 8 / 8 on vanilla AND mc-go. Counts read
back via `execute store result storage z:cram n int 1 run execute if
entity @e[tag=…]` + `data get storage z:cram n` (translate key
commands.data.storage.query).

Open items still ahead: block→SoundType fall-sound table, online
mode, redstone, terrain gen, lighting.

## Slice 2026-09-24m — block fall-sound table + fallOn dispatch

Vanilla 26.2 (Entity.checkFallDamage → Block.fallOn →
LivingEntity.causeFallDamage): a landing mob that takes >0 damage
emits its Fallsounds pair, then playBlockFallSound — the support
block's SoundType.fallSound positionally at vol*0.5, pitch*0.75.
SoundType ctor args are (vol, pitch, break, step, place, hit, fall);
the per-block table came out of Blocks.<clinit>: scan each register
window (BlockItemIds/BlockIds opener → Blocks.FIELD putstatic) for
the last SoundType getstatic, resolve ofFullCopy/Legacy/ShallowCopy
and the colour/copper collections (ColorCollection,
WeatheringCopperCollection) through their props lambdas via the
BootstrapMethods table. All 1196 registry blocks landed →
tools/genfallsounds + testdata/vanilla-26.2/block_fall_sounds.tsv →
blockfalls_gen.go (blockFallSoundNames, blockFallSoundScale for the
non-1.0 vol/pitch exceptions like ANVIL's 0.3, blockFallSounds
resolved via soundEventIDs). Quirks verified by javap: doors/
pressure plates keep STONE (open/close sounds ride on
BlockSetType), candle_cake→WOOL, water/lava/bubble→EMPTY.

Block.fallOn overrides measured too: slime nullifies the landing
(causeFallDamage mult 0 → silent), honey slides + 0.2 + replays its
fall sound when causeFallDamage returns true, hay 0.2, beds halve
the fallDistance, an up-tip dripstone impales with
(d+2.5, mult 2, stalagmite), powder snow plays only the Fallsounds
pair (small<7) with no damage — turtle-egg trample and farmland
trample deferred (GOAL). Player landing (applyFallDamage) got the
same generic Fallsounds pair + block fall sound it was missing.

TestBlockFallSoundParity: normal-AI zombies (NoAI is fully
physics-frozen — they never fall) dropped ~9 blocks onto single-cell
strips in 1x2 stone cages spaced 2 apart, P centred so every column
sits inside vanilla's max(vol*16,16) sound broadcast radius. Caught
per column: Fallsounds pair + the strip's fall id, vol, pitch, count
(honey = 2 + slide), slime silent. Test pitfalls recorded in-file:
await keys must match the *.success translate (commands.fill.failed
/ summon.failed* contain the command name), and sink.take() mid-test
drains packet dumps so fills/summons stash every drained line.

Known remaining delta: stalagmite damage — vanilla lands on the tip
top and deals (9+2.5-3)*2=17 while mc-go stops at the integer floor
and dealt 8 (probe visible in the test log); damage amount parity
for thin blocks is part of the larger collision-height gap.

Open items still ahead: online mode, redstone, terrain gen,
lighting.

## Slice 2026-09-24n — redstone signal graph (sources + consumers)

Measured the 26.2 `SignalGetter` model off the jar and implemented it
in internal/server/redstone.go:

- `getSignal(N, dir)` = `state(N).getSignal(N, dir)` (ownSignal minus
  per-source direction carve-outs) or, when N is a redstone
  conductor, `getDirectSignalTo(N)` — the strong power pushed into N.
  `hasNeighborSignal(P)` ORs that over the six directions;
  `hasSignal(P, d)` evaluates a single one (the torch's check).
- ownSignal sources: redstone_block 15; wire/daylight `power` prop;
  plates power|powered; lit torches 15; powered levers/buttons/
  observer/lectern/lightning_rod/hooks/sculk sensors/detector rail 15.
- getSignal carve-outs: floor torch emits 0 toward DOWN (never powers
  its mount), wall torch 0 toward facing; wire emits 0 DOWN, full UP,
  and horizontally only where the `side[dir.opposite]` prop is
  side|up.
- getDirectSignal (strong power): lever/button 15 only toward their
  pointing direction (getConnectedDirection = opposite the mount, so
  only the mount's query matches), plates UP, torches DOWN
  (powers the block above), wire = getSignal.
- Consumer dispatch (`rsNeighborChanged`, fanned out from
  setBlockAndBroadcastNotify + the face-control updateNeighbours):
  doors/trapdoors/gates keep explodeNeighborChanged's powered+open
  sync (now sensing the real signal model), lamp latches lit
  instantly and unlatches on a 4-tick tick, powered/activator/
  detector rails sync powered, torches schedule a 2-tick toggle when
  lit==signal, TNT primes.
- Torch place/remove fires notifyNeighbors — updateNeighborsAt at
  every neighbour of the torch, which reaches back and re-triggers
  the torch itself (that's how a torch setblock'd onto a powered
  mount turns off).
- Hand use: lever/button/door/trapdoor/gate useWithoutItem wired in
  handleUseItem — shares rsButtonPress / rsLeverPull /
  rsToggleOpenable with the wind-charge trigger path (explosion.go
  emit table deleted; explodeEmitSignal/explodeHasNeighborSignal
  call sites all read the rs model).
- isRedstoneConductor approximated by name (vanilla's flag ≈
  canOcclude): transparent solids + non-full shapes excluded.

Deferred: wire power propagation (`updatePowerStrength` /
ExperimentalRedstoneWireEvaluator), rail 9-chain, torch burnout
(RECENT_TOGGLES, 1502 event + 160t restart), comparator output
fanout (`updateNeighbourForOutputSignal`).

TestRedstoneParity pins all of it: 18 probes covering instant/4t
lamp latch+unlatch, torch quirk (powered mount alone doesn't wake a
2-away torch), torch self-unlatch through the ring-2 notify, iron
door, rail, TNT priming, and hand-press lever + 20t stone button
release — all matching vanilla.

## Slice 2026-09-24o — redstone wire power + connection model + update queue

Measured DefaultRedstoneWireEvaluator / RedStoneWireBlock off the jar
and implemented:

- updatePowerStrength (`rsWireUpdate`): target = max(best non-wire
  neighbour signal with dust silenced via shouldSignal, best
  neighbouring wire power −1); flag-2 power write only while the cell
  still holds the wire, then updateNeighborsAt on pos plus all six
  neighbours.
- getIncomingWireSignal: same-level wires, the wire above a
  conducting neighbour (climb), and the wire below a non-conducting
  one (drop) — decayed one per cell.
- getConnectingSide / getConnectionState (`rsConnectingSide` /
  `rsWireFold`): up-arm when the wire can climb a sturdy neighbour,
  side-arms toward connectables (wire, repeater facing its axis,
  observer facing the wire, any signal source), plus the
  survivor-arm quirk — a wire with exactly one connected axis sprouts
  the opposite arm when the perpendicular pair is empty.
- RedStoneWireBlock overrides: updateShape (DOWN pops over no
  support, UP refolds, horizontal re-arms or refolds),
  updateIndirectNeighbourShapes (the diagonal corner-wire fold),
  onPlace (power recompute + vertical neighbours +
  updateNeighborsOfNeighboringWires), affectNeighborsAfterRemoval.
- `rsPending`/`rsWriteDepth`: inside a block write,
  updateNeighborsAt fan-outs queue instead of running synchronously —
  vanilla's CollectingNeighborUpdater runs chained updates only after
  the write's own neighbour pass and updateNeighbourShapes fold —
  and the outermost setBlockAndBroadcastNotify drains them FIFO.
  This is what lets a lever write fold a dot dust's survivor arm to
  SIDE before the dust's power change reaches the rail next to it.
- `rsWriteBlock` gives the same layered ordering to writes outside
  the shared path: lever/button/door hand flips and the scheduled
  torch/lamp/button toggles now run updateNeighborsAt +
  updateNeighbourShapes + the attached-mount ring, so a hand flip
  folds dust arms just like a setblock does.
- Default wire state (setblock) stays a dot; hand-place folds from
  crossState via getStateForPlacement.

Deferred: rail 9-chain (findPoweredRailSignal), torch burnout,
comparator output fanout, `useExperimentalEvaluator`.

TestRedstoneWireParity pins it — 23 probes covering decay over a
15-chain, lamp at chain end, dot→cross folding both orders, wire
climbing a ledge, mount-powered drop, power collapse on removal,
corner-arm fold, rail lit via the survivor arm (both setblock
orderings), hand-placed cross, and a hand-flipped lever folding the
dot before powering the rail — all matching vanilla.

## Slice 2026-09-24p — redstone diodes (RepeaterBlock + DiodeBlock)

Measured DiodeBlock/RepeaterBlock off the jar (javap + live probes on
the 26.2 dedicated server) and implemented:

- Scheduling model: checkTickOnNeighbor arms the powered flip at
  getDelay (DELAY×2 for repeater) when unlocked and powered !=
  shouldTurnOn and !willTickThisTick; priority is EXTREMELY_HIGH when
  the front diode faces a different direction (shouldPrioritize),
  VERY_HIGH when turning off, HIGH when turning on. The scheduler now
  carries a per-entry priority ordering the poll batch (keep-earliest
  dedup retained; latest priority wins).
- tick: locked diodes sit still; powered&&!shouldOn writes
  powered=false, !powered writes powered=true and re-arms at VERY_HIGH
  when the input died mid-delay. Both flips are flag-2 writes
  (`rsWriteBlockFlag2`): broadcast + onPlace + the shape fold, no
  updateNeighborsAt ring — the diode's own onPlace announces only its
  output side.
- Emission: getSignal/getDirectSignal are dir-gated FACING==dir →
  powered?15:0 (FACING names the INPUT side; output is FACING.opp).
  getInputSignal = getSignal(input, FACING) with the wire-POWER
  fallback; getAlternateSignal/max-of-cw+ccw getControlInputSignal with
  sideInputDiodesOnly for the repeater's isLocked.
- updateNeighborsInFront runs on EVERY write (onPlace fires on the
  powered flip too — that is how the front dust learns the output
  changed) and on removal (affectNeighborsAfterRemoval, old facing).
  The pending-update queue now carries single-position NCs and a
  skip-direction for updateNeighborsAtExceptFromFacing, and the NC
  ring iterates vanilla's UPDATE_ORDER (W,E,D,U,N,S).
- neighborChanged: re-checks the live state (is(this)), pops via
  dropResources-equivalent + removeBlock + the six updateNeighborsAt
  rings when the support below isn't sturdy, else checkTickOnNeighbor.
- RepeaterBlock.updateShape: DOWN against a lost support pops;
  off-axis changes silently rewrite LOCKED (the display prop — the
  client folds it too, so no broadcast).
- Placement: getStateForPlacement sets facing=look.opposite and
  LOCKED=isLocked; setPlacedBy schedules the 1-tick flip when the
  input is already powered; useWithoutItem cycles DELAY (mayBuild
  gate: spectator/adventure pass through).
- Fixture-range finding: far-away setblocks fail `argument.pos.
  unloaded` silently and un-ticked chunks never fire scheduled ticks —
  blackbox fixtures must sit inside ticking range (near spawn works).

TestRepeaterParity pins it — 19 probes covering the wake-by-NC flip,
dust power on the output, the no-self-NC setblock quirk, the
output-side non-input gate, locking/unlocking through a side diode,
support-loss pop, and the hand-use delay cycle — all matching vanilla.

Deferred: comparator's analog half (output prop, container read,
updateNeighbourForOutputSignal) shares the diode shell.

## Slice 2026-09-24q — comparators (analog redstone)

Measured on vanilla 26.2 (javap + live probes): ComparatorBlock on top
of the DiodeBlock shell.

- The output lives on the block entity (ComparatorBlockEntity.output,
  persisted as OutputSignal NBT): getSignal emits it only while
  powered, only toward the output face. mc-go's per-dim
  comparatorOut map stands in.
- checkTickOnNeighbor (comparator's own): willTickThisTick→return;
  schedule +2 at HIGH (shouldPrioritize — front is a diode pointing
  elsewhere) or NORMAL when calc != stored output OR powered !=
  shouldTurnOn. tick = refreshOutputState: store calc, flag-2 flip
  powered when it disagrees with shouldTurnOn, then
  updateNeighborsInFront — skipped wholesale when calc is unchanged
  in COMPARE mode.
- getInputSignal override: the input cell's own
  getAnalogOutputSignal replaces the plain signal, or — when the
  input is a non-analog conductor — the same read one cell deeper
  (item-frame entity read not implemented; mc-go has no item frames).
- getAnalogOutputSignal by block: container fill floor(avgFill*14)+1
  over the chest/furnace/hopper/generic-container stores, composter
  LEVEL, cake (7-bites)*2, respawn_anchor charges*15/4, layered
  cauldrons LEVEL, lava_cauldron 3, bee nest/hive HONEY_LEVEL,
  end_portal_frame eye?15:0, crafter's occupied|disabled slot count,
  lectern book slot. Jukebox/chiseled_bookshelf/decorated_pot/
  command blocks have the analog capability flag but read 0 (contents
  not modelled).
- updateNeighbourForOutputSignal: on flag-1 writes where the new
  state hasAnalogOutputSignal, and on removal of the container set
  (Containers.updateNeighboursAfterDestroy — the comparator two cells
  deep behind a conductor learns a chest break), wake the comparator
  facing the cell horizontally or one deeper through a conductor.
  BlockEntity.setChanged does the same for content writes — wired
  through setMenuStack, /item replace, hopper push+pull, furnace
  smelt, brew completion, crafter eject.
- shouldTurnOn: input>alt, or input==alt in COMPARE; side inputs use
  the full getControlInputSignal (sideInputDiodesOnly=false — a plain
  dust chain feeds it, unlike the repeater's lock read).
- useWithoutItem: !mayBuild→PASS, else cycle MODE flag-2,
  comparator_click 0.3 at pitch 0.55 (subtract) / 0.5 (compare), then
  refreshOutputState in place. updateShape keeps only the DOWN
  support-loss pop (no LOCKED).
- setPlacedBy dispatches on the override: comparators use their own
  shouldTurnOn for the 1-tick first-arm.

TestComparatorParity pins it — 19 probes: powered passthrough,
composter level, COMPARE 15-vs-10, SUBTRACT side-wins dark, the
conductor deep read through stone, chest-fill wake via /item replace
(block-entity setChanged — no block write at all), the hand-use mode
cycle recomputing 15→5 in place, full-chest 15, and the removal wake
unpowering a comparator behind a conductor — all matching vanilla.

## Slice 2026-09-24r — pistons (PistonBaseBlock + PistonStructureResolver)

Measured on the 26.2 jar (javap: PistonBaseBlock, PistonHeadBlock,
MovingPistonBlock, PistonStructureResolver, SignalGetter) and pinned by
TestPistonParity against both servers — 44 probes.

- checkIfExtend: hasNeighborSignal is getNeighborSignal —
  quasi-connectivity. The piston reads every neighbour except its own
  facing at cell level, its own cell with dir=DOWN (the conductor
  strong-power read — a piston is a conductor), then the ring around
  pos.above skipping DOWN. A power source two cells up fires it only
  after some neighbour update lands — the QC latch is pinned: power
  removed without a touch leaves it extended.
- Extend/contract are block events drained at tick tail. The trigger
  re-reads power at fire time: a stale extend under lost power is
  dropped, a stale contract under power re-latches EXTENDED.
- The resolver walks push chains with the 12-block cap
  (MAX_PUSH_DEPTH), DESTROY fronts (torch pops, head lands on the
  cell), BLOCK vetoes (obsidian, crying obsidian, respawn anchors,
  reinforced deepslate, unbreakable-destroySpeed, any block entity —
  chest stays), PUSH_ONLY glazed terracotta (pushes but never pulls),
  and slime/honey branching: sticky blocks drag every perpendicular
  neighbour that sticks, honey never sticks to slime.
- moveBlocks(extend=false) clears the front piston_head before the
  pull resolver runs — without that the head vetoes the pull.
- Writes land directly (no moving_piston intermediates — the settle
  state is identical at probe granularity; the client-side animation,
  entity pushing, and the block_event packet stay on the deferred
  list).
- PistonHeadBlock.canSurvive: the head pops when the cell behind is
  no longer an extended piston of its type facing its way.
- getStateForPlacement: facing = getNearest of the look direction
  flipped — looking down places an up piston (dirOpposite gained the
  vertical cases for this).

TestPistonParity: extend+head, push-1, obsidian/chest/extended-piston
vetoes, torch destroy, 12-vs-13 cap, sticky pull, terracotta no-pull,
slime branch+drag, slime/honey non-stick, QC power + latch + retract,
piston-pushes-piston, and hand placement facing — all matching.

## Slice 2026-09-24s — powered rail 9-chain (findPoweredRailSignal)

Measured on the jar (PoweredRailBlock, PoweredRailBlock$1 switch,
DetectorRailBlock) and pinned by TestPoweredRailChainParity — 13 probes.

- updateState: powered = hasNeighborSignal(pos) || chain(true,0) ||
  chain(false,0). On a flip vanilla setBlock's flag-3 fan-out runs
  plus updateNeighborsAt(below) and — for a slope shape —
  updateNeighborsAt(above).
- findPoweredRailSignal steps one cell along the track's own axis per
  link, capped at 8 hops (the lit rail up to 8 away counts). A level
  step checks the moved-to cell AND the cell below it — that is how a
  chain follows a rail that descends one block — while an ascending
  step climbs and checks only the elevated cell.
- isSameRailWithPower: the link must be the same block (powered_rail
  never hops through activator_rail), on a compatible axis (an
  EW-axis walk rejects north_south and the NS ascending shapes), and
  already powered — then it either carries a direct neighbour signal
  or the walk continues through it.
- DetectorRailBlock.neighborChanged is only checkPressed (the
  minecart AABB scan) — an adjacent lever does nothing; the earlier
  mc-go code wrongly let a direct signal light it.

Probes: direct-lit source, links 7 and 8 lit, links 9 and 10 dark,
the flat→ascending→elevated climb staying lit both directions of the
walk, the collapse wave on power removal, and the detector-rail
negative — all matching vanilla.

Deferred still: torch burnout (RECENT_TOGGLES), piston moving-block
intermediates + entity push, useExperimentalEvaluator.

## Slice 2026-09-24t — redstone torch burnout (RECENT_TOGGLES)

Measured on the jar (RedstoneTorchBlock.tick + isToggledTooFrequently,
PoweredRailBlock already done) and pinned by TestTorchBurnoutParity.

- RECENT_TOGGLES is one shared list per level (WeakHashMap keyed on
  Level); toggles older than RECENT_TOGGLE_TIMER=60 prune lazily from
  the head on every torch tick.
- Only the lit→dark transition records a toggle. When the count at
  the torch's pos reaches MAX_RECENT_TOGGLES=8, the off-flip also
  fires levelEvent 1502 (fizz+smoke) and re-arms a
  RESTART_DELAY=160-tick tick.
- The dark→lit path checks the count without recording — a saturated
  window refuses the relight even unpowered. Once the 60-tick window
  has drained the toggles, the restart tick relights the torch.
- Pin: 8 mount-cell power flips (stone<->redstone_block under a floor
  torch) inside the window → dark and refused relight while the window
  holds; a control torch toggled 3 times relights immediately; the
  burnt-out torch relights on its own ~160t later — both servers
  match.

## Slice 2026-09-24u — observers (ObserverBlock)

Measured on the jar (ObserverBlock + BlockInput.place +
updateFromNeighbourShapes) and pinned by TestObserverParity
(22 probes, both servers).

- updateShape arms the 2-tick pulse: `dir == FACING && !POWERED` →
  startSignal → scheduleTick(+2), dedup'd by hasScheduledTick.
- tick(): powered → flag-2 write powered=false; unpowered → powered=true
  + re-arm the off-tick +2. Both flips then updateNeighborsInFront:
  neighborChanged on the back cell (pos - facing) + the
  updateNeighborsAtExceptFromFacing ring there skipping the facing dir.
- getSignal/getDirectSignal emit ownSignal (15 when powered) only
  toward dir==FACING — the consumer sits behind the sensor; the back
  cell is strongly powered, so the pulse drives a conductor.
- Placement arm asymmetry: BlockInput.place (setblock/fill/structure
  writes) folds the REQUESTED state through updateFromNeighbourShapes
  — the dir==FACING self-fold arms the pulse, so a setblock'd observer
  always fires once, while a powered=true request never arms (the fold
  gates on the parsed prop before onPlace runs). Hand placement is
  placeBlock → setBlock(11) with NO self-fold — a placed observer does
  not pulse.
- onPlace: powered=true write over a non-observer + no queued tick →
  snaps to powered=false (flag 18) + updateNeighborsInFront.
  affectNeighborsAfterRemoval: powered observer replaced mid-pulse
  notifies the back cell once.
- mc-go: arm lives in checkSurvival (the updateShape pass) + the
  command-write arm in writeOnPlace keyed on commandWrite reading the
  requested powered prop; tick arm in blocktick.go; emission carved in
  rsGetSignal/rsGetDirectSignal; tickScheduler.hasScheduled added.
- Witness trick: a south piston at the back cell pushes a stone — the
  pushed chain (stone landing two/three cells out, head popped,
  observer dark at settle) is the durable pulse witness.

Deferred: none new — observers complete.

## Slice 2026-09-24v — dispensers + droppers (DispenserBlock/DropperBlock)

Measured on the jar (DispenserBlock, DropperBlock,
DefaultDispenseItemBehavior, HopperBlockEntity.addItem) and pinned by
TestDispenserDropperParity (19 probes, both servers).

- neighborChanged (shared by DropperBlock): powered =
  hasNeighborSignal(pos) || hasNeighborSignal(pos.above()) — the QC
  cell — and a rising edge while triggered=false latches TRIGGERED and
  scheduleTick(+4); the falling edge clears TRIGGERED. Both writes are
  flag-2 (no neighbour fan-out).
- tick → dispenseFrom: getRandomSlot picks uniformly among non-empty
  slots; an empty store only fires the 1001 click. Otherwise one item
  leaves the slot.
- Dropper override: a container at the mouth eats the item via
  HopperBlockEntity.addItem (sided insert, silent) — mc-go reuses
  hopperInsert; no container → the default entity throw.
- Dispenser default: DefaultDispenseItemBehavior throws one item as an
  entity at the mouth + levelEvent 1000 (click) and 2000 (stream
  animation). Per-item behaviors (projectiles, buckets, boats, spawn
  eggs, …) deferred to the dispense-behavior registry slice.
- QC caveat measured live: a source two cells up only powers the
  device when a separate update reaches it; the latch then persists
  through the source's removal until another update lands.
- Adjacent fix pinned by the same test: LevelChunk.setBlockState
  early-outs on an identical state — a stone→stone setblock carries no
  update. setBlockAndBroadcastNotify now skips broadcast/neighbour
  work when the folded result equals the old state (self-fold still
  runs, matching updateFromNeighbourShapes ordering).

## Slice 2026-09-24w — dispense projectile behaviors (ProjectileDispenseBehavior)

Measured on the jar (DispenseItemBehavior.bootStrap,
ProjectileDispenseBehavior, ProjectileItem$DispenseConfig) and pinned
by TestDispenseProjectileParity (23 probes, both servers).

- Projectile items spawn their entity at pos.center + facing*0.7 +
  (0,0.1,0) shot along the facing at power 1.1 (uncertainty jitter
  0.0172275*uncertainty, triangle); sound is levelEvent 1002 with
  per-item overrides (fire_charge/wind_charge 1018, rocket 1004).
- Item → entity map: arrow/tipped_arrow→arrow, spectral_arrow→
  spectral_arrow, egg variants→egg, snowball, experience_bottle,
  splash_potion/lingering_potion, fire_charge→small_fireball,
  wind_charge, firework_rocket — DispenseItemBehavior$1 covers
  armor_stand (spawns in the mouth cell, yaw = dispenser facing).
- arrowTickStep gained the whole family: thrown items use gravity
  0.03, arrows/trident 0.05, hurting projectiles + rockets 0; arrows
  stick on block hit, everything else despawns. Firework rockets
  despawn on a 40-tick fuse. Egg chick-hatch, lingering clouds,
  fireball ignite and thrown-entity damage stay deferred.
- Fixes found by the differential: itemTickStep keyed on
  `Item.ID != 0`, so any entity carrying an item (all the thrown
  projectiles) got dragged into item physics and froze on a block —
  now gated on the item/xp-orb entity types. (Same class as the
  identical-setblock early-out shipped last slice.)

## Slice 2026-09-24x — dispense behaviors: buckets, minecarts, boats, flint&steel

More of `DispenseItemBehavior.bootStrap`, all pinned by
`TestDispenseBehaviorParity` (z=130 fixtures, 20 probes both sides):
- DispenseItemBehavior$3 (`DispensibleContainerItem`):
  water/lava/powder_snow/fish buckets place their fluid block at the
  mouth and consume-with-remainder to an empty `bucket` (remainder
  inserts or throws). Fish buckets' mob half is deferred.
- DispenseItemBehavior$4 (`bucket`): BucketPickup at the mouth —
  water/lava source (level=0) → the filled bucket, `powder_snow` →
  powder_snow_bucket. Reflood from neighbours is real flowing water,
  not the source — pinned via `!water[level=0]`.
- MinecartDispenseItemBehavior (6 cart items): the mouth or the
  cell below it must be a rail (flat → y+0.1, slope → +0.6 /
  below-rail → −0.9/−0.4); anything else falls back to the item
  throw.
- BoatDispenseItemBehavior (21 boats/rafts): the mouth must be
  water, or air over water — dry mouth falls back to the throw.
- FlintAndSteelDispenseItemBehavior: TNT at the mouth primes in
  place; else fire where it can burn; else `lit=true` on a
  campfire/candle; else silent fail (the item stays).

## Slice 2026-09-24y — dispense behaviors: tnt, spawn eggs, shulker box

- DispenseItemBehavior$6 (tnt): primed-tnt entity at the mouth
  centre — the item never throws; `tnt_explodes=false` fails it
  silently. A solid mouth still spawns the entity (measured).
- SpawnEggItemBehavior (`*_spawn_egg`): the mob spawns at the mouth
  cell, slot drains.
- ShulkerBoxDispenseBehavior (`*_shulker_box`): places the box as a
  block at a replaceable mouth; solid mouth fails silently.
- Same `TestDispenseBehaviorParity` run, now 27 probes.

## Slice 2026-09-24z — dispense behaviors: bottle/anchor/wax/shear

- DispenseItemBehavior$9 (glass_bottle): water source at the mouth →
  `potion` with potion_contents=water. The cell refloods from
  neighbour flow (probes pin the slot, not the cell — reflow timing
  differs under mc-go's fluid engine; deferred).
- DispenseItemBehavior$10 (glowstone): respawn_anchor at the mouth
  charges +1 (max 4) and shrinks the stack.
- DispenseItemBehavior$12 (honeycomb): waxes any copper-family
  block (waxed_ prefix variant).
- ShearsDispenseItemBehavior: full beehive/bee_nest → honey_level 0
  + 3 honeycomb drop (entity-shear path deferred).
- TestDispenseBehaviorParity now 33 probes.

## Slice 2026-09-25a — dispense: potion→mud; test row power hardening

- DispenseItemBehavior$13: a `potion_contents=water` potion at a
  convertable_to_mud mouth (dirt/coarse/rooted) → `mud` +
  glass_bottle remainder. Other potions fall back to the throw.
- The z=150 fixture row powers from the floor cell (y=-61) — mouth
  water otherwise floods the y=-60 power cells on vanilla.
- Dispense registry remaining deferred: $5 bone meal (needs crop
  growth), $7/$8 golem patterns, $11 brush, chest-saddle,
  EquipmentDispenseItemBehavior (needs mob slot reads for probes),
  shears entity-shear, SulfurCubeBlock.

## Slice 2026-09-25b — projectile-impact aftermath

Thrown-hurt: egg/snowball/experience_bottle now run `thrownCollide` on
entity contact — dmg 0, snowball vs blaze 3, type
`thrown_item_projectile` with a `damageInfo` carrying the shooter's
position. Lingering potions spawn their `area_effect_cloud` on both
entity-hit and block-hit (`spawnLingeringCloud`; its empty-Effects early
return removed — vanilla always emits the cloud). `small_fireball`
block-hit writes plain `fire` into the face cell — gated on
`fireballOwnerLiving` (owner is a living entity + mob_griefing);
dispenser-fired shots have no owner and never ignite (measured: oak_log
west-face and under-log hits both leave air on vanilla). Splash/lingering
dispense now wires `st.Potion` through `splashPotionContents` into
`e.Effects` + `e.splashPotion`. `TestDispenseProjectileParity`: +lingering
fixture (`potion_contents={potion:"minecraft:poison"}`), fire_charge
facing-east + facing-up cells, probes for cloud formation and the
miss/miss ignite rule.

## Slice 2026-09-25c — bone meal dispense ($5)

`bone_meal` now runs `bonemealGrow` at the mouth: CropBlock family
(wheat/carrots/potatoes 7, beetroots 3, melon/pumpkin stems 7,
torchflower_crop 2→morphs to torchflower) advances `age+nextInt(2,5)`
clamped to max; cocoa/sweet_berry_bush/pitcher_crop advance +1 (their
own performBonemeal bodies — measured). Nether wart is NOT
bonemealable (no BonemealableBlock impl — verified in bytecode).
Success consumes + event 1505(front,15) + 1000/2000; a non-target or
max-age front is a silent optional-fail keeping the item (event 1001 —
OptionalDispenseItemBehavior extends Default but its dispenseItem never
throws on fail). Player-use bonemeal (BoneMealItem.useOn) still
unimplemented — same helper, entry point differs. Grass/moss/nylium/
sapling/seagrass bonemeal paths deferred (bigger tables).

## Slice 2026-09-25d — golem-head dispense ($7/$8) + fixture pacing

`carved_pumpkin` and `wither_skeleton_skull` dispense only when the
mouth cell completes a pattern (canSpawnGolem / canSpawnMob): the head
writes silently, the existing `golemOnPlace`/`golemTryWither` hooks run
(`golemOnPlace` now returns whether a mob spawned), and the block is
restored to air on a bare mouth — the head never lands without spawning.
`jack_o_lantern` is NOT registered in vanilla's bootStrap $8 — dispenser
keeps the default throw for it. Blackbox: two down-facing dispensers at
z=140 spawn a snow golem / keep the pumpkin, both pinned.

Root-caused the recurring fixture flake: far-out cells intermittently
never schedule vanilla's dispense (power write lands, +4t tick never
runs). Mitigations: probe client now `tp`s over the fixture field
(view/simulation distance is 8 — far cells sit at the chunk-ticket
edge), power writes are paced 30ms apart, settle 900ms. Saw one mc-go
double-fire (bottle count 0) — suspected `triggered` unlatch race on a
transient unpowered read; not yet reproduced deterministically.

## Slice 2026-09-25e — EquipmentDispenseItemBehavior

Equippable items (armor family, elytra, shield→offhand, carved_pumpkin,
mob heads/skulls) dispense onto the first LivingEntity whose hitbox
intersects the mouth cell — `dispenseEquip` maps item→slot via
`dispenseEquipSlot` (vanilla's getEquipmentSlotForItem table, armor +
head + chest/offhand specials only; saddles/harness and the $2
chest-on-llama path deferred). Vanilla semantics measured: split(1),
overwrite any existing slot, mob targets also gain persistence + a
guaranteed drop (no per-slot drop table in mc-go — deferred). Probe:
pig in the mouth cell wears the iron_helmet (`items entity ... armor.head`).

Test-harness note: fixture commands are now paced 25ms and the probe
client teleports over the fixture field — the far-row cells (z≥140,
x≥~84) sit at the 8-chunk simulation edge and unfired bursts were the
dominant flake. Still residual: rare whole-row unfired on vanilla and
one observed mc-go double-fire (triggered-latch re-arm race suspected,
not yet reproduced in-process).

## Slice 2026-09-25f — shears entity-shear + brush + fireball ignite

DispenseItemBehavior.bootStrap, continued:

- `ShearsDispenseItemBehavior.tryShearEntity`: after the beehive
  branch, a Shearable entity in the mouth AABB is sheared — sheep
  drop 1-3 white_wool (the `shearing/sheep/white` table) and set
  `Entity.Sheared` (new field; no metadata broadcast yet — deferred
  with wool regrowth). Shears take 1 damage (wearContainerSlot).
- `DispenseItemBehavior$11` brush: armadillo in the mouth AABB →
  1 armadillo_scute drop (the `shearing` loot shape) and the brush
  wears by 16/64.
- Bug found by the suite, not by javap: summoned mobs wander out of
  the mouth cell during the powered window — fixtures now summon
  `{NoAI:1b}` (parity convention already used elsewhere).
- `SmallFireball.onHitBlock` re-read: the gate is
  `!(owner instanceof Mob) || mob_griefing` — dispenser shots have NO
  owner so they DO ignite the empty face cell (earlier read had it
  backwards; fixed `fireballIgnites`).
- Fixture lesson: an open water source 4 cells from a rail floods it
  during the ~1s power window (water tickDelay=5, pickup races the
  spread); the bucket-pickup source is now penned on three sides.
- tp recentered to (80,-59,140) — row-3 (z=150) unfired flaking on
  vanilla when centered at 135.

Deferred from bootStrap: llama chest/saddle ($2), mob equip
persistence+guaranteedDrop, shearOffAllLeashConnections, wool
regrow on grass-eat, mooshroom/snow-golem/bogged shear paths.

## Slice 2026-09-25g — bottle honey, saddle equip, chest-on-mount

- `DispenseItemBehavior$9` second half: glass_bottle vs a
  beehive/bee_nest at honey_level>=5 → honey_level reset +
  honey_bottle back into the dispenser (overflow drops). The bee
  release half of releaseBeesAndResetHoneyLevel is deferred — hives
  don't track occupants.
- Saddle: `EquippableComponent(slot=saddle)` via
  EquipmentDispenseItemBehavior — first saddleable mount in the
  mouth cell (pig/strider/horse family, camel) gets Saddle=true.
- `DispenseItemBehavior$2`: chest onto a TAMED chestless
  AbstractChestedHorse (donkey/mule/llama) sets HasChest; untamed
  or already-chested mounts make it keep the item (throw fallthrough
  is the same as before).
- Probe-side fix: `execute if items entity` now reads mob slots via
  readMobSlot (was player-only) — the saddle slot reports the
  Saddle flag as a 1-count stack like vanilla.
- NOTE: `Entity.Sheared` has no metadata write yet — sheared sheep
  still LOOK woolly to clients until the metadata half lands.

Deferred: shearOffAllLeashConnections, wool regrow on grass-eat,
mooshroom/snow-golem/bogged shear paths, happy-ghast harness,
mob equip persistence/guaranteed-drop.

## Slice 2026-09-25h — BoneMealItem.useOn (player-use path)

- `useBonemeal` in the use_item_on chain (after the clicked block's
  own use, before the igniter): held bone_meal + a bonemealable cell
  → `bonemealGrow` (the same crop/cocoa/berry table the dispenser
  $5 drives), `consumeHeld(1)` in survival only, `levelEvent 1505`.
- TestBoneMealUseParity: survival clicks — wheat 6→7 clamp + consume,
  max-age and non-crop clicks consume nothing, empty/stone hands are
  no-ops, nether_wart verified NOT bonemealable on both servers.
- Fixture lessons: vanilla's setblock refuses chunks that are still
  streaming after a tp — fixtures now wait 1.5s post-tp + 60ms per
  write. Wheat needs farmland, wart needs soul_sand — mc-go's
  setblock skipped the support check vanilla enforces (a gap of its
  own, noted).
- Deferred bonemeal targets: sapling→tree, grass-block patch spread,
  moss→moss patch, nylium, seagrass, sea_pickle, pink_petals,
  glow_berry, rooted/watered plants.

## Slice 2026-09-25i — Sheared flag: save/apply/metadata

- `Entity.Sheared` now round-trips: entitySaveMap emits
  `Sheared`+`Color=0` for sheep, applyEntitySave reads `Sheared`
  back (Color ignored — sheep stay white), and the dirty pack
  carries the wool byte when it flips.
- Sheep's DATA_WOOL_ID measured live: index 18, type byte, sheared
  = 0x10 (low nibble = dye color). encodeLivingMetadata emits it
  when Sheared; the dispenser shear path broadcasts the metadata.
- Probe: `entity @e[type=sheep,nbt={Sheared:1b}]` hits on both.

## Slice 2026-09-25j — mob shears, sheep EatBlockGoal, tick-sprint ordering

- Shears on a living entity (dispenser $-behavior AND held-item
  interact) now mirror vanilla's per-type outcomes: snow_golem →
  `Pumpkin:0b` (DATA_PUMPKIN_ID index 16 byte — default on, packs
  only when off) + carved_pumpkin drop; bogged → `sheared:1b`
  (index 16 boolean) + BOGGED_SHEAR loot (2×{brown,red} mushroom);
  mooshroom → convertTo(COW) at pos + 5×red_mushroom; sheep → wool
  drop + Sheared flag. Held-shears path (`shearMob`) fires only
  when BodyItem is empty — saddled/wolf-armored mobs take the
  equipment path instead.
- `handleInteract` gained vanilla's isWithinEntityInteractionRange
  gate: eye→entity-box distance must stay under
  entity_interaction_range (base 3.0) + 3.0 padding — out-of-range
  clicks silently dropped (measured: vanilla ignores ~7-block
  clicks).
- Sheep's EatBlockGoal: 1/1000-tick canUse roll (1/50 for babies)
  gated on an edible_for_sheep block underfoot or a grass_block
  below; start sets a 40-tick anim (entity event 10); at
  adjustedTickDelay(4) the eat lands — underfoot edible destroyed
  or grass→dirt, both mob_griefing-gated — then ate() clears
  Sheared (+60 age for babies). While anim runs the sheep stands
  still. TestSheepEatGrassParity: 6-sheep grass pen vs stone pen,
  3000-tick sprint, 4 probes hit/hit.
- `/summon` reads the shear-family NBT keys where vanilla's
  readAdditionalSaveData does: `Sheared` (sheep), `sheared`
  (bogged), `Pumpkin` (snow_golem; absent → default-on).
- `/tick sprint` runs synchronously on the command path — vanilla
  sprints on the server thread, so the ack leaves first and every
  queued command sees post-sprint state (fixes mid-sprint reads
  in TestTickCountParity). finishTickSprint's report
  (`commands.tick.sprint.report`, ticks-per-second + %.2f ms/tick)
  now broadcasts to every player on completion or early stop —
  tests wait on "Sprint completed" instead of wall-clock guesses.
- Probe lessons: `drainChunkWindow` never terminates under a long
  quiet timeout — vanilla's ~1s time_update heartbeat keeps the
  stream non-quiet, use a fixed bounded read; a stale read-deadline
  armed by a capture loop starves a later chatSink — clear it with
  `SetReadDeadline(time.Time{})` before starting the sink.
- Deferred: shearOffAllLeashConnections (no leash model), bee
  release on honey harvest, remaining bonemeal targets, happy-ghast
  harness, egg chick-hatch, piston entity push + block_event packet.

## Slice 2026-09-25k — /tick wire surface: broadcasts, ops echo, sprint interrupt

- ClientboundTickingStatePacket (0x7F, rate+isFrozen) now broadcasts on
  EVERY setFrozen/setTickRate call — vanilla's updateStateToClients is
  unconditional, so `tick rate 8` twice emits twice; and
  ClientboundTickingStepPacket (0x80, VarInt owed) on every
  stepGameIfPaused/stopStepping. Both also still go to a joiner
  (updateJoiningPlayer) with the real clock values, not constants.
- Frozen restore: requestGameToSprint saves isFrozen (previousIsFrozen)
  and unfreezes for the run; finishTickSprint restores it — a sprint
  started under `tick freeze` ends back frozen ({rate,true} pair) and
  `tick step` works again after.
- sendOpsEcho mirrors CommandSourceStack.sendSuccess(_, broadcastToOps):
  the caller still gets the bare line (gated on send_command_feedback);
  every OTHER op on the wire gets it wrapped as `chat.type.admin`
  [name, message] gray+italic — isOp = ops.json membership, so mc-go's
  isOp reads s.ops directly (opLevel() would lie 4-for-all under the
  open default). The sprint report broadcasts through the server source
  ("Server" as sender name). sendFailure never echoes — verified on
  CommandSourceStack bytecode.
- /tick freeze/unfreeze/rate/step.success/step.stop/sprint.stop all
  reply broadcastToOps (opsEchoReply); tick query does NOT
  (broadcastToOps=false on all three of its sendSuccess calls — bare
  lines to the asker only).
- tick query takes vanilla's 3-line shape: status
  (sprinting|frozen|lagging when nanosecondsPerTick<avg|running) +
  rate line (query.rate.sprinting drops the target arg;
  query.rate.running carries rate/avg/target) + query.percentiles —
  the server now keeps the real 100-entry tickTimesNanos ring
  (recordTick after every GameTick, sprint ticks included; avg = agg /
  min(100,max(idx,1)); percentiles sorted[50],[95],[99], sample=100,
  %.1f ms via float32 — matching nanosToMilisString).
- Sprint/step semantics re-verified against bytecode: remaining>0 is
  the liveness test for both stopSprinting and checkShouldSprintThisTick
  (scheduled>0 is isSprinting); stopSprinting runs finishTickSprint
  synchronously on the caller — mc-go's StopSprint drains sprintMu so
  the report still lands before the stopper's next act.
- TestTickingStateParity (new tests/blackbox/tickstate_test.go): two
  op'd sessions run the whole mutator surface on BOTH servers and diff
  the ordered ticking_state sequence, ticking_step sequence, per-wire
  bare reply keys, and per-wire admin-echo keys — including the
  cross-session cases (B's sprint interrupting A's, B's step-stop
  draining A's frozen step, query mid-sprint). Hit/hit.
- Wire lessons: vanilla's sprint/step run async on the server loop —
  commands interleave BETWEEN sprint ticks, so a marker can return
  mid-sprint and a frozen-only command issued then FAILS; gate on the
  sprint.report broadcast, and let waits also accept a report that
  beat the marker into an earlier drain (waitForKey). mc-go's model
  keeps the sprint on the caller's handler instead — same wire
  ordering, different interleave (queued commands on the OWNING session
  wait out the run; other sessions' commands still interleave).
- Harness: ops.json only lands in vanilla's dir — mc-go needs
  SetOpLevel in the mutate hook for isOp/broadcastToAdmins parity.
- Known diff to revisit: on vanilla a second command on the SAME
  session still interleaves with that session's own sprint ticks
  (single server thread); mc-go blocks the session's handler for the
  run, so same-session commands strictly queue behind it.
- Deferred: shearOffAllLeashConnections, bee release on honey harvest,
  remaining bonemeal targets, happy-ghast harness, egg chick-hatch,
  piston entity push + moving_piston + block_event packet, audit of
  broadcastToOps flags on other commands (time set, gamerule, etc).

## Slice 2026-09-25l — broadcastToOps audit sweep (ops echo parity)

- Measured every vanilla command class's sendSuccess(_,flag) on the
  26.2 jar (javap sweep, ~60 classes): mutations broadcast (66 sites),
  queries don't (31). Mutator replies answer the caller bare AND wrap
  to every other op as chat.type.admin [name: msg] in gray italic.
- Converted ~215 mc-go reply sites to opsEchoReply, reverted ~45 query
  keys, unified the hand-rolled broadcastAdmin/broadcastTeamAdmin onto
  sendOpsEcho semantics — which also fixed three real echo bugs:
  * the send_command_feedback gamerule gate was missing on the manual
    fan-outs, and
  * opLevel()==0 leaked echoes to every player when OpsRequired was off
    (4-for-all); the membership test now reads the ops map (isOp).
- Echo sender name resolves at send time through teamFormattedName —
  vanilla's getDisplayName = decorate + formatNameForTeam, so a
  teammate's echoes carry the team dress (the old team.go resolved the
  name BEFORE mutating team state, which vanilla does not).
- reply.extraEcho []bool: per-line flags — save-all's "saving" stays
  private, "success" echoes (replyEchoLast). RCON's flat-text replies
  now echo to ops as "RCON" via sendOpsEchoNamed.
- Surprise measured on the wire: turning send_command_feedback OFF
  eats the toggle's own caller reply too — acceptsSuccess on a player
  source reads the rule at send time. mc-go matched already.
- TestOpsEchoParity (tests/blackbox/opsecho_test.go) drives two ops +
  a non-op through mutator/query/save-all/team-dress/gamerule legs on
  BOTH servers — green, ~5s per target.
- mc-go harness note: /save-all needs a WorldPath (the mutate hook
  sets a TempDir file; vanilla always has a world dir).

## Slice 2026-09-25m — moving-piston wire surface (block_event + intermediates + entity push)

The full vanilla moving-block protocol, measured by javap AND live
wire capture:

- block_event 0x07 (Pos + b0 + b1 + block registry id): fired at the
  tick-end blockEvent drain when triggerEvent returns true; broadcast
  same-dimension within 64.0 of the piston (PlayerList.broadcast
  radius). b0: extend=0, retract=1, drop-in-flight-head=2; b1: the
  piston facing's get3DDataValue (= rsDir ordinal). Stale-power events
  no-op — trigger re-reads power at fire time, matching triggerEvent.
- moving_piston intermediates are INVISIBLE on the wire: moveBlocks
  writes them with UPDATE_INVISIBLE — no set_block, no beType-11
  block_entity_data. The client re-simulates the whole slide locally
  from the event params alone. mc-go now writes them world-side only
  (d.w.SetBlock) and tracks live movingPiston BEs — never broadcasts.
- Each moved/head cell wears moving_piston[facing,type] carrying the
  arriving state for the 2-tick interlude (progress += 0.5/tick);
  settle = finalTick's unconditional setBlock broadcast at the cell.
- Entity push runs per BE tick: swept hitboxes slide dir*0.5 each
  tick (vanilla's progress increment), clamping to the cell's far face
  when the landing cell is blocked. Positions ride the normal
  entity-sync pass — a fractional push is inside the relative-move
  threshold, so the wire shows two ~2048-dy move_entity_pos packets.
- Extend write order fixed: piston cell latches extended BEFORE the
  move writes (triggerEvent's setBlock), then destroys (level_event
  2001 + air), then mp cells + head mp, then leftover-origin airs.
- Retract pulls ride retracting moving_pistons (facing=opposite);
  resolver-vetoed pulls skip the event entirely (moveBlocks=false);
  TRIGGER_DROP (front mp still extending) deletes the in-flight head
  BE and leaves the cell air.
- TestPistonWireParity (tests/blackbox/pistonwire_test.go) joins a
  client on BOTH servers, decodes the raw packet stream per toggle,
  and pins: exact block_event params (pos/b0/b1/registry id), the
  absence of moving_piston writes + piston BE data, settle writes,
  air at cleared origins, and the two-tick entity push summing ~4096
  dy units. Green on vanilla and mc-go.

## Slice 2026-09-25n — thrown egg/snowball hand-throw + chick hatch gate

- handleUseItemPacket (internal/server/eating.go): the
  block_changed_ack (ackBlockChangesUpTo) now fires unconditionally at
  the top of the handler like vanilla, and use_item dispatches
  egg/blue_egg/brown_egg/snowball to throwHandItem (instant-throw
  items, not hold-uses).
- throwHandItem (internal/server/projectile.go) ports
  EggItem.use/SnowballItem.use: named throw sound at the player
  (entity.egg.throw src PLAYERS, entity.snowball.throw src NEUTRAL;
  vol 0.5, pitch 0.4/(rand*0.4+0.8)), spawn at eyeY-0.1, look-vector +
  per-axis triangle(0,0.0172275) jitter scaled ×1.5, entity yaw/pitch
  derived from the resulting motion, spawn packet data=owner id,
  DATA_ITEM_STACK metadata, consume-1 unless creative/spectator, then
  the arm swing broadcast to other sessions.
- arrowTickStep calls thrownItemImpact at both egg/snowball death
  sites (entity hit and block hit). The 3-status entity_event the
  javap shows inside onHit never reaches the wire — vanilla discards
  the projectile the same tick, so mc-go emits remove_entities alone
  (measured).
- Chick hatch measured: egg 1/8 → one chick, 1/32 → four, Age -24000,
  snapped to the egg pos+yaw, chicken type id 26, CHICKEN_VARIANT
  component from the thrown stack (variant encoding itself still a
  GOAL.md open item).
- The fudgePositionAfterSizeChange veto is REAL on the wire: top/bottom
  face (axis==1) block impacts never hatch — 0/64 measured pitched
  onto flat ground, while side-face (axis 0/2) and entity hits hatch
  ~1/8. implemented as faceVeto on thrownItemImpact.
- NOT a cooldown: the earlier 16/24 use acceptance turned out to be
  max_stack_size 16 emptying the held stack — no use_cooldown
  component exists on egg/snowball in 26.2.
- TestEggThrowParity (tests/blackbox/eggthrow_test.go): leg1 egg wire
  shape (ack seq, named sound, spawn data!=0, item-stack meta, slot
  shrink accepting either sync shape), leg2 snowball, leg3 impact
  remove-without-event, leg4 flat-ground volley pins 0 chicks on both
  servers, leg5 wall-face volley pins the [1,16] hatch band per 48.
  Green on vanilla and mc-go.

## Slice 2026-09-25o — experience bottle throw + orb award

Measured on the 26.2 jar (javap + wire probes):

- ExperienceBottleItem.use: entity.experience_bottle.throw at NEUTRAL,
  vol 0.5, pitch 0.4/(rand*0.4+0.8); spawnProjectileFromRotation with
  xRot offset -20 and power 0.7 (vs 1.5 for egg/snowball).
- ThrownExperienceBottle.getDefaultGravity is 0.07 — the bottle drops
  harder than every other ThrowableItemProjectile (0.03).
- onHit: level_event 2002 at the bottle's block position with data
  -13083194 (the splash color), then xp = 3 + nextInt(5) + nextInt(5)
  (3-11) run through ExperienceOrb.getExperienceValue's denomination
  ladder (>=7->7, >=3->3, else 1 at this scale) and awarded along a
  direction — blockHitResult.getDirection().getUnitVec3() on a block
  hit, -deltaMovement on an entity hit. Each orb's initial velocity is
  (rand*0.2-0.1)*2 on x/z and rand*0.2*2 on y, flipped when it points
  opposite the direction; then discard.
- Orb spawn packet data = 0 (default getAddEntityPacket) — NOT the xp
  value. The value only rides set_entity_data index 8 (serializer 1,
  VarInt). Entity gained OrbValue; the spawn Data field now stays 0.
- Vanilla emits each orb's metadata twice on spawn (bundle + tracker
  refresh) — the test dedupes by entity id.
- Test harness fix: the spawn_entity decoder now skips the variable-
  length LpVec3 velocity field (ReadLpVec3) — a moving entity's data
  field was previously misread (mattered first for orbs, which always
  spawn with nonzero velocity).
- TestXpBottleParity (tests/blackbox/xpbottle_test.go): wall-face and
  ground lobs each pin ack+sound+spawn(data=shooter)+item-meta+consume+
  remove, level_event 2002/-13083194, and 1-3 orbs with data=0 spawns
  whose index-8 values are vanilla denominations summing 3-11. Green
  on vanilla and mc-go. TestEggThrowParity re-run green on both after
  the LpVec3 decoder fix.

## Slice — 2026-09-25p — thrown-item entity-hit hurt(0) + VILLAGER_ANGRY + grace + ack batching

Measured on the 26.2 jar (javap + wire probes):

- ThrowableItemProjectile.onHitEntity calls entity.hurt(source,0) for
  egg/snowball — a zero-damage hit that STILL runs the landed hurt
  tail on the wire: damage_event (damage_type minecraft:thrown = 45,
  cause+1/direct+1 VarInts), the victim's hurt voice, and a knockback
  set_entity_motion. Graced repeats (inside invulnerableTime>10) ship
  nothing: no damage_event, no sound, no motion.
- Villager's hurtServer reputation hook broadcasts VILLAGER_ANGRY
  (entity_event 13) on the victim BEFORE the damage_event when the
  damage source's entity is a player — every landed player-caused
  hit on a villager, not only melee.
- ThrownExperienceBottle never overrides onHitEntity — an entity hit
  only runs onHit's splash (2002 + orbs): NO damage_event, no sound,
  no motion, no ANGRY, no entity_event.
- Egg/Snowball.onHit broadcast entity_event 3 on the projectile on
  entity hits (never on block hits); the xp bottle broadcasts none.
- ServerGamePacketListenerImpl.ackBlockChangesUpTo only raises a
  pending max; the ONE block_changed_ack per tick ships in tick()
  with the highest seq — back-to-back use_item packets produce a
  single ack for the higher seq (mc-go still acks per-packet; the
  suite accepts >= covers).
- Egg impacts can hatch a chicken (spawn etype 26) — preexisting
  mechanic; the hit test tolerates the extra spawn.

mc-go: damageMob's zero-damage branch now runs the full landed tail
(damage_event + knockback + hurt voice) for un-graced hurt(0) hits;
mobVillagerAngry emits entity_event 13 for player-caused villager
hits in both the zero-damage and dealt paths; thrownCollide passes
"thrown"/cause/direct through damageInfo, skips hurt for the xp
bottle entirely, and broadcasts entity_event 3 for all three egg
types + snowball.
TestThrownHitParity (tests/blackbox/thrownhit_test.go): villager
victim — two back-to-back eggs pin one ANGRY-then-damage_event(45,
direct=proj+1)+hurt-sound+knockback vs one graced silent break,
both eggs' entity_event 3 + remove, one covering ack; snowball leg
repeats the full tail; xp-bottle leg pins total hurt-tail absence +
the 2002/orb splash. Green on vanilla and mc-go (the throw spread
makes an occasional villager miss possible on both — inherent RNG,
not a wire diff). TestXpBottleParity + TestEggThrowParity re-run
green; go vet clean; go test ./internal/server green.

## Slice — 2026-09-25q — small_fireball entity-hit (ignite + hurt 5 + graced-restore)

Vanilla (javap + live probe): SmallFireball.onHitEntity saves the
victim's fire ticks, igniteForSeconds(5.0), then hurtServer(
damageSources.fireball(ball, owner), 5.0) — a summoned/ownerless
ball resolves to DamageTypes.UNATTRIBUTED_FIREBALL with the source
entity reporting as the projectile itself (cause+1 = direct+1 =
proj+1 on the wire). hurt() returning FALSE restores the saved fire
ticks (the graced second ball does NOT re-ignite). No entity_event
on the projectile ever; the ball is removed. /summon key is Motion
(power/acceleration_power drive acceleration, not needed for flat
flight).

mc-go: /summon gains a small_fireball arm (entity spawn + Motion —
the projectile loop already ticks it). fireballCollide implements
the measured tail: damageMob(5.0) with typeName
unattributed_fireball + cause=e.ID when ownerless, else fireball +
arrowShooter; SetFireTicks(100) only when the hurt landed
un-graced, else restores the prior ticks; the victim's shared-flags
byte ships once with the final state.
TestThrownHitParity leg 5: two back-to-back summoned fireballs pin
one damage_event etype=47 cause=direct=ball1+1 + the on_fire flag
byte + hurt sound + knockback on ball1, total silence (besides
remove) on the graced ball2, no entity_event on either. The
villager is now summoned floated (NoGravity) so its hitbox centers
on the eye-line (~1.0 margin both ways — spread misses at the old
0.43 top margin let follow-up throws land as fresh hits on both
servers), and hatched chickens are killed before legs 3 and 5 (a
wandering hatchling can eat a projectile — same behavior exists on
vanilla). Green on both servers, two consecutive runs.

## Slice — 2026-09-25r — wind_charge entity-hit (hurt 1.0 + burst-on-hit)

Vanilla (javap): AbstractWindCharge.onHitEntity sets the owner's
lastHurtMob, hurtServer(damageSources.windCharge(charge,
livingOwnerOrNull), 1.0) — source(WIND_CHARGE, charge, owner): an
ownerless summon wires cause 0 and direct = charge+1 — then ALWAYS
explodes at the charge's position, graced hit or not. The burst's
SimpleExplosionDamageCalculator is (explodesBlocks, damagesEntities=
false): entities in the 2r box are pushed but not hurt.

mc-go: windChargeTickStep only burst on block contact and players —
mobs now scan through windChargeHitsMob (living entities clipped by
segmentHitsBox's 0.3-inflated slab test, nearest wins), the mob hurt
runs damageMob(1.0, wind_charge, cause=arrowShooter, direct=charge)
then windBurst at the charge position, and the player arm now runs
the same damageWith(1.0) ahead of its burst (it previously burst
with no hurt at all). damageWith's hurt knockback now anchors on
getSourcePosition — cause falls back to direct, matching vanilla
(ownerless projectiles still shove away from where they hit; the mob
path had this fallback already).
TestThrownHitParity leg 6: two summoned wind_charges at a floated
villager — one damage_event etype=48 cause=0 direct=charge1+1, hurt
sound + knockback motion, TWO explode packets (the graced second
burst carries no dmg), no entity_event, no ANGRY (ownerless), both
charges removed. Green on vanilla and mc-go;
TestWindChargeExplodeParity + TestWindChargeTriggerBlocksParity +
TestCreeperBoomParity re-run green.

## Slice — 2026-09-25s — ender_pearl throw + hit teleport

Vanilla (javap): EnderpearlItem.use throws like an egg (power 1.5,
entity.ender_pearl.throw NEUTRAL). ThrownEnderpearl.onHitEntity hurts
the victim 0.0 through DamageSources.thrown TWICE — the second call
is always graced, so one wire event — and onHit then runs 32
Level.addParticle calls that are an EMPTY NO-OP on the dedicated
server (zero particle packets on the wire), a 5% endermite roll
(monsters+difficulty gated), and — when the owner is a living
same-dimension player — ServerPlayer.teleport to the pearl's
pre-tick position (rotation+delta relative), hurtServer(
damageSources.enderPearl(), 5.0) — an entity-less source so cause 0
and direct 0 — and entity.player.teleport at the landing.

mc-go: ender_pearl joins the instant-throw items and
projectileTickTypes (it spawned but never ticked — invisible pearl),
gravity 0.03. enderPearlImpact teleports the owner (teleportWithin),
runs damageWith(5.0, death.attack.fall, ender_pearl) and plays the
teleport sound; the particle loop is correctly absent.
TestThrownHitParity leg 7 vs vanilla: thrown hit wires
cause=thrower/direct=pearl, one ender_pearl damage_event
(etype 8, cause 0, direct 0), one sync_pos east, teleport sound,
one ANGRY, no particles, pearl removed. Green on both targets;
assertHitWire/fireball direct asserts now accept whichever of the
paired projectiles landed (same launch order isn't guaranteed).

## Slice — 2026-09-25t — breeze_wind_charge entity-hit coverage +
explode decode fix

BreezeWindCharge inherits AbstractWindCharge.onHitEntity unchanged —
same hurt(1.0, wind_charge) + unconditional burst — only the explode
profile differs (radius 3.0 vs 1.2). mc-go already routes both types
through windChargeHit*/windBurst, so the slice is pure coverage:
TestThrownHitParity leg 8 pins two summoned breeze charges on a third
villager lane — one dmg etype 48 (cause 0, direct a charge), two
radius-300 explode packets, knockback motion, both removed. Green on
both targets.

Fixed a real decode bug in the shared drain's 0x24 explode case: the
centre is three plain doubles (not an LpVec3) and the optional
knockback vec3 must be skipped when hasKB — the old read left garbage
in radius/sound on every breeze packet. Also made the paired-summon
direct-id asserts order-agnostic (either of two same-tick spawns can
land the hurt).
### 2026-09-25u — wither_skull entity-hit + unconditional explode + wither tick
WitherSkull.onHitEntity: living-owner → hurt 8.0 wither_skull(+kill→heal
owner 5.0); OWNERLESS → hurt 5.0 through the entity-less magic type
(cause/direct 0). Landed hit → MobEffectInstance(WITHER, NORMAL 10s /
HARD 40s, amp 1) gated only on hurt true + Living. onHit explodes
unconditionally (power 1.0, MOB) at the skull's position then discards.
mobEffectTickStep now runs WitherMobEffect.applyEffectTick on BOTH arms:
hurt 1.0 via entity-less wither() every 40>>amplifier live ticks.
Blackbox leg 9 pins: ownerless magic dmg cause/direct 0/0, graced
second skull no event, 2 explode packets radius 1.0, ≥1 wither-type
tick event, both skulls removed.
### 2026-09-25v — fireball entity-hit + ExplosionPower + setFire blast arm
LargeFireball.onHitEntity hurts 6.0 through DamageSources.fireball —
which picks UNATTRIBUTED_FIREBALL when the shot has no owner (cause AND
direct both wire the ball's id +1 on the damage_event). onHit explodes
unconditionally: explode(this, pos, explosionPower, setFire=
MOB_GRIEFING, ExplosionInteraction.MOB). Entity.ExplosionPower reads the
summon's ExplosionPower NBT (default 1). explodePowerFire adds the
setFire arm — vanilla's finalizeExplosion rolls 1-in-3 to light base
fire in every carved air-over-solid cell.
Leg 10 pins: ownerless unattributed_fireball dmg cause==direct==ball,
graced second ball no event, 2 radius-1.0 explode packets, knockback,
both removed. Spawn geometry note: a 1-block fireball at -59.7 clips
the ground as a block hit and explodes without ever reaching the
villager — the leg aims -59.3. Green on both targets.
### 2026-09-25w — dragon_fireball cloud impact + indirect_magic dose wire
DragonFireball.onHitEntity is empty on vanilla — no direct hurt. onHit
skips when the struck entity is the owner, else scans LivingEntity in
the (4,2,4)-inflated ball box and spawns AreaEffectCloud at the nearest
inside distanceToSqr<16: radius 3.0 shrinking (7-3)/600 per tick, wait
10, duration 600, potionDurationScale 0.25, addEffect(INSTANT_DAMAGE,1)
payload — plus level_event 2006 and discard.
Two latent cloud bugs fixed to make it work: mobEffectTickStep's
world-entity arm was expiring the cloud's Effects payload one tick
after spawn (AEC isn't a LivingEntity — its effects are the dose
template, never ticked or expired — the arm now skips clouds), and the
dose wired "magic" 0/0 while vanilla hurts through
damageSources.indirectMagic(cloud, owner) — type 23, direct = cloud
eid+1, cause = owner. mobGiveEffect/mobInstantEffect/
playerGiveEffect/applyInstantEffect now take an optional damage-source
override; cloudTickStep passes indirect_magic{direct:ep.ID,
cause:ep.CloudOwnerID} on both arms. CloudOwnerID is the creeper for
explodeCreeper clouds and the ball's shooter for dragon balls.
Leg 11 pins: 2 balls, 2 clouds, 2 level 2006, every dmg on the
villager type 23 / cause 0 / direct = a cloud, both balls removed.
Green on both targets.
### 2026-09-25x — shulker_bullet impact + wander
ShulkerBullet.onHitEntity hurts 4.0 through
damageSources.mobProjectile(bullet, livingOwner) — ownerless /summon
wires cause 0, direct = bullet eid+1 — then LEVITATION 200t when the
hurt lands. onHitBlock = base block crumbs + EXPLOSION x2 +
SHULKER_BULLET_HIT sound; onHit destroys via discard + gameEvent.
Entity hit scan is mc-go's usual swept segment (mob + session arms).
No-target tick: applyGravity (getDefaultGravity 0.04, NOT zero like
the other hurting projectiles) then selectNextMoveDirection wander —
flightSteps counts down (10 + nextInt(5)*10 per roll), re-picks a
Direction excluding the current axis, latches targetDelta =
step*0.15, lerps delta toward it 20%/tick. Blocked-neighbor filter
skipped (unpinnable on flat ground). checkDespawn discards on
peaceful — test world is normal so unpinned.
Homing (finalTarget NBT) unimplemented — vanilla reads a UUID
EntityReference a /summon can't express; bullets summoned targetless
only wander.
Leg 12 pins: single bullet, Motion-driven in through the box face —
vanilla's swept hitscan resolves the face crossing while a bullet
spawned already inside the box emits a degenerate ray and falls to a
block hit instead (measured: spawn-inside died to EXPLOSION+
remove with zero dmg). Asserts type-30 mob_projectile dmg cause 0 /
direct bullet+1, villager levitation motion, bullet removed.
Also fixed the drain's level_particles decode: 26.2 writes two flag
bytes before pos — now captures particle id + pos (that's how the
block-hit EXPLOSION at the ground was identified).
Green on both targets (vanilla leg-1 egg flake excluded).
### 2026-09-25y — periodic applyEffectTick hooks (poison/regen/saturation)
MobEffectInstance.tick calls shouldApplyEffectTickThisTick on every
live tick; the session arm previously only ran wither, the entity arm
likewise. Both arms now iterate every live finite effect and dispatch
through applyEffectTick{Session,Entity}: wither 40>>amp (1.0 type-49
wither), poison 25>>amp (1.0 type-27 magic, skipped at health<=1 —
can't kill), regeneration 50>>amp (heal 1.0 below max), hunger
0.005*(amp+1) exhaustion every tick players-only (survival+adventure
gate), saturation FoodData.eat(amplifier+1, 1.0) every tick —
food += amp+1, saturation += (amp+1)*2 clamped to food.
Java note: vanilla HungerMobEffect.applyEffectTick has no gamemode
check in the bytecode; the drain gate lives in the food tick, which
mc-go mirrors — exhaustion simply doesn't accumulate outside
survival/adventure here (invisible either way).
Boundary convention resolved: vanilla fires on PRE-decrement
remaining, so a 5s regen (100%50==0) fires at remaining 100 AND 50 —
measured: 2 heals on both targets. mc-go commands run off-tick, so the
old rem%ivl rule was a coin flip (fires at dur only when the give beat
that tick's step). Fixed: mobEffectInst.born records the give tick —
the step never observes an instance in its birth tick — and the fire
check reads rem+1 (the duration vanilla sees at that observation), so
fire positions/count are deterministic: floor(dur/ivl) fires always.
Leg D pins it: regen 5s on a Health:10f villager ends at health 1200 —
2 heals on both targets.
TestEffectTickParity pins: poison 4s → exactly 3 type-27 dmg events,
regen 4s on a Health:10f villager → health meta >= 1100, wither 3s →
exactly 1 type-49 dmg event with cause/direct 0/0. Green on both
targets (summons stay in the player's chunk — a diagonal spawn at
~5.4 blocks didn't reach tracking on vanilla).
### 2026-09-25z — xp orb merge bucket, followNearbyPlayer, count pickup
ExperienceOrb.scanForMerges runs when tickCount%20==1, absorbing every
orb inside bbox.inflate(0.5) — but only the merge bucket: canMerge
requires (other.id - self.id) % 40 == 0 AND equal values (the earlier
"absorb all neighbours" port was wrong — javap shows the two gates).
merge() adds the neighbour's count, takes min age, discards it; the
victim's last lifecycle event is a remove. mc-go's stale items
snapshot also ticked just-removed orbs (victim absorbed the absorber)
— both the mid-pass tick and the canMerge victim now check
GetPtr presence, mirroring isRemoved().
followNearbyPlayer every tick: latch keeps while the player is
non-spectator and within 8 (dist²<=64), else rescans nearest
non-spectator alive player in 8; pull is normalize(dir to eye-center
y+0.81) * (1-d/8)^2 * 0.1 onto deltaMovement.
playerTouch: takeXpDelay!=0 skips, else takeXpDelay=2, grants
getValue() xp once per take, count--, discards at zero — a merged
count-2 orb pays out twice.
Summon gaps closed along the way: /summon rejected non-living types
(mc-go's max_health gate) — marker and experience_orb now spawn as
plain Entities, {Value:N} wires OrbValue, orb meta encodes index-8
value, and marker broadcast is suppressed entirely (vanilla sends no
spawn/meta for it — observed on wire).
TestXpOrbMergeParity pins both legs on both targets: orb + 39 markers
+ orb at the same spot (ids 40 apart → mergeable) + a distant orb →
the live-orb set ends at 2 (merge victim's remove, distant stays);
then a survival player at the spot → 3 set_experience updates (count2
→ two takes) and 0 live orbs. The drain now decodes 0x67
set_experience.
### 2026-09-25za — mending: repairPlayerItems on orb pickup
ExperienceOrb.playerTouch routes the orb's value through
repairPlayerItems before giveExperiencePoints: pick a random
!isEmpty && isDamaged equipment item carrying REPAIR_WITH_XP
(mending) — the slot set is both hands plus the four armor slots —
repair min(value*durabilityPerXp, damage) (mending's component
repairs 2 durability per xp point), then recurse with
value - repaired*value/modified until the remainder is 0 or the
candidates run out; only the leftover becomes experience.
mc-go mirrors it: candidates scan the held hotbar slot, offhand,
and armor 5-8 for damaged stacks with mending; the repaired slot
rides container_set_slot like vanilla's inventory broadcast.
TestMendingOrbParity pins both legs on both targets: a
chestplate[damage=100, mending] in mainhand drinks a Value:5 orb
completely — 0 set_experience — and a Value:60 orb over-repairs
the remaining 90 damage leaving exactly 15 xp granted.
Debug note for future probes: `enchantments={mending:1}` parses
on both servers; `enchantments={minecraft:mending:1}` fails
identically (unquoted namespaced keys are not SNBT) — the suite's
enchantments syntax is the unprefixed bare name.
### 2026-09-25zb — beehive release: harvest + break
BeehiveBlock.useItemOn / dispenser use + BeehiveBlockEntity
releaseAllOccupants landed:
- honey_level>=5 gates shears and glass_bottle interact paths
  (honeycomb x3 + beehive_shear + wear 1; bottle shrink + fill +
  swap/add), then campfire-within-5-below (isSmokeyPos) decides
  reset-only vs EMERGENCY release of every occupant.
- Dispenser shears/bottle on a nest → BEE_RELEASED release (no
  campfire check).
- Breaking a nest without silk touch → EMERGENCY release of the
  stored occupant count (mc-go tracks count only).
- releaseOccupant gates: BEES_STAY_IN_HIVE env attribute
  (night/rain/thunder) and a blocked front cell both hold the
  release unless EMERGENCY; spawn pos+0.5+(0.55+w/2)*step,
  feet at pos.y+0.5-bbHeight/2, beehive_exit sound per bee.
  Measured bee hitbox for the offset: 0.55w x 0.5h.
TestBeeReleaseParity pins five legs on both targets: shears
interact (EMERGENCY, snap 64.325/-59.75/60.5 + shear sound +
3 honeycomb + honey_level 0), lit campfire harvest keeps both
bees inside, dispenser shears, dispenser bottle, and a
survival dig on a front-blocked nest releasing at the nest
centre.
Debug notes for future probes: after `tp`, the next packet
drain MUST run before use_item_on — vanilla drops block
interactions until accept_teleportation (serverbound 0x00)
replies to the sync-pos (0x48) teleportId. `say` broadcasts
ride clientbound 0x21 (disguised/player chat), NOT 0x79 —
marker probes must scan 0x21 or raw payload. Occupant NBT
needs a large min_ticks_in_hive or vanilla's hive tick
auto-releases occupants before the harvest lands.
### 2026-09-25zc — dispenser $7/$8 equip fallback + canEquipWithDispenser
DispenseItemBehavior$7 (wither_skeleton_skull) / $8 (carved_pumpkin)
decoded and wired: on an empty mouth the head block places, the
golem/wither pattern is attempted, and on failure the block reverts
and the behavior falls back to EquipmentDispenseItemBehavior's
shared dispenseEquipment — equipping the first LivingEntity whose
box overlaps the unit cell at the mouth. OptionalDispenseItemBehavior
semantics preserved: success -> event 1000, failure -> event 1001
and the item is KEPT (no throw), which is why a blocked pumpkin
stays in its slot while a failed helmet throws via
DefaultDispenseItemBehavior.
canEquipWithDispenser (LivingEntity.final) decoded gate-by-gate:
isAlive && !isSpectator && equippable!=null && dispensable &&
canUseSlot(slot) && canBeEquippedBy(typeHolder) &&
getItemBySlot(slot).isEmpty() && canDispenserEquipIntoSlot(slot).
The tail pair is AND'd — an occupied slot refuses everything.
Mob.canDispenserEquipIntoSlot = canPickUpLoot() (the CanPickUpLoot
NBT flag, default false — summoned mobs without it refuse equip);
LivingEntity.canDispenserEquipIntoSlot = unconditional true, so
armor stands and players always pass. ArmorStand.canUseSlot =
slot!=BODY && slot!=SADDLE && !isDisabled — mc-go's
armorStandFilterID+DisabledSlots mirror isDisabled. Equippable
allowedEntities is empty for both humanoidArmor and
equippableUnswappable (mob heads), so canBeEquippedBy passes every
registered type. mc-go's dispenseEquip rewritten to mirror the
predicate; wither_skeleton_skull added to the mob-heads helmet
equip table.
DispenseItemBehavior$8 golem-spawn legs unchanged: iron golem on a
T of iron blocks, snow golem on two snow blocks, wither on soul
sand T (heads left for the skull case only).
TestDispenseBehaviorParity gained a no-pickup pig leg: pig without
CanPickUpLoot stays bare and the iron_helmet is thrown (event
path), while pickup pigs keep wearing pumpkin/skull.
Debug notes for future probes: summoned entities need a tick or
two of world time before getEntitiesOfClass sees them — immediate
power after summon can outrun section registration (a z=140
fixture row intermittently read as entity-blind on vanilla;
re-verified clean at z=60). A hovering survival-mode probe client
within pickup reach vacuums thrown dispenser items — check
`items entity @s container.*` before concluding a throw vanished.
### 2026-09-25zd — bee anger: releasee sweep + nearby sweep + sting chain

BeehiveBlock anger model decoded from 26.2 bytecode. Two layers:

1. `BeehiveBlockEntity.emptyAllLivingFromHive` per-releasee sweep —
   for each spawned bee with `player.position().distanceToSqr(bee)
   <= 16.0`: `isSedated()` (CampfireBlock.isSmokeyPos on the nest
   pos) → `setStayOutOfHiveCountdown(400)`, else `setTarget(player)`.
   CampfireBlock.isSmokeyPos scans 1..4 blocks below for a lit
   campfire; a campfire under a solid floor still counts (collision
   shape intersects the smoke post).
2. `BeehiveBlock.angerNearbyBees` — a second, separate pass on the
   whole neighbourhood: `getEntitiesOfClass(Bee, box pos±(8,6,8))`
   → if empty return; `getEntitiesOfClass(Player, same box)` → if
   empty return; each `getTarget()==null` bee → `setTarget(random
   in-box player)`.

Call-site differences measured: `playerDestroy` (break) calls
angerNearbyBees UNCONDITIONALLY — a campfire does NOT protect a
breaker. `useItemOn` harvest gates it on `!isSmokeyPos` — a smokey
nest only resets honey_level (bees stay inside). `onExplosionHit`
also calls angerNearbyBees. mc-go mirrors all three paths; a
dispenser trigger passes nil sess → the per-releasee sweep
early-returns.

Bee combat chain (Bee.doHurtTarget, measured): sting damageSource +
attack_damage 2.0 → LivingEntity target gets `stingerCount+1` and
POISON secs*20 ticks (NORMAL 10s, HARD 18s, else 0) → `setHasStung`
→ `stopBeingAngry` → entity.bee.sting sound. Post-sting death
(customServerAiStep): `timeSinceSting++`, every 5th tick a
`nextInt(clamp(1200-t,1,1200))==0` roll hurts the bee its own
health (generic source) — a ~60s bleed-out. Bee NBT keys now
written: HasNectar/HasStung/TicksSincePollination/
CannotEnterHiveTicks. mobNeutral += bee; mobAlwaysFlies added so
bees chase in 3D (flying_speed .6, XZ path would strand them);
bee melee chase moves along the 3D vector toward reach.

Gotcha worth remembering: `s.entities.All()` returns *snapshots* —
field writes on the loop variable silently drop. Mutations go
through `s.entities.GetPtr(id)` (piglinAlert already did this; the
first angerNearbyBees write lost AngryAt for exactly this reason —
388 ticks of a "targetless" angry bee).

TestBeeAngerParity (differential, both targets): leg 1 shears
harvest → chase + sting + poison + HasStung; leg 2 player >4
blocks from released bees → bees out, no anger target, no sting;
leg 3 campfire break → bees out AND sting lands anyway (campfire
is useItemOn-gated only, not break-gated). Vanilla passes all
three; mc-go matches. bee_nest_destroyed advancement trigger kept
firing before the release bookkeeping.

### 2026-09-25ze — bee-nest removal matrix: occupants exit only via explicit calls

Full removal-path audit of BeehiveBlockEntity vs the naive "teardown
releases" model — bytecode-verified, then probed on vanilla:

- `BeehiveBlockEntity` does NOT override `preRemoveSideEffects` —
  the `emptyAllLivingFromHive(null, EMERGENCY)` in its dump is
  `setChanged()` gated on `isFireNearby()` (fire evacuation, still
  unimplemented). Occupants are NOT a generic block-entity-teardown
  side effect at all.
- Survival break (`playerDestroy`): the only block-removal path
  that calls emptyAllLivingFromHive(player, EMERGENCY) — release +
  per-releasee sweep + unconditional `angerNearbyBees`. mc-go:
  `beeNestDestroyed` deferred after the air write.
- Explosion carve (`onExplosionHit` → super loot +
  angerNearbyBees): occupants VANISH with the block entity — no
  release anywhere on the path — but the 8x6x8 sweep still runs.
  ServerExplosion order: hurtEntities BEFORE interactWithBlocks —
  entities near the center get flung out of the box before the
  sweep samples it (this is why a "free bee" probe has to stand
  beyond blast reach d>8 while staying inside the box x∈[55,71]).
- `/setblock`/`/fill` (flag 2|256), `/clone` (flags 816 ⊃ 256),
  `setblock ... destroy`, creative dig (`destroyBlock(pos,false)`,
  drops off → `playerDestroy` skipped): all vanish occupants
  silently — LevelChunk.setBlockState gates preRemoveSideEffects on
  !(flags&256) and no path calls emptyAllLivingFromHive anyway.
  mc-go: `syncBlockEntities` plain `delete(d.bees,pos)` — no
  release plumbing at all.
- Silk-touch break still carries occupants into the dropped item
  (copy_components NBT copy — already-deferred loot gap); the
  release is correctly skipped on that path.

TestBeeAngerParity extended to 7 legs, all differential vs vanilla
26.2: leg 4 explosion (free bees beyond blast reach get angered by
the box sweep and sting); leg 5 setblock overwrite = miss; leg 6
creative dig = miss; leg 7 setblock-destroy = miss. Vanilla passes
every leg; mc-go matches. TestBeeReleaseParity still green both
targets.

### 2026-09-25zf — beehive fire evacuation (setChanged + isFireNearby)

The last occupant-exit path: `BeehiveBlockEntity.setChanged` runs
`isFireNearby()` on every mark-dirty — any `FireBlock` in the
nest's 3x3x3 → `emptyAllLivingFromHive(null, EMERGENCY)`: the
occupants release but nobody angers (the per-releasee sweep and
angerNearbyBees both live on the block's callers, not the BE).
`instanceof FireBlock` matches plain `fire` only — `SoulFireBlock`
is a `BaseFireBlock`, so soul fire does NOT evacuate.

mc-go: `syncBlockEntities` handles both directions — a nest write
while fire sits in pos±1 evacuates (the mark-dirty side), and a
`fire` write scans pos±1 for nests and evacuates each (the
neighbour-write side). Vanilla probed: `setblock fire` adjacent to
a stocked nest releases all occupants, no HasStung.
TestBeeAngerParity leg 8 pins it differentially.

### 2026-09-25zg — leash model: lead, leash_knot, tickLeash, Entity.interact chain

The full leash surface, bytecode-measured on the 26.2 jar and pinned
differentially on both targets (TestLeashParity, 7 legs):

- `Entity.interact` chain order (all five branches verified at
  bytecode level and mapped onto the consumed-style handlers):
  (1) sneak + leashable + !baby → transfer the player's whole 32-cube
      flock onto the clicked mob;
  (2) shears → `shearOffAllLeashConnections` → own leash + every
      leashable leashed TO this mob → `dropLeash()` each (lead item
      drops) + SHEARS_SNIP at blockPos + wear 1;
  (3) equipment shears (BODY slot, !sneak) — was already implemented
      but sat in the wrong phase; vanilla runs it inside
      `Entity.interact`, BEFORE the wool shear which lives in the
      item's `interactLivingEntity` phase after `mobInteract`. Split
      `shearAnimal` (equipment only) from `shearMobItemPhase` (runs
      last in the chain);
  (4) holder == player → `hasInfiniteMaterials ? removeLeash :
      dropLeash` + LEAD_UNTIED;
  (5) lead in hand + !(holder instanceof Player) + canHaveALeash
      AttachedTo (other, leashable, dist<=12) → `setLeashedTo` +
      LEAD_TIED + `stack.shrink(1)` unconditional.
- `ClientboundSetEntityLinkPacket` wire shape: two raw int32s
  (sourceId, destId=-1 for unlink), NOT varints, id 0x64.
- `Leashable.canBeLeashed` = !Enemy; leashableInArea = 32-cube on
  the holder side. `tickLeash`: holder gone + ENTITY_DROPS → dropLeash
  else removeLeash; dist>12 → LEAD_BREAK at holder + dropLeash (lead
  item at the mob); dist>6 → elastic pull (approximated as a capped
  step min(0.9, (dist-6)*0.4) — vanilla's spring stiffness/damping
  constants not replicated; deferred).
- `LeadItem.useOn` on a `fences`-tag block → `bindPlayerMobs`: the
  player's leashees in the 32-cube re-attach to a knot at the exact
  fence pos (`leash_knot`, type 76); zero transfers on a fresh knot
  → discard + PASS. `knotInteract`: player's leashees → knot, else
  !sneak → knot's leashees → player; a knot with zero leashees
  discards; `survives()` needs the fence still standing.
- `leash` NBT key: knot holder → `leash:[I;x,y,z]`; entity holder →
  `leash:[I;a,b,c,d]` UUID int-array — saveMap/nbtMatches now thread
  the entity Manager through so the holder can be resolved.
- Sneak-click is `isSecondaryUseActive`-gated (branch 1); baby mobs
  refuse the transfer-target role.

Also fixed two real bugs found by the run:

- `nbtPartialEqual` compared `tv == pv` — any `nbt={...:[I;...]}`
  selector panicked the whole server (uncomparable []int32). Typed
  arrays now compare element-wise ([B;/[I;/[L;) requiring the same
  tag type, matching vanilla.
- Blackbox harness port collision: two consecutive `freePort` calls
  can return the same port; mc-go then answers the vanilla readiness
  probe and the whole "vanilla" leg silently runs against mc-go.
  Ports are re-rolled until distinct.

TestLeashParity legs: (1) attach (lead shrinks + leash key), (2)
unleash (lead drops; player moved out of pickup range first — a
survival player vacuums the drop before the probe), (3) fence bind
(knot spawns + `leash:[I;12,-60,12]`), (4) knot click returns
leashees + knot discards, (5) >12 snap drops the lead at the mob,
(6) shears drop own leash + item, (7) sneak-transfer to a cow then
player walks past snap distance — leash survives (holder = cow).
All green on vanilla 26.2 and mc-go.

Deferred: dying-mob lead drop (no dropLeash on die/onRemoval in the
bytecode — the lead appears lost), elastic-spring pull fidelity,
lead-drop item velocity (spawnAtLocation defaults used).

### 2026-09-25zh — chested mounts: equipChest, llama Strength columns, Items NBT, death drops

`AbstractChestedHorse.mobInteract` (javap-measured, 26.2): the
sneak-menu path short-circuits first (`flag3 = !baby && isTamed &&
sneak → super.mobInteract`), then `isVehicle() || flag3 ||
(baby && holding GOLDEN_DANDELION) → super`, then food, then
`!isTamed → makeMad()` (SUCCESS — the wild animal never reaches the
chest branch), then `!hasChest && stack.is(CHEST) → equipChest`
(SUCCESS). `equipChest` = setChest(true) + playChestEquipsSound
(DONKEY_CHEST; Llama overrides to LLAMA_CHEST) +
`stack.consume(1, player)` + `createInventory()` which rebuilds the
inventory at the new size and copies `min(old, new)` cells.
`ItemStack.consume` shrinks only when `!hasInfiniteMaterials` —
creative keeps the chest.

- `getInventoryColumns`: `AbstractChestedHorse` = `hasChest ? 5 : 0`;
  `Llama` = `hasChest ? strength : 0` (strength ∈ [1,5],
  `setStrength` clamps). `setRandomStrength` (finalizeSpawn, so
  /summon too): `rand < 0.04 ? 1+nextInt(5) : 1+nextInt(3)`.
- NBT: `ChestedHorse` bool always on the chested family; `Tame` on
  mount-screen animals; `Items` (ItemStackWithSlot codec — flat
  `{Slot:Nb,id:"...",count:N}`) only when chested, every non-empty
  cell — saddle slot 0, body slot 1, chest cells 2+. Llamas add
  `Strength` int and `Variant` int (LEGACY_CODEC id 0-3).
- `Items` on /summon reads the same slotted form (only applied when
  ChestedHorse): slot 0 → Saddle flag, slot 1 → BodyItem, slot≥2 →
  MountInv[slot-2].
- Death: `AbstractHorse.dropEquipment` drops EVERY non-empty
  inventory cell (saddle, body, chest cells; skips
  PREVENT_EQUIPMENT_DROP stacks), then `AbstractChestedHorse`
  additionally drops the chest block itself and clears the flag.
- mc-go: `equipMountChest` in the interact chain after tameAnimal /
  before openMountInventory; `mountInventoryColumns` now per-type
  (llama → Strength, donkey/mule → 5); saveMap emits Tame,
  ChestedHorse, Items (as `nbt.List` — a plain `[]any` never matches
  the selector's `nbt.List` in nbtPartialEqual), Strength, Variant;
  summon-NBT parses Strength/Variant/Items with the finalizeSpawn
  randomizers for absent llama keys; spawnMobLoot drops the full
  mount inventory + chest block.

TestMountChestParity legs (all green on vanilla 26.2 + mc-go):
(1) survival chest interact → ChestedHorse:1b + chest count 2→1;
(2) llama Strength:4 + Variant:2 summon NBT + sneak-interact →
mount_screen_open cols=4; (3) mule Items slotted NBT round-trip;
(4) kill chested mule → chest + cobblestone items drop (player moved
out of pickup range first); (5) untamed donkey + chest → makeMad,
no equip.

Harness note for future tests: `say` output arrives as
disguised_chat (0x21), not system_chat — probes must read
eggDrainFor chat events, not raw SystemChatCB packets.

### 2026-09-25zi — silk-break copy_components/copy_state; bee nest keeps occupants + honey_level through the item

Datagen + javap-measured (26.2): `blocks/bee_nest` and `blocks/beehive`
each declare ONE pool gated on `match_tool{predicates:{enchantments:
[{enchantments:silk_touch, levels:{min:1}}]}}` whose item entry carries
`copy_components{include:[bees], source:block_entity}` +
`copy_state{block, properties:[honey_level]}`. A non-silk break
therefore drops NOTHING from the table — the occupants just
EMERGENCY-release (the block-entity bee path already shipped).

Wire form (javap `DataComponents` clinit order − 1): `block_state` =
component id 76 = `VarInt n` + n×(String key, String value) — the flat
property map `{honey_level:"4"}`; `bees` = id 77 = `VarInt count` +
per occupant a TypedEntityData (holder = raw registry id+1, then the
named NBT compound of entity_data with `id` folded out) +
`VarInt ticks_in_hive` + `VarInt min_ticks_in_hive`. NBT save form is
`bees:[{entity_data:{id:"minecraft:bee"},ticks_in_hive,min_ticks_in_hive}]`.
mc-go models bees as a count (the occupant entity-data round-trip is
unmodelled beyond `id:"minecraft:bee"`); block_state is the canonical
sorted `k=v` string like Ench.

Implementation: `ItemStack.Bees` already existed for placement
restore; added `ItemStack.BlockState`, wire + NBT + `/give` parse
(`block_state={honey_level:"4"}` — string values only, matching
BlockItemStateProperties's STRING codec), `!block_state` removal,
`lootApplyFuncs` cases for both functions, and a `lootCtx.bees` field
fed from `d.bees[pos]` captured BEFORE the break's world write deletes
the occupant record (the silk gate no longer excludes the capture).
Placement folds `block_state` back onto the placed state via
`blockStateComponentApply` (BlockItem.updateBlockStateFromTag — only
properties the block declares).

Two real parity bugs the differential run exposed:
- `handleChat` truncated EVERY chat-command line at 256 chars — the
  Chat Command packet's command field is a longer string (vanilla
  accepted a 277-char setblock; mc-go cut mid-NBT and replied
  "Invalid block data"). Chat text keeps the 256 cap; commands don't.
- `blockHardnessTable` lacked bee_nest/beehive, so they dug at the
  default hardness 1.0 (150 ticks offground) instead of vanilla's
  0.3/0.6 (45/90 ticks). Added measured entries.

`TestNestSilkCopyParity` (nestsilk_test.go): setblock a 3-occupant
honey_level:4 nest → silk dig → the drop carries `bees`+`block_state`
in Item.components and releases no bees; non-silk dig drops NOTHING
and EMERGENCY-releases 3 bees; `item replace` a component-carrying
nest, place → the block lands as honey_level:4 and re-breaks with
its occupants. Green on both legs (vanilla 25.6s, mc-go 23.4s).

Harness note: `execute if` probes must be polled to a deadline —
`unless`-for-fast-miss fires immediately, which races delayed-destroy
drops. And the delayed destroy armed by an early dig-finish still
commits the break ~45 ticks later; probes must outlive it.

### 2026-09-25v — copy_components: custom_name/container/container_loot + armed-box unpack

Extended the copy_components coverage to `custom_name` (the 68
chest/trapped_chest/barrel/furnace/head/banner/copper-family tables)
and `container`/`container_loot`/`lock`/`custom_name` (the 16 shulker
tables), plus the block-entity `Items`/`CustomName`/`LootTable`/
`LootTableSeed` NBT surfaces on place and break.

Measured on the jar: `container` IS network-synchronized (id 75,
`ItemContainerContents.STREAM_CODEC` = VarInt slot count + per-slot
optional {id,count,patch} — the inner patch recurses the same shape as
the slot patch). `container_loot` (id 80) and `lock` (id 79) are
persistent-only — never wire-emitted. `SeededContainerLoot` CODEC =
`{loot_table: ResourceKey-string, seed: long default 0}`. Vanilla NBT
keys are `LootTable`/`LootTableSeed` — mc-go's container store used
`LootSeed` on the wire tag; fixed.

Vanilla drop semantics measured (ShulkerBoxBlock.playerWillDestroy):
`be.isEmpty()` runs `unpackLootTable` BEFORE `collectComponents` — an
armed box's `LootTable` rolls into the slots (wiping any pre-existing
Items), so the drop carries `container` + `custom_name` and
`container_loot` is consumed, never on the drop. Saved-NBT symmetric:
`trySaveLootTable` writes `LootTable`/`LootTableSeed` INSTEAD of
`Items` when armed — `data get block` on an armed box shows no Items.
mc-go now mirrors both: `dropShulkerBox` rolls the armed table into
Contents (seed-scattered slots) and all three BE save emitters skip
Items when a LootTable is armed.

Item side: `ItemStack.LootTable/LootSeed/Contents` + `container`
component wire/read parse (`container=[{slot,item}]` on `/give`,
`itemComponentsNBT` emits `container_loot:{loot_table[,seed]}`), and
`applyImplicitComponents` order on place — container_loot re-arms
`c.lootTable/lootSeed`, `container` restores slots, `custom_name`
names the BE (`d.beNames` map, cleared on dig).

`TestBECopyParity` (be_copy_test.go): 4 legs — named chest dig →
named drop + contents spill; armed shulker dig → container+name on
drop, container_loot absent; armed+named+filled shulker placed →
LootTable/LootTableSeed/CustomName on the BE, Items hidden; named
chest placed → CustomName on the BE. Green on both legs (~58s total).

Harness note: a single START→STOP dig pair is flaky on vanilla —
retry pairs until `unless block` says gone. And probe markers must
never appear in a command line twice: an `execute if block
...{NBT}` parse failure echoes the whole command (including the say
marker) back as the error — probeAbsent saw its own marker as a hit.
