# Native full-collision facts

Requires JDK 25 and the pinned unpacked 26.2 jar plus bundled libraries from the vanilla setup. Run from the repository root:

```sh
go run ./tools/collisionfull -out internal/server/testdata/collision-full26.2.hex
```

The probe calls `BlockState.isCollisionShapeFullBlock` at `BlockPos.ZERO` with `EmptyBlockGetter.INSTANCE` for every registered state. The fixture records the input/probe checksums, coverage and a state-ID bitset. It tests the independent Go geometry predicate; it is not a runtime table. The probe also exercises synthetic epsilon-sized boxes/gaps. No world or listening server is started.
