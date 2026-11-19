# mapiter (idiomatic, hash map iteration)

**Task.** Iterate every entry of a filled hash map, many times, with each language's standard hash map. This row prices `Map` iteration; `hashmap` and `wordfreq` time inserts and lookups. `mapiter <n> <passes> <seed>`:

```
st = seed
m = new empty map u64 -> u64
for i in 0 .. n-1:
    st = st + G; m[mix(st)] = i                (mix is a bijection, so the n keys are distinct)
total = 0
for p in 0 .. passes-1:
    for (k, v) in m:                           (the library's own order)
        total = total + k * (v + 1)            (wrapping: order-free)
    total = total + len(m)
```

`G` and `mix` are splitmix64's (tests/bench/README.md, "Inputs"). The sum is commutative, so the checksum does not depend on the iteration order, which differs by library. The fill happens once and is inside the program's time.

**Cells**:

| Column | Map | Hash | DoS posture | Iteration |
|---|---|---|---|---|
| mk | `Map[u64, u64].New` in `sys.arena` | the fixed fast hash | none: a public fixed key | `for (key, value) in m.Iter()` |
| mk-keyed | `Map[u64, u64].NewKeyed(sys.arena, HashKey.New(io))` | SipHash-1-3 under 16 random bytes per process | keyed (Rust's and Go's peer) | `for (key, value) in m.Iter()` |
| rust, rust-ovf | `std::collections::HashMap<u64, u64>` | SipHash-1-3 under `RandomState` | keyed | `for (k, v) in &m` |
| zig-fast, zig-safe | `std.AutoHashMap(u64, u64)` over `std.heap.smp_allocator` | wyhash-based `autoHash`, seed 0 | none: fixed | `iterator()`, `next()` |
| go | `map[uint64]uint64` | runtime AES hash, seeded per map | seeded | `for k, v := range m` (randomised start) |
| c | none: no std hash map | | | |

Iteration does not hash; the one fill of 2^20 inserts does, so the hash and its DoS posture are in the time through the fill only. How the fill's share compares with the passes' is not measured.

Sizes: n = 2^20 in both sizes; quick 2 passes, publish 200 passes. The publish size is not yet checked against the 1 s floor: `tools/bench verify -publish-size mapiter` on a quiet machine sets it. Overflow class `wraps`.
