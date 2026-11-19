# compare (SIMD table)

**Kernel.** Unsigned byte-slice ordering (-1, 0, 1) with each language's SIMD layer or std facility (`simd.Compare`; the SIMD table of tests/bench/README.md). `compare <size> <reps> <seed>`:

```
a[i] = b[i] = (i * G + seed) >> 56                 (u64 arithmetic, wrapping)
span = min(size, 64); st = seed; total = 0         (u64, wrapping)
reps times:
    st = st + G; m = mix(st)
    pos = size - 1 - m mod span; d = (m >> 32) & 1
    b[pos] = b[pos] ^ d                            (a difference in the last 64 bytes in about half the reps)
    total = total * 31 + (compare(a, b) + 1)       (1 when equal; 0 or 2 by the planted bit)
    b[pos] = b[pos] ^ d
print total
```

`G` is `0x9e3779b97f4a7c15` and `mix` splitmix64's finaliser, as in every row. Every compare reads both whole buffers (to the last 64 bytes), so GB/s counts `2 * size * reps`. Sizes and targets as tests/bench/simd/indexbyte: L1 16 KiB, L3 32 MiB, DRAM 1 GiB (each buffer), at x86-64-v2 and v3 with no run-time dispatch, except Go's `bytes.Compare` (assembly with its own CPU check, labelled) and Mk's `std.simd.Compare`, which dispatches by `simd.Cpu()` and so is compared in the v3 variants only. Quick: 100,000 bytes, 10 compares.

| Column | Vector layer |
|---|---|
| mk | `std.simd.Compare` |
| c | `<immintrin.h>`: four `_mm*_cmpeq_epi8` ANDed, one `_mm*_movemask_epi8` test a step, `__builtin_ctz` of the inverted mask (glibc memcmp's shape) |
| rust, rust-ovf | the same with `std::arch`, loads from checked slices, `cfg(target_feature = "avx2")` |
| zig-fast, zig-safe | `@Vector(W, u8)` compares, `@reduce(.And)`, `@ctz` |
| go | `bytes.Compare`, assembly-backed, labelled |
