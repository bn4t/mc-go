# mc-go (lean Java-Edition server skeleton)

This repository contains a lean, production‑ready architecture skeleton for a Java‑Edition Minecraft server in Go. It focuses on a deterministic world loop and a four‑stage packet pipeline that exposes clean hooks for anti‑cheat.

Current milestone: transport + framer + Status ping + minimal Login→Play with KeepAlive (target 1.21.1).

## Layout

```
/cmd/server               # entrypoint (mcserver)
/internal/runtime/        # process boot, config, telemetry
/internal/netx/           # TCP acceptor, conn setup
/internal/proto/          # VarInt framing, helpers
/internal/packets/        # typed packet structs per version
/internal/pipeline/       # four-stage packet pipeline + interceptors
/internal/session/        # per-connection state machine (Handshake/Status now)
/internal/world/          # (stub) authoritative world loop
/internal/events/         # (stub) in‑process event bus
/internal/anticheat/      # interceptors (MovementIC stub)
/internal/persist/        # (stub) persistence interfaces
/internal/plugins/        # (stub) plugin host surface
```

## Quick Start

- Build: `go build -o mcserver ./cmd/server`
- Run: `./mcserver`

On first run a minimal `server.yaml` is created. The server listens on `:25565` and supports the Status query/ping path so it shows as online in the Minecraft server list.

## Pipeline (hooks for anti‑cheat)

- S0 Inbound/Raw → after length framing (no compression yet)
- S1 Inbound/Decoded → typed packet, pre‑domain mapping
- S2 Pre‑Apply → world thread (authoritative) before state mutation
- S3 Outbound → just before send (bait/rate policing)

This skeleton enables S0/S1/S3; S2 integrates once the world loop and command application land.

## Versioning

Codecs are organized per protocol version under `internal/packets`. This cut includes 1.21.1 Handshake + Status only.

## Next Steps

- Login→Play path and keep‑alive
- World loop @ 20 TPS and S2 checks
- Shadow physics, setbacks, and evidence recorder
- Compression and online‑mode auth

## Requirements

- Go 1.21+
- TCP port 25565 available
