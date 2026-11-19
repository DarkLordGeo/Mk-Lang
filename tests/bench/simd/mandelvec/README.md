# mandelvec (SIMD)

**Kernel.** `same/mandelbrot`'s algorithm (`../../same/mandelbrot/README.md`: an n by n grid, at most 50 iterations a point, the escape test after each step, the total iteration count) computed L points at a time with each language's SIMD layer. The name differs from the scalar row so their programs never share a build path.

```
for each row y, for x in steps of L (then the scalar loop for the rest):
    cr[l] = (2.0 * (x + l)) / n - 1.5;  ci = (2.0 * y) / n - 1.0
    zr = zi = 0; cnt = 0; active = all lanes
    50 times:
        tr = (zr*zr - zi*zi) + cr; ti = (2.0*zr)*zi + ci; zr = tr; zi = ti
        active = active and not (zr*zr + zi*zi > 4.0)
        if no lane is active: stop
        cnt = cnt + 1 in every active lane
    total = total + the lanes of cnt
```

A lane that escaped keeps computing but is no longer counted, so each lane's count is the scalar loop's, and the checksums are `same/mandelbrot`'s (`5600`: 746474893, the quick `200`: 952474). No product is fused into a sum.

**Fixed targets, no dispatch:** `v2` (SSE2, L = 2 doubles) and `v3` (AVX, L = 4), every column built for that target. Compute-bound, so the row reports time, not GB/s, at n = 9000 (the scalar row runs 5600; at that size the fastest v3 cell ran 0.40 s in a verify run on a shared machine, 2026-11-07), with the quick size 200 shared with the scalar row (952474).

**Cells.**

| Column | Vector layer |
|---|---|
| mk | `std.simd`: `Vec[f64, L]`, `Mask[f64, L]`, lane-wise `* + -`, `>`, `& !`, `Mask.Any`, `Mask.Select`; L = 4 in `mk/` (v3) and L = 2 in `mkv2/` (v2, the variant's own source), fixed at compile time with no dispatch, as C's `__AVX2__` and Zig's `suggestVectorLength` are |
| c | `<immintrin.h>`: `__m128d` / `__m256d`, `_mm*_cmp*_pd`, `_mm*_andnot_pd`, `_mm*_movemask_pd` |
| rust, rust-ovf | `std::arch::x86_64`, the same intrinsics, `cfg(target_feature = "avx2")` |
| zig-fast, zig-safe | `@Vector(L, f64)`, L = `std.simd.suggestVectorLength(f64)`, `@select`, `@reduce(.Or)` |
| go | scalar, one point at a time (same/mandelbrot's program): Go has no SIMD layer |
