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
different paths. When a container closes, a cached carried stack or missing
player-slot update may be stale: capture the timing first, then request an
authoritative inventory resync to assert final inventory state. A resync must
not be used to manufacture an earlier ordering claim.


## Scripted preconditions and shared inventory state

- Retain the packet-driven inventory cache across actions. A clear followed
  by reseeding the same stack can coalesce without a slot delta; a missing
  update does not mean the slot is empty. Map a menu's player-slot region to
  the shared inventory using its measured layout, and handle direct player
  inventory updates. Assert each selected hand's item ID, count and damage.
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
- Attribute replies do not prove settled body dimensions. When a clearance
  test needs settled scale, observe completed entity ticks; test immediate
  resize timing separately instead of claiming that the settled case pins it.

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
