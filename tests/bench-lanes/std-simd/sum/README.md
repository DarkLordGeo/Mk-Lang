# sum (SIMD table)

**Kernel.** The `f32` sum of a vector with each language's SIMD layer (`simd.Sum`; the SIMD table of tests/bench/README.md). The accumulators and the reduction order are `std.simd`'s stated order, the same as tests/bench/simd/dot's without the product, so every column prints the same bits. `sum <n> <reps> <seed>`:

```
x[i] = f32((i * 0x9e3779b97f4a7c15 + seed) >> 48) * 2^-16 - 0.5      (u64 arithmetic, wrapping; exact in f32)
total = 0                                                            (u64, wrapping)
for r in 0 .. reps-1:
    x[r mod n] = x[r mod n] + 1.0                                    (so no sum can be hoisted)
    total = total + bits(sum(x))
sum(x):
    s[0..16] = 0
    for i = 0, 16, 32, ... while i + 16 <= n:
        for j in 0..16: s[j] = s[j] + x[i+j]
    for w in 8, 4, 2, 1: for j in 0..w: s[j] = s[j] + s[j+w]
    r = s[0]; for the tail i: r = r + x[i]
    return r
```

**Fixed targets, no dispatch; sizes** as tests/bench/simd/dot: v2 and v3; L1 n = 4,096 (16 KiB) with 6,000,000 sums at v2 and 10,000,000 at v3, L3 n = 8,388,608 (32 MiB) with 3,000 and 5,000, DRAM n = 268,435,456 (1 GiB) with 40. Mk's `std.simd.Sum` dispatches by `simd.Cpu()` and so is compared in the v3 variants only. GB/s counts `4 * n * reps` bytes over the median kernel time, the fill timed by the variant's `baseline` (reps 0) and subtracted. Quick: n = 10,007 (a 7-element tail), 20 sums.

| Column | Vector layer |
|---|---|
| mk | `std.simd.Sum` |
| c | `<immintrin.h>`: `_mm_add_ps` at v2 (four accumulators), `_mm256_add_ps` at v3 (two) |
| rust, rust-ovf | `std::arch::x86_64` intrinsics in `unsafe`, every load and store through a checked slice, `cfg(target_feature = "avx2")` |
| zig-fast, zig-safe | `@Vector(W, f32)`, W = `std.simd.suggestVectorLength(f32)` |
| go | scalar, the same 16 sums in a `[16]float32`, labelled: Go has no SIMD layer and no std float sum |
