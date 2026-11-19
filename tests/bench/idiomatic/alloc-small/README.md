# alloc-small (idiomatic, small-object allocation)

**Task.** Churn small heap objects through each language's standard general-purpose allocator: a ring of 4,096 live objects of 8 to 256 bytes, each step replacing the oldest. This row prices the general allocator. `alloc-small <steps> <seed>`:

```
src = 4,096 bytes: gs = seed xor 0x5555555555555555; src[i] = 'a' + mix(gs += G) mod 26
make(): st = st + G; v = mix(st); l = v mod 249 + 8; off = (v >> 16) mod 3840
        allocate l bytes and copy src[off .. off+l] into them        (one allocation, then the fill)
read(o) = len(o) + o[0] + o[len-1]
ring[i] = make()                       for i in 0 .. 4095
total = 0
for j in 0 .. steps-1:
    slot = j mod 4096
    total = total + read(ring[slot])   (wrapping)
    o = make(); free ring[slot]; ring[slot] = o
for i in 0 .. 4095: total = total + read(ring[i]); free ring[i]
```

`G` and `mix` are splitmix64's (tests/bench/README.md, "Inputs"). The new object is allocated before the old one is freed, in every column, so the allocator never sees an immediate free-then-allocate of one size. The sizes are spread over 249 values, so the allocator's size classes, free lists and reuse are what is priced.

**Cells.** Each allocator is named (as the hash rows name their hashes):

| Column | Object | Allocator |
|---|---|---|
| mk | `HeapList[u8]` from empty, one `Extend` of the bytes; `Replace` into the ring, `Free` | `sys.heap.Alloc()`, std's general heap |
| c | `malloc(l)` and `memcpy`; `free` | the static UCRT's `malloc` (`-fms-runtime-lib=static`) |
| rust, rust-ovf | `Box<[u8]>` from `Box::from(&src[..])`; `std::mem::replace`, drop | the global allocator, `System` (HeapAlloc on Windows) |
| zig-fast, zig-safe | `gpa.dupe(u8, ...)`; `gpa.free` | `std.heap.smp_allocator` |
| go | `bytes.Clone`; the slot is overwritten | Go's runtime allocator, garbage collected (GOGC=100, labelled: it frees in batches, not per step) |

Sizes: quick `2000000 1`, publish `30000000 1`. The publish size is not yet checked against the 1 s floor: `tools/bench verify -publish-size alloc-small` sets it. Overflow class `wraps`.
