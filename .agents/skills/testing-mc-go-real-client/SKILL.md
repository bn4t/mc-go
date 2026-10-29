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
