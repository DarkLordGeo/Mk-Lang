# count (SIMD table)

**Kernel.** Count the occurrences of a byte with each language's SIMD layer or std facility (`simd.Count`; the SIMD table of tests/bench/README.md). `count <size> <reps> <seed>`:

```
buf[i] = (i * 0x9e3779b97f4a7c15 + seed) >> 60     (u64 arithmetic, wrapping: 4-bit values, each about 1/16 of the bytes)
st = seed; total = 0                               (u64, wrapping)
reps times:
    st = st + G
    total = total * 31 + count(buf, mix(st) & 15)
print total
```

`G` is `0x9e3779b97f4a7c15` and `mix` splitmix64's finaliser, as in every row. Sizes and targets as tests/bench/simd/indexbyte: L1 16 KiB, L3 32 MiB, DRAM 1 GiB, at x86-64-v2 and v3 with no run-time dispatch, except Go's `bytes.Count` (assembly with its own CPU check, labelled) and Mk's `std.simd.Count`, which dispatches by `simd.Cpu()` and so is compared in the v3 variants only. Quick: 100,000 bytes, 10 counts.

| Column | Vector layer |
|---|---|
| mk | `std.simd.Count` |
| c | `<immintrin.h>`: `_mm*_cmpeq_epi8` subtracted from four counters, `_mm*_sad_epu8` every 255 steps, `_mm_popcnt_u32` |
| rust, rust-ovf | the same with `std::arch`, loads from checked slices, `count_ones` of the movemask for single vectors, `cfg(target_feature = "avx2")` |
| zig-fast, zig-safe | `@Vector(W, u8)`: compares selected to 1 and added into four `u8` counters, widened and `@reduce(.Add)`-ed every 255 steps, `@popCount` for single vectors |
| go | `bytes.Count`, assembly-backed, labelled |
