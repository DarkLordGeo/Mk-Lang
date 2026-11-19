# hashmap (idiomatic)

**Task.** Insert, lookup and delete churn on each language's standard hash map with its default hash, `u64` keys and values. It is the task of the `maphash` row (`tests/bench-lanes/std-hash/maphash/README.md`, which has the step-by-step definition), written here with a keyed Mk column: `hashmap <n> <rounds> <seed>`; per round a new, empty map (it grows from empty, never presized), n inserts of `mix(st)`, n probes (a hit, a miss, and every other key removed), n rechecks; the checksum is the wrapping sum of the values found, plus 1 per miss found (never), 3 per key left, plus the length.

**Cells**:

| Column | Map | Hash | DoS posture |
|---|---|---|---|
| mk | `Map[u64, u64].New` in a sub-arena per round | the fast fixed hash | none: a public fixed key |
| mk-keyed | `Map[u64, u64].NewKeyed(&a, HashKey.New(io))` | SipHash-1-3 under 16 random bytes per process | keyed (Rust's and Go's peer) |
| rust, rust-ovf | `HashMap::new()` | SipHash-1-3 under `RandomState` | keyed |
| zig-fast, zig-safe | `std.AutoHashMap(u64, u64)`, an arena per round | wyhash-based `autoHash`, seed 0 | none: fixed |
| go | `map[uint64]uint64` | runtime AES hash, seeded per map | seeded |
| c | no std hash map in C | | |

Checksums: quick `100000 3 1` `15000450000`, publish `3000000 3 1` `13500013500000` (as the `maphash` row records them). Overflow class `wraps`.
