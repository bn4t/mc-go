# Repository consolidation

Consolidated on 2026-10-25 at https://github.com/bn4t/mc-go.

## Sources

| Original repository | Imported master commit | Location |
| --- | --- | --- |
| bn4t/mc-go-parity | `16f385112c3392c84d7dfbf87ecb7c3e147f2666` | Repository root, active server |
| bn4t/mc-go | `def0b32d8643a149fa3d42385e8ec15d8051745f` | `legacy/mc-go/` |
| bn4t/mc-proto-go | `6e3e080c83bf95a60e6096bf4ab12f0019f27860` | `legacy/mc-proto-go/` |

All three source histories are parents of the consolidation merge; commits
retain their original IDs and authorship. The legacy directories are exact
source snapshots, including their independent Go modules. No legacy packages
are imported into the active server and no server behavior is changed.
The active source files match the parity master commit; only repository
documentation and the legacy directories are added or updated.

## Preserved refs

Each source master is retained as `archive/<original-repository>/master`.
The four non-default parity branches are also preserved under
`archive/mc-go-parity/devin/`:

- `1789861613-anvil-parity`
- `1789869071-protocol-fixes`
- `1789895060-gameplay-advancements`
- `1789898216-adv-persistence`

These are historical branches, not additional features merged into the active
server. There were no source tags, releases, or open issues/pull requests to
migrate at inspection time. The source repositories remain available as
archived references pointing to the consolidated repository.

## Validation and known limitations

- Verified every imported file's Git blob and mode against its source tree.
- Active server: `go build ./...` passes with Go 1.27.3.
- Active server: `go vet ./internal/... ./cmd/... ./plugins/... ./tools/...` passes.
- Original server: `cd legacy/mc-go && go test ./...` passes.
- Protocol prototype: `go test ./...` fails because the original snapshot
  references missing `auth`, `codec`, and `dns` packages. Other packages compile.
- Parity's untagged `go test ./...` already fails to compile the blackbox
  package: some tests reference helpers only included with the `blackbox` tag.
  The README now uses the core-test command and removes the nonexistent `pkg/` path.
- Full vanilla differential testing was not run for this repository-only migration.
- Gitleaks 8.30.1 scanned all source refs before publication. Neither private
  source produced findings. The public parity source produced one finding,
  manually verified as the fixed AES test vector `cfb8KeyHex` in
  `internal/protocol/cipher_test.go`, not a service credential.
