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
- [x] Encryption / online mode
- [x] Cookie / transfer / dialog / code-of-conduct packets.
      /transfer writes play transfer (host + port); /dialog show/clear
      write show_dialog (Holder VarInt = registry index + 1) and
      clear_dialog ahead of their echo - `TestDialogPacketsParity`.
      Vanilla emits no cookie_request/store_cookie and no
      code_of_conduct packets under the default datapack, so there is
      nothing else on the wire to pin.

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
- [~] Lighting engine: block and sky light both propagate per-cell on
      vanilla's sectioned layer-storage model — eligibility materialization,
      the 27-box init-mark fan-out that crosses two chunks, the
      written-vs-virgin emit rule (never-written layers ride the empty
      mask; written-then-emptied ship zeroed arrays), per-tick mark
      batching emitted before block updates, border-recipient gating,
      and the full measured emission table — all pinned byte-for-byte
      on the wire (`TestTorchLightParity`, `TestNetherTorchLightParity`,
      `TestLightEmissionParity`, `TestSkyPropParity`); nether ambient
      light (lava-sea seeding) remains unmodeled so arrays only match
      inside sealed regions
- [~] Fluids: water and lava flow with vanilla's levels, drop-off,
      falling columns, equal-supply ring decay, min-slope-distance
      spread gating, source-formation gamerules and wash-away loot.
      canPassThroughWall collision shapes and waterloggable containers
      remain approximated. Redstone and scheduled block ticks
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
- [x] Full entity type registry
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
  1568-entry give-all blob; smithing table menu, mapextending,
  bannerduplicate, bookcloning, shielddecoration, firework_rocket/
  firework_star/firework_star_fade all measured + differential-pinned;
  decorated_pot (pot_decorations assemble), crafting_dye (applyDyes
  channel mix) and crafting_imbue (potion ringed by material) all
  measured + pinned)
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
  matches exactly. Scoreboard objectives are parity-tested end-to-end:
  the full criteria registry (dummy/trigger/kill/death counts, the
  live health/xp/level/food/air/armor feed, teamkill/killedByTeam,
  and every stat-backed minecraft.<type>:<id> form) with vanilla's
  per-tick doTick poll, the add-delta stat feed (no backfill, sums
  onto manual sets), and resetStat's zero write. Non-recipe
  advancement triggers remain open; scoreboard teams are implemented
  and parity-tested)
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
  and the exotic distance buckets (vehicle ride stats, water arms,
  tick-physics jump/fall) are pinned by TestMoveStatParity)
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
- [x] World save format parity (region files)
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

### 2026-09-25zj — beacon effects: commit/validate/kick + payment-slot cap + menu-removed teardown + duplicate-login kick

`handleSetBeacon` is now vanilla's `handleSetBeaconPacket` end to end:
menu must be the beacon menu (silent return otherwise), the menu must
still be valid (`stillValid`: dist²≤64 from player FEET to the block —
measured; was previously ungated), `updateEffects` runs
payment→levels→`validateEffects` and any false kicks the client with
`multiplayer.disconnect.generic` (empty payment included — the kick is
not just for invalid pairs). On pass: `beaconData` props
[levels, primary+1, secondary+1] land async via the next broadcast tick
(menu props + slot removal alike — tests poll), paymentSlot.remove(1),
setChanged. `validateEffects` table pinned: secondary only at 4,
required-level ≤ levels, primary never needs 4, secondary in tiers
1-3 must equal primary, both-empty still consumes the payment.

`PaymentSlot.getMaxStackSize()=1` — the beacon payment takes ONE item
even from a stack: `containerSpec.slotMax` now feeds clickLeft's
place/merge, `placeCapped` and `quickMoveIntoContainer`. And
`BeaconMenu.removed` drops the payment stack as an item entity on ANY
close (never the inventory), plus drop-or-place for the cursor stack
(dropped on disconnect/kick, placed back on client close) — wired as
`menuRemoved(sess, w, dropped)` from both `handleCloseContainer` and
`untrack`, so menu teardown now runs on disconnect too (was skipped —
a kicked player's payment leaked into the shared container state).

Two adjacent measured fixes:
- Duplicate login: `handlePlay` disconnects the OLDER same-name
  session with `multiplayer.disconnect.duplicate_login`
  (PlayerList.placeNewPlayer). mc-go previously coexisted, which also
  poisoned `findSessionByName`: `@s`-style name lookups could resolve
  the dying session — `/tp @s` moved the kicked player while the
  issuer stayed at spawn and out-of-reach uses silently dropped.
  Sessions closing down are marked (`closing` flag in the kick paths
  + closeQueue) and `findSessionByName` skips them.
- `containerStillValid` measured: dist²≤64 from feet, i.e. an 8-block
  sphere around the block the menu belongs to; Set Beacon ignores
  commits from a stale menu rather than kicking.

`TestBeaconEffectsParity` (beacon_parity_test.go): 4 legs —
happy path lv4 pyramid → pay 1 → speed+speed commit (props 4,1,1,
UpdateMobEffect amp1 dur340, NBT primary/secondary both speed) →
re-pay → speed+regen commit (props 4,1,10, regen amp0 dur340);
invalid pair haste+strength → disconnect; empty payment →
disconnect; commit with no beacon menu → ignored. Green on both
servers (~52s).

## Slice 2026-09-25zk — /experience parity + independent xp model

javap-measured (26.2 ExperienceCommand/Player/ServerPlayer):

- Grammar: `experience add|set <targets> <amount> [points|levels]` plus
  `query <target> points|levels`; `/xp` is a redirect alias. Bare
  amount defaults to points, `add` takes any int (negatives remove),
  `set` is integer(0)+.
- The three fields are INDEPENDENT — xpProgress/xpLevel/xpTotal — total
  is bookkeeping only, never the level source. mc-go previously
  re-derived level+bar from total on every point grant, which pinned
  `/xp set 30 levels` → level snapped back at the next orb.
- Semantics: giveExperiencePoints = progress += i/needed, total clamps
  [0,max], negative/positive carry loops walk levels (ServerPlayer:
  n==0 early-return, no packet). giveExperienceLevels = saturated add,
  level<0 resets all three. setExperiencePoints refuses i>=needed
  (whole-command set.points.invalid only when EVERY target fails) and
  clamps the bar a point shy of full; setExperienceLevels touches only
  the level; an unchanged field sends no Set Experience. Query replies
  are source-local; add/set broadcast to ops with
  success.single(amount, displayName) / success.multiple(amount,
  collection.size()) — the count, not the successes.
- Error surfaces: bad int start → parsing.int.expected, digits+junk →
  command.expected.separator mid-token, overflow →
  parsing.int.invalid; missing trailing args → command.unknown.command
  at end-of-input; EntityArgument.player() multi-selector rejection →
  argument.player.toomany with cursor 0 (whole input underlined), while
  entity-notfound carries NO context line at all.

Orb pickup (addXP) now routes through giveExperiencePoints — same call
vanilla's ExperienceOrb.playerTouch makes.

`TestXpParity` (xp_parity_test.go): 37 legs — level/point adds, bare
unit, negative adds crossing and draining levels through the carry
loops, the total-reset leg, multi-target success.multiple, the partial
+ full set-fail, toomany/notfound/error cursors, plus every leg's last
Set Experience packet compared by bar-bits/level/total. Green on both
servers (~34s).

## Slice 2026-09-25zl — lectern books + use_item_on revert pair + placeItemBackInInventory

javap-measured (26.2 LecternBlock/LecternBlockEntity/LecternMenu/
Inventory/ServerGamePacketListenerImpl):

- The lectern slice is now complete end-to-end: place-book writes
  has_book/powered + book_put sound (id 194), the written/writable
  book lands in the block entity with resolveBook applied
  (resolved:1b, Filterable leaves always {raw:}), page nav
  prev/next/direct buttons move Page within [0,pageCount), take-book
  empties the slot and closes the menu via stillValid, and
  signalPageChange emits powered=true + a 2-tick unpower + levelEvent
  1043 — the redstone pulse a comparator reads. changePowered and the
  tick are UNGUARDED: the same-state setBlock still emits a block
  update because Level.sendBlockUpdated is unconditional.
- `handleUseItemOn`'s tail sends every dispatched use_item_on a
  client-prediction revert pair — BU(clicked) + BU(clicked.relative
  (face)) reading live state — before the sequence ack. This was the
  mystery extra BU pair on every interact (empty-hand, book-on-stone,
  successful placements). Implemented as a deferred sendRevert in
  handleUseItem so every early return — itemBlock fails, occupied
  targets, empty hands — still emits it; out-of-reach never dispatches.
- Inventory.add(i,stack) is silent, but
  placeItemBackInInventory(stack,true) emits one
  ClientboundSetPlayerInventoryPacket per call: slot =
  getSlotWithRemainingSpace else getFreeSlot (flat items scan 0-35,
  hotbar 0-8 then main 9-35, no offhand), remainder drops at the feet.
  The lectern stillValid/removed close path now runs it, which is why
  a picked-up book's cursor copy lands in the player inventory as one
  SPI packet. Menu clicks never accept placement on a lectern
  (mayPlace=false — books only enter through the block interact).
- writeSlotPatch counted book components wrong — payloads ran past
  the declared add count and read back component-less.

`TestLecternParity` (lectern_parity_test.go): 20 legs — place, open,
page nav, take, pick, park, close, redstone/property/sound/levelEvent
checks — all packet shapes compared per leg on both servers. Green
(~35s); place-denial unit tests updated to the measured revert wire.

## Slice 2026-09-25zm — crafter broadcast + craft dispatch

javap-measured (26.2 CrafterBlock/CrafterBlockEntity/CrafterMenu/
HopperBlockEntity/DefaultDispenseItemBehavior/DispenserBlock):

- `rsNeighborChanged` now has a crafter case — the TRIGGERED latch on
  rising edge (scheduleTick-style 4-tick arm + flag-2 TRIGGERED=true
  write + containerData prop 9 to viewers) and the falling-edge clear
  (TRIGGERED=false,CRAFTING=false one flag-2 write). The old
  `crafterNeighborCheck` 6-scan off `setBlockAndBroadcast` is gone —
  lever/button/wire/pending-drain fan-outs all reach it now through
  the normal neighbor-update path. A queued craft still fires when
  the power drops first (scheduleTick has no cancel).
- `getStateForPlacement` parity: crafters place front-facing the
  player (nearest look opposite), top per the
  down→horizontal-opposite / up→horizontal / else-UP rule, and
  TRIGGERED starts powered iff the cell already sees a signal — a
  powered placement arms the first craft 4 ticks out
  (`setPlacedBy` reads the placed state's TRIGGERED).
- `dispenseFrom`: recipe miss → levelEvent 1050 and early return.
  Hit → `craftingTicksRemaining=6` + flag-2 CRAFTING=true, then
  `dispenseItem`, then every non-empty slot shrinks 1 (disabled
  slots included — vanilla quirk). `serverTick` clears CRAFTING via
  flag-3 write when the 6-tick countdown hits 0.
- `dispenseItem`: the whole tail (item spawn + trigger + 1049 +
  2010) is gated on the insert leaving a REMAINDER — a fully-eaten
  result into a front chest/hopper/furnace/crafter is silent. The
  dst picks the one-at-a-time loop when the front container is
  itself a crafter or the stack exceeds its max size, else the
  whole-stack loop. Crafter destinations apply the real
  `canPlaceItem` even-fill rule (disabled slots refuse; a slot with
  a smaller stack behind it is skipped) via `crafterContainerInsert`
  — shared by hopper inserts too.
- spawnItem: the tossed item spawns 0.7 blocks out the front face,
  dips 0.125 on the Y axis (0.15625 otherwise), and takes
  dir*j + triangle(spread=0.0172275*6) per-axis motion (j =
  rand*0.1+0.2) — was a fixed 0.1*dir before.
- containerData wire: props 0-8 are per-slot enables and prop 9 is
  DATA_TRIGGERED — all 10 stream on menu open; SLOT_DISABLED=1 and
  SLOT_ENABLED=0 measured on the wire (the opposite of the first
  guess). The container-slot-state packet ({slotId, containerId,
  newState} — vanilla's exact field order) toggles and pushes the
  prop to every viewer of the open menu.
- NBT: the crafter's save keys are now vanilla's — `disabled_slots`
  as an IntArray of disabled indices, `triggered` int, and
  `crafting_ticks_remaining` int (was a bare uint16 short before).
- `broadcastContainerProp(c, prop, val)` is the new helper pushing a
  Container Property to every session viewing a container — used by
  the trigger latch and the slot toggle.
- Deferred: recipe `getRemainingItems` (bucket→empty bucket style
  remainders) — `data.Recipes`/`craftResult` carry no remainder
  table yet.

New: `TestCrafterWireParity` — opens the menu (10 props), toggles
slot 2 off/on (prop trace), arms on a redstone_block write
(BU sequence TRIGGERED→CRAFTING→clear + 1049/2010(front) + item
spawn), releases on power-off (TRIGGERED=false BU), inserts the full
result into a front chest (silent — no spawn/1049/2010), and clicks
1050 on an empty grid. `TestCrafterRecipeCraftedParity`,
`TestDispenserDropperParity`, `TestHopperTransferParity` and the unit
suite stay green.

## Slice 2026-09-25zn — recipe getRemainingItems (bucket/bottle remainders)

Measured (javap): `ResultSlot.onTake` shrinks each input 1, then runs
the remainder tail per grid slot — remainder lands in the slot when it
drained, `grow` when the same item is still there, else `Inventory.add`
with `player.drop` on overflow. `Item.getCraftingRemainder()` is final
and returns the `craftRemainder` property — only 5 items set it in the
26.2 Items static init: water/lava/milk buckets → bucket, dragon_breath
and honey_bottle → glass_bottle (stews' `usingConvertsTo` is eat-only).
`CrafterBlock.dispenseFrom` calls `getRemainingItems(input)` after the
result dispense and runs `dispenseItem` per non-empty remainder —
every remainder gets the full spawn + trigger + 1049/2010 tail.

- `craftRemainders` table + `craftRemainderOf` in recipes.go — the
  measured 5 entries.
- `takeCraftResult` (2x2) and `takeTableResult` (3x3) now run the
  remainder tail per craft; leftovers that can't land/merge defer to
  `placeCraftLeftovers` (add → drop) after invMu unlocks.
- `crafterDispenseFrom` dispenses each non-empty remainder through
  `crafterDispenseItem` — disabled slots read empty in the input so
  they yield none, matching vanilla's CraftingInput.

New: `TestCrafterRemainderParity` — 4 honey_bottles → honey_block
through the crafter, diffing item-entity spawn count and the
per-dispense 1049/2010/1050 level events vs vanilla (5 spawns / 5
crafts worth of events). `TestCraftRecipeParity` (incl. the cake
bucket-remainder case) and the unit suite stay green.

## Slice 2026-09-25zo — filled_bucket trigger: measured, kept off

`Bucketable.bucketMobPickup` bytecode does call
`FILLED_BUCKET.trigger(ServerPlayer, bucketStack)` straight-line before
`discard()` — so wiring `fireFilledBucket` into `bucketFish` looked
correct. It is not what the wire shows: on the 26.2 jar the pickup
removes the fish but never lands the criterion — verified end to end by
granting `husbandry/fishy_business` first (making `tactical_fishing`
visible), then bucketing a second cod: `tactical_fishing/cod_bucket`
stays `false`. The trigger call exists but the award never happens —
an apparent vanilla regression in 26.2 (listener map for the trigger
comes back empty, or the predicate never matches; either way zero wire
output). mc-go now documents this and leaves `fireFilledBucket`
unwired so the flush matches vanilla. `TestTacticalFishingParity`
green again.
## Slice 2026-09-25zp — packet census + open_book + debug_sample

Censused all 141 play-clientbound packet ids from
`vanilla/reports/packets.json` against mc-go's emission sites: six
were never emitted. Three resolved this slice:

- `open_book` (0x3a): both `WritableBookItem.use` and
  `WrittenBookItem.use` call `Player.openItemGui`, and the
  `ServerPlayer` override gates the packet on the stack carrying the
  `written_book_content` component — a bare book is a silent no-op.
  mc-go's use_item dispatch now emits `open_book{hand}` under exactly
  that gate. `TestOpenBookParity` pins both hands and the bare-book
  negative on both servers.
- `debug_sample` (0x1e): `ServerboundDebugSubscriptionRequestPacket`
  stores a subscription set of registry ids; vanilla's
  `ServerDebugSubscribers.tick` flushes `RemoteDebugSampleType`TICK_TIME`
  (0) to `dedicated_server_tick_time` subscribers every wall tick —
  and `debugSubscriptions()` is empty unless the player `isOp`, so
  the stream is op-gated on the wire. mc-go stores the set
  (immutable map behind atomic.Pointer) and emits per wall tick.
  `TestDebugSampleParity` pins cadence (~20 Hz), the
  `{long[], varint 0}` shape, and silence before subscribing.
- `recipe_book_remove` (0x4b): false positive — mc-go already emits
  it via `IDPlayRecipeBookRemove` in recipecmd.

Deferred by measurement:
- `teleport_entity` (0x7d) is emitted only by
  `sendTeleportTransitionToRidingPlayers` — riding passengers of a
  teleporting vehicle. It needs the vehicles/riding track, not a
  standalone stub.
- `debug/block_value`/`chunk_value`/`entity_value`/`event` (0x1a/1b/
  1c/1d) are the per-event debug streams behind the other fifteen
  subscription types (pois, paths, brains, neighbor_updates, game
  events, ...). Each is its own producer feature; stored ids land
  with this slice, streams are a later one.

Also measured while probing `/tp @e`: vanilla un-pairs a tracked
entity (sends `remove_entities`) when it leaves the effective
tracking window — `min(viewDistance, clientTrackingRange) * 16`
blocks, axis-aligned — evaluated per `TrackedEntity.update`. mc-go
broadcasts entity packets to every in-dimension session with no
range or pairing state — that is the next slice's work, with the
68-type `clientTrackingRange`/`updateInterval` table extracted from
the jar (Probe.java under tools/).

## Slice 2026-09-25zq — per-player entity tracking (TrackedEntity.updatePlayer)

Replaced the broadcast-to-every-in-dimension-session entity model with
vanilla's per-player pairing state, measured end-to-end from
`ChunkMap$TrackedEntity.updatePlayer` bytecode:

- Pair condition: `dx²+dz² <= d²` where `d = min(getEffectiveRange(),
  getPlayerViewDistance(player)*16)` — the jar operand order puts the
  *view distance* (clamped 2..serverViewDistance, ×16) on the min's
  left, NOT the tracking range. `getEffectiveRange` is
  `clientTrackingRange*16` in blocks, maxed over indirect passengers.
  Also requires `broadcastToPlayer` (always true for our entities) and
  `isChunkTracked(entity.chunk)` — the chunk-tracking view ANDed with
  `!chunkSender.isPending`, the not-yet-written LongSet.
- `pairable`/`tracksEntity`/`setTracked` in track.go hold per-session
  pairing state; `entityTrackStep` re-evaluates on the level tick
  AFTER mobTickStep (vanilla's updateEntitySections runs after the
  entity tick — sendChanges only reaches sessions still paired).
- `pairEnt` re-latches the shared send baseline (VecDeltaCodec analog:
  syncPX/Y/Z, rot bytes, wasOnGround, PosDirty, teleportDelay) so a
  re-pair doesn't emit a residual move/sync against the stale latch.
- `sendPairingData` stream order per javap: add_entity →
  set_entity_data (full tracked values) → update_attributes (living
  only, every syncable attribute) → set_equipment → set_passengers →
  vehicle's set_passengers → set_entity_link. pairingAttrs() emits
  the entity's full attribute-supplier set over the new generated
  `attrIDByName` table (40 names from registries.json), folding the
  speed effect modifier like mobSyncAttrs.
- `removePairing` = stopSeenByPlayer + ClientboundRemoveEntitiesPacket
  (`{count,ids[]}` — count is the first varint, not an entity id).
- `/tp` on an entity only marks needsSync (mc-go's PosDirty +
  teleportDelay=401): the tracker emits entity_position_sync on the
  NEXT sendChanges, never from the command itself — measured.
- `broadcastToTrackers`/`broadcastEntityTeleport` now gate on
  tracksEntity + a live pairable check (player entities excepted —
  stale byID positions make it unsafe there).
- Vanilla's isPending race makes pair/un-pair timing nondeterministic:
  the same 15-block cross-chunk tp produced position_sync, remove, or
  silence across three runs. `TestEntityTrackingParity` therefore pins
  only deterministic transitions: same-chunk move = pos packet no
  remove; far move eventually removes; re-entering range re-pairs with
  the full add_entity+data+attrs stream — race-tolerant on both
  servers, 3× green.

Also fixed a committed regression found bisecting this test's flake:
`golemOnPlace` (inside writeOnPlace) matches the iron-golem pattern
and airs the cells BEFORE `setBlockAndBroadcastNotify` reached
`checkSummonedEntity` — the blk==old early-out made the
summoned_entity grant unreachable. The grant now fires from
`golemSpawn` for every session in the dim (vanilla's
SUMMONED_ENTITY fans over the player list, placer or not), and the
dead `checkSummonedEntity`/`summon.go` is gone. TestSummonedEntityParity
3× green; the earlier flake was two pattern checkers racing.
## Slice 2026-09-25zr — per-entity-type updateInterval cadence (ServerEntity.sendChanges)

Wired the trackSpec table's second column (extracted from the jar in
zq, never applied): vanilla's ServerEntity.sendChanges runs every tick
but only enters the position branch when
`entity.tickCount % updateInterval == 0 || needsSync || entityData.isDirty`.
The interval is per entity type — displays=1, allay/evoker_fangs=2,
eye_of_ender=4, projectiles=10, item/falling_block/experience_orb/arrow
=20, item_frame/glow_item_frame/end_crystal/area_effect_cloud=MAX_INT,
most mobs=3 (tools/trackprobe/trackranges.txt).

- `entitySyncInterval` (track.go) reads the type's interval, default 3;
  `mobSyncFlush` gates `mobMoveSync` on `SyncCount % iv == 0 ||
  PosDirty || MetaDirty` — the fixed `mobUpdateInterval = 3` is gone.
- The gate reads SyncCount BEFORE the tick's increment, matching
  sendChanges' read of the entity's own tickCount: a fresh tracker
  emits on its first sendChanges (0 % iv == 0), which is how vanilla's
  summoned item answers its first fall tick with a move_entity_pos
  ~1 tick after the spawn packet.
- The send baseline latches at first flush (pairing/spawn position —
  vanilla establishes it in ServerEntity.broadcast at pairing), not at
  first gate-fire; latching mid-flight silently ate the interval-20
  item's first emit.
- Teleport marking (tpMove) still rounds SyncCount UP to the next
  interval multiple — syncPosition's `tickCount = (t/I)*I + I`
  measured from bytecode — so the position packet rides this tick and
  the cadence phase restarts a full interval later.
- `teleportDelay` and the %60 periodic resync stay inside the gated
  branch, matching sendChanges: interval-20 items promote to
  position_sync after ~8000 entity ticks of silence, not 400.
- itemTickStep routes through mobSyncFlush (was a teleport-per-tick
  broadcast); item entities now set Grounded on rest so the landing
  onGround flip emits position_sync like vanilla.
- ItemEntity merge/pickup behavior unchanged; XP orbs share the
  interval-20 path.

Pinned by TestItemSyncCadenceParity: summon an item 30 blocks over a
flat drop, timestamp every pos-family packet naming it, and compare
the median inter-emit interval — vanilla spaces emits ~1000 ms
(20 ticks); mc-go must land within ±300 ms. Counts are boundary noise
(first-emit phase + landing timing), so the test pins the spacing,
not the count.

Also fixed the collector bug in trackRead/fallCollect: a read timeout
used to `goto outer`, ending the window after one quiet 80 ms span —
legs now collect for their full duration (tracking legs unchanged,
still green).
## Slice 2026-09-25zs — pairing burst rides ClientboundBundlePacket + set_equipment in-stream

javap'd addPairing: it collects sendPairingData's packet list and wraps
the whole thing in ONE ClientboundBundlePacket — on the wire that's
bundle_delimiter (0x00), the inner packets, bundle_delimiter. Raw
probe on vanilla 26.2: a bare zombie's pairing burst is
`0 1 99 131 0` (add_entity + set_entity_data + update_attributes),
and `0 1 99 131 102 0` when the mob carries equipment.

- sendEntityTo now bookends its stream with PlayBundleDelimiter —
  covers pair, re-pair, and the join-time entity burst identically
  (each addPairing call = one bundle).
- set_equipment added to the pairing stream between attributes and
  passengers (sendPairingData's order): emitted when
  armorStandEquipAll returns non-empty — matching vanilla's
  `!equipmentList.isEmpty()` gate. The EquipDirty post-spawn resend
  stays loose like vanilla's detectEquipmentUpdates packet.
- sendPairingData's remaining in-stream members (velocity,
  vehicle's set_passengers, set_entity_link leash) are still gaps —
  passengers ride the existing set_passengers branch.

Pinned by TestPairingBundleParity: the bundle containing an equipped
zombie's add_entity must carry the identical inner-id sequence on both
servers ([1 99 131 102]).

Also measured this slice: mc-go emits set_entity_data/update_attributes
storms the vanilla pairing + wander legs don't (e.g. leg0 mc-go 13×
{0x53,0x54,0x83} vs vanilla's 1× {0x23}) — a dirty-metadata cadence
diff, queued for the next slice.

## Slice 2026-09-26a — lazy attribute-instance model + leash-knot pairing wire

javap'd ServerEntity: sendPairingData emits
`getSyncableAttributes` — only attribute INSTANCES that exist
(AttributeMap materializes them lazily on first getInstance) AND are
marked syncable. sendChanges emits `getAttributesToSync` — the dirty
subset. Measured in-bundle sets on vanilla 26.2:

- zombie/husk/drowned → [movement_speed] (setBaby's SPEED_MODIFIER_BABY
  remove/add touches the instance — runs even for NBT summons)
- wolf → [max_health] (setTame re-asserts the wild base)
- horse-family → [max_health, jump_strength, movement_speed]
  (AbstractHorse.finalizeSpawn rolls all three)
- every other living mob → nothing in-bundle; a loose
  [movement_speed] update_attributes follows once the first living
  tick materializes the instance (NoAI included).

Model: `Entity.AttrTouched` is the lazy instance set; `AttrTouched ∩
attrSyncable` (the 32 setSyncable attrs from javap Attributes — notably
NOT follow_range/attack_damage/knockback_resistance) is the pairing
burst. `AttrsDirty` is the resend set: instance creation marks dirty,
value writes re-dirty (markAttrDirty), repeat reads do not. summonAttrTouches
encodes the summon-path touches (tamable max_health, zombie-family
speed unconditionally, finalizeSpawn rolls init-only); the speed
instance materializes on the first living mobSyncFlush for everything
else — reproducing vanilla's [26]-in-bundle vs [26]-one-tick-later
split per type.

Leash summon: `summon sheep ~ ~ ~ {leash:[I;x,y,z]}` resolves the knot
AFTER sendPairingData built the sheep's bundle — so the wire is sheep
bundle → knot bundle ({0, add_entity, 0} — the knot's empty nonDefault
set_entity_data is skipped) → loose set_entity_link. mc-go now attaches
the NBT leash after broadcastFreshMob to match. knotTickStep mirrors
BlockAttachedEntity.tick: the survives() fence check runs only every
100 ticks (checkInterval), not every tick — a knot on a non-fence pos
lives ~5s on vanilla. Empty-knot discard moved to notifyLeasheeRemoved
called from leashDrop (vanilla discards the knot eagerly on its last
leashee's detach).

Pinned by TestPairingAttrsParity (in-bundle attr-id sets per summoned
type + loose speed emit) and TestLeashSummonWireParity (sheep bundle →
knot bundle → one loose set_entity_link, no spurious unlink).

## Slice 2026-09-26b — non-Mob LivingEntity has no AI (armor_stand storm)

The tracking legs showed mc-go emitting ~10 move_entity_pos /
rotate_head packets per leg for a static Marker armor stand where
vanilla emits one position sync. Cause: summonMobRaw marks every
LivingEntity Living:true and mobTickStep runs the whole Mob.aiStep
block — wander heading changes, ledge spins, goal steps — on the
armor stand. Vanilla's ArmorStand is a LivingEntity, not a Mob: it
ticks LivingEntity.tick (gravity, effects, leash, equipment sync)
but never reaches Mob.aiStep's goal selector, so it never walks or
turns its own head.

- `nonMobLiving` name-set (armor_stand) now gates the whole AI block
  in mobTickStep (`!e.NoAI && e.Riding == 0 && !nonMobLiving`) — the
  Living-only tick parts (leash, fire, cramming, attr touch,
  metadata/effect sync, despawn rules) still run.
- summonMobRaw's max_health gate already restricts Living:true to
  LivingEntity types, and armor_stand is the only non-Mob
  LivingEntity — the set is complete.

Leg streams after: leg0 identical ([{35}]), legs 1+2 the tolerated
unpair-timing variants, leg3 re-pair bundle identical ([{1 99 131}])
minus vanilla's two post-repair position packets.

## Slice 2026-09-26c — episodic wander: RandomStrollGoal + look goals + tracked motion

The 30s wander probe showed mc-go's continuous crawl emitting ~3.4x
vanilla's position packets with zero set_entity_motion, while vanilla
emits ~40-80 of each class. javap'd the oracle:

- RandomStrollGoal: 1/120 roll per idle tick (reducedTickDelay(120)),
  target = DefaultRandomPos.getPos(mob, 10, 7), walks at the
  movement_speed attribute until navigation is done.
- ServerEntity.sendChanges emits set_entity_motion when
  distanceToSqr(currentDelta, lastSentMovement) > 1e-7, or when the
  delta just turned exactly zero — per-tick emits while accel/decel,
  none at cruise.
- LookAtPlayerGoal + RandomLookAroundGoal re-aim the head on a ~20-40
  tick cadence; LookControl lerps the turn so one look change lands as
  several rotate_head emits.

mc-go now models all three:

- mobMoveStep is episodic: deterministic per-entity stroll interval
  (100-140 ticks via eid hash — kills test flakes), ±10 target pick,
  walk at entityAttrBases movement_speed, ends on arrival (<0.4),
  ledge/obstacle/sun-trim (goal gives up), or a 160-tick stall.
- Entity.SentMotionX/Y/Z mirror lastSentMovement; mobSyncFlush emits
  set_entity_motion on the same drift>1e-7 or became-zero edge.
  MotionX/Z now carry the walking delta; mobFrictionStep decays it
  0.8/tick when idle.
- mobLookStep re-aims e.LookYaw every 20-40 ticks (nearest player
  within 6 blocks, else ±90 offset) and lerps HeadYaw 10°/tick —
  rotate_head count went from ~1 to vanilla's ~30-55 per 30s.

Pig 30s after: pos 49 vs vanilla ~57-102, rotate_head ~24-55 vs ~30-55,
motion ~34-73 vs ~37-84 — all classes inside a 4x band. Pinned by
TestWanderDensityParity (wanderCounts collector + per-class band
asserts). TestMobWanderMovesOnGround loop now runs past the entity's
stroll interval instead of the old continuous-walk 60 ticks.

## Slice 2026-09-26d — entity collision push (LivingEntity.pushEntities)

javap on LivingEntity.tick: pushEntities runs for every living entity —
NoAI included — and Entity.push writes deltaMovement only. The position
integrates in travel→move(), which NoAI mobs never run. mc-go now
models it exactly:

- mobPushStep (Living-level, before mobCrammingStep): iterates
  entities whose AABBs intersect this mob's; skips same-dim failures,
  dead, riders/ridees (isPassengerOfSameVehicle), vehicles-as-target
  sides, and absMax<0.01 near-coincident pairs. Separation normalizes
  by sqrt(absMax), scales min(1,1/absMax)*0.05, writes MotionX/Z on
  both sides — never the position.
- mobMoveStep's idle + stroll-end branches integrate X/Z from the
  delta after mobFrictionStep, so AI mobs drift apart while NoAI mobs
  accumulate an unbounded delta that emits set_entity_motion every
  tick (the SentMotion drift predicate catches it).

Live probe: two NoAI pigs 0.4 apart on vanilla froze at sep=0.100 with
~119 motion emits/pig/6s; mc-go now reports sep=0.100, ~119/118 — the
same storm. Pinned by TestMobPushParity (push_test.go): NoAI legs
assert sep≤0.3 + ≥20 motion emits each on both servers; AI legs
assert separation ≥0.8. The probe's read loop uses one absolute
deadline — a pair of active pigs streams packets every ~25ms, so
per-read quiet windows never fire.

## Slice 2026-09-26e

PanicGoal (javap + tag JSON + live probes): priority-1 goal — a
panic-capable mob whose last damage type is in PANIC_CAUSES runs to a
DefaultRandomPos target (±5 x/z) at speedModifier x movement_speed
until the navigation ends. panicCauseTypes carries the whole tag
(mob_attack, player_attack, arrow, explosion, fire causes, ...);
mobPanicSpeed carries the per-type modifiers (pig/sheep 1.25,
cow 2.0, rabbit 2.2, cat 1.5, ...). damageMob arms PanicTX/TZ away
from the attacker with a hash jitter; mobPanicStep integrates X/Z,
yaws into the heading, writes the delta for the wire, and ends on
arrival or a 60-tick stall. It preempts stroll/eat (priority 1) and
only runs for AI mobs.

Measured on vanilla: a pig hit once sprints ~2.6 b/s for 4-14 blocks
over ~2.5 s (sometimes two bursts). mc-go now lands 4.7 blocks in the
same window. Pinned by TestMobPanicParity (panic_test.go): panic_run
asserts movement in the 1-3.5 s window on both servers;
noai_no_panic asserts a NoAI pig stays under 2.5 blocks.

Fix folded in: rested item entities + XP orbs oscillated
grounded↔airborne every other tick (the below-block sample at
-0.25 lands exactly on the cell boundary at restY), storming
set_entity_motion ~20/s/entity forever — enough packet volume to
starve drain-based test legs. The sample now dips 0.26.

## Slice 2026-09-26f

TemptGoal (javap + FOOD tag JSONs): an animal within TEMPT_RANGE (10.0
attribute default) of a player holding one of its tempting items —
main OR off hand — navigates to the holder at speedModifier ×
movement_speed and stands at the 2.5 default stopDistance, looking at
them. mobTemptFood carries the tag membership (pig: carrot/potato/
beetroot; sheep/cow/mooshroom: wheat; chicken: the six seeds;
equines: the HORSE_FOOD set) and the per-type speedModifier off the
registerGoals constructors (pig 1.2, sheep 1.1, cow/horses 1.25,
chicken 1.0). Rabbit/cat canScare variants are not modeled. Ending
the latch starts the 100-tick calm-down (vanilla
reducedTickDelay(100)).

Measured on vanilla: a pig 7.6 blocks from a carrot-holding player
closed to 2.06 and parked (position stream stops — the goal keeps
the mob still while latched); mc-go parks at 2.16. Pinned by
TestMobTemptParity (tempt_test.go): the tempt leg puts a carrot in
weapon.mainhand via /item replace and asserts closest-approach < 3.5
on both servers; the NoAI leg asserts the pig never approaches.

## Slice 2026-09-26g

FollowParentGoal (javap): a baby animal (Age < 0) scans its bounding
box inflated (8,4,8) for the nearest adult of the SAME type and
follows while the gap sits in the window — start past 3 (9.0 dist²),
keep inside 16 (256 dist²), repathing every ~10 ticks at
speedModifier × movement_speed (pig/sheep/chicken 1.1, cow 1.25,
equines 1.0). mobFollowParentStep recomputes the nearest parent each
tick; the 3-block gap releases the baby to wander, exactly like the
goal dropping.

Two findings folded in: the earlier failed vanilla leg pinned the
scan radius — a baby 9 blocks out never starts (pig bbox + inflate 8
= ~8.45 reach). And entities.All() returns COPIES, so mobPushStep's
neighbour half of the shove silently no-oped; the other entity's
delta now lands through GetPtr.

Measured on vanilla: baby pig spawned 7.5 from a NoAI adult closed to
1.84 and parked (mc-go 2.35). Pinned by TestMobFollowParentParity
(followparent_test.go): baby_follows asserts closest approach < 3.5
on both servers; noai_no_follow asserts a NoAI baby stays past 5.5.

## Slice 2026-09-26h

BreedGoal (javap): feeding only arms the love state — vanilla's pair
then walks to each other through the goal. canUse = isInLove &&
nearest same-species partner (bbox inflated 8 all axes, canMate:
age == 0, not panicking); tick looks at the partner and repaths at
speedModifier 1.0; loveTime counts the approach and the pair breeds
once loveTime >= ~60 with dist² < 9. spawnChildFromBreeding spawns
the baby at the breeder, clears love on both, sets the 6000-tick
Age cooldown, drops a 1-7 xp orb, and fires bred_animals.

This replaced mc-go's instant breed-at-feed within 8: feedAnimal now
only sets loveTicks + hearts (gated on Age == 0 so babies and
cooldown adults can't love), and mobBreedStep runs the approach and
the breedPair aftermath at priority 2-3.

Measured on vanilla: two fed pigs 4 apart produced a baby + xp orb
inside ~4s; mc-go does the same. Pinned by TestMobBreedParity
(breed_test.go): breed_baby asserts a third pig + orb spawn on both
servers; noai_no_breed asserts a NoAI pair never produces one.

## Slice 2026-09-26i

dropAllDeathLoot + dropExperience pinning. Vanilla's gates (javap,
LivingEntity): shouldDropLoot = !isBaby && mob_drops gamerule;
shouldDropExperience = !isBaby; the xp award additionally needs the
lastHurtByPlayer window (100t). The sheep wool pool resolves a
per-color sub-table gated on type_specific/sheep {sheared:false} —
mc-go now skips the wool pool on Sheared instead of always dropping
white_wool.

Pinned by TestMobLootParity (mobloot_test.go): adult_drops asserts a
player-killed pig emits loot + xp on both servers; sheared_no_wool
asserts exactly one item (mutton) on a shorn sheep; baby_no_drops
asserts a baby drops nothing. /damage ... player_attack by @s arms
the killed-by-player window through the command path.

## Slice 2026-09-26j

move_vehicle driver control (javap ServerGamePacketListenerImpl.
handleMoveVehicle): only the root vehicle's FIRST passenger on a
client-controllable vehicle steers — boats always; pig/strider/
happy_ghast and the eight equines only when Saddle (isEquineName is
mount-side Tamed&&!Baby, control-side Saddle). The packet clamps
xz ±3.0E7 / y ±2.0E7, wraps degrees, then measures the delta against
vehicleFirstGood — the mount-time anchor, updated per accepted move.
dist²(delta) - vehicleDelta² > 100 rejects: server logs "moved too
quickly" and rubber-bands the driver with a clientbound move_vehicle
of the server position; otherwise the vehicle applies pos/yaw/ground
and the anchor moves. Mount gates measured on the jar: boats cap 2,
steerables need Saddle, equines need Tamed adult.

Two harness + one real bug fixed along the way: (1) vanilla drops
move_vehicle entirely while updateAwaitingTeleport is pending — the
test's watchers now answer sync_pos/teleport_confirm on BOTH sockets
while polling either one. (2) mc-go's send baseline latched at first
mobSyncFlush — for unticked entities (boats) that was inside
handleMoveVehicle AFTER the position write, so delta measured zero
and no pos packet went out. Baseline now latches at pair time in
sendEntityTo (ServerEntity.broadcast semantics), and
broadcastEntitySpawnIn/join-burst pair on the LIVE entity instead of
their snapshot copies — before this, pairing a copy meant the real
entity's syncInit never set.

Pinned by TestVehicleMoveParity (vehicle_test.go): interact mounts
(set_passengers n=1), a 3-block move_vehicle broadcasts the vehicle
to a watcher on both servers, a 12-block jump draws the 0x39
rubber-band to the driver, and sneak input dismounts (n=0).

Paddle input (javap handlePaddleBoat): the controlled vehicle's
AbstractBoat takes setPaddleState(left,right) — metadata indices
11/12 (bool serializer), each entry dirty only when it changed, so
the watcher-side dirty pack carries just the paddle that moved.
Pairing mid-stroke ships the non-default entries in the attach burst
(encodeBoatMeta). mc-go previously discarded the packet entirely;
handlePaddleBoat now gates on first-passenger of a boat and emits the
per-entry dirty pack. Same test asserts the release re-dirties with
false and that the entries ride the pairing burst.

## Slice 2026-09-26k

Vehicle hurt/damage (javap VehicleEntity.hurtServer): a melee attack
on any VehicleEntity (all _boat/_raft/_chest_* + *minecart — measured
isVehicleEntityName by name suffix) flips DATA_ID_HURTDIR(9) sign,
re-arms DATA_ID_HURT(8) to 10, and banks amount*10 into
DATA_ID_DAMAGE(10). The dirty pack carries only the entries set()
marked dirty (hurtTime re-emit is skipped when a rapid second hit
re-arms while still 10). On the tick both counters decay — hurtTime
1/tick AND damage 1/tick to zero (probe-verified live: the wire
shows the pair counting down in lockstep, emitted every tick not on
the 3-tick sendChanges interval). A hit when banked damage >40
destroys the vehicle: remove_entities plus a spawn of the same-named
item entity at its spot. A player with instabuild (creative) instead
discards it with no drop. shouldSourceDestroy measured false for
boats/minecarts; chest-vehicle contents spill is unmodelled.

attackEntity previously fell through for non-living targets entirely
— boats ate hits silently. New branch gates on isVehicleEntityName
with the same withinAttackRange bounding-box reach check, then runs
vehicleHurt; mobBoundsTable now covers every boat/raft/minecart
registry name (1.375x0.5625 boats, 0.98x0.7 minecarts) so the gate
uses the real box instead of the 0.6x1.8 default.

Pinned by TestVehicleHurtParity (vehicle_test.go): survival punch
emits the {8:10,9:-1,10:10} wobble pack and both counters decay to
zero in view; a second punch flips hurtDir back to +1; a
diamond-sword hit banks >40 in one blow and the watcher sees the
boat removed plus an oak_boat item entity; a creative punch removes
the boat with NO item drop. Green vs vanilla and mc-go.

## Slice 2026-09-26l

Attack invalid-target disconnect (javap handleAttack): after the
world-border and isWithinAttackRange gates, vanilla runs the invalid
check — ItemEntity, ExperienceOrb, the attacker themself, or a
non-attackable AbstractArrow — and DISCONNECTS with
multiplayer.disconnect.invalid_entity_attacked rather than ignoring
the packet. mc-go silently dropped all four; it now kicks with the
same translatable, reach-gated the same way (an out-of-range poke at
an item is still a no-op). The self-attack early return becomes a
kick too — vanilla treats entity==player identically.

Also measured in the same method, for the record: the reach gate is
isWithinAttackRange(mainHand, box, 3.0) — the attack_range attribute
base — and a held stack carrying PIERCING_WEAPON bypasses the
dedicated attack packet entirely.

Same TestVehicleHurtParity test pins the kick on both servers: after
destroying the boat, /summon a fresh item entity with a huge
PickupDelay, attack it — the attacker's socket receives the play
disconnect packet.

## Slice 2026-09-26m — rider seat + boat dismount escape

javap + live probe: positionRider seats a passenger at the vehicle's
PASSENGER attachment minus the rider's VEHICLE attachment — the
fallback attachments every boat/minecart/mob uses give (0, h/2, 0) -
(0,0,0), so the rider sits at vehicle center half-height. mc-go had
the rider AT the vehicle position, 0.28 low.

Dismount is getDismountLocationForPassenger: boats (AbstractBoat
override) run getCollisionHorizontalEscapeVector — half of
vehW*sqrt2 + riderW + eps pushed along the RIDER's yaw, normalized
to the longer axis — then floor candidates at the escaped column's
block pos and below. Our collision model has no partial floors so
non-solid blocks floor at height 0 and full blocks at 1 (rejected by
isBlockFloorValid's h<1 gate). canDismountTo rejects candidates whose
standing AABB intersects a collision block; water below or no valid
candidate falls back to the vehicle's bounding-box top. Live probe
pin: boat at (8.5,-60,8.5), rider yaw 0 -> sync_pos (8.5,-60,9.7723).

The dismount emits the same three wire facts on both servers:
set_passengers cleared, a sync_pos player teleport to the rider, and
an entity_teleport to trackers. TestVehicleMoveParity asserts the
coordinates on the sneak-dismount leg (boat at (11,-60,8) ->
rider at (11,-60,9.2723)).

## Slice 2026-09-26n — minecart mount, seat, dismount scan

javap + live probe on AbstractMinecart: a plain minecart is rideable
through interact (container carts open their UI instead) — mc-go's
mount table now admits name == "minecart" with cap 1, and /summon's
vehicle branch covers the whole *_minecart family with Rotation NBT
applied (it feeds the dismount scan). mc-go previously rejected the
minecart at the summon health gate, so carts couldn't spawn at all.

Seat: minecarts declare passengerAttachments(0.1875) where boats use
the AT_HEIGHT fallback h/2 — passengerSeatY picks the declared value
for the family.

Dismount is AbstractMinecart.getDismountLocationForPassenger: the
behavior's motion direction maps through Direction.fromYRot(yaw),
the scan direction rotates one step clockwise (live probe: rider
lands BEHIND the facing — yaw0 cart drops the rider on the north
block), then offsetsForDirection walks 8 offsets
[cw, ccw, opp+cw, opp+ccw, dir+cw, dir+ccw, opposite, dir] x
POSE_DISMOUNT_HEIGHTS{STANDING:[0,1,-1]} x getBlockFloorHeight.
Candidates land at upFromBottomCenterOf the block pos; our collision
model has no partial floors so non-solid blocks floor at h=0 and
solid blocks reject. Fallback is the vehicle bounding-box top.

TestMinecartDismountParity summons a cart facing each cardinal yaw
on both servers, mounts, sneak-dismounts, and asserts the sync_pos
landing matches the probed block for all four (yaw0 -> N, 90 -> E,
180 -> S, 270 -> W relative to the cart's facing-rotated-90).

## Slice 2026-09-26o — minecart off-rail physics + yaw chase

OldMinecartBehavior is the 26.2 default (MINECART_IMPROVEMENTS is not
in the vanilla flag set), so the off-rail roll in comeOffTrack plus the
yaw chase in tick() is the behavior to match:

Physics (javap + live probe): gravity dy -= 0.08, horizontal clamp
+/-0.4 (0.2 in water), onGround decays the WHOLE delta vec x0.5, then
move(SELF) axis-wise; airborne decays x0.95. A cart summoned with
Motion:[0.3,0,0] rests at the 0.3/(1-0.5) = 0.6-block asymptote
(probe: spawn 8.5 -> 9.0994). No kill threshold — pure geometric
decay.

Yaw (javap tick tail, correction to the earlier probe read): the
chase is atan2(zo-z, xo-x)*180/pi — the NEGATED displacement — so a
+X slide gives 180, not 90; isFlipped adds 180 while moving; then
wrapDegrees(yRot - yRotO) flips the cart and adds 180 whenever the
delta reaches +/-170 (>= on the positive side only); frem-360
normalizes. A +X slide ends yaw = 0 (probe via /data get
Rotation[0]). setRot at spawn latches yRotO, so a summoned
{Rotation:[180]} cart does NOT instantly flip — mc-go latches
CartYawO the same way.

Rider input does NOT move a flat-ground cart: the intent kick lives
inside moveAlongTrack only (probe: 2s forward input, Pos unchanged).
Rail-following itself is still unmodelled — carts standing on rail
blocks are skipped by this tick step entirely.

TestMinecartRollParity: push-slide rest pos ~=9.1 (differential via
/data get Pos[0]), settled yaw ~=0 (Rotation[0]), and a rider's
forward input leaves Pos[0] untouched — on both servers.

## Slice 2026-09-26p — minecart pushAndPickupEntities + no entity collision

OldMinecartBehavior.tick's tail is pushAndPickupEntities
(box = cartBB.inflate(0.2,0,0.2); javap-verified):

- isRideable && horizontalDistanceSqr >= 0.01 (moving): entities in
  the inflated box filtered by pushableBy (isPushable + teams +
  !spectator) are split: Player | IronGolem | AbstractMinecart |
  cart.isVehicle() | e.isPassenger() -> e.push(cart); else
  e.startRiding(cart) — the classic minecart mob pickup.
- else (stationary/non-rideable): NO pushableBy filter, only other
  pushable un-ridden carts get e.push(cart).

push mechanics (javap): Entity.push -> dx=other-self over
absMax(dx,dz), m<0.01 early-out, inv=min(1,1/sqrt(m)), pushes self
-dx*inv*0.05 (when !isVehicle && isPushable) and other +dx*inv*0.05
(when !isVehicle). AbstractMinecart.push overrides: d6<1e-4 gate,
self takes -dx full, other +dx/4 — no vehicle gates. Carts hitting
carts instead run pushOtherMinecart: dir must be within |dot|>0.8
of the pusher's yaw, else no-op; then each side gets delta*0.2 plus
the averaged momentum +/- the 0.05 impulse.

startRiding gates measured: canRide (no sneak, boardingCooldown<=0)
+ canAddPassenger (empty passenger list); seat offset is +0.1875
except villager/wandering_trader at 0.

The BIG correction this slice: there is NO entity-vs-minecart
collision clip. The earlier "cart stops exactly at pig's box edge"
was push-equilibrium — the pig's own pushEntities shoving the cart
back each tick — not a swept collider. getEntityCollisions'
predicate is e.canBeCollidedWith(cart) and nothing in the mob chain
overrides canBeCollidedWith (base Entity = false), so no mob is a
move-collider: a ridden cart plows THROUGH a NoAI pig and rests
~10.0 while the pig accumulates -1.1 Motion[0] (pushed -X once the
cart passes it; the delta never integrates to Pos because NoAI
mobs skip travel). LivingEntity.pushEntities itself is NOT gated
by isEffectiveAi — NoAI mobs still push back.

mc-go: minecartPushPickup (moving->scoop-or-push / still->cart-only)
+ minecartPushCart (pushOtherMinecart: 0.8 facing gate, 0.2 delta +
avg momentum exchange) + mobPushStep now repels carts
(AbstractMinecart.push: cart full, mob quarter, no vehicle gates)
+ per-tick rider weld (cart.X, cart.Y+0.1875-or-0, cart.Z) and
/execute seat-tag selection support (Tags NBT applied to vehicle
summons). Entity clamps reverted — block-only collision.

TestMinecartPushPickupParity (differential, 4 legs): A a sliding
empty cart scoops a NoAI pig and welds it at (cart.X, Y+0.1875)
mid-slide; B two carts rest ~0.5 apart then repel to (8.15, 9.43)
+-0.15; C a ridden cart plows through a NoAI pig, keeps it
un-ridden, and leaves it Motion[0] < -0.02; D a rammer transfers
momentum to a parked cart (vanilla 9.60/10.84, mc-go 9.95/11.19).

## Slice 2026-09-26q — minecart moveAlongTrack rail-following

The big deferred vehicle item: OldMinecartBehavior.moveAlongTrack
(javap, MINECART_IMPROVEMENTS off). tick() order: applyGravity
(dy -= 0.08) -> getCurrentBlockPosOrRailBelow (floor pos, swapped
for y-1 when that block is a rail) -> isRail ? moveAlongTrack +
activator branch : comeOffTrack -> yaw tail -> pushAndPickup.

moveAlongTrack sequence (bytecode-verified): slope accel
+-0.0078125 toward the downhill exit (x0.2 underwater); snap
ty = bp.y (+1 on slopes); exits pair -> vec snapped onto the
rail axis when vec.dot(dir) flips; speed clamp |xz| -> 2.0;
player-intent kick +0.001 skipped (no lastClientMoveIntent —
noted gap); unpowered powered_rail brake (|xz|<0.03 -> zero,
else *0.5); xz snap onto the a1->a2 line at param t; clamped
move (x0.75 when ridden, maxSpeed 0.4 / 0.2 water) with real
axis-cancel on block collision; edge-step (floor(x,z)-bp ==
exit's (x,z) -> y += e.y); applyNaturalSlowdown
(0.997 ridden / 0.96 unridden, x0.95 water, MotionY=0); the
pos0/pos1 bank: getPos at tick-start and post-move, when both
non-null d47 = (pos0y-pos1y)*0.05 rescales xz by (h+d47)/h and
setPos(x, pos1.y, z); cross-block redirect motion ->
h*(floor-bp); powered rail boost |xz|>0.01 -> +=unit*0.06,
else +-0.02 kick toward an isRedstoneConductor neighbour.

EXITS map verified from the static init: straight pairs,
ascending (uphill exit plain, downhill exit .below()), the
four curves.

THE BIG MODEL CORRECTION: the raw anchor formula
fy+0.0625+e.y*0.5 banks a cart onto a line dipped BELOW the
ascending rail's block (-60.44 -> -59.44), which sends a
climbing cart off-rail into a dip/fall/recover stall. Live
probes falsified it: vanilla banks smoothly along the
SURFACE-TO-SURFACE line (-59.9375 -> -58.9375, slope 1/block)
and never leaves the rail's floor y. cartTrackPoint therefore
anchors y at fy+0.0625 + e.y (+1 for slopes), i.e. downhill
exit at this rail's surface, uphill exit at the block above's
surface; dy = a2y-a1y (the bytecode *2 that un-halves the
e.y*0.5 anchors is already folded into the +1 offset).

Fixture lessons: powered rails revert powered->false without
real redstone — plant a redstone_block under each powered
rail. mc-go's execute-if-block still lacks blockstate
predicates (separate gap, deferred).

mc-go: minecartTickStep rewrite (shared gravity -> rail lookup
below-first -> minecartOnRail/minecartOffRail -> yaw tail ->
push/pickup) + cartTrackPoint (getPos fold + anchors + t param)
+ railExits + minecartActivatorHit (eject passengers, hurtTime
10/damage 50/dir flip on powered activator rail).

TestMinecartRailParity (differential, 5 legs, all exact):
A powered run reaches 35.99; B unpowered powered_rail brakes
to rest at 20.86 with mot 0; C flat rail coasts to 29.35; D
L-curve (east_west + south_west + north_south) parks at
(25.50, 27.51); E ascending climb banks to the top rail at
(24.63, -58.94) — identical on vanilla and mc-go to 4 decimals.

## Slice 2026-09-26r — minecart rider intent-kick + activator eject + RootVehicle

Measured on 26.2 bytecode + live probes:

Intent kick (OldMinecartBehavior ~449-583): controller =
getFirstPassenger(); intent = controller instanceof ServerPlayer ?
getLastClientMoveIntent() : Vec3.ZERO — fires iff controller is a
Player && intent.lengthSqr()>0 && intent.normalize().lengthSqr()>0
&& delta.horizontalDistanceSqr()<0.01: delta += (intent.x*0.001, 0,
intent.z*0.001) AND clears the unpowered-powered_rail brake flag for
the tick. getLastClientMoveIntent (ServerPlayer): x = left!=right ?
(left?+1:-1) : 0; z = forward!=backward ? (forward?+1:-1) : 0; then
getInputVector(vec,1.0,yRot): len2<1e-7->ZERO, len2>1->normalize,
vec2=vec*scale, returns (x*cos - z*sin, y, z*cos + x*sin) with
sin/cos(yRot*0.017453292). Input flags byte (serverbound
player_input 0x2B): 0x01 forward 0x02 back 0x04 left 0x08 right
0x10 jump 0x20 shift 0x40 sprint.

Rider NBT probe findings (both measured live, then pinned):
- Players never serialize into the vehicle's Passengers — the
  mounted state is only visible on the RIDER: RootVehicle =
  {Entity: <vehicle full save, id included>, Attach: <vehicle UUID
  int-array>}; absent when on foot.
- /ride mount reports success but the mount does not stick —
  IDENTICAL on vanilla and mc-go (matching quirk, right-click
  interact is the working mount path in tests).
- 60-tick boardingCooldown after a dismount blocks re-mounting;
  activator ejects count.
- A powered activator_rail ejects EVERY tick a mounted cart sits
  on it and stamps hurt metadata (hurtDir flip, hurtTime 10,
  damage 50) when hurtTime hits 0 — isVehicle-gated so the damage
  proves the mount happened.

mc-go: moveInputX/Z on the session (handlePlayerInput decodes the
flags into left/right +1/-1 x and forward/back +1/-1 z);
moveIntentVec (getInputVector rotate); minecartOnRail's brake now
skips on the intent kick (+0.001 xz toward rotated intent) when a
player rider has WASD intent and the cart is near stopped;
minecartActivatorHit already ejected (prev slice); vehicleTickStep
emits the hurt countdown metadata per tick while dirty.
playerSaveMap emits RootVehicle {Entity: vehicle full save + id,
Attach: uuid-ints} while mounted — entitySaveMap reused, "id" key
overlaid (the embedded save carries id even though data-get strips
it). entitySaveExtras gains minecart FlippedRotation/HasTicked.

TestMinecartRiderKickParity (differential): leg A mounts the
activator cart via right-click interact -> floatMax[10] >= 45
(damage 50 decaying) + RootVehicle gone on the rider; leg B mounts
the dead powered_rail cart as player B and holds forward ->
the cart creeps past the brake run to x 23.24 (vanilla) / 23.19
(mc-go) vs the dead stop without input.

## Slice 2026-09-26s — non-living save shape + minecart variant NBT

Measured: `data get entity` on a summoned minecart, every variant
(chest/furnace/hopper/tnt/command_block/spawner) and an oak_boat on
vanilla — NONE emit LivingEntity fields (Health, HurtTime,
DeathTime, AbsorptionAmount, FallFlying, LeftHanded, NoAI,
CanPickUpLoot, PersistenceRequired, equipment, attributes,
active_effects, leash, Brain, current_impulse_context_reset_grace_time).
mc-go leaked the whole living block onto every non-living entity.
Variant save keys on vanilla (live dump): chest=`Items`; hopper=
`Enabled1b`,`Items`; furnace=`Fuel0s`,`PushX/PushZ 0.0d`; tnt=
`fuse -1` int; command_block=`Command ""`, `SuccessCount 0`,
`UpdateLastExecution 1b`, `TrackOutput 1b`; spawner=`Delay`,
`MinSpawnDelay200s`, `MaxSpawnDelay800s`, `SpawnCount4s`,
`MaxNearbyEntities6s`, `RequiredPlayerRange16s`, `SpawnRange4s`,
`SpawnPotentials[{data:{entity:{}},weight:1}]`. All carts also
carry `FlippedRotation 0b` + `HasTicked 0b`.

mc-go: entitySaveMap now emits the shared entity fields for every
entity and gates the entire living block on e.Living — the living
keys, equipment map, horse/sheep/pig per-type fields, bee fields,
villager fields, attributes + active_effects, and the leash entry.
summonMobRaw already set Living on every mob path, so mobs serialize
unchanged. entitySaveExtras swaps the old `minecart` entry for the
full measured per-variant table (Items/Enabled/Fuel+Push/fuse/
command-block quad/spawner octet + SpawnPotentials). AbstractCart's
FlippedRotation + HasTicked emit under isMinecartName.

Spawner Delay is live on vanilla: randomized into
[MinSpawnDelay, MaxSpawnDelay] on the first tick and counts down —
the test asserts presence + range (0..800), not a value; the
countdown itself stays a spawner-behavior gap. dataNumRe only
matches d/f-suffixed numbers, so dataGetNum (loose suffix
regex over dataGetReply) reads bytes/shorts/ints, and dataGetReply
pins presence for non-numeric values (Items, Command, SpawnPotentials).

TestMinecartVariantNBTParity (differential): summons all 7 cart
types + an oak_boat on each server; per cart asserts
FlippedRotation/HasTicked = 0 and Health absent; 15 variant
key/value checks + 4 presence checks; boat Health absent. PASS on
vanilla and mc-go.

Notes: boats are non-living too — vanilla's oak_boat emits the
same shared-only shape. Preexisting gap found: TestDataEntityParity
fails on clean master — mc-go emits an extra set_entity_data flush
around the /summon success chat (meta,meta,chat,meta vs vanilla
meta,chat,meta) — same packet set, ordering + one extra flush;
tracked here, not touched this slice.

## Slice 2026-09-26t — attach-bundle meta dedup (/summon + item drops)

Root cause of the TestDataEntityParity gap logged in 26s: sendEntityTo
ships the entity's metadata INSIDE the attach bundle (vanilla's
sendPairingData carries exactly one set_entity_data in the bundle), but
broadcastFreshMob and eight item/living spawn sites then re-sent the
same metadata via a standalone broadcastEntityMetadata — a duplicate
packet vanilla never emits. Wire diff was mc-go [bundle-meta, chat,
extra-meta, dirty] vs vanilla [bundle-meta, chat, dirty].

mc-go: removed the redundant broadcastEntityMetadata at every site
where the entity's Item/living fields were already set before
broadcastEntitySpawn (broadcastFreshMob, spawnMobStack, runSummon's
item path, allay/chest/fishing/furnace/hopper/inventory drop sites).
Kept the sites where the bundle legitimately carries nil meta —
spawnXPOrb, cloud, bobber, splash-potion without Item, dispense's
late Item assignment — those explicit sends are the only meta on the
wire. Kept MetaDirty=true on fresh mobs: the post-attach dirty resend
on the next tick is also vanilla behavior.

TestDataEntityParity now PASSES (was failing on clean master). The
sweep covering every touched path (22 tests: item drops, merges,
hoppers, furnace, chest, fishing, allay, breeding, leashing, summons,
equipment, loot, tracking, thrown pickups, S-NBT) is clean except two
preexisting/flaky tests: TestItemDropMergeParity (vanilla-side merge
timing flake, passes on rerun) and TestSurvivalBreakDropPickupPackets
(fails identically on clean master — block-break item spawn packet
never reaches the client; real gap, next slice).

## Slice 2026-09-26u — break-flow packet order (test fix, not a server gap)

TestSurvivalBreakDropPickupPackets asserted block_update BEFORE the
item spawn bundle — failing on clean master. Bytecode check on the
26.2 jar shows vanilla defers it instead: ServerPlayerGameMode.
destroyBlock → removeBlock → markAndNotifyBlock → ChunkHolder.
blockChanged only APPENDS to changedBlocksPerSection (the packet
lands at the end-of-tick broadcastChanges), while playerDestroy →
popResource → addFreshEntity → sendPairingData writes the
delimiter/spawn/meta bundle immediately. Vanilla's wire order is
bundle first, block update at the tick flush — exactly mc-go's
(broadcastBlockState dirties the section, sendEntityTo writes
directly). Test rewritten to collect block update + spawn + item
meta in arrival order; all three with the same field checks.

## Slice 2026-09-26v — /dialog show_dialog + clear_dialog packets

runDialog only echoed sendSuccess chat — vanilla's DialogCommand also
writes the client-bound packets ahead of the echo (wire probe on the
26.2 jar: show → show_dialog then chat; clear → clear_dialog then
chat). show_dialog's body is a Holder: one VarInt, registry index + 1
(the all-elements holder encodes element i as i+1). registry.go now
keeps a dialogIDs entry map parsed from the captured
026-minecraft_dialog.bin next to biomeIDs — server_links lands at
index 2 → body 03, matching the probe. runDialog writes the packet to
each target before returning the chat reply, so wire order matches.
TestDialogPacketsParity diffs the full window (packet + echo, and
the no_such_dialog chat-only error path) verbatim on both servers.

## Slice 2026-09-26y — stonecutter menu end to end

The stonecutter is live: filtered stonecutting list rebuilt when the
input item type changes (datapack recipe order, measured identical on
the wire — stone cuts [chiseled, cobblestone, cobble_slab, cobble_stairs,
cobble_wall, ...]), button click selects by index (out-of-range
ignored silently), take shrinks the input by one and plays
ui.stonecutter.take_result at the block (holder 1673 = id 1672 + 1),
selection persists across takes while the item type holds, and the
transient container returns its input on close. Measured quirks pinned
in TestStonecutterParity: the input slot has NO mayPlace filter (dirt
swaps in), select plays no wire sound. Result takes now merge onto a
stackable cursor (doClick pickup-all). Two latent fixes the pin
exposed: a self-deadlock when a slotsChanged hook reads menuStack
under containers.mu, and smithing inputs also return on close via the
same transient-container path... (smithing close-return not added —
flagged as a likely follow-up gap).

## Slice 2026-09-26x — smithing_trim: minecraft:trim component + recipe_crafted

The trim path now produces results: ItemStack.Trim models the
minecraft:trim component as "<material>/<pattern>" (bare names),
writeSlotPatch emits comp 56 with ItemTrim's holder order (material
first, then pattern — each a registry id +1; measured on the wire:
lapis bolt emits 38 07 01) appended last since
SmithingTrimRecipe.assemble sets TRIM on a copy of the base.
smithingCompute handles smithing_trim recipes: result = copy of the
base + trim (material from the addition's provides_trim_material —
the 11-item measured table — pattern from the template's
*_armor_trim_smithing_template suffix). Take paths feed the matched
recipe id into recipe_crafted on the pre-consume inputs, which is
what grants the trim advancements (Crafting a New Look announced on
the first take — measured announce=1, matched).

Measured surprise: vanilla does NOT reject same-material trims —
checkMaterialEquality compares the RECIPE ingredient's material (the
trim_materials tag's first member) against the armor's, never the
input stack's, so diamond-on-diamond emits [tdiamond/bolt] fine.
Pinned in TestSmithingTrimParity including that case.

## Slice 2026-09-26w — smithing_transform menu + two latent bugs the pin exposed

Wired the smithing table's result slot end to end: smithingCompute
mirrors TransmuteRecipe.assemble (result = recipe item with the base's
whole component patch carried — renamed+enchanted diamond chestplate
comes out renamed+enchanted netherite), recompute rides inside the
click's slotsChanged step so its update shares the broadcast batch and
window state, smithingConsume shrinks all three ingredient slots by
one on any take path (hand / quick-move / throw), and
smithingAccepts gives each input slot the union mayPlace vanilla
builds from the recipe registry. Measured on the jar: no sound, no
world event, no advancement announce on the take. smithing_trim
still needs the unmodeled trim component.

The pin surfaced two unrelated latent bugs, both fixed:
- syncContainerMenuSlots bumped windowState on EVERY click including
  no-ops that send nothing state-bearing; the client's echoed state
  then lagged and the NEXT click arrived stale -> resync+drop.
  The bump now happens only when a slot update actually emits
  (vanilla's broadcastChanges semantics: state travels with packets).
- fireGameInventoryChanged flattened multi-predicate items[] into a
  single OR-set, granting net/netherite_armor off one piece; the
  parse now keeps one name-set per predicate and requires every set
  to match some slot (Cover Me in Debris needs all four).

## Slice 2026-09-27a — grindstone (result compute, merge, disenchant, XP orbs, levelEvent 1042, mayPlace) + transient-menu close-return
Measured on the 26.2 jar (`GrindstoneMenu*` bytecode) + live packet probes: computeResult (count>1→empty; pair→mergeItems; single→strip), mergeItems (same id; damageable → own-max remaining sum + 5% of max; non-damageable → count-2 when stackable+matches), mergeEnchantsFrom (curse already-present skipped, else SUM upgrade), removeNonCursesFrom (crafting comp = stored_enchantments for enchanted_book else enchantments; empty stored → transmute to book; repair_cost = 2c+1 fold per kept curse → 1/3), getExperienceFromItem/getExperienceAmount (Σ getMinCost per non-curse → i3=ceil(x/2) + rand[0,i3)), ExperienceOrb denominations {2477…1}, onTake order (orbs + levelEvent 1042 BEFORE slot clears), GrindstoneMenu$2/$3.mayPlace = isDamageableItem||hasAnyEnchantments (stone can't enter — the non-damageable merge is reachable only via enchanted stackables like sticks).
mc-go gains: `internal/server/grindstone.go` (compute/merge/strip/XP/denominations/awardXPOrbs/grindstoneTake/transientMenuReturn), wired into container.go (mayPlace gate, post-click recompute, mode-0/1/4 take sites) and chest.go (grindstone + smithing inputs return on close — `removed`→clearContainer parity). Also fixed: `item replace` now parses `stored_enchantments` (book give was silently failing), `ItemStack.Stackable` now compares `Trim`, and the test infra sorts `[e…]`/`[se…]` entries (vanilla's ItemEnchantments is an Object2IntOpenHashMap — wire order is bucket order, not insert order).
`TestGrindstoneParity` is differential vs BOTH servers: single/curse/book/pair sticks exact result keys, mayPlace refusal keeps carried stack, enchanted-stick 2-stack count>1 → empty, stick pair → `974#2` merge, named+rc input drops repair_cost, mismatch → empty, per-take XP delta inside the measured random ranges, levelEvent 1042 per take.

## Slice 2026-09-27b — loom (selectable patterns, selection prop, result build, take, 6-layer cap, mayPlace) + banner_patterns wire component
Measured on the 26.2 jar (`LoomMenu*` bytecode), the captured config burst (`020-minecraft_banner_pattern.bin`, `tags.bin`) + live click probes: getSelectablePatterns (empty pattern slot → no_item_required tag iterated in its bound file order — NOT registry order; a pattern item → its provides_banner_patterns holder: 8 namesakes + field_masoned→bricks + bordure_indented→curly_border), slotsChanged (empty banner/dye → clear result+list+sel-1; 1-entry list auto-selects 0; prior selection re-indexes by holder or resets -1; ≥6 input layers force sel-1+empty result; fires only on loom-container changes, never player-slot clicks), clickMenuButton (valid index → sel+setupResult; NO layer check — clicking at 6 layers writes a 7-layer result), setupResultSlot (count-1 banner copy + BANNER_PATTERNS.append {pattern holder, dye ordinal}), onTake (banner.remove(1)+dye.remove(1), pattern item kept, sel-1 only when BOTH run out, ui.loom.take_result at the block), selectedBannerPatternIndex = Container Property 0 (standalone initial 0; vanilla emits -1 only on a real selection reset), mayPlace ($3 BannerItem, $4 loom_dyes tag, $5 loom_patterns tag, $6 false). The banner_pattern registry order is the sorted datapack file order — verified off the captured registry packet: 43 entries, no field_masoned (that item provides bricks).
mc-go gains: `internal/server/loom.go` (loomSelectable/loomSelMove/loomSync/loomSelect/loomTake/loomAccepts + the 43-pattern registry table, the 32-tag-order free list, the 10 pattern-item provides map, the 16 dye ordinals), `ItemStack.BannerPatterns` + the compBannerPatterns wire encode (holder id+1, dye ordinal) + Stackable equality, wired into container.go (loomData slots, accepts gate, loomSync on loom-slot touches only, prop-0 open emit), sbpackets.go (button → loomSelect), chest.go (3 inputs return on close via transientMenuReturn).
The pin surfaced a real wire bug: the compBannerPatterns entry was emitted after the patch's add/remove counts without bumping the add counter — vanilla reads it as trailing garbage and the result shows a plain banner.
`TestLoomParity` is differential vs BOTH servers: free-list selections by index (tag-file order), bad-button drops, take+consume+sound, layered input re-looming, 2-layer result key, black-banner swap flows, pattern-item auto-select (creeper→1-entry), gated patterns hidden when the item leaves, mayPlace refusals (dirt/stone/dye-into-banner/banner-into-dye), the 6-layer refuse-on-input, and clickMenuButton's missing cap (7-layer result).

## Slice 2026-09-27c — cartography_table (map data, scale/lock/clone recipes, post-processing take, empty-map use, mayPlace, quick-move relocation)
Measured on the 26.2 jar (`CartographyTableMenu*` + `MapItem`/`EmptyMapItem` bytecode) + live packet probes: slotsChanged gates on the container slots only (same rule as loom — player-slot clicks don't recompute); setupResultSlot reads the map_id's saved data (missing → no result), paper + unlocked + scale<4 → count-1 copy + MAP_POST_PROCESSING=SCALE, glass_pane + unlocked → =LOCK, empty map → count-2 copy of the same id, else clear; onTake removes one of each input, resolves the post via onCraftedPostProcess (LOCK/SCALE mint a fresh map id whose data is {locked}/{scale+1}), and plays ui.cartography_table.take_result deduped by gameTime; mayPlace is has(MAP_ID) / is(PAPER)||is(MAP)||is(GLASS_PANE) / false; quickMoveStack splits filled maps→0, paper/empty-map/glass→1, and relocates anything else across the player sections (inv→hotbar, hotbar→inv, both forward — mc-go used to leave non-matching stacks alone, a gap every typed-dests menu shares). EmptyMapItem.use mints scale-0 data, plays take_result entity-anchored (ClientboundSoundEntityPacket, PLAYERS source — not the position packet), and in creative leaves the hand alone so the filled map lands via Inventory.add.
mc-go gains: `internal/server/mapdata.go` (mapStore {scale,locked} + useEmptyMap), `internal/server/cartography.go` (accepts/sync/take/applyPost + the window-aware inventory emitters syncInventorySlot/addStackMenu — a use_item transform while a menu is open now emits on the open window like broadcastChanges, not window-0), `ItemStack.MapID`/`MapPost` (+1 encodings) on the wire (comps 46/48), `spec.reloc` + relocatePlayerSlots for the player-side quick-move split, close-return of both inputs.
`TestCartographyParity` is differential vs BOTH servers: empty-map use lands the filled map via add, 4-step scale chain to the scale<4 gate, glass→lock, locked refusing paper+glass but allowing the count-2 clone, mayPlace refusals (dirt/empty-map into the map slot, filled map into additional), quick-move relocation into the hotbar and the occupied-map-slot stay, close returning both inputs, and take_result firing exactly once per take.

## Slice 2026-09-27d — crafting_special_shielddecoration (banner + shield decoration, layers + base_color copy) + base_color wire component
Measured on the 26.2 jar (`ShieldDecorationRecipe` bytecode): matches needs exactly two grid items — one passing the `#minecraft:banners` ingredient AND instanceof BannerItem, one passing the `minecraft:shield` target ingredient with `getOrDefault(BANNER_PATTERNS, EMPTY).layers().isEmpty()` — duplicates or a patterned shield fail; assemble is TransmuteRecipe.createWithOriginalComponents(resultTemplate, shield) (the shield's whole patch rides onto the result — custom_name/damage preserved) then `set(BANNER_PATTERNS, banner's)` + `set(BASE_COLOR, banner.getColor())`.
mc-go gains: `matchShieldDecoration` in `internal/server/recipes.go` (dispatched on `crafting_special_shielddecoration`; banner via loomIsBanner — covers the 16 placeable banners the BannerItem check sees; a shield already carrying layers refuses), `ItemStack.BaseColor` (+1 encoding, comp 73 = bare DyeColor VarInt), wire decode case 73 + `[bc]` stackKey bracket, `item replace`/`give` parsing of `banner_patterns` ([{pattern,color}…] SNBT list), `base_color` (dye name), and `!banner_patterns`/`!base_color` clears.
Six new TestCraftRecipeParity cases are differential vs BOTH servers: patterned banner copies layers + blue bc, plain banner gives bc only, re-decorating a base_color shield (vanilla's gate checks banner_patterns only — a bc-only shield re-decorates), two-banner/two-shield/3-item refusals, banner+non-shield mismatch, and the renamed+damaged shield keeping name and damage on the result.

## Slice 2026-09-27e — crafting_special_bookcloning (written_book + writable_book copies, generation gate, source remainder)
Measured on the 26.2 jar (`BookCloningRecipe` bytecode): matches needs ingredientCount≥2 — exactly one item passing the `minecraft:written_book` source ingredient WITH written_book_content whose generation is inside `DEFAULT_BOOK_GENERATION_RANGES` between(0,1) (the serializer default when allowed_generations is absent; declared ranges clamp to between(0,2)) — plus ≥1 item passing `#minecraft:book_cloning_target` (writable_book) — anything else fails the whole match. assemble = `createWithOriginalComponents(result, source, materials-1)` → written_book template + source's whole patch, count = material stack count, then `set(WRITTEN_BOOK_CONTENT, content.craftCopy())` — a new content with generation+1. getRemainingItems: craftingRemainder template wins first; else a stack carrying written_book_content leaves `copyWithCount(1)` — the source book merges back post-decrement (unconsumed); a bare written_book carries no default component so it fails `has()` and is never a source.
mc-go gains: `matchBookCloning` (dispatched on `crafting_special_bookcloning`), `bookCanCopy`/`bookCraftCopy` in book.go (generation [0,1] gate + the gen+1 rebuild), and the recipe-typed remainder tail in BOTH take paths (`takeTableResult` + `takeCraftResult`): when the winning recipe is book_cloning, a slot whose stack has written_book_content leaves `withCount(1)` — merging onto the decremented stack or landing where it emptied.
`TestCraftRecipeParity` gains five differential cases vs BOTH servers: single clone (gen 0→1 result, source left in the grid), two writable stacks producing a count-2 result on the 3x3 table with the source surviving the take, generation 2 refused, a bare written_book refused, and source+writable+dirt refused.

## Slice 2026-09-27f — crafting_special_mapextending (fixed 3x3 paper ring + center filled_map, saved-data gates, post-processed take)
Measured on the 26.2 jar (`MapExtendingRecipe` bytecode): matches is a hardcoded `ShapedRecipePattern.of(map, "###", "#x#", "###")` — eight paper around one `minecraft:filled_map` center — then `findFilledMap` must be non-empty AND `MapItem.getSavedData(stack, level)` must resolve (a map_id pointing at real data) with `!isExplorationMap()` and `data.scale < 4`. assemble = `createWithOriginalComponents(filled_map template, map)` — count-1 with the source's whole patch — then `set(MAP_POST_PROCESSING, SCALE)`; ResultSlot.onTake runs onCraftedPostProcess so the taken stack mints a fresh derived map id (scale+1) before it lands.
mc-go gains: `matchMapExtending` in `internal/server/recipes.go` (dispatched on `crafting_special_mapextending`; all nine cells required — a 2x2 window-0 grid can't hold the pattern, and missing data or scale>=4 refuse), `craftResult` now takes the dimension's `*mapStore` threaded from every caller (table refresh/take, window-0 refresh/take, crafter), and the take-time post-processing applies `cartographyApplyPost` to the taken stack in BOTH take paths.
The pin surfaced a real menu-sync gap: `useEmptyMap` emitted its add via `addStackMenu` only when the open window had `w.container` — under a crafting table (craft window, no container) the filled map entered inventory unsynced. `addStackMenu` now keys off `w.containerSize()` (kind-generic) and runs for any open window.
`TestMapExtendingParity` is differential vs BOTH servers: empty-map use under the open menu mints + lands the filled map, the 8-paper ring shows `1133#1[mid*][mpp1]`, four take/re-center steps chain scale 0→4 minting a new map id per take, and the scale-4 map refuses a result.

## Slice 2026-09-27g — crafting_special_bannerduplicate (two same-color banners, one patterned ≤6 layers + one plain, count-1 copy)
Measured on the 26.2 jar (`BannerDuplicateRecipe` bytecode): matches needs ingredientCount==2 — every item must pass the `minecraft:banner` ingredient AND be instanceof BannerItem, the first banner's getColor pins the DyeColor and the second must equal it — then per stack `layers().size()` gates: >6 → false, >0 → the single patterned slot (a second one → false), ==0 → the single plain slot. assemble scans for the 1..6-layer stack and returns `createWithOriginalComponents(resultTemplate, stack)` — the patterned stack's whole patch on the count-1 template.
mc-go gains: `matchBannerDuplicate` in `internal/server/recipes.go` (dispatched on `crafting_special_bannerduplicate`; same-color via the banner's dye-name suffix, `bannerLayerCount` off the "pat:color,…" string).
`TestCraftRecipeParity` gains six differential cases vs BOTH servers: patterned+plain duplicates, a count-2 patterned stack still yielding count-1, mismatched colors refused, two-patterned and two-plain refused, a 7-layer stack refused, and exactly six layers still duplicating.

## Slice 2026-09-27h — crafting_special_repairitem (canCombine count-1 gate, fresh-stack result, curse-only merge)
Measured on the 26.2 jar (`RepairItemRecipe` bytecode): canCombine requires the second stack to `is(item1)` the same item, BOTH stacks at count 1, and both carrying MAX_DAMAGE and DAMAGE. assemble builds `new ItemStack(item)` — a FRESH default stack, so custom_name/lore/other patch components all drop — sets `MAX_DAMAGE = max(maxD1, maxD2)`, `damage = max(0, i5-(rem1+rem2+i5*5/100))` (rem = max-damage each), then updateEnchantments with a union merge that keeps ONLY CURSE-tagged enchantments (`minecraft:curse` = binding_curse + vanishing_curse) at the max level of either input.
mc-go gains: `matchRepair` now enforces count-1 on both inputs (a 2-stack no longer repairs) and carries the merged curses onto the result via the shared grindstone helpers (`grindstoneCurses`, `grindstoneEnchEntries`, `grindstoneWriteEnch`); the damage formula is written the way the bytecode computes it (identical result for same-item pairs, where both maxes are the item's own).
`TestCraftRecipeParity` gains two differential cases vs BOTH servers: repair-curse merges binding+vanishing curses while dropping sharpness and the name, and repair-stack refuses a count-2 input stack.

## Slice 2026-09-27i — firework_star/firework_star_fade/firework_rocket + the vacuous-case repair
Measured on the 26.2 jar (`FireworkStarRecipe`, `FireworkStarFadeRecipe`, `FireworkRocketRecipe`, `DyeColor` bytecode + `Items.<clinit>` defaults):
- star: [gp + star-shape modifier? (feather→burst, fire_charge→large_ball, gold_nugget→star, #skulls→creeper, default small_ball) + 1..8 dyes + optional trail(diamond)+twinkle(glowstone_dust)] → `firework_explosion{shape, colors per dye's fireworkColor table (red=11743532 NOT the text color 11546150), fade_colors[], trail, twinkle}`; >8 dyes or >1 shape/trail/twinkle → no match.
- fade: [star + 1..8 dyes] → appends dye colors into fade_colors; a star carrying no firework_explosion comp refuses.
- rocket: [paper + 1..3 gunpowder + 0..N stars] → `Fireworks(fuelCount, [explosions])`; stars are OPTIONAL. The result patches comp 69 — and Fireworks(1,[]) IS the item's default, so a starless single-fuel craft drops off the wire patch entirely (bare `1272#3`) — the "which recipe wins for {paper,gp}" question moots itself: the special always wins AND produces the default.
- Wire shapes: firework_explosion = `shape VarInt | INT-list colors | INT-list fade | trail | twinkle`; fireworks = `flight VarInt | VarInt-counted explosions`.
mc-go gains `internal/server/firework.go` (DyeColor.fireworkColor table, shape-name table, SNBT explosion parser, the three matchers dispatched on the special-ids) plus `FireworkExp`/`Fireworks` ItemStack fields, comp-68/69 emit+read, and `!`/`=` give support.
Fixes the new differential coverage forced out:
- `parseSNBTArray` rejected empty typed arrays — `[I;]` (a star patching `fade_colors:[I;]`) now parses.
- `matchFireworkRocket` skips emitting `Fireworks == "1"` (default-equal → wire-omitted).
- `BannerDuplicateRecipe.getRemainingItems`: a stack carrying banner_patterns leaves `copyWithCount(1)` — added to both take paths.
- SUITE-WIDE: all `menu:2`+`w0:true` cases were VACUOUS — vanilla `handleContainerClick` early-returns on containerId mismatch, so window-0 clicks while a chest menu is open are silently ignored; those cases pinned nothing. All 20 converted to `menu:12` (real craft-table menu) with remapped slots — the conversion immediately surfaced every fix above plus a no-op-click desync: a click that changes nothing must emit nothing or the client's next stateId lags; mode-0 compares before/after stacks, quick-move checks the move happened, both take paths return on empty touched.
- The full-suite run then exposed a TEST-side pollution: every case teleports to the same spot (6,-60,2), and leftover drops pooled there got vacuumed into the next joiner's inventory — repair-stack/firework-star diffs showed foreign books/banners. `newClickDriverOn` now hashes the driver name to a per-case 32-block station offset.
`TestCraftRecipeParity` gains four firework cases (star+flags+nofuel+fade, rocket plain/starless/2-fuel) — 37/37 green vs BOTH servers.

## Slice 2026-09-27j — crafting_dye (DyeRecipe: one dyeable + 1..N dyes, applyDyes channel mix)
Measured on the 26.2 jar (`DyeRecipe`/`DyedItemColor`/`DyeColor` bytecode): matches needs ingredientCount>=2, exactly one stack passing the target ingredient (the result item for all six dyeable recipes — leather armor x4 + leather_horse_armor + wolf_armor), every other slot a #dyes item that carries the DYE component. Assemble is createWithOriginalComponents(result, target) — the target's whole patch on a count-1 template — then DYED_COLOR = applyDyes(existing, dyes): the existing dyed_color contributes its channels once (no default on undyed leather — the brown is a texture, not a component), each dye adds its textureDiffuseColor (arg5 of DyeColor.<clinit>: red=11546150, white=16383998, ...), channels average over the entry count, then rescale by (mean of per-entry max channel)/(max of mean channels) in f32 — Java's f2i truncates like Go int(f).
mc-go gains: `matchCraftingDye` in `internal/server/loom.go` (`dyeOrdOf` — explicit `dye` comp override or the dye item's own color; `applyDyeMix` mirroring the f32 math), dispatched on `crafting_dye`; `Dye`/`DyedColor` ItemStack fields (ord+1 / rgb int), comp-43/44 emit+read, `!`/`=` give support.
`TestCraftRecipeParity` gains six differential cases vs BOTH servers: a single dye, a two-dye mix, a pre-dyed re-dye (existing color enters the mix), wolf_armor, two-target refusal, and a dye-free pair refusal.

## Slice 2026-09-27k — crafting_imbue (ImbueRecipe: potion ringed by material, POTION_CONTENTS copy)
Measured on the 26.2 jar (ImbueRecipe bytecode): matches wants width==3 && height==3 && ingredientCount==9 — the whole 3x3 filled — the (1,1) cell tests the `source` ingredient (lingering_potion), the eight ring cells the `material` (arrow). assemble is `result.create()` then `set(POTION_CONTENTS, source.get(POTION_CONTENTS))` — the tipped arrows inherit the potion's whole component. A lingering potion has no crafting remainder (Items.<clinit> sets craftRemainder only on the two soup buckets, dragon_breath, and honey_bottle — potions use usingConvertsTo).
Also fixed a generator gap: gendata read `input`/`material` for crafting_dye and crafting_imbue while the data pack spells them `target`/`dye` and `source`/`material` — the generated rows carried empty ingredient lists. Regenerated: dye recipes now carry their target+dyes, tipped_arrow its source+arrows.
mc-go: `matchImbue` in `internal/server/recipes.go` — all nine filled, center `r.Input`, ring `r.Material`, fresh result template with the source stack's `Potion` field copied.
`TestCraftRecipeParity` gains three differential cases vs BOTH servers: a harming-lingering imbuement (result carries [p"harming"] via stackKey's existing bracket), an eight-slots-only refusal, and a splash-potion-center refusal.

## Slice 2026-09-27l — result.components (suspicious_stew_effects, comp 53)
The 26.2 data pack's only `result.components` payloads live on the seventeen suspicious_stew_from_<flower> recipes: `{suspicious_stew_effects: [{id, duration}]}`. SuspiciousStewEffects' STREAM_CODEC (javap-verified) is a VarInt entry count then (MobEffect holder id, duration VarInt) pairs.
mc-go: `ItemStack.StewEffects` ("name:dur,..." in list order), comp-53 emit+read, `!`/`=` give support, `data.ItemStack.StewEffects` threaded through `recipeStack`. The generator now reads `result.components` (it previously dropped it entirely — the regenerated rows carry e.g. `saturation:7` for dandelion, `wither:140` for wither_rose).
`TestCraftRecipeParity` gains two differential cases (dandelion→saturation:7, wither_rose→wither:140) — caught a real first-run bug: the emit wrote the component but `writeSlotPatch`'s patch count forgot to include it, so the slot shipped bare.

## Slice 2026-09-27m — pot_decorations (crafting_decorated_pot assemble, comp 74)
Measured on the 26.2 jar (DecoratedPotRecipe + PotDecorations bytecode): matches wants the full 3x3's four edge middles filled — back=(1,0), left=(0,1), right=(2,1), front=(1,2), each its own ingredient (all four #decorated_pot_ingredients in the bundled recipe). assemble builds `new PotDecorations(back.getItem(), left.getItem(), right.getItem(), front.getItem())` and applies it to the result template. Wire: `ByteBufCodecs.registry(ITEM).apply(list(4))` — VarInt count then four raw item registry ids in back,left,right,front order.
mc-go: `matchDecoratedPot` now sets `ItemStack.PotDeco` ("back,left,right,front" names); comp-74 emit+read, `!`/`=` give support (`pot_decorations=[item,item,item,item]`).
`TestCraftRecipeParity` gains two differential cases vs BOTH servers: one sherd + three bricks, and four distinct sherds.

## Slice 2026-09-27n — charged-creeper cloud guard fix
The skull-drop implementation itself was already at parity (committed in slice 2026-09-23j: `Creeper.killedEntity` rider — powered, first victim per creeper via `droppedSkulls`, victim-typed head, `shouldDropLoot` gate). `TestChargedCreeperParity` surfaced one real gap in `spawnLingeringCloud`: mc-go spawned an AreaEffectCloud even for a clean creeper — vanilla's `LivingEntity.spawnLingeringCloud` early-returns when `getActiveEffects()` is empty. Added the `len(e.Effects)==0` guard; all 5 legs now green vs BOTH servers (skull, one-shot latch, unpowered no-drop, cloud yes/no, cloud dose).

## Slice 2026-09-27o — movement + riding statistics parity (checkMovementStatistics, checkRidingStatistics, jump trigger, moved-wrongly rejection)
Measured on the 26.2 jar (`ServerPlayer.checkMovementStatistics`/`checkRidingStatistics`, `handleMovePlayer`, `setOnGroundWithMovement` bytecode): the move-stat chain is isPassenger→return; isSwimming→swim_one_cm(3D); isEyeInFluid(WATER)→walk_under_water_one_cm(3D); isInWater→walk_on_water_one_cm(XZ); onClimbable&&dy>0→climb_one_cm(dy); onGround→sprint/crouch/walk_one_cm(XZ); isFallFlying→aviate_one_cm(3D); else cmXZ>25→fly_one_cm (no fall_one_cm in 26.2). The jump stat is NOT a move-stat branch — `handleMovePlayer` calls `jumpFromGround` (→ awardStat JUMP) when the server-side onGround flag was up, the packet says airborne, and dy>0.
`checkRidingStatistics` runs per rideTick with the seat-follow delta (rider pos pinned to the seat): AbstractMinecart→14, AbstractBoat→15, Pig→16, HappyGhast→17, AbstractHorse→18, Strider→21, AbstractNautilus→22, all 3D distance.
mc-go fixes, all measured:
- `awardMoveStats` rewritten to that exact branch order/distances (was a looser approximation).
- `rideMoveStats`+`vehicleMoveStat` on the rideTickStep seat-follow — the per-tick rider delta banks the vehicle bucket.
- `/ride mount|dismount` now drives `sess.riding` (was Passengers-only — command mounts never seat-followed, couldn't shift-dismount, and banked no stat) and seats the rider at mount like `positionRider` — `interact`-mount too — so the mount hop isn't counted as ridden distance.
- `teleportPlayerRot` preserves the stored onGround flag — vanilla's `setOnGroundWithMovement` is the only path that updates it, so a ground→air rise right after /tp still reads as a jump. Added that `jump` trigger to `handleMove`.
- A passenger's position packets are ignored for position (vanilla snaps to current pos, keeps rotation+grounded) — was processed as a normal move.
- NEW: the "moved wrongly" gate — a position packet whose target AABB (0.6×1.8) enters a solid cell it didn't already overlap is rejected (`sync_pos` back, no stats/fall/broadcast). mc-go had no collision check at all — clients could phase into blocks. `playerBoxCollides` uses `NonSolid` + a `moveNoCollision` empty-shape set (fluids, portals, signs/banners/buttons/cobweb/powder_snow…).
`TestMoveStatParity` runs the same water/move/ride script on both servers and diffs the Award Statistics table: walk 50, walk_on_water XZ, walk_under_water 3D (splits XZ/3D), swim sprint leg, airborne fly (>25cm), jump 1, minecart ride (value-exempt). Green vs BOTH.

## Slice 2026-09-27p — finishUsingItem parity (food table, listeners, consume effects, cooldowns)
Measured on the 26.2 jar (`ItemStack.finishUsingItem`, `Consumable.canConsume`, `Item$Properties.food`/`usingConvertsTo`/`useCooldown`, `Consumables`, `SuspiciousStewEffects$Entry` bytecode): consume order is ConsumableListeners (FoodProperties→burp+food, PotionContents, SuspiciousStewEffects, OminousBottleAmplifier) → onConsumeEffects list (apply_status/remove/clear_all/teleport) → `stack.consume(1)` → use_remainder (empty slot→replace, else inv-add) → use_cooldown packet+gate. `canConsume` gates only on the FOOD component: consumable-only items (milk, potion, ominous_bottle) skip the hunger check entirely — hence the `foodless` flag. `Item.Properties.food()` auto-adds Consumables.DEFAULT_FOOD. SuspiciousStewEffects.Entry has `duration` as lenientOptionalFieldOf(160).
mc-go fixes, all measured:
- Generated `foodtable_gen.go` + `tools/genfoods`: 1205 items / 46 potions — nutrition, satMod (×2 saturation), eatTicks, drink, always, foodless, cooldown, converts, effects (prob), remove, clear, teleport.
- `finishConsume` runs the measured order: stat, burp sound (1310/src7/0.5/0.9-1.0), potion/stew/ominous listeners, clear→remove→effects(prob roll)→teleport chorus, consume(1), remainder replace-or-add, cooldown packet (clientbound 0x16 {Identifier group, VarInt ticks}) + `cooldowns` group gate (silent FAIL while hot — ender_pearl 20t, wind_charge 10t, chorus 20t).
- `protocol.Buffer.WriteString` wire bug: it embeds bytes.Buffer so WriteString emitted raw UTF-8 with no VarInt length — cooldown packets went out malformed. Now `b.String`.
- genfoods missed `ClearAllStatusEffectsConsumeEffect.INSTANCE` (getstatic not `new`) — milk now `clear: true`.
- suspicious_stew_effects SNBT: `duration` now optional, defaulting to 160 (was required — a give vanilla accepts was rejected).
- Removed dead applyPlayerSave: `/data` mutations on players are already rejected with `commands.data.entity.invalid`, same as vanilla.
`TestConsumeParity` runs 14 legs on both servers: starved-via-hunger-effect food deltas (foodLevel min(20, +nutrition)), always-edible effects (gapple/egapple listeners), milk clear_all + bucket, honey remove[poison]+bottle, potion contents, stew component effects + bowl, ominous amplifier, chorus teleport+cd+sound, pearl/wind cooldown packets + spawn. Every leg identical on both targets — `vanilla` and `mc-go` rows match word-for-word.
`TestStewGiveSyntax` pins the four suspicious_stew_effects SNBT forms (prefixed/bare id, with/without duration, bare component key) — all accepted on both.

## Slice 2026-09-27q — scoreboard kill/death criteria + kill-credit window
Measured on the 26.2 jar (`ServerPlayer.die` order, `Entity.awardKillScore`, `LivingEntity.lastHurtByPlayer`/`getKillCredit`, `damage` command): a death bumps `deathCount` on the victim; the kill-credit (last player to hurt them inside the 100-tick `lastHurtByPlayer` window — NOT the damage source that landed the final blow) gets `player_kills`/`mob_kills` stat awards, which the stat feed mirrors onto `playerKillCount`/`totalKillCount` objectives; team criteria `teamkill.<victim's team color>` bump on the killer and `killedByTeam.<killer's color>` on the victim's score holder. `kill`/`damage` with no player in the history credit nobody. Self-kills (`victim==killer`) early-return before any score or `player_killed_entity` trigger.
mc-go fixes, all measured:
- `Session.lastHurtPlayerBy`/`lastHurtPlayerTicks` — the player-only 100-tick credit ref (vanilla's `lastHurtByPlayer`), set in `damageSession` only when the hurt's cause resolves to a session, decremented in `playerTick`, cleared on respawn.
- `mob.go` credits through `Entity.LastHurtByPlayerUUID` (mob-side same ref) instead of awarding `mob_kills` at the kill site — a credit with no matching damage source (hurt→generic-kill) now still pays out, and the hurter≠finisher double-award is gone.
- `awardKillScore` owns the stat awards (`player_kills`/`mob_kills`) so `feedStatCriteria` mirrors the criteria — direct `bumpCriteria` on stat-backed objectives double-counted (zdc=2 per death).
- `/team join @e[<mob>]` resolves through `selectDataEntities` + `selHolder` (mob UUID names — vanilla `getScoreboardName`), so `teamColorOf` finds mob holders for `teamkill.`/`killedByTeam.`. Bare names still store literally.
`TestKillScoreParity` runs 27 legs on both servers (kill/death criteria on player-kills, mob-kills, env-kill credit, `kill` command non-credit, the 100-tick expiry, teamkill/killedByTeam colors, self-kill no-ops). All rows identical word-for-word.

## Slice 2026-09-27r — elytra glide parity (START_FALL_FLYING, canGlide, glide wear, aviate_one_cm)
Measured on the 26.2 jar (`ServerGamePacketListenerImpl.handlePlayerCommand` action 6, `LivingEntity.tryToStartFallFlying`/`canGlide`/`updateFallFlying`, `LivingEntity.tick` fallFlyTicks++ / =0, `ItemStack.hurtAndBreak`, `core.component.predicates.DamagePredicate` bytecode): START_FALL_FLYING calls tryToStartFallFlying — shared flag 7 (index-0 0x80) sets only when `!isFallFlying && canGlide && !isInWater`, and every other state calls stopFallFlying, so the packet toggles the glide off too. There is NO gamemode gate in the handler — creative deploys the flag. canGlide = !onGround && !isPassenger && !hasEffect(LEVITATION) && ∃ slot with `canGlideUsing` (glider component + equippable slot matches + `!nextDamageWillBreak` — Damage >= maxDamage-1 blocks, so a 431/432 elytra is dead). updateFallFlying runs each tick the flag is set: clears it when canGlide lapses, and `i = fallFlyTicks+1` — i%10 fires ELYTRA_GLIDE, i%20 wears a random glide-capable slot (i.e. on the 19th, 39th glide tick; the counter zeroes while not gliding in base tick). While flag-set, moved distance banks aviate_one_cm (3D) before the fly_one_cm XZ>25 branch.
mc-go fixes, all measured:
- `Session.fallFlying`/`fallFlyTicks` + `elytra.go`: canGlide (chest elytra, `Damage < damageableItems["elytra"]-1`), playerInWater (feet cell), tryStartFallFlying (set-or-stop toggle), fallFlyingTick (canGlide-lapse clear, `(fallFlyTicks+1)%20==0` wearSlot, zero while off).
- `broadcastSharedFlags` emits the 0x80 bit; respawn clears flag+ticks (broadcasting flag-off); `persist` round-trips `FallFlying` through the player .dat.
- `awardMoveStats` gains the `sess.fallFlying` → aviate_one_cm(3D) branch between onGround and fly.
- `execute if items` item predicates learned the two forms the vanilla grammar actually parses: exact component `damage=N` and the `DamagePredicate` sub-predicate `{damage:{min:N}|{max:N}|N}` / `{durability:...}` MinMaxBounds (its fields are `durability`/`damage` — `{value:N}` parses vacuously on vanilla, tolerant unknown keys).
- mobeffect_test.go gains its missing `//go:build blackbox` tag (referenced tag-gated joinPlayEid, broke untagged vet).
Not pinned (noted gap): ELYTRA_GLIDE game-event emission every (field+1)%10 — mc-go has no sculk/vibration gameEvent emitter yet; fly_into_wall glide collision and firework boost while gliding are still open.
`TestElytraParity` runs 15 legs on both servers via a second session's entity-metadata stream ({0,0,flags,0xFF} tails): no-item/grounded/levitation/water/riding/broken gates, deploy+toggle+land-clear flag broadcast, aviate vs fly bucket, glide wear ({damage:{min:1}} + {damage:0} pristine), damage=431 threshold, creative deploys. All legs identical word-for-word.

## Slice 2026-09-27s — firework_rocket boost + explosion parity
Measured on the 26.2 jar (`FireworkRocketEntity`/`FireworkRocketItem` bytecode + live wire decode): rocket entity metadata = idx8 ITEM_STACK (always sent), idx9 OPTIONAL_UNSIGNED_INT attached-eid+1 (elided when empty), idx10 BOOLEAN shot_at_angle (elided when false); serializer ids are register-order — 19=OPT_UINT, 8=BOOLEAN (not the old 1.9 table's 20/10). `FireworkRocketItem.use` spawns attached-at-player + ITEM_USED stat + consume(1); `useOn` gates gliding first (returns PASS → `use()` fallback — a use-on-while-gliding still boosts). Explosion (Life>fuse or Life>maxLife): entity_event 17 + 5+2*stars self-damage when attached + area damage 5/sqrt((5-d)/5) inside 25 dist² + remove. Creative `useOn` still consumes (consume always runs); only `use()` skips the consume when creative... wait, measured: vanilla creative useOn free-spawn CONSUMES too (consume is unconditional in useOn).
mc-go gains `internal/server/fireworkrocket.go`: encodeRocketMetadata (conditional idx9/idx10), useFireworkBoost (leash drop + LEAD_BREAK + attached spawn + non-creative consumeHandSlot + ITEM_USED), spawnFireworkRocket, fireworkTick (launch sound 632 at RocketLife 0, ride-owner teleport follow, free ×1.15/+0.04 accel, explode at life>fuse), rocketExplode (event17 + 5+2n attached + sqrt area damage + remove), applyRocketSummonNBT (Life/LifeTime/FireworksItem/ShotAtAngle). use_item_on SB reader updated: wire is {hand, pos, face, cursor xyz, inside, missed(worldBorderHit), seq} — the `missed` bool was missing → vanilla kicks with DecoderException.
`TestFireworkBoostParity` runs 17 legs on both servers: grounded-use quiet, glide boost (attached spawn @ player pos + launch sound + consume + ITEM_USED stat), useOn→boost fallback while gliding, starred-boost explode (event17 + hurt-bucketed self-damage — vanilla's hand-offset position drifts the exact falloff), creative keeps-stack / free-spawn consume, free spawn x8.50 + angle flag, summon {LifeTime:1} starless event17 + no damage (P healed via instant_health first — regen drift would flake the float), starred area damage vs pig, {ShotAtAngle:1b} → idx10=1. All legs identical word-for-word.
Not pinned (noted gap): getHandHoldingItemAngle hand-offset for the attached spawn pos (drifts explode falloff — hurt/safe bucket covers it), LOS for area damage (mc-go has no block-clip), onHit explode-on-impact.

## Slice 2026-09-27t — fly_into_wall glide-collision parity
Measured on the 26.2 jar (`LivingEntity.travelFallFlying`/`handleFallFlyingCollisions`/`updateFallFlyingMovement`, `Entity.move`/`restituteMovementAfterCollisions` bytecode + live `/data get` Motion/Pos probes): each glide tick recomputes deltaMovement from the look angle — look = (-sin yaw·cos pitch, -sin pitch, cos yaw·cos pitch); dy += g·(-1+0.75·cos²pitch); sink converts to forward thrust along the look; nose-up pitch<0 adds a dive-pitch pull (h·(-sin p)·0.04, dy ×3.2); the horizontal ease term rotates toward the look; drag ×(0.99,0.98,0.99). Entity.move does NOT store the clamped delta — the entity advances by the collided vec and `restituteMovementAfterCollisions` multiplies collided axes by -bounciness (0 for players → the component dies). handleFallFlyingCollisions then reads post-move horizontalDistance: `f = (oldH-newH)·10-3`; `horizontalCollision && f>0` → getFallDamageSound((int)f) (≤4 small, else big) + fly_into_wall hurt(f). Hovering pressed to a wall never hits — the delta dies every collide tick and can't rebuild (measured: Motion[0] pins at 0, zero damage events); hits only arrive with speed, i.e. the box reaches the wall while oldH>0.3 — so the test leg advances claims into the wall like a real client (build ~1s in open air, ~4-block approach).
mc-go gains the server-side sim in `elytra.go`/`session.go`: `sess.glideDelta` is the unclamped stored delta (zeroes per collided axis), `sess.glideSim` a drift position advanced by the collided vec each tick and resnapped on every accepted move packet (vanilla's entity drifts between claims — packets snap pos, never delta). `sweepGlideBox`/`clampBoxAxis` reproduce `collideWithShapes` order (Y on the raw box, X on box.move(0,cy,0), Z on box.move(cx,cy,0)) with boundary-ahead-only clamping (a shape binds only when its face lies ahead of the box face — existing overlap tolerated). `updateFallFlyingDelta` mirrors the measured formula; `effectiveGlideGravity` = 0.08 (min 0.01 under slow_falling). `clientIsFloating` exempts gliding (no float-kick while flag-7 set).
`TestElytraParity` adds 2 legs: wall approach → damage_event hit on both; the same approach in open air → safe on both. All 18 legs now identical word-for-word.
Not pinned (noted gap): block speed-factor multiply (honey/soul_sand slow the stored delta — vanilla's move() tail), getBlockSpeedFactor for blocks the box is inside; y-axis bounce restitution (slime et al.); ELYTRA_GLIDE game event/sculk emitter still open.

## Slice 2026-09-27u — firework rocket onHit + boost mechanics parity
Measured on the 26.2 jar (`FireworkRocketEntity.tick`/`onHitEntity`/`onHitBlock`, `ProjectileUtil.getHitResultOnMoveVector`/`computeMargin`, `Projectile.onHit`/`canHitEntity`/`isOutsideOwnerCollisionRange`, `Entity.getHandHoldingItemAngle`, `dealExplosionDamage` bytecode): both the attached and free branches run the move-vector hit scan each tick — block clip first (caps the segment), then nearest entity box inflated by `computeMargin = max(0, min(0.3, (tickCount-2)/20))`; `onHitEntity` explodes UNCONDITIONALLY (starless rockets pop on entities too); `onHitBlock` detonates only when `hasExplosion` (stars>0) — a starless rocket keeps its unclamped delta pushing into the wall till fuse. The attached follow: while `attached.isFallFlying`, the owner's delta gains `look·0.1 + (look·1.5−d)·0.5` (=0.5d+0.85look → converges ~1.7·look, the real boost) and the rocket rides at `getHandHoldingItemAngle` = `view(0°, yaw±80°)·0.5` (+80 main arm, −80 when only the offhand holds a rocket); the rocket's delta copies the owner's. Free flight: `!shotAtAngle || horizontalCollision` → `delta.multiply(f,1,f).add(0,0.04,0)` with f = hColl?1.0:1.15; move clamps position but the stored delta is restored UNCLAMPED. `dealExplosionDamage`'s 5-block area sweep runs a two-ray LOS gate — rocket→victim feet and rocket→victim mid; either ray reaching counts as visible. `canHitEntity` exempts the owner until the rocket's `bb.expandTowards(delta).inflate(1)` stops overlapping the owner's box (`leftOwner`), so a riding rocket can never hit its rider.
mc-go gains all of it in `fireworkrocket.go`: `fireworkTick` rewired to vanilla order (attached boost+offset+follow → hit scan → explode-on-hit → fuse), `rocketHitScan` (new `clipSolidSegment` block clip → per-candidate `segmentBoxT` nearest-win), `rocketOwnerLeftOwner`, `rocketHandOffset` (main-arm +80 default, −80 when only offhand holds one), glide boost into `sess.glideDelta`, `rocketHColl` box-sweep latch (new generic `sweepEntityBox`/`clampBoxAxis` in elytra.go — glide wrapper unchanged), `rocketExplode` LOS gate (pose-height mid: 0.6 glide / 1.8 standing / mobBounds). Target blocks fire `targetSignal`/`fireTargetHit` on rocket impacts (vanilla's `onProjectileHit` hook).
`TestFireworkRocketHitParity` runs 7 legs on both servers: starred shot-at-angle rocket into a pillager → pop+hurt, starred into a wall face → pop on impact, starless into the same wall → quiet until fuse, starred pop behind a two-thick ceiling → LOS safe, same pop in open air → hurt, attached boost → rocket Pos ≈ rider+view(0,yaw+80)·0.5 via /data get, boost delta → Motion[0] > 1.0. All legs identical word-for-word.
Not pinned (noted gap): `applyEffectsFromBlocks` on the rocket (fire/lava inside), `updateRotation` yaw/pitch from delta (cosmetic), onHitBlock's `entityInside` (powder snow/lava), main_arm from client_information (RIGHT assumed).
Also fixed: `TestFireworkBoostParity`'s `area damage` leg compared exact health floats while the launch jitter is unseeded RNG — bucketed to hurt/safe.

## Slice 2026-09-27v — sculk sensor / VibrationSystem parity (gameEvent dispatch, wool occlusion, calibrated gate, amethyst resonance, container events)
Measured on the 26.2 jar (`VibrationSystem$Listener/Data/Ticker/Selector/User`, `EuclideanGameEventListenerRegistry`, `SculkSensorBlock`/`CalibratedSculkSensorBlock`, `SculkSensorBlockEntity$VibrationUser`, `GameEvent`, `ContainerOpenersCounter` + a `ShulkerBoxBlockEntity` start/stopOpen sweep, `PrimedTnt` NBT keys, live `/data get block` + `/tick sprint` probes): the dispatcher notifies every listener block entity in the ±radius box whose floored-BlockPos distSqr is ≤ listenerRadius² INCLUSIVE (8 plain, 16 calibrated); the selector keeps one candidate per tick (closer, or equal-closer louder); travel = floor(raw source→listener float distance); on arrival `onReceiveVibration` latches `getGameEventFrequency(holder)` (resonate_N → N via the map's tail loop), power = `getRedstoneStrengthForDistance(distanceBetweenInBlocks(BlockPos.containing(srcPos), listenerPos), radius)` = 15 − floor(blockDist·15/radius) clamped ≥1; `getActiveTicks` is 30 plain / **10 calibrated**, deactivate's cooldown 10. A sneaking source suppresses the `ignore_vibrations_sneaking` events (and banks avoid_vibration); a dampens_vibrations affected state silences the event; the six-ray wool-occlusion march between the floored endpoints silences listeners behind occludes_vibration_signals cells; a sensor never hears its own block_place/block_destroy. CalibratedSculkSensorBlockEntity's user gates on `getBackSignal` — signal arriving on the side opposite FACING must be 0 or exactly the event frequency. `tryResonateVibration` runs on every activation: for each of the 6 neighbours in `vibration_resonators` (amethyst_block) it emits `resonate_<freq>` AT THE RESONATOR'S POS (freq survives the hop for gating/lastFreq/power) plus `block.amethyst_block.resonate` at `RESONANCE_PITCH_BEND[freq]` = getPitchFromNote(notes[16]={0,0,1,2,4,6,7,9,10,12,14,15,18,21,22,24}); activation also posts `sculk_sensor_tendrils_clicking` (freq 0 — inaudible). Container open/close emitters in 26.2: ONLY chest/barrel/ender_chest (ContainerOpenersCounter) + shulker_box (direct start/stopOpen) — furnace/hopper/dispenser/brewing/lectern emit nothing.
mc-go gains `internal/server/sculk.go`: the frequency table + resonate_N init loop, `emitGameEvent` (±16 box scan, floored-distSqr radius gate, busy/dampen/self/phase/back-signal gates, wool occlusion march, single-slot selector), the traveling-vibration queue + per-sensor `{sel, cur, travel, lastFreq}` data, `sculkTick` (arrival → `sculkReceive` → `sculkActivate` → `tryResonateVibration`), the phase machine (ACTIVE→30/10t→COOLDOWN power0→10t→INACTIVE), NBT `last_vibration_frequency` for /data get, the vibration particle packet, and `sculk_sensor_phase` block-tick wiring. Emit sites wired across the codebase: entity_damage/entity_die (damage/mob), explode (explodePowerFire), container_open/close (chest/barrel/ender/shulker only — gated helpers, `openWindow.closeEmitted` dedupe), elytra_glide every (ticks+1)%10 (closes the noted gap), block_destroy/block_place/block_change/fluid, prime_fuse, step/swim/flap/projectile_*, equip/unequip, shear, eat/drink, instrument, lightning, teleport, entity_place/mount/dismount/interact/action. `/summon minecraft:tnt` now spawns a PrimedTnt (default fuse 80, `{fuse:n}`/`{explosion_power:n}` read, BlastPower override in tntExplode) — it was silently rejected by the living-entity health gate. `isOp` treats an empty ops list as everyone-op (mc-go has no permission levels — ops echoes must reach players for /tick sprint reports).
`TestSculkSensorParity` runs 29 probes on both servers: power falloff 14/8/2 by floored dist, inactive restore + lastFreq, wool-wall occlusion vs open control, calibrated radius-16 cross-arena hear, powered-lever back gate (freq7 rejected, freq15 kill accepted power14), free-cal power 11 + busy-rejection of the kill, the 10-tick calibrated active window (phase already COOLDOWN/INACTIVE at kill+8 — the bug that surfaced getActiveTicks' override), amethyst resonance hops (sensor→amethyst→sensor at distSqr 49/64 with power 2/1 and freq7 latching — unreachable directly), container_open 10/container_close 9 on a chest, summoned-TNT explode freq15 power2 + survival. All rows identical word-for-word.
Not pinned (noted gap): `sculk_sensor_tendrils_clicking` emitted-but-inaudible is modelled as freq-0 skip (equivalent); sensor NBT round-trip (listener queue persistence), `VibrationInfo` selector's projectile-owner distance credit, vibration_resonators beyond amethyst_block (tag is the single-entry one today).

## Slice 2026-09-27w — item/orb collide+restitution+speed-factor parity (honey/soul slow, slime bounce, moveTowardsClosestSpace)
Measured on the 26.2 jar (`Entity.move`/`getBlockSpeedFactor`/`getOnPos`/`restituteMovementAfterCollisions`, `ItemEntity.tick`, `ExperienceOrb.tick`, `Blocks` friction/speedFactor/restitution tables, plus live `/data get`+`/tick sprint` differential probes): `Entity.move` is sweep → position advance → `setOnGroundWithMovement` (onGround = any vertical bound incl. ceilings) → `restituteMovementAfterCollisions` → `delta.x/z *= getBlockSpeedFactor`. The tick tail then multiplies all three axes by `airDrag(0.98)` — and by the supporting block's friction when grounded (ice/packed/frosted 0.98, blue_ice 0.989, slime 0.8, default 0.6) — read under `floor(y-0.500001)`. getBlockSpeedFactor reads the feet cell; water/bubble return early; when that cell's factor is 1 vanilla falls back to the supporting block — empirically `floor(y)-1`, one full cell below the feet cell (a sim depth sweep: depth 1.0 reproduces vanilla's 13.922-block honey/soul slide exactly where the literal `floor(y-0.500001)` reads 14.32 — flagged in code). Speed factors: soul_sand/honey_block = 0.4 only. Restitution: horizontal bounds reflect `delta = -d*bounce` with bounce = entityBounciness (0 for items/orbs); a below-collision is gated on `-dy >= gravity(0.04/0.03)` and `below !in SUPPRESSES_BOUNCE` (honey_block is the tag's only member), else `bounce = blockRestitution` (slime_block = 1.0 only) × 0.8 for non-living; the y rewrite `(ratio*gravity - dy) * lerp(ratio,1,airDrag) * bounce` runs whenever vColl — so bounce 0 flattens delta.y to 0 and the post-move rest-bounce gates (item `onGround && delta.y<0 → ×-0.5`; orb `onGround && delta.y<-gravity → ×-0.4`) are dead code on non-bounce blocks, exactly like vanilla (no micro-hops on stone; slime's positive restitution skips them identically).
mc-go gains `internal/server/entitymove.go`: `entityMove` (sweepEntityBox → flags → restitution → speedFactor), `restituteMove`, `entitySpeedFactor`/`entityFrictionBelow`/`blockSpeedFactorOf`/`blockFrictionOf`/`blockBounceOf`, `collisionTopOf` (honey 0.9375, soul_sand 0.875, else 1.0), `entityInsideSolid` (ItemEntity.tick's `!noCollision(bb.deflate(1e-7))` gate — shape overlap at the box center, so a box merely resting on a shape top never lands inside it). `clampBoxAxis` learned real collision tops: the downward sweep clamps at each cell's shape top and horizontal sweeps skip cells whose top stays below the box minY (soul-sand/honey rests slide free). `itemTickStep` rewired per type: item gravity 0.04 + every-4th-tick rest skip `(age+id)%4`, orb gravity 0.03 + scanForMerges at `age%20==1` + the latched-player homing pull `(1-d/8)^2*0.1` toward the eye; the moveTowardsClosestSpace branch keeps vanilla's 0.1-0.3 least-distant-face push (N/S/W/E/UP, never down) with 0.75 motion retain; orb rest-bounce now reads the post-move delta like the bytecode. `/summon` reads `Motion:[..]` (throws the item like a toss) and `NoGravity` for items/orbs.
`TestItemPhysParity` pins 15 probes word-for-word on both servers: floor state, slide distances (stone 15.45, honey 13.92, soul 13.92 — the 0.4 factor + 0.6 friction exact), drop apexes and rest heights (slime/honey/stone −59.00/−59.06/−59.00 incl. resting INSIDE the 15/16 honey cell), orb exists/bounces-on-slime/rests, and the bounce gameEvent reaching a sculk sensor. Test-harness lessons pinned in comments: vanilla freezes then unloads entities outside the player's entity-ticking/simulation distance (a tp'd-far player makes a summoned orb freeze mid-air and go unselectable — park ~30 out, which still defeats the 8-block homing); `execute at` with `limit=1` silently fails without `sort=`; tp-onto-orb absorbs it (pickup) — probe with an offset. `playerdat_test.go` gained the missing `//go:build blackbox` tag (was breaking untagged `go vet` on master).

## Slice 2026-09-27x — mob ground-move parity (walk delta chain, slime stepOn, random spawn yaw, tick sprint fixes)
Measured on the 26.2 jar (`LivingEntity.travelInAir`/`handleRelativeFrictionAndCalculateMovement`/`getFrictionInfluencedSpeed`/`getInputVector`, `MoveControl.MOVE_TO`/`setSpeed`, `Mob.setSpeed`/`rotlerp`, `EntityType.create`'s snapTo, `SlimeBlock.stepOn`, `Entity.applyEffectsFromBlocks`/`getOnPosLegacy`, `ServerTickRateManager` bytecode + live `/data get Pos|Motion` + `/tick sprint` probes on six floor materials): a walking mob's per-tick chain is `followThePath → setWantedPosition(node, speedModifier)` every tick → `setSpeed(speedModifier·MOVEMENT_SPEED)` → `setZza` → input = `zza·getFrictionInfluencedSpeed` rotated by the CURRENT yaw (the mob walks where it faces, so a turning mob curves); `applyInput` decays zza ×0.98 while idle residue keeps creeping. On ground the input speed is `speed` when friction ≤0.6 else `speed·0.21600002/f³` (slime 0.8 → 0.42×). Inside move(): `applyEffectsFromBlocks` calls the support block's `stepOn` (cell holding the point 0.2 below the feet) every grounded tick — SlimeBlock's damps horizontal delta ×(0.4+|Δy|·0.2) while |Δy|<0.1 — then the sf multiply, then the travel tail ×(f3·0.91) horizontal/(0.98) vertical. `Mob.rotlerp` bounds each turn at ±step deg/tick (MOVE_TO 90, look-goal turns 30). `EntityType.create` snaps every summon to `wrapDegrees(rand·360)` yaw (yHeadRot=yBodyRot=yRot) — summons face a random direction, then rotlerp curves them toward the path: vanilla's measured backward/curving walk starts on ice are that random yaw plus the ≤90°/tick turn bound, not physics. Steady state: advance = input/(1−T) with T = sf·friction·0.91 (·stepScale on slime) — stone 0.137/t, slime 0.0378/t, soul/honey 0.08/t, ice ramps toward 0.133/t.
mc-go fixes, all measured:
- `mobTravelStep` rewired to the full chain: `Entity.WalkSpeed`/`WalkZZA` fields carry the drive; zza decays ×0.98 idle and re-latches on each drive tick; input = zza·(friction-influenced speed); delta recurrence advance=stored+input, stored=advance·T.
- `entityMove` gains `SlimeBlock.stepOn` — `StateOwner` returns UNPREFIXED names, so the check is `TrimPrefix(name,"minecraft:")=="slime_block"` (the first version silently never matched).
- `mobTurnToward` reproduces rotlerp; every walk site converted: tempt/stroll/panic/flee/chase/breed/follow-parent/walk-to-speed at ±90°, melee/creeper/skeleton/witch approach at ±30°, teleport hops replaced by travelStep.
- Summons snap to `wrapDegrees(rand·360)` yaw (HeadYaw/LookYaw = Yaw) on both spawn paths; `Rotation:[yaw,pitch]` NBT overrides it.
- **tick sprint corrected twice over**: the loop decremented before checking so `tick sprint n` simulated only n ticks (vanilla does n+1 — the arriving tick counts), and `runGameLoop` leaked ~1 wall tick per sprint boundary — `next()` snapshots `run=true` while the sprint runs, the 50ms nap spans the sprint end, and the stale flag fires a GameTick after the freeze was restored. The run gate now re-reads `isFrozen()` so a stale run can't fire.
- `TestMobGroundParity` freezes the clock, sprints 3-tick windows, and reads |ΔPos|+|Motion| per window on stone/slime/ice/soul_sand/honey/blue_ice — pins steady-state advance bands, terminal stored delta, and slime/honey rest heights on BOTH servers; the data-get parser now pulls SNBT-typed numbers (trailing `d`/`f` suffix) instead of the last bare digit run.
Not pinned (noted gap): skeleton strafe is still a direct-position op (needs strafe-input modeling), guardian beam look stays instant, navigation node malus isn't modeled (paths are straight-line), `moveTo`'s missed `nav.speedModifier` surface, FOLLOW_RANGE `RANDOM_SPAWN_BONUS_ID` triangle modifier, `applyEffectsFromBlocks` fire/lava inside-entity effects.

## Slice 2026-09-27y — TrackedDeltaMovement interval gating + Mob.finalizeSpawn attribute rolls + randomTick perf
Measured on the 26.2 jar (`ServerEntity.sendChanges`, `EntityType.trackDeltas`, `ItemEntity.tick`, `Mob.finalizeSpawn`, `Zombie.handleAttributes` bytecode + live packet-count probes): the whole sendChanges body — position packet AND the TrackedDeltaMovement velocity emit — is gated on `entity.tickCount % updateInterval != 0 && !needsSync && !entityData.isDirty` (bytecode 249-281). `trackDeltas()` returns true for every type EXCEPT a hardcoded list (player, llama_spit, wither, bat, item_frame, glow_item_frame, leash_knot, painting, end_crystal, evoker_fangs) — items ARE delta-tracked, but their interval-20 sendChanges means a resting item's per-tick gravity-then-restitution delta noise never emits; measured vanilla 21 set_entity_motion/10s vs mc-go's 196 (the resting porkchop flooded clients at ~20Hz, silently stalling drainChunkWindow-style reads past a 36s conn deadline — the intermittent TestMobPanicParity "write i/o timeout" flake). Mob.finalizeSpawn also rolls: follow_range gets a RANDOM_SPAWN_BONUS triangle modifier (0·0.11485·rand, ADD_MULTIPLIED_BASE) plus the LeftHanded ~5% roll; Zombie.handleAttributes layers spawn_reinforcements (binomial ~N(0.05·rand,0.5)), knockback_resistance random_spawn_bonus, follow_range zombie_random_spawn_bonus (triangle 0·1.5, ADD_MULTIPLIED_BASE), and on leader_election the leader_zombie_bonus (movement_speed×1-3, spawn_reinforcements base += 0.5-0.75).
mc-go: the velocity-drift emit moved INSIDE mobMoveSync (the interval gate) — it now emits at most once per entity's sync interval, matching the measured cadence. `randomTickDimension` draws `speed` uniform positions via direct `GetBlock` per roll (was FillBox'd 4096-block snapshot per tickable section per tick — ~96% of FillBox CPU). `mobFinalizeAttributes` (new, mob.go) applies the follow_range triangle + LeftHanded roll + the zombie-family handleAttributes tail at `mobDefaultEquipReason`; the three follow_range readers now resolve through `mobAttr`.
`TestZZPace` (diagnostic, removed): mc-go item motion emits 196→1 per 10s, matching vanilla's cadence class. `TestMobPanicParity` + `TestFluidParity` — the two tests the flood starved — both pass end-to-end (48.5s / 307s) alongside `TestAttrPackParity` legs covering the new follow_range spread.
Not pinned (noted gap): ItemEntity.tick's intra-tick needsSync (`delta.subtract(preTick).lengthSqr() > 0.01` — a thrown/knocked item's instant motion emit bypassing the interval; mc-go still waits for the interval boundary), LivingEntity fall-flying as an extra velocity-emit bypass flag, horse randomizeAttributes.

## Slice 2026-09-27z — ConversionType.convert carry-list parity (piglin/zombie/skeleton conversions, summon conversion-NBT hooks)
Measured on the 26.2 jar (`ConversionType.convert`/`convertTo`, `Zombie.readAdditionalSaveData`/`startUnderWaterConversion`, `AbstractSkeleton.startFreezeConversion`, `NeutralMob` bytecode + live `/data get` before/after dumps): the converted entity spawns fresh (new UUID, full type Health, zeroed AbsorptionAmount, attributes re-rolled through convertTo's finalizeSpawn — spawn_reinforcements/random_spawn_bonus land on a converted drowned) and convert() copies a measured carry list: Pos/Rotation/Motion, IsBaby, CustomName+CustomNameVisible, Silent, NoAI, NoGravity, LeftHanded, PersistenceRequired, CanPickUpLoot, Fire, Invulnerable, active_effects (continued + the conversion nausea), entity Tags, Zombie.canBreakDoors, Brain{memories:{}}, NeutralMob's anger_end_time, InWaterTime/DrownedConversionTime (zombie family), FallFlying, DeathTime/HurtTime. NOT carried: AbsorptionAmount (6→0), Health (9hp piglin → full-20hp zpiglin), UUID, TimeInOverworld, piglin Inventory. `Zombie.readAdditionalSaveData` reads `InWaterTime` (default 0) and `DrownedConversionTime` (default −1; any other value calls startUnderWaterConversion — once armed the countdown finishes even out of water, which is the deterministic conversion hook: the waterline eye-submersion accrual is inherently flaky since a bobbing zombie's eye clears the surface and resets to −1); `Skeleton.readAdditionalSaveData` reads `StrayConversionTime` → startFreezeConversion (armed after the mob is registered in-snow, else the first dry tick cancels the chain). mc-go gains: the full carry list in `mobConvert` — including `Living:e.Living` (the old copy dropped the entire living NBT block — converted entities emitted bare-minimum SNBT with no attributes/effects/AI), `mobFinalizeAttributes(ne)` BEFORE the carries so the attribute rolls land but carried flags win (matches vanilla's kept LeftHanded/CanPickUpLoot), Effects/Equips map copies, Tags into ExtraNBT; entity fields `CustomNameVisible`/`Invulnerable`/`CanBreakDoors` (+encode index-3 byte); summon/`data merge` reads for `InWaterTime`/`DrownedConversionTime` (zombie family via new `zombieTypes`) and `StrayConversionTime` (skeleton); save emits `Invulnerable`/`CustomNameVisible`/`CanBreakDoors`/`InWaterTime`/`DrownedConversionTime`/`anger_end_time`/`Brain{memories:{}}` for all living types + piglin `CannotHunt`/`Inventory`; `isBabyTypes`+`neutralAngryTypes`+zombieTypes tables; the freeze check now TrimPrefix's `blockNameAt` (StateOwner is prefixed — `bn=="powder_snow"` never matched → freeze conversion was dead code). `TestConvertCarryParity` runs 3 legs on BOTH servers asserting ~20 carry keys on the converted NBT: piglin (NoGravity/Silent/baby/Persistence/CanPickUpLoot/LeftHanded/CustomName+Visible/Tags/speed effect/Absorption6/Health9) → zpiglin keeps all carries, gains nausea + Health20 + Absorption0; zombie{DrownedConversionTime:10} → drowned keeps CanBreakDoors/LeftHanded + spawn_reinforcements attr; skeleton{StrayConversionTime:10} armed post-settle in a snow box → stray keeps CustomName. Also pinned a revert: `/summon` default yaw is [0,0] not random — the random facing on summoned mobs is the wander goal's heading pick (misattributed in 09-27x); TestDataEntityParity's pig Rotation now matches. Not pinned (noted gap): PortalCooldown/forcedAge/Team carries mc-go lacks fields for; the 600-tick waterline accrual path stays environment-timed (vanilla resets −1 whenever the eye clears the surface — mc-go's isEyeInFluid model is steadier); vanilla zombie→drowned `Invulnerable`/`anger` interplay.

## Slice 2026-09-27aa — inside-entity block effects parity (powder-snow freeze cycle, fire/lava ignite+hurt, extinguish, sun-burn snow veto)
Measured on the 26.2 jar (`Entity.applyEffectsFromBlocks`/`checkInsideBlocks`, `StepBasedCollector`/`InsideBlockEffectType` + the per-cell registered λ bodies, `LivingEntity.baseTick`, `Entity.clearFire`/`canFreeze`/`getTicksRequiredToFreeze`/`lavaHurt`, `EntityType` freeze/freeze-hurts-extra tags, `hurtServer`'s `invulnerableTime>10` grace bytecode + live `/data get`+`/tick sprint` differential probes): the inside step collects every overlapping cell's registered effect λs per tick (alive gate) — powder_snow runs runBefore(EXTINGUISH→melt-λ: `isOnFire&&(mobGriefing||player)` → destroyBlock) then FREEZE (setIsInPowderSnow + canFreeze → `TicksFrozen=min(140,frozen+1)`) then EXTINGUISH; fire/soul_fire run CLEAR_FREEZE (`remainingFireTicks=min(0,…)` clamp) → FIRE_IGNITE (remaining<0→+1, then `remaining≥0 && !fireImmune → ignite 160t`) then runAfter hurt `in_fire` 1.0 / 2.0; lava/lava_cauldron run CLEAR_FREEZE → LAVA_IGNITE (`!fireImmune → ignite 300t`) → runAfter lavaHurt 4.0 (early-out on fireImmune); water/bubble/water_cauldron/powder_snow_cauldron run EXTINGUISH. LivingEntity.baseTick thaws `frozen=max(0,frozen−2)` when `!isInPowderSnow || !canFreeze` and lands `freeze` 1.0 every 40th tick while `TicksFrozen≥140` (×5 for freeze_hurts_extra {strider,blaze,magma_cube}); `isInPowderSnow` resets at the head of Entity.baseTick before the inside pass re-arms it, and `isSunBurnTick` vetoes while the flag holds. `isInvulnerableToBase` makes a fireImmune type invulnerable to every is_fire-tagged source (no event at all); `hurtServer` grace: `invulnerableTime>10 → amount≤lastHurt swallows, else excess-only`, full hit resets to 20.
mc-go gains `entityInsideStep` per-cell dispatch: `powderSnowInside` (melt λ → flag → +1 accrual → extinguish), `fireBlockInside` (freeze clear → −1-to-0 climb → ignite 160 → hurt), `lavaInside` (clear → ignite 300 → lavaHurt 4.0), `extinguishInside`. Entity fields `HurtTime`/`TicksFrozen`/`IsInPowderSnow`; the fire pass hoists mobFireImmune (immune → FireTicks=0+flags off) and gates `on_fire` on `!entityInLavaWorld`; freeze lifecycle (thaw −2, %40 freeze tick, ×5 extra) in the living pass; metadata index 7 emits DATA_TICKS_FROZEN; summon/`data merge`/`/data` round-trip TicksFrozen/HurtTime and — after this slice — `Fire` (the save map had a literal `Fire:0s` stub — ignite worked but never persisted). `Manager.Damage` gains the is_fire invulnerability gate (Graced swallow = no event) and sets `HurtTime=10` per landed hit; `sunBurnTick` vetoes inside powder snow.
`TestInsideEffectsParity` pins 8 legs on BOTH servers via distance-scoped `/data get` reads: snow freeze to the 140 cap + freeze damage ticking (pig ends at Health9 HurtTime~6), freeze-immune stray accruing nothing AND not sun-burning inside snow (Health20), fire ignite-160 + in_fire cadence (Health5), soul_fire double (Health2), lava cauldron ignite-300 + one 4.0 lavaHurt inside the grace window (Health6), fire-immune blaze in lava untouched (Health20 Fire0), water cauldron extinguish, burning-mob melt-λ destroying the snow. Harness lessons pinned: `/data get` output is delimiter-stripped SNBT (key glued to value — parse `key+number+suffix`, never `key:value`); `/data get entity` requires `limit=1`; unscoped `limit=1` picks ARBITRARY corpses — scope every pick by `x/y/z/distance=..1.5`.
Not pinned (noted gap): player-side inside effects (freeze overlay, melt on player without mobGriefing gate is wired but player FireTicks aren't modelled); `freeze_hurts_extra` ×5 verified in code but only blaze asserted; water extinguish on a burning entity shows Health9 both sides (one on_fire hit lands before the clear).

## Slice 2026-09-27ab — ItemEntity intra-tick needsSync parity (Δdelta >0.1 emit bypass)
Measured on the 26.2 jar (`ItemEntity.tick`/`ServerEntity.sendChanges`/`Entity.tick` bytecode): tick snapshots `preDelta = getDeltaMovement()` at the head, and the tail flags `needsSync |= delta.subtract(preDelta).lengthSqr() > 0.01` — the net intra-tick motion change exceeding 0.1 bypasses the updateInterval-20 cadence on the NEXT sendChanges (the same flag `updateFluidInteraction` ORs into — mc-go does not model item fluid physics, noted gap). The resting-item signature this produces: gravity accumulates MotionY −0.04/tick, the %4-staggered move() clamps it to 0 through restitution (Δdelta=+0.12, lengthSqr=0.0144>0.01 → needsSync), but the emitted delta {0,0,0} equals lastSentMovement → the motion packet is suppressed by the `distanceToSqr>1e-7` dedup — so resting emits are phase-dependent noise, not a differential signal (measured `map[53:1 101:10]` one run, `map[]` the next on identical setups).
mc-go gains: `itemTickStep` snapshots `preDX/preDY/preDZ` and sets `e.PosDirty` when the intra-tick net exceeds 0.01 lengthSqr; XP orbs set `e.PosDirty` on the inside-solid `moveTowardsClosestSpace` path (ExperienceOrb.tick's only sync bypass).
`TestItemNeedsSyncParity` re-tosses a settled item via `data merge …{Motion,OnGround:0b}` and pins the emit step-set over 20×100ms buckets on BOTH servers — identical signature ({merge-chatter at step 0, then suppression silence}). Harness lessons pinned: vanilla does NOT emit `add_entity` for entities summoned while `tick freeze`d (tracker add queues into the frozen tick loop — summon unfrozen then freeze); `/summon` intermittently produces no entity at all on vanilla — verify via `/data get` + retry (`summonItemRetry`); `newChatSinkTP` owns the conn reader — packet-level tests need a dedicated pump goroutine (`startPacketPump`), never mix foreground `ReadPacket` with a sink; never `SetReadDeadline` mid-read — a deadline between header and body desyncs the stream (channel drain + select timeouts only); `tick sprint` steps produce essentially no tracker packets (sprint ticks don't flush sends — can't be used for tick-resolution emit diffs).
Not pinned (noted gap): `needsSync |= updateFluidInteraction()` — item fluid physics unmodelled; fall-flying velocity-emit bypass; thrown-arc landing emit unobservable against vanilla's phase noise.

## Slice 2026-09-27ac — ServerEntity.sendChanges velocity-branch gate parity (needsSync||trackDelta||fallFlying)
Measured on the 26.2 jar (`ServerEntity.sendChanges`/`TrackedEntity.updatePlayer`/`Entity.push`/`Entity.load`/`Entity.push(Entity)` bytecode): inside the interval||needsSync||isDirty gate the velocity branch emits `ClientboundSetEntityMotionPacket` only when `entity.needsSync || entityType.trackDeltas() || (entity instanceof LivingEntity && isFallFlying())`, then the lastSentMovement dedup `delta.distanceToSqr(lastSent) > 1e-7 || (d>0 && delta.lengthSqr()==0)` decides the actual emit. `EntityType.trackDeltas` is a hardcoded ten-type set: PLAYER, LLAMA_SPIT, WITHER, BAT, ITEM_FRAME, GLOW_ITEM_FRAME, LEASH_KNOT, PAINTING, END_CRYSTAL, EVOKER_FANGS. `needsSync` writers catalogued: `Entity.push(double,double,double)` (adds to deltaMovement + flags), `Entity.load` Motion read (clamped ±10 setDeltaMovement + flags), `ItemEntity.tick` Δdelta>0.1 + `updateFluidInteraction`, and per-type writes (Guardian/Blaze/Shulker/Firework/ShulkerBullet/Projectile/AbstractArrow/deflection/AbstractHurtingProjectile/SulfurCube/cubes/ExperienceOrb/BlockAttachedEntity/PotentSulfurBlockEntity/LivingEntity) — only push+load+item-tick wired so far. `setDeltaMovement` does NOT flag. Packet order in sendChanges: motion emit BEFORE pos/rot emit BEFORE rotate_head (mc-go's order already matched).
mc-go gains: `trackDeltaTypes` table in track.go; `mobMoveSync` motionGate = PosDirty || (Living && FallFlying) || trackDeltaTypes[type]; new `FallFlying` shared-flag field (vacuous — no mob models elytra yet); `applyEntitySave`/entsave keep `PosDirty` set across entity loads so stored Motion re-emits once (Entity.load's needsSync); generic push + mobPushStep flag `PosDirty` on both pushed sides; the emit dedup matches `>1e-7 || (drift>0 && curLen==0)`.
`TestVelocityEmitParity` pushes two pigs (0.25 apart — identical positions never push, `absMax<0.01` gate) plus one lone wandery pig, verifies each exists server-side via position-scoped `/data get` (tracking add_entity is unreliable in BOTH directions on vanilla — spawns allocate eids yet never pair), and asserts ≥1 motion emit on both legs — totals are re-collision/wander chaos, logged not compared. `TestItemNeedsSyncParity` now polls for a genuinely quiet 600ms window (summoned items get a random kick and slide for seconds while emitting on each interval pass) before pinning: resting item emits 0 motions on both legs, and a `data merge …{Motion}` re-toss emits in bucket 0 on both.
Harness lessons pinned: `joinPlayEid`/`joinPlayBlackbox` never send Client Information — vanilla's `TrackedEntity.updatePlayer` pairing eval `dist² <= min(effectiveRange, getPlayerViewDistance·16)²` collapses to ~0 → entities never pair → tracking tests are vacuously green; `sendClientInformation(t, pw)` right after join is required for any vanilla pairing. `Mob.isEffectiveAi()` requires `!isNoAi()` — NoAI mobs skip serverAiStep/travel on BOTH servers, so a tossed NoAI mob produces constant delta and trivially-equal silence; `pushEntities` still runs under NoAI. Identical-position entities never push (absMax<0.01 → no push, no random nudge).
Not pinned (noted gap): per-type needsSync writers (Guardian/Blaze/Shulker/projectiles/cubes/orbs/BlockAttachedEntity/PotentSulfurBlockEntity); AbstractHurtingProjectile's BundlePacket(motion+ProjectilePower); trackDelta-types' live-emit arm (only the set is wired, no differential pin since their drifts are hard to drive deterministically); `isVehicle`/`isPushable` push gates approximated by `len(Passengers)==0`; item landing-arc emit counts (chaotic upstream physics — vanilla arc ~1.6s vs mc-go near-instant settle — real item-physics gap for a future slice).

## Slice 2026-09-27af — /data merge killed non-living entities + item landing-arc pin

Measured (javap + live /data probes): vanilla's `data merge` on an item entity keeps it alive — non-living entities carry no Health, and `LivingEntity.load`'s `Health` read is a `getFloatOr(key, getMaxHealth())` default that only applies to living types.

Bug fixed: `applyEntitySave` fell back to `mobHealth[name]` when `Health` was absent — for unregistered types (item, xp_orb) that resolved to 0 — and `applyDataEntity`'s `Health<=0 → mobDie` then despawned the item. Every `/data merge` on an item was silently lethal (the arc-probe "missing item" mystery). Now: absent Health on a living entity defaults to effective max health; non-living entities keep health untouched and skip the merge-then-die gate entirely.

Physics finding: mc-go's item toss arc was already at parity — merge-toss {Motion:[0.32,0.30,-0.14]} lands the item at (35.48,-60,8.32) on both servers, trajectory matching to ~7 decimals at every probe step (same 0.98 drag, 0.6 grass friction, ×-0.5 bounce, %4 rest-stagger). The earlier "no arc on mc-go" was entirely the merge-kill.

Test changes: TestItemNeedsSyncParity now (a) relaxes the first-emit pin to buckets 0-3 (needsSync → sendChanges is defensible across the %4 rest-stagger + 2-tick cadence), and (b) asserts the resting landing Pos lands in disc x∈[34,37] y≈-60 z∈[7.5,9.5] on both legs — the deterministic physics signature that replaced the chaotic step-correlation compare (removed).

Residual micro-gap (noted, unpinned): resting MotionY equilibrium is -0.08 on vanilla vs -0.04 on mc-go — a sub-centi difference in how many accumulated gravity ticks the resting snapshot retains.

## Slice 2026-09-27ag — AbstractHorse.randomizeAttributes per-type roll-set parity
Measured on the 26.2 jar (`net.minecraft.world.entity.animal.equine.{Horse,AbstractChestedHorse,SkeletonHorse,ZombieHorse,AbstractHorse}` + `animal.camel.Camel` bytecode): `AbstractHorse.finalizeSpawn` calls the polymorphic `randomizeAttributes(random)` on every spawn kind, and each subclass rolls a DIFFERENT subset — Horse all three (generateMaxHealth `15+nextInt(8)+nextInt(9)`, generateJumpStrength `0.4+0.2·3r`, generateSpeed `(0.45+0.3·3r)·0.25`), the chested family (donkey/mule/llama/trader_llama) max_health only, skeleton_horse jump_strength only, zombie_horse jump_strength (`0.5+3r/15`) + movement_speed (`(9+3r)/42.16`) on its own formulas. Camel is NOT an AbstractHorse (its own finalizeSpawn under `animal.camel`) and rolls nothing. A rolled max_health also resets the spawn Health to the rolled value.

Divergence fixed: mc-go's `horseFamilyAttrTouch` lumped all 8 types (camel included) into one uniform triple-roll and left Health at the 25 mobHealth default against a rolled MaxHealth. Now `horseAttrRolls` maps each type to its measured subset, `horseAttrRoll` carries the per-type formulas + health reset, and `mobFinalizeAttributes` (natural/convert spawn path) rolls the same set as `summonAttrTouches` (summon path) — vanilla makes no spawn-kind distinction here.

`TestHorseRollAttrParity` bare-summons all 8 types under `tick freeze` (a frozen clock keeps idle strolls from lazily materializing extra attribute instances — the save pack then holds exactly the finalizeSpawn-touched set) and pins on BOTH legs: rolled attrs present inside their measured bands, no unexpected attributes in the pack (only the rolled set + follow_range's random_spawn_bonus), and Health == rolled max_health for the health rollers.

## Slice 2026-09-27ah — Item/XP-orb fluid physics parity (setFluidMovement + updateFluidInteraction)
Measured on the 26.2 jar (`ItemEntity.tick`, `Entity.tick`, `Entity.updateFluidInteraction`, `EntityFluidInteraction$Tracker.applyCurrentTo`, `ExperienceOrb.tick` bytecode): an entity whose box submerges deeper than 0.1 in water or lava replaces `applyGravity` with the fluid formula — items `setFluidMovement(0.95)` (`{x·0.95, y+(y<0.06?0.0005:0), z·0.95}`, same damp for lava), orbs their own `{x·0.99, min(y+0.0005,0.06), z·0.99}` gated on `isEyeInFluid`. Separately, `Entity.tick` calls `updateFluidInteraction()` every tick for every entity — it recomputes submersion and applies `applyCurrentTo` (water ×0.014, lava ×0.0023333, nether-fast-lava ×0.007): accumulated flow normalized then scaled, with a 0.0045 ramp-up floor when |dx|,|dz|<0.003 and the push is weaker. ItemEntity.tick calls it AGAIN at the tail (`needsSync |=` its result), so items get the current push twice per tick and submerged items emit every sendChanges pass.

mc-go gains: `entityFluidScan` (per-tag submersion depth from `fluidSurfaceHeight` — falling=1, flowing=(9-level)/9 — plus the accumulated downhill flow of flowing cells, `fluidFlowDir` scoring neighbors by level descent or open edge), `applyFluidCurrent` (non-player normalize-branch, ramp floor, addDeltaMovement), `entityFluidTick` (scan + push for both tags). Item branch: Entity.tick-style fluid push at head, depth>0.1 submersion replaces gravity, tail re-runs the push and sets PosDirty on in-fluid. Orb branch: single push at head + eye-in-water formula else gravity 0.03.

Two real bugs surfaced: `entityInsideSolid` used `b != Air && !NonSolid(b)` — water/lava/other shapeless cells counted as SOLID, so an item in water entered `moveTowardsClosestSpace` every tick (xz·0.75 + push; the item then never reached the fluid branch). Now shares `moveNoCollision` with the sweep. And `e.Item.Empty() → continue` skipped `ItemAge++` and the 6000-tick despawn — empty items ghosted forever; vanilla's `isEmpty → discard` now removes them.

`TestItemFluidParity` builds a walled 1×1 water column over a settled item and `tick sprint 300`s: vanilla climbs +3.53 to the -56.47 surface bob, mc-go follows within 0.28 — both asserted inside the [-57.3,-55.2] band with no sideways drift (still water carries no flow; the current push is wired but only exercised by flowing cells — noted residual: `fluidFlowDir` is a downhill-gradient approximation of FlowingFluid.getFlow, falling-column and multi-source flow shapes are not exactly modeled; orb lava bob noise `nextFloat-nextFloat` unmodelled; player inside-effects unmodelled).

## Slice 2026-09-27ai — Conversion carry-list parity (convertCommon live-state fields)
Measured on the 26.2 jar (`Mob.convertTo`, `ConversionType$1.convert`, `ConversionType.convertCommon` bytecode): the convert passes the source's live state to the fresh mob — besides the cosmetic/save keys mc-go already carried, `target.setOnGround(source.onGround)`, `setSharedFlag(FALL_FLYING)`, `hurtTime` (the lastHurtByPlayer memory window), `portalCooldown`, `setAbsorptionAmount`, the AgeableMob `age` triple, `getLeashHolder → setLeashedTo`, and every passenger re-rides (boardingCooldown aside). `finalizeConversion` and `shouldDiscardAfterConversion` wrap the call.

mc-go's `mobConvert` now carries Grounded, FallFlying, HurtTime, PortalCooldown, AbsorptionAmount, Age, LeashHolder, and re-points `Passengers` (each passenger's Riding updated post-Spawn). `AbsorptionAmount` also became a real Entity field (save/load round-trip; `/data merge` was previously dead-write to a fixed `0f`) along with `PortalCooldown`. Java nuance: `setAbsorptionAmount` clamps to the `max_absorption` attribute — base 0 on every mob — so convertCommon's call still lands the fresh mob at 0f; mc-go applies the same attribute clamp rather than a hard 0 (a `max_absorption`-boosted mob would keep it).

Three save-shape diffs the dumps surfaced and fixed: `InWaterTime` now saves `-1` unless `WaterConv` is armed (vanilla's sentinel default; mc-go emitted the raw 0), `NoAI` writes only while set (vanilla's addAdditionalSaveData is conditional — absent reads false), and `FallFlying` writes `int8(1)` when set instead of fixed 0. `TestConvertCarryParity` pins `InWaterTime-1` on the zombified piglin + drowned posts and asserts the NoAI key absent — green on both legs. Open residuals: Team join/leave, ANGRY_AT Brain memory, sleepingPos, forcedAge/forcedAgeTimer, equipment dropChances, boardingCooldown — no mc-go model yet (Teams/Brain/loot-drop-chance unimplemented).

## Slice 2026-09-27aj — Mob water travel + FloatGoal parity
Measured on the 26.2 jar (`LivingEntity.travel`/`travelInFluid`/`travelInWater`, `getFluidFallingAdjustedMovement`, `FloatGoal`, `JumpControl`, `aiStep` jump branch, `getFluidJumpThreshold`, `jumpOutOfFluid` bytecode): a mob whose box touches water leaves the air chain entirely — `moveRelative(0.02·zza)` drive (mobs' water_movement_efficiency is 0), `Entity.move`, delta·0.8, then `dy -= effectiveGravity/16` (≈-0.005 → -0.025 terminal sink, with the near-terminal snap to -0.003). On top of that, FloatGoal (priority 0, gated `isInWater && depth > 0.4 || isInLava`) jumps on an 80%/tick roll: aiStep's jump branch turns it into `+0.04` while shallow (depth ≤ threshold), a `0.3` wall-climb hop when deeper and horizontally colliding with the escape space free, `+0.04` in lava, or a `jump_strength` floor hop with a 10-tick `noJumpDelay` when grounded and dry. `Entity.updateFluidInteraction`'s current push applies to mobs too (`isPushedByFluid` default true).

mc-go's `mobMoveStep` now runs `entityFluidTick` for every mob each tick and skips `mobGrounded` while submerged (the water chain owns the fall). `mobTravelStep` gained the water branch verbatim (0.98-decayed zza → moveRelative 0.02 → entityMove → ·0.8 → -g/16). The wander's obstacle check now treats fluid cells as swimmable and keeps the swim drive pressing when the target is blocked in water — the sustained wall contact is what lets the hop fire, matching vanilla's navigation press. New fields `Jumping`, `NoJumpDelay`, `HColl` (recorded in `entityMove`).

`TestMobFluidSinkParity` drops a pig at the surface of a walled 4-deep pool and reads after 200/320 sprinted ticks: vanilla hovers at the -56.4 surface bob (depth ≈0.4 — the FloatGoal gate boundary), mc-go matches within the band; a bare sink would have bottomed at -60. Residuals: lava travel (moveRelative 0.02, ·0.5 damp, `depth ≤ threshold → +0.04 else wall climb`) not wired; `jumpOutOfFluid`'s isFree is approximated as a cell-overlap box check; canStandOnFluid/isAffectedByFluids type overrides unmodelled; mob drowning damage (air supply) not in this slice.

## Slice 2026-09-27ak — LivingEntity.baseTick air supply / drowning
Measured on the 26.2 jar (`LivingEntity.baseTick` air block, `decreaseAirSupply`, `increaseAirSupply`, `shouldTakeDrowningDamage`, `getMaxAirSupply`, `MobEffectUtil.hasWaterBreathing`/`shouldEffectsRefillAirsupply`, `EntityTypeTags.CAN_BREATHE_UNDER_WATER` tag jsons, `Entity.<init>` bytecode): the eye-level block — not the feet — decides the drain. `isEyeInFluid(WATER)` with a non-bubble-column eye cell on a mob failing `canBreatheUnderwater() || hasWaterBreathing` runs `setAirSupply(decreaseAirSupply(air))` — a flat `air-1` for mobs since the `oxygen_bonus` attribute branch never fires on them — and `shouldTakeDrowningDamage` (`air <= -20`) resets air to 0, broadcasts entity event 67 and hurts `drown, 2.0` — i.e. first damage ~320 ticks in, then every 20. Everywhere else (out-of-water OR a bubble-column eye) `air < maxAir` regenerates by `increaseAirSupply`'s `min(air+4, 300)` — inside the drain cell only while a refilling effect (water_breathing | conduit_power | breath_of_the_nautilus — both guards read the same triple) is on. `canBreatheUnderwater` is the `can_breathe_under_water` entity-type tag (flat list + `#undead` = `#skeletons` + `#zombies` + wither + phantom). Entity constructs `airSupply = getMaxAirSupply() = 300`; the value syncs on metadata index 1 (DATA_AIR_SUPPLY_ID, int) — vanilla's attach burst omits it (300 default) and the drain/regen ticks dirty-resend it.

mc-go gains `Entity.AirSupply` (init 300 in `Manager.Spawn`, save emits the live int16 `Air`, `/data merge` loads it), the mobTickStep air block verbatim beside the freeze block, `mobBreathesWater` (the tag set), `mobEyeInBubbleColumn`, `mobHasAirEffect` (the three-effect predicate), and index-1 emission in `encodeLivingMetadata` gated `AirSupply != 300` so the attach pack is unchanged. `mobEyeInWater` switched from the hardcoded witch 1.95·0.85 to `mobBounds(type)[1]*0.85` (same value for witches).

`TestMobDrownParity` sinks a NoAI pig to the bed of a 5-deep walled pool (a swimming pig never drowns — the 0.4 bob keeps its 0.765-high eye above the surface): ~100 ticks in Air reads ≈200 both legs (band 150-250, |Δ|<40), ~350 in it sits in the (-20,0] reset cycle with health below 10, ~630 in the pig is gone on both. `TestAirSupplyRestoreParity` merges `Air:100s` on land and reads the +4/tick regen — vanilla measured 148 after ~12 ticks.

Preexisting gap surfaced, NOT this slice's: `kill @e` emits the first killed entity's dying-metadata pack BEFORE the /kill reply chat on mc-go (vanilla: chat, then both packs — the pack is queued correctly but the per-mob sendChanges pass flushes entity A's pack inside the same tick that ran the command). Reproduces on bare master — TestDataEntityParity fails deterministically there; left for a scheduling-order slice.

## Slice 2026-09-27al — travelInLava + aiStep jump-chain restructure + FloatGoal registration set
Measured on the 26.2 jar (`LivingEntity.aiStep` jump block, `travelInFluid`, `travelInWater`, `travelInLava`, `jumpOutOfFluid`, `isInShallowFluid`, `getFluidFallingAdjustedMovement`, `FloatGoal` bytecode, plus a `grep -rln FloatGoal` over every entity class for registerGoals):

- aiStep's jump block is a three-arm chain, NOT the shallow/deep split previously modeled: `h = isInLava ? fluidHeight(LAVA) : fluidHeight(WATER)`; `b1 = isInWater && h > 0`; arm 1 `b1 && (!onGround || h > thresh)` → `jumpInLiquid(WATER)` (+0.04); arm 2 `isInLava && (!onGround || !isInShallowFluid(LAVA))` → `jumpInLiquid(LAVA)` (+0.04); arm 3 `(onGround || (b1 && h ≤ thresh)) && noJumpDelay==0` → `jumpFromGround` + delay 10. The 0.3 wall hop does NOT live in aiStep — it is `jumpOutOfFluid(y0)`, the unconditional tail of BOTH travelInWater and travelInLava (arg = pre-move Y; aiStep's earlier `thresh` arm was misattributed). `isFree(dx, dy+0.6-getY()+arg, dz)` translates the box BY the vector, so the absolute probe is `(x+dx, dy+0.6+arg, z+dz)`.
- travelInFluid dispatch: `isInWater → travelInWater else travelInLava` (water priority). Water tail: `delta.multiply(f,0.8,f)` (f=0.8 getWaterSlowDown, 0.9 sprint) + getFluidFallingAdjustedMovement + jumpOutOfFluid(y0); the hColl+onClimbable arm flattens x/z to 0.2 pre-damp (unmodelled — no climbable yet).
- travelInLava: moveRelative(0.02) → move → `isInShallowFluid(LAVA)` (depth ≤ thresh): `multiply(0.5,0.8,0.5)` + fall-adjust; deep: `scale(0.5)`; `g≠0 → delta.y -= g/4` (4× water's g/16); tail jumpOutOfFluid(y0).
- getFluidFallingAdjustedMovement(g, falling, delta): `g≠0 && !sprinting`: `falling && |y-0.005|<0.003 && |y-g/16|<0.003 → y'=-0.003 else y'=y-g/16`.
- FloatGoal registration is per-type (grep over registerGoals): animals, most villagers/monsters (creeper, spider, enderman, witch, illagers, silverfish, endermite, ravager, vex, cubemobs, wandering_trader) have it; zombies, skeletons, piglins, hoglins, zoglins, blazes, guardians, fish, squids do NOT — they sink to the fluid bed (a wither_skeleton sits at -60.0 in 4-deep lava; verified live — reads at ~200t/~320t both -60.000, survives on fire immunity).

mc-go gains `mobHasFloatGoal` (gating e.Jumping), the restructured three-arm aiStep block (with `h` lava-priority probe and the not-jumping `noJumpDelay=0` else-arm), the travelInLava branch in mobTravelStep, `mobJumpOutOfFluid` (box-translate probe) tailing both fluid branches, and the mobMoveStep travel gate widened to `water || lava || grounded`. The swim-drive wall press now applies to lava too.

`TestMobLavaTravelParity` drops a fire-immune wither_skeleton into a walled 4-deep lava column and reads at ~200t/~320t: both servers sit the skeleton's feet at -60.0 (bed band ±0.35), health intact (fire immunity — a pig dies inside three ticks and can't be read). Preexisting gap confirmed out-of-scope: `TestWanderDensityParity` fails on bare master (0 set_entity_motion emits while strolling — the TrackedDeltaMovement live-emit arm on the open gap list).

## Slice 2026-09-27am — Entity.baseTick lavaHurt (lava contact damage)
Measured on the 26.2 jar (`Entity.baseTick`/`lavaHurt` bytecode): every entity whose bounding box touches lava and isn't fire-immune takes `lavaHurt()` every tick — `igniteForSeconds(15)` (the reignite keeps fire ticks pinned ~300 while submerged; the on_fire damage itself is still skipped while in lava) plus `hurtServer(lava, 4.0)`. Invulnerability frames inside `hurt` thin the effective rate to 4 per 20 ticks, so a 10-health pig in lava dies at ~41 ticks (three landed hits) while a wither_skeleton — `fireImmune`, in the `is_fire` tag set — takes nothing.

mc-go's mobTickStep gained the lava contact block after the fire-tick pass: `!mobFireImmune && entityInLavaWorld` → shared-flags on-fire broadcast on the flag flip + `SetFireTicks(300)` + `damageMob(4, lava)` — the invuln thinning is damageMob's existing graced-excess branch, so nothing else is needed for the effective rate. `TestMobLavaTravelParity` gained the pig probe: summoned alongside the skeleton into the same walled 4-deep lava column, it's absent on both servers at the ~200t read (the skeleton's Health stays at the 20-base — fire immunity pinned via `is_fire`).

## Slice 2026-09-27an — Entity.needsSync + aiStep push() latch (TrackedDeltaMovement live emit)
Measured on the 26.2 jar (`ServerEntity.sendChanges`, `Entity.push`, `Entity.needsSync` field writes, `Entity.markHurt` bytecode): the set_entity_motion emit branch inside sendChanges is gated `needsSync || trackDelta || (LivingEntity && isFallFlying)`, and `needsSync` is latched by `push(Vec3)`/`push(d,d,d)` — which `LivingEntity.aiStep` calls with the walk input EVERY tick (push runs and latches even for a zero vector, and even for NoAI mobs since aiStep still runs its physics half) — so every living entity is gate-open constantly and the `drift > 1e-7 || (drift > 0 && |delta| == 0)` check alone decides emission: accel/decel/stop edges, not cruise. `sendChanges`' tail clears `needsSync = false`; `hurtMarked` is a separate force-emit path with its own self-inclusive packet.

mc-go previously emitted ZERO set_entity_motion for a strolling mob: the gate read `PosDirty`, which only flips at spawn/push sites. Now `Entity.NeedsSync` models the field — set every living tick in `mobMoveStep`'s head (aiStep's unconditional push), latched alongside every `PosDirty = true` site (vanilla needsSync also forces the sendChanges pass early — the two flags coincide at every push/snap point), cleared in `mobMoveSync`'s sendChanges tail beside `PosDirty = false`, and the motion gate reads `NeedsSync || trackDelta || fallFlying` with PosDirty dropped (redundant — every PosDirty site pairs NeedsSync).

`TestWanderDensityParity` (preexisting failure on bare master, confirmed by stash) now passes on both legs — vanilla emitted 26 motion packets across the wander window, mc-go 46, inside the drift-band. Residuals: `hurtMarked`'s self-inclusive emit path unmodelled (knockback already ships via broadcastEntityMotion), push() callers beyond aiStep/collision (knockback's own push path) not yet flagged NeedsSync.

## Slice 2026-09-27ao — session-player fluid/inside-block chain (lava, air/drown, fire pass, void)
Measured on the 26.2 jar (`ServerGamePacketListenerImpl.handleMovePlayer`, `Entity.move`/`checkInsideBlocks`/`applyEffectsFromBlocks`, `LivingEntity.aiStep`, `Entity.igniteForTicks`/`lavaHurt`/`lavaIgnite`/`clearFire`, `Entity.baseTick`/`onBelowWorld`, plus live probes: `execute if block` markers, `data get` NBT reads, damage_event packet decode):

- Session-player inside-block effects are driven TWICE, not once: every accepted move packet runs the scan once (handleMovePlayer's unconditional `player.move` — a same-position resend STILL scans the occupied cells: a tp echo into real lava landed `Fire=300` + `Health-4`), AND the entity's own per-tick `move()` re-feeds it (a player standing still in lava takes ~4 lava hits per 30 ticks, paced by invulnerability — the "still" window killed the vanilla player 11.5→0).
- `igniteForTicks(n)` is a plain SET — `if (remainingFireTicks < n) setRemainingFireTicks(n)` — NOT additive: lavaIgnite's `igniteForSeconds(15)` pins `Fire=300` from the -20 spawn baseline in one application; fire blocks `igniteForSeconds(8)` → `Fire=160`. `clearFire` = `min(0, ft)`: water drops 300→0 but leaves the -20 baseline untouched. Spawn baseline is `remainingFireTicks=-20`, persisted verbatim in NBT.
- The vanilla 26.2 fluid sim turns lava+water contact into solid obsidian — filling lava inside a water column's flow range produced obsidian, and the entity then took `in_wall` (type 22) suffocation, not lava. Test columns must sit ≥7 cells from water.
- `onBelowWorld` is Entity.baseTick business (entity tick, freeze-exempt, sprinted) — 4.0/tick below `minY-64` — not wall-clock connection-tick work.
- airSupply/drowning, invuln-grace decay, and the on_fire pass (1.0 every 20 ticks, skipped while `isInLava`) are all entity-tick work for session players too.

mc-go gains `playerFluidTickStep` on the game tick (freeze-exempt like the entity tick): inlined onBelowWorld void damage, the eye-block air drain/refill (300 cap, -20 drowning floor, Air index-1 metadata broadcast), the on_fire pass moved off the wall-clock hunger tick, invuln/lastHurtPlayer decay, and the per-tick inside-block scan (`playerInsideBlocks`: lava ignite 300 + 4.0 lavaHurt, fire/soul_fire ignite 160 + 1/2 hurt, water/powder_snow clearFire). `handlePlayMove` calls the same scan once per accepted position packet (same-position resends included). `entityInsideStep` skips `player` records — the record-level ignite would fight the session-side pass. Persist: `Air` now round-trips (`playerPersist.Air`, `playerSaveMap`, `playerDatCompound`); `Fire` writes `sess.fireTicks` keeping the -20 baseline. `voidTickStep` deleted — the void check is the game-tick step now.

`TestPlayerFluidParity` (new, differential vs both servers): water-column air drain 300→0 with drowning at -20, refill to 300 on exit; lava column far from water — tp echo lands `Fire>0` + the hurt, sprint-25 stationary window hurts more on both legs (healed first, direction asserted not cadence), move-in-lava keeps `Fire=300`, water move clears to ≤0, creative move ignites without hurting. Green vs vanilla and mc-go.

Open gaps surfaced: lava+water→obsidian conversion unmodelled in mc-go's fluid sim; `in_wall` suffocation for session players (entity-AABB `isSuffocating` cells, 1.0/tick grace-paced) unmodelled; lava hurt cadence on vanilla (~4 hits/30t) is tighter than the 20t invuln pacing — the effective invuln for lava-type damage wants a measured sub-gap.

## Slice 2026-09-27ap — LivingEntity.baseTick in_wall suffocation (session players)
Measured on the 26.2 jar (`LivingEntity.baseTick` suffocation arm, `Entity.isInWall` + `lambda$isInWall$0` bytecode): every entity tick, `isInWall()` probes a plane centered on the eye position — `AABB.ofSize(eyePos, width*0.8, 1e-6, width*0.8)` — for cells that are `!isAir && isSuffocating` AND whose collision shape intersects the plane; true → `hurtServer(inWall, 1.0)` (the invuln window paces the visible rate; measured ~4 events/25t). `noPhysics` players (spectator noclip) return false up front — the invuln gate already covers them. `isSuffocating` = the block's opaque full-cube `canOcclude`: stone/dirt/logs/furnaces/barrels suffocate; leaves, glass, ice, blue_ice, slime, honey, slabs, stairs, soul_sand, snow layers do NOT.

mc-go gains `suffocatingBlocks` (generated from the blocks report's `definition.type` — `tools/gensuffoc` whitelists the opaque full-cube types: block/rotated_pillar/sand/grass/mud/infested/ores/drop_experience/glazed_terracotta/furnaces/piston_base/note/jukebox/beehive/tnt/crafter/vault/trial_spawner/... 365 owners) and `playerInWall` (the eye-plane scan, sharing the air pass's eye-height formula) inside `playerFluidTickStep` after the inside-block scan — same game-tick clock, invuln-paced via `damageWith(srcInWall)`.

`TestPlayerFluidParity` gained the bury phase: heal → `fill` stone around the player → sprint 25 → both legs emit in_wall damage events (vanilla 4, mc-go 3) and drop to ~17; `oak_leaves` at eye level hurts nothing on either leg.

## Slice 2026-09-27aq — powder-snow freeze for session players + teleport record fix + inside-effect dedup
Measured on the 26.2 jar (`LivingEntity.baseTick` freeze block, `PowderSnowBlock.entityInside`, `Entity.checkInsideBlocks` applier dedup, `TeleportCommand.performTeleport`→`teleportSetPosition`→`setPosRaw`): TicksFrozen accrues +1/tick while `isInPowderSnow` (cap 140 = getTicksRequiredToFreeze), thaws -2/tick out of snow, fully-frozen living entities take `freeze` 1.0 every tickCount%40 boundary; `data get` emits TicksFrozen only when >0. Vanilla's inside pass groups cells by effect applier — two powder_snow cells in one box still apply ONCE (+1/tick, not +2). `/tp` repositions the server entity itself via setPosRaw — no client echo needed for the record to track.

mc-go had three bugs, all surfaced by the slice: (1) `tpToPos→tpMove→teleportPlayerRot` set the session position but never touched the entity record — a `/tp` left the record at the last move-packet position (vanilla's setPosRaw updates it immediately; `teleportWithin` already did the right thing — now `teleportPlayerRot` runs the same `entities.Update` + `broadcastEntityTeleport`). (2) `entityInsideStep` applied inside-block arms PER CELL — a 2-cell box accrued TicksFrozen at +2/tick; now deduped per owner name per entity per tick, matching the applier grouping. (3) `playerSaveMap` never emitted TicksFrozen — the player dataEntity now reports the record's value when >0.

Session-side freeze damage arms in `playerFluidTickStep` (entity-tick clock, freeze-exempt): `pe.TicksFrozen >= 140 && pe.SyncCount%40 == 0` → `damageWith(1, death.attack.frozen, srcFreeze)`; the record-level arm in `mobTickStep` is gated `ambName != "player"` so it can't drain record health. `TestPlayerFluidParity` gained the powder phase: heal → powder_snow column → tp inside → sprint 170 → both legs emit freeze damage events (vanilla 2, mc-go 4 — SyncCount/tickCount phase noise, cadence not pinned) and drop below 20; `data get TicksFrozen` now reads 140 on both legs. Green vs vanilla and mc-go.

## Slice 2026-09-27ar — tryAddFrost frost modifier (transient vs permanent attr mods)
Measured on the 26.2 jar (`LivingEntity.baseTick` → `removeFrost`/`tryAddFrost`, `AttributeInstance.permanentModifiers`): every entity tick the frost pair drops the `minecraft:powder_snow` MOVEMENT_SPEED modifier unconditionally, then re-adds `-0.05*getPercentFrozen` ADD_VALUE while the entity's TicksFrozen>0 and its on-block (pos y-0.2) is non-air. The modifier is a TRANSIENT add — it rides update_attributes to clients but never serializes: `data get` on a TicksFrozen=140 player shows attributes [oxygen_bonus, entity_interaction_range, waypoint_transmit_range, block_interaction_range, movement_speed] with no modifiers — the attribute map instantiates lazily (a ticked player has exactly those five instances) and the save pack writes permanent modifiers only.

mc-go gains `attrMod.permanent` (vanilla's permanentModifiers vs the transient set): `mobAttrModAdd` stays permanent (spawn bonuses, /attribute writes, applyPackedAttrs loads), new `mobAttrModAddTransient` carries drinking and powder_snow frost; `mobAttrPack` now serializes permanent-only; `playerAttrPack` emits the measured instantiated-five plus any touched attribute, permanent modifiers only. `frostPass` runs the removeFrost/tryAddFrost pair per tick for living records (mobTickStep freeze block) and for the session player inside playerFluidTickStep — where a wire flip emits `update_attributes` id-26 with the current modifier set (one packet per change, matching vanilla's dirty-per-tick cadence while percentFrozen slides).

`TestPlayerFluidParity` powder phase now also scans the sprint window's clientbound stream for a 0x83 update_attributes packet carrying `minecraft:powder_snow` — both legs emit it (the save-side pin is impossible: transient mods never serialize). Green vs vanilla and mc-go.

## Slice 2026-09-27as — hurtMarked: unconditional hurt motion emit

- **Measured (jar)**: `LivingEntity.hurt` calls `Entity.markHurt()`
  on every landed hit outside `damage_type:no_impact` (tag = `drown`
  only) — le.txt:349, gate `!NO_IMPACT && (b7 || dmg>0)`, so even a
  zero-damage thrown impact marks. `ServerEntity.sendChanges`' last
  block clears the flag after emitting
  `ClientboundSetEntityMotionPacket` via
  `sendToTrackingPlayersAndSelf` — self-inclusive, no drift gate, no
  updateInterval cadence. `Player.attack` additionally sends the
  victim-player's motion immediately when its `hurtMarked` is set.
- **Implemented**: `Entity.HurtMarked` (entity.go). `markHurt(e,
  typeName)` skips `drown`, sets via `GetPtr` with snapshot fallback
  (mob.go:2139); called from `damageMob` on both landed paths
  (zero-damage impact + damage) and from `damageWith` for session
  players (damage.go). Emit sites: `mobSyncFlush` tail for entity
  records (mobsync.go — unconditional, syncs SentMotion), and the
  `playerFluidTickStep` freeze-arm block for player records
  (survival.go) — `broadcastEntityMotion` already self-includes the
  victim session via the entityID check.
- **Pinned**: `TestHurtMarkedMotionParity` — NoAI pig damaged with
  `generic` (in `no_knockback`, so delta stays ~0 and the hurtMarked
  arm is the only possible emitter) then `damage @s 1 generic` for
  the self-inclusive player arm. Both legs emitted exactly 1 pig
  motion + 1 self motion.
- **Commit**: server + test.

## Slice 2026-09-27at — boardingCooldown: 60-tick re-ride gate

- **Measured (jar)**: `Entity.removePassenger` sets the departing
  rider's `boardingCooldown = 60` (ent.txt:5685-5691); `Entity.tick`
  decrements it unconditionally each tick (ent.txt:830-838);
  `canRide(vehicle)` requires `!isShiftKeyDown && boardingCooldown
  <= 0` (ent.txt:5525+), and `startRiding` consults canRide unless
  forced. Net effect: a mob dropped off a vehicle can't be
  re-scooped for three seconds.
- **Implemented**: `Entity.BoardingCooldown`, counted down in the
  living-mob tick loop ahead of `mobCheckDespawn` (mob.go). Armed at
  both mob-dismount points: `minecartActivatorHit`'s powered-rail
  eject and `runRideDismount` — which also fixed a latent bug where
  `/ride @e pig dismount` cleared `veh.Passengers` but never cleared
  the mob's own `Riding` field (the record stayed welded to the
  cart). The scoop gate in `minecartPushPickup` now requires
  `o.BoardingCooldown <= 0` (canRide).
- **Pinned**: `TestBoardingCooldownParity` — a minecart lapping a
  plain-rail loop scoops a NoAI pig off the track (one 0x6B mount
  packet), `/ride dismount` drops it, laps inside the 60-tick window
  emit no further mount, and a fresh cart after expiry re-scoops.
  Vanilla needed the rig tuned twice: powered rail does NOT push a
  stationary cart (initial `Motion` required) and lap collisions
  drain the cart + shove the pig off the pad (bigger floor, fresh
  cart for the post-cooldown phase).
- **Commit**: server + test.

## Slice 2026-09-27au — drop_chances: per-slot equipment drop table

- **Measured (jar)**: `Mob.dropCustomDeathLoot` iterates all
  EquipmentSlot.VALUES — slot chance 0 never drops; `isPreserved`
  (chance > 1.0) drops unconditionally and skips the wear roll;
  otherwise the kill must be a player's and `rand < chance`, with
  `EnchantmentHelper.processEquipmentDropChance` adding the killer's
  looting to the chance. A dropped damageable item lands nearly worn:
  `damage = maxD − rand(1 + rand(max(1, maxD−3)))`. `DropChances` is a
  full eight-slot table defaulting 0.085; save emits `drop_chances`
  (all eight keys) only while ≠ DEFAULT. `LivingEntity.dropEquipment`
  is empty — this all lives in dropCustomDeathLoot.
- **Implemented**: `Entity.DropChances` map parsed from the summon
  `drop_chances` compound; `mobDropChance` (override → patrol-leader
  head 2.0 → 0.085) feeding both the full eight-slot save emit and
  the death loop. The death loop replaces the captain-banner-only
  special case with the real per-slot roll: player-kill gate, preserve
  bypass, looting bonus (+0.01/level), near-max damage randomization,
  and slot clear on drop.
- **Pinned**: `TestDropChancesParity` — `1.0f` emit through data get,
  1.0+player-kill drops, 2.0 preserved drops on /kill, 1.0 gated on
  /kill, 0.0 never drops. All four probes non-vacuous on both legs.
- **Commit**: server + test.

## Slice 2026-09-27av — Passengers save + mount back-reference

- **Measured (jar + live)**: `Entity.saveWithoutId`'s tail emits
  `Passengers` only while non-empty — each child via saveAsPassenger
  (its own save + "id"). A passenger's Pos reads `(veh.X, ownY,
  veh.Z)`. `save()` returns false for passengers, and `data get` on a
  mounted mob measures NO `RootVehicle` key — that compound rides on
  the passenger-save path (a session player's own save), not the
  entity one.
- **Implemented**: `entitySaveMap` now emits `Passengers` for a
  vehicle (child saves through entitySaveMap + "id") and writes the
  vehicle-relative Pos for a mob rider. `runRideMount` finally sets
  the mob rider's `Riding` — it appended to `vehEnt.Passengers` but
  never set the back-reference, so every /ride-mounted mob read as
  dismounted (the minecart scoop path was the only place that set
  it).
- **Pinned**: `TestRideNBTParity` — mounted cart shows
  `Passengers:[{id:"minecraft:pig"}]` with the pig's NoAI save
  inside; the pig shows no RootVehicle (vanilla-measured); dismount
  clears both.
- **Commit**: server + test.

## Slice 2026-09-27aw — AgeableMob/Animal age NBT parity

- **Measured (jar + live sweep)**: `AgeableMob.addAdditionalSaveData`
  emits `Age`/`ForcedAge`/`AgeLocked` unconditionally while
  `canBeABaby`; `Animal` declares `InLove` separately — so
  adult-only Animals (frog, camel_husk, parrot, zombie_nautilus)
  emit InLove but NO age keys, and Slime is not AgeableMob at all
  (it carries `Size`). Three distinct emission classes measured
  live: full quad (35 types), InLove-only (4 types), none
  (slime/magma_cube/zombie).
- **Implemented**: Entity gains `ForcedAge`/`AgeLocked`/`InLove`;
  `entitySaveMap` emits the age triple for `ageableTypes` and
  `InLove` for `animalTypes`. `ageableTypes` drops slime/
  magma_cube/sulfur_cube/frog/camel_husk/parrot/zombie_nautilus;
  new `animalTypes` table (Animal leaves only). Summon parses
  ForcedAge/AgeLocked (ageable) + InLove (animal) after Age.
  Pig's templated extras lose the now-generic age quad.
- **Pinned**: `TestAgeableNBTParity` — 43-type sweep, per-key
  `data get` path queries (full-entity SNBT exceeds the chat
  path), all three emission classes match.
- **Commit**: server + test.

## Slice 2026-09-27ax — overworld zombification timer

- **Measured (jar + live)**: `AbstractPiglin.customServerAiStep` and
  `Hoglin`'s equivalent tick `TimeInOverworld` while
  `isConverting()` = `!IsImmuneToZombification && !NoAI &&
  envAttr(PIGLINS_ZOMBIFY,pos)` (default true; nether overrides
  false). Past 300 ticks the mob converts — piglin/piglin_brute →
  zombified_piglin, hoglin → zoglin — through ConversionType's full
  carry list (baby flag, Age/forcedAge, effects, leftHanded,
  equipment, CanPickUpLoot, LeashHolder, Passengers re-ride). Both
  NBT keys emit unconditionally on save.
- **Implemented**: the zombify tick/convert/save path already
  existed; the live sweep surfaced one real gap — mc-go's gate
  missed vanilla's `!isNoAi()` condition, so a NoAI piglin would
  still zombify. Fixed.
- **Pinned**: `TestOverworldZombifyParity` — counter non-zero at
  ~200 ticks, type flips past ~300 on both legs (piglin→
  zombified_piglin, hoglin→zoglin), IsImmuneToZombification and
  NoAI each block conversion with the counter pinned at 0.
- **Commit**: server + test.
## Slice 2026-09-27ay — item-entity lava burn

- **Measured (jar)**: `Entity.lavaHurt` runs fireImmune →
  hurt(lava, 4.0) → GENERIC_BURN at vol 0.4 / pitch 2+rand·0.4
  while `shouldPlayLavaHurtSound` (health>0 || tickCount%10==0)
  and !silent. `ItemEntity.health=5`; hurtServer drains it int-wise
  → `ItemStack.onDestroyed` + discard at ≤0 — a common item burns
  one tick then dies on the second. `ItemEntity.fireImmune` =
  stack `canBeHurtBy(inFire)` — the `damage_resistant:#is_fire`
  component measured on 16 items (netherite tools/armor/ingot/
  scrap/block/spear/horse+nautilus armor, ancient_debris;
  nether_star is explosion-immune only).
- **Implemented**: `lavaInside` gained the item branch —
  ItemHP field (lazy 5), −4/lava-tick, GENERIC_BURN ride each
  hurt tick (srcNeutral), discard at ≤0; `itemFireImmuneNames`
  is the measured 16-item set.
- **Pinned**: `TestItemBurnParity` — stone item gone after 20
  lava ticks on both legs; netherite_ingot + ancient_debris still
  riding at 80.
- **Commit**: server + test.
## Slice 2026-09-27az — death-pack scheduling (corpse linger)

- **Measured (jar + live)**: `LivingEntity.tickDeath` is
  `deathTime++`; at `>=20` vanilla broadcasts entity event 60 and
  `remove(KILLED)`. `/kill` bypasses it entirely (Entity.kill =
  remove — no corpse), and selectors never match a corpse on
  EITHER server (mc-go already skips `Dying` in selection). The
  `DeathTime` save key carries the live counter, not a constant.
- **Implemented**: `data get` now emits `e.deathTicks` (was
  hardcoded 0). Corpse linger, event 60 and the 20-tick removal
  already existed.
- **Pinned**: `TestDeathTimeParity` — raw-packet pin: entity_event
  3 on death, entity_event 60 + remove_entities ~1s (20 ticks)
  later, on both legs.
- **Commit**: server + test.
## Slice 2026-09-27ba — GENERIC_BURN lava-hurt cadence

- **Measured (jar + wire)**: `Entity.lavaHurt` plays GENERIC_BURN
  (vol 0.4, pitch 2+rand·0.4, entity's own getSoundSource) on every
  landed lava tick — `shouldPlayLavaHurtSound` is true for all
  living entities (ItemEntity's `health>0||tick%10` is the only
  override). Wire capture: a 10hp pig takes 3 lava ticks → hurt(1270)
  + burn(696 = holder id+1) per tick, then death+event3+event60.
  Per-type getSoundSource: Monster→HOSTILE, Player→PLAYERS,
  ItemEntity→AMBIENT (measured override, was srcNeutral), the rest
  NEUTRAL.
- **Implemented**: `lavaInside`'s living branch plays GENERIC_BURN
  on the mob's own `v.src()` when the hurt lands; the item branch
  now rides srcAmbient.
- **Pinned**: `TestLavaBurnSoundParity` — ≥2 GENERIC_BURN packets
  (sound event 696/name) while a pig burns in lava on both legs,
  cadence stops with the corpse.
- **Commit**: server + test.
## Slice 2026-09-27bb — player `data get` key set

- **Measured (jar + live)**: `data get entity @s` on a fresh player
  reports the Entity base set + LivingEntity block + Player/Avatar
  (Inventory, SelectedItemSlot, SleepTimer, Xp quad, Score,
  abilities, EnderItems, food quad) + ServerPlayer
  (warden_spawn_tracker, seenCredits, recipeBook, Dimension,
  playerGameType, DataVersion=4903). Exact value shapes probed:
  recipeBook = {recipes,toBeDisplayed} lists only; abilities seven
  fields; warden tracker {warning_level,ticks_since_last_warning,
  cooldown_ticks}.
- **Implemented**: `playerSaveMap` gained the 11 missing keys —
  DataVersion 4903, Dimension, FallFlying, Score, SleepTimer,
  seenCredits, playerGameType (+previousPlayerGameType when set),
  current_impulse_context_reset_grace_time, abilities from the
  live mode/flying state, warden_spawn_tracker, and the real
  recipeBook built from unlockedRecipes + seenRecipes.
- **Pinned**: `TestPlayerDataParity` — 44-key presence vector,
  conditional keys (CustomName, sleeping_pos, Passengers, respawn,
  entered_nether_pos, active_effects, RootVehicle…) pinned absent
  too; identical on both legs.
- **Commit**: server + test.

## Slice 2026-09-27bc — player entity attach metadata

- **Measured** (live wire, two clients on vanilla): a player entity's
  pairing stream carries set_entity_data with index 9 (DATA_HEALTH_ID,
  float — the session's real health, 20.0 fresh) and index 16
  (DATA_PLAYER_MODE_CUSTOMISATION, byte 0x7f — client information
  dirty-marks skin parts at join). DATA_PLAYER_MAIN_HAND (index 15)
  sits at its right-hand default and stays off the wire. mc-go sent no
  set_entity_data at all on player attach.
- **Implemented**: `encodePlayerAttachMeta` emits index 9 health (the
  owning session's live value via `playerAttachHealth`, 20 fallback)
  + index 16 0x7f; `sendEntityTo` selects it for the player entity
  type ahead of the Living branch (player records aren't Living).
- **Pinned**: `TestPlayerAttachMetaParity` — second client join,
  capture the spawn eid + set_entity_data entries on the first
  client's stream; assert index 9 float=20.0 and index 16 byte=0x7f.
  Identical on both legs.
- **Commit**: server + test.

## Slice 2026-09-27bd — NeutralMob persistent-anger NBT

- **Measured** (jar interfaces + live `/data get`): `anger_end_time` /
  `angry_at` come from `NeutralMob.addPersistentAngerSaveData` — the
  implementor set is {zombified_piglin, bee, enderman, wolf,
  polar_bear, iron_golem}; piglin/hoglin/zoglin do NOT emit (mc-go had
  wrongly included zoglin). Unarmed reads `anger_end_time=-1L`
  (NO_ANGER_END_TIME), no `angry_at`. Armed reads the absolute
  game-time end and `angry_at` as the provoker's UUID `[I;4]`
  (EntityReference = UUIDUtil.CODEC). All six roll
  `rangeOfSeconds(20,39)` (= mc-go's 400+rand381). Arming rides
  HurtByTargetGoal — a NoAI mob provoked by `/damage` stays unarmed;
  an iron golem arms universal anger at monsters even under NoAI.
- **Implemented**: `neutralAngryTypes` now matches the measured set;
  `anger_end_time` emits `AngryUntil` or -1 (was hardcoded 0);
  `angry_at` emits `uuidInts(AngryAt)` only when armed and !NoAI;
  `AngryUntil` arms for every NeutralMob type (was zombified_piglin
  only) and respects the NoAI gate.
- **Pinned**: `TestAngerNBTParity` — -1L/absent unarmed vectors for
  all six + zoglin absent, then an AI-on piglin provoked by the
  player: `angry_at` == player UUID ints, `anger_end_time` positive.
  Identical on both legs.
- **Commit**: server + test.

## Slice 2026-09-27be — player bed-sleep wire + skip

- **Measured** (26.2 bytecode + live capture): a bed click fans out, in
  one tick, `02` swing ×2 (tracker + sleeper), `79` `block.minecraft
  .set_spawn` overlay=0 (sleeper-only system chat), `54`
  SectionBlocksUpdate marking both halves `occupied=true`, and `63`
  entity-data idx6 pose=2 (SLEEPING) + idx14 serializer 11
  (OPTIONAL_BLOCK_POS) holding the HEAD half's packed pos. Wake
  flips idx6=0 + idx14 absent + `occupied=false`. Night skip needs
  every play-state player in the dimension asleep ≥100 ticks (default
  `playersSleepingPercentage=100`), then sends `sleep.skipping_night`
  overlay=1 (action bar) to the dimension + `71` set_time. A second
  click on an occupied head answers `bed.occupied` (action bar,
  sleeper-only style redirect foot→head); a day click answers
  `bed.no_sleep`. `data get entity` on a sleeper reads `sleeping_pos
  [I;x,y,z]` (int-array → styled `[x,-60,z]`) + `SleepTimer` short.
- **Implemented**: `useBed` now drives the full sequence — foot→head
  redirect, silent consume on non-bed, `bed.occupied`/`bed.no_sleep`
  action bars, `sleeping`/`sleepingPos` session state, swing ×2,
  `broadcastSleepMeta` (pose + optional-pos to trackers AND sleeper),
  `setBedOccupied` on both halves, `set_spawn` chat, slept-in-bed
  advancement. `sleepTickStep` per-dimension counts sleeping players
  ≥100 ticks, fires `skipping_night` once when ALL are asleep, then
  `setDayTime` morning + wake broadcast. `stopSleeping` (leave-bed
  PlayerCommand id 0 or disconnect) clears pose/sleeping_pos and
  frees the bed. `data get` emits `SleepTimer` + `sleeping_pos`.
- **Pinned**: `TestSleepBedParity` — two players, two beds: swing ×2,
  pose=2 + sleeping_pos meta head pos, `54` occupied=true, two ordered
  `data.entity.query` reads (`-60` in sleeping_pos, `s`-suffixed
  SleepTimer), `set_spawn` overlay=0 on the sleeper stream,
  `bed.occupied` on a second click; then all-asleep →
  `sleep.skipping_night` overlay=1, wake pose=0 + absent pos meta,
  `54` occupied=false; then a day click → `bed.no_sleep`. Green on
  both servers.
- **Known gap**: vanilla teleports the sleeper to a bedside stand
  position on wake (`stopSleepInBed` → findStandUpPosition) — noted
  in `stopSleeping`, not yet implemented.

## Slice 2026-09-27bf — sleep position snap + stand-up restore

- **Measured** (26.2 bytecode): `LivingEntity.startSleeping` does a
  single `setBlock(sleepingPos, OCCUPIED=true)` — only the HEAD half
  flips — then `setPosToBed` snaps the entity to the head center at
  +0.6875, zeroes delta, latches needsSync. `stopSleeping`'s bed
  lambda (skipped entirely when the bed is gone) clears head
  OCCUPIED, runs `BedBlock.findStandUpPosition`, snaps the entity to
  the pick, and re-aims the yaw toward the bed center with xRot=0 —
  before the STANDING pose + sleeping_pos clear. `useWithoutItem`
  checks OCCUPIED before the sleep-window gate (occupied beats
  no_sleep). `findStandUpPosition` picks the stand side as
  facing.clockwise flipped by `Direction.isFacingAngle(yaw)`, walks a
  fixed 12-offset ring (10 surround + on-head + on-foot) through
  `DismountHelper.findSafeDismountLocation` twice — strict pass then
  loose; a candidate needs a floor (shape top in cell <1.0, else
  below's top-1 ≥0), a clear 0.6×1.8 box, and on the strict pass no
  burning/wither/berry/cactus/powder cell or floor and no
  invalid_spawn_inside (end_portal/gateway). Bunk-bed branch noted,
  not implemented.
- **Implemented**: `useBed` snaps the session + entity record to
  bed-center+0.6875 (broadcast to trackers, no SyncPos — the client
  predicts it); `stopSleeping` frees the head only, runs the same
  offset search via a `collisionInterval` shape approximation
  (partial-height table: slabs, carpets, snow, path/farmland, chests,
  fences/walls at 1.5, trapdoors, no-collision attachables), falls
  back to on-bed+0.1, swings yaw to face the bed, and leaves the bed
  alone when it was broken mid-sleep. Occupied gate reordered before
  the day gate to match `useWithoutItem`.
- **Pinned**: `TestSleepBedParity` extended — `data get Pos` on the
  sleeper reads head-center+0.6875 while asleep and the computed
  stand-up cell after the skip (both deterministic on flat ground);
  the occupied flip asserts a block update at the head pos in either
  0x08 or 0x54 form. Green on both servers.

## Slice 2026-09-27cg — Guardian land flop + water air reset

- **Measured** (Guardian.aiStep, server side of the isClientSide arm):
  `isInWater()` → `setAirSupply(300)` every tick; else `onGround()` →
  `delta += (rand·2−1)·0.4, 0.5, (rand·2−1)·0.4`, `setYRot(rand·360)`,
  `setOnGround(false)`, `needsSync=true`. Tail: an active attack
  target forces `yRot = yHeadRot`. The flop lives in Guardian.aiStep
  itself — above `super.aiStep` — so it fires even under NoAI
  (isEffectiveAi only gates serverAiStep/the goal chain). The swim
  drive is GuardianMoveControl.tick (MOVE_TO: rotlerp 90°/tick yaw,
  speed lerp 0.125, sin-wave bob delta, look-ahead look control) —
  not implemented yet. ElderGuardian inherits the flop; its
  customServerAiStep also does the Mining-Fatigue curse and the
  home-claim — both already in.
- **Implemented**: `guardianFlopStep` in the every-tick living section
  (same placement as witch drink — outside the NoAI gate): water →
  `AirSupply = 300`; grounded-on-land → the ±0.4/+0.5 delta add,
  random yaw, grounded clear, NeedsSync latch.
- **Pinned**: `TestGuardianFlopParity` — both servers: land guardian
  + elder outside follow_range reach Pos[1] > floor+0.3 within a 6s
  window, Motion reads nonzero while airborne, Rotation[0] jitters;
  water-summoned guardian reads Air = 300. Green on both servers.
- **Correction — NoAI hover is parity, not a gap**: LivingEntity.aiStep
  calls `travel` only under `isEffectiveAi` — measured on the jar and
  live: a `{NoAI:1b}` zombie 10 blocks up stays at its summon pos
  forever on vanilla too (the classic floating-NoAI behavior). mc-go's
  missing NoAI motion integration is *correct*; the flop's single-hop
  latch under NoAI matches. `TestNoAIMobHoverParity` pins the hover:
  Pos[1] stays within 0.5 of the summon height for a 10s window on
  both servers. (An earlier version of this note claimed vanilla NoAI
  mobs fall — wrong.)
- **Still open**: online mode, terrain gen, lighting, region/.dat
  persistence, GuardianMoveControl swim (above), chicken-jockey,
  nav-malus + moveTo nav.speedModifier, per-type needsSync writers,
  player pose/swimming eye height, skeleton strafe friction-chain,
  FallFlying real impl, resting MotionY micro-gap, bunk-bed stand-up
  branch, per-damage-type hurt variants, selector nbt= arg,
  non-recipe advancement triggers, enchanting/brewing/beacon/lectern
  menus, real-client playtest at end.

## Slice 2026-09-27ch — Guardian swim chain (MoveControl + travelInWater)

- **Measured** (`Guardian$GuardianMoveControl.tick` + `Guardian.travelInWater`,
  26.2 javap): the MOVE_TO arm rotlerps the body yaw 90°/tick toward the
  horizontal heading, lerps the speed field 0.125 toward
  `speedModifier·movement_speed` (1.0 · 0.5 for the stroll), adds the
  sin wobble `sin((tick+id)·0.5)·0.05` rotated by yaw, the bob
  `sin((tick+id)·0.75)·0.05·(sin+cos)·0.25`, and the normalized-y drive
  `·speed·0.1`, then `setMoving(true)`. travelInWater is
  `moveRelative(0.1, (0,0,zza))` — a FIXED 0.1 horizontal drive along
  yaw while the speed field is on — then `move()`, then `delta·0.9` on
  all axes: no gravity, only `-0.005` on delta.y while `!moving &&
  target==null`. Out of water the control just zeroes speed+moving.
  `DATA_ID_MOVING` = meta index 16 boolean; `DATA_ID_ATTACK_TARGET` =
  index 17 int (already emitted at beam start, now also rides the
  guardian attach/dirty pack).
- **Implemented**: `guardianMoveStep` (dispatched for mobBeam types in
  place of `mobMoveStep`): out of water → `guardianSetMoving(false)` +
  the same `mobGrounded`/`mobTravelStep(0)` chain the flop's delta
  integrates through; in water → WanderTicks, fixed 80-tick interval,
  3D aquatic target via `strollOffset` x/z ±10, y ±7·0.7 confined to
  `fluidOf==water` cells (a dry pick aborts like a failed vanilla
  probe), `guardianSwimDrive` (dist<0.4 or >160-step stall → idle),
  `guardianTravelWater` (0.1 yaw drive, entityMove, ·0.9, idle
  −0.005), `guardianSetMoving` latching `Entity.Moving`+`MetaDirty`.
  `Entity` grew `Moving bool` (idx16) and `StrollTY float64`.
- **Pinned**: `TestGuardianSwimParity` — both servers: a guardian
  summoned mid-pool (20×8×20 water shell above min-y) reaches ≥1.5
  horizontal displacement and ≥0.8 vertical range inside 18s, while a
  same-pool zombie sinks ≥3.5 blocks to the bed. Green on both.
  `TestGuardianFlopParity` + `TestNoAIMobHoverParity` re-run green.
- **False lead corrected**: the first deep-pool run plummeted BOTH
  servers' guardians into the void at terminal velocity — the shell
  was below world min-y (-64) and `/fill` silently no-ops those cells,
  so the summons landed in void air. Geometry pinned to legal depth.

## Slice 2026-09-27ci — Bunk-bed stand-up position (BedBlock.isBunkBed)

- **Measured** (`BedBlock.findStandUpPosition` +
  `findBunkBedStandUpPosition` + both private offset-table builders,
  26.2 javap): when `getBlock(headPos.below()) instanceof BedBlock`
  the wake-position scan reorders — surround@head strict, surround@
  head-1 strict, above@head strict, then the same three loose —
  instead of the normal strict-all-12-then-loose-all-12. The two
  builders emit 10 surround + 2 above entries identical to the
  existing `bedWakeOffsets` table split at index 10.
- **Implemented**: `findStandUpPosition` gained the `isBedBlock`
  check on the cell under the head plus the six-stage scan order via
  a `scan` closure over the existing surround/above slices; the
  normal path is unchanged.
- **Pinned**: `TestBunkBedStandUpParity` — stacked south beds, the
  upper ring's head-space roofed at y=-58 (upper-ring and on-bed
  candidates all collide; cells directly above the bed stay air or
  vanilla reports bed_obstructed and never sleeps), flat slimes
  killed + spawning off, one sleeper on the top bunk. Night skip
  fires ~5.1s in; the wake teleport lands beside the LOWER bunk at
  (43.5,-60,45.5) — first strict surround candidate at the bunk
  level for a south bed. Green on both servers.
- **Harness lesson**: the poll originally broke on `wy < -59.5`
  which the tp spot already satisfies — the loop exited before sleep
  even engaged and reported the tp pos as a phantom failure. Wake
  detection now requires leaving the tp (x,z) too; the tp itself is
  asserted landed before the click.
- Also added a packet-id dump to TestSleepBedParity's occupied-flip
  failure message — that check flakes on vanilla timing (the flip
  sometimes lands outside the 2.2s capture window); pre-existing,
  unrelated to this slice.

## Slice 2026-09-27cj — Skeleton bow strafe through the travel friction chain

- **Measured** (`RangedBowAttackGoal.tick` + `MoveControl` STRAFE tick
  + `isWalkable`, 26.2 javap): seeTime ±1/tick, reset 0 on visibility-
  flag change; `d2 <= attackRadiusSqr && seeTime >= 20` →
  `navigation.stop()` + strafingTime++, else moveTo + strafingTime=-1;
  strafingTime>=20 → 30% flips on strafingClockwise/Backwards; while
  strafing, edge overrides at 0.75/0.25·rSqr, then
  `moveControl.strafe(back?-0.5:0.5, cw?0.5:-0.5)`. The STRAFE tick
  scales the ±0.5 pair to speedModifier·movement_speed (m<1 floor →
  ±0.125/±0.125 for a 0.25-speed skeleton), yaw-rotates the pair for
  `isWalkable` — a non-WALKABLE probe (fluid, void edge, wall) reverts
  to raw strafeForwards=1/strafeRight=0 — and lands zza/xxa on the
  travel input pair, NOT a position write.
- **Implemented**: `WalkXXA` on Entity; `mobTravelStep` refactored to
  `mobTravelStepPair(e,w,speed,zIn,xIn)` — both inputs decay ×0.98,
  the pair is length-capped at 1 (inputDirectionVector semantics) and
  rotated `x'=xxa·cos−zza·sin, z'=xxa·sin+zza·cos` in all three travel
  branches; the 3-arg wrapper keeps zza-only callers. New
  `strafeWalkable` approximates the node evaluator: feet non-solid
  over a non-solid-or-fluid-free below cell. The `mobRangedStep`
  strafe arm now builds the scaled pair, probes the rotated step, and
  drives `mobTravelStepPair` — momentum and floor friction carry the
  skeleton sideways instead of a direct ±0.177 delta.
- **Pinned**: `TestSkeletonStrafeParity` — night + spawning off,
  bow via `item replace` (summon runs no equipment populate),
  survival player 10 blocks on +x. 9s of `data get Pos` sampling:
  lateral z spread 7.82 on vanilla / 4.88 on mc-go (a standing or
  straight-chasing skeleton stays <1), and the draw→fire chain lands
  arrows — pinned as the player's Health dipping below 20 (arrows
  that hit despawn on impact, so the entity probe alone can't see
  them). Green on both servers.
- **Harness lessons**: the comparison world joins players CREATIVE —
  `gamemode survival` is required before a hostile mob will target at
  all (without it the "strafe" readings were just RandomStrollGoal
  wander). Arrow entities hit→despawn instantly, so probe the damage,
  not the projectile count.
- **Surfaced gap** (unpinned): `execute if entity @e[type=#minecraft:
  arrows]` on mc-go never matched its own arrow entity — entity-type
  tag selectors are a separate gap from the strafe chain.
- **Follow-on fix in the same commit**: the mc-go leg's `arrSeen`
  stayed false because every arrow hit — `mobShootArrow` had no
  Projectile.shoot inaccuracy. Added `triangle(0, 0.0172275·(14 −
  difficulty·4))` per-axis perturbation of the normalized aim before
  the 1.6 scale; mc-go now occasionally misses and leaves ground-
  stuck arrows (the `#arrows` tag probe fires — `type=#` selectors
  were already implemented, not a gap).

## Slice 2026-09-25a — Chicken-jockey seat model, NoAI physics, dismount

- **Gap list**: the welded-seat model for living vehicles (Entity.
  positionRider's PASSENGER-attach rotation − rider VEHICLE-attach
  offset) + the two follow-on gaps the differential probe rooted out:
  baby-mob bb dims at every bb-construction site, NoAI mobs never ran
  gravity, and vehicle removal never placed the dismounted rider.
- **Measured** (javap Entity.positionRider +
  LivingEntity.dismountVehicle + findFreePosition, live tp-echo +
  data-get probes on 26.2): seat = veh.pos + yRot-rotated PASSENGER
  attach − rider's VEHICLE attach, in Vec3.add/subtract op order
  (parens matter — `veh.X + (rx0+rx1)`, not `(veh.X + rx0) + rx1`).
  Baby-zombie-on-chicken welds to (vehX+0, vehY+0.5125, vehZ−0.1);
  adult zombie to (vehX+0, vehY, vehZ−0.1). Rider VEHICLE attach is
  0.6 player / per-type baby table (zombie 0.1875) / adult
  vehicleAttachY / 0. `Entity.getDimensions` for a baby resolves
  declared BABY_DIMENSIONS → `mobBounds·babyDimsScale` (armadillo
  0.6, happy_ghast 0.2375, armor_stand 0.5) → `mobBounds·0.5`
  (ageScale); adults use `mobBounds`. Vanilla's dismount chain:
  rider's rideTick sees `vehicle.isRemoved()` → stopRiding →
  LivingEntity.dismountVehicle → removed-vehicle path keeps
  `(riderX, max(riderY, vehY), riderZ)` then `findFreePosition`
  (deterministic Shapes.join ONLY_FIRST closest-point, bb inflated
  half-spreads — the welded spot is already free, returns itself) →
  dismountTo. LivingEntity.tickDeath removes the corpse at
  deathTime≥20 (rider stays welded through the linger), then the
  rider falls; a still-registered dead vehicle bb pushes it ~0.9
  diagonal off the weld — push direction is ill-conditioned (centers
  nearly coincide) and varies per run, so the pin is an envelope,
  not a point. NoAI is fully passive — `Mob.isEffectiveAi()` gates
  the whole travel branch of aiStep, so a NoAI mob never runs
  gravity at all: a welded-then-ejected NoAI rider FLOATS at the
  seat forever (measured >3.3s). FloatGoal canUse does NOT run
  under NoAI.
- **Implemented**: `entityBBDims(e)` (babyDimsTable → babyDimsScale →
  ·0.5 fallback) at every bb-construction site (ride, mob push/
  cramming, dataentity aabb, entityinside, explosion, firework,
  interact, itempickup, projectile, spear, tnt, gameadv);
  `passengerSeatPos(veh,rider)` in exact op order; riders emit the
  real welded Pos in entity NBT; `mobRemovedPassengerCleanup`
  applies `max(riderY, vehY)` on eject.
- **Pinned**: `TestChickenJockeyParity` — 12 probes, green on both
  servers: IsChickenJockey flag + set_passengers wire, welded-seat
  `distance=..0.05`/`..0.005` pins, baby-bb band hit + top miss
  (0.49×0.98 vs adult 0.6×1.95), rider stays alive after `kill
  @e[type=chicken]`, the welded bb's low band dips below the seat
  (see 2026-09-27a for the centered-seat bb model), an AI rider
  ejects and lands near the weld while the NoAI one floats, and
  EggLayTime lay + jockey-laying suppression.
- **Harness lessons**: `nbt=` Pos pins are ulp-fragile (vanilla
  serializes welded doubles at different bits than the literal —
  use `distance=`); existential list-nbt compare makes Pos probes
  vacuous in multi-entity worlds; exact welded positions of a
  dying vehicle's rider vary per run on vanilla (corpse push) —
  pin region envelopes.
- **Surfaced gaps** (unpinned): vanilla's own Pos double bits differ
  from literal NBT; entity-type `#tag` selectors verified working
  (earlier note was stale); dead-entity 20-tick linger + corpse
  push-apart not modeled (dismount lands inside envelope anyway);
  NoAI mobs still skip `mobLookStep` (head aim is a goal — correct)
  but also skip the fluid `entityFluidTick` push specifics of
  boats/minecarts (non-living path unchanged).

## Slice 2026-09-27a — execute-as/at retarget, welded-bb model, Age write

- **Gap list**: the execute-fork entity semantics the earlier entry
  logged as "silent on mobs", the welded passenger's selector-visible
  bounding box, and the `/data`-written AgeableMob fields.
- **Measured** (javap EntitySelector + live band sweeps + `data get`
  probes on 26.2): vanilla `execute as @e` swaps the executor AND
  re-anchors position/dim at the entity; `execute at @e` re-anchors
  position/dim only — `@s` stays the sender. A welded passenger's
  selector bb is the type's bounding box CENTERED on the seat point
  (a baby zombie welded to -59.4875 spans [-60.4625,-58.5125] —
  1.95 tall reaching 0.975 below pos), persisting after the
  vehicle's death until the entity moves again. `data modify /
  store result entity` writes to Age/ForcedAge/AgeLocked/InLove
  land on the real AgeableMob fields — a written Age=9 counts down
  to 0 through baseTick even under NoAI.
- **Implemented**: `executeFork` splits the two forks (`as` sets
  executor + re-anchors; `at` re-anchors only, `@s` stays the
  sender); `tpNeedsDimHop` only intercepts true cross-dim player
  hops so `execute as @e run tp @s` reaches the real tp;
  `baseExactPos` honors the execute-context pos/entity before the
  session pos; `summonPassengers` applies nested `Tags` lists;
  `Entity.WeldedPos` records the last seat weld (set on summon
  weld, cart scoop, seat-follow tick, dismount cleanup) and the
  selector bb reads the type's box centered on it while the entity
  still sits there — a moved entity's bb reverts to feet-anchored
  like vanilla's next setPos rebuild; `tpComp` emits the decorated
  entity namecomp (hover show_entity + uuid insertion) instead of
  a bare translate; `applyEntitySave` maps Age/ForcedAge/AgeLocked
  for ageable types and InLove for animal types to the live fields
  (Baby resyncs from Age<0) instead of ExtraNBT.
- **Pinned**: `TestExecuteIfParity` gains tp-fork rows (`as` moves
  the entity, `at` moves the sender at entity-anchored coords, the
  Pos reads and decorated feedback shape) plus the Age store/read
  pair — green on both servers. `TestChickenJockeyParity` re-pins
  the welded-bb low band as a positive hit on both legs and the
  AI-rider landing by position (distance, not box).
- **Surfaced gaps** (unpinned): `execute as @e run in <dim> run tp`
  for a mob still can't dim-hop (only session players route the
  intercept); the physical collision bb under a welded passenger is
  still feet-anchored on mc-go (only the selector path measured);
  `data get entity @s` inside `execute at` reads the sender — the
  anchor's own data needs a separate `@e`-first form, same as
  vanilla.

## Slice 2026-09-27b — cross-dim entity semantics (parked entities, worldLimited, all-dims @e)

- **Gap list**: `execute in <dim>` fork scoping — what happens to an
  entity teleported or summoned across a dimension boundary, and how
  far a selector can reach.
- **Measured** (javap `EntitySelector.findEntities`/`isWorldLimited` +
  `Entity.teleportCrossDimension` + live probes on 26.2): a selector
  is **world-limited** only when x/y/z/dx/dy/dz/distance args are
  given — then it searches only the source's level (the `in`-fork
  level, NOT the sender's real dim); otherwise `@e` walks EVERY level
  (`in the_nether` + free `@e[pig]` finds an overworld pig).
  `teleportCrossDimension` moves the entity to the target level via a
  parked add (`addDuringTeleport` into an unticketed chunk): the pig
  is invisible to `@e` in BOTH dimensions — never ticks, never tracks.
  `execute in <dim> run summon` parks the spawned entity the same way.
  A player arriving in the destination dimension wakes parked entities
  (vanilla wakes asynchronously on chunk ticket — a tp'd pig at y=70
  falls and may die; the visible-wake pin uses the free-@e leg plus a
  wide fall-column probe).
- **Implemented**: `Entity.Dormant` marks a parked entity — skipped by
  all selectors, the tracker (`pairable`), spawn fan-out, and
  `mobTickStep`; `wakeDormant` clears it (through `GetPtr` — `All()`
  snapshots copies) on `changeDimension` into the dim; `reparkDim`
  re-parks a dim's live entities when its last non-spectator leaves
  (dimension change or disconnect) — vanilla's chunk unload;
  `shouldPark` = someone online but not in the dim (a session-less
  world keeps entities live, like vanilla's always-ticketed spawn
  chunks — unit tests summon into no-session worlds); `dimSwapPrev`/
  `dimSwapDepth` on Session record the real dim through `in`-fork
  swaps so `dimHasLiveSession` counts the player's true level — a
  swapped issuer can't ticket the fork dim; `selectorSpec.worldLimited`
  set by x/y/z/dx/dy/dz/distance; `selectDataEntities` takes `srcDim`
  (the chain's `ctx.dim` under execute, sender dim elsewhere) so
  world-limited selectors scope to the source level and free ones walk
  every level; `tpToEntity`/`tpToPos` thread the destination level
  (dest entity's dim / issuer's swapped dim) into `tpMove`, whose mob
  branch routes through `tpCrossDimension` (ejects passengers, despawn
  in old dim, respawn in new unless parked); `broadcastFreshMob`
  no-ops on dormant entities and conversion carries the flag.
- **Pinned**: `TestDimHopParity` — 9 legs green on both servers:
  free-@e under `in` resolves cross-dim, world-limited scopes to the
  fork dim, post-tp invisibility in both dims, summon parks, wake on
  player arrival.
- **Surfaced gaps** (unpinned): passengers of a hopping mob eject
  instead of re-teleporting via `calculatePassengerTransition`; a
  player reached by `tp @e`-style multi-victim tp doesn't get the
  cross-dim path (tpNeedsDimHop only covers simple forms); vanilla
  wake is async chunk-ticketed while mc-go's is instant flag-clear
  (a prompt world-limited probe can see mc-go's pig before vanilla's);
  preexisting `TestDataEntityParity` failure confirmed on stock master
  (extra set_entity_data flush on `data modify` + chat/packet order on
  `kill` multi — separate slice needed).

## Slice 2026-09-27c — dirty-pack ordering + dirty-on-change gate

- **Gap list**: the `TestDataEntityParity` failure logged last slice —
  an extra set_entity_data flush after every /data op, plus a
  chat-vs-packet interleave on multi-victim /kill. Confirmed
  preexisting on stock master (4d2ca9b), not from the dim-hop slice.
- **Measured**: vanilla's SynchedEntityData.set marks an entry dirty
  only when its value changes, and packDirty emits just the dirty
  entries at the end-of-tick sendChanges — after the command's own
  reply (command dispatch runs inside the tick, sendChanges at the
  boundary), so a multi-kill wires [chat, dying-pack, dying-pack].
  mc-go emitted the full non-default pack one tick later (the merged
  save always carries Age, and the ageable read marked MetaDirty
  unconditionally), and a tick boundary landing mid-command flushed a
  victim's dying pack between the kills and the chat.
- **Implemented**: applyEntitySave marks MetaDirty only on an actual
  wire-visible change (Age→Baby flip, WaterConv/FreezeConv toggles —
  a restarted countdown alone doesn't resend). Entity.dataSyncTick
  stamps the flush-ready tick: dataSyncReadyTick() returns gameTime()
  inside GameTick (same-tick sendChanges like vanilla's tick-internal
  writes) and gameTime()+1 off-thread (command path — the pack waits
  one full boundary so the chat lands first). Both flush sites
  (mobTickStep dying branch, mobSyncFlush) gate on it, and
  dataSyncAttrs/AttrDirty hold with it so meta+attrs keep vanilla's
  same-packet order.
- **Pinned**: TestDataEntityParity green on both servers again (was
  failing on stock); TestDimHopParity, TestKillAdvParity,
  TestKillScoreParity, TestDataMergeHealthParity green.
- **Surfaced gaps** (unpinned): TestDataCommandParity fails on stock
  too — /data get block appends a stray "block_entity_data" + sign
  NBT dump after the chest query component (unrelated to this slice);
  a real Baby/WaterConv/FreezeConv flip still emits the full
  non-default pack rather than the single changed entry; pig's baby
  flag has no wire encoding in encodeLivingMetadata at all.

## Slice 2026-09-27d — cold section scans + mushroom walk

- **Measured**: the preexisting TestDataCommandParity failure was a
  313ms GameTick, not a wiring bug — the first randomTickStep after
  join built random-tick flags for every cold section in the
  simulation window (each costs a 4096-block FillBox, ~10,584 of
  them), so the setblock sign's queued block_entity_data waited for
  that tick's end-of-tick flush and landed ~50ms past the test's
  300ms setup drain. Vanilla has no equivalent hitch: its
  LevelChunkSection.isRandomlyTicking is computed when the section
  materializes, off the tick.
- **Implemented**: sendWindow now warms the random-tick/light flag
  cache for every column entering a session's view (packet-handler
  thread, never GameTick), and randomTickDimension scans at most
  randScanBudget=64 remaining cold sections per tick — cold stragglers
  are skipped as untickable that tick and join on later ticks.
  blockChangesFlushStep moved ahead of chunkTickStep so a queued
  update never waits behind a chunk-batch encode.
- **Measured+fixed**: MushroomBlock's spread walk was chained — each
  hop offset from the previous cell, so spreads drifted ±5 (observed
  landings at x=63..72 around a seed at 67). Vanilla rolls all four
  candidates as offsets from the seed and keeps the last survivable
  one — the spread lands within ±1. mushroomSpread now matches.
- **Pinned**: TestDataCommandParity green on both servers (was failing
  on stock); TestMushroomSpreadParity green (ring counts now match:
  4=4, 2=1); TestCropGrowthParity, TestIceMeltParity,
  TestLeafDecayParity green.
- **Surfaced gaps** (unpinned): mc-go's overall random-tick throughput
  still runs below vanilla's at huge tick speeds — at
  random_tick_speed 4096 a single tick costs ~1-2s because every
  tickable section (all grass_block surface sections) does 4096
  GetBlock rolls, so a 30s window simulates ~15 ticks vs vanilla's
  ~600; the >0 spread assertions mask it, but a rate-parity probe
  would show the gap. Also still open from 2026-09-27c: a real
  Baby/WaterConv/FreezeConv flip emits the full non-default pack
  rather than the single changed entry, and pig's baby flag has no
  wire encoding in encodeLivingMetadata.

## Slice 2026-09-27e — dirty-diff pack emit + AgeableMob DATA_BABY wire entry

- **Measured** (26.2 jar, live packet capture): SynchedEntityData
  .packDirty emits ONLY entries whose value differs from the defined
  default AND whose dirty bit is set — the summon-time non-default set
  stays dirty, so vanilla emits the SAME non-default pack twice (the
  pairing/attach emit does NOT consume the dirty set; the next
  sendChanges' packDirty replays it then clears). Empty dirty set →
  no packet at all. Compound merges emit one index-ascending pack
  ({Age,Health} → [9,16]). DATA_BABY_ID = index 16 boolean for every
  AgeableMob subclass (pig/cow/sheep/chicken/wolf/villager measured,
  plus zombie/husk/drowned/zombie_villager). LivingEntity defined
  defaults: health 1.0f, air 300, snow-golem pumpkin true. Villager
  display name is profession-suffixed (entity.minecraft.villager.none).
  Frog is NOT ageable on vanilla.
- **Implemented**: Entity.metaSent livingMeta snapshot + metaInited
  flag; ensureMetaInit seeds the defined-default baseline.
  encodeLivingMetadataDirty diffs each field vs metaSent and emits
  only changed entries index-ascending, returns nil on empty —
  replaces the full-pack resend in the MetaDirty flush arm. Attach
  encoder emits the non-default pack WITHOUT stamping (reproduces
  vanilla's duplicate emit via the post-attach MetaDirty latch).
  packsBabyAt16 gates the idx-16 emit; the surgical dataSync pack
  gained the baby entry and stamps metaSent per emitted field so the
  flush won't re-emit. Health/dying/witch/projectile emit sites stamp
  metaSent.health. villagerProfessionNames lazy inverse for the
  profession-suffixed display key.
- **Pinned**: new TestAgeableMetaParity (16 cmds) green vs both
  servers — [16,bool] alone on Age merge, [9] alone on Health merge,
  [9,16] one-pack compound, frog exclusion, villager/sheep attach
  packs with the dirty-replay duplicate.
- **Regression check**: 15-test metadata-sensitive batch — 12 green.
  Three deterministic failures reproduce identically on stock 328f384
  (pre-my-slice): TestCureConversionSyncParity (zombie_villager
  emits no move_entity_pos — cured entity never wanders),
  TestWitchHealParity (mc-go throws only regeneration h31, never
  healing h24 — raider never drops to ≤4hp), and
  TestPlayerShearedEquipmentParity (mc-go misses the
  recipes/misc/wolf_armor_dyed unlock + a gamemode chat). Preexisting
  regressions from an earlier session commit — bisect next.

## Slice: summon-time zero-delta move emit + shear-test determinism

Regression triage after the dirty-pack slice surfaced two preexisting
breaks and one flaky test:

- `mobMoveSync`'s position gate dropped `e.PosDirty` when the
  `!e.syncInit` latch branch moved to `latchSyncBaseline` (which clears
  PosDirty silently). Vanilla's sendChanges forces the position branch
  on syncPosition needsSync — snapTo's mark at summon answers the
  pairing burst with a zero-delta move_entity_pos. Restored:
  `pos := posChanged || e.PosDirty || period`. TestCureConversionSyncParity
  (zombie_villager→villager wire shape) green again.
- TestPlayerShearedEquipmentParity had a harness asymmetry — the test
  set `s.GameMode=Survival` for mc-go while vanilla joins creative, so
  `/gamemode survival @s` chatted on one leg only — and flaky geometry:
  the wolf sat 2.0z from the player, close enough for the sheared
  armor's random drop offset/drift to sometimes land inside the 1.425
  pickup reach, emitting an extra wolf_armor_dyed advancement on one
  leg. Both legs now join creative (matching vanilla), and the wolf
  moved to 2.5z so the drop zone stays outside reach. Green.

Also verified `rewards.recipes` is a non-path on 26.2: no game
advancement declares recipe rewards (only recipe advancements
self-unlock, already implemented), so nothing to wire.

## Slice: witch heal-goal parity — cooldown halving + throw scatter

Third preexisting regression from the dirty-pack batch. mc-go threw
regeneration forever, never healing; vanilla threw [regen regen heal
heal]. Two measured divergences, both fixed:

- `NearestHealableRaiderTargetGoal.start()` sets cooldown =
  `adjustedTickDelay(200)` — the goal isn't requiresUpdateEveryTick,
  so that's `reducedTickDelay(200)` = `positiveCeilDiv(200,2)` = **100**
  wall ticks (decremented in Witch.aiStep every tick). mc-go locked at
  200 → re-locks always arrived after the regen splash had already
  ticked the raider past the ≤4hp heal window. Now 100.
- `Witch.performRangedAttack`'s `spawnProjectileUsingShoot(..., F)`
  passes uncertainty **8.0**: `getMovementToShoot` normalizes the aim
  vector then adds `random.triangle(0, 0.0172275·u)` per axis before
  scaling by velocity. mc-go lobbed perfectly, so every regen splash
  applied at near-full prox — raider healed past 4hp and every later
  pick stayed regen. The 8.0 scatter lands some splashes far enough
  that duration×prox <50t → zero heal ticks → the ≤4hp window stays
  open for a healing throw. Implemented in witchThrowAt.
- Heal-goal selector left unfiltered (javap: lambda is
  `hasActiveRaid && !is(WITCH)` — no health check on the target; the
  health gate lives only in the performRangedAttack potion pick,
  `health <= 4.0 → HEALING else REGENERATION`, which mc-go already
  matched).
- TestWitchHealParity green: van [h31 h31 h24 h24], mc [h31 h24].

## Slice: /difficulty same-value failure

javap DifficultyCommand: setDifficulty throws
ERROR_ALREADY_SAME_DIFFICULTY (commands.difficulty.failure with the
target's display name, runtime exception — red, no parse context)
when WorldData.getDifficulty already holds the requested value.
mc-go reported success on every name. Added the same-value gate;
TestUtilCommandParity gained a repeat `difficulty hard` in its
sequence (the only place an unchanged value occurs) — green vs both.

## Slice: turtle egg random-tick hatch parity

javap TurtleEggBlock.randomTick (26.2): the timing gate moved into
the environment-attribute system — shouldUpdateHatchLevel rolls
`random.nextFloat() < envAttr(TURTLE_EGG_HATCH_CHANCE)` AND onSand
(#minecraft:sand directly below). day.json timeline feeds constant
keyframes 21062 -> 1.0 and 21905 -> 0.002 (the attribute default), so
the roll is guaranteed inside [21062,21905) and 0.002 elsewhere.
hatch < 2: egg_crack sound (BLOCKS 0.7 / 0.9+0.2f) + hatch+1 (flags 2,
no neighbor updates) + BLOCK_CHANGE game event. hatch == 2: egg_hatch
sound + remove + BLOCK_DESTROY + per-egg 2001 break effect and a baby
turtle snapTo(x+0.3+i*0.2, y, z+0.3, 0, 0), age -24000, homePos = egg
pos. Implemented in turtleEggRule (randtick.go), registered in
randRules.

Turtle NBT parity: addAdditionalSaveData writes home_pos (BlockPos
codec, own field — separate from the Mob home restriction pair, read
back as `read().orElse(blockPosition())` so every summon lands the
spawn pos) and has_egg. mc-go now emits both after the Mob pair
(write-order match), and /summon applies home_pos/has_egg SNBT plus
defaults TurtleHome to the summon pos.

TestTurtleEggHatchParity: sand pad + isolated stone pad; mature egg
(eggs=4,hatch=2) and a fresh egg (eggs=2,hatch=0) inside the
[21062,21905) window with random_tick_speed 4096 — both hatch to air,
the off-sand egg stays, 6 babies per leg, baby home_pos/has_egg match,
Age in (-24000,0) — green vs vanilla and mc-go (-23884 / -23967).
Reply parsing note: `data get` replies arrive as
commands.data.entity.query NBT components — decode the `with` tail,
not ASCII digits in the raw line.

Open adjacent gaps: turtle metadata accessors HAS_EGG (~17) /
LAYING_EGG (~18) not emitted; 2001 effect asserts come for free but
block_update order vs the per-egg event loop is unpinned.

## Slice: frogspawn scheduled-tick hatch parity

javap FrogspawnBlock (26.2): NOT random-ticking — onPlace arms
scheduleTick(pos, this, nextInt(3600,12000)) on every write; the due
tick re-checks canSurvive → mayPlaceOn(level, below): fluid@below in
#supports_frogspawn (water only) OR block@below in the empty block tag,
plus fluid@pos EMPTY (always true — frogspawn is a plain block). Fail
→ destroyBlock (2001 + drops); pass → hatchFrogspawn:
destroyBlock(pos,false) + block.frogspawn.hatch (677, BLOCKS 1.0/1.0)
+ nextInt(2,6) tadpoles at snapTo(x+clamp(nextDouble,0.2,0.8), y-0.5,
z+clamp(...)), yaw nextInt(1,361), setPersistenceRequired, reason
BREEDING. updateShape re-checks canSurvive unconditionally and folds
to air silently (no 2001); entityInside pops under a falling_block.
Implemented in frogspawnTick/frogspawnCanSurvive (randtick.go), armed
in writeOnPlace, dispatched in blockTick, plus the silent updateShape
branch in checkSurvival and the falling-block inside case. mobHealth
gained tadpole=6.

TestFrogspawnHatchParity: water pad floats the egg, stone egg is dry;
`tick sprint 13000` covers the full delay range on both legs —
7 probes symmetric (both eggs gone, tadpoles present + persistent +
absent near the stone egg), tadpole count in [2,5] per leg (4/4).

## Slice: sniffer_egg scheduled-tick hatch parity

javap SnifferEggBlock (26.2): NOT random-ticking — onPlace arms
scheduleTick at (boosted ? 12000 : 24000)/3 + nextInt(300) where
boosted is #sniffer_egg_hatch_boost (moss_block) below; boosted writes
also levelEvent 3009, and every write gameEvents BLOCK_PLACE — the
flag-2 bump writes re-arm through the same path (the delay is a total
split across three crack stages, not per-stage). tick: hatch < 2 →
sniffer_egg.crack (1565) + hatch+1 flags 2; hatch == 2 →
sniffer_egg.hatch (1566) + destroyBlock(false) (2001 + air) + one
baby sniffer snapped to the egg center, yaw wrapDegrees(nextFloat*360).
Sniffer babies spawn at Age=-48000 (Sniffer.class ldc #121), not the
Animal default -24000. Implemented as snifferEggBoost/snifferEggOnPlace
(randtick.go — called from writeOnPlace) + snifferEggTick dispatched
in blockTick.

TestSnifferEggHatchParity: moss vs stone fixture legs pin the 2x boost
differentially — sprint 13000 hatches boosted eggs (max ~12897) while
the unboosted egg sits deterministically at hatch=1, sprint +12000
completes the unboosted three-stage max (~24897). All 11 probes
hit/hit, global sniffer count 4/4, baby Age in (-48000,0) both legs
(-47204/-47321).

## Slice: snow-layer random-tick melt parity

javap SnowLayerBlock.randomTick (26.2):
getBrightness(LightLayer.BLOCK, pos) > 11 → dropResources(state,
level, pos) + removeBlock(pos, false). No dampening term (unlike
frosted ice's 11 - age - dampening), no 2001 — removeBlock is a
silent air write, not destroyBlock.

The differential pinned the real propagation semantics: the step
into the snow cell costs 1 + the layer's own lightBlock (1 for
partial layers, 15 for layers=8), so melt = emission - taxicab > 12
for layers 1..7 — arrival 12 (torch taxicab-2, layers=2) holds,
arrival 13 (adjacent torch layers=2 OR layers=3) melts — and a full
layers=8 layer can never melt this way (adjacent glowstone held on
vanilla). dropResources yields NOTHING on melt: every pool in
blocks/snow.json is entity:this-gated and the bare block context
binds no entity — measured absence of any item entity at melted
cells. Existing snowMeltRule was corrected from broadcast 2001 +
snowball drops + naive >11 gate to the measured shape (layers>=8
early-out, blockLightAbove(12), no effect, no drop). Snow formation
on precipitation stays unmeasured — no weather system.

TestSnowMeltParity (tests/blackbox/snowmelt_test.go): six fixtures
across the 12/13 arrival edge at layers 2/3/8 with torch (14) and
glowstone (15) sources — 14 probes hit/hit, both melt sites empty.

## Slice: move-stat exotic buckets — water arms, fly XZ, tick-og jump, fall landing

javap (26.2) `ServerPlayer.checkMovementStatistics` /
`handleMovePlayer` / `Player.causeFallDamage` /
`Entity.checkFallDamage` / `Entity.updateSwimming`:

- The fly arm reads the 2D XZ leg only (`sqrt(dx²+dz²)·100 > 25`) —
  a pure rise banks NOTHING there (a straight drop banks fly only
  through its XZ component). Earlier "3D leg" assumption was wrong;
  measured `+0.5y` rise → zero stat on vanilla.
- The jump check reads the tick-physics onGround flag: the entity
  settles onto its support between packets server-side, so an
  airborne-flag packet that rises after a grounded entity
  (`fly-x` then `+0.5y`) counts JUMP and then banks nothing —
  the packet flag would say fly. mc-go now recomputes the flag as
  `supportedBelow(last pos)` once a tick has elapsed since the
  previous move (`sess.lastMoveAt`, ≥50ms).
- `updateSwimming` asymmetry: enter needs sprint + eye-in-water +
  feet-in-water (a shallow-water sprint stays walk_on_water); stay
  only needs sprint + feet water (`sess.swimming` latch).
- isCrouching is the CROUCHING pose (latched per tick), not the
  sneak flag — a same-tick sneak+move still banks walk.
- fall_one_cm lives in `Player.causeFallDamage` on the landing
  edge (`fallDistance ≥ 2.0` → round(fd·100), mayfly gate):
  accumulation is `!isInWater && dy<0` — including the landing
  packet's own descent — water legs accumulate nothing.
- `checkRidingStatistics` (rideTick path) already mapped; added
  camel_husk to the horse arm.

mc-go (`internal/server/play.go`, `session.go`, `ride.go`):
`awardMoveStats` rewritten to the measured order — swim(3D) →
eye-in-water(3D) → feet-in-water(XZ) → climbable&&dy>0(dy) →
grounded sprint/crouch/walk(XZ, sprint wins) → fall-flying(3D) →
fly(XZ>25). Jump og is the ≥1-tick support estimate; fall stat
fires in the landing branch before the damage gate; exhaustion
folded into the water/sprint arms (creative exempt).

The rig is a stone shell with a 3x3 hollow at y=200 — an open pool
refilled from its own border flow between legs and polluted every
post-air-fill bucket with isInWater (the earlier -60 aquifer rig
did the same). Probes confirmed leg-by-leg dumps on BOTH servers.

`TestMoveStatParity` legs: walk / feet-water on_water / shallow
sprint on_water (NOT swim) / submerged under_water+3D-XZ split /
sprint-submerged swim / sneak-latched crouch / sneak+sprint sprint /
fly-x fly / fly-y nothing+jump / 4m descend+land fall_one_cm /
minecart ride (value-exempt). Green vs BOTH servers.

## Slice: scoreboard criteria wiring — stat feeds, live poll, kill/death, reset

javap (26.2) `ServerPlayer`/`Scoreboard`/`ObjectiveCriteria`:

- `Stat extends ObjectiveCriteria` — every awardStat calls
  `scoreboard.forAllObjectives(stat, holder, access ->
  access.add(amount))`: stat objectives gain the awarded DELTA, not
  the stat total — they count forward from creation (no backfill)
  and sum onto manual `players set` writes.
- `resetStat(stat)` → `forAllObjectives(stat, holder, access ->
  access.set(0))` — a getOrCreate zero write broadcast to trackers
  even when the score never existed.
- die() order: `forAllObjectives(DEATH_COUNT, +1)` BEFORE the
  stats — then `credit.awardStat(ENTITY_KILLED.get(victimType))`,
  `credit.awardKillScore(victim, src)` (KILL_COUNT_ALL first, then
  the kill stat — which feeds its own criteria — KILL_COUNT_PLAYERS
  for player victims, then TEAM_KILL/KILLED_BY_TEAM), then
  `awardStat(DEATHS)`, then `resetStat(TIME_SINCE_DEATH)` and
  `resetStat(TIME_SINCE_REST)` — mc-go only reset the first.
- ServerPlayer.doTick tails the read-only poll per tick in order
  HEALTH(+absorption), FOOD, AIR(real airSupply), ARMOR,
  EXPERIENCE(totalXp), LEVEL — each behind a per-player
  lastRecorded gate, one update_score per change; objective
  creation does NOT emit (an objective created mid-session stays
  unset until the value next moves).
- Real gap surfaced: die()'s `credit.awardStat(ENTITY_KILLED.get
  (type))` was only modeled at hit sites (melee/projectile/spear)
  — `/damage by`, env finishes, and credit-window kills never fed
  `minecraft.killed:<type>` objectives (and missed the stat
  itself). The award moved into die()'s credit resolution where it
  belongs; the three hit-site awards were redundant.

mc-go: `feedStatCriteria` switched to delta-add via the existing
bumpCriteria; `resetStatCriteria` (zero write) wired inside
`setStat` for play-state calls; `awardKillScore` reordered to
vanilla's sequence; `playerDied` gains the leading deathCount bump
plus the missing time_since_rest reset; `playerFluidTickStep`
polls all six live criteria per tick (creation seeds the
per-criteria snapshot so only changes emit — same gate shape as
lastRecorded); `air` reads the real `sess.airSupply`.

`TestScoreboardCriteriaParity` adds legs: stat feed hit,
manual-set-then-award sums (7+1=8, 3+1=4), fresh objective never
backfills, submerged air feed, and the death reset's zero write.
`TestKillScoreParity` exercises the reordered kill path. Both
green vs BOTH servers.

## Slice: block-light propagation — per-cell grid, BFS engine, real nibble arrays

Live probe on 26.2 (`/setblock torch` / `air` at (8,-58,0), flat
overworld, watching Update Light per chunk):

- The packet carries skyYMask, blockYMask, emptySkyYMask,
  emptyBlockYMask (BitSet = VarInt long count + big-endian longs, bit
  = section+1, bit 0 = below-world) then the per-type array counts and
  2048-byte nibble arrays (low nibble first, index order x->z->y).
- Torch place: `skyMask=[]`, `blockMask={1,2}` (sections 0,1), no
  empty bits, two block arrays of 628 + 20 nonzero nibbles — the
  packet ships ONLY the light type that changed; sky is not re-sent.
- Torch remove: identical masks, both arrays all-zero — removal ships
  zeroed arrays under blockMask, never the empty mask (vanilla ships
  arrays whenever the section's light-data object exists; it is not
  content-based).
- The torch's 29x29x15 glow crosses six chunks — vanilla emits one
  Update Light per affected chunk in arbitrary order, and roughly a
  quarter of runs drops the centre chunk's packet entirely. The test
  compares over the chunks vanilla actually emitted.
- A torch does not move the sky heightmap: vanilla's map counts only
  light-occluding cells (isSuffocating/canOcclude), so mc-go's old
  "highest non-air" top counted the torch and shipped a spurious
  skyMask. chunkTops/columnTop now scan for suffocatingBlocks cells.
- `affectedSections` on an unmoved top returned the one-section range
  [oldTop] — now nil, matching vanilla emitting nothing for sky.

mc-go (`internal/server/light.go`, `chunk.go`, `dimension.go`):
per-dimension `blockLight map[world.Pos]uint8` grid of nonzero cells
plus `lightDirty` chunk->sections pending emission. `blockLightChanged`
gates on emission/opacity deltas, then runs vanilla's algorithm: add
BFS at `nv = cur - max(1, opacity)` (opaque 255, water 3, lava 255),
remove as the erase flood (cells with `nv <= v - opacity` erased, the
brighter boundary re-seeded, eaten emitters re-spread at their own
level). All six write-path emit sites now call `emitLightUpdate`,
which drains the dirty set into one packet per touched chunk and
tags the column's sky sections onto the position chunk's packet —
the same wire order as before (after the caller's ack). The
join-time light body moves lit sections from the captured flat
empty-Block mask into real arrays.

`TestTorchLightParity` collects every Update Light for ~700ms after
each write on BOTH legs and compares masks + full nibble arrays per
chunk — 4/4 consecutive passes; preexisting-fail `TestDigParity`
(mc-go never breaks, identical on clean master) logged for later.

Residual: raw `SetBlock` write paths outside the two funnels
(pistons, explosions, portals, sculk, fluids) still bypass light
recompute; horizontal sky propagation and nether update_light remain
unmeasured; the join light body still can't distinguish a section
object that was lit and emptied from one never created.

## Slice: dig speed onGround settle — /tp'd players dig at full speed

`Player.getDestroySpeed` divides destroy speed by 5 while the entity's
onGround flag is down — vanilla's physics tick resolves that flag onto
the footing every tick, so a player teleported onto solid ground digs
at full speed almost immediately. mc-go's flag only moved with move
packets, so any session that /tp'd and dug without moving paid the
airborne penalty forever (grass 90 ticks vs vanilla's 18) —
`TestDigParity` had every mc-go leg timing out at broke=false.
`digTicks` now falls back to `supportedBelow` when the flag is down,
the same settle estimate the jump-stat check uses. Green: grass
start+stop 17.9 vs 17.1 ticks, stone bare-hand 149.2 vs 150.9.

## Slice: emission table — state-folded lightLevel lambdas, water opacity

`internal/server/ice.go`'s `lightEmit` gained `stateEmission` — the
half of vanilla's table whose lightLevel lambdas read block state:
CaveVines.emission(14) on berries (+ plant), the sea pickle's
waterlogged `3+3*pickles`, a lit candle's `3*candles` (+ cake at 3),
the respawn anchor's `floor(15*charges/4)` → 3/7/11/15, and the light
block's level prop. Missing flat entries filled: enchanting_table 7,
sculk_catalyst 6, end_portal 15, nether_portal 11, lava_cauldron 15;
lit-gated set extended to redstone_ore 9, redstone_torch/wall_torch 7
and the 4 waxed copper bulbs 15 (previously emitted even unlit).

Three measurement corrections surfaced while probing: the daylight
detector's inverted and the lightning rod's powered 15s are redstone
signal, not light — neither registration sets lightLevel (checked the
26.2 `Blocks` bytecode directly), so both emit 0. And water's opacity
for block light is 1, not 3 — `Properties.liquid()` sets no lightBlock,
measured live: pickle emission spreads 15→14 into water cells.

`TestLightEmissionParity` writes 26 emitter probes one at a time into
border chunk (0,0) — sealed cavity so interior sky reads 0 on both
engines — and diffs masks+nibble arrays per write against vanilla.
Measured on the way: an airborne waterlogged pickle drains and dies
(emits 0), so the wet probes run in a contained pool; cave vines
self-destroy without an overhead block; and a dark transparent probe
writes air→transparent — no packet on either leg, which the test
asserts stays silent. `collectChunkLightFor` now merges multi-packet
updates per chunk (mask-bit-keyed) instead of last-writer-wins, and
`drainLight` waits for real silence before probing. The test caught
two real bugs: the respawn prop is `charges` not `charge` (emission
never applied) and the water-3 entry above.


## Slice: nether update_light — border-recipient rule, layer-presence masks

Vanilla DOES broadcast block-light updates in the nether, but only to
players for whom the lit chunk sits on the tracked border:
`ChunkHolder.broadcastChanges` sends `ClientboundLightUpdatePacket` via
`playerProvider.getPlayers(pos, true)` where boundaryOnly resolves to
`isChunkOnTrackedBorder` — `isChunkTracked` (in the tracking view AND
not pending in `PlayerChunkSender`) plus at least one of the eight
neighbours not tracked. Interior light changes get no wire update at
all — the client relights locally. `sendLightUpdate` now applies the
same per-session gate.

`chunkSeen` (mc-go's `isChunkTracked`) previously answered
queued&&sent over the hysteresis ring, which keeps columns the circle
already forgot — after a teleport that left stale "tracked" chunks
and suppressed border emission. It now checks membership in the
current view — `isWithinDistance` with the two-chunk shrink
(`max(0,|d|-2)²` sums `< D²`, the 7x7 square at D=2) around the
player's live chunk — AND sent.

Vanilla marks, per written cell, every section the cell's 3x3x3
neighbourhood reaches (`SectionPos.aroundAndAtBlockPos` →
`sectionLightChanged` filter bits), including across chunk edges; at
packet build a marked section ships its array when a light layer was
materialized there (it outlives its contents — emptied sections keep
shipping all-zero arrays) and lands on the empty mask when it never
was. `blockLightChanged`'s dirty fold now marks the same around-set,
`lightLayerSecs` records materialization on first write, and
`lightUpdateBody` splits `blockMask`/`emptyBlock` on it.

Two measured bugs surfaced along the way: the nether emitted nothing
at all before (emission was gated on `d.skylight`), and `netherrack`
was missing from `suffocatingBlocks` — the generator's type whitelist
skipped vanilla's custom `minecraft:netherrack` block class, so light
leaked through opaque walls (mc-go lit 584+690 nibbles where vanilla
lit 0+301). gensuffoc now whitelists the nine occluding custom types
(netherrack, redstone_ore/lamp, jigsaw, structure, test, test_instance,
barrier, infested_rotated_pillar) → 375 entries, regenerated.

`TestNetherTorchLightParity` seals a netherrack pocket inside border
chunk (9,6) of a 7x7 window and pins the torch place/remove wire on
both servers: `blockMask=[32]` with a byte-identical 301-nibble sec4
array, `emptyBlock=[16]` for the boundary-touched unlit sec3 — on both
legs. `TestTorchLightParity` now teleports the player so the torch
chunk is deterministically border (spawn selection is not shared), and
re-verified green after the gate landed.

## Slice: sky-light sectioned storage — init-mark propagation, written-vs-virgin emit rule, tick-end batching

Measured on 26.2 by decompiling `ClientboundLightUpdatePacketData`,
`LayerLightSectionStorage`, `SkyLightSectionStorage`,
`QueueBasedLightEngine`, `FlowingFluid` and `LiquidBlock`, plus live
wire probes on the vanilla jar:

- `prepareSectionData`: for each section in the mark set, a null data
  layer ships **no bit at all**; `DataLayer.isEmpty()` (allocated but
  never written — `data==null && defaultValue==0`) ships the empty
  mask; otherwise the data mask + array, even when every nibble is
  zero. The emit rule is layer existence + written state, not content.
- `putSectionState`: every 0→nonzero transition runs
  `initializeSection` → `markSectionAndNeighborsAsAffected` — the full
  27-box on BOTH engines — including the neighbour-count bumps, so
  init marks propagate two chunks out from any write. `removeSection`
  only adds to `toRemove` — removals mark nothing.
- `swapSectionMap` drains `sectionsAffectedByLightUpdates` once per
  tick into `broadcastChanges`, which emits the light packet BEFORE
  the block updates; marks on unlayered sections drop silently.

mc-go's column-model sky is replaced by the same sectioned
layer-storage: `lightLayered`/`blkLayered` eligibility maps, the
`blkWritten`/`skyWritten` virgin bits (non-virgin materializations —
the above-top full-15 default and the `repeatFirstLayer` replica —
count as written at creation), `materializeLightSec` now emits the
27-box marks on every materialization while `dematerializeLightSec`
marks nothing, and `emitLightUpdate` batches per-tick behind
`s.ticking` with the light flush ordered before `blockChangesFlushStep`
— all serialized under `d.lightMu` because session goroutines, the
tick flush, and chunk encoders all touch the maps.

The chunk pipeline previously generated sky lazily inside
`encodeLightDataFor`: a flood reaching an ungenerated column mid-tick
materialized it then and there, and its init marks went out inside an
unrelated window. Vanilla runs INITIALIZE_LIGHT on every streamed
chunk up front, so `sendWindow` now `ensureSkyChunk`s each fresh
column plus two chunks around it while streaming — the init emits
land during streaming on both legs.

`TestSkyPropParity` pins the wire on both servers for twelve probes —
opaque write + undo, a 9x9 platform + undo, a torch under the platform
(sky and block masks together), a water source's shadow as it falls
per-section (hasData flips fan init marks ±2 chunks), a full-chunk-top
clear, transparent torch (block-light bits appear while sky stays),
and glass. All twelve compare chunk-for-chunk, mask-for-mask and
array-for-array identical.

Honest residuals: the fluid probes surfaced real fluid-sim divergences
that are NOT light bugs and now have their own gap — mc-go's
water-column drain cascade runs ~7t/cell where vanilla's is ~17t, and
its foot spread reaches further; the undo probe sidesteps them by
clearing the whole top. `lightSectionFlipped`'s cached-count
bookkeeping keeps the count stale-by-one (works empirically, worth a
second pass).

## Slice: fluid-sim parity — getNewLiquid ring decay, getSpread slope-distance gating

Measured on 26.2 by decompiling `FlowingFluid`, `FlowingFluid$SpreadContext`,
`LavaFluid`, `WaterFluid` and `LiquidBlock` (javap bytecode + Vineflower):

- `getNewLiquid` has NO strictness check: one horizontal pass counts
  `highestNeighbor` and source-neighbours; `amount = best - dropOff`
  (≤0 → empty). Equal-level neighbours still feed a cell, so a severed
  pool's ring decays through its levels (~tickDelay per step) instead
  of dying on the spot — mc-go's strict-supplier rule killed flows in
  one step, the ~3× drain-rate divergence the light probes surfaced.
- `tick` gates the recompute on `!isSource` only, writes via
  `getSpreadDelay` and self-reschedules on any level change — that
  reschedule is what keeps a decaying ring stepping; `spread` runs on
  every tick regardless.
- `getSpreadDelay` is the tick delay for water; LavaFluid overrides it:
  old/new both non-empty non-falling and new taller → `delay*4` with
  probability 3/4. Water is deterministic.
- `spreadToSides` does not fan out uniformly: `getSpread` scores each
  direction by `getSlopeDistance` — hops from the neighbour to the
  nearest cell a fluid could fall through (`isHole`), bounded by
  `slopeFindDistance` (water 4, lava 2 outside ultrawarm) — and only the
  minimum-score directions get written. Flat ground scores all four
  1000 → equal flood. A same-kind flow competes for the score but is
  never written (`canBeReplacedWith` only allows water-into-water from
  above); a same-kind source is not even a candidate.
- `isHole` = `canPassThroughWall(DOWN)` && (below-fluid same kind ||
  `canHoldFluid(below)`); `canHoldAnyFluid` answers true for cells
  ALREADY holding fluid (fluids never block motion) — so a lava-flowing
  cell below is a hole for water: water seeks lava to harden it, and
  lava seeks water the same way. Its exclusion set (doors, signs,
  ladder, sugar_cane, bubble_column, portals, structure_void) is now
  enforced — mc-go's set wrongly washed sugar cane.
- Source formation: `neighbourSources >= 2 && canConvertToSource` over
  solid ground (or a same-kind source below) merges a full source —
  gated on `water_source_conversion` / `lava_source_conversion`
  gamerules (true/false here, matching vanilla defaults).
- `spreadTo` runs `beforeDestroyingBlock` → `Block.dropResources`: a
  washed block drops its loot (session-less `lootCtx`, no tool/player).

The recompute (`newFluidState`), the side-spread (score-then-write on
the min-distance directions), the down-path (write the below cell's own
recompute — falling, or a merged source), the lava ×4 reschedule and
the loot-drop-on-wash now all live in `internal/server/fluid.go`.

`TestFluidParity` pins all of it on both servers: A flat spread, B the
falling column + landing, C lava's reach, D/E the water↔lava names, and
new scenario F — a settled pool with its source cut, queried mid-decay,
which pins the ring-decay rate rather than only the end state. Unit
side: `TestWaterFallingOnLavaFlowMakesCobblestone` now also exercises
the hole-seeking (water flows toward the cell above the lava source →
obsidian).

Honest residuals: `canPassThroughWall`'s collision-shape check is
approximated open (fluid flows through any traversable cell's wall) —
matters for fences/walls that liquids can't pass in vanilla.
`LiquidBlockContainer.placeLiquid` (waterloggable containers absorbing
flow) is unmodeled. Lava's `canBeReplacedWith` height ≥0.444 water-
overwrite is routed through `wakeLava` instead of a block write — the
net cobblestone result is the same, the in-between ticks differ.

## Slice: waterloggable containers — placeLiquid, chained source feeds

Measured on 26.2 from `FlowingFluid.canHoldAnyFluid` /
`canHoldSpecificFluid` / `isWaterHole` and `LiquidBlockContainer`:

- `canHoldAnyFluid` answers true EARLY for a `LiquidBlockContainer` —
  before the motion check — so every block carrying a `waterlogged`
  property (fences, walls, slabs, stairs, trapdoors, doors, signs,
  chests, panes, chains, scaffolding, candles, ...) is a cell a fluid
  may occupy, even though the block itself blocks motion.
- `canHoldSpecificFluid` → `container.canPlaceLiquid`: water only, and
  only when not already waterlogged. Lava never enters a waterloggable
  cell — it is not a spread candidate nor traversable for lava's slope
  search, so lava flows around waterloggables rather than destroying
  them.
- `spreadTo` → `placeLiquid`: water entering a container writes
  waterlogged=true on the SAME block state — no drops — and the cell
  then reads as a water SOURCE via getFluidState (supplies neighbours
  at full strength, feeds the next cell, counts for infinite-water
  formation, never decays).
- `isWaterHole` counts a non-waterlogged container below as a hole for
  water, so a spread prefers the direction that fills a fence.

mc-go: `fluidOf` reads waterlogged=true (and the intrinsic kelp-family
set) as a water source; `fluidCanHold`/`fluidWritable`/`fluidIsHole`/
the slope search split traversable from writable per kind;
`fluidSpreadTo` fills the container in place instead of the loot+write
path. A waterlogged cell's own fluid tick spreads it like any source —
the chained feed where water re-emerges beyond a filled fence.

`TestFluidParity` scenario G pins it on both servers: a fence and a
bottom slab next to a source come back `waterlogged=true`, the cell
between them holds water, and the slab only fills because the filled
fence re-feeds it. Unit side: `TestWaterloggableFillsAndFeeds`,
`TestLavaCannotEnterWaterloggable`.

Residual: `canPassThroughWall`'s merged-shape occlusion check is
approximated open — for holdable (non-motion-blocking) cells it is
trivially true in vanilla anyway, so the visible fluid rules all sit
in canHoldAnyFluid/canHoldSpecificFluid.
## Slice: saplings → trees — stage counter, two-layers space rule, blob canopy

Measured on 26.2 by decompiling `SaplingBlock`, `TreeGrower`,
`TreeFeature`, `StraightTrunkPlacer`, `BlobFoliagePlacer`,
`TwoLayersFeatureSize` and reading `oak.json`/`birch.json` configured
features plus `replaceable_by_trees`/`cannot_replace_below_tree_trunk`/
`logs` tag JSONs:

- `SaplingBlock.randomTick`: `getMaxLocalRawBrightness(pos.above()) >= 9`
  and a 1/7 roll → `advanceTree`. The brightness read is max(propagated
  sky − skyDarken, block grid) — a cell shaded by one floating block
  still floods in at ~14 from the open columns beside it, so a
  line-of-sight check wrongly starves it.
- `advanceTree`: STAGE 0 cycles silently to 1 (flags 260), stage 1 runs
  the feature. A sapling that can never place still reaches stage 1 —
  the two-step counter is observable without growing a single log.
- `getMaxFreeTreeHeight` loops `y = 0..treeHeight+1` calling
  `getSizeAtHeight`: `y < limit(1) ? lowerSize(0) : upperSize(1)` — the
  trunk column at layer 0, the whole 3x3 at EVERY layer from 1 up.
  An earlier read had the 3x3 only in the top two layers; the real rule
  clips trees beside any clutter at all.
- `TrunkPlacer.isFree` (the space-check gate) is `validTreePos` OR the
  `minecraft:logs` tag — every `*_log/_wood/_stem/_hyphae` state — so
  trunks read as open space but never as write targets.
- `StraightTrunkPlacer`: `placeBelowTrunkBlock` writes dirt under the
  trunk when the cell below isn't in `cannot_replace_below_tree_trunk`
  (dirt/mud/moss/podzol — grass is NOT, so grass roots to dirt); logs
  y=0..treeHeight-1 each gated by validTreePos.
- `BlobFoliagePlacer` (radius 2, offset 0, height 3): the attachment
  sits one above the top log and places FOUR layers at yo = 0..-3 with
  radius `max(2-1-yo/2, 0)` → 1,1,2,2; `shouldSkipLocation` always skips
  corners on the top layer and coin-flips them below.
- `updateLeaves` BFS-seeds DISTANCE from every log in the feature box
  immediately after placement (cap 7).
- `TreeGrower.growTree` clears the sapling cell to the fluid-legacy
  block FIRST (silent), restores it when `feature.place` fails; oak
  rolls `nextFloat() < 0.1` for `Trees.FANCY_OAK` — unimplemented, so
  the roll happens and the residual fails like a space-check failure,
  exactly the visible behaviour when fancy is rolled.

mc-go: `internal/server/tree.go` — `saplingRule` (light ≥9 via
`skyEffective` − `skyDarken`, 1/7), `advanceTree`, `growTree` for the
oak/birch straight+blob features; bonemeal's 0.45 hook sits in
`bonemealGrow`'s new sapling branch. Other species' placers
(spruce/acacia/jungle/dark-oak/mangrove/cherry/pale-oak), 2x2 mega
trees, the beehive `hasFlowers` variants and fancy oak are residuals.

`TestSaplingParity` pins it on both servers: four birch grow with
identical trunk-zone names maps (heights inside 5-7 and the
measured-height leaf cell asserted per leg), and a ceilinged oak sits
at stage=1 forever on both. The sprint drives `random_tick_speed 600`
inside simulation-distance 3 — mc-go's shard-channel block reads make
each 600-speed tick cost ~250ms wall, so the test carries
`sprintTicksLoose`, a sprint poll that out-waits a synchronous
75-second sprint (the shared helper's ten-second game-time query
expires mid-run: the query queues behind the sprint exactly like
vanilla's server thread).

## Slice: leaf decay tail — dropResources, getStateForPlacement, hand-placement

The distance-propagation and decay pipeline was already in
(`leafShape`/`leafDistanceTick`/`leafDecayRule`, measured off
LeavesBlock earlier); this slice finished the two missing halves.

- `LeavesBlock.randomTick` calls `dropResources` before removeBlock:
  a decaying leaf now rolls `blocks/<leaf>` through the shared loot
  roller (no tool context — shears/silk pools skip, leaving
  saplings ~5%, sticks ~2%, apples ~0.5%) and spawns the item
  entities before folding to air.
- `LeavesBlock.getStateForPlacement` forces `persistent=true` and
  runs `updateDistance` AT PLACE TIME: a hand-placed leaf lands
  persistent with the distance computed off its six neighbours
  (1 beside a log, 7 in the open) — previously the item's default
  state went in (persistent=false, distance=7) and player-placed
  leaves crumbled.
- `BlockInput.place` truth re-verified for the command paths: flags
  `2 | (strict ? 816 : 256)` — non-strict runs
  `updateFromNeighbourShapes` on the placed state, so a /setblock'd
  leaf DOES schedule its own distance tick (mc-go's
  `selfCheckSurvival` leaf branch was already correct parity).

Blackbox: `TestLeafDecayParity` now also pins the hand placements —
`/give` + use_item_on on a stone lands `oak_leaves[persistent=true]`
on both servers (distance=1 next to the log), a 16x16 /fill'd patch
of doomed leaves drops a sapling and a stick, and hand leaves never
decay. Harness note for future interaction tests: the spawned mc-go
runs frozen, so the 60-tick `interactionsOpen` grace never lapses —
use_item_on needs an explicit `player_loaded` packet first (other
tests do the same).

All 21 probes hit/hit (chain-mid distance lands miss/miss — the
recompute cascade wrote it already past 3 by probe time on both
legs). Unit: `leaves_test.go` — place state (persistent + distance),
decay loot (225 decays drop a sapling), the persistent/distance<7
decay gates.

## Slice: spruce sapling growth (SpruceFoliagePlacer + forking of space rules)

- `spruce.json` (26.2 configured_feature): StraightTrunkPlacer
  {base 5, randA 2, randB 1} → 5-8 logs (height = base + U(0..randA)
  + U(0..randB) — randB was previously hardwired 0 and is now a real
  shape field). SpruceFoliagePlacer {radius U(2..3), offset U(0..2),
  trunk_height U(1..2)}.
- `SpruceFoliagePlacer.createFoliage` (decompiled): the zigzag cone.
  foliageHeight = max(4, treeHeight - U(1..2)); rows run
  yo = offset..-foliageHeight; the row radius starts at nextInt(2),
  counts up while under maxRadius, then drops to the running minimum
  while maxRadius grows toward the sampled leafRadius. Corner skip is
  `dx == radius && dz == radius && radius > 0` — no random roll like
  the blob's. shouldSkipLocationSigned feeds it absolute dx/dz.
- `treeShape` now carries the whole per-species table: randB, the
  two_layers space rule (minLimit/minUpper), and a canopyKind
  switch (blob | spruce). getMaxFreeTreeHeight runs
  `y < limit ? 0 : upper` — spruce checks the 5x5 from y>=2 vs the
  oak/birch 3x3 from y>=1.
- Guaranteed-leaf pin: four below the attachment
  (y = posY + treeHeight - 3) lands radius >= 1 for every
  (cr0, offset, leafRadius) path, and (x±1, z) are never corners.

Blackbox: `TestSpruceParity` (spruce_test.go) — four spruce grown at
random_tick_speed 600, trunk zone + sapling cells + dirt + stones in
the names map; per leg trunk height in [5,8], the band leaf east of
the trunk, and a crowded spruce (stone inside its 5x5 at y+2) pinned
as a stage-1 sapling. Green vs both servers. Unit:
`TestSpruceCanopyShape` sweeps 200 sampled canopies for the band
invariant.

## Slice: jungle 1x1 growth — jungle_tree_NO_VINE, and the decorator trap

- `TreeGrower.JUNGLE` (decompiled): the 1x1 feature is
  `JUNGLE_TREE_NO_VINE`, not `jungle_tree` — a lone jungle sapling
  grows StraightTrunkPlacer {4,8,0} → 4-12 logs + the shared
  BlobFoliagePlacer, with decorators: [] — no vines, no cocoa.
  The vine'd `jungle_tree` (cocoa 0.2, trunk_vine, leave_vine 0.25)
  is only reachable through biome worldgen in 26.2; the 2x2 mega gets
  `mega_jungle_tree` (trunk_vine + leave_vine 0.25).
- The decorator family IS implemented in `jungleDecorators`
  (CocoaDecorator 0.2 tree-gate + 0.25/log-face on the bottom three
  logs; TrunkVineDecorator nextInt(3)>0 per log face, single-face
  vine states; LeaveVineDecorator 0.25/leaf-face hanging down ≤4
  cells) and wired via `treeShape.jungleDecor` — currently no entry
  sets it; it exists for the future mega_jungle path.
- The differential suite caught the trap before commit: the first
  jungle_test run pinned vine *presence* from the jungle_tree config
  — vanilla grew zero vines, which re-derived the no_vine truth from
  the wire rather than the config file.
- Also fixed in-tree plumbing: trunk cells are now collected into
  `logs` during placeTrunk (the decorators' context.logs()).

Blackbox: `TestJungleParity` — three jungle at 10 spacing grown at
random_tick_speed 600; names map (trunk zone + dirt), per-leg trunk
4-12, the guaranteed blob-canopy leaf beside the trunk top, and the
vine ring scan pinned to ZERO on both legs. NOTE: z=-56 was outside
the sim-distance-3 tick radius — scenario rows must stay within ±48.

## Slice: acacia growth — forking trunk + flat umbrella canopy

- `acacia.json` (26.2 configured_feature): ForkingTrunkPlacer
  {base_height 5, randA 2, randB 2} → 5-9 logs. The column leans one
  horizontal step per level for its top leanSteps(1-3) levels starting
  at leanHeight = treeHeight - U(0..3) - 1; a second staircase branch
  (a different random direction, skipped when the rolls coincide)
  climbs 1-3 logs from leanHeight - U(0..1) - 1. Attachments: main tip
  radiusOffset 1, branch tip 0.
- AcaciaFoliagePlacer {radius 2, offset 0}: per attachment a skirt row
  at attach-1 of radius 2+radiusOffset (3 main / 2 branch) with the
  square's corners skipped, plus a full 3x3 cap at the attach level
  (the measured skip predicate can never fire at radius 1).
- minsize two_layers {upper 2} → the 5x5 space check from y>=1.
- treeShape grew `trunk trunkKind` + `canopy canopyKind` variants and
  `forkingTrunk` hands `foliageAttach`s to the canopy — the same
  attachment list vanilla's placeTrunk produces for createFoliage.
- `treeLeafDistances` now seeds its BFS at the collected written-log
  slice, not the origin column — a leaning trunk leaves the column,
  so distance was previously computed from a stale seed set.
- Bug the differential caught: the attachment liveness check compared
  a world Y against >= 0, so every tree grown below sea level silently
  dropped its foliage (trunk only, no canopy — exactly what the leaf
  count pins flagged).

Blackbox: `TestAcaciaParity` — three acacia at 10 spacing grown at
random_tick_speed 600 plus one crowded by a stone inside its 5x5;
per-leg counts over the grown volume (acacia_log >= 5, acacia_leaves
>= 25), the origin log cell, the rooted dirt, and the crowded sapling
pinned stage-1 on both legs. Also introduced `scanNames`: the
unpaced variant of queryNames (per-position done-marker instead of
if/unless pairs) — count scans over ~800-cell volumes went from
~16 min/leg to seconds since the probe disables the command-spam
threshold anyway.

## Slice: fancy oak + /place feature (FancyTrunkPlacer/FancyFoliagePlacer, clipped-height gate)

Measured on 26.2 (`fancy_oak.json` + FancyTrunkPlacer/FancyFoliagePlacer/
TwoLayersFeatureSize/TrunkPlacer decompiles):

- fancy_oak samples trunk 3+intn(12)+intn(1) (3..14); placeTrunk runs on
  height = clipped+2 with trunkHeight = floor(height*0.618). Foliage
  spots: seed attach at pos+(height-5) anchored to the trunk top, then
  per layer relY = height-5..0 where treeShape >= 0 (dead below 30% of
  the tree, half-chord of the ellipse otherwise) one cluster at radius
  = treeShape*(nextFloat+0.328) on a random angle — kept when the
  upward 6-cell column AND the limb from (x=origin, branchTop) both
  pass isFree; branchTop = min(trunkTop, trunc(attach.Y -
  hypot(dx,dz)*0.381)).
- makeLimb writes steps+1 cells by Java's (int)(0.5+i*d/steps) floor;
  doPlace only writes where validTreePos; per-cell axis = dominant
  horizontal offset from the limb start (x>=z -> x, z -> z, on-column
  -> y) — the branch limbs are the only source of non-y oak_log.
- isFree = validTreePos OR a #logs cell, so limbs may pass through
  earlier logs. trimBranches drops limbs/attachments whose branch base
  is below 20% of the tree (the trunk-top attachment always survives).
- FancyFoliagePlacer {radius 2, offset 4, height 4}: rows yo = 4..0
  with radius 2 on the top/bottom rows and 3 between; circular skip
  (dx+.5)^2 + (dz+.5)^2 > r^2.
- minsize two_layers {limit 0, upper 0, min_clipped_height 4}: the
  space check is column-only (radius 0), capped at sampled+1 cells
  above — an obstacle beyond the sampled height is invisible and the
  tree grows unclipped; otherwise clipped = blockedY-2, and the tree
  still grows when clipped >= 4 (or >= sampled).
- /place feature <configured> now runs real doPlace for the
  implemented trees (fancy_oak, oak, birch, spruce, acacia,
  jungle_tree{,_no_vine}) with vanilla replies
  commands.place.feature.{success,failed,invalid}; unimplemented names
  fail like a real placement that could not run.

Blackbox: `TestFancyOakParity` — three fancy_oak placed at 10 spacing;
per-leg counts (oak_log >= 3, oak_leaves >= 25), >= 1 horizontal-axis
oak_log across the grove (branch limbs), stone at pos+4 -> clipped 2 <
4 -> failed reply on both legs, stone at pos+6 -> clipped 4 -> stubby
clipped tree grows on both legs (the clipped shape itself is
sampling-dependent so only the invariants are pinned). `scanNames`
joins every matching candidate with "+" — horizontal logs read
"oak_log+oak_log[axis=x]"; count via Contains.

## Slice: cherry trees + two_layers limit=1 (CherryTrunkPlacer/FoliagePlacer)

Measured on 26.2 (cherry.json + CherryTrunkPlacer/CherryFoliagePlacer/
FoliagePlacer/TwoLayersFeatureSize decompiles):

- CherryTrunkPlacer {base 7, randA 1, randB 0}: first branch start
  max(0, h-1+U{-4..-3}), second max(0, h-1+U{-4..-4}) bumped past the
  first on collision; weighted 1/2/3 branch count; trunkHeight = h
  (3 branches), max(f,s)+1 (2), or first+1 (1); column y<trunkHeight,
  plus a top attachment at count==3. Each branch: 1-2 horizontal
  steps off the column (2 when middleContinuesUpwards or the end is
  below the start), then a random walk to the end cell stepping
  vertical with probability |dy|/manhattan; horizontal cells take
  the branch direction's axis, vertical keep y.
- CherryFoliagePlacer {radius 4, offset 0, height 5, wide_bottom .25,
  corner .25, hanging .1667, hang_ext .3334}: currentRadius = 3;
  square rows (1,y=2),(2,y=1),(3,y=0) then hanging rows (3,-1),
  (2,-2) whose perimeter drops a leaf at 1/6 under each set cell and
  one more at 1/3, within Manhattan 7 of the cell below the attach.
  Skips: y=-1 edge cells at .25, else corner always on r>2 with
  |dx|+|dz|>2r-2 at .25, or corner .25 on r<=2.
- two_layers limit defaults to 1, not 0 (codec optionalFieldOf):
  oak/birch/jungle {} check the column at y=0 then the 3x3; acacia and
  cherry {upper_size 2} check the column at y=0 then the 5x5 — so a
  block beside the trunk at ground level never blocks growth (cherry
  entry corrected from limit 0).

Blackbox: `TestCherryParity` — three cherries placed at 10 spacing;
per-leg counts (cherry_log >= 3, cherry_leaves >= 30) and >= 1
horizontal-axis cherry_log per tree (the first branch step always
lands off-column — deterministic); stone beside the column at y=0
grows anyway on both legs, stone inside the 5x5 at y=2 fails on both.
## Slice: dark oak — 2x2 quad detection, leaning trunk, fringe stems

Measured on 26.2 (dark_oak.json + DarkOakTrunkPlacer/
DarkOakFoliagePlacer/GiantTrunkPlacer/ThreeLayersFeatureSize/
TreeGrower decompiles):

- TreeGrower.growTree's mega path runs BEFORE the single feature:
  the ticked sapling scans the four quads it can anchor (dx,dz in
  {0,-1}, order (0,0),(0,-1),(-1,0),(-1,-1)), takes the first
  matching 2x2 of same-block saplings, clears all four to air (flags
  260), places the mega feature at the quad's NW corner, restores on
  failure — and a failed quad is terminal (no fall-through to a
  single feature). dark_oak has no single feature, so a lone
  dark_oak sapling can never grow.
- DarkOakTrunkPlacer {base 6, randA 2, randB 1}: dirt under all four
  column cells; a random lean direction with leanHeight=h-intn(4)
  and leanSteps=2-intn(3) (the 2x2 shifts one cell per dy while
  steps remain); each layer writes only when its NW cell is
  isAirOrLeaves (air || #leaves — NOT the vine check). Top
  attachment (leaned, doubleTrunk). Then the fringe: for each cell
  on the ring around the ORIGIN x/z (ox,oz in [-1,2] minus the inner
  4), a 1-in-3 roll drops a 2..4-long stem from ey-1 plus a
  single-trunk attachment.
- DarkOakFoliagePlacer {radius 0, offset 0}: double-trunk rows
  (r2,y-1),(r3,y0),(r2,y1) and 50% (r0,y2); single-trunk rows
  (r2,y-1),(r1,y0). Rows extend +1 on x/z for the double trunk; skip
  rules — the y0 double row holes the 3x3 where both coords are on
  the outer boundary {-r,r,r+1} (SIGNED compare), the single y-1 row
  drops corners, the y1 row drops minDx+minDz > 2r-2 with min
  measured around the +x/+z extension.
- three_layers {limit 1, upper_limit 1, lower 0, middle 1, upper 2}:
  column-only at y=0, 3x3 middle, 5x5 top two layers.
- treeShape's minsize ints refactored to minR func(h,y) with
  twoLayers/threeLayers helpers; mega flag on the shape routes
  growTree through quadGrow.
- Bug found by the slice: isAirOrLeaves compared against
  "minecraft:air" but StateOwner returns bare names — every NW-gate
  check failed and trunks never wrote (only fringe stems, which use
  treeCanOccupy).

Blackbox: `TestDarkOakParity` — three /place'd dark oaks; per-leg
floors (>=20 dark_oak_log, >=60 dark_oak_leaves); stone beside the
column at y=0 grows (limit=1), stone inside the middle 3x3 fails; a
lone dark_oak sapling stays a sapling under random ticks while a
2x2 quad's NW cell becomes a log.

## Slice — mega trees (spruce/pine/jungle) + pale oak + decorators

Oracle: GiantTrunkPlacer, MegaJungleTrunkPlacer, MegaPineFoliagePlacer,
MegaJungleFoliagePlacer, AlterGroundDecorator, TrunkVineDecorator,
LeaveVineDecorator, PaleMossDecorator, TreeGrower decompiles +
configured_feature JSONs, differential /place + random-tick growth.

- GiantTrunkPlacer {13,2,14}: podzol-ready dirt under all four
  belows; per height layer the column cell always writes, the other
  three only when hh < h-1 (top layer = single column cell);
  attachment at origin.above(h) doubleTrunk. Cells via
  placeLogIfFree (isFree — replaceable set, same as vanilla).
- MegaJungleTrunkPlacer {10,2,19}: giant trunk plus the branch
  loop — for branchHeight = h-2-intn(4); >h/2; -=2+intn(4): angle =
  rand·2π, b=0..4 cells at int(1.5+cos·b)/int(1.5+sin·b),
  y=branchHeight-3+b/2; attachment radiusOffset -2 single.
- MegaPineFoliagePlacer {r0,off0,crown U{13..17}/U{3..7}}: rows
  yy=fp.Y-crown..fp.Y, smoothR=radiusOffset+floor(yo/crown·3.5),
  jagged +1 when yo>0 && smooth==prev && yy even. MegaJungleFoliage
  {r2,off0,h2}: leafHeight = doubleTrunk?2:1+intn(2), rows yo=0..-h,
  r=2+radiusOffset+1-yo (widens downward). Both circular placers
  skip dx+dz>=7 || dx²+dz²>r² (SIGNED sum).
- TreeGrower table: spruce mega=MEGA_SPRUCE alt MEGA_PINE at 0.5
  (single spruce is always plain spruce); jungle mega=
  MEGA_JUNGLE_TREE (single=jungle_tree_no_vine); dark_oak & pale_oak
  are mega-only; mangrove 0.85→TALL_MANGROVE (noted, not sliced).
- Decorators measured and wired: AlterGroundDecorator podzol (4
  fixed circles at the compass offsets + 5 random border circles on
  the 8x8 ring, 5x5-minus-corners, placeBlockAt dy=2..-3 against
  #beneath_tree_podzol_replaceable); TrunkVineDecorator per log per
  dir 2/3; LeaveVineDecorator p=.25 + hang≤4; PaleMossDecorator
  hangers (log .4 / leaf .15, descend while air, tip at exit).
- Bug found by the slice: isAir cells compared to "minecraft:air"
  but StateOwner returns bare names — the SAME bug shape as
  isAirOrLeaves — silently killed every vine/moss/podzol write in
  all four decorators (incl. preexisting jungleDecorators).
- pale_oak_sapling was missing from the randtick whitelist.
- Measured gap (noted): PaleMossDecorator's 0.8 ground pass runs the
  nested PALE_MOSS_PATCH vegetation feature — not implemented;
  bonemeal-vs-sapling split (pale_oak vs pale_oak_bonemeal) pinned
  instead.

Blackbox: `TestMegaTreeParity` — /place mega_spruce ×3 (logs≥40,
leaves≥25, podzol≥4), lower_size=1 stone adjacency blocks spruce and
jungle (pale grows beside stone), three 2x2 quads
(spruce/jungle/pale → NW log via random ticks), mega_jungle (logs
≥30, leaves≥80, vines≥5), pale_oak bonemeal variant (moss≥3),
sapling-grown pale quad must have zero moss. Both legs green.

## Slice — pale oak decorators: moss ground patch + creaking heart

Oracle: PaleMossDecorator + CreakingHeartDecorator + VegetationPatch
decompiles (26.2), pale_moss_patch / pale_moss_vegetation /
pale_oak_creaking configured_feature JSONs, differential /place.

- PaleMossDecorator ground pass (0.8): anchors the PALE_MOSS_PATCH
  vegetation feature one cell above the lowest-Y log. Patch: radius
  U{2..4}+1 per axis; corner columns skipped, edge columns kept at
  .75; per column descend through air (<=5) then ascend through solid
  (<=5) to the first air cell; isEmptyBlock + isFaceSturdy(UP) gate,
  depth 1 writes pale_moss_block only on #moss_replaceable (17 names:
  base_stone_overworld + cave_vines + dirt + mud + moss_blocks +
  grass_blocks). Surface cells roll .3 for pale_moss_vegetation
  (moss_carpet 25 / short_grass 25 / tall_grass-lower 10) in the air
  cell above.
- CreakingHeartDecorator (pale_oak_creaking, p=1.0): shuffle logs,
  first log whose six neighbours are all #logs →
  creaking_heart{creaking_heart_state=dormant, natural=true}.
  Candidates are trunk cells hugged by fringe stems on both outer
  sides — ~1 in 3 trees, so the pin is batch-based (16 trees).
- Fixed the decor switch to run the heart after pale_moss; added
  "pale_oak_creaking" to /place feature.
- Note: four decorator air checks compared StateOwner output to
  "minecraft:air" (bare names returned) — killed in the mega-tree
  slice commit.

Blackbox: `TestMegaTreeParity` extended — pale_oak_creaking batch of
16 on a strip pad: heart>=1 per leg (~30%/tree measured on both),
mossBlock>=5, vegetation>=1. Both legs green (v=1,g=3 hearts;
moss 330/364; veg 128/120).

## Slice — mangrove + tall_mangrove (stilt trunk, simulated roots, propagule)

Oracle: UpwardsBranchingTrunkPlacer / MangroveRootPlacer /
RandomSpreadFoliagePlacer / AttachedToLeavesDecorator /
LeaveVineDecorator / BeehiveDecorator / MangrovePropaguleBlock
decompiles (26.2), mangrove + tall_mangrove configured_feature JSONs,
mangrove_logs/roots_can_grow_through + supports_(hanging_)mangrove_propagule
tag JSONs, differential /place + randtick.

- treeShape gains secondary/secondaryChance (getConfiguredFeature's
  secondaryFeature roll — propagule's 0.85 → tall_mangrove before the
  fancy roll), branchStepsMax, trunkOff{Min,Max}; new trunkMangrove /
  canopyMangrove / decorMangrove kinds; mangroveShape +
  tallMangroveShape specs; "mangrove_propagule" in treeShapes;
  /place mangrove + tall_mangrove.
- MangroveRootPlacer: trunkOrigin raises trunk_offset_y U{min..max};
  the gap column must all pass canPlaceRoot (validTreePos ||
  #mangrove_roots_can_grow_through) or the whole feature fails.
  Written set: trunkOrigin.below() + per-4-dir walk seeded at
  trunkOrigin+dir — simulateRoots recursion fails the feature at
  layer 15 or >15 positions in one direction; potentialRootPositions
  by Manhattan width: >5..8 → below[+nextTo.below at .2], >8 → below,
  inside → .2 below else coin nextTo|below. placeRoot: muddy provider
  (axis=y) in mud/muddy_mangrove_roots else mangrove_roots, both with
  the waterlogged fold; above_root .5 → moss_carpet when the cell
  above is air. Runs BEFORE the trunk; no dirt under the stilt
  (UpwardsBranchingTrunkPlacer never calls placeBelowTrunkBlock).
- UpwardsBranchingTrunkPlacer: vertical column; each placed log below
  the top rolls .5 → branch walking a random horizontal dir from
  branchPos=max(0,lenA−lenB−1) (two U{0..1} samples) for
  extra_branch_steps U{1..max}; every step attaches foliage at its
  own cell, and a tip >1 above its base adds +1/+3-up attachments;
  column top attaches at +1. Logs use validTreePos OR
  #mangrove_logs_can_grow_through.
- RandomSpreadFoliagePlacer: 70 attempts per attachment at
  ±(intn(3)−intn(3)) xz, ±(intn(2)−intn(2)) y.
- Decorators: leave_vine refactored to a p-parameter (mangrove .125,
  mega jungle .25 — identical addHangingVine verified on bytecode);
  attached_to_leaves propagule (.14, [down]): shuffled leaves, needs
  two air cells, blacklists the placement ring, writes
  propagule{age U{0..4},hanging,stage:0,waterlogged:false};
  beehive .01: horizontal neighbors of logs at max(leaf0.Y−1,
  log0.Y+1) (or log0.Y+1+U{0..2} capped at last log), first shuffled
  air cell → bee_nest.
- propaguleRule replaces saplingRule for mangrove_propagule:
  hanging → cycles age toward 4, never grows; planted →
  SaplingBlock's 1/7 advanceTree with NO light check.
- Neighbor updates: hanging propagule folds to air when the cell
  above leaves #supports_hanging_mangrove_propagule (={mangrove_leaves});
  planted propagule keeps BushBlock's unconditional canSurvive — pops
  on any neighbor change when the floor leaves
  #supports_mangrove_propagule (#substrate_overworld+farmland+clay).
  Surfaced by the test's fill-order dependence: decorations must be
  counted before their support cells.

Blackbox: new tests/blackbox/mangrove_test.go — TestMangroveParity
places 4 mangrove + 4 tall_mangrove on a dirt pad (roots need ground
to terminate — in open air every chain hits layer 15 and fails, on
both servers), grows 4 planted propagules via randtick, then counts
with `fill air replace` (O(1) per type vs per-cell scans; replies
parsed as commands.fill.success components). Floors per leg:
logs>=5, tallLogs>=8, roots>=3, leaves>=30, prop>=1, vine>=1,
carpet>=1; failures<=2 (root-chain layer-15 fails are legit on both).
Green both legs (v: logs 28/78, roots 232, leaves 1934, prop 42,
vine 772, carpet 55; g: 29/66, 221, 1645, 40, 1168, 36). Vine count
ran ~1.5x over vanilla this run — leave-vine mechanics verified
identical to bytecode (hang = first + up to 4 below), chalked to
rng/hang compounding; not pinned.

## Slice — TreeGrower hasFlowers → BEES_005 variants

Oracle: TreeGrower.getConfiguredFeature + BeehiveDecorator +
TreeDecorator$Context decompiles (26.2), *_bees_005 configured_feature
JSONs, #flowers + #small_flowers + #replaceable_by_trees tag JSONs,
differential grown-sapling + /place.

- getConfiguredFeature draws ONE roll: below secondaryChance the
  secondary spec wins (oak→fancy_oak, mangrove→tall_mangrove), else
  the primary — then a #flowers block anywhere in the 5x3x5
  (x±2, y−1..+1, z±2) around the sapling swaps the chosen spec for
  its BEES_005 variant: identical trunk/foliage/minsize with only
  [beehive 0.05] decorators. Oak's secondaryFlowers is
  fancy_oak_bees_005 (fancy + .05). birch/cherry grow
  *_bees_005 directly (secondaryChance 0). New oakBees/birchBees/
  cherryBees specs + decorBeehive(beehiveP); flowersTag = #flowers
  (small_flowers + tall + azalea/propagule/cherry_leaves/pink_petals/
  wildflowers/chorus/spore/cactus); /place routes all four
  *_bees_005 names.
- BeehiveDecorator picks leaves.get(0)/logs.get(0)/logs.last() from
  the placement SETS — Sets.newHashSet() copied to ObjectArrayList:
  iteration is HashMap bucket order (spread h^(h>>>16),
  Vec3i.hashCode = x+31y+961z). Endpoints are arbitrary cells, so
  javaSetOrder emulates first/last bucket entries. Without it
  leaves[0] was always the first-written top leaf, the nest row sat
  inside the canopy, and nests could never place — the blackbox diff
  surfaced this as vanilla=4 nests vs mc-go=0; emulated run lands
  ~equal rate (9/200).

Blackbox: new tests/blackbox/beehive_tree_test.go —
TestBeehiveTreeParity grows two birch grids (sapling columns at even
x, dandelion columns — flowers need soil — at odd x) vs a flowerless
control, plus /place of all four BEES_005 names. Pins: nests>=1 in
the flowered run, nests==0 in the control, log floors everywhere.
Green both legs (nests v=4 g=1, plain 0/0, flower logs v=787 g=878,
placed v=35 g=45).

## Slice — azalea tree (BendingTrunkPlacer) + bonemeal-only bush growth

Measured from the 26.2 jar (javap) + configured-feature JSONs:
- azalea_tree.json: BendingTrunkPlacer{base 4, randA 2, bend_length
  U{1..2}, min_height_for_leaves 3}, RandomSpreadFoliagePlacer
  {radius 3, offset 0, foliage_height 2, leaf_placement_attempts 50},
  weighted foliage azalea_leaves:flowering_azalea_leaves = 3:1,
  below_trunk_provider rooted_dirt, decorators [].
- BendingTrunkPlacer.placeTrunk (javap): one horizontal direction up
  front; the column rises treeHeight−1 levels, each stepping sideways
  when level+1 < branchesHeight+U{0..1} BEFORE placeLog (the origin
  cell is never a log — a grown bush keeps its azalea at origin);
  validTreePos gates each write; foliage attaches at levels >= 3;
  then a side bend walks bend_length+1 more cells, attaching each.
- AzaleaBlock: isBonemealSuccess is the same 45% roll as saplings —
  a miss still consumes the meal (growCrop shrinks unconditionally
  once the target validates); performBonemeal then calls
  TreeGrower.AZALEA.growTree unconditionally. Bushes have no stage
  and never grow via random tick.
- RandomSpreadFoliagePlacer shared with mangrove: xz ±(intn(r+1) −
  intn(r+1)), y ±(intn(h) − intn(h)) — azalea {r 3, h 2, 50 attempts}
  vs mangrove {r 2, h 2, 70 attempts}; mangrove canopy generalized
  into randomSpreadCanopy.
- placeBelowTrunkBlock writes rooted_dirt (not dirt) when the
  substrate isn't in #cannot_replace_below_tree_trunk — grass pads
  root, dirt pads don't.

Blackbox: tests/blackbox/azalea_test.go — TestAzaleaParity /places
four trees on grass and bonemeals a bush until rooted appears under
it (<=10 clicks; P(10 misses)≈0.0025). Floors both legs: oak_log>=4,
azalea_leaves>=30, flowering>=3, rooted_dirt>=2, bush grew==true.
Surfaced a harness fact: vanilla drops use_item_on when the client
never confirmed the teleport — newChatSinkTP (teleport_confirm +
pos echo) is required for reach-gated packets. Green: logs v=35
g=38, leaves v=297 g=423, flowering v=119 g=158, rooted v=5 g=5,
bushGrew true/true.

## Slice — rooted_azalea_tree (RootSystemFeature)

Measured from the 26.2 jar (javap) + rooted_azalea_tree.json:

- place(): origin must be air. placeDirtAndTree walks the probe UP
  from origin+1 (<=root_column_max_height 100), each iteration
  checking getHeight(WORLD_SURFACE) < probe.y → abort when the probe
  rises above the surface. The first probe passing all three checks
  places the tree:
  - allowed_tree_position (all_of): cell in #air or
    #replaceable_by_trees AND cell.below in #azalea_grows_on
    (substrate_overworld + sand + terracotta + snow + powder_snow).
  - spaceForTree: cells +1..+3 pass isAllowedTreeSpace — air, or
    water only at +1 (i+1 <= allowed_vertical_water 2).
    level_test_distance 0 skips the lateral level test.
  - below must be non-lava fluid AND solid.
  Then azalea_tree places at the probe; on success placeDirt runs
  20 rooted_dirt scatter attempts per level y∈[origin.Y, origin.Y+i)
  inside ±3xz of the origin column through
  #azalea_root_replaceable (base_stone + substrate + terracotta +
  red_sand + clay + gravel + sand + snows); placeRoots then drops
  hanging_roots into 20 empty-cell attempts inside ±3xz/±2y whose
  above is face-sturdy DOWN.
- /place rooted_azalea_tree routes to rootedAzaleaFeature; skyTop+1
  stands in for the WORLD_SURFACE heightmap; speleoSturdy stands in
  for face-sturdy.

Blackbox: tests/blackbox/rooted_azalea_test.go —
TestRootedAzaleaParity builds a layered pocket (stone ground, two
stone roofs over air bands, dirt tower) so the probe walks the full
column: roots eligible ≈70 cells → ~6 expected. Floors both legs:
oak_log>=4, azalea_leaves>=20, rooted_dirt>=30, hanging_roots>=1.
Green: logs v=7 g=6, leaves v=58 g=52, rooted v=93 g=90, roots v=2
g=7.

## Slice — fire spread/decay blackbox pin

FireBlock.tick was already implemented end-to-end (reschedule 30-40,
canSpreadFireAround gamerule, canSurvive pop, infiniburn, rain quench,
age+nextInt(3)/2, invalid-location decay, checkBurnOut bases
300/250, spread box x±1 z±1 y[-1,4] with height-scaled threshold and
(odds+40+difficulty*7)/(age+30) roll, getStateWithAge face folds) —
this slice pins it differentially: an infiniburn netherrack source
beside a 35-plank wall, ~1000 sprinted ticks, floors fire>=3 cells
and planks<35 per leg. Green: cells v=6 g=10, planks left v=1 g=7.

## Slice — non-living /summon coverage

The entity registry gap closed: all 24 non-living EntityTypes
vanilla's EntityType.create accepts now summon (arrow arm extended to
spectral_arrow + trident; summonThrownNames for the seven throwables;
summonPlainNames for the rest). Per-tick fates measured from 26.2
bytecode and run in ephemeralTickStep:

- item: empty-stack ItemEntity.tick discards tick 1 (bare summon =
  air stack).
- fishing_bobber: getPlayerOwner()==null → discard tick 1.
- evoker_fangs: warmup 0, life 22 → dealDamage at warmup==-8 (6.0
  indirect_magic, AABB±0.2 horiz) → discard ~tick 23.
- eye_of_ender: life>80 → discard (~80% drop ender_eye item).
- ominous_item_spawner: spawnItemAfterTicks stays 0 on the summon
  path (the 60-120 roll only runs in the static create() factory) →
  kill tick 1; spawn_item_after_ticks NBT honored.
- painting/item_frame/glow_item_frame: checkInterval-100 survives()
  pass — bare summons face south, so only a sturdy -Z neighbour
  saves them (a block below doesn't count); pop drops the item.
- leash_knot: existing knotTickStep now drops the lead item on pop
  (vanilla dropItem behaviour).
- falling_block: gravity 0.04/tick → lands as its BlockState (sand
  default, BlockState.Name honored) where the cell below stops being
  replaceable; out-of-world drops the item.
- llama_spit joined projectileTickTypes: gravity 0.06, discard on
  block hit; ownerless entity-hit does no damage (spit damage needs
  a living owner).

Blackbox: tests/blackbox/summon_coverage_test.go —
TestSummonCoverageParity summons all 25 types (24 + arrow control),
checks summon.success + presence probes, sprints 140 ticks, and pins
the fates: thrown/gone set, hanging pops at 100, fence knot
persists, falling_block becomes sand. Green both legs.

## Slice — noSummon reject set + item air-stack accept

The registry sweep's tail end. Measured: EntityType.Builder.noSummon
fires exactly twice in EntityTypes' clinit — player (sized 0.6×1.8,
eye 1.62) and fishing_bobber (0.25×0.25) — and the refuse reply is
the red translatable entity.not_summonable with `with:
["minecraft:<name>"]`, not the summon.failed key. mc-go previously
accepted fishing_bobber and answered player with "Unknown entity";
both now return componentReply(commandError(entity.not_summonable))
in runSummon and in `execute summon`. Separately, a bare
`/summon item` is accepted by vanilla (ItemEntity spawns holding the
air stack — "Summoned new Air" — and its own tick discards it next
tick), so the dedicated item arm no longer rejects empty stacks.
TestSummonCoverageParity now also pins both not_summonable rejects
plus presence pins for the already-summonable NBT-fed types
(tnt{fuse:30000}, experience_orb{Value:7}, area_effect_cloud,
marker, mannequin). Green both legs.

## Slice — encryption / online-mode login

Vanilla (javap on ServerLoginPacketListenerImpl + ClientboundHelloPacket
+ ServerboundKeyPacket, 26.2): with online-mode=true the login stage
answers Login Start with encryption_request 0x01 = Utf("") +
ByteArray(PKIX RSA-1024 pubkey) + ByteArray(Ints.toByteArray(nextInt),
4 bytes) + Bool shouldAuthenticate; the key response is two
ByteArrays (RSA-PKCS1v15 secret + challenge echo) where a challenge
mismatch is "Protocol error"; the session id is
BigInteger(SHA1(""+secret+pubDER)).toString(16); both directions then
switch to AES/CFB8 (key==IV==secret) before compression and
Login Success, and hasJoined supplies the real profile UUID +
properties.

mc-go: Server.OnlineMode, lazy 1024-bit keypair (PKIX DER — the diff
caught a raw-PKCS#1 mismatch the Java getEncoded() format rules out),
the same hello, challenge check + "Protocol error" disconnect, a
hand-rolled CFB8 stream (Go's crypto/cipher CFB is full-width only)
proven byte-exact against the JDK's own AES/CFB8/NoPadding, and the
hasJoined lookup (HasJoinedURL overridable for tests) whose profile
UUID + properties land in Login Success; compression negotiates
inside the encrypted stream like vanilla.

Tests: internal/server TestOnlineModeLogin drives the full flow
end-to-end against an httptest session server (hello shape, key
exchange, encrypted compression+success, property round-trip) and
TestOnlineModeBadChallenge pins the Protocol-error disconnect;
protocol TestCFB8MatchesJDK pins the cipher against JDK-produced
ciphertext; tests/blackbox TestEncryptionHelloParity compares the
encryption_request shape field-by-field against real vanilla with
online-mode=true. The real-Mojang lookup is the one piece no
differential can cover (needs a paid account); the stubbed path pins
everything else.

## Slice — region save: light and biome parity

- Measurement: dumped vanilla's saved `.mca` (javap
  `SerializableChunkData$SectionData` + live NBT): `SkyLight`/`BlockLight`
  keys appear iff the section's `DataLayer.data != null` — a real write
  pass, not mere eligibility; `isLightOn=1` iff the chunk was lit.
- Sky top semantics decoded via the actual 26.2 jar run as a library:
  `propagatesSkylightDown` dumped per block from `BuiltInRegistries.BLOCK`
  (727 blocks propagate: glass, slabs, stairs, torches, fences...).
  The sky beam passes propagating cells at 15; the first
  non-propagating cell is the top and stores `15-opacity` — leaves
  count (14), glass doesn't (15). Fluids don't propagate either, so
  water/lava still raise the top. `lightOpacity` lava 255→1 (liquid
  dampening 1 like water). `blockskylight_gen.go` generated table.
- region.go: `chunkNBT`/`sectionNBT` write `SkyLight`/`BlockLight`
  byte arrays (2048 nibble-packed) keyed on the `skyLayers`/`blkWritten`
  layer maps; `isLightOn` set when a light provider answers.
- advancements race fix: `flushAdvancements` read `advDone`/`advSent`
  outside `advMu` while award paths mutated them — crashed the game
  loop (`fatal error: concurrent map read`). Now holds the lock for
  the whole function.
- Test fixes: the scratch world never `SetBiome`d so the region compare
  read `plains` vs vanilla's `the_void`; vanilla `forceload`+`/setblock`
  raced async chunk load (leaves occasionally never placed) — placement
  now confirmed via log markers with reissue.
- `TestWorldSaveParity` all green: chunk_format + both directions,
  5 light arrays byte-equal.


## Slice — entity move: bed restitution

- Measurement (26.2 jar as a library + Entity.move bytecode):
  `getBounceRestitution` per registered block is {slime_block 1.0,
  all 16 beds 0.75} — mc-go's table had slime only. `suppresses_bounce`
  block tag confirmed honey_block only. `getBlockBounciness` damps the
  restitution 0.8x for non-living entities; `restituteMove`'s
  gate order (entity bounce → x/z reflect → vCollBelow +
  -dy<gravity + SUPPRESSES_BOUNCE + sneak → bounce=0, else
  max(bounce, blockBounciness)) was already at parity.
- BedBlock SHAPES = box(0,0,0,3,3,3) leg + column(16,3,9) mattress →
  collision top 9/16; added every _bed to `collisionTopOf` (0.5625)
  and `blockBounceOf` (0.75).
- `TestItemPhysParity` extended: red_bed pad, item leg (0.8x-damped
  bounce, apex band -57.9, rest y -59.44 matches byte-exact), pig leg
  (living: full 0.75, apex band -56.9 past the item's reach).
  Green vs both servers (234s).


## Slice — packet: teleport_entity (0x7D) for mounted teleports

- Census (`vanilla/reports/packets.json` as oracle): play phase = 141
  clientbound + 69 serverbound ids; `IDPlayEntityTeleportCB = 0x23` is
  entity_position_sync (mislabeled but right id), `teleport_entity` =
  0x7D — the 12 uncovered names were waypoint, set_simulation_distance,
  tab_list, select_advancements_tab, store_cookie,
  resource_pack_push/pop, server_links, custom_report_details,
  projectile_power, test_instance_block_status, teleport_entity.
- Wire (javap STREAM_CODEC): VarInt entityId + pos 3×double + delta
  3×double + yRot/xRot floats + int32 relatives + bool onGround.
- Emit site (Entity.sendTeleportTransitionToRidingPlayers): only to
  riding players of a teleported mount — getIndirectPassengers walk.
  The controlling passenger gets PMR.of(transition) + the command's
  relatives set; every other rider gets PMR.of(mount) + empty set.
- getControllingPassenger per type (javap): base Entity returns null;
  AbstractBoat = first passenger when living; Pig = saddled + Player +
  holding carrot_on_a_stick; Strider = saddled + warped fungus stick;
  AbstractHorse (incl. Camel) = saddled + Player; HappyGhast = body
  armor + Player; Nautilus = saddled + Player. Minecarts and all other
  mobs have none — an unsaddled pig's riders all see rel=0 (measured).
- getRelatives (TeleportCommand): direction() bits for every ~ or ^
  axis + position() bits when isDelta = same-dimension + rotation() —
  the whole ROTATION set when the command carries no rotation arg.
  performTeleport delta-encodes each position-relative axis (pos −
  entity.pos) and wraps rotation deltas — the packet's pos vec is a
  delta, not the absolute target, on ~-relative axes.
- calculatePassengerTransition collapses: per axis the rider lands at
  old + (transition − mount.old) whether the axis is relative or not
  (relative arms compose client-side to the same outcome); rot slides
  the same. Implemented once in tpPassengerTransition.
- Boat parity found while pinning: seats were centered — vanilla boat
  attach = (0, height/3, z): z = singlePassengerXOffset (0) for one
  rider, +0.2 front / −0.6 rear once two ride (+0.2 more for Animal).
  Attach y = 0.1875 puts riders at boat.y − 0.4125 (measured). Boats
  also never latched Grounded — added boatGroundedAt (solid top under
  box bottom) so onGround reads true once landed.
- `TestMountTeleportParity` (oak_boat, driver + rear rider): tp
  absolute + `~ ~1 ~` probes diff the raw 0x7D bodies (pos/delta/rot/
  rel/og) + Pos reads — green vs both servers.
- Known approximation: rider players still get absolute
  player_position syncs; vanilla emits the relative-encoded form —
  outcome-identical, wire-shape noted.


## Slice — packet: select_advancements_tab (0x55) on seen_advancements

- javap: ServerGamePacketListenerImpl.handleSeenAdvancements only
  acts on OPENED_TAB — resolves the id via ServerAdvancementManager
  (null holder = skip entirely) and calls
  PlayerAdvancements.setSelectedTab. setSelectedTab keeps the holder
  iff value().isRoot() && display().isPresent(), else stores null, and
  emits ClientboundSelectAdvancementsTabPacket (writeNullable id) only
  when the stored selection changed. CLOSED_SCREEN is a no-op —
  lastSelectedTab survives closing the screen. mc-go's old comment
  "vanilla echoes nothing" was wrong.
- Root/display detection reuses the AdvDefWire table — the
  update_advancements wire's hasParent=false + hasDisplay=true pair is
  exactly isRoot && display present (5 selectable tabs:
  adventure/end/husbandry/nether/story roots; recipes/root is a
  display-less root that clears the selection).
- handleSeenAdvancements rewritten: tab id only read for OPENED_TAB
  (matches the SB codec), lastAdvTab tracked per session,
  select_advancements_tab emitted on change. The ProtoIDs field and
  test pin already existed — only the emit was missing.
- `TestSelectAdvancementsTabParity`: select / re-select / display-less
  root / unknown id / close / re-open-same probes diff the raw 0x55
  bodies — green vs both servers.
## Slice — packet: projectile_power (0x87) bundled motion emit

- ServerEntity.sendChanges (decompiled): inside the gate-open body the
  motion emit runs `if (needsSync || trackDelta || LivingEntity &&
  isFallFlying)` — delta drift vs lastSentMovement > 1e-7 (or >0 with
  zero current delta) → for AbstractHurtingProjectile sends
  ClientboundBundlePacket{delimiter, SetEntityMotion,
  ProjectilePowerPacket(id, accelerationPower), delimiter}; every
  other type gets the bare 0x65.
- EntityType.trackDeltas() is an EXCLUSION list (javap iconst_0 on
  match): PLAYER, LLAMA_SPIT, WITHER, BAT, ITEM_FRAME,
  GLOW_ITEM_FRAME, LEASH_KNOT, PAINTING, END_CRYSTAL, EVOKER_FANGS
  return false; every other type — hurting projectiles included —
  runs the drift check at every gate-open. mc-go's trackDeltaTypes
  map was inverted: fixed to `!trackDeltaTypes[tn]`.
- Outer gate = `tickCount % updateInterval == 0 || needsSync ||
  entityData.isDirty` — JDI-confirmed updateInterval=10 on a live
  WindCharge tracker; needsSync latches unconditionally in
  Entity.load (summon AND /data merge), Projectile shadows it with an
  inert field of the same name.
- lastSentMovement latches in the ServerEntity constructor = first
  pair-time delta, so a summoned already-moving projectile emits the
  bundle at its first gate-open (mc-go now latches SentMotion* in
  latchSyncBaseline).
- accelerationPower: public field, NBT `acceleration_power`
  (getDoubleOr 0.1, putDouble always); applyInertia adds
  normalize(delta)*power then multiplies inertia (0.95 base, 1.0
  wind/breeze wind charge, 0.8 liquid, 0.73 dangerous wither skull);
  onDeflection(true)=0.1, (false)=*0.5.
- mc-go: hurtingProjectileTypes (6: fireball, small_fireball,
  dragon_fireball, wither_skull, wind_charge, breeze_wind_charge),
  AccelPower field + NBT round-trip, applyHurtingInertia per tick,
  broadcastHurtingMotion emits the bundle shape.
- `TestProjectilePowerParity`: bundle shape pin (each 0x87 bracketed
  {delim, 0x65 same-eid, delim}) + union of power values {0.005,
  0.02, 0.1} across summon/merge windows — emit counts and window
  placement are jittery (gate-boundary alignment, range exits,
  deflections); green vs both servers.
- Deferred: liquid inertia 0.8 and dangerous-skull 0.73 unmodelled,
  onDeflection semantics unimplemented. (sendChanges conversion done —
  see the projectile-sync slice below.)

## Slice — packet: tracked_waypoint (0x8A) locator bar

- Wire (javap ClientboundTrackedWaypointPacket + live probe):
  op VarInt (TRACK=0, UNTRACK=1, UPDATE=2) + Either<UUID,String>
  identifier (bool 1 + 16-byte UUID for entity transmitters) +
  Icon{style ResourceLocation string ("minecraft:default"),
  optional int32 RGB} + VarInt type (EMPTY=0, VEC3I=1, CHUNK=2,
  AZIMUTH=3) + contents (VEC3I: 3 VarInts; CHUNK: 2; AZIMUTH:
  float32 angle). UNTRACK carries the bare identifier.
- ServerWaypointManager (javap): connections table
  receiver->transmitter; addPlayer/trackWaypoint create conns,
  removePlayer/untrackWaypoint drop + UNTRACK; per tick
  updatePlayer/updateWaypoint run updateConnection (not broken ->
  update(); broken -> remake TRACK or disconnect UNTRACK) then
  createConnection for unconnected transmitters.
- Kind selection (LivingEntity.makeWaypointConnectionWith): empty on
  firstTick/self/ignore; AZIMUTH iff distanceTo > 332, else CHUNK
  iff source chunk not in the receiver's tracking view, else VEC3I.
- Tracking view = receiver chunk centre, radius
  clamp(requestedViewDistance, 2, serverViewDistance); visibility is
  CHESSBOARD <= radius (measured: chunk (2,2) VEC3I, (3,x)/(x,3)
  CHUNK at radius 2 — the squared-sum bytecode formula did not
  match; chessboard does).
- doesSourceIgnoreReceiver: receiver spectator -> visible; else
  ignored iff source spectator, receiver is an indirect passenger,
  or distanceTo >= min(transmit_range, receive_range). Transmitters
  are entities with waypoint_transmit_range > 0 (players 6e7, mobs
  0 default) — /attribute base set flips a mob into a transmitter.
- isBroken per kind: VEC3I manhattan(lastPos, blockPos)>1 or ignore;
  CHUNK chessboard(lastChunk, chunkPos)>1 or ignore or the STORED
  lastChunk now visible; AZIMUTH ignore or chunk visible or dist
  <= 332. Broken -> remake (fresh TRACK) or UNTRACK.
- Emitted angle = atan2(diff.x, -diff.z), diff = receiver - source;
  UPDATE gates |delta| > 0.008726646 rad (0.5 deg). Verified on the
  wire: B at x=400 emits atan2(-400,0) = -pi/2 = -1.5708 rad.
- mc-go: waypoint.go — per-receiver conn table ticked once per
  GameTick (waypointTickStep), wpCreateConnection/wpUpdateConnection
  mirrors, icon {minecraft:default, /waypoint-modify overrides},
  gamerule minecraft:locator_bar gate, firstTick on Entity cleared
  by ClearWaypointFirstTicks, player sources read live session pos.
- TestWaypointParity: join TRACK, VEC3I move, CHUNK flip at
  chessboard 3, AZIMUTH at >332 with -pi/2 angle, sub-epsilon
  silence, UPDATE azimuth on drift, VEC3I re-flip, mob silent at
  transmit 0 then TRACK after /attribute base set, UNTRACK on
  disconnect and on /kill — green vs BOTH servers.
- Deferred: team-color icon assignment (getTeam color) unmodelled —
  players on a colored team still emit no color.

## Slice — waypoint icons: 3-byte RGB, team colors, remakeConnections

- Icon wire color is ByteBufCodecs.RGB_COLOR = THREE bytes, not an
  int32 — mc-go's encoder was writing Int (a wire bug hidden because
  uncolored icons dominated); test decoder fixed to match the live
  vanilla bytes (01 ffaa00 + kind).
- cloneAndAssignStyle (Vineflower on Waypoint$Icon): explicit icon
  color wins; else the entity's team color — BLACK maps to -13619152
  (0x303030) so it stays readable. Team lookup is by scoreboard name
  (player name / dashed-UUID for mobs).
- ServerScoreboard hooks (javap): addPlayerToTeam and
  removePlayerFromTeam call updatePlayerWaypoint(name) ->
  ServerWaypointManager.remakeConnections(player); onTeamChanged
  calls updateTeamWaypoints(team) -> remake per online member.
  getPlayerByName resolves ONLY online players — mob/free-form
  members never remake.
- remakeConnections -> createConnection per receiver: self and
  locatorBar-off skip; a make result Table.put-REPLACES the existing
  conn (silent drop, no UNTRACK) + TRACK emit; an empty make removes
  the old conn + UNTRACK. Team-change re-TRACKs carry the re-cloned
  icon.
- mc-go: wpIconFor evaluates the team color (wpScoreboardName holder
  key, black lift), wpRemake{Connections,For,ByName,Team} wired into
  teamJoin (remove+add sides), teamDrop/teamLeave, and every /team
  modify success — emission shape identical to vanilla including the
  two-TRACK re-join window.
- TestWaypointParity W7b: join gold -> TRACK vec3i 0xFFAA00; re-join
  green -> remove-side no-color TRACK + add-side 0x55FF55; black ->
  0x303030; reset and leave -> no-color TRACKs — green vs BOTH.

## Slice — projectile sendChanges conversion + AbstractArrow PosRot + NoGravity

- Non-hurting projectiles emitted broadcastEntityTeleport every tick
  (wrong packet, ~20x traffic); every non-firework projectile now runs
  mobSyncFlush = vanilla ServerEntity.sendChanges: pos/motion at the
  updateInterval cadence (arrow/trident 20, thrown 10) plus the
  drift-gated set_entity_motion emit.
- sendChanges emit order (javap): the pos packet is BUILT first but
  SENT after the motion emit — motion (or the hurting bundle) precedes
  Pos/PosRot/teleport on the wire within one call.
- instanceof AbstractArrow quirk: arrow/spectral_arrow/trident emit
  PosRot on EVERY outer-gate tick unconditionally — even zero-delta —
  the Pos/Rot selection is skipped for them entirely.
- Tracker flush sees last-tick state: vanilla call0 (outer gate
  tickCount%interval==0) emits before the entity has ticked, so the
  first-call drift is 0 and only the pos packet goes out. mc-go now
  flushes at tick head (before physics), and stuck arrows keep
  flushing (their PosRot gates still fire).
- NoGravity: summon SNBT was parsed for mobs and thrown items but
  never on the arrow/spectral/trident path — now parsed; the
  projectile tick skips gravity when set while air drag still applies
  (vanilla same).
- TestProjectileSyncParity: NoGravity + gravity arrows over equal
  windows pin per-family counts — identical {pos:0 moves:2 motion:1
  teleport:0} on both legs, PosRot-only (no move_entity_pos for an
  arrow), zero teleports inside the sub-8-block window. TestThrownHitParity
  vanilla leg flake noted (egg spread miss, preexisting — mc-go leg
  green).
- Deferred: entity.syncPosition tickCount rounding edge — the pos
  gate (SyncCount+1)%60 vs javap tracker-tickCount%60 — for
  non-arrow types a fresh tracker would emit a zero-delta pos at call0
  in vanilla; unmeasured.

## Slice — hurting-projectile inertia + fluid skip + dangerous skull

Measured on the extracted 26.2 jar (javap `-c` of
`AbstractHurtingProjectile`, `WitherSkullEntity`,
`AbstractWindCharge`, `Entity.baseTick`, `ServerEntity.sendChanges`)
plus live packet probes on the vanilla server:

- `applyInertia` semantics: `if isInWater() { spawn 4 BUBBLE
  particles; inertia=getLiquidInertia() } else
  inertia=getInertia()`; final `delta =
  (delta + normalize(delta)*accelerationPower) * inertia`.
- Inertia table: base `getInertia`=0.95 / `getLiquidInertia`=0.8;
  wind charges use 1.0 for both; `WitherSkullEntity` overrides
  `getInertia` to `isDangerous() ? 0.73 : 0.95` but keeps
  `getLiquidInertia`=0.8 — a dangerous skull is *faster* in water
  (asymptote 0.4) than in air (0.27).
- `isInWater`/`wasTouchingWater` refresh: `baseTick` runs
  `updateFluidInteraction()` per tick over the entity-type AABB
  deflated 0.001 — skulls/wind charges are the 0.3125 cube, the
  fireballs 1x1.
- `ClipContext.Block.COLLIDER` skips fluids — a hurting
  projectile's `arrowBlockHit` must not count water/lava cells as
  block hits.
- `AbstractHurtingProjectile.tick` discards an ownerless hurting
  projectile the tick its position lands outside every loaded
  chunk — on a dedicated server, loaded == player-ticketed
  (streamed window + the ticket ring's border chunk). mc-go tracks
  no projectile shooter so shots read as ownerless too.
- `WitherSkullEntity`'s `dangerous` NBT byte round-trips through
  summon, save/load, and entity-metadata index 8 on the wire; the
  metadata only emits when dangerous=1.
- Drift-emit cadence: `sendChanges` emits the
  {set_entity_motion, projectile_power} bundle when
  `distanceToSqr(lastDelta, delta) > 1e-7` — i.e. |Δv|>3.16e-4 —
  while the outer gate is `tickCount%updateInterval==0` (10 for
  wither_skull). Water lanes emit exactly 3 bundles (~ticks
  10/20/30) before convergence drops drift under the floor; air
  lanes emit until untracked past the 64-block client-tracking
  range (wither_skull `clientTrackingRange`=4).
- join paths must send `client_information` — without it vanilla's
  default view distance 2 makes entities untrack past 32 blocks.

Pinned end-to-end: `TestHurtingInertiaParity` summons six
wither_skull lanes (water/air x dangerous/plain x moving/still)
and compares every set_entity_motion+projectile_power bundle —
emit counts and per-emit values — between vanilla and mc-go, plus
the `dangerous` metadata flag and NBT round-trip.

Deferred: `onDeflection` semantics; dangerous-skull
`getBlockExplosionResistance` (`min(0.8f, ...)`) and
`isOnFire`/`shouldBurn` overrides; `Entity.syncPosition`'s
tickCount%60 zero-delta emit; the view-distance-default hazard in
other join paths suite-wide; `sendWindow`'s ~3.2 s synchronous
light-init on the packet-handler thread (perf gap vs vanilla's
async pipeline — it stalls command processing, which is why the
test drains with `dataGetLine`).

## Slice — projectile deflection + owner/leftOwner/hasBeenShot tail

Measured (javap `Player.attack`, `Projectile`,
`AbstractHurtingProjectile`, `ProjectileDeflection` + generated
entity_type tag JSONs):

- `Player.attack` → `deflectProjectile`: the target must be in the
  `redirectable_projectile` tag ({fireball, wind_charge,
  breeze_wind_charge}) and a `Projectile` — then
  `deflect(AIM_DEFLECT, player, EntityReference.of(player), true)`
  and the attack returns before damage math. AIM_DEFLECT sets
  delta := attacker look angle; `deflect` also calls
  `setOwner(attacker)` then `onDeflection(true)` — for hurting
  projectiles that resets `acceleration_power` to 0.1 (flag=false
  halves it). Success plays PLAYER_ATTACK_NODAMAGE.
- `hitTargetOrDeflectSelf` (entity-hit arm, AbstractHurting-
  Projectile.tick and AbstractArrow.tick — NOT Throwable-
  Projectile): if the hit entity's `deflection()` != NONE — only
  breeze carries `deflects_projectiles` — and it isn't the last
  deflector, `deflect(REVERSE, entity, owner, false)`:
  delta *= -0.5, yRot += 170+rand*20, lastDeflectedBy = entity.
- `Projectile.hurtServer` always returns false, so punching a
  non-redirectable projectile (skull, arrow, throwable) falls
  through to the swing's failed-hurt arm: PLAYER_ATTACK_NODAMAGE
  plays on the punch too.
- `Projectile.tick` head: `hasBeenShot` latches on the first tick;
  `checkLeftOwner` latches `leftOwner` once the projectile box —
  expanded along delta and inflated by 1 — clears the owner's
  root-vehicle CAN_BE_PICKED boxes (ownerless → latched tick 1).
- `Projectile.addAdditionalSaveData`: `Owner` [I;] uuid ints while
  set, `LeftOwner` bool once true, `HasBeenShot` unconditionally.
- `sendChanges`'s outer gate also opens on `entityData.isDirty` —
  a summon-time `dangerous:1b` (entityData.set changing the
  default) emits one extra bundle ahead of the %10 gates.

Pinned end-to-end: `TestProjectileDeflectParity` — punch a still
fireball in reach (delta := look angle, power reset to 0.1, the
acceleration drifts on the analytic curve v_n = 1.9 − 0.9·0.95ⁿ —
drift gates may land one tick apart between legs since the punch
is processed mid-tick against each server's entity pass), Owner
[I;] digit-equal on the punched runner (sort=furthest — the
reversed lane answers nearest and is ownerless: REVERSE passes
`this.owner` back), HasBeenShot/LeftOwner = 1b, wither_skull
punch leaves Motion {0,0,0} + nodamage count equal (hurt-false
arm). Lane C: a NoAI breeze parked at z=4 REVERSE-deflects an
incoming fireball — delta flips to −0.5·(0,0,−0.5), accel halves
to 0.05, and the reversed drift re-follows the 0.95ⁿ curve
byte-equal across legs.

Two real bugs the lane-C differential surfaced:

- `parseDouble` added +0.5 to integer tokens lacking ".eE"
  ("centering") — every Vec3/Vec2 command arg (summon, tp,
  execute positioned, worldborder center…) landed half a block
  off vanilla, whose Vec3 coords are exact. Removed the center
  param across 19 call sites; the ops_test expectations that
  encoded the centered values corrected to exact ones.
- Post-deflect ordering: vanilla's `hitTargetOrDeflectSelf` runs
  BEFORE the entity move, so after REVERSE the projectile
  integrates the reversed delta the same tick and next tick's
  scan starts past the deflector's inflated (+0.3) box face.
  mc-go moved first, then deflected + snapped back — next tick's
  segment started exactly ON the face → a phantom re-hit
  exploded on the breeze. Fix: after `delta *= -0.5`, advance
  position by the reversed motion before continuing.

Also measured: `EntityReference.store` writes `Owner` via
UUIDUtil.CODEC unconditionally when the ref is non-null — so a
REVERSE-deflected ownerless projectile keeps `Owner` absent on
both legs (the earlier "missing Owner" was the test selecting
the wrong — nearer, reversed — fireball), and vanilla unloads
entities past the view-distance ticket window, so the test pins
view 24 to keep both runners loaded for the NBT probes.

Deferred: MOMENTUM_DEFLECT (no vanilla caller found — constant
only); EntityReference for non-session owners
(vehicles/passengers unmodelled); `rotateTowardsMovement` facing
lerp; Owner NBT for non-projectile references (angry_at etc.).

## Slice — AbstractArrow lifecycle: stick/shake/despawn/pickup/water-inertia

Measured (javap -c -p + Vineflower `AbstractArrow`, `Entity`,
`ProjectileUtil`, 26.2):

- tick() head: `movement = getDeltaMovement` captured BEFORE any
  mutation — the block-embed re-stick, `shakeTime--`,
  `isInWaterOrRain → clearFire` all read the field later.
- Embed re-stick: pos inside a non-air block with a nonempty
  collision shape (top face check) → `setDeltaMovement(ZERO)` +
  `setInGround` — summoned-in-stone arrows stick instantly.
- inGround arm: `lastState != blockState(blockPos) &&
  shouldFall()` → `startFalling` (inGround=false, per-axis
  delta *= rand·0.2, life=0); `shouldFall` =
  `noCollision(pos±0.06)` — a tip resting 0.05 off the face still
  counts embedded, so breaking the wall does NOT immediately
  un-stick the arrow. Otherwise `tickDespawn` (life++, discard
  ≥1200) and `playerTouch` pickup gated by shakeTime≤0.
- `onHitBlock` backoff: `pos = hitLoc − signum(movement)·0.05`
  PER-AXIS on all three axes (not just the hit face) — a
  dy<0 arrow lands +0.05 high; plus shakeTime=7, crit=false,
  pierce=0, soundEvent=ARROW_HIT at pitch 1.2/(rand·0.2+0.9).
- **Captured-movement semantics**: `clipIncludingBorder` /
  `stepMoveAndHit` use `originalPosition.add(movement)` — the
  TICK-HEAD delta — while `applyInertia` mutates the field. The
  displacement therefore uses the PRE-inertia delta: a tick's
  water drag (0.6) or air drag (0.99) only reaches the flight
  one tick late, and `isInWater` itself latches one tick behind
  the fluid scan (Entity.tick's water update runs after the
  projectile body). Reproduced mc-go by capturing `mv` before the
  water arm and moving by it; `arrowInWater` field latches the
  flag. Verified tick-for-tick against the live jar via
  freeze+step probes (van t-series posz −7.5,−7.0,−6.505,−6.208…
  matches mc-go exactly).
- NBT tail emit: `life` short, `inBlockState`{Name,Properties?},
  `shake` byte, `inGround` bool, `pickup` byte, `damage` double
  (2.0), `crit` bool, `PierceLevel` byte, `SoundEvent` string,
  `item`{id,count:1}, `weapon` nullable.
- Water buoyancy: in water `delta.y = delta.y·0.6 − 0.05`
  (gravity still applies after inertia) — arrows sink to a
  −0.125 asymptote.

Pinned end-to-end: `TestArrowStickParity` — lane A wall stick
(pos within 1e-6 of vanilla's margin-epsilon clip points, NBT
inGround/inBlockState/shake/life/damage/crit/PierceLevel/
SoundEvent/item all equal, the hit sound lands in range), lane B
the shouldFall un-stick semantics (wall broken → stays stuck
because the tip still touches → NBT/pos parity), despawn removals
after a 1210 sprint, lane D the water arm via freeze+`tick step 8`
+ `data get Motion/Pos` (the 0x65 emit cadence is interval-latched
and not semantic — probes go through NBT), lane E the pickup gate
(ALLOWED take_item_entity vs DISALLOWED none).

Two probe-protocol notes for future slices:

- `/tick step` is NOT atomic: queued steps run over real time and
  commands interleave into the step count — after a step request
  you must wait the count out before probing (the world stays
  frozen, so the post-step state is deterministic).
- Scalar `data get` reads under freeze+step alternate fresh/stale
  one tick late on vanilla — for one-shot probes read after all
  steps complete; for per-tick traces treat line n as
  end-of-tick n−1.

Deferred: `inGroundTime` counter (no observable consumer yet); sprint's
n+1 clock-vs-entity-tick asymmetry observed on a freeze+summoned
arrow (re-measure if a slice pins entity counts against
`/tick sprint`); entities summoned under freeze appear not to
tick on the first stepped tick on vanilla — arrow and
wither_skull probes both showed the spawn state one tick late —
but the summon-vs-step command ordering makes the mechanism
unclear (queued-into-T+1 vs late ticklist join); reads under
freeze+step shift ±1 tick between legs, so step-checkpoint data
gets are only ±1-tick aligned (see the hurting-water slice).

## Slice — entity flow push: source-cell getFlow + flag-latched arrow current

Measured on the 26.2 jar (Entity.updateFluidInteraction /
EntityFluidInteraction$Tracker.applyCurrentTo + freeze+`tick step 2`
checkpoints on a boundary-grazing arrow): the per-tick current push is
the accumulated `getFlow` of every fluid cell the entity box covers —
**source cells (level=0) push too**, normalized to unit ×0.014 for
water. `entityFluidScan`'s flow gate dropped `lv >= 1` so lv 0–7 all
contribute (falling lv=8 still excluded).

Push timing is one tick behind the flag: the push at end-of-tick n
fires only when the isInWater flag used during tick n−1 was water —
verified to the digit: pushes at t5–t8 ends reproduce vanilla's
motz 0.0823923 / 0.0072612 and posz −5.0780570 / −4.9602292 at steps
6/8, while gating on the current tick's flag over-pushes by ~0.014.
Implemented with a second latch `arrowInWaterWas` alongside
`arrowInWater` (flag still latched end-of-tick for the next tick's
inertia arm, moved from top-of-tick rescan to the entity-loop tail,
mirroring where vanilla's water update runs in Entity.tick).

Pinned end-to-end: `TestArrowStickParity` lane D2 — small source box
whose edge cells flow outward; freeze → step 2×4 → `data get
Motion/Pos` after each pair; mc-go matches vanilla at every checkpoint
within 1e-6 (water drag + boundary push + entry-tick lag all
composed). Interior lane D unaffected (zero-flow box).

Preexisting regressions found while sweeping (reproduced on clean
master via stash — NOT from this change): TestFluidContactParity
("lava source directly over water" settles lava in vanilla vs stone in
mc-go), TestLavaBurnSoundParity (mc-go keeps GENERIC_BURN cadence 4
packets after pig death), TestPlayerFluidParity (mc-go powder-snow
freezeHits=0 with TicksFrozen=140 vs vanilla 2 hits — likely a
regression from an earlier slice, worth its own investigation).

## Slice — player freeze hurt paced on entity tickCount, not SyncCount

`TestPlayerFluidParity`'s powder-snow lane was failing (the
preexisting regression noted above): mc-go showed TicksFrozen=140 but
zero freeze damage events with an over-drained Health. Instrumenting
`damageWith` showed the hurt arm firing **every entity tick** — the
gate read `pe.SyncCount%40`, and SyncCount only advances in
`mobSyncFlush`, which never runs for session-player records: the
modulo was stuck at 0, so i-frames alone throttled the visible
cadence to ~every 10 ticks. Vanilla paces the freeze hurt in
LivingEntity.baseTick on the entity's own `tickCount % 40`.

Fix: new `sess.entityTicks` counter advanced once per session inside
`playerFluidTickStep` (the player entity's game tick — sprint/step
ticks included, plus wall ticks while frozen since players are
exempt; `sess.locTicks` stays the wall-clock counter for the location
advancement). The freeze gate now reads `sess.entityTicks%40 == 0`.

Test harness lesson: `sprintTicks` polls `gameTimeOf`, which reads
the same chat sink — polling consumed the damage-event lines mid-
sprint and starved the later count on BOTH legs. The lane keeps a
plain `sendCommand` + 2.5s wait (mc-go's sprint rate is well under
vanilla's ~1 ktps; the tail hits emit late) and was widened to a
200-tick sprint so the frozen window (≥140) always covers a %40
boundary. Green on both servers: vanilla Health=17/4 hits, mc-go
Health=17.67/3 hits, TicksFrozen=140 both.

## Slice — down-spread ends the fluid tick (sourceNeighborCount gate)

`TestFluidContactParity`'s "lava source directly over water" scenario
was the one preexisting regression left from the flow-push slice:
vanilla settled the ring beside the source to lava level 8 while mc-go
hardened it to stone.

Vineflower on FlowingFluid.spread showed why: after a successful
spreadTo(DOWN) — including the LavaFluid.spreadTo that hardens a water
cell to stone — vanilla RETURNS from the tick; sideways only still
happens when `sourceNeighborCount(level, pos) >= 3` (same-kind source
blocks horizontally adjacent). mc-go's spreadFluid fell through to the
side spread in the same tick, so the lava source's level-2 side flows
at the layer above landed in the -60 ring while the water's own level-1
flows were still mid-decay — and lava-falling-into-water makes stone.
Vanilla waits a full lava tick (~30) for the sides, by which time the
starved water has decayed to air and the lava lands as level-8 lava.

Fix, mirroring the bytecode:

- the lava-down-into-water branch returns early unless
  `sourceNeighborCount >= 3` (new helper — horizontal same-kind source
  neighbours, same count vanilla uses);
- the ordinary `canFall` path applies the same gate — vanilla spreads
  down AND out in the same tick only for the ≥3-source crowd.

Step-probe before/after on mc-go now matches vanilla at every
checkpoint (water lv7 mid-decay at +30, lava ring at +400, centre cell
stone). `TestFluidContactParity` green end-to-end, and the whole
fluid-adjacent sweep stays green: TestFluidParity, TestItemFluidParity,
TestPlayerFluidParity, TestBubbleColumnParity.

Noted for a later slice, not chased here: vanilla's
LavaFluid.canBeReplacedWith lets WATER spread down into deep lava
(height ≥ 4/9), an edge mc-go currently never takes — the obsidian
path covers the scenarios already pinned.

## Slice — hurting projectiles: latched isInWater flag, liquid inertia + flow push

The deferred "hurting projectiles likely share the captured-movement
semantic" item turned out to have a false premise — decompiled
AbstractHurtingProjectile.tick moves on the POST-inertia delta
(applyInertia() first, position().add(getDeltaMovement()) second), and
mc-go already did exactly that. But the trajectory probe wrote the
real gap: vanilla's applyInertia reads the isInWater flag Entity.tick
latches at the previous tick's tail — updateInWaterStateAndDoFluidPushing —
one tick behind the box position, while mc-go re-scanned the fluid box
live inside applyHurtingInertia and switched to liquid inertia at the
boundary tick itself.

Fix: hurting projectiles now share the arrow water model. The entity
loop's tail scan (the former arrows-only `sticks` arm) widened to
hurtingProjectileTypes with their own dims (fireball 1x1, skull/wind
charge 0.3125 cube) — it latches e.arrowInWater for next tick's
applyInertia and applies the ~0.014/tick flow push gated on the
previous tick's flag (arrowInWaterWas), the same Entity.tick-tail
semantics arrows already used. applyHurtingInertia reads the latch
instead of a live scan.

`TestHurtingWaterParity` (new, differential vs both servers): a
wither_skull summoned inside a deep water box (interior sources, zero
getFlow — pure inertia) and one grazing a small box's flowing boundary
cells, freeze + `tick step 2` checkpoints probing Pos/Motion. The push
lane aligns element-for-element at 1e-6; the deep lane aligns at a
constant ±1 checkpoint shift — vanilla's summon-under-freeze lands one
tick behind and the data gets read ±1 stale between legs, so the test
finds the single constant shift then pins every overlapping checkpoint
at 1e-6 (a wrong inertia or missing flag tick aligns at no shift).
Green both legs; TestHurtingInertiaParity, TestProjectilePowerParity,
TestArrowStickParity stay green.

## Slice — mob suffocation: isInWall eye-plane arm on LivingEntity.baseTick

The other half of the in_wall arm: `playerInWall` on the survival
path already covered session players, but `LivingEntity.baseTick`
runs the same arm for every living entity — the only missing piece
was the mob loop. Measured on the 26.2 jar: baseTick's isAlive block
(ServerLevel) probes `isInWall()` — a 1e-6-tall plane centred on the
eye position, `width*0.8` across — over the cells it overlaps; a cell
counts when it is non-air, `isSuffocating` (the opaque full-cube
table), and its collision shape meets the plane. Every member of
mc-go's suffocating table collides as a full cube, so the shape
clause is implied by the table. Each true tick lands a 1.0
`in_wall` hit, thinned inside damageMob by the twenty-tick
invulnerability window to one accepted hit per ~10 ticks.

Implementation: `mobTickStep` calls `mobInWall` (eye-plane probe
over `entityBBDims` half-width ×0.4, eye = feet + height×0.85) and
runs the existing `damageMob` arm — invuln cadence, hurt broadcast,
death/linger all reuse the shared path.

`TestSuffocateParity` (new, differential vs both servers): pig +
villager + glass-covered pig summoned in open air (vanilla /summon
refuses colliding spots), frozen, then buried under fills; stepped
checkpoints read `data get entity … Health` per leg. The pin is
offset-immune — each leg's mob falls a different distance before
the freeze lands, so it compares the 10-tick cadence via
inter-checkpoint health deltas (villager ≈3 per 30 ticks, ≈6 per
60) plus dead/alive parity at every checkpoint (pig dead by ~t115,
villager by ~t215, glass pig untouched at 10). Green both legs.

Test-harness note folded in from this slice: `tick step N` still
runs at the server's ~20tps — reads must wait the real-time span
or they sample mid-window.

## Slice — water into deep lava: measured dead code + isHole depth gate

The long-deferred `LavaFluid.canBeReplacedWith` edge resolved by
measurement rather than implementation. The bytecode is exact:
`getAmount()/9 >= 0.4444 && incoming is WATER` — a source, the four
strongest flow levels (block levels 0-4), and every falling state
accept water from any direction, with no direction check at all.
But a pool probe on the 26.2 jar shows the path never fires in
play: lava solidifies synchronously on `neighborChanged` the moment
any water lands beside or above it (sides + above are the contact
set; below is not), so a deep-lava cell is crust — obsidian at a
source, cobblestone at a flow — before any water tick can select it
as a spread target. Spread targets are by definition water-adjacent,
so the pre-crust always wins; the water column ends on top of the
crust, identical cell-for-cell between mc-go and vanilla across a
9x7 probe dump of a two-deep pool.

The one place the check is still reachable is `SpreadContext.isHole`
— the slope search asks whether the cell below a traversal cell is
enterable, a hypothetical the crust never pre-empts. mc-go treated
every lava cell below as a hole; vanilla accepts only the deep half
(levels 0-4 and falling), while lava sees a water cell below as a
hole unconditionally (`WaterFluid.canBeReplacedWith` is its
direction==DOWN arm — lava drops into water and lands as stone).
`fluidIsHole` now routes other-kind cells through `fluidEntersBelow`
with exactly those two rules. TestFluidParity and
TestFluidContactParity stay green vs both servers.

## Slice — hurting projectiles: rotateTowardsMovement facing lerp

`AbstractHurtingProjectile.tick` eases the entity facing 20% per tick
toward the post-inertia delta heading — `ProjectileUtil.
rotateTowardsMovement` between the delta-add and setPos — and
`ShulkerBullet.tick` calls the same util at 0.5. The bytecode is
exact: yaw target `atan2(z,x)·deg+90` written through `setYRot`
(which keeps the raw value — yRot drifts outside [0,360)), pitch
`atan2(horiz,y)·deg−90` through `setXRot` (fmods by 360); the
previous rotation unwraps to within ±180 of the target via ±360
while-loops, then `lerp(f, rotO, rot)` — and a zero delta is a
no-op. `rotateTowardsMovement` in projectile.go implements it
verbatim (e.Yaw/e.Pitch double as yRotO/xRotO — the only per-tick
writer), wired into arrowTickStep (0.2 hurting, 0.5 shulker) and
windChargeTickStep.

The inertia chain keeps the delta parallel to the initial heading
(accel lands along normalize(delta)), so the facing always
converges to the summon heading — the differential pin compares the
remaining-gap decay (rem_cp2 = rem_cp1·0.8^20) plus the converged
Rotation NBT, both legs identical. Three real side-gaps fixed along
the way: the /summon hurting arm now reads `Rotation` through
setRot semantics (each axis %360 — Rotation [370,-450] lands as
[10,-90]), and `small_fireball` joins the summonable hurting set.
The zero-delta guard pins a hovering projectile's rotation frozen
on both servers. ShulkerBullet's 0.5 share is unpinnable
differentially (the wander lerp picks directions off RandomSource —
per-leg divergence is the generator, not behavior); the call site
still carries it for wire parity.

## Slice — projectile-deflect hits: AIM_DEFLECT, breeze/arm deflectors, border arms, computeMargin ramp

This slice lands the full `hitTargetOrDeflectSelf` shape measured
from 26.2 bytecode. The entity sweep is three priority picks along
the tick's segment: a `deflects_projectiles`-tagged deflector (the
breeze — returning NON-NONE `deflection`) consumes the hit as
deflect(REVERSE·0.5) + `lastDeflectedBy`, a `redirectable_projectile`
(fireball, wind_charge, breeze_wind_charge) takes
`Projectile.onHit`'s entity arm — `hitProj.deflect(AIM_DEFLECT,
shooting projectile's owner…)` so the target inherits the SHOOTER's
look delta + owner + accelerationPower 0.1 — then the shooter's own
onHitEntity runs its hurt-fail tail (arrow reverses −0.1·delta,
hurting types keep flying because Projectile.hurtServer returns
false). `Breeze.deflection` returns NONE for the two charge types
(its own shots land as hits — measured 1.0 hurt on the summoned
breeze) and wraps REVERSE with the `breeze.deflect` sound otherwise;
entity-attached `playSound` rides sound_entity (0x74), not the
positional 0x75 — fixed both the deflect emit and the ambient-voice
emit, which had been on the wrong packet type.

The world border is an implicit wall: arrows bounce off it as
deflect(REVERSE) then delta·0.2 (net −0.1 + the 170-190° spin),
while hurting types take the border clip as a BLOCK hit — the face
explosion runs at the clamped position with no tail.

The deep fix is `ProjectileUtil.computeMargin`: the entity-sweep pick
radius is `max(0, min(0.3, (tickCount-2)/20))` — zero for the first
~3 sweeps, full 0.3 at sweep 9 — and `tickCount++` runs at the END
of the tick (Projectile.tick is the last call in AbstractArrow.tick),
so sweep N sees tickCount N-1. mc-go had hardcoded +0.3 on every
entity pick, then the first-pass ramp off-by-oned a tick; a live
probe caught an arrow sitting exactly on the fireball's box
boundary (slab entry t0=0) that vanilla never clips. New
`Entity.TicksLived` feeds `projectilePickMargin` on every
segmentHitsBox/T call in the projectile + wind-charge paths.

Pinned: TestProjectileDeflectParity (punch deflects, powers, owner
NBT, breeze sounds) + new TestProjectileDeflectHitsParity — lane A
the arrow→fireball AIM_DEFLECT (low-arrow under-flight pins the
margin=0 clip), lane B the charge→breeze NONE-deflection hurt, lane
C the border bounce + border explosion — all green vs both servers.

Deferred: EntityReference (non-session) owners on AIM_DEFLECT;
onDeflection for thrown-vs-redirectable hits; the near-stopped
arrow's getPickupItem drop arm (speed²<1e-7 unreachable while
moving); breeze deflect for its own charge types.

## Slice — sound channel parity: positional-vs-entity overload split + Player self-exclusion

javap end-to-end of the vanilla sound dispatch, plus a jar-wide caller
sweep of the two-entity overload, established the true 0x74/0x75 model:

  - Entity.playSound -> level.playSound(null, x,y,z,...) -> the
    POSITIONAL clientbound sound packet (0x75), broadcast to everyone
    in range. Nearly every mob/voice/item emit uses it.
  - Player.playSound overrides it -> level.playSound(this, x,y,z,...)
    — the player IS the except arg, so a victim never receives their
    own hurt/fall/burn voice over the wire (the client plays it
    locally off the damage event); bystanders still hear it.
  - LivingEntity.die -> broadcastEntityEvent(this, 3) — the death
    sound is client-side on entity_event, no sound packet at all.
  - Only the explicit level.playSound(except, entity,...) overload
    anchors a sound on an entity (sound_entity 0x74): BREEZE_DEFLECT,
    the firework launch + LEAD_BREAK on the leashed mob, TRIDENT_THROW
    on the thrower, map/instrument/dye/shear items and a few animal
    behaviors — the whole rest of the jar's call sites are positional.
  - Breeze.playAmbientSound routes through playLocalSound — a
    server-side no-op, so mc-go now gates the ambient emit off for
    breeze entirely.

An earlier sweep that had moved ~30 sites onto playSoundEntity was
reverted once the wire + javap proved hurt/ambient voices positional;
the genuine two-entity callers kept entity anchoring (lead.break per
leashable, trident throw, firework launch — now with an arrowShooter==0
fallback — and the pre-existing map/dye sites). The hurt voice and
elytra fall voice restored their sess exclusion to match
Player.playSound's self-except, and the entity-inside burn voice now
self-excludes when the burning entity is a session player.

Pinned: TestEntitySoundChannelParity — two conns, one victim one
bystander; zombie.hurt positional for both listeners, player.hurt
absent on the victim conn and present on the bystander's, identically
on both legs (measured: 3x zombie.hurt + 1x player.hurt per leg).

Still open: allay sounds, breeze whirl/charge/jump/land. (The
shear/dye/stew/goat-horn entries here were stale — shear.go,
animalinteract.go and useGoatHorn emit them; the sheep-shear 0x74
is now pinned by TestAnimalInteractParity — 2026-10-01.)


## Slice — enchanting table: offer-roll parity pinned on a shared seed

javap end-to-end of EnchantmentMenu + EnchantmentHelper, then a
differential pin that eliminates the PRNG's only uncontrolled input:

  - The offer pipeline is java.util.Random exactly: a full LCG port
    (javarand.go — setSeed XOR, 48-bit state, power-of-2 fast path +
    rejection sampling, nextFloat from next(24)) verified bit-exact
    against a real JVM across five seeds before wiring. Menu rolls
    re-seed the shared menu random per slot (seed, then seed+slot)
    and the book case sheds one extra pick on the same tail stream.
  - The candidate list iterates the in_enchanting_table tag's
    DECLARED order (non_treasure resolution order, generated as
    InEnchantingTableOrder) — vanilla's HolderSet stream walks tag
    order, not registry order, and the weighted pick's index lands
    differently under the wrong order. That ordering bug was the
    only divergence the pin caught after the LCG was exact.
  - BOOKSHELF_OFFSETS is betweenClosedStream(-2,0,-2, 2,1,2) — all
    50 cells of the 5x2x5 volume, not the 32-cell ring — and the
    transmitter check offsets (X/2, Y, Z/2) with the Y component
    NOT halved. Both were corrected.
  - EnchantmentMenu$2 caps the item slot at stack 1 but takes ANY
    item (mayPlace not overridden); $3 takes lapis only. Click
    deductions are the button tier i+1 (not the displayed cost) for
    both xp and lapis, the negative-xp clamp zeroes progress+total,
    and the post-enchant emit is a positional sound at the table
    with the rolled rand*0.1+0.9 pitch.
  - quickMoveStack: lapis always targets slot 1; any other stack
    moves a single item into slot 0 only while it is empty;
    otherwise the stack relocates across the player sections.
  - XpSeed: /data can't write player NBT (commands.data.entity.invalid
    on both servers), so offers can't be seed-pinned that way — but a
    fresh player's ctor seed is 0 on both implementations, and
    readAdditionalSaveData rerolls 0 -> rand on load. Fresh conns
    therefore share seed 0, making every roll reproducible.

Pinned: TestEnchantParity — fresh player per leg, diamond shovel +
lapis: all ten container properties (three costs, seed, three clue
enchant ids, three clue levels) match EXACTLY across vanilla and
mc-go, plus the enchanted result stack after button 0.

Grindstone test fix (same commit): orb payout probes read XpTotal=0
on both legs — orbs spawn inside the grindstone's own collision
shape and can never fly out (verified by Pos query: exact block
center, both servers). They pay only on playerTouch, so the fixture
now tp's the player onto the block; payouts land in range on both
legs.

Deferred: none for this slice; slot0 mayPlace / XpSeed reroll /
quick-move split / bookshelf count are all pinned above.

## Slice — shear sound emits (Shearable + equipment)

Shearing any mob emitted zero sound; vanilla plays one per shear
event. Measured 26.2 bytecode (Sheep.mobInteract, SnowGolem/Bogged/
MushroomCow.shear, ShearsDispenseItemBehavior, Mob.shearItem) and
pinned vs both servers:

  - Every Shearable leads shear() with playSound(null, this, <its
    shear sound>, source, 1, 1) — an entity-anchored 0x74. The
    interact path passes SoundSource.PLAYERS (7): sheep.shear=1441,
    snow_golem.shear=1574, bogged.shear=184, mooshroom.shear=989.
    The dispenser path passes SoundSource.BLOCKS (4) through the
    same shear() — same sounds, src=4, both legs.
  - The emit rides inside the ready branch only: an already-sheared
    sheep/bogged or pumpkinless golem plays nothing (mobInteract
    returns CONSUME before shear()).
  - mooshroom's emit precedes convertTo(COW) so the anchor entity
    still exists when the packet goes out.
  - Equipment shear (wolf_armor on a tamed wolf): Mob.shearItem, then
    mob.playSound(equippable.shearingSound) — POSITIONAL 0x75 at the
    mob, not entity-anchored; Entity.getSoundSource is NEUTRAL (6),
    adult voice pitch (r1-r2)*0.2+1. wolf_armor's shearing_sound is
    item.armor.unequip_wolf (registry 78, wire 79).

Pinned: TestShearMobParity now asserts the four (snd, src=7, eid)
0x74 tuples per leg (ambient mob sounds also ride 0x74 — the pin is
the full tuple, not the packet's presence); the wolf-armor half of
TestPlayerShearedEquipmentParity asserts the (79, src=6) 0x75 emit.

Deferred: leash shearOffAll, dye/bucket/stew interact sounds, and the
dispenser shear pin (javap-measured src=4, no live differential yet —
the interact path pins the code shape).

## Slice — farmland trample (FarmlandBlock.fallOn)

Mobs and players landing on farmland never converted it to dirt.
Measured 26.2 bytecode (FarmlandBlock.fallOn, turnToDirt,
Entity.checkFallDamage/getOnPos) and pinned vs both servers:

  - Roll: ServerLevel && rand.nextFloat() < fallDist - 0.5 &&
    entity instanceof LivingEntity && (Player || mobGriefing) &&
    getBbWidth()^2 * getBbHeight() > 0.512 -> turnToDirt:
    setBlockAndUpdate(DIRT) + GameEvent.BLOCK_CHANGE.
  - checkFallDamage dispatches state.fallOn BEFORE causeFallDamage —
    a fall-immune mob still tramples (iron_golem volume 5.29).
    Implemented at the top of mobFallDamage before the immunity
    early-return; zombie (0.702) tramples, chicken (0.112) can't.
  - Player path: handleMove's onGround landing branch rolls with
    volume 0.36 * height (1.8 standing / 1.5 sneaking — both >0.512)
    and the accumulated sess.fallDistance; support pos is
    floor(y - 1e-5) (getOnPos default epsilon).

REAL bug the differential surfaced: playerBoxCollides treated every
non-air cell as a full cube, so a landing packet whose feet rest on a
15/16 top (feet inside the cell) was always rejected "moved
wrongly" — players could never trample farmland or stand on
dirt_path. Vanilla's noCollision runs the real VoxelShape overlap.
Now the gate skips a cell unless the box dips below
collisionTopOf(b) and rises above the cell bottom; collisionTopOf
gained farmland/dirt_path=0.9375.

Pinned: TestFarmlandTrampleParity — zombie + player trample to dirt,
chicken blocked by the 0.512 volume gate, both legs.

Deferred: gliding-pose bb height for trample volume; turtle-egg
trample.

## Slice — mob fall tunneling (mobGrounded collide sweep)

TestMobFallDamageParity had been failing on bare master: every mob
leg took ~1-2 extra damage vs vanilla, including the fall-immune
chicken. Pos probe showed 10-block-fall mobs resting at feet=-62 —
one block INSIDE the stone floor — while vanilla rests at -61.

Root cause: mobGrounded applied gravity with a bare e.Y += MotionY.
At cap -1.0 the feet swept a full cell per tick, so the support
check at getOnPos(0.500001) — which looks at the half-below cell —
never saw the floor the feet had already tunneled INTO: it grounded
the mob on the next solid cell down, feet embedded a full block
deep, and the embedded mobs then suffocated (that's the damage on
the fall-immune chicken).

Fix: the airborne step now sweeps the foot face through
clampBoxAxis (the same VoxelShape-top sweep entityMove uses), so a
downward step stops at the highest collision top in the span —
mobs land at exactly -61 like vanilla. No new code path: it reuses
the box-collision helper.

Pinned: TestMobFallDamageParity green on both legs (19/13/3/4
health + the hostile/generic fallsounds), TestFarmlandTrampleParity
still green, mob ground/lava/item-drop/fluid suites unaffected.

## Slice — chicken wing-flap glide (Chicken.aiStep)

The tunneling fix's Pos probe showed vanilla's chicken still mid-air
at -55 long after mc-go's had landed: Chicken.aiStep damps the
stored descent delta by Vec3.multiply(1, 0.6, 1) every airborne
tick (after the air-travel tail's 0.98 drag — effective 0.588), so
the glide settles near -0.114 blocks/tick instead of the -1.0 cap.

Measured via javap on 26.2's Chicken.class (aiStep: !onGround &&
delta.y < 0 -> multiply(1, 0.6, 1)); jar scan found no other class
with the pattern, so mobSlowFalls holds only "chicken". Scale
applied to the stored delta BEFORE the descent step is applied —
matching vanilla's order (the damped delta IS the next tick's
displacement).

Pinned: TestChickenGlideParity — Pos[1] of a chicken dropped 21
blocks at a fixed window: vanilla -44.290, mc-go -44.404 (0.11
drift, well inside the 0.5 band; a full-gravity fall would rest at
-61). TestMobFallDamageParity + TestFarmlandTrampleParity still
exact-match.

Deferred: none for this slice.

## Slice — turtle egg trample (TurtleEggBlock.fallOn/stepOn)

TurtleEggBlock's two break arms measured off 26.2 bytecode:

- fallOn rolls destroyEgg(3) — 1/3 per landing — for any LivingEntity
  that lands, EXCEPT the Zombie class family (instanceof Zombie:
  zombie, husk, drowned, zombie_villager, zombified_piglin — the
  subclass check rides the type table). No distance floor: a 0.04
  summon-rest drop still rolls.
- stepOn rolls destroyEgg(100) — 1% per grounded tick — on the
  getOnPosLegacy support cell (floor(y-0.2)), every tick the entity
  is grounded, UNLESS isSteppingCarefully (sneaking).
- canDestroyEgg gates both: turtles and bats are always exempt, and
  every non-player LivingEntity additionally needs mob_griefing.
- Each accepted break plays TURTLE_EGG_BREAK at BLOCKS 0.7 /
  0.9+r*0.2, then eggs>1 rewrites eggs-1 (vanilla flag 2 — clients
  only, no neighbour updates) with BLOCK_DESTROY + levelEvent 2001 on
  the pre-decrement state; eggs==1 destroyBlock(false)s the cell.

mc-go: internal/server/turtleegg.go carries the mechanics;
mobFallDamage gained the zombie-family-exempt fallOn arm next to
farmland's (both dispatch before the fall-immunity early-return, like
checkFallDamage's fallOn-then-causeFallDamage order); mobGrounded's
grounded tail rolls the stepOn arm on floor(y-0.2); the player arms
sit in handleMove's onGround fallDistance block (fallOn) and the
per-tick player pass (stepOn gated on !sneaking). collisionTopOf
learned the egg shapes (0.75 eggs==1, 0.4375 eggs>=2) — without it
landings clipped like farmland's did.

Test-design discovery worth keeping: a remote player whose scripted
client jitters +-Y while standing on eggs produces micro-landings —
every downward packet accumulates fallDistance and the next
onGround packet rolls fallOn (sneak does not exempt it). Horizontal-
only jitter exercises stepOn with no fallOn because fallDistance
only accumulates on airborne downward deltas. That is how the sneak
leg isolates cleanly.

Pinned: TestTurtleEggTrampleParity — A sneaked-player x16 falls
(fallOn), B penned zombie stand (stepOn; zombie is fallOn-exempt so
the attribution is clean), C penned turtle (canDestroyEgg exempt),
D sneaked jitter-stand (isSteppingCarefully exempt), E penned zombie
under mob_griefing=false (rule gate). Vanilla 0/0/4/4/4 + sound+2001,
mc-go 0/1/4/4/4 + sound+2001 — green.

Deferred: vanilla's eggs-1 write is flag 2 (no neighbour updates);
mc-go's setNaturalBlock sends neighbour updates — a wire-order nit
only, same end state. turtle_egg's randomTick hatch arm is separate
(already sliced in c29ea69).

## Slice — vanilla getFlow current: ownHeight differentials + armor-stand physics tick

Replaced mc-go's `fluidSurfaceHeight`/`fluidFlowDir` approximations
with the exact 26.2 model decompiled from FlowingFluid:

- `getOwnHeight(state) = amount/9.0` where amount = 8 − LEVEL (source
  and FALLING both read 8/9 — not the 1.0 mc-go first coded).
- `getHeight(state,pos)` (the fluid top a fluid-interaction scan
  sees) = 1.0 when the same-kind fluid sits directly above, else
  ownHeight. `entityFluidScan`'s per-cell fluidTop now uses it and
  cells whose top is below the box bottom are skipped outright
  (vanilla's `!(fluidTop < box.minY)` gate — the reason items resting
  on a surface bob never read overlap and never drift).
- `getFlow` verbatim: per horizontal neighbor,
  `distance = ownHeight − neighborHeight` when the neighbor holds
  same-tag fluid; when the neighbor is empty AND doesn't block
  motion the look drops one cell below — the below cell's ownHeight
  minus 0.8888889 shifts the weight (the ledge-pull arm); solid
  neighbors contribute 0. A FALLING cell with a solid face at its
  own y or the cell above normalizes then adds (0,−6,0), and the
  whole vector is normalized once.
- `isSolidFace` approximation: same-fluid → false, ice → false, else
  `!world.NonSolid(b)` — the UP special case isn't needed (getFlow
  only asks horizontal faces).
- Depth scale: the running tracker height is per-cell-max of
  `fluidTop − entityY`; a cell's flow is scaled by that depth when
  below 0.4, then accumulated — previously mc-go scaled by a global
  depth after summing.

Entity side (EntityFluidInteraction$Tracker): the scan now returns
the 3-vector + cell count; `applyFluidCurrent` normalizes the full
3D sum (non-player branch), floors to 0.0045 when |deltaXZ|<0.003
and |flow|<0.0045, and addDeltaMovement's all three components —
the −6 waterfall pull reaches entities through the y channel.

Bigger gap the differential surfaced: armor stands never ticked.
`nonMobLiving` excluded them from the mob travel path entirely —
they're LivingEntity not Mob, so vanilla still runs
updateFluidInteraction + aiStep's zero-input travel (gravity, fluid
drag, current push). mobTickStep now runs that arm for nonMobLiving:
entityFluidTick + mobTravelStep(0) + NeedsSync (aiStep's push latch).

Probe-design findings worth keeping (each measured on vanilla):
items hover with their box bottom just above the fluid top → zero
overlap, no push; a NoGravity item escapes the column via the
underwater-lift cap (+4.4 in 80 ticks); a NoAI zombie doesn't tick
physics AT ALL — hung frozen mid-air over 100 ticks; a NoGravity
arrow/armor_stand likewise never moves. A plain armor stand sinks,
ticks, and lets the current do the talking.

Pinned: TestWaterCurrentPushParity — leg A feeds a walled lane from
both ends (the level-4 tie cell reads flow 0; the left front pushes
+x, the right −x) and the stand converges at the boundary: vanilla
−1.432, mc-go −1.420. Leg B ends the lane in a dug sump: the level-7
lip drops falling water, the stand rides +x, falls in, and pins
against the cap face at the pit floor: identical (21.75, −61) on both
legs.

Deferred: the Tracker's Player branch scales the accumulated flow by
1/count instead of normalizing — session players have no server-side
delta integration yet (client-authoritative pos), so the arm waits
for that substrate. applyFluidCurrent currently always normalizes.

## Slice — turnToDirt pushEntitiesUp

FarmlandBlock.turnToDirt (jar-decompiled: turnToDirt → pushEntitiesUp
→ setBlockAndUpdate → gameEvent BLOCK_CHANGE) lifts every entity whose
box intersects the collision volume gained by the swap — for
farmland→dirt the slab [pos.y+15/16, pos.y+1] — via
teleportRelative(0, 1+Shapes.collide, 0): the +1-lifted box descends
onto the slab top, so an entity standing on farmland ends its feet
exactly on the new dirt top (a 1/16 lift). Selection is strict-AABB
intersect of the gained slab; the trampler itself lands in the slab
and self-pushes. Grass/mycelium→dirt reverts reach the same
turnToDirt but their ONLY_SECOND join is empty (equal collision
shapes) → no push — turfRule stays direct-write.

mc-go gains pushEntitiesUpFarmland (randtick.go): entities via
entities.All + GetPtr write (PosDirty+NeedsSync like a tracker sync),
sessions via sess.setPos so the wire teleport goes out. Both
turnToDirt call sites wired: farmlandTrample (fallOn) and
farmlandRule's dry-out — which was also missing turnToDirt's
gameEvent BLOCK_CHANGE, now emitted at the block center.

Pinned: TestFarmlandTrampleParity leg TR3 — an item resting on the
farmland cell at feet -60.0625 plus a 5m zombie trampler; after the
trample the item reads -60.0000 on both servers (identical), the
zombie self-push reads -60.0000 both legs.
## Slice — leash death-drop (canInteractWithLevel gate)

Leashable.tickLeash's head gate: !leashee.canInteractWithLevel() ||
!holder.canInteractWithLevel() → entity_drops ? dropLeash (item) :
removeLeash (no item). canInteractWithLevel = isAlive && !isRemoved &&
!isSpectator — tickLeash runs from Entity.baseTick, which keeps
ticking through the 20t death animation, so a dying leashed mob
self-drops its leash on death tick 1 (measured: vanilla drops the
lead item when a knot-leashed sheep is /kill'd — intermittent probe
misses are item-timing flakes, not a different mechanism). The same
gate covers a dying/removed holder: leashDrop fires with dropItem
gated on the entity_drops gamerule.

mc-go: mob tick Dying branch now drops the leash first
(LeashHolder!=0 → leashDrop(e, gameRuleBool(entity_drops))); the
tickLeash holder gate gains holder.Dying plus the gamerule gate
(was: drop unconditionally, only when the holder pointer vanished).
Also Mob.startRiding's `if isLeashed() dropLeash()` — unconditional
item drop when a leashed rider boards a vehicle — wired into
rideMobOnto.

Pinned: TestLeashDeathDropParity — A: knot-leashed sheep /kill'd →
lead item lands (both); B: entity_drops=false repeat → unlink, no
item (both); C: fence under the knot broken → knot pops on the
~100t survives pass, holder gone, leashee drops leash+item (both).

## Slice — animal item-interacts (milk / stew / dye / shear)

Measured arms on vanilla 26.2 via live packet probes:
- bucket→cow/mooshroom: milk_bucket + `entity.cow.milk` emitted via
  Player.playSound → positional 0x75 src=7 (PLAYERS), self-excluded
  (bystander hears, actor does not); goat's is `entity.goat.milk`.
- bowl→mooshroom: mushroom_stew + `entity.mooshroom.milk` at entity
  anchor, src=6 — also self-excluded from the actor's wire.
- dye→sheep: `entity.dye.use` (soundEvent 571) anchored on the sheep
  via Entity.playSound src=7 PLAYERS → bystander hears, actor does
  not; sheep `Color` byte-18 updates; dye consumed.
- shears on dyed sheep: drop carries the color (`red_wool`); the
  actor standing on the mob vacuums the item before a positional
  probe can see it on vanilla — probe must check Inventory too.

mc-go: new animalinteract.go — milkInteract (cow/mooshroom snd 449,
goat 740, playSoundSrc src7 + createFilledResult), stewInteract
(bowl→mushroom_stew + snd 987 src6 anchor), dyeSheepInteract
(snd 571 + Color NBT/metadata byte-18 + consumeSlot), sheep Color
field with summon-NBT + save/load + shear.go color-aware wool drops.
Also fixed wpTransmitters All()-snapshot race panic the test hit.

Pinned: TestAnimalInteractParity — 12 probes (3 item swaps, 4 sound
emit/absence, Color NBT, red wool drop) green vs both servers.

Harness lessons (why prior legs all-missed for ~15 runs):
- `summon minecraft:X a b c ` with a TRAILING SPACE (empty NBT slot in
  Sprintf) → brigadier `command.unknown.argument` → summon silently
  fails on vanilla only (mc-go trims). Build the command without it.
- command→command ordering is strict FIFO but command→game-packet is
  not: verify held-item with a single-shot `execute if SelectedItem`
  probe before every interact click, and prove interact range with a
  `distance=..3` selector probe after `tp @s @e[...]`.
- VD=2 client_information shrinks join chunk storms ~289→25 packets
  per conn so the serverbound queue drains in seconds.
- Single-shot `execute if` probes evaluate state at run time — they
  miss permanently if ordering flakes; re-issue (waitFor) only where
  post-click state is probed.

## Slice — MushroomCow variant + stew_effects + thunder convert + variant shears (2026-09-28)

Measured (jar javap + live probes on 26.2): DATA_TYPE metadata index 18
INT (0=red,1=brown, default RED), NBT `Type` always + `stew_effects`
pending list, bowl arm (stewEffects!=null → suspicious_stew + copied
component + clear + suspicious_milk(988), else mushroom_stew +
milk(987)) → shears → BROWN+adult flower arm (already-fed → 2× SMOKE
no consume; else consume+4× EFFECT+set+MOOSHROOM_EAT vol2) → super.
thunderHit: bolt-uuid dedupe → Type swap + MOOSHROOM_CONVERT(985,2,1)
positional, NO super (bolt fire kills, not the strike). shear → shear
snd(989)+convertTo(cow)+EXPLOSION particle+5× variant mushroom.
Entity.playSound(SoundEvent) → positional 0x75 broadcast incl actor.
LightningBolt strike box (x±3,y-3..+9,z±3); mc-go's bolt never struck.

mc-go: entity.go MooshroomVariant+StewEffects fields; mob.go livingMeta
mooVar (snap+pack idx18 int + dirty-diff idx18), summon-NBT
Type/stew_effects parse, lightning_bolt summon strike pass (box filter
→ variant^=1 + dirty meta + snd 985); dataentity.go Type always +
stew_effects nbt.List write/read/allowlist; animalinteract.go
stewInteract suspicious arm (StewEffects→suspicious_stew+component+
clear+988 else mushroom_stew+987), mooshroomFeedInteract (brown+adult+
flower: fed→2×SMOKE no-consume else consume+4×entity_effect+set+986),
17-flower effect table; interact.go broadcastSpellParticles
(entity_effect INT color+FLOAT power tail); worldstate.go wiring;
shear.go EXPLOSION + variant mushroom drops.

Pinned: TestMooshroomVariantParity — 16 probes (Type NBT, idx18 meta,
feed/consume/stew_effects, sounds 985/986/987/988 positional src6 on
actor+bystander, suspicious_stew swap, thunder convert, brown_mushroom
drops) green vs both servers.

Harness lessons:
- every sink drain must accumulate into the probe buffer — waitFor
  loops silently discard sound/metadata packets they read; drain BOTH
  conns on every take or the bystander buffer stays empty.
- `nbt=` subset matching compares nbt.List, not []any — save-map lists
  must be nbt.List{Elem:TagCompound}.
- shear/drop probes: AP parked on the mob vacuums drops — probe
  Inventory first, item-entity fallback.

## Slice — dispenser shear live pin + mooshroom variant drops (2026-09-29)

The dispenser shear paths were implemented but never pinned live; the
mooshroom path hardcoded red mushrooms. Measured 26.2 bytecode
(ShearsDispenseItemBehavior.tryShearEntity: Shearable entity in the
mouth AABB → shear(level, BLOCKS, stack); MushroomCow.shear →
playSound(null,this,MOOSHROOM_SHEAR,BLOCKS,1,1) + convertTo(COW) with
the same finalizeConversion EXPLOSION+loot lambda as the hand path;
shearing/mooshroom loot is variant-conditional red/brown subtables)
and pinned vs both servers:

  - Sheep leg: powered dispenser → 0x74 (snd1441, src=4, sheep eid) +
    Sheared:true + wool drop.
  - Brown mooshroom leg: → 0x74 (snd989, src=4, mooshroom eid) + cow
    spawn + brown_mushroom drops (red absent).
  - ServerLevel.playSeededSound measured: PlayerList.broadcast radius
    = SoundEvent.getRange(vol) = 16*max(1,vol) — all 26.2 registry
    sounds are variable-range (no fixed-range registrations left).
    mc-go's playSoundSrc/playSoundEntity already implement the same
    16*max(1,vol) cap — the first run's "miss" was the probe standing
    27 blocks out, not a server bug.

mc-go: dispense.go mooshroom arm now variant-aware (brown_mushroom
drops) + emits the EXPLOSION puff — matching the hand-shear path.

Pinned: TestDispenserShearParity — 7 probes (0x74 shear tuple src4,
Sheared NBT, wool drop, cow spawn, brown_mushroom Inventory/entity,
red absent) green vs both servers.

Harness lessons:
- summon AI mobs with {NoAI:1b} inside dispenser/piston fixtures — a
  25s spawn poll window lets them wander out of the interaction cell
  and the behavior fires on nothing (silently, on both servers).
- Sound probes must account for the 16*vol broadcast radius: park the
  listener within range of the emit point or the packet legitimately
  never ships.

## Slice — LightningBolt.thunderHit per-type conversion (2026-09-29)

LightningBolt.tick's strikeEntities pass calls each victim's
`thunderHit(level, bolt)`; the per-type overrides were unmodeled.
Measured the 26.2 bytecode for all four overrides plus the
Mob.convertTo chain, then implemented + pinned vs both servers:

  - Entity.thunderHit base: setSecondsOnFire(8) THEN
    hurt(lightningBolt, 5.0) — the 160-tick ignite precedes damage.
    mc-go had no ignite in the lightning paths; added igniteMob(160)
    ahead of every damage call (strike + channeling).
  - Pig (non-peaceful): convertTo(ZOMBIFIED_PIGLIN, single(mob,false,
    true), lambda) — lambda = populateDefaultEquipmentSlots (golden
    sword mainhand) + setPersistenceRequired. No saddle transfer; the
    pig's saddle simply disappears with the pig.
  - Villager (non-peaceful): convertTo(WITCH, single(mob,false,false))
    — lambda = witch.finalizeSpawn + setPersistenceRequired + release
    all POIs (unmodeled).
  - Creeper: super.thunderHit (ignite+hurt) THEN DATA_IS_POWERED=true.
  - Peaceful difficulty: pig/villager fall through to super (ignite+
    damage, no conversion).
  - Mob.convertTo/SINGLE: fresh entity at position+rotation, then
    COMPONENTS_TO_COPY = {CUSTOM_NAME, CUSTOM_DATA} copied verbatim,
    convertCommon carries Age pair + baby flag + active effects +
    persistence; add-then-remove wire order.

CustomName NBT canonical form measured live: a leaf text component
saves as a STRING tag holding its JSON string form — summon
{CustomName:'"ZapPig"'} → save emits CustomName:'"ZapPig"'.
dataentity's new componentNBTValue mirrors this: structured
{...}/[...] components emit as compounds, string leaves keep their
verbatim quotes (bare text re-wraps). Creeper save keys
(powered/Fuse/ExplosionRadius/ignited) also added.

Pinned: TestThunderConvertParity — 14 probes green vs both servers:
conversion presence/absence, golden-sword mainhand + PersistenceRequired
on both conversions, verbatim CustomName string round-trip (nbt=
'"ZapPig"' hits / "ZapPig" bare misses / compound form misses), powered
creeper, damage-taken legs via `unless Health:20.0f` (Health itself
drifts under ignite so the 20f-absence check is the stable pin).

Harness lessons: `name=` selectors miss on BOTH servers for JSON-stored
names — selector behaviour is its own rabbit hole, kept as an
informational probe only. system_chat 0x79 replies carry binary
components; use `execute if entity nbt=... run say MARKER` probes
instead of reading data-get output.

## Slice — bogged shear mushroom rolls (2026-09-29)

Bogged.shear measured on 26.2 bytecode (Bogged is under
entity/monster/skeleton/): mobInteract = stack.is(SHEARS) &&
readyForShearing() (!isSheared) → shear(level, PLAYERS, stack) +
gameEvent(SHEAR) + hurtAndBreak(1) + SUCCESS. shear() =
playSound(null,this,BOGGED_SHEAR) + spawnShearedMushrooms +
setSheared — NO convertTo, the bogged stays a bogged. The drop is
dropFromShearingLootTable(BOGGED_SHEAR): loot table shearing/bogged =
rolls 2 over a 2-entry pool (brown_mushroom x1, red_mushroom x1) —
each roll independently picks one entry, 1 item per roll, NOT one of
each and NOT roll-per-entry.

mc-go's bogged shear (both the hand path and the dispenser
ShearsDispenseItemBehavior arm) looped `for i<2 { for {brown,red} }`
→ 4 item spawns instead of 2. Both arms now roll the pool per roll.

Pinned: TestShearMobParity gained a spawn-packet count leg —
decodes 0x01 spawn_entity lines (id, uuid, type, x, y, z) and counts
type-71 (item) spawns within 4 blocks of the bogged; want exactly 2
on both servers. Green vs both.

## Slice — plain-int variant metadata sweep (2026-09-29)

Six variant-carrying types measured on 26.2 bytecode + live packet
probes and pinned end-to-end: axolotl (Variant int LEGACY_CODEC,
FromBucket bool, rare blue = 1/1200), parrot (Variant int), rabbit
(RabbitType int + MoreCarrotTicks int; ids 0 brown, 1 white, 2 black,
3 white_splotched, 4 gold, 5 salt, 99 evil), tropical_fish (packed
(pattern|base|patternColor) Variant int), fox (Type STRING via
Fox$Variant.CODEC "red"/"snow" + Sleeping/Sitting/Crouching bools +
Trusted list), panda (MainGene/HiddenGene strings via Panda$Gene.CODEC).

Summon NBT parse arms land each key on the entity; defaults roll the
vanilla table (axolotl uniform-common + 1/1200 blue, parrot 0-4,
rabbit plains roll brown/salt/black, tropical fish the kob common
variant, fox red, panda normal/normal). Save-map emits mirror
vanilla's addAdditionalSaveData keys.

Metadata indices measured off the live 0x63 spawn burst, not guessed:
axolotl variant idx18 int + from_bucket idx20 bool; parrot variant
idx20 int; rabbit type idx18 int; tropical_fish variant idx17 int;
fox type idx18 int + flags idx19 byte (0x01 sit, 0x04 crouch, 0x20
sleep); panda genes as Panda$Gene ordinal bytes at idx21/22 (normal=0
… aggressive=6). Vanilla packs only non-defaults — normal/normal panda
genes and fox trusted refs stay unpacked.

Found while probing: RabbitType 99 fires setRabbitType's auto-name —
vanilla's spawn metadata carries idx2 = {"translate":
"entity.minecraft.killer_bunny"} as an optional-component NBT
compound. writeComponentTag gained the structured-component branch
(leading `{` → full compound tag via nbt.WriteUnnamed instead of a
TAG_String leaf), matching the same leaf/compound split
componentNBTValue already applies on the save side.

Pinned: TestVariantParity decodes every 0x63 entry per summon and
compares idx->(type,value) across both legs (extendable shared
decoder in curesync_test.go gained varint + optional-component arms),
plus 9 nbt= selector probes for the save keys (including the empty
Trusted list and the killer-bunny translate name). Green vs both.

Still open: registry-holder variants (cat CatVariant+CatSoundVariant,
frog, wolf WolfVariant+WolfSoundVariant, pig/cow/chicken
variant/sound_variant holders — different wire shape, deferred);
fox Trusted entity-reference metadata entries 20/21 when populated.

## Slice — registry-holder variant sweep + spawn-roll parity (2026-09-29)

Cat/frog/wolf/pig/cow/chicken read namespaced `variant` +
`sound_variant` holders (frog has no sound registry) and salmon the
plain enum `type` (small/medium/large, unnamespaced). Wire packs a
VarInt holder id under dedicated serializer ids measured on the live
0x63 spawn burst (idx/serializer, variant+sound): cat 20/21 + 24/22,
frog 18/27, wolf 23/25 + 24/26, pig 19/28 + 20/29, cow 18/23 + 19/24,
chicken 18/30 + 19/31; salmon's DATA_TYPE is idx17 plain varint. Registry ids are the datapack registration order measured off
generated/data (cat 11, wolf 9, farm/frog 3, sounds 2-7).

Measured: EntityType.spawn rolls EVERY summon (tagged or bare) —
variant via VariantUtils.selectVariantToSpawn (condition = biome tag
membership, max-priority tier, uniform within) and sound via
Registry.getRandom; NBT keys then override. spawnvariant.go tables
rebuild the datapack spawn_conditions (cat all-11 unconditional;
wolf only pale unconditional; farm/frog temperate default, cold/warm
biome-gated priority 1) and the uniform sound draws (no roll for
salmon). The defined defaults (tabby/pale/temperate/classic/medium)
stay unpacked on spawn and ride orStr on the save side; pig's static
sound_variant/variant extras went dynamic.

Pinned: TestHolderVariantParity pins 10 summons' metadata vs both
legs — explicit values asserted exactly, rolled keys asserted as
optional idx->{serializer, max id} bounds — plus 13 nbt= selector
probes for the save keys. Green vs both.

Still open: fox Trusted entity refs 20/21, cat/wolf CollarColor,
pig DATA_BOOST_TIME, frog DATA_TONGUE_TARGET, passive passenger
entities' rolled variants.
## Slice — fox Trusted entity references (2026-09-29)

Fox's DATA_TRUSTED_ID_0/1 (metadata idx 20/21) ride the
optional_living_entity_reference serializer (id 13): bool + raw
16-byte uuid (EntityReference stream codec = UUIDUtil.map). Vanilla
packs only populated slots; the NBT side is TRUSTED_LIST_CODEC =
EntityReference.codec().listOf() — a list of [I;a,b,c,d] int-array
uuids, capped at two slots (addTrustedEntity fills slot0 then
slot1). Measured live: the input must be the list form
`Trusted:[[I;1,2,3,4]]` — the flat `[I;]` int-array is rejected by
the codec (an earlier probe's sprintf wrapped the uuid in an extra
bracket and made the flat form look accepted). Save emits the same
list-of-int-arrays shape; empty keeps `Trusted:[]`.

Pinned: TestVariantParity gains a snow fox with Trusted — metadata
idx20 = serializer 13 bool+uuid (last byte 4), plus the nbt=
probe for the save form. Green vs both.

Still open: cat/wolf CollarColor, pig DATA_BOOST_TIME, frog
DATA_TONGUE_TARGET, addTrustedEntity via taming (runtime path —
trusted list only enters via summon NBT for now), passive
passenger entities' rolled variants.
## Slice — cat/wolf CollarColor (2026-09-29)

Measured: Cat and Wolf both define DATA_COLLAR_COLOR as an int
accessor — cat's accessor-id lands at idx23 (after IS_LYING/
RELAX_STATE_ONE), wolf's at idx21; both serialize as a varint dye
legacy id (0-15). NBT reads/writes go through
DyeColor.LEGACY_ID_CODEC (Codec.BYTE): read applies the defined
default RED(14) to a missing key; the save writes CollarColor
unconditionally as a byte. Cat's write order is
variant→sound_variant→CollarColor, wolf's is CollarColor→variant→
sound_variant — the global entitySaveSeq takes cat's order
(selector probes are order-agnostic).

Pinned: TestHolderVariantParity adds a calico cat with CollarColor:5
(idx20 variant id3 + idx23 varint 5) and a wolf with grumpy sound +
CollarColor:9 (idx21 varint 9 + idx24 sound id4), plus four nbt=
probes pinning explicit values and the RED(14) default emit. Green
vs both.

Still open: pig DATA_BOOST_TIME, frog DATA_TONGUE_TARGET, cat
IS_LYING/RELAX_STATE_ONE (idx21/22 — set at runtime, never via
NBT), wolf interested/anger (idx25/26), addTrustedEntity via
taming.
## Slice — NeutralMob persistent anger (2026-09-29)

Measured: NeutralMob.addPersistentAngerSaveData writes
anger_end_time (long — absolute game-time the anger lapses, -1
unarmed) unconditionally and angry_at (single flat [I;a,b,c,d]
int-array uuid via EntityReference — NOT a list like fox
Trusted) when non-null. Reads accept anger_end_time verbatim, legacy
int AngerTime (converted to gameTime+value), and angry_at's
single int-array (a flat uuid is NOT the list shape Trusted
uses). Implementers: bee, enderman, iron_golem, polar_bear,
wolf, zombified_piglin — piglin/hoglin/zoglin do NOT carry the
keys (measured earlier). Only wolf packs DATA_ANGER_END_TIME
on the wire (idx22, long serializer 2, default -1L); the
zombified piglin stores the fields but has no anger accessor.
mc-go already emitted both save keys; gaps closed: wolf wire
accessor, the summon-tag read arms, angry_at's emit for a
NoAI summon-set target (the runtime provocation arm correctly
stays gated on !NoAI — a NoAI mob provoked by /damage keeps
both keys unarmed), and known-map/extras coverage for the
new read keys. An explicit-set sentinel (AngryUntilSet)
keeps a verbatim 0/-1 on anger_end_time distinguishable from
unarmed; legacy AngerTime converts to gameTime+value.

Pinned: TestHolderVariantParity adds an angry wolf
(anger_end_time:600L + angry_at flat array → idx22 "2:600"
wire) and an angry zombified_piglin (no wire anger pack),
plus nbt= probes for both save keys and the -1L unarmed
emit. Green vs both.

Still open: bee arm (flags + own DATA_ANGER_END_TIME idx19),
pig DATA_BOOST_TIME (runtime-only), frog DATA_TONGUE_TARGET,
cat IS_LYING/RELAX_STATE_ONE, wolf DATA_INTERESTED_ID,
addTrustedEntity via taming, AngerTime→absolute conversion
pin (gameTime-dependent — verified by read path only).
## Slice — bee fields + hive validation drop (2026-09-29)

Measured: Bee defines DATA_FLAGS_ID (byte: 0x08 nectar |
0x04 stung | 0x02 rolling) at wire idx18 and its own
DATA_ANGER_END_TIME (long, -1L default) at idx19. The save
writes hive_pos/flower_pos as storeNullable BlockPos.CODEC
[I;3] int-arrays (dropped when absent), then HasNectar/
HasStung bytes and the ints TicksSincePollination,
CannotEnterHiveTicks, CropsGrownSincePollination in order.
Reads accept the same keys (hive_pos/flower_pos as [I;]
int-arrays). Two tick counters move: aiStep decrements
stayOutOfHiveCountdown even under NoAI, while
customServerAiStep's ticksWithoutNectarSinceExitingHive++
(!hasNectar) is NoAI-gated — a NoAI bee's
TicksSincePollination freezes. And aiStep's tail drops
hivePos every 20th tick when it no longer names a hive
block entity (flower_pos has no such check).

Pinned: TestHolderVariantParity adds nectar/stung flag
bees (idx18 0x08/0x04), an angry bee (idx19 long 600),
the NoAI counter freeze ({TSP:5} positive + unless-probe),
an AI bee whose TicksSincePollination leaves 0 (unless-
probe), the CannotEnterHiveTicks decrement (unless-probe),
and the hive-validity drop (flower_pos survives, hive_pos
gone within 20 ticks). Green vs both. The rolling bit and
the goal-driven dropFlower/dropHive paths stay runtime-only.

Still open: frog DATA_TONGUE_TARGET, cat IS_LYING/
RELAX_STATE_ONE, wolf DATA_INTERESTED_ID, addTrustedEntity
via taming, AngerTime→absolute conversion pin.
## Slice — wolf BegGoal + summon roll gate + component either-codec (2026-10-01)

Measured: BegGoal (javap-verified 26.2) targets the nearest
player inside 8.0 blocks holding a bone or any wolf_food item
(= #meat + cod, cooked_cod, salmon, cooked_salmon, tropical_fish,
pufferfish, rabbit_stew), raises DATA_INTERESTED_ID (idx20,
boolean) and steers LookControl at the holder while
lookTime = adjustedTickDelay(40+rand(40)) counts down; it
stops when the player dies/moves past 8 blocks/stops holding
and clears flag+player. Vanilla's tick order runs sendChanges
BEFORE the living tick — a freshly-paired mob's first
sendChanges replays every non-default meta entry (define
dirty flags survive the attach pack), the attr-set replay
follows, then the first living tick's effects (the beg flag,
movement_speed's first-travel touch) land a tracker tick
later; e.firstTick gates both the BegGoal canUse and the
movement_speed touch so the emit order matches byte-for-byte.
Replay arms now exist for wolf (baby16, interested20,
collar21, anger22, variant23, sound24), bee (flags18,
anger19) and every holder-variant type (cat 20/23/24, frog
18, pig 19/20, cow 18/19, chicken 18/19) — pig replay was
previously silent on a rolled sound_variant.

Measured: SummonCommand.createEntity gates the explicit
Mob.finalizeSpawn call on its `initialize` arg — the brigadier
lambda for the NBT-tag overload passes false, so a TAGGED
summon never rolls variant/sound_variant (the define defaults
stand unless the tag sets them); only the bare overload rolls.
mc-go rolled unconditionally — fixed for both /summon and
/execute summon (always bare → rolls). Verified live: tagged
pigs emit the explicit sound_variant and nothing else, tagged
wolves ditto; vanilla's bare-summon rolls are world-seeded
deterministic (classic+classic repro'd twice) — explicit pins
are the only portable differential there.

Measured: ComponentSerialization's either-codec writes a
literal-text component ({"text":...}) as a STRING tag of the
JSON text on both the entity-data wire and the save map, but
a structured component (translate/score/...) as the full
compound — the killer-bunny auto-name renders
{translate:"entity.minecraft.killer_bunny"} while a summoned
'{"text":"Babe"}' stays quoted JSON. mc-go emitted a compound
for text components on the wire AND save (breaking
data-get/'data merge'/name revert and compound nbt= selector
probes for programmatic names); both encode sites now split on
componentIsLiteral. Fixes the whole pre-existing
TestDataEntityParity failure (CustomName wire + save + merge
loss + remove merge.failed + name-revert translate).

Pinned: TestWolfBegParity (beg on bone/wolf_food within 8
blocks → idx20 raise + look-lock; the NoAI wolf never begs;
swap to stick clears; re-beg after re-equip) and the now-green
TestDataEntityParity + TestVariantParity (killer-bunny
compound probe). Green vs both.

Still open: cat IS_LYING/RELAX_STATE_ONE (idx21/22), frog
DATA_TONGUE_TARGET, addTrustedEntity via taming,
AngerTime→absolute conversion pin (gameTime-dependent),
world-seeded RNG parity for bare-summon rolls (variant/
sound_variant/attr rolls — deterministic on vanilla per
world seed; mc-go rolls from its own stream).
## Slice — AngerTime→absolute conversion pin (2026-10-01)

Measured: NeutralMob.readPersistentAngerSaveData's legacy branch
(javap): anger_end_time long wins when present; else an AngerTime
int routes through setTimeToRemainAngry → setPersistentAngerEndTime(
level.getGameTime() + value). mc-go already converted at read —
the gap was the missing blackbox pin.

Pinned: TestAngerTimeLegacyParity summons a wolf {AngerTime:600}
and asserts anger_end_time − gametime lands in (540, 600] on both
servers — the conversion ran on the summon tick, a few ticks before
the query, so the delta sits just under 600 without depending on
world age. (Drive-by fix: data get entity needs a single-entity
selector — @e[limit=1] — vanilla rejects the multi form with
argument.entity.toomany.)

Still open: cat IS_LYING/RELAX_STATE_ONE (idx21/22), frog
DATA_TONGUE_TARGET, addTrustedEntity via taming, world-seeded
RNG parity for bare-summon rolls.
## Slice — pig DATA_BOOST_TIME + carrot_on_a_stick (2026-10-02)

Measured: ItemBasedSteering.boost (javap): already-boosting →
false; else boosting=true, boostTime=0, entityData.set(BOOST,
random.nextInt(841)+140) — one entity-data write per boost, the
accessor keeps its last value (re-emitted only to new trackers).
tickBoost (inside Pig.tickRidden — ridden-only) ticks boostTime++
until > total → boosting=false. FoodOnAStickItem.use (server):
getControlledVehicle → ItemSteerable + isPassenger + vehicle.
is(target) → boost(); on true hurtAndConvertOnBreak(
consumeItemDamage, FISHING_ROD) → SUCCESS_SERVER.
heldItemTransformedTo; every gate below boost returns PASS and
falls through to awardStat(ITEM_USED) — the stat lands on every
FAILED use, never on success. carrot_on_a_stick = (PIG, damage 7)
durability 25; warped_fungus_on_a_stick = (STRIDER, 1) 100; both
convert to fishing_rod on break. Pig metadata measured live:
baby idx16, DATA_BOOST_TIME idx18 (ser1), variant 19, sound 20.

Pinned: TestPigBoostParity mounts a saddled NoAI pig, uses
carrot_on_a_stick[minecraft:damage=20] twice and asserts a single
idx18 emit ∈[140,980] (the second use no-ops behind the boosting
gate) + the break resync delivering a fresh count-1 fishing_rod
into the held hotbar slot (vanilla resyncs via 0x12 set_content,
mc-go via 0x14 set_slot — asserted generically). mc-go:
useFoodOnAStick models the full gate chain (stat on fail only),
hurtAndConvertOnBreak does the damage+transmute, and the boost
rides the shared dirty-diff meta encoder via livingMeta.boost.

Still open: cat IS_LYING/RELAX_STATE_ONE (idx21/22), frog
DATA_TONGUE_TARGET, addTrustedEntity via taming, strider
DATA_BOOST_TIME+DATA_SUFFOCATING (ItemBasedSteering is shared —
the strider meta emit case doesn't exist yet), boost travel
physics (getRiddenSpeed·0.225·boostFactor), world-seeded RNG
parity for bare-summon rolls.
## Slice — strider DATA_SUFFOCATING + suffocating modifier (2026-10-02)

Measured: Strider.defineSynchedData (javap): DATA_BOOST_TIME idx18
(int) then DATA_SUFFOCATING idx19 (boolean) after the Animal chain —
no variant/sound holders. Strider.tick under !isNoAi: warm =
own-block or on-block is STRIDER_WARM_BLOCKS ({lava}) or lava fluid
height > 0, or the vehicle is a non-suffocating strider;
suffocating = !warm, entityData.set every tick (no-op writes land
as nothing). setSuffocating also flips a transient SUFFOCATING
movement_speed modifier (id "suffocating", -0.34
add_multiplied_base, base 0.175) — re-arming the update_attributes
resend, which carries the literal modifier id on the wire.
Suffocating is never persisted (no Strider save keys).

Pinned: TestStriderParity — summon saddled strider on land flips
idx19 true within a tick (+ update_attributes carrying the
"suffocating" modifier id), setblock lava beneath flips it back,
and warped_fungus_on_a_stick (strider, damage 1, durability 100)
boosts idx18 exactly once. mc-go: mobMoveStep computes the warm
predicate per tick (blockAt both positions + the lava fluid probe
+ the strider-stack parent check), mobSyncAttrs now folds every
live AttrMod into the emitted entry (previously only effect.speed
reached the wire — summon/attribute-command modifiers never
resynced on mobs).

Still open: cat IS_LYING/RELAX_STATE_ONE (idx21/22), frog
DATA_TONGUE_TARGET, addTrustedEntity via taming, boost travel
physics (getRiddenSpeed·0.225·boostFactor — strider's adds the
0.35/0.55 suffocating factor), world-seeded RNG parity for
bare-summon rolls.
## Slice — allay hand-off sounds + take-back (2026-10-02)

Measured: Allay.mobInteract (javap): give = MAINHAND <-
copyWithCount(1) + removeInteractionItem + playSound(ALLAY_ITEM_GIVEN,
NEUTRAL, 2.0, 1.0) + LIKED_PLAYER — an entity-targeted playSound, so
the wire is sound_entity (0x74), and the causer is the excluded
entity. Take-back = empty player hand + holding allay + MAIN_HAND:
setItemSlot(EMPTY) + ITEM_TAKEN + allay swing + removeAllItems thrown
at the allay's own position + LIKED_PLAYER erased + hand item
addItem'ed. InventoryCarrier.pickUpItem writes the inventory only —
MAINHAND is never touched by pickups or by GoAndGiveItemsToTarget's
throw (inventory.removeItem(0,1) per behavior tick = one item per
game tick). AllayAi.onItemThrown: gameTime%7==0 && random<0.9 plays
ALLAY_THROW vol 1.0 pitch ~ THROW_SOUND_PITCHES (18-entry list).

Pinned: TestAllayInteractParity — a second observer session watches
the same allay (the causer is excluded) and asserts the three
sound_entity emits: ITEM_GIVEN on give, ALLAY_THROW while inventory
throws to the liked player, ITEM_TAKEN on the empty-hand take-back.
mc-go: giveAllay now models both directions of the hand-off,
pickup/throw no longer clobber MAINHAND with the inventory stack
(the take-back fired never because the hand was already empty), the
throw gate dropped from 20t to vanilla's per-tick rate, and all
three sounds ride playSoundEntity not positional playSound.

Still open: cat IS_LYING/RELAX_STATE_ONE (idx21/22), frog
DATA_TONGUE_TARGET, addTrustedEntity via taming, boost travel
physics, breeze whirl/charge/jump/land (Brain AI), world-seeded
RNG parity for bare-summon rolls.
## Slice — equipment-break event bytes + join op level (2026-10-02)

Measured: LivingEntity.entityEventForEquipmentBreak's tableswitch is
keyed by the LivingEntity$1 switchmap — MAINHAND,OFFHAND,HEAD,CHEST,
FEET,LEGS,BODY,SADDLE → 47,48,49,50,52,51,65,68 (feet and legs swap
values against the natural reading; verified by the emitted bytes:
a feet/legs/head break walk leaves [52,51,49]). Player.hurtArmor
walks FEET,LEGS,CHEST,HEAD. PlayerList.sendPlayerPermissionLevel on
join broadcasts 24 + the player's op level — level-4 ops get 28.

Pinned: TestOffhandBreakParity — shearing a sheep with an offhand
shears one point from max leaves the [28,48] event sequence on both
servers. mc-go's wearSlot now resolves its event byte per slot via
breakEventForSlot (hotbar→47, offhand→48, armor→armorBreakEvents;
the elytra chest break at +1 now emits 50, was 47), and the join
op-level event sends opStatus(sess.opLevel()) instead of a flat 24.
TestArmorWearParity — red on master since it was written — is green:
the armor map was right all along, only the join event and the
non-armor wear path were wrong.

Still open: cat IS_LYING/RELAX_STATE_ONE (idx21/22), frog
DATA_TONGUE_TARGET, addTrustedEntity via taming, boost travel
physics, breeze whirl/charge/jump/land (Brain AI), world-seeded
RNG parity for bare-summon rolls.

## Slice — client-authoritative ridden steerables (2026-09-25)

Measured: ridden steerable movement is CLIENT-authoritative in 26.2.
Entity.isLocalInstanceAuthoritative inverts isClientAuthoritative,
which Entity delegates to getControllingPassenger — and
Player.isClientAuthoritative is constant true. So a controlled-ridden
vehicle's canSimulateMovement is false on a dedicated server:
aiStep's ridden branch runs travelRidden → tickRidden (welds
yRot=rider.yRot, xRot=rider.xRot·0.5, yHeadRot=yBodyRot=yRot) and
ItemBasedSteering.tickBoost, then zeroes the delta instead of
setSpeed+travel — the vehicle freezes mid-air until the driver's
serverbound move_vehicle packets carry its position.
getControllingPassenger itself gates on saddle + first passenger
being a player + Player.isHolding(food-on-a-stick, both hands).
ServerboundMoveVehiclePacket handling: silently dropped unless the
root vehicle's controlling passenger is the sender — no correction
packet for a missing stick (correction only on the dist²>100
"moved too quickly" branch). The legacy `Saddle:1b` summon key is
ignored in 26.2 — EntityHorseSaddleFix's datafixer only moves it to
equipment.saddle on old saves; live summon NBT is
equipment:{saddle:{id:"minecraft:saddle"}}.

Pinned: TestRiddenSteerableParity — mount the NoAI saddled
pig/strider, arm the rider with the matching stick, turn the camera
to (-160,20): the vehicle's Rotation welds to (-160,10) exactly and
its Pos stays frozen at spawn (8.5,-58,8.5) across a sprint on both
servers; a move_vehicle report advances z to 10.0; dropping the
stick unwelds (rider yaw -80 does not re-weld) and a second
move_vehicle is silently rejected. mc-go's mobRiddenStep now
freezes+welds instead of walking (the earlier server-side travel
was wrong for the dedicated-server model), the boost timer moved
inside the controlling-rider gate, handleMoveVehicle requires the
held stick for pig/strider, and the legacy `Saddle` summon read is
removed. TestStriderParity (suffocating emit counts) is a
preexisting timing flake — fails intermittently on bare master and
on this change on both legs.

## Slice — jukebox record lifecycle + allay dance/duplicate (2026-09-25)

Measured: JukeboxBlock.useItemOn — has_record→TRY_WITH_EMPTY_HAND
(any click ejects, disc or not); empty + JUKEBOX_PLAYABLE →
setTheItem → consumeAndReturn(1, creative-gated) →
notifyItemChangedInJukebox: flag-2 `has_record` write +
gameEvent(BLOCK_CHANGE). JukeboxSongPlayer: play → levelEvent(null,
1010, pos, songRegistryId); tick%20 → gameEvent(JUKEBOX_PLAY,
radius 10) + spawnMusicParticles (NOTE, count=0,
xd=nextInt(4)/24f, speed=1); hasFinished → stop (song!=null
guarded, so eject-after-finish must not re-emit 1011).
popOutTheItem: item entity at pos+(0.5,1.01,0.5)+offsetRandomXZ(0.7),
pickup delay 10, then notifyItemChanged(false)+songPlayer.stop.
Allay.setJukeboxPlaying: playing&&!isDancing → store pos + setDancing(true);
!playing → clear only when pos matches or none stored. setDancing
gates isClientSide, isEffectiveAi (NoAI stores pos, never flips
DATA_DANCING), and isPanicking; shouldStopDancing at %20 on null pos,
dist>notificationRadius, or block!=jukebox. aiStep heals 1.0 every
10 ticks even for NoAI. duplicateAllay: ALLAY.create(BREEDING) at
position (yaw/pitch not carried), PersistenceRequired,
resetDuplicationCooldown(6000) on both → addFreshEntity +
broadcastEntityEvent(18) + AMETHYST_BLOCK_CHIME (NEUTRAL, 2.0/1.0,
except=player) + consume(1). The event chain (EntityCallbacks →
DynamicGameEventListener.move → dispatcher.visitInRangeListeners)
has no NoAI/vibration gate — the only gate is setDancing itself.

Pinned: TestJukeboxParity — insert disc → blockUps≥1 +
level_event(1010,5) + in-radius DATA_DANCING(16)=true; eject →
item spawn + has_record=false + STOP_PLAY(1011,0) + dance clear;
NoAI in-radius allay never flips idx16 (stores pos only);
out-of-radius allay never dances. TestAllayDuplicateParity —
amethyst_shard interact on the dancing allay → fresh allay add +
entity_event 18 + chime + DuplicationCooldown≈6000 both parent and
child, second click no-op (cooldown), stick click no-op (tag gate).
Fixed under test: allayJukeboxEvent lacked the isEffectiveAi/isPanicking
gates on setDancing (NoAI allays danced); entity_event watcher read
eid as VarInt — the wire is Int.

## Slice — cat IS_LYING/RELAX_STATE_ONE + wake-gift (2026-10-03)

Measured: Cat$CatRelaxOnOwnerGoal (priority 3, before TemptGoal) —
canUse tamed && !orderedToSit && owner.isSleeping && dist²<100 &&
ownerPos block is BEDS → goalPos = ownerPos.relative(facing.
getOpposite()) (foot-side cell, NOT the head-behind). tick:
moveTo(goalPos,1.1) every tick; dist²(owner)<2.5 → onBedTicks++;
>16 → IS_LYING(21)+RELAX(22) off, else lookAt+RELAX on; out-of-range
→ IS_LYING off (goal keeps running). stop() on wake/sit/occupied:
both flags off, gift if owner.getSleepTimer()>=100 AND random <
cat_waking_up_gift_chance (0.7 in the 23667–362 night window), gift
pool rabbit_hide/rabbit_foot/chicken/feather/rotten_flesh/string@10 +
phantom_membrane@2 (62). Player.stopSleepInBed leaves sleepCounter=100
on a normal wake (forceful→0) — that's what makes the gift check
reachable at all.

Pinned: TestCatRelaxParity — 3-night differential loop: use_item_on
the bed head → sleeping_pos query → vehWatch meta22 then meta21 on
the cat → 5s night-skip → meta21=false + item spawn (p=.7/night).
Both legs green.

Fixed under test: stopSleeping now stores sleepTicks=100 on normal
wake (was 0 — the gift check could never fire); sleepTicks decays
100→110→0 while awake like vanilla's sleepCounter; catRelaxStep
dispatched before mobTemptStep (priority 3<4); goalPos direction
flipped to the foot cell; gift drop via spawnItemDrop so add_entity
broadcasts (bare entities.Spawn never emitted the item on the wire).
Harness: dataGetLineDirect now surfaces nbtpath.nothing_found
replies — the sleeping_pos query is a binary awake/asleep probe
immune to the command-queue reply lag that made Pos checks stale;
per-night wait-awake before each time set (a still-sleeping click is
an occupied no-op); pumpSleepClient/vehWatch read-deadline clears
so polled sockets don't leak 30ms deadlines into the next helper.

## Slice — frog DATA_TONGUE_TARGET / ShootTongue (2026-10-03)

FrogAi's TONGUE activity (ShootTongue) measured on the 26.2 jar:
FROG_ATTACKABLES sensor finds a FROG_FOOD entity — slime/magma_cube
at getSize()==1 — inside follow_range; start() sets
DATA_TONGUE_TARGET_ID (metadata 19, OPTIONAL_UNSIGNED_INT serializer
19 — wire value eid+1, 0 empty) and walks at the target with
speed-modifier 2.0. dist < 1.75 → entity.frog.tongue (NEUTRAL
2.0/1.0), Pose.USING_TONGUE(9) on metadata 6 (serializer 20), and the
target's deltaMovement = unit(target→frog)·0.75 — the yank — then
CATCH_ANIMATION. ≥6 ticks → entity.frog.eat + doHurtTarget
(mob_attack, attack_damage 10) — a size-1 slime (health 1²) dies on
the hit and rides remove_entities. ≥10 eat ticks → DONE → stop()
writes the target id back to 0 and the pose back to STANDING.

AbstractCubeMob NBT: Size = getSize()-1, so {Size:0} is the small
edible slime; setSize clamps 1-127 and setcubeMobHealth makes
max_health = size². DATA_SIZE is metadata 16 (int serializer),
defined default 1, emitted only when it moves off the default.

Implemented: internal/server/frog.go (frogTongueStep state machine +
stop/eat), Entity Pose + FrogTongue* fields + CubeSize, the livingMeta
diff emitting pose/variant/tongue for "frog" and DATA_SIZE for
slime/magma_cube, summon NBT Size→CubeSize+health=size².

Pinned: TestFrogTongueParity — frog + {Size:0} slime 2 blocks apart;
asserts meta19=slimeEid+1 ever-seen (the set→clear window is short
enough that last-value capture can miss it — intMetaMax), pose 9, a
set_entity_motion on the slime, its remove_entities, and meta19 back
to 0. Both legs green; the vanilla leg measures the true wire.
vehWatch learned serializers 19 (OPTIONAL_UNSIGNED_INT→VarInt) and 20
(POSE→VarInt) plus per-eid set_entity_motion counts.

Fixed under test: summon AttrBase was nil on cube mobs — the
max_health write panicked (nil-map); guarded like the horse/zombie
paths.

## Slice — fox addTrustedEntity via breeding (2026-10-03)

Fox.FoxBreedGoal.breed() measured on the 26.2 jar: the offspring's
trusted list fills from the love causes — addTrustedEntity(
animal.getLoveCause()) then addTrustedEntity(partner.getLoveCause())
when it's a different player; addTrustedEntity puts slot0 first then
slot1 (two max). EntityReference.of is the feeder's uuid, which on an
offline-mode server is the deterministic OfflinePlayer:<name> uuid —
byte-identical on both legs (verified live).

isFood is ItemTags.FOX_FOOD (sweet_berries/glow_berries) — already in
animalFood; the breed walk, love-clear, age-cooldown and xp orb were
already pinned by TestMobBreedParity. This slice lands the trusted
half: breedPair fills baby.FoxTrusted from the session uuids
(session first, offlineUUID fallback), and the existing full-pack
emit rides the baby-spawn metadata (idx20/21, ser13, bool+uuid).

Pinned: TestFoxTrustedBreedParity — two players each feed their own
fox (interact packets, sweet_berries mainhand); asserts the baby
fox's DATA_TRUSTED_ID_0/1 both populated AND the uuid set equals
{OfflineUUID(BOATA), OfflineUUID(BOATB)}. Both legs green.
vehWatch learned serializer 13 (bool + 16-byte uuid) captured
per-eid unscoped — the baby's eid isn't known ahead.

## Slice — per-slot equipment delta + aimed throw parity (2026-10-03)

LivingEntity.detectEquipmentUpdates measured on the 26.2 jar (decomp
LivingEntity.java:2953-3037): every living tick diffs each
EquipmentSlot vs lastEquipmentItems via ItemStack.matches — same item
+ components, count-insensitive — packs changed slots into ONE
ClientboundSetEquipmentPacket in enum order (MAINHAND, OFFHAND, FEET,
LEGS, CHEST, HEAD, BODY, SADDLE), sent to tracking players only
(owner excluded). handleHandSwap: when BOTH hands changed and each
new stack matches the other's previous slot, the pair leaves the
delta and vanilla emits ClientboundEntityEventPacket(this, (byte)55)
instead, then updates the baseline — no set_equipment for the hands.

mc-go's player path resend the full 6-slot set from every mutation
site (held switch, selectHotbar, container click, durability break,
item replace); the mob path already had EquipDelta. Now: playerTick
runs detectEquipmentUpdates per spawned session — equipMatch
(normalized-empty, zeroed-count, deep-compared Contents) diffs the
baseline, the hand-swap case emits entity_event 55 to trackers, and
remaining slots go out as one packed delta. The redundant per-site
broadcastEquipment calls were dropped; the join path still writes
the baseline + full set via broadcastEquipment.

Pinned: TestPlayerEquipDeltaParity — held0→1 emits exactly one
{slot0,dirt} packet; held1→2 one {slot0,empty}; empty→empty emits
ZERO; `item replace armor.head` one {slot5,leather_helmet}; offhand
torch + player_action SWAP_ITEM_WITH_OFFHAND (action 6 + trailing
sequence varint — vanilla silently misparses without it) emits
entity_event 55 and ZERO set_equipment. Both legs green.

Surfaced while bisecting a TestAnvilParity regression: the failure
was not the hook — mc-go's container THROW scattered the item with
symmetric ±1-block drift (spawn jitter + random MotionX/Z), so a
thrown stack occasionally wandered back inside the ~1.425 pickup
box and re-entered inventory; vanilla's aimed throw (eye−0.3 spawn,
velocity look*0.3 + up 0.1 + swirl ≤0.02) lands predictably ahead.
dropFromMenu now uses vanilla's createItemStackToDrop model, and
reset() kills leftover item entities between stages.

Note: TestArmorStandInteractParity fails on base master too
(preexisting — swap-chain order/count differs), needs its own look.

## Slice — armor-stand emit dedup + voluntary_exile trigger ordering (2026-09-29)

Two real bugs pinned by existing differential tests, no new test file
needed.

**Spawn-time equipment emit dedup.** broadcastFreshMob ran THREE emits
for an equipped armor stand — the pairing bundle (sendEntityTo), a
direct sessions.Range set_equipment, and the first-tick EquipDirty
flush — while vanilla emits exactly two (sendPairingData + the
empty-baseline detectEquipmentUpdates). The middle emit was removed;
EquipDirty stays so the first tick replays the packed set.
TestArmorStandInteractParity — failing preexisting on master per the
prior slice's note — is now green.

**voluntary_exile kill trigger read the post-drop slot.** The death
drop loop deleted each dropped slot from e.Equips
(dropCustomDeathLoot), and the player_killed_entity dispatch read
Equips[equipHelmet].Ominous after damageMob returned — always false.
Vanilla fires the trigger inside die() before dropAllDeathLoot
empties the slots. Fix: the four kill call sites (primary melee,
sweep splash, spear, projectile) capture the head flag before
damageMob and pass it through onPlayerKilledMob /
firePlayerKilledEntityDamage. All three diffs in TestCaptainParity
(missing def, missing progress, missing chat announce) collapse to
this one cause — now green.

Also measured during the same run: vanilla announces BOTH
kill_a_mob and voluntary_exile on a captain kill — the earlier
"extra announce" reading was the missing grant, not over-announce.

Flakes noted this session, unrelated to the slice: TestKillAdvParity
once showed mc-go granting zero on the zombie leg (entity died
without a player-creditable source — likely suffocation at spawn);
passes on rerun on both master and this change. TestCaptainParity's
deterministic failure was the real bug above.

## Slice — item_durability_changed advancement pin (2026-09-29)

The trigger was already implemented end to end — fireItemDurability-
Changed rides hurtAndBreak (durability.go) and hurtAndConvertOnBreak
(ride.go), matching the only advancement that uses it: nether/
ride_strider's used_warped_fungus_on_a_stick criterion (item =
warped_fungus_on_a_stick + player.vehicle.entity_type = strider).
What was missing was the pin.

Pinned: TestRideStriderAdvParity — survival join, saddled-strider
summon via equipment.saddle NBT, empty-hand interact mount, then
use_item on open air. Vanilla grants def + progress + the task
announce; the whole advFlush (defs, progress entries, system-chat
keys) diffs clean vs mc-go. Verified: the criterion's two legs
(item name + ridden vehicle type read through Session.riding) match
vanilla's ItemPredicate + entity_properties(vehicle) evaluation.

Closes the item_durability_changed line in the 2026-09-20 tool-wear
slice. The same note's still-open items: attack-cooldown scaling and
unbreaking-tier rolls (unbreakingReduce exists but the vanilla
enchanted-reduce roll is unmeasured).
## Slice — fishing bobber entity-hooking + retrieve parity (2026-09-29)

Cast model rewritten to vanilla's FishingHook constructor, measured
from 26.2 decompile: spawn = (x - sin(yaw)·0.3, y + 1.62, z + cos(yaw)·
0.3) — pure-yaw horizontal offset, NOT along the look vector; initial
delta = (-sin(yaw), clamp(-tan(pitch), ±5), cos(yaw)) scaled PER
COMPONENT by (0.6/dist + random.triangle(0.5, 0.0103365)) with three
independent draws — total cast speed ~0.92, not the 0.6 mc-go had.
Bobber yaw/pitch derive from the post-jitter velocity, and the cast
uses the use-item packet's own yaw/pitch floats, not tracked rotation.
Vanilla emits no spawn-time idx8; the emit only fires on hook/clear.

Hooking: while FLYING the movement ray's first living-or-item entity
within the swept AABB becomes hookedIn — idx8 emits eid+1 (Vanilla
serializer = plain VarInt). HOOKED_IN_ENTITY rides hookedIn.getY(0.8);
when the hooked entity dies/leaves, hookedIn clears and idx8 re-emits
0. retrieve() pulls the entity, broadcasts entity_event 31, and wears
the rod: 5 for a mob/player, 3 for an ItemEntity.

Two measured non-obvious behaviors: (a) vanilla's onGround is NOT
latched — Entity.move() recomputes per tick, so a dry-landed bobber
oscillates grounded↔airborne and a bank retrieve wears 0-or-2 by tick
phase (both outcomes pinned); (b) the lured/hooked/nibble countdown
only runs while currentState == BOBBING, which only water entry sets
— a dry-landed bobber never ticks its timers.

Test surfacing (TestFishingHookParity, green vs both): creative-mode
hurtAndBreak is a no-op — the vanilla harness world defaults to
gamemode=creative, so the fixture must `gamemode survival` the caster
for rod-wear reads. Also fixed a real harness race: the chat-sink and
vehWatch goroutines reading the same conn split framed packets and
silently desynced BOTH readers — query commands now ride a dedicated
third connection.

Honest caveat: ItemEntity-hook (wear 3) and player-hook are
implemented per the decompiled source but unpinned — the block clip
truncates the ray first, so a grounded item is effectively
unhookable, and no player-hook fixture is built.

## Slice — fishing catch path: catchingFish model, loot, XP, emit shape (2026-09-29)

Ported vanilla's full catchingFish model from FishingHook decompile:
lured = nextInt(100,600) − lureLevel·5; a per-tick fishingSpeed
integer (1 +25% rain roll −50% no-sky roll) decrements it; splash
teases fire while lured with the <60/<40/<20-tick ramp (random angle,
dist 25-60·0.1, water-block gated). On lured expiry hooked =
nextInt(20,80) and the fishing-approach stream runs (fishAngle +=
triangle(0,9.188)°/tick, two crossed FISHING at hooked·0.1 offset,
15% BUBBLE, water-gated). On hooked expiry: splash sound, BUBBLE +
FISHING bursts of count 1+width·20=6, DATA_BITING (idx9) emits true,
and nibble = nextInt(20,40) opens the reel-in window; expiry clears
idx9 and both timers. openWater resets true while bobbing, else
latch-ANDs with outOfWaterTime<10 && calculateOpenWater — now the
full four 5x5 layers y −1..2 (was three). Dry-landed bobbers skip
the whole model (timers only run while in water).

retrieve() while biting rolls gameplay/fishing.json with quality·luck
weights (junk 10−2·luck, treasure 5+2·luck gated on openWater, fish
85−luck; luck = luck_of_the_sea level read off ItemStack.Ench), throws
the catch with delta·0.1 and vy += sqrt(dist)·0.08 (fixed a real bug:
was dist·0.08), spawns the 1-6 XP orb at owner+0.5, and wears 1.
Metadata emits now match vanilla's one-dirty-field-per-packet shape —
idx8 and idx9 each ride alone (fishhook pin reads idx8 only and still
passes). Blackbox: TestFishingCatchParity casts into an open-water
pool at tick rate 40, waits for idx9=1, reels inside the nibble window,
and pins on both servers: biting emit single-field, item + experience_orb
spawns, rod damage exactly 1. vehObs gained unscoped bitHist/metaHist/
partHist captures for the pins.

Caveats: the catch's looted stack id isn't pinned (the draw is
probabilistic; the item-entity spawn is). Tease/approach particles are
recorded but not asserted — their rates are roll-gated and noisier
than the differential is worth. The retrieve-miss path (no bite, just
the bobber discard) is unchanged and unpinned here.

## Slice — frog ShootTongue parity: sensor, tongue emit, eat-kill loot (2026-09-29)

Ported ShootTongue + FrogAttackablesSensor from the 26.2 decompile:
the FROG_FOOD sensor is a fixed TARGET_DETECTION_DISTANCE 10.0 (not
follow_range) + VISIBLE — nearest size-1 slime/magma_cube inside 10
blocks with an eye-to-eye LoS (mc-go uses the collider clip as the
LoS walk). MOVE_TO_TARGET walks at speedModifier 2.0; inside
EATING_DISTANCE 1.75 the tongue sound plays, pose flips to
USING_TONGUE(9), and the target is yanked at normalize·0.75; CATCH
(6t) runs eatEntity's doHurtTarget — a mob_attack with the frog as
direct+cause entity (frog attack_damage 10) — EAT (10t) then stop()
clears the tongue target + pose. LastDamageCause now records the
killer entity so loot tables can gate on it.

Loot transcription: slime.json's pool is cubeSizeExact:1 with two
entries — slime_ball 0-2+looting under the INVERTED frog-kill gate
(damage_source_properties source_entity entity_type != frog) and a
deterministic slime_ball x1 under the frog-kill gate — a frog-eaten
size-1 slime always drops one slime_ball. magma_cube.json's pool:
magma_cream uniform(-2,1)+looting gated size>=2 AND !frog-kill, plus
pearlescent/verdant/ochre_froglight x1 gated frog-kill + the frog's
variant holder (warm/cold/temperate). spawnMobLootPools now filters
each entry's conditions into the valid list then weighted-picks —
matching LootPool.addRandomItem semantics (conditioned-fail entries
simply aren't in the draw).

Blackbox: TestFrogTongueParity runs a temperate frog + size-1 slime
and a warm frog + size-1 magma_cube on both servers and pins the
ordered DATA_TONGUE_TARGET_ID emits (idx19 = prey eid+1, then 0 on
stop), the USING_TONGUE(9) pose emit on the frog, the prey motion
yank, the prey removal, and the deterministic drop id
(slime_ball / pearlescent_froglight). Green vs vanilla and mc-go.

Harness fix that mattered: vehObs's metadata parser bailed at the
first serializer id it didn't handle (default: goto done) — the
frog's idx18 frog_variant rides serializer 27, so any metadata packet
carrying a variant before the tongue field silently dropped the
tongue entry. The emit was on the wire the whole time; added the
variant-holder serializers (21-31) and varlong (2) as VarInt reads.

Known gaps still open: frog Croak pose activity (60 ticks, measured
but unimplemented), UNREACHABLE_TONGUE_TARGETS (5-entry, 100-tick
cooldown), and the magma_cube's own hop-AI/chase parity (the frog
chases a hopping cube across the flat in the meantime — the eat
still lands, slower than vanilla's discrete jump-navigation).

## Slice — frog Croak + ShootTongue timeout (2026-09-29)

Croak is the IDLE RunOne's weight-3 entry: start gates WALK_TARGET
absent + pose STANDING + !isInLiquid; it flips pose CROAKING(8) for
exactly 60 ticks, then stop() lands STANDING(0) and the RunOne
repicks immediately (the weighted draw is 3/5 croak vs stroll+
lookwalk once the triggerIf(onGround) no-op's same-tick repick is
folded in — croaks chain 36% of the time). mc-go folds the draw into
the stroll repick: 3/5 croak roll, 60-tick pose, and the next repick
armed for the tick after a croak ends — matching vanilla's
pick-on-finish cadence rather than the ~6s wander interval.
ShootTongue's extra start conditions refuse pose CROAKING, so a
running croak outranks a pending tongue; a running tongue's
WALK_TARGET blocks the croak pick the other way. The ShootTongue
behavior itself now caps at TIME_OUT_DURATION 100 for the whole
move+catch+eat run — a timed-out chase stops, clears idx19 and the
pose, and the sensor is free to repick the same visible target.

vehObs gained a poseHist capture (idx6/ser20 emits as "eid:v") for
the pin. Blackbox: TestFrogCroakParity summons one lone temperate
frog with do_mob_spawning off (a stray slime would flip TONGUE),
waits for the CROAKING(8) emit, freezes, and single-steps: the pose-0
emit lands inside 61 steps on both servers (vanilla 56, mc-go 58 —
the croak had already run a few wall ticks when the freeze landed).
Emit sequence is strictly 8 then 0 on both, no USING_TONGUE flap.

Caveats: UNREACHABLE_TONGUE_TARGETS (5-UUID FIFO, 100-tick expiry)
has no observable wire signature in mc-go's model — with no nav
graph, the sensor's LoS clip is the only reachability signal and a
visible-but-unreachable target can't be distinguished, so the memory
is folded into the LoS gate rather than built as a dead structure.
The cube hop-AI (slime/magma_cube jump navigation the frog chases)
is still open — a live chase overshoots vanilla's discrete hops.

## Slice — cube hop-AI parity: slime/magma_cube discrete hops (2026-09-29)

Measured AbstractCubeMob + MagmaCube + Slime + MoveControl + Mob
(setSpeed → setZza) + LivingEntity (air influence speed*0.216 on the
friction-1.0 arm) on 26.2:

- CubeMobMoveControl: rotlerp(yRot, heading, 90°) toward the move
  heading each tick; grounded arm sets the speed field to
  speedModifier*(0.2+0.1*size), decrements jumpDelay — on <=0 it
  resets (slime rand(20)+10, magma *4, /3 while aggressive), fires
  JumpControl.jumpFromGround (vy = jump_strength 0.42, magma adds
  size*0.1; magma's lava jumpInLiquid is 0.22+size*0.05) and plays
  the jump sound; if the delay hasn't elapsed the move control
  zeroes xxa/zza outright (slimes never slide on the ground).
  Airborne: MOVE_TO stays armed so zza keeps being re-issued at the
  drive, and moveRelative's input only normalizes above length 1 —
  a sub-1 drive stays sub-1 — while the air influence is
  LivingEntity.getFlyingSpeed, a flat 0.02 for a mob (only a
  player-controlled ride gets getSpeed()*0.1).
- Heading: AttackGoal picks the nearest survival-mode player
  within 10 blocks and |dy|<=4 (else an iron golem inside
  follow_range) and yRot = lookAt bearing; RandomDirectionGoal
  repicks a 40+rand(60)-tick timer and a fresh 0-360 heading only
  while targetless AND (onGround || inFluid || levitating).
  keepOnJumping simply arms MOVE_TO every tick while not a
  passenger.
- Landing edge (onGround && !wasOnGround): size*16 individual
  addParticle emits (item_slime for slime, flame for magma) each at
  a radius*rand(0.5..1.0) ring offset + the squish sound; liftoff
  latches targetSquish=1 (client squish anim only).
- Sounds: volume 0.4*size, pitch ((r1-r2)*0.2+1)*(tiny?1.4:0.8)
  with squish an extra /0.8, HOSTILE category. slime attack 1493,
  jump 1496, squish 1497 (tiny 1548/1549); magma jump 965, squish
  966/967.
- isDealsDamage: slime size>1 && !NoAI; magma always !NoAI.
  playerTouch box-overlap deals mob_attack (slime size, magma
  size+2) + slime attack sound. movement_speed 0.2+0.1*size via
  setSize.
- Wire shape: needsSync emits set_entity_motion carrying the
  POST-physics delta — the hop lands on the wire as
  vy = 0.42*0.98-0.08 = 0.332 (slime) / 0.528 (magma size-2), not
  the launch vy, because sendChanges runs after the tick's gravity.

mc-go gained cubemob.go: cubeHopStep reproduces the whole chain
(jump delay, heading pick/lock, 90° rotlerp, zeroed-input wait,
zza=drive drive at ground influence and the flat-0.02 air influence,
landing particle loop + squish, touch damage),
cubeJumpDelayFor/cubeDealsDamage/cubeTarget/cubeSound/cubeLand/
cubeTouchDamage helpers, Entity fields CubeJumpDelay..CubeJumpSeq,
and the mob.go dispatch for slime+magma_cube. vehObs gained motVec
(set_entity_motion decoded as 26.2's LpVec3 — earlier short-parse
read garbage like vy=4.096).

Blackbox: TestCubeHopParity — frozen single-steps count hop-motion
emits and pin the observed vy (slime ~0.33 over 80 steps, magma
~0.53 over 200), then a survival player next to a size-2 slime
takes the touch hit. Both legs green: vanilla slime 6 hops vy
0.333, magma 4 hops vy 0.529, health 20->18; mc-go slime 4 hops vy
0.333, magma 6 hops vy 0.529, health 20->17 (hop counts vary with
the random delay — the pinned values are vy, per-hop travel within
2x of vanilla, and the damage).

Correction folded into this entry: the first pass had the input
normalized to unit (zza=1) and the air influence at getSpeed*0.216 —
both wrong per Entity.getInputVector (normalizes only length>1) and
LivingEntity.getFlyingSpeed (flat 0.02). The wire pin now also sums
posNet horizontal travel per hop and compares legs.

## Slice — melee attack goal parity: aggressive flag, reach, knockback (2026-09-29)

Measured MeleeAttackGoal + ZombieAttackGoal + Mob
(isWithinMeleeAttackRange/doHurtTarget) + LivingEntity.knockback on
26.2:

- The goal's tick: lookAt(30,30), repath on a 4+rand(7)-tick timer
  (+10 beyond 32 blocks, +5 beyond 16, +15 on moveTo failure), then
  attack on a 20-tick adjusted cooldown gated on
  isWithinMeleeAttackRange — the mob's box inflated by
  DEFAULT_ATTACK_REACH = sqrt(2.04)-0.6 ~= 0.828 on XZ only,
  intersected against the target hitbox (full y-extents must
  overlap). Zombie reach vs a player: 0.3+0.828+0.3 ~= 1.43.
- doHurtTarget: swing packet + attack_damage hurt + post effects;
  hurtServer's generic arm shoves the victim 0.4 away from the
  attacker's position — knockback(x/2 - 0.4*dir, min(0.4, vy/2+0.4)
  when grounded, z/2 - 0.4*dir) — the emit lands on the victim's
  own connection as set_entity_motion.
- ZombieAttackGoal (zombie/husk/drowned/zombie_villager/
  zombified_piglin) pulses Mob's DATA_MOB_FLAGS byte idx15 bit2:
  raiseArmTicks >= 5 && ticksUntilNextAttack < interval/2, reading
  the post-attack cooldown — the byte is dark on the swing tick
  itself and lights for the last 10 of the 20-tick cycle. Plain
  MeleeAttackGoal latches the flag for the whole run on every melee
  mob except the Brain-driven bee.
- Mob.setAggressive is runtime-only — not persisted to NBT.

mc-go: mobMeleeStep now computes the flag every goal tick (marked
MetaDirty on flips so packDirty's idx15 emit fires — the emit was
already wired but unreachable without the dirty mark), uses the
vanilla reach formula (was width+0.6+0.3 ~= 1.5, now
width/2+0.8284+0.3), and the y-overlap gate replaces the flat |dy|<2.5.
Entity gained Aggressive/MobArmTicks; mobFlagsByte gained bit2;
zombieAttackTypes maps the five ZombieAttackGoal users. vehObs
gained flagsHist (idx15/ser0 byte emits) and posNet travel capture
in the previous entry.

Blackbox: TestMobMeleeParity — a zombie 5 blocks off a survival
player chases in, and the window is pinned for >=3 health drops,
the idx15 bit2 pulse (at least one set and one clear), and a
hurt-knockback motion emit. Vanilla: hits=4 flagSet=2 flagClear=5
knock=4; mc-go: hits=4 flagSet=5 flagClear=4 knock=4.

## Slice — baby voicePitch wire parity (getVoicePitch's isBaby arm)

Vanilla LivingEntity.getVoicePitch: (nextFloat() - nextFloat()) *
0.2 + 1.0 — baby mobs pitch the same call at +1.5 instead of +1.0
(the isBaby arm). Every ambient/hurt/death voice rides it, so a
baby zombie's hurt cries land inside [1.3,1.7] on the wire while
an adult's sit in [0.8,1.2]; step sounds stay at their fixed 1.0.

mc-go: voicePitchE already implemented the model for every voice
path (ambient, hurt, death) — the gap was the pin. vehObs now
captures the sound id + pitch off named_sound packets, and
TestBabyPitchParity buckets on the zombie voice ids only: wire
ids 1870/1875/1882 for ambient/death/hurt — playSoundSrc's +1 on
write lands mc-go's table ids (1869/1874/1881) on the wire at the
same numbers vanilla emits.

Blackbox: baby zombie {IsBaby:1b} + adult, two /damage hits and a
kill each; every voice pitch must sit in the band. Both legs:
baby ~1.46/1.52, adult ~0.86-1.14. Second hits inside the
ten-tick hurt grace are silent on both servers — queue jitter can
slide a hit into it, so the pin demands >=1 voice per phase, not
a count match.

Findings: (a) command sends on different connections race — the
summon queued on BOATA while BOATC's data get saw zero zombies
(argument.entity.notfound.entity, then hp=20 unhurt); ordering-
dependent commands must share one conn. (b) /damage dealt 1 on
mc-go vs 2 on vanilla against a bare zombie — armor-absorb delta,
investigate as a separate slice.

## Slice — bee sting + anger metadata emits (26.2)

NeutralMob provocation + the full bee-sting sequence now emits on the
dirty gate, and `TestBeeStingParity` pins every beat vs both servers:

- Provocation (real `sendAttackEntity` punch): `AngryAt`/`AngryUntil`
  arm → `DATA_ANGER_END_TIME` idx19 varlong emit of the absolute end
  tick — `MetaDirty` marks added at the provoke site (mob.go ~4286),
  the lapse-clear branches (~4706), and both anger-clear sites in
  beehive.go.
- `Bee.customServerAiStep`'s `setRolling` implemented: bit 0x02 of
  `DATA_FLAGS_ID` idx18 while `isAngry && !hasStung && target d²<4` —
  vanilla emits `idx18=2` on approach before the sting.
- Post-sting: `Stung`/`AngryAt` clear marks `MetaDirty` → vanilla packs
  `idx18=4` + `idx19=-1` in one 18/19 packet; mc-go now emits both.
- Harness: vehObs gained `beeFlHist` (idx18 ser0) + `angerHist`
  (idx19 ser2 — **VarLong**, not VarInt: `-1` is a 10-byte LEB and
  VarInt misreads/drops it, which hid vanilla's clear emit).

## Slice — tadpole/fish survival + FishSwimGoal parity (26.2)

`TestFrogspawnHatchParity` is green vs both servers. Two real bugs the
test surfaced:

- mobBoundsTable missed every aquatic mob: the [0.6,1.8] player-box
  fallback put the in_wall eye probe (Y + h*0.85) ~1.5 cells above a
  tadpole's body — it read the grass rim instead of the water and
  suffocated every hatch within seconds. All 12 aquatics now carry
  their EntityTypes.sized() dims (tadpole 0.4x0.3, axolotl 0.75x0.42,
  nautilus family 0.875x0.95, ...).
- Fish used the land RandomStrollGoal: ±10 ground targets with a
  ledge rule, which walked tadpoles out of the pad. Vanilla fish run
  AbstractFish.FishSwimGoal = RandomSwimmingGoal(1.0, 40) — the
  getRandomSwimmablePos draw retries DefaultRandomPos <=10x for a
  water-pathfindable cell. New fishSwimStep: 40-60 cadence, 10-draw
  water-cell target pick, no ground-support requirement (box is
  fluid-borne), stroll ends on solid-block contact.
- Probe fix: distance=..4 under-covered the 6x6 pad (corner ~4.5) —
  widened to ..6; mc-go's survivors were in the far corner.

## Slice — wolf/cat/parrot tame + sit parity (26.2)

`TestWolfTameParity` is green vs both servers. The slice surfaced a
26.2 semantics change AND a real interact-routing bug:

- orderedToSit is a plain field in 26.2 — the idx18 flags byte packs
  inSittingPose (0x01), applied only by SitWhenOrderedToGoal /
  readAdditionalSaveData / the cat lie goals. A NoAI wolf's tame emit
  is flags=4 alone (setOrderedToSit moves no wire bits); with AI the
  pose bit lands 4→5 a tick later. mc-go conflated the two — now
  e.Sitting = orderedToSit (NBT only), e.SittingPose = wire bit.
- SitWhenOrderedToGoal (pri 2, wolf/cat/parrot) implemented as
  tamableSitPoseStep: pose tracks the field except !onGround /
  isInWater starts; a sitting tame also drops out of the land stroll
  (goal holds MOVE+JUMP).
- Owner sit-toggle clicks are wire-silent in 26.2 — the field flips
  but nothing emits; Sitting:1b→0b shows only on data get. The
  differential pins that silence (non-owner click moves nothing).
- Real bug: mc-go ran tameAnimal AND tamableSitToggle on the same
  click — the consume that tamed also un-sat (bone isn't wolf_food,
  so the toggle fired). Vanilla's SUCCESS_SERVER consume prevents the
  toggle arm entirely; tameAnimal now returns consumption and the
  toggle only runs on an unconsumed click.
- Also gated: Wolf's untamed arm refuses bone while isAngry
  (AngryAt non-empty); summon {Sitting:1b} mirrors vanilla's
  setInSittingPose(orderedToSit) load path (pose set on spawn pack).
- Measured and pinned per-species tame odds already: wolf/cat 1-in-3
  with auto-sit-order, parrot 1-in-10 standing.

## Slice — collar dye parity + the cat/wolf same-color divergence (26.2)

`TestWolfTameParity`'s dye phase is green vs both servers (idx21 wolf
/ idx23 cat emit + CollarColor NBT + consume + the two swallow cases
+ the cat same-color toggle). Collar metadata/NBT/summon-read were
already in place; the missing piece was the interact arm itself:

- New tamableCollarDye (breed.go): owner + <color>_dye + color!=collar
  → repaint + consume(1) + persistenceRequired + MetaDirty. Called
  before the sit toggle in handleInteract, matching vanilla's inTame
  arm ordering.
- The fallthroughs differ per species and both are pinned: wolf's
  same-color dye falls PAST the toggle arm (it lives in the !dyes
  branch) — a silent no-op — while cat's same-color dye falls INTO
  the owner toggle and flips orderedToSit. Non-owner dye swallows on
  both (super.mobInteract → no isOwnedBy).
- CollarColor legacy codec = BYTE (id 0-15), RED(14) defined default;
  dataentity already emitted it unconditionally — repaint just sets
  CollarColor+Set.

## Slice — wolf armor interact + hurtServer unsit + tame health

- `wolfArmorInteract` in `handleInteract`'s tamable chain pins
  `Wolf.mobInteract`'s two body-slot arms: owner + wolf_armor on a
  tamed adult with an empty BODY equips `equipment.body` and
  consumes one (survival/adventure only); owner + armadillo_scute on
  a sitting wolf with damaged wolf_armor repairs
  `int(maxDamage*0.125)` = 8 of 64, `shrink(1)`s in every mode, and
  plays `item.wolf_armor.repair` (wire id 1795).
- `Manager.Damage` now mirrors `Wolf.hurtServer`: every accepted hit
  clears `orderedToSit` before `actuallyHurt` — even when the armor
  absorbs — so a struck wolf stands (idx18 5→4) and has to be
  re-seated before the repair arm's `isInSittingPose` gate accepts.
- `tameAnimal` applies `Wolf.applyTamingSideEffects`: taming a wolf
  resets max_health's base to 40 and fills it (wild base is 8).
- `TestWolfTameParity` extends into the armor leg: equip →
  `equipment.body.id` answers, iron-sword hits drop wolf_hp at 40
  while `equipment.body.components."minecraft:damage"` climbs,
  `Sitting` flips to 0 through the hits, a scute click on the
  standing wolf falls to the owner toggle (re-orders the sit — pose
  bit relands on the wire), and the second click repairs damage
  7→0 with the repair sound + one scute consumed — green vs vanilla
  (`armor=true:7→0 hp=40 unsit=0 reposed=true snd=true scute=3`)
  and mc-go (`armor=true:9→1 hp=40 unsit=0 reposed=true snd=true
  scute=3`).

## Slice — equine metadata chain + camel dash + vehicle player_command (26.2)

- Equine metadata: AbstractHorse extends Animal (NOT TamableAnimal),
  so the family's chain is Entity 0-7, LivingEntity 8-14, Mob 15,
  AgeableMob 16-17, DATA_ID_FLAGS **18** (byte; TAME 0x02 — BRED/
  EATING/STANDING/OPEN_MOUTH bits ride unmodeled animations), then
  per-subtype: Horse DATA_TYPE_VARIANT 19 (int type|markings<<8),
  llama family DATA_STRENGTH_ID 20 + DATA_VARIANT_ID 21, Camel
  DATA_DASH 19 + LAST_POSE_CHANGE_TICK 20 (long — unmodeled). Wired
  in the attach pack + diff emit + metaSent baseline.
- `entityAttrBases` now carries the full resolved supplier sets for
  donkey/mule/llama/trader_llama (chested set: horse rows with jump
  0.5, speed 0.175) + skeleton_horse (15hp/0.2 speed) + zombie_horse
  (25hp) — a tagged `/summon` skips finalizeSpawn's attribute rolls,
  so vanilla emits the unrolled bases (horse health 53.0 not the
  hardcoded 25, llama 53.0 not the 20 fallback). `summonHealthDefault`
  deleted; `mobHealth["horse"]=53`.
- `/data merge` now applies `Tame` (flags-byte flip), `ChestedHorse`,
  `Strength` (clamped [1,5]) + `Variant` (llama id `v&3`, horse
  packed int) instead of dropping them into ExtraNBT.
- `handlePlayerCommand` wires START_RIDING_JUMP (action 3) +
  STOP (4, no-op) + OPEN_INVENTORY (5). `canJump` = isSaddled
  (camel adds `!refuseToMove` — unmodeled sit). Camel's
  handleStartJump: CAMEL_DASH positional (262) + entity_action
  gameEvent + DASH=true; the data write rearms `DashCooldown`=55
  (onSyncedDataUpdated — every DASH write while 0 rearms), tick()
  clears DASH only at cooldown<50 AND grounded, and decays to
  CAMEL_DASH_READY (263) at 0. Measured: a mounted NoAI camel keeps
  DASH on for the whole ride (its server-side onGround mirrors the
  driver's move_vehicle packets; never grounded → never clears) —
  the move_vehicle probe that reports ground clears it next tick.
- Non-camel equines play their family jump sound at 0.4/1.0:
  donkey→535, mule→1026, skeleton_horse→1488 in water else horse's
  864 (llama/zombie_horse inherit horse).
- Camel mounts regardless (isTamed() is literal true — fixed
  mountAllowed + sendMountMenu's isTamed arm; camel still opens the
  mount screen saddled or not).
- `TestEquineMetaParity` verbatim-compares the attach/diff metadata
  stream on six summon+merge cases vs both servers;
  `TestCamelDashParity` pins [eid:1]-persists-while-ridden +
  262+263 sounds + the move_vehicle clear + data=0 silence — green
  vs vanilla and mc-go.
- Axolotl-family `Variant` merges now decode through the enum
  LEGACY_CODECs: axolotl's continuous-ZERO drops out-of-range ids to
  LUCY, parrot CLAMPs to [0,4], rabbit's sparse map accepts
  {0..5,99} else BROWN (and id 99 auto-renames to the killer_bunny
  translate component), fox's `Type` string decodes "snow" else RED
  with the Sleeping/Sitting/Crouching fflags byte, and the tropical
  fish repacks Variant through (pattern|baseColor|patternColor) with
  sparse-pattern and continuous-ZERO DyeColor fallbacks — so
  `Variant:7` emits 0. The summon NBT path applies the same decodes.
- WaterAnimal air parity: fish/squids/tadpole/axolotl/nautilus run
  handleAirSupply — out of water the pre-tick supply drops 1/tick
  (LivingEntity's +4 refill is overwritten), in water it snaps to
  300, and at -20 it resets with 2 damage — `drown` for fish, `dryOut`
  for axolotl/nautilus, which also gate the step on `!isNoAi` (the
  axolotl's check is isInWaterOrRain). Dolphin's override is empty
  (moistness) and turtle is a plain Animal.
- Ageable prelude hoisted: every AgeableMob emits baby@16 + the
  universal AgeLocked@17 bool before per-type data in both emit
  paths (zombies keep their own DATA_BABY_ID@16 — not AgeableMob).
- CustomName lifecycle corrected to ComponentSerialization's CODEC:
  STRING tags decode as Component.literal (raw text, never reparsed —
  `CustomNameComp` clear), compounds/lists decode into real
  components, and numbers/bools fail the decode and clear the name.
  Wire emit collapses pure-text components to their string; replies
  and hover names render the literal or parsed component to match.
  `TestAnimalVariantParity` pins the six summons + nine merges vs
  both servers; `TestDataEntityParity` green again.
- Fox `Trusted` merge parity: the list-of-[I;4] codec reads into
  DATA_TRUSTED_ID_0/1 (idx20/21, optional_living_entity_reference
  serializer 13). addTrustedEntity fills slot 0, then slot 1, then
  overwrites slot 1 on every further uuid; /data merge runs
  clearTrusted first, so each entityData.set latches dirty on the
  intermediate value — a merge carrying the entity's own uuids stays
  clean while a changed list emits both slots. Same-value merges
  re-emit the current uuids (per-item dirty latch, not snapshot
  compare). Summon applies the same slot fill semantics.
- `TestAnimalVariantParity` now pins the variant codecs + Trusted
  slot churn vs both servers — summon {Trusted:[[I;..],[I;..]]},
  the three-entry slot-1 overwrite, single-uuid re-merge, and
  {Trusted:[]} clear all green.
- Camel `LastPoseTick` parity: DATA_LAST_POSE_CHANGE_TICK rides
  idx20 (LONG serializer) on both emit paths; NBT `LastPoseTick`
  long reads through `getLongOr` — `<0` sets Pose.SITTING(10) then
  `resetLastPoseChangeTick(v)` unconditionally. DATA_POSE moved to
  the shared prelude (idx6 emits on pack/diff whenever pose leaves
  STANDING) instead of the frog-only arm. Camel.isTamed() is
  hardcoded true, so its save always writes `Tame:1b`; the next
  /data merge re-reads it into setTamed(true), latching the equine
  DATA_ID_FLAGS byte 0x02 at idx18 — pinned vs both servers in
  `TestAnimalVariantParity` (sitting summon + two merges).
- Equine DATA_ID_FLAGS NBT bits (0x08 BRED, 0x10 EATING): Bred,
  EatingHaystack, Temper and Owner ride AbstractHorse's
  readAdditionalSaveData on both summon and merge; the flags byte
  packs 0x02 tamed | 0x08 bred | 0x10 eating at idx18. Temper is a
  plain int (reply only, no wire slot); Owner is an EntityReference
  field. Camel.isTamed() is hardcoded true — its save always writes
  Tame:1b but the flag bit only latches when NBT Tame is read.
  aiStep's eating decay (canEatGrass→true, ++eatingCounter > 50 →
  setEating(false)) runs per tick outside the NoAI gate, plus the
  1/300 grass-below re-arm roll — pinned vs both servers in
  `TestAnimalVariantParity` (bred/tamed summons + five merges).

## Slice — pufferfish DATA_PUFF_STATE + player pose + scalesWithDifficulty (26.2)

- Pufferfish NBT/metadata: `PuffState` int reads clamped ≤2 on
  summon and /data merge, rides idx17 varint; `FromBucket` rides
  AbstractFish's idx16 bool on every bucketable type (cod, salmon,
  pufferfish, tropical_fish, tadpole). Both keys round-trip through
  save (`known` set — `/data get` shows them).
- PufferfishPuffGoal live cycle: while a scary LivingEntity sits
  inside bbox.inflate(2.0) (`not_scary_for_pufferfish` tag exempt),
  inflateCounter >40 escalates state 0→1→2; once it leaves,
  deflateTimer >60 steps down to 1 and >100 to 0 — pinned by
  `TestPufferfishParity` on the sprint windows.
- Sting arms run OUTSIDE the NoAI gate: aiStep's contact arm hits
  every Mob inside bbox.inflate(0.3) with mobAttack 1+state + POISON
  60*state + sting sound; playerTouch hits a damageable overlapping
  player with the same hit plus the event-9 game_event.
- Player.hurtServer's scalesWithDifficulty arm ported to damageWith:
  the captured damage_type registry's `scaling` field (4 `always`,
  47 `when_caused_by_living_non_player` — the latter only when the
  causing entity is living non-player) scales EASY min(d/2+1,d),
  HARD d*1.5, PEACEFUL 0 — and `damage==0` returns false outright,
  so the sting's game_event/poison never fire at peaceful.
- Player pose model (Player.updatePlayerPose): runs every player
  tick while the SWIMMING box still fits; getDesiredPose resolves
  sleeping>swimming>fallFlying>spin>sneak>stand; the fit ladder
  keeps the desired pose for spectators/passengers, else CROUCHING
  beats SWIMMING. canPlayerFit is blocks-plus-hard-colliders only —
  canBeCollidedWith answers true for boats/shulkers/happy_ghasts
  alone, so a fish overlapping the player never blocks the pose
  (measured: vanilla emits pose=3 inside the fish's cell).
- DATA_POSE emits once per change through a deferred flush:
  poseEmit marks the entity-data entry dirty like setSynchedData and
  the packet rides the next playerTick, matching vanilla's
  sendChanges pass-after-tick — a teleport's reply always lands
  before the emit on the wire.
- Literal chat serialization: a bare PlainTextContents component
  encodes its text under the empty entry name ({"": "Server"});
  any other field keeps "text". `with` args: all-primitive lists
  stay primitive (long list for gametime); any compound or mixed
  types wrap EVERY arg as a {"": v} compound.
- /fill modes measured end-to-end: REPLACE, OUTLINE (interior
  skipped, destroy affector still counts), HOLLOW (interior→air),
  DESTROY (level.destroyBlock drops + 2001 before the filter),
  `keep` = REPLACE + empty-block predicate, `strict` = REPLACE +
  flags 816 (no neighbor updates/no shape fold); same-state place
  isn't counted.
- `home_radius` default fixed: absent key now reads -1 like
  getIntOr, so mobs stop leaking HasHome+home_radius:0 into saves.

## Slice — equine mobInteract food/makeMad arms + DATA_ID_FLAGS 0x20/0x40 (26.2)

- `equineInteract` owns the whole AbstractHorse-family gate
  (horse/donkey/mule/llama/trader_llama/zombie_horse/
  skeleton_horse; camel keeps its own arm): passengers → false;
  baby holding golden_dandelion → false; tamed adult sneak →
  false (inventory arm); empty hand → false. Non-empty hand:
  isFood → handleEating arms; !tamed → makeMad → SUCCESS; else
  falls through to ride.
- handleEating arm order (decompile-verified): golden-food
  in-love (tamed+age==0+!inLove → entity_event 18 + 600 ticks) →
  heal (health<max, capped) → ageUp (baby+!ageLocked,
  happy_villager server-side) → temper (temper>0 && (itemUsed ||
  !tamed) && <maxTemper 100/llama 30) → on itemUsed: openMouth
  flag 0x40 + eat sound (vol 1.0, random pitch) + gameEvent EAT +
  swing arm + consume(1) survival/adventure. Feed tables from
  decompile: horse family {wheat 2/20/3, sugar 1/30/3,
  hay_block 20/180/0, apple/carrot 3/60/3, golden_carrot 4/60/5,
  golden_apple+enchanted 10/240/10}; llama {wheat 2/10/3,
  hay_block 10/90/6}.
- makeMad: non-llama → setStanding → flag 0x20 + standCounter=20
  tick countdown (any setStanding call resets); all → angry sound
  vol 0.8 + random pitch; skeleton_horse silent (no eat/angry
  sound in SOUND_TYPES).
- Tick countdowns in the equineTypes tick arm: standCounter--
  ≤0 clears 0x20; mouthCounter++ >30 clears 0x40 — both dirty the
  meta byte so the clearing emit rides the per-tick diff pack.
- Baby horse (any equine Baby) uses entity.baby_horse.eat/
  .angry sound ids instead of the type's own.
- `feedAnimal` no-ops on isEquineName (equineInteract owns the
  family); animalFood stays as the breedable registry.
- Horse `Variant` NBT: save emit + /data merge read + {Variant:N}
  summon NBT — the packed type|markings int riding idx19.
- itemDisplayKey: the 7 names with BOTH item.minecraft.X and
  block.minecraft.X lang keys — wheat/nether_wart/pitcher_plant/
  resin_clump take item.X (ItemNameBlockItem identity); every
  other name takes block.X iff ItemBlockState resolves.
- `TestEquineFlagsParity` pins makeMad (0x20 emit + angry sound
  0x75 + 20-tick clear) and wheat-feed (0x40 emit + eat sound +
  30-tick clear + consume) on both legs.

## Slice — equine ride-tame loop (RunAroundLikeCrazyGoal) (26.2)

- `equineRideTameStep` per tick on the equine family: gates =
  !tamed && isVehicle && !isMobControlled (zombie_horse with a
  mob first-passenger skips). Riding sets FleeRidden →
  `equineFleeStep` drives the flee target draw (±5 via
  strollOffset/2, 1.2× move speed, give-up on ledge/obstacle or
  >160 steps).
- Per tick 1/25 roll (vanilla's reducedTickDelay(50)=25):
  passenger is a player → maxTemper>0 && rand<temper →
  `equineTameWithName` (setOwner+setTamed+TAME_ANIMAL trigger+
  entity_event 7) else modifyTemper(5) → ejectPassengers +
  makeMad + entity_event 6. Non-player passengers just eject.
- `doPlayerRide` arms at rideVehicle: equines clear eating/
  standing counters + weld the rider's rot to the mount's.
- mountAllowed: adults only; skeleton_horse additionally requires
  Tamed (its mobInteract returns PASS untamed).
- `TestEquineRideTameParity` pins both lanes: Temper:0 → eject +
  event6 + angry + 0x20 pulse + Temper5 + Tame0b; Temper:100 →
  event7 + Tame1b + OwnerI — deterministic on both servers.
- Fixed: the DATA_ID_FLAGS comment said idx20 — the wire (and
  tame emit) confirmed idx18.

## Slice — equine breeding (Animal.spawnChildFromBreeding) (26.2)

- `mobBreedStep` gained an equine arm: `equineMates` matrix
  (horse×{horse,donkey}, donkey×{horse,donkey}, llama×llama+trader_llama,
  camel×camel; mule/skeleton/zombie_horse sterile), `equineCanParent`
  (no vehicle/passenger, !baby, tamed — camel always, health≥max).
- `equineOffspringType`: horse×donkey → mule both directions;
  trader_llama keeps its type (+PersistenceRequired on the baby).
- `equineOffspringAttr` = vanilla's createOffspringAttribute: clamp
  both parents to [lo,hi], margin=0.15·(hi−lo), span=|a−b|+2·margin,
  avg+span·(3-dice−0.5), fold back at bounds. Ranges pinned:
  max_health [15,30], jump_strength [0.4,1.0], movement_speed
  [0.1125,0.3375].
- Baby spawns at supplier base Health=53 then max_health's
  setBaseValue clamps live health to the blended value (measured:
  vanilla data get shows Health≈base29.71).
- Horse baby Variant: selectSkin nextInt(9) (<4 self, <8 partner,
  else random 0-6); selectMarking nextInt(5) (<2 self, <4 partner,
  else random 0-4); packed skin|marking<<8. Llama baby: strength =
  1+nextInt(max parents) +1 on 3% roll (clamp 5), variant coinflip.
- finalizeSpawnChildFromBreeding fires entity_event 18 on the
  PARENT (`this`), not the baby — mc-go had it on the baby.
- `TestEquineBreedParity` pins 3 lanes (horses, horse×donkey→mule,
  llamas) on both servers: feed hearts on both parents, breed heart
  on a parent, baby add_entity, and the baby's NBT (negative Age,
  Tame0b, blended jump_strength+max_health, Variant/Strength).

## Slice — camel feed arm + equine feed/NBT fixes (26.2)

- `camelInteract`/`camelFeed` = Camel.mobInteract's food arm:
  CAMEL_FOOD=cactus, CAMEL_HUSK_FOOD=rabbit_foot. handleEating's
  three independent arms — couldHeal (+2.0), couldSetInLove
  (isTamed→true always, so no Tame NBT needed), couldAgeUp
  (+10s=200 ticks + HAPPY_VILLAGER). Nothing applicable → PASS,
  no consume.
- CamelHusk.canFallInLove=false and canBeABaby=false — the husk
  heals on rabbit_foot but never loves; mc-go's love arm now skips
  the husk (was firing hearts).
- Camel.getBreedOffspring never calls setOffspringAttributes —
  camel babies keep the 32/0.42/0.09 supplier bases; the equine
  blend now skips the camel family (was blending max_health to
  ~15-30 like the horse family).
- Fix: equineFeed's ageUp table stores vanilla's SECONDS values —
  Age now advances row.ageUp*20 ticks (wheat was +20t, vanilla +400t).
- Fix: the ageUp arm now emits vanilla's HAPPY_VILLAGER particle.
- Fix: Tame NBT write forces 1b for camel_husk too (inherits
  Camel.isTamed→true, not just camel).
- Fix: camelInteract rejects dead (Health<=0) entities — a killed
  camel lingering in byID for its death-anim window could take
  the feed.
- `TestCamelFeedParity` pins camel love (no tame, second feed PASS),
  husk heal-only, and baby ageUp×2 on both servers; the breed
  suite gained the camel×camel lane (baby Tame1b, Health32,
  Age-24000).
- Harness: per-lane summon X positions — a previous lane's corpse
  re-emits add_entity into the fresh session and can steal the
  nearest-match eid.
