---
name: testing-mc-go-real-client
description: How to playtest mc-go with a real vanilla Minecraft client in offline mode — launch the client directly (launchers block offline-only accounts), drive the GUI, and wire-diff packets to root-cause kicks.
---

# Real-client playtesting of mc-go

## Devin Secrets Needed
None — everything runs offline. No Microsoft account is needed if you hand-write the launcher account file or launch the client directly.

## Why not Prism/Fjord launcher
Prism Launcher ≥9 and Fjord Launcher both require a Microsoft account that owns Minecraft before "Add Offline" works, and refuse to launch with "No Accounts / Play demo". Two ways around it:

1. **Direct vanilla launch (preferred, proven):** download the client jar + libraries + assets + natives via piston-meta and run `net.minecraft.client.main.Main` under JDK 25 on DISPLAY :0. The vanilla launcher arg substitution is mechanical; see the launch script pattern below. This gives the genuine unmodified client.
2. **Hand-written accounts.json** into Prism's data dir (~/.local/share/PrismLauncher or a portable dir): formatVersion 3, account type "Offline", `ygg:{token:"0",extra:{userName,clientToken}}`, `profile:{id,name,skin:{id,url,variant},capes:[]}`, `entitlement:{ownsMinecraft:true,canPlayMinecraft:true}`, `active:true`. This loads the account but Prism still blocks launch — only use it if you also bypass the launch gate.

## Toolchain
- Discover Go and Java with `command -v` and check their versions. In the
  managed workspace, source `/workspace/mcgo-tools/activate.sh` for Go 1.27.3
  and Temurin 25.0.5+8. MC 26.2 requires JDK 25 (class-file major version 69);
  JDK 17 is too old.
- mc-go: `go build -o /tmp/mc-go ./cmd/mc-go && /tmp/mc-go -addr :25565 -gamemode creative -world /tmp/mcgo-world.json`. Creative mode is better for smoke tests (instant block break, free placement).
- Kill mc-go by exact PID (`kill <pid>`) — `pkill -f "mc-go -addr"` matches your own shell wrapper and kills your session.

## Before recording
`wmctrl -r :ACTIVE: -b add,maximized_vert,maximized_horz` on the Minecraft window (it starts small). Do NOT use xdotool Super+Up — it tiles on GNOME.

## Driving the client
- Direct connect: Multiplayer → Add Server → `localhost:25565` → Join.
- F3 shows XYZ — the reliable way to assert movement (compare coords before/after holding W). In-game the pointer is captured: `mouse_move` produces relative deltas and pitch clamps at ±90, so verify actions via F3 coords and screenshots, not cursor position.
- Creative inventory: press E, hover a grid item, press a number key (1-9) to assign it to that hotbar slot — more reliable than drag-click.
- Creative break is instant (single left-click). Survival break needs `left_mouse_down` + hold ~1-3s + `left_mouse_up`; watch for the crack overlay.

## Debugging kicks ("Failed to decode packet 'clientbound/X'")
mc-go logs NOTHING on a failed join — the client screen is your only error surface. To root-cause:
1. Write a raw-socket probe that does handshake(proto 776) → login_start → reads framed packets → answers config (echo select_known_packs, ack finish_configuration) → dumps play packets as hex.
2. Diff the same packet's bytes from a real vanilla 26.2 server (leftover ones may run on random ports; check `ss -tlnp` for java). Field-level byte diff shows schema gaps immediately.
3. Common bug class in mc-go: **registry ids out of range** — the client's decoder validates VarInt ids against the registries sent in config. If mc-go sends clock id 2 for a 2-entry world_clock registry, or attribute ids 84/132 for a 40-entry attribute registry, the client throws DecoderException. Always check sent ids against the registry size mc-go itself transmitted.
4. Rejoins exercise a different code path (mc-go's `markJoined`/returning-player branch) — test disconnect → rejoin, not just first join.

## Player-tick readiness

For scripted combat or advancement probes, first observe the relevant inventory
or state receipt, then measure player `play_time` statistic changes. World-time
steps and elapsed wall time do not establish player cooldown readiness. Keep
exact damage assertions and include a reduced tick-rate control when timing is
the suspected cause.

## Tick-liveness check (do this before diagnosing tick-driven features)
Survival dig, hunger drain, item despawn/pickup, furnaces, daylight advance all live in `Server.GameTick`, which only runs from `runGameLoop` (started by `startGameTicks` inside `Server.Serve`). If `main`/a frontend calls `fe.Serve(ln)` directly, that loop may never start while everything packet-driven still works.
- Check: run `/time query gametime` twice a few seconds apart. Same number twice = the game clock is dead — every tick-driven feature will silently no-op even though packets are acked.
- For controlled verification, `/tick freeze` then `/tick step N` advances
  the requested simulation ticks; observe actual game-time completion before
  checking results. A command reply alone is not a completion barrier.
  E.g. latch a survival dig, step 30 ticks and observe the final block/drop.
  `/tick sprint N` is asynchronous and measured 26.2 advances N+1 ticks, so
  do not use its argument as an exact elapsed-tick count.

## Notes
- ALSA/OpenAL errors and authlib 401s in the client log are benign (no sound card, offline account).
- Client version must match mc-go's advertised protocol (const.go: ProtocolVersion / VersionName).

## Availability and evidence

Check client dependencies before claiming a graphical playtest. The server
oracle jar alone cannot launch the client: the matching client jar, libraries,
assets, natives and a working graphical display are also required. In the
2026-10-29 managed environment, Xvfb is installed under
`/workspace/mcgo-tools/gui/root`, but the provider proxy returned HTTP 403 for
`piston-meta.mojang.com`, `libraries.minecraft.net` and
`resources.download.minecraft.net`. No graphical playtest was completed there.
Recheck availability in a new environment; do not bypass network controls.
Continue independent implementation and scripted protocol checks, identifying
those checks accurately instead of describing them as real-client playtests.

For timing bugs, retain actual packet order and establish preconditions from
spawn/update receipts. Serverbound editor and command packets can complete on
different paths. When a container closes, first observe the expected direct
player-inventory update before querying the returned cursor stack. A later
command reply alone does not establish close application. If the expected
packet cannot establish final state, capture timing and request an authoritative
inventory resync; a resync must not manufacture an earlier ordering claim.


## Scripted test build modes

When adding protocol fixtures or shared helpers, check default `go vet ./...`
and tagged compilation with `go test -tags blackbox -run '^$' ./tests/blackbox`.
Keep vanilla-only tests that use tagged helpers under the same build tag. A
successful tagged native run does not establish default test compilation.

## Scripted preconditions and shared inventory state

- Complete the client terrain-loaded notification before entity-interaction
  probes. Receiving an entity spawn or tracking update alone does not prove
  the server has admitted that interaction path.
- Retain the packet-driven inventory cache across actions. A clear followed
  by reseeding the same stack can coalesce without a slot delta; a missing
  update does not mean the slot is empty. Map a menu's player-slot region to
  the shared inventory using its measured layout, and handle direct player
  inventory updates. Assert each selected hand's item ID, count and damage.
- For native 26.2 player data witnesses, offhand is `equipment.offhand`, not
  the legacy `Inventory` slot -106. For malformed-use probes, prove setup first,
  observe disconnect/no acknowledgement, then wait for actual retirement and
  rejoin to check saved inventory and world state.
- Distinguish a block's default interaction from the held item callback. For
  containers, decode screen kind/title and contents, then prove the menu is
  usable after a second player changes the block. Observe acting-player and
  observer sound/particle packets separately; local prediction can exclude
  the actor from server effects without suppressing the gameplay action.
- Before a multi-chunk setup fill, observe every chunk in its full footprint;
  loaded diagonal endpoints do not prove intermediate chunks are ready. Verify
  a positive setup block before attributing later misses to gameplay. Keep the
  intended action count and original behavioral expectations unchanged.
- Wait for the exact use-on sequence acknowledgement before observing the
  action. Keep one action per control; an observation barrier is not a reason
  to retry placement. A frontend connection's Close revokes its handle before
  asynchronous world retirement; observe the public Removed update before
  asserting that its collider is gone.
- Frozen dying mobs can still obstruct building until removal ticks finish.
  Relocate live fixture mobs before killing when isolating another mechanic.
  Keep a separate before/after-removal control when testing that collision.
  Establish the same entity ID through spawn, death status and actual removal
  packets; selector absence is insufficient because selectors omit corpses.
- Attribute replies do not prove settled body dimensions. When a clearance
  test needs settled player scale, observe positive `play_time` advancement;
  world steps and Pos/Pose replies do not prove a player frame. Test immediate
  resize timing separately instead of claiming that the settled case pins it.
  A use ACK can flush on the next connection tick before player work. It proves
  processing, but does not timestamp the action inside earlier command-counter
  queries; a guarded TCP batch does not erase that dispatch distinction.

## Random physics probes

Constrain geometry to one outcome direction, then compare measured impulse bounds
and displacement/velocity relations rather than random sample equality. An item
Age1 witness isolates its first movement tick. Reject apparatus that moves during
setup before attributing displacement to a piston or other tested action.

## Entity pairing receipts

Use decimal X/Z coordinates for exact summon identities: integer coordinates are
centered at +0.5. Establish the observer's loaded chunks before creating targets.
For late pairing, start capture before readiness drains can consume spawn or
metadata packets. Pairing's non-default metadata and pending dirty metadata are
separate operations; preserve exact counts and repeat-write controls.

## Command witness packets

For a conditional `execute ... run say MARKER` witness, require the exact
message content in an actual Disguised Chat packet (26.2 ID 0x21). A generic
substring search can match the command echoed in syntax-error context, the
sender's name or an unconditional `MARKER_END` barrier. Decode the message
component and keep condition and barrier identities distinct. The scoped
`structureSaidBB` helper and its negative controls demonstrate this contract;
`sawMarker` intentionally remains a generic feedback-key search for other
fixtures and is insufficient on its own for a command-success assertion.


## Large runtime definition packets

Advancement tab updates can exceed4KiB. Scripted probes must explicitly retain
these packet IDs when using a sink with a size filter, decode runtime definitions
rather than stopping at unknown resources, and require meaningful node counts.
The `newUseClient` helper accepts optional large-packet IDs without changing its
default capture policy. For nondeterministic layouts, first demonstrate variation
across unchanged native runs, normalize only those fields, and independently
check each server's geometric constraints; equality after discarding all layout
data is insufficient.

## Component identity and effect ordering

Persistent codec defaults and an item's prototype defaults are separate facts.
An explicit empty component can differ from absence; test both identity and
serialization before normalizing them. Before asserting spawn/effect packet
order, establish a positive same-chunk spawn witness. Block updates can be
batched after synchronous effects, so packet order alone does not establish
internal mutation order.

## Datapack function probes

Measure empty/missing group callbacks separately from nonempty no-return calls.
Assert numeric result, success, feedback style and execute-as suppression where
relevant. A decoder's partial result does not establish resource acceptance: the
loader may reject the whole file. Default function permission is two; a load
function containing permission-three `/tick freeze` is invalid and cannot prove
automatic-function tick ordering. Use a valid function and external freeze.

## Clock-state probes

Native clock rows are unordered. Compare by registry ID with strict row counts,
duplicate/missing checks and typed totals/fractions/rates. World age, clock totals,
raw saved rates and effective packet rates are separate observations. A paused
clock can send rate zero while retaining a positive raw saved rate. Copy a
completed save before source shutdown, then verify progression after cross-server
reload; default-valued codec round trips alone do not establish persistence.

## Socket phase deadlines

A client helper's absolute deadline keeps running across login, configuration
and Join readiness. Set the intended readiness deadline before those reads,
then start the separate streaming deadline after Join. Do not compensate by
reducing required chunks or dropping packet assertions. Compare unchanged and
patched fixtures and retain any full-run failure when diagnosing load-sensitive
readiness timeouts.

## Population-cap observations

Count newly spawned cap-eligible entities across the relevant dimension, not only
a fixture arena. Initial filler IDs, persistent mobs and custom-persistence riders
need separate treatment. A neighboring valid slime spawn can consume the last
slot while the dark arena stays empty. Capture positions/types and reproduce the
same input on unchanged code before changing cap behavior or attempt limits.

## Player attribute and effect restart probes

Capture live attributes before common player retirement removes the entity.
Save effect identity and remaining duration together with its permanent modifier;
that persistence classification does not mean the effect lasts forever. Verify
that an active effect's overridden ID is removed at expiry while the same command
ID survives when no effect is active. Read raw saved fields separately from
loaded attributes: native walking ability overrides movement base after raw
attribute load, with float32-to-double widening. Use actual ability packets and
finite player-tick expiry checks in both native/Go handoff directions. Keep
finite JSON compatibility and test explicit lossless nonfinite encoding.


## Save writer versus loader evidence

A native server successfully loading a Go-written save establishes compatibility
of that output. It does not establish that Go can restore the same records.
Keep writer and loader coverage explicit, and test actual remaining lifetime
and expiry after each supported restart direction. If startup needs a longer
fixture delay, first assert the original saved value, copy the completed save,
and modify only the copy. Preserve the original artifact and its assertions.


## Relative teleport acknowledgements

Protocol 776 SyncPos can carry relative X/Y/Z coordinates. A scripted client
must resolve those offsets against its known current position before sending
absolute MovePos. An opt-in teleport-only sink must first receive an absolute
seed; fixtures that also send their own movement must update that base too.
Otherwise the test client can change the subject's actual position and falsely
blame command-source semantics. Keep an independent typed position query in
teleport/function probes. The default legacy sink is not a general position
simulator; the stateful sink covers its measured teleport-only sequence.


## Health-consumer probes

Pair raw bases and effective attribute queries with actual typed Health and
applied damage. Raising max_health alone grants no health; removing a modifier
can require the living refresh before excess health is clamped. Clear a prior
instant effect and observe actual player ticks before measuring a subsequent
heal. For periodic effects, include both a zero-interval amplifier and a wrapped
amplifier32 on a short finite duration: treating all high amplifiers as every
tick would incorrectly pass only the first control. Finite effect evidence does
not establish the entity-age phase used by infinite instances.


## Collision-fixture state witnesses

A requested block name does not prove which shape the physics consumer sees.
Covered farmland can change to dirt before the measurement. Require exact block
state witnesses before and after the measured tick, and preserve invalid
apparatus logs separately from behavioral mismatches. Inspect the complete
aggregate result and both server legs before calling a native baseline valid.


## Periodic effect clocks

Infinite status instances use living entity age for cadence; finite instances
use remaining duration. A due age alone does not prove a tick ran. Require an
actual tick and a no-advance control, and distinguish frozen player ticks from
paused mobs. Ordinary respawn creates a new body clock; live dimension transfer
preserves it. Bound wire observation windows with player counters rather than
claiming an exact effect-action timestamp from a command reply.


## Verification export integrity

When verifying an owned overlay on an immutable archive, compare its file hashes
to the frozen manifest before building. git apply inside an untracked nested
export can discover the outer repository and skip paths. A successful process
exit with no tests run is not behavioral evidence: list or run the intended
named tests and retain their results. Explicit empty-run compilation gates are
separate and must be labeled as compilation only.


## Numeric packet rejection and command-source witnesses

For malformed movement, wait for the typed disconnect packet and connection
closure. A following chat reply can arrive before a queued kick, so it cannot
prove that an invalid packet was accepted. Retain separate valid finite and
infinity controls according to the measured packet-specific contract.

For player-only self selectors, distinguish the original caller, another player
and a nonplayer executor through actual inventory ownership. Preserve different
item-count sentinels and include scheduled neutral-source execution; feedback
names alone do not prove that the intended inventory changed.


After teleporting to a transient-entity test arena, verify the destination chunk
and authoritative actor position before summons. Login readiness alone does not
prove that destination visibility is established. Compare a fixture correction
on unchanged baseline and candidate source while retaining all original
spawn, survivor and reward assertions.


For recurring effects, raise the health maximum without healing before counting
pulses: a capped target can hide a missing final callback. Pair loaded one-tick
and zero-duration instances, observe effect expiry, and distinguish actual body
ticks from a merely matching world-clock phase.


Instantaneous effect metadata does not imply an extra dose when a status is
stored. For add/re-give causality, freeze a mob, verify unchanged game time and
health before ticking, then observe exactly one tick. Keep uncapped multi-tick
command effects separate from direct scaled drinking/splash/cloud doses.


For execute-budget probes, restore deliberately low sequence/fork limits before
using execute to observe results. Unknown objectives throw, while missing scores
in existing objectives can be ordinary false; retain both controls. Decode
nested typed error components and exact say bodies, and retain source ownership
and partial-fork sentinels when testing callback order.


World-step completion can precede a player's held-use action pass. Before
releasing a single use action, observe the appropriate player-tick receipt;
retain the exact world-step budget and original damage/motion assertions.
A returned mounted position alone does not establish that seat-follow or the
player action pass has run.


For direct Saturation potion delivery, assert exact food/saturation pools, the
consumed item and absence of active status together. The direct dose ignores
its scale; accidental status ticking must not compensate for wrong dispatch.
Arrows add stored effects and require separate origin-specific controls.


Scheduled callback completion can follow a query that already reports the target
game tick. Wait read-only for the applied result while asserting unchanged game
time; do not add ticks, rearm the owner or replace the original delay. Distinguish
FULL data, queue activation, entity-loaded predicates and simulation admission.
For native saved-delay aging, verify fresh LastUpdate metadata after harmless
real changes in the measured chunks; a save reply alone may leave an unchanged
chunk file stale. Go SaveWorld supplies a writer completion barrier, but native
LastUpdate equivalence remains a separate claim.

Explicit command source frames can bypass legacy teleport shortcuts. Verify
actual destination dimension and retained rotation through direct commands,
functions and scheduled neutral sources. Include public avatar and streamed
window controls; a passing same-dimension local-coordinate pin is insufficient.


Observer rotation probes must retain fresh spawn packets across readiness and
decode LpVec3 before spawn angles. Keep body and head expectations separate.
For a pitch-only unchanged-head control, verify the new authoritative pitch and
observe actual player ticks; packet silence alone can mean the input was dropped.


A one-dose retirement guard can expose an adjacent cloud lifetime defect. Keep
that guard and record food, status and retirement separately; a surviving first
cloud can contaminate a later player. Verify raw summon hydration through the
actual command, since it can bypass generic entity load helpers.


Pair splash no-status assertions with a positive noninstant effect on the same
recipient. Actual held-item consumption, projectile retirement and exact food
pools separately constrain the player's result; a missed throw proves nothing
about instantaneous dispatch.


Damage-source exceptions use the actual responsible entity, independently of
kill credit. Retain creative/survival/spectator causes and bypass-tag controls,
and check health, feedback and numeric result stores together. Measure physical
contact separately: a rejected hurt callback can still allow native dismount.


Eye-anchor probes need an actual source-existence witness and successful
teleport receipt before reading position. Registry baseline dimensions do not
establish runtime pose, inherited baby eyes or cached scale. Pin these variants
separately, preserve source-order controls, and reproduce extracted constants
through the committed probe and generator.


For silent operator queries, require a later packet-owner response on the same
connection. Separate permission level from game mode, retain signed/repeated
transaction IDs, and query another player's typed inventory/equipment to prove
that the target's save view is used.


For kinetic mount transitions, observe passenger attachment before the action,
then actual unlink and position updates afterward. Check damage, motion and
mount survival separately: source invulnerability and hurt cooldown rejection
have different contact consequences. Match vehicle spawn IDs to actual saved
UUIDs; requested UUID hydration is a separate behavior to measure.


Cross-dimension save/restart predicates must first observe the destination as
loaded. Keep the original editor's dimension and pose intact when later commands
are unqualified; a separate physical loader can establish readiness. Preserve
copy-before-shutdown, explicit-air and generation-isolation assertions. Use exact
typed state/fence tokens so an error echo cannot satisfy the check.


For chunk scheduling, distinguish admission loss from container detachment and
physical unload. Observe serialization time and saved relative deadlines before
claiming a paused clock. A detached resident may retain absolute deadlines;
reattachment and reconstruction need separate controls. Pin stale completion
fences and already-collected callbacks independently of future queue membership.


Numeric physics assertions must explicitly reject NaN and infinity before
applying tolerance or interval comparisons. For pose-dependent outcomes,
observe metadata for the actual actor and allow preparation to settle before
creating the entity whose exact first tick is measured.


For immediate teleport angle state, automatic movement echoes can hide whole-turn
behavior by normalizing yaw afterward. Use a keepalive-only command-state probe
or atomic function capture, while retaining actual teleport/chunk acknowledgements
for physical-action fixtures. Player arrival remainder and later movement
normalization are separate observations.


Held-use age and remembered-contact cooldown can belong to different clocks.
Warm use with actual admitted player ticks while asserting unchanged world time;
then test contact expiry with a separate exact simulation delta. Preserve the
original numeric outcomes when migrating a fixture's clock domain.


For source-level mutations, check both intended and unintended dimensions and
compare typed reply arguments. Two incorrect handlers can agree on the wrong
world. Cover primitive and batch-plan function leaves, and position before/after
execute-in to expose coordinate-scale order. Forced-set success is not a chunk
readiness receipt.


For selective chunk saves, mutate an unselected live neighbor and verify that
its previous disk contents, compressed bytes and timestamp survive. Check native
readback of the selected edit and queued work. Storage compatibility does not
prove runtime unloading, same-region writer coordination or stale IO fencing.


Frozen command polls and player frames are distinct clocks. Compare actual
play_time with an independent environmental quantity while world age stays
fixed. Validate matching mode, health and admission first; preserve strict
outcome assertions when a repaired fixture exposes a production clock defect.


For numerical command parity, retain float widths and lookup-phase boundaries
in independent expected values. Pair complete public API sample checks with
actual command receipts. Check duplicate/missing corpus keys; distinguish exact
basis equality from tolerance in weighted sums, addition and scaled floors.


For asynchronous chunk saves, distinguish automatic subset completion from a
full-save receipt. Test last-controller loss and shutdown after completed IO,
reticket/edit/re-detach with stale completion, and write failure/retry. Preserve
serialized delays independently of unload latency. Future unmaterialized queue
rows must not invoke the provider just to create a save snapshot.


When sampling relative saved timers, a clean native save may leave old chunk bytes. Require an independently new persisted marker in the same compound as the timer before declaring automatic unload complete; equal game time and delay can describe an earlier save. Keep activation, FULL loss, serialization, physical discard and cold timer behavior distinct.


For NPC environmental fixtures, freeze setup and reads, validate actual blocks and bodies, and prove exact world-step completion through game time. Player frames require separate receipts; preserve original health, fire, freeze, melt and retirement outcomes when repairing synchronization.


A capped freeze counter does not establish its increment: use independent uncapped player-frame windows in normal and frozen worlds. Native zero TicksFrozen is omitted, so assert canonical membership rather than an explicit zero tag. Keep stationary, re-entry and movement-path exposure claims separate.
