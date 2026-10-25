# Earlier experiments

These are preserved source snapshots, not dependencies of the active server.
Run Go commands inside the relevant directory; each snapshot retains its
original `go.mod` and import paths. The root module remains standard-library only.

- [`mc-go/`](mc-go/): the original Java Edition 1.21.1 server skeleton,
  including its README and configuration examples.
- [`mc-proto-go/`](mc-proto-go/): the Java Edition 1.21.8 protocol prototype.
  This snapshot is incomplete: its root package references missing `auth`,
  `codec`, and `dns` packages. It is retained for reference, not advertised
  as a working dependency.

Continue server development at the repository root. See
[the migration record](../MIGRATION.md) for history and validation details.
