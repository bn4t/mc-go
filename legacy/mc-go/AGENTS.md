# Repository Guidelines

This repo now uses a lean internal/ architecture designed for a deterministic world loop and a four‑stage packet pipeline with anti‑cheat hooks. Treat earlier `pkg/` packages and the `.so` plugin system as removed. Add new code only under `internal/` per the structure below.

## Project Structure & Modules
- Entry point: `cmd/server/main.go` → builds the `mcserver` binary.
- Runtime config: `server.yaml` (created on first run; unknown fields ignored).
- Source lives under `internal/`:
  - `internal/runtime/` — process boot, config, logger, telemetry.
  - `internal/netx/` — TCP acceptor, conn setup (Nagle off/keepalive in future).
  - `internal/proto/` — VarInt framing, helpers; compression later.
  - `internal/packets/` — typed packet structs & IDs per protocol version (e.g., `v1211`).
  - `internal/pipeline/` — packet pipeline API + interceptors (S0..S3).
  - `internal/session/` — connection state machine (Handshake/Status now; Login/Play next).
  - `internal/world/` — authoritative world loop (stub; to host 20 TPS loop).
  - `internal/events/` — in‑process event bus (stub for domain events).
  - `internal/anticheat/` — detectors/interceptors; `MovementIC` lives here.
  - `internal/persist/` — persistence, NBT/region, evidence recorder (stubs).
  - `internal/plugins/` — plugin host surface (static registration for now).

## Build, Test, and Dev Commands
- Build: `go build -o mcserver ./cmd/server`
- Run: `./mcserver` or `go run ./cmd/server`
- Format: `go fmt ./...`
- Vet: `go vet ./...`
- Tests: `go test ./...` (start with table‑driven tests under `internal/*`).

## Coding Style & Naming
- Go 1.21; follow Effective Go and standard library idioms.
- Tabs for indentation; run `gofmt`/format‑on‑save.
- Package names are short, lowercase; files use `snake_case.go` when multi‑word.
- Exported APIs require clear names and doc comments; avoid stutter.
- Errors: wrap with `%w`; use Zap for structured logs.
- Hot paths: avoid reflection; prefer typed decoders/encoders and buffer reuse.

## Concurrency Model (authoritative loop)
- One goroutine per connection reader; one per writer.
- One world goroutine at 20 TPS (50 ms); only this thread mutates world state.
- Sessions enqueue decoded packets → world drains per tick.
- Rule: world goroutine is the single source of truth. Do not mutate shared world state from other goroutines.

## Packet Pipeline & Hooks (anti‑cheat)
- Stages: S0 Inbound/Raw → S1 Inbound/Decoded → S2 Pre‑Apply (world thread) → S3 Outbound.
- Interceptor interface lives in `internal/pipeline`; use `NopInterceptor` to implement only needed stages.
- Place anti‑cheat and I/O policy interceptors under `internal/anticheat/` (e.g., `movement.go`).
- Keep S0/S1 cheap; do heavy checks in S2 where authoritative state is available.

## Protocol Versioning
- Per‑version typed packets live under `internal/packets/<version>/` (e.g., `v1211`).
- Use constants for Packet IDs; implement typed `ReadFrom`/`EncodeBody` without reflection.
- Do not add compression handling inside decoders; keep it in the framer/transport layer.

## World Loop Guidelines
- Tick at 20 TPS using a `time.Ticker(50 * time.Millisecond)`.
- Per tick: drain session inboxes → translate to Commands → run `pipeline.PreApply` (S2) → apply state → enqueue outbound.
- Setbacks and enforcement happen through commands on the world thread for determinism.

## Testing Guidelines
- Co‑locate tests as `*_test.go`; table‑driven where practical.
- Names: `TestXxx`, `BenchmarkXxx`; use `t.Helper()` for helpers.
- Run race detector locally: `go test -race ./...`.
- Target coverage on `internal/proto`, `internal/pipeline`, and session/world integration as it grows.

## Commit & PR Guidelines
- Commits: imperative, scoped (e.g., `pipeline: add S2 movement checks`).
- Keep diffs focused; reference issues (e.g., `Fixes #123`).
- PRs include purpose, key changes, how tested, and any protocol/compat notes; attach logs/screens when relevant.
- Ensure build, fmt, vet, and tests pass before review.

## Security & Configuration
- Do not commit secrets to `server.yaml`; prefer env overlays.
- Default port is 25565; ensure firewall/NAT allows inbound for testing.
- Treat third‑party code as untrusted; plugin `.so` loading is currently removed. Register interceptors statically under `internal/plugins` for now.

## Contribution Workflow
- Branch from `main` (`feat/...`, `fix/...`), open PR into `main`.
- Before push: `go fmt ./... && go vet ./... && go test ./...`.
