# validutf8 (SIMD table)

The row `tests/bench/simd/validutf8` (its README states the kernel, the sizes and the checksums) with an Mk column that calls std.simd: `mk/validutf8.mk`. The C, Rust, Zig and Go columns are that row's sources, named by relative path and unchanged. The Mk column is skipped in the fixed-v2 variants, because `std.simd` dispatches by `simd.Cpu()` and so runs its 32-lane avx2 form on an avx2 machine; it is compared in the v3 variants. Its buffers are arena slices (the labelled exception of tests/bench/README.md). Nothing here is published.
