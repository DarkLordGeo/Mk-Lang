# std-coll2 benchmark rows (idiomatic standard library)

Rows of std.coll for the idiomatic table: each language uses its standard library's own container in its idiomatic form, and a language with none says so in `skip`. Inputs are splitmix64 (Steele, Lea and Flood 2014) from the seed in argv, as in `tests/bench/README.md`; every program prints one checksum line folded with wrapping arithmetic. Only `tools/bench` publishes a number from these rows.

| Row | Work | Mk | Baselines |
|---|---|---|---|
| `btree` | `n` keys `k = mix(s) >> 1` inserted with value `3k`; `n` lookups, key `i` from the same stream when `i` is even and from `mix(q + 7) >> 1` when odd; `sum += v` per hit; one ascending iteration `sum = 31 sum + k`; every inserted key removed, `sum += v`; prints `sum len` | `coll.BTree[u64, u64]` | Rust `BTreeMap` |
| `btreewide` | the `btree` work with a `[32]u64` value (256 bytes): value `v[0] = 3k`, `v[31] = k`; a hit adds `v[0] + v[31]`, a remove adds `v[0]`; prints `sum len`. It measures the node width when the slot is large (COLL2-5) | `coll.BTree[u64, [32]u64]` | Rust `BTreeMap<u64, [u64; 32]>` |
| `pq` | `n` values `mix(s)` pushed, then popped least first, `sum = 31 sum + x`; prints `sum` | `coll.PriorityQueue[u64]` | Rust `BinaryHeap<Reverse<u64>>`, Zig `std.PriorityQueue`, Go `container/heap` |
| `deque` | `n` steps of `x = mix(s)`: step `i` with `i & 15 == 15` pushes front and, past depth 1,000, pops back; any other step pushes back and pops front; `sum = 31 sum + popped`; prints `sum len` | `coll.Deque[u64]` | Rust `VecDeque`, Zig `std.Deque` |
| `bitset` | `n` bits, `rounds` rounds: set `i = r % 3, +3, ...`, remove (`Remove`) `i = r % 7, +7, ...`, `total += Count()`; then `sum += i` for every set bit; prints `total sum` | `coll.Bitset` | Zig `std.DynamicBitSet` |

`mix` is splitmix64's output function and the stream steps `s += 0x9E3779B97F4A7C15` before each use, starting from the seed.
