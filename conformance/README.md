# Portable protocol conformance

`mc-conformance` connects to any Minecraft Java TCP endpoint. Its public Go
package depends only on the standard library: no `internal/server`, generated
game tables, server process, Java runtime, or vanilla jar. Protocol constants
and layouts are independently pinned to Java Edition **26.1 / 775** and
**26.2 / 776**. Official vanilla oracles were probed over TCP; the small
[observation records](testdata/java-profile-observations.json) retain representative
packet bodies, source class, tool description and official jar checksums. The
public [Java protocol reference](https://minecraft.wiki/w/Java_Edition_protocol)
is additional format context. mc-go admits both measured profiles against the
same world; admission does not establish complete older-version gameplay parity.

```sh
go run ./cmd/mc-conformance -endpoint 127.0.0.1:25565
go run ./cmd/mc-conformance -config conformance/endpoint.example.json -output report.json
go run ./cmd/mc-conformance -config conformance/endpoint.play.example.json -output play-report.json
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
properties and the profile-specific login tail: 775 has none, while 776 requires
a 16-byte session UUID. `configuration` includes that login, negotiates zero known packs so registries are sent in full, answers
configuration keepalives/pings, validates full registry entry/NBT, feature and
tag packet structure and their negotiation order, requires Finish Configuration, acknowledges it and decodes
the complete first Play Login packet including dimension references. The online
mode boolean exists in 776's Join Game and is absent in 775; using the wrong
version's body fails the exact-length checks.
Compression, including threshold zero, is supported in both directions.
`malformed` requires immediate peer rejection of an invalid handshake state and
an impossible frame length; a read timeout fails, and later probes check status
availability. Run these probes only against an endpoint configured for testing.
The runner does not change server settings or require operator privileges.

`play-chunks` opts into a separate initial-window fixture contract. The endpoint
must admit a fresh, stationary offline player into the declared initial
dimension, with server view distance at least two. Supply independently known
decoding bounds explicitly; the runner does not infer them from the target's
numeric content.

The [complete Play manifest](endpoint.play.example.json) enables all eight
scenarios with the measured vanilla 26.2 overworld bounds and a 30-second
per-scenario deadline. Set its
endpoint to the implementation under test and use it only with the stated
fixture contract. A minimal Play-only manifest is:

```json
{
  "endpoint": "127.0.0.1:25565",
  "protocol": 776,
  "capabilities": ["play-chunks"],
  "timeout_ms": 60000,
  "play": {"shape": {"sections": 24, "block_states": 32366, "biomes": 66}}
}
```

Use the actual initial dimension's section count and independently known registry
bounds for another fixture. The measured 775 overworld has 24 sections, 29,873
block states and 65 biomes. Sections must be 1..256, block states 1..16,777,216
and biomes 1..1,048,576; invalid or missing declared bounds fail before dialing.
The probe sends Configuration Client Information requesting view distance two
before admission, confirms the absolute initial teleport, checks its column
against the observed cache center, and requires all 49 unique columns of the
centered 7-by-7 window. It independently decodes complete chunk/light bodies,
including palette bounds and light mask ranges/disjointness, and checks batch
start/end framing and exact declared counts. The opening batch must include the
player's column; its size and later batch sizes are allowed to vary. A nonempty,
structurally valid attribute update for the admitted player must follow the
opening batch. The probe answers keepalives and chunk batches, sends Player
Loaded, and passes only after the complete window's final batch has finished.
Respawn, unloading, duplicate/outside chunks and moved cache centers violate this
stationary fixture. Other packets remain in the transcript without a claim that
their gameplay semantics were checked. This scenario does not assert a chunk
rate, withheld-acknowledgement policy, latency bound, block-content parity or
teleport responsiveness. Heightmap arrays and block-entity NBT are checked for
framing and structure; packed heightmap values, required heightmap presence,
block-entity type registries and block-entity gameplay meaning are outside this
scenario's assertions.

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
Transcript limits apply separately per scenario to bound memory; `truncated: true` marks bytes omitted and prevents a
partial capture being mistaken for a complete replay. For replay, concatenate
serverbound `wire_hex` within a scenario, respecting clientbound transitions
and compression negotiation. UUID/session values can vary on a fresh run.

This is a reusable **foundation and smoke profile**, not a claim of gameplay
parity. It validates registry/NBT structure without asserting the game semantics
of every registry value. It does not exercise online Microsoft authentication or
cover general gameplay, persistence and plugin behavior. Its optional Play check
establishes the stated initial-window contract only.
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
server or `MCGO_TARGET`. `TestPortableConformanceVanilla` and
`TestPortableConformanceVanilla775` run the same eight checks against independent
vanilla 776/775 endpoints with the existing explicit `MCGO_BLACKBOX=1` gate. The 775
jar defaults to `vanilla/server-26.1.jar`, overridable with
`MCGO_VANILLA_775_JAR`; absent prerequisites skip explicitly without downloads.
Set `MCGO_CONFORMANCE_REPORT=/path/report.json` to retain the 776 report and
`/path/report.775.json` for 775. To probe any 775 implementation directly:

```sh
go run ./cmd/mc-conformance -endpoint 127.0.0.1:25565 -protocol 775 -version 26.1 -capabilities status,ping,offline-login,configuration,malformed
```

`OpenPlay(ctx, Config)` opens a scripted offline session for either measured
profile. It validates login/configuration/Join Game, services teleport,
chunk-batch and keepalive replies, waits for initial player attributes, and
sends Player Loaded. The caller can inspect captured initial/configuration
packets and send/read further bodies over real TCP. A read timeout closes the
framed stream; it cannot resume safely after consuming a partial frame.
`DecodeChunk` independently checks complete 775/776 chunk/light bodies against
explicit dimension and registry bounds. These helpers support experiments and
blackbox assertions; they do not implement rendering or complete gameplay.
