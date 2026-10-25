# Portable protocol conformance

`mc-conformance` connects to any Minecraft Java TCP endpoint. Its public Go
package depends only on the standard library: no `internal/server`, generated
game tables, server process, Java runtime, or vanilla jar. Protocol constants
and layouts are independently pinned here to Java Edition **26.2 / 776**, with
the public [Java protocol reference](https://minecraft.wiki/w/Java_Edition_protocol)
and the existing differential suite's wire observations as evidence.

```sh
go run ./cmd/mc-conformance -endpoint 127.0.0.1:25565
go run ./cmd/mc-conformance -config conformance/endpoint.example.json -output report.json
```

The default opts into status schema/version and ping echo. Enable additional
capabilities explicitly in a JSON endpoint manifest:

```json
{
  "endpoint": "127.0.0.1:25565",
  "protocol": 776,
  "version": "26.2",
  "capabilities": ["status", "ping", "offline-login", "configuration", "malformed"],
  "username": "Conformance",
  "timeout_ms": 10000,
  "transcript_limit_bytes": 16777216
}
```

`offline-login` checks the offline UUID algorithm, echoed player name, login
properties and the protocol-776 trailing session UUID. `configuration` includes
that login, negotiates zero known packs so registries are sent in full, answers
configuration keepalives/pings, validates full registry entry/NBT, feature and
tag packet structure and their
negotiation order, requires Finish Configuration, acknowledges it and decodes
the complete first Play Login packet including dimension references.
Compression, including threshold zero, is supported in both directions.
`malformed` requires immediate peer rejection of an invalid handshake state and
an impossible frame length; a read timeout fails, and later probes check status
availability. Run these probes only against an endpoint configured for testing.
The runner does not change server settings or require operator privileges.

Each scenario has one of four explicit statuses:

- `pass`: all assertions in this smoke scenario passed.
- `fail`: a connected endpoint violated the declared contract or timed out.
- `unsupported`: its capability was omitted, or no profile exists for the
  declared protocol version. This never counts as a pass.
- `infrastructure`: invalid manifest, TCP dial failure or caller cancellation.

Exit codes are 0 for declared checks passing, 1 for protocol failures, and 2 for
infrastructure/configuration failures. Declaring a scenario with an unsupported
protocol profile, or executing no scenarios at all, also returns 2 with a report
reason. Omitted capabilities may remain unsupported in an otherwise successful
run. The `declared` result field distinguishes these cases. Any infrastructure
failure takes precedence. JSON `mc-conformance/v1` reports contain the normalized target,
per-scenario durations and a chronological transcript of inbound/outbound **raw
framed bytes** as hex, including compression framing. Connection-close/error
events retain diagnostic errors. Captures include the probe username and UUIDs,
server descriptions and registry payloads; treat report files as packet captures.
Transcript limits apply separately per
scenario to bound memory; `truncated: true` marks bytes omitted and prevents a
partial capture being mistaken for a complete replay. For replay, concatenate
serverbound `wire_hex` within a scenario, respecting clientbound transitions
and compression negotiation. UUID/session values can vary on a fresh run.

This is a reusable **foundation and smoke profile**, not a claim of gameplay
parity. It validates registry/NBT structure without asserting the game semantics
of
every registry value. It does not exercise online Microsoft authentication or
cover gameplay, persistence and plugin behavior.
The larger `tests/blackbox` differential suite remains the strict vanilla
oracle, with its own setup requirements and runtime `MCGO_BLACKBOX=1` gate.
Default `go test` keeps Java-free TCP checks runnable. Both default and
`-tags blackbox` builds compile the same test helpers.

Extend `scenarios` with independent wire assertions and add a failing TCP peer
control whenever an assertion is added. Keep implementation imports and
in-process mutation out of this package. To reuse outside this repository,
import `mc-go/conformance` in a module replacement, copy this stdlib-only
package, or build the CLI and point it at another implementation.

`TestPortableConformanceProfile` runs the public package against the local Go
server or `MCGO_TARGET`. `TestPortableConformanceVanilla` runs the same seven
checks against vanilla with the existing explicit oracle gate. Set
`MCGO_CONFORMANCE_REPORT=/path/report.json` while running the latter to retain
its JSON packet capture for inspection.
