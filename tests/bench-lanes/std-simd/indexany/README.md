# indexany (SIMD table)

**Kernel.** Find the first byte of a set with each language's SIMD layer or std facility (`simd.IndexAny`; the SIMD table of tests/bench/README.md). The set has 8 bytes, so std.simd takes its Truffle path (sets of 2 or 3 bytes take memchr3's shape, covered by the differential tests and not timed here). `indexany <size> <reps> <seed>`:

```
SET = tab, LF, CR, space, '"', ',', ':', '='       (a tokenizer's set; all below 0x40)
buf[i] = 0x40 | ((i * 131 + seed) & 0x3F)          (u64 arithmetic, wrapping: no byte of the set)
span = min(size, 64); st = seed; total = 0
reps times:
    st = st + G; m = mix(st)
    pos = size - 1 - m mod span
    old = buf[pos]; buf[pos] = SET[(m >> 32) & 7]
    total = total + indexany(buf, SET)             (-1 if none)
    buf[pos] = old
print total
```

`G` is `0x9e3779b97f4a7c15` and `mix` splitmix64's finaliser, as in every row. Sizes and targets as tests/bench/simd/indexbyte: L1 16 KiB, L3 32 MiB, DRAM 1 GiB, at x86-64-v2 and v3 with no run-time dispatch. Mk's `std.simd.IndexAny` dispatches by `simd.Cpu()` and so is compared in the v3 variants only. The haystack is 7-bit and the set ASCII, so Go's rune-based `bytes.IndexAny` gives the byte kernel's answer. Quick: 100,000 bytes, 10 searches.

| Column | Vector layer |
|---|---|
| mk | `std.simd.IndexAny` (Truffle) |
| c | `<immintrin.h>`: Hyperscan's Truffle, three `_mm*_shuffle_epi8` a vector (low table, high table on `v ^ 0x80`, the bit by high nibble), one vector a step |
| rust, rust-ovf | the same with `std::arch`, loads from checked slices, `cfg(target_feature = "avx2")` |
| zig-fast, zig-safe | `@Vector(W, u8)`: no run-time byte shuffle in `@Vector`, so one compare per set byte, ORed (labelled) |
| go | `bytes.IndexAny`: a 256-bit ASCII set scanned byte by byte, no assembly (labelled) |
