# Repository Guidelines

## Project Structure & Modules
- Source lives in `pkg/` (server, network, protocol, world, player, plugin, config) and `internal/utils/` for private helpers.
- Entry point: `cmd/server/main.go` → builds the `mcserver` binary.
- Runtime config: `server.yaml` (generated/used at startup).
- Plugins: build `.so` files and place in `plugins/` (loaded by the plugin manager).

## Build, Test, and Dev Commands
- Build: `go build -o mcserver ./cmd/server`
- Run: `./mcserver` or `go run ./cmd/server`
- Format: `go fmt ./...`
- Vet: `go vet ./...`
- Tests: `go test ./...` (no tests yet; see below)

## Coding Style & Naming
- Go 1.21; follow Effective Go and standard library idioms.
- Indentation via tabs; always run `gofmt` (or editor format-on-save).
- Package names: short, lowercase; files `snake_case.go` when multiple words.
- Exports require clear names and doc comments; avoid stutter (`player.Manager`, not `player.PlayerManager` when feasible).
- Errors: wrap with context using `%w`; use Zap for structured logs.

## Testing Guidelines
- Co-locate tests as `*_test.go` next to source; table-driven preferred.
- Names: `TestXxx`, `BenchmarkXxx`; use `t.Helper()` for helpers.
- Run with race detector locally: `go test -race ./...`.
- Aim for meaningful coverage on core packages (`pkg/server`, `pkg/network`, `pkg/protocol`).

## Commit & PR Guidelines
- Commits: imperative, scoped messages (e.g., `server: fix disconnect timeout`).
- Keep diffs focused; reference issues (e.g., `Fixes #123`).
- PRs include: purpose, key changes, how tested, and any config or protocol notes; attach logs/screens when relevant.
- Ensure build, fmt, vet, and tests pass before requesting review.

## Security & Configuration
- Do not commit secrets to `server.yaml`; prefer environment overlays if needed.
- Default port is 25565; confirm firewall/NAT rules in dev.
- Treat third-party plugins as untrusted; audit before enabling in `plugins/`.

## Contribution Workflow
- Branch from `main` (`feat/…`, `fix/…`), open PR into `main`.
- Run: `go fmt ./... && go vet ./... && go test ./...` prior to push.
