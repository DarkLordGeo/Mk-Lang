# indexbyte (SIMD)

**Kernel.** Find the first occurrence of a byte in a buffer with each language's SIMD layer. `indexbyte <size> <reps> <seed>`:

```
buf[i] = (i * 131 + seed) & 0x7F          (u64 arithmetic, wrapping: 7-bit bytes, so 0xFF never occurs)
st = seed; total = 0
reps times:
    st = st + G
    pos = size - 1 - mix(st) mod min(size, 64)
    buf[pos] = 0xFF
    total = total + index_byte(buf, 0xFF)  (= pos)
    restore buf[pos]
print total
```

Every search reads all but at most the last 64 bytes, so GB/s is `size * reps` over the median kernel time: the fill (one arithmetic pass) and its page faults are timed by the variant's `baseline` (reps 0) and subtracted. The buffer starts on a 64-byte boundary in the c, rust, zig and go columns, each refusing to run otherwise; the mk column's cells read an arena slice whose alignment Mk code cannot set without `std.raw` (the labelled exception to aligned buffers, the mk row below). `index_byte` in the vector columns: four vectors of the target's width a step (64 bytes at v2, 128 at v3), their compares ORed and tested once; on a hit, or for the rest, one vector a step, the first set lane by count-trailing-zeros of the compare mask; then bytes.

**Fixed targets, no dispatch.** Each row runs twice, at `x86-64-v2` (SSE2, 16-byte vectors) and at `x86-64-v3` (AVX2, 32-byte vectors), with every column built for that one target: C `-march`, Rust `-C target-cpu`, Zig `-mcpu`, Go `GOAMD64` (row `cpu`, `tools/bench/columns.go`). Nothing picks a path at run time, except Go's `bytes.IndexByte`, which is assembly in Go's runtime with its own CPU check, and is labelled so.

**Sizes.** L1: 16 KiB, 8,000,000 searches at v2 and 20,000,000 at v3. L3: 32 MiB (the CPU has 96 MB of L3), 4,000 and 5,000. DRAM: 1 GiB, 36. The counts were set so the fastest cell of each variant ran at least about 1 s in a verify run on a shared machine (2026-11-07). Quick: 100,000 bytes, 10 searches.

**Cells.**

| Column | Vector layer |
|---|---|
| mk | The width-fixed cells: `mkfix/` (v3, `Vec[u8, 32]`, one ymm) and `mkfixv2/` (v2, `Vec[u8, 16]`, one xmm), the loop of `indexbyte.c` (four vectors OR-ed and tested with `Mask.Any`, then one vector, its first match by `Mask.FirstSet`, then bytes), the same text but `const L`, no dispatch. Every variant's quick and publish checksum was checked, untimed (2026-11-15). Their buffer is an arena slice at 16 mod 64 (the labelled exception to aligned buffers): at v3 half of the 32-byte loads cross a cache line, at v2 none. `mk/` calls `std.simd.IndexByte`, which picks its form at run time (`Cpu()`: in a v3 build its 64-lane avx512bw form from 256 bytes on this AVX-512 machine, wider than every other column), so it is no cell of this table |
| c | `<immintrin.h>`: `_mm_cmpeq_epi8` / `_mm256_cmpeq_epi8`, `_mm*_movemask_epi8`, `__builtin_ctz`, chosen by `__AVX2__` |
| rust, rust-ovf | `std::arch::x86_64` intrinsics in `unsafe`, each load's pointer taken from a checked slice (`&s[i..i + 128]`, then constant sub-slices), chosen by `cfg(target_feature = "avx2")`; portable `std::simd` is nightly-only |
| zig-fast, zig-safe | `@Vector(W, u8)`, W = `std.simd.suggestVectorLength(u8)`, `@reduce(.Or)`, `@ctz` of the compare bitcast to an integer |
| go | `bytes.IndexByte` (assembly-backed, SSE2 and AVX2): Go has no SIMD layer a program can write vector code in |
