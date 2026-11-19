# sort (idiomatic, unstable)

**Task.** Sort 10 M `u64` with each language's standard unstable sort (sorting is three rows: this one, `sortstable` and `sortstr`). `sort <n> <rounds> <seed>`; one array of n keys, allocated once; per round, four patterns, each filled, sorted ascending and checked:

```
st = seed; total = 0
for r in 0 .. rounds-1:
    for p in 0 .. 3:
        for i in 0 .. n-1:
            p0 random:   st = st + G; xs[i] = mix(st)          (full 64-bit keys)
            p1 reversed: xs[i] = n - i
            p2 sawtooth: xs[i] = i mod 1000
            p3 16 keys:  st = st + G; xs[i] = mix(st) >> 60
        std sort of xs, ascending
        total = total + check(xs)                              (wrapping)
check(xs) = 0xdead if some xs[i-1] > xs[i], else the wrapping sum of xs[i] * (i mod 1000 + 1)
```

The patterns are `same/sortints`' (random keys here use all 64 bits), so random, reversed, sawtooth and few-unique inputs each reach a different path of every sort. The fill is inside the program's time, the same O(n) loop in every column.

**Cells.**

| Column | Sort | Comparison |
|---|---|---|
| mk | `Slice[u64].Sort(Less)` (pdqsort) | `Less` is a const fn argument: a direct call, inlinable |
| c | `qsort` | an indirect comparator through a function pointer, per comparison (C's only std sort) |
| rust, rust-ovf | `sort_unstable` (ipnsort) | `u64: Ord`, inlined |
| zig-fast, zig-safe | `std.sort.pdq` | `std.sort.asc(u64)`, comptime |
| go | `slices.Sort` (pdqsort) | `cmp.Ordered`, no call |

Sizes: quick `100000 1 1`, publish `10000000 4 1` (the fastest cell, Rust, sorted one round in about 0.30 s in a verify run on a shared machine, 2026-11-07; four rounds clear the 1 s floor). Overflow class `wraps` (the checksum).
