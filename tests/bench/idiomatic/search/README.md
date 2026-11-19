# search (idiomatic, binary search)

**Task.** Look up random keys in a sorted `u64` slice with each language's standard binary search. This row prices `Slice.Search`. `search <n> <lookups> <seed>`; one slice of n keys, filled once, then the lookups:

```
xs[i] = 2*i + 1                         for i in 0 .. n-1   (sorted, distinct, odd)
st = seed; total = 0
for j in 0 .. lookups-1:
    st = st + G; k = mix(st) mod (2*n + 1)                  (about half the keys are present)
    i = std binary search of k in xs
    if i < n and xs[i] == k: total = total + i + 1          (wrapping)
    else:          total = total + 1
```

`G` and `mix` are splitmix64's (tests/bench/README.md, "Inputs"). Every key is distinct, so a present key has exactly one index and every library returns the same one; an absent key adds 1, so the checksum does not depend on where a library says the absent key would go. That lets C's `bsearch`, which finds exact matches only, compute the same sum.

**Cells.**

| Column | Search | Comparison |
|---|---|---|
| mk | `Slice[u64].Search(&k, Less)`: the first index whose element is not less than the key | `Less` a const fn argument: a direct call |
| c | `bsearch` (exact match only; labelled) | an indirect comparator through a function pointer, per probe |
| rust, rust-ovf | `<[u64]>::binary_search` | `u64: Ord`, inlined |
| zig-fast, zig-safe | `std.sort.lowerBound` | a comptime `std.math.order` comparator |
| go | `slices.BinarySearch` | `cmp.Ordered`, no call |

Every column's index result is compared with the key (`xs[i] == k`) by the program itself: Mk's `Search`, Zig's `lowerBound` and Go's `BinarySearch` return the lower bound (Go also says whether it found it), Rust's `binary_search` returns `Ok(i)` or `Err(i)`, and C's `bsearch` a pointer or NULL.

Sizes: n = 2^20 (8 MiB of keys, larger than L2) in both sizes; quick 10^6 lookups, publish 2*10^7 lookups. The publish size is not yet checked against the 1 s floor: `tools/bench verify -publish-size search` sets it. Overflow class `wraps` (the checksum and the key generator).
