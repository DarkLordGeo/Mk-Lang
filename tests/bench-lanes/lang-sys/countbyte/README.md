# countbyte (SIMD table)

Task: doc/reference.md's `CountByte` block, the count of the bytes of `xs` equal to `b` in 16-lane steps with a scalar tail, against C's SSE2 version of the same loop. `countbyte <n> <reps> <seed>`. The row shows that the std.simd lowering is vector code; the std-simd rows compare every language's SIMD layer.

Input, built in each program: `st = seed`; for `i` in `0..n`, `st += 0x9e3779b97f4a7c15` (wrapping) and `xs[i]` is the low byte of `mix(st)` (splitmix64's finalizer, `intsum`'s).

Passes: for `r` in `0..reps`, `c = CountByte(xs, r % 256)`, and the sum (wrapping, `u64`) gains `c * (r % 1000 + 1)`. The program prints the sum.

Columns: `mk`, the reference block unchanged (`simd.Vec[u8, 16].Splat`, `FromSlice`, `==`, `Mask.CountSet`), `--release`; `mk-wrap` (`mkwrap/`), the same with the count's `+=` written as `n = n +% ...`; `c`, `_mm_set1_epi8`, `_mm_loadu_si128`, `_mm_cmpeq_epi8`, `_mm_movemask_epi8` and the vector layer's `_mm_popcnt_u32` per 16 bytes (this table allows no `__builtin_` but `__builtin_ctz`), then the same tail, `-O3`. Every column at the variant's `cpu`, single-threaded.

Variants: `l1v2` (16 KiB, 5,000,000 passes), `l3v2` (32 MiB, 2,500), `dramv2` (1 GiB, 24), all at `x86-64-v2`; each has a `baseline` of 0 passes, so the fill and its page faults are subtracted, and GB/s counts `n * reps` bytes. Only v2: the block is fixed at 16 lanes, so at v3 every column would run the same 16-byte loop (C's intrinsics are SSE2); the std-simd rows carry the v3 widths. The pass counts were set so the fastest cell ran about a second in one `go run ./tools/bench verify -publish-size` on a shared machine (AMD Ryzen 7 9800X3D, Windows 11, 2026-11-07), not on a quiet machine; they are to be adjusted if a cell runs under about a second. Quick: 64 KiB, 100 passes.

Aligned buffers: the `c` column over-allocates by 64 bytes, starts its buffer on the 64-byte boundary, and exits 3 if it is not on one. The `mk` and `mk-wrap` columns read `sys.arena.Slice[u8](n)`, whose alignment Mk code cannot set or read without `std.raw`, which this table bans (an unsafe module): the labelled exception of `tests/bench/README.md`, as `validutf8`'s mk column, until std's aligned allocation reaches `Arena`.

Mk checks what the reference says: `FromSlice` checks `0 <= i <= xs.Len() - 16` once per load (LLVM removes it under the loop condition), and the `mk` column's `n +=` panics on overflow (one branch per 16 bytes); `mk-wrap` drops that check and keeps the span check. C checks nothing. So `mk / c` includes the overflow check's price and `mk-wrap / c` does not.
