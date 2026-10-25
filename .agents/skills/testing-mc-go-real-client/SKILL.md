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
- Go: `/usr/local/go/bin/go` (not on default PATH)
- Java for the client: `/usr/local/jdk25/bin/java` — MC 26.2 needs class majorVersion 25; the system JDK 17 is too old.
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
- Workaround for verification: `/tick freeze` then `/tick step N` drives `GameTick` synchronously. E.g. latch a survival dig (hold LMB ~1s so START+FINISH arrive), then `/tick step 30` — the block breaks and the drop is collected, proving the pipeline and isolating the bug to the wall-clock loop.

## Notes
- ALSA/OpenAL errors and authlib 401s in the client log are benign (no sound card, offline account).
- Client version must match mc-go's advertised protocol (const.go: ProtocolVersion / VersionName).
