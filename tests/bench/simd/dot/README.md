# dot (SIMD)

**Kernel.** The `f32` dot product of two vectors with each language's SIMD layer. The accumulators and the reduction order are stated so every language produces the same bits; they are `std.simd`'s `Dot` (doc/reference.md, `std.simd`: 16 partial sums, the `j`th taking elements `j`, `j + 16`, ..., added as a pairwise tree, then the tail in order). `dot <n> <reps> <seed>`:

```
x[i] = f32((i * 0x9e3779b97f4a7c15 + seed) >> 48) * 2^-16 - 0.5      (u64 arithmetic, wrapping; exact in f32)
y[i] = f32((i * 0xbf58476d1ce4e5b9 + seed) >> 48) * 2^-16 - 0.5
total = 0                                                            (u64, wrapping)
for r in 0 .. reps-1:
    x[r mod n] = x[r mod n] + 1.0                                    (so no product can be hoisted)
    total = total + bits(dot(x, y))
dot(x, y):
    s[0..16] = 0
    for i = 0, 16, 32, ... while i + 16 <= n:
        for j in 0..16: s[j] = s[j] + (x[i+j] * y[i+j])              (a product rounded to f32, then a sum: never fused)
    for w in 8, 4, 2, 1: for j in 0..w: s[j] = s[j] + s[j+w]
    r = s[0]; for the tail i: r = r + (x[i] * y[i])
    return r
```

The vector columns hold `s` in 16 / W vectors of W lanes (4 SSE vectors at v2, 2 AVX vectors at v3), which is the same arithmetic in the same order, so every column prints the same bits. No column fuses a product into a sum: C `-ffp-contract=off` and separate `_mm*_mul_ps` / `_mm*_add_ps`, Rust and Zig never fuse, Go writes `float32(x*y)`.

**Fixed targets, no dispatch; sizes** as in `../indexbyte/README.md`: v2 and v3; L1 n = 2,048 (16 KiB for x and y) with 12,000,000 products at v2 and 20,000,000 at v3, L3 n = 4,194,304 (32 MiB) with 5,000 and 6,000, DRAM n = 134,217,728 (1 GiB) with 40 (set so the fastest cell of each variant ran at least about 1 s in a verify run on a shared machine, 2026-11-07). GB/s counts `8 * n * reps` bytes over the median kernel time: the fill and its page faults are timed by the variant's `baseline` (reps 0) and subtracted. Both buffers start on a 64-byte boundary in the c, rust, zig and go columns, each refusing to run otherwise; the mk column's cell reads arena slices whose alignment Mk code cannot set without `std.raw` (the labelled exception to aligned buffers, the mk row below). Quick: n = 10,007 (a 7-element tail), 20 products.

**Cells.**

| Column | Vector layer |
|---|---|
| mk | The width-fixed cell: `mkfix/`, Dot's baseline body without the `Cpu()` test (the same 16 partial sums in one `Vec[f32, 16]` and tree), one text for all six variants, which LLVM keeps in four xmm registers at v2 and two ymm registers at v3, the same bits. Every variant's quick and publish checksum was checked, untimed (2026-11-15). `mk/` calls `std.simd.Dot`, which picks its form at run time (`Cpu()`; its avx2 form aligns its loads of x, `std/simd/float.mk`), so it is no cell of this table. mkfix's buffers are arena slices at 16 mod 64 (the labelled exception to aligned buffers, bench.json notes) and its loads do not align themselves: at v3 half of its 32-byte loads cross a cache line, at v2 none (std.simd.Dot's avx2 form aligns its loads, so the cost is mkfix's, not Dot's) |
| c | `<immintrin.h>`: `_mm_*_ps` at v2, `_mm256_*_ps` at v3 (`__AVX2__`) |
| rust, rust-ovf | `std::arch::x86_64` intrinsics in `unsafe`, every load and store through a checked slice (`&x[i..i + 16]`, then constant sub-slices), `cfg(target_feature = "avx2")` |
| zig-fast, zig-safe | `@Vector(W, f32)`, W = `std.simd.suggestVectorLength(f32)` |
| go | scalar, the same 16 sums in a `[16]float32`: Go has no SIMD layer and no std dot product |
