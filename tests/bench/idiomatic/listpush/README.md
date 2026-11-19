# listpush (idiomatic, growable array)

**Task.** Grow a list of `u64` from empty by single pushes and one bulk extend, with each language's standard growable array over its general heap allocator. This row prices `HeapList` growth. `listpush <n> <rounds> <seed>`:

```
ext[i] = 3*i + 1                         for i in 0 .. n/4 - 1   (made once, before the rounds)
st = seed; total = 0
for r in 0 .. rounds-1:
    l = new empty list (no reserved capacity)
    for i in 0 .. n-1:
        st = st + G; push mix(st) onto l
    extend l by ext                        (one call, n/4 elements)
    for x in l: total = total + x          (wrapping)
    total = total + len(l)
    free l
```

`G` and `mix` are splitmix64's (tests/bench/README.md, "Inputs"). The list starts with no capacity in every column, so each library's growth policy and its allocator's reallocation are in the time, as the task means them to be.

**Cells.** Allocators are named, as the hash rows name their hashes:

| Column | List | Allocator |
|---|---|---|
| mk | `HeapList[u64]`: `Push`, `Extend`, `for x in l`, `Free` | `sys.heap.Alloc()`, std's general heap |
| rust, rust-ovf | `Vec<u64>`: `push`, `extend_from_slice` | the global allocator, `System` (HeapAlloc on Windows) |
| zig-fast, zig-safe | `std.ArrayList(u64)`: `append`, `appendSlice`, `deinit` | `std.heap.smp_allocator` |
| go | a slice: `append` of one value, `append(l, ext...)` | Go's runtime allocator, garbage collected (GOGC=100, labelled) |
| c | none: no std growable array (a `realloc` loop by hand would be a stand-in) | |

Sizes: quick `1000000 2 1`, publish `10000000 20 1` (12.5 M elements, 100 MB, at the end of each round). The publish size is not yet checked against the 1 s floor: `tools/bench verify -publish-size listpush` sets it. Overflow class `wraps` (the checksum and the generator).
