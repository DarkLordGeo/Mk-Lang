# sortstr (idiomatic, strings)

**Task.** Sort strings with each language's standard unstable sort, as views into one buffer, compared bytewise. `sortstr <n> <rounds> <seed>`:

```
st = seed
for i in 0 .. n-1:                      the keys, one buffer
    st = st + G; len = 1 + mix(st) mod 16
    len times: st = st + G; append byte 'a' + mix(st) mod 26
    key[i] = a view of those bytes (no copy)
total = 0
for r in 0 .. rounds-1:
    work = the n views in their generated order
    std sort of work, bytewise ascending
    total = total + check(work)                          (wrapping)
check(xs) = 0xdead if some xs[i-1] > xs[i], else the wrapping sum of fnv1a64(xs[i]) * (i mod 1000 + 1)
```

Equal keys hash equally, so the checksum does not depend on how a sort orders ties.

**Cells.**

| Column | Keys | Sort |
|---|---|---|
| mk | `str` views of one `str` (checked once by `str.From`); each round pushes them into a `List[str]` in a sub-arena | `Slice[str].Sort(StrLess)`, `str`'s `<` |
| c | `{pointer, length}` structs, `memcpy` per round | `qsort` with `memcmp` then length, an indirect comparator |
| rust, rust-ovf | `&str` into one `String`'s bytes, `copy_from_slice` per round | `sort_unstable` |
| zig-fast, zig-safe | `[]const u8` slices, `@memcpy` per round | `std.sort.pdq` with `std.mem.lessThan(u8, ...)` |
| go | substrings of one `string`, `copy` per round | `slices.Sort` |

Sizes: quick `50000 1 1`, publish `2000000 3 1` (the fastest cell ran two rounds in about 0.97 s in a verify run on a shared machine, 2026-11-07). Overflow class `wraps`.
